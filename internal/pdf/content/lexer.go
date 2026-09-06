package content

import (
	"bytes"
	"errors"
)

// MaxOperations bounds how many instructions a single content stream may
// yield. Mirrors the Rust extractor's guard against pathological streams.
const MaxOperations = 1_000_000

// ErrTooManyOperations is returned when a stream exceeds MaxOperations.
var ErrTooManyOperations = errors.New("content: operation limit exceeded")

func isWhitespace(b byte) bool {
	switch b {
	case 0x00, 0x09, 0x0A, 0x0C, 0x0D, 0x20:
		return true
	}
	return false
}

func isDelimiter(b byte) bool {
	switch b {
	case '(', ')', '<', '>', '[', ']', '{', '}', '/', '%':
		return true
	}
	return false
}

func isRegular(b byte) bool { return !isWhitespace(b) && !isDelimiter(b) }

type lexer struct {
	data []byte
	pos  int
}

// Decode tokenizes a content stream into its operations.
//
// Unlike lopdf's Content::decode, comments are skipped during lexing rather
// than by a pre-pass, so streams with `%` inside string literals need no
// sanitization. Malformed constructs are skipped rather than aborting the
// stream: partial extraction beats none on real-world PDFs.
func Decode(data []byte) ([]Operation, error) {
	l := &lexer{data: data}
	var ops []Operation
	var operands []Object

	for {
		l.skipSpace()
		if l.pos >= len(l.data) {
			break
		}

		if obj, ok := l.readObject(); ok {
			// Cap operand accumulation so a stream of pure numbers with no
			// operator cannot grow without bound.
			if len(operands) < 512 {
				operands = append(operands, obj)
			}
			continue
		}

		kw := l.readKeyword()
		if kw == "" {
			// Unconsumable byte (stray delimiter); skip it to stay in sync.
			l.pos++
			continue
		}

		switch kw {
		case "true":
			operands = append(operands, Bool(true))
			continue
		case "false":
			operands = append(operands, Bool(false))
			continue
		case "null":
			operands = append(operands, Null{})
			continue
		case "BI":
			// Inline image: parse its dict, then skip the binary payload.
			if img, ok := l.readInlineImage(); ok {
				ops = append(ops, img)
			}
			operands = operands[:0]
			continue
		}

		ops = append(ops, Operation{Operator: kw, Operands: operands})
		if len(ops) > MaxOperations {
			return ops, ErrTooManyOperations
		}
		operands = nil
	}

	return ops, nil
}

// skipSpace advances past whitespace and comments. A `%` outside a string runs
// to end-of-line; strings are consumed atomically by readObject, so a `%`
// reached here is always a real comment.
func (l *lexer) skipSpace() {
	for l.pos < len(l.data) {
		b := l.data[l.pos]
		switch {
		case isWhitespace(b):
			l.pos++
		case b == '%':
			for l.pos < len(l.data) && l.data[l.pos] != '\n' && l.data[l.pos] != '\r' {
				l.pos++
			}
		default:
			return
		}
	}
}

// readObject reads one operand. It returns false when the next token is a
// keyword (operator) rather than an operand.
func (l *lexer) readObject() (Object, bool) {
	if l.pos >= len(l.data) {
		return nil, false
	}

	switch b := l.data[l.pos]; {
	case b == '/':
		return l.readName(), true
	case b == '(':
		return l.readLiteralString(), true
	case b == '<':
		if l.pos+1 < len(l.data) && l.data[l.pos+1] == '<' {
			return l.readDict(), true
		}
		return l.readHexString(), true
	case b == '[':
		return l.readArray(), true
	case b == ']' || b == '>' || b == ')' || b == '}' || b == '{':
		return nil, false
	case b >= '0' && b <= '9', b == '+', b == '-', b == '.':
		return l.readNumber(), true
	}
	return nil, false
}

func (l *lexer) readKeyword() string {
	start := l.pos
	for l.pos < len(l.data) && isRegular(l.data[l.pos]) {
		l.pos++
	}
	return string(l.data[start:l.pos])
}

// readNumber parses integers and reals, tolerating the malformed forms real
// generators emit: leading `+`, repeated signs (`--5`), a bare `.5`, and a
// trailing `4.`.
func (l *lexer) readNumber() Object {
	start := l.pos
	for l.pos < len(l.data) && (l.data[l.pos] == '+' || l.data[l.pos] == '-') {
		l.pos++
	}

	intStart := l.pos
	for l.pos < len(l.data) && l.data[l.pos] >= '0' && l.data[l.pos] <= '9' {
		l.pos++
	}
	intPart := l.data[intStart:l.pos]

	var fracPart []byte
	isReal := false
	if l.pos < len(l.data) && l.data[l.pos] == '.' {
		isReal = true
		l.pos++
		fracStart := l.pos
		for l.pos < len(l.data) && l.data[l.pos] >= '0' && l.data[l.pos] <= '9' {
			l.pos++
		}
		fracPart = l.data[fracStart:l.pos]
	}

	// Consume any trailing garbage attached to the token (e.g. "12abc") so the
	// next read starts on a clean boundary.
	for l.pos < len(l.data) && isRegular(l.data[l.pos]) {
		l.pos++
		isReal = true // token was not a clean number; treat as real
	}

	neg := bytes.Count(l.data[start:intStart], []byte{'-'})%2 == 1

	var mag float64
	for _, c := range intPart {
		mag = mag*10 + float64(c-'0')
	}
	if len(fracPart) > 0 {
		scale := 1.0
		var frac float64
		for _, c := range fracPart {
			frac = frac*10 + float64(c-'0')
			scale *= 10
		}
		mag += frac / scale
	}
	if neg {
		mag = -mag
	}

	if isReal || len(fracPart) > 0 {
		return Real(mag)
	}
	return Integer(int64(mag))
}

// readName reads a `/Name`, resolving `#XX` hex escapes (PDF 32000-1 7.3.5).
func (l *lexer) readName() Name {
	l.pos++ // consume '/'
	var out []byte
	for l.pos < len(l.data) && isRegular(l.data[l.pos]) {
		b := l.data[l.pos]
		if b == '#' && l.pos+2 < len(l.data) {
			hi, ok1 := hexVal(l.data[l.pos+1])
			lo, ok2 := hexVal(l.data[l.pos+2])
			if ok1 && ok2 {
				out = append(out, hi<<4|lo)
				l.pos += 3
				continue
			}
		}
		out = append(out, b)
		l.pos++
	}
	return Name(out)
}

// readLiteralString reads `( ... )`, honouring balanced nesting, the standard
// escape set, octal escapes, and backslash line continuations.
func (l *lexer) readLiteralString() String {
	l.pos++ // consume '('
	var out []byte
	depth := 1

	for l.pos < len(l.data) {
		b := l.data[l.pos]

		if b == '\\' {
			l.pos++
			if l.pos >= len(l.data) {
				break
			}
			e := l.data[l.pos]
			switch e {
			case 'n':
				out = append(out, '\n')
			case 'r':
				out = append(out, '\r')
			case 't':
				out = append(out, '\t')
			case 'b':
				out = append(out, '\b')
			case 'f':
				out = append(out, '\f')
			case '(', ')', '\\':
				out = append(out, e)
			case '\r':
				// Line continuation: swallow the newline (and a following \n).
				if l.pos+1 < len(l.data) && l.data[l.pos+1] == '\n' {
					l.pos++
				}
			case '\n':
				// Line continuation: emit nothing.
			default:
				if e >= '0' && e <= '7' {
					v := int(e - '0')
					for k := 0; k < 2 && l.pos+1 < len(l.data); k++ {
						n := l.data[l.pos+1]
						if n < '0' || n > '7' {
							break
						}
						v = v*8 + int(n-'0')
						l.pos++
					}
					out = append(out, byte(v))
				} else {
					// Unknown escape: the backslash is dropped, char kept.
					out = append(out, e)
				}
			}
			l.pos++
			continue
		}

		if b == '(' {
			depth++
		} else if b == ')' {
			depth--
			if depth == 0 {
				l.pos++
				return out
			}
		}
		out = append(out, b)
		l.pos++
	}
	return out
}

// readHexString reads `< ... >`, ignoring interior whitespace. An odd number of
// digits is padded with a trailing zero per PDF 32000-1 7.3.4.3.
func (l *lexer) readHexString() String {
	l.pos++ // consume '<'
	var out []byte
	var cur byte
	half := false

	for l.pos < len(l.data) {
		b := l.data[l.pos]
		if b == '>' {
			l.pos++
			break
		}
		if v, ok := hexVal(b); ok {
			if half {
				out = append(out, cur<<4|v)
				half = false
			} else {
				cur = v
				half = true
			}
		}
		l.pos++
	}
	if half {
		out = append(out, cur<<4)
	}
	return out
}

func (l *lexer) readArray() Array {
	l.pos++ // consume '['
	arr := Array{}
	for l.pos < len(l.data) {
		l.skipSpace()
		if l.pos >= len(l.data) {
			break
		}
		if l.data[l.pos] == ']' {
			l.pos++
			break
		}
		obj, ok := l.readObject()
		if !ok {
			// Keywords are not legal inside an array; consume and ignore so a
			// malformed array cannot stall the lexer.
			if kw := l.readKeyword(); kw == "" {
				l.pos++
			}
			continue
		}
		arr = append(arr, obj)
	}
	return arr
}

func (l *lexer) readDict() Dict {
	l.pos += 2 // consume '<<'
	d := Dict{}
	for l.pos < len(l.data) {
		l.skipSpace()
		if l.pos >= len(l.data) {
			break
		}
		if l.data[l.pos] == '>' {
			l.pos++
			if l.pos < len(l.data) && l.data[l.pos] == '>' {
				l.pos++
			}
			break
		}
		if l.data[l.pos] != '/' {
			// Key must be a name; resync by dropping a token.
			if kw := l.readKeyword(); kw == "" {
				l.pos++
			}
			continue
		}
		key := l.readName()
		l.skipSpace()
		val, ok := l.readObject()
		if !ok {
			if kw := l.readKeyword(); kw != "" {
				switch kw {
				case "true":
					val = Bool(true)
				case "false":
					val = Bool(false)
				default:
					val = Null{}
				}
			} else {
				l.pos++
				continue
			}
		}
		d[key] = val
	}
	return d
}

// readInlineImage consumes `<dict pairs> ID <binary> EI` following a BI token
// and returns it as a synthetic operation carrying the image dict. The binary
// payload is discarded — the extractor only needs the image's presence and
// geometry, which come from the dict and the CTM.
func (l *lexer) readInlineImage() (Operation, bool) {
	d := Dict{}
	for l.pos < len(l.data) {
		l.skipSpace()
		if l.pos >= len(l.data) {
			return Operation{}, false
		}
		if l.data[l.pos] == '/' {
			key := l.readName()
			l.skipSpace()
			if val, ok := l.readObject(); ok {
				d[key] = val
			} else if kw := l.readKeyword(); kw != "" {
				switch kw {
				case "true":
					d[key] = Bool(true)
				case "false":
					d[key] = Bool(false)
				default:
					d[key] = Name(kw)
				}
			}
			continue
		}
		kw := l.readKeyword()
		if kw == "ID" {
			break
		}
		if kw == "EI" {
			return Operation{Operator: "INLINE_IMAGE", Operands: []Object{d}}, true
		}
		if kw == "" {
			l.pos++
		}
	}

	// Exactly one whitespace byte separates ID from the data.
	if l.pos < len(l.data) && isWhitespace(l.data[l.pos]) {
		l.pos++
	}

	// Scan for an `EI` that is delimited on both sides — the payload is raw
	// binary and may contain the bytes "EI" incidentally.
	for l.pos < len(l.data)-1 {
		if l.data[l.pos] == 'E' && l.data[l.pos+1] == 'I' {
			beforeOK := l.pos == 0 || isWhitespace(l.data[l.pos-1])
			after := l.pos + 2
			afterOK := after >= len(l.data) || isWhitespace(l.data[after]) || isDelimiter(l.data[after])
			if beforeOK && afterOK {
				l.pos += 2
				return Operation{Operator: "INLINE_IMAGE", Operands: []Object{d}}, true
			}
		}
		l.pos++
	}
	l.pos = len(l.data)
	return Operation{Operator: "INLINE_IMAGE", Operands: []Object{d}}, true
}

func hexVal(b byte) (byte, bool) {
	switch {
	case b >= '0' && b <= '9':
		return b - '0', true
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10, true
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10, true
	}
	return 0, false
}

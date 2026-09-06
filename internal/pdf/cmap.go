package pdf

import (
	"strings"

	"github.com/adrianliechti/go-extract/internal/pdf/content"
)

// codespace is one entry of a CMap's codespace range: an inclusive code
// interval together with the number of bytes codes in it occupy.
type codespace struct {
	low, high uint32
	nbytes    int
}

// cmap maps character codes to replacement text. It backs both ToUnicode
// CMaps (code to Unicode) and the encoding CMaps that select CIDs.
type cmap struct {
	spaces []codespace
	// text maps a character code to its replacement string.
	text map[uint32]string
	// identity marks Identity-H/V, where every 2-byte code maps to itself.
	identity bool
	// vertical marks a writing mode of 1.
	vertical bool
}

// codeLen returns how many bytes the code starting at b[0] occupies, using the
// codespace ranges. Falls back to the shortest declared width, then to 1 byte.
func (c *cmap) codeLen(b []byte) int {
	if c.identity {
		return 2
	}
	for n := 1; n <= 4 && n <= len(b); n++ {
		var v uint32
		for i := range n {
			v = v<<8 | uint32(b[i])
		}
		for _, s := range c.spaces {
			if s.nbytes == n && v >= s.low && v <= s.high {
				return n
			}
		}
	}
	if len(c.spaces) > 0 {
		shortest := 4
		for _, s := range c.spaces {
			if s.nbytes < shortest {
				shortest = s.nbytes
			}
		}
		return shortest
	}
	return 1
}

// lookup resolves a character code to its replacement text.
func (c *cmap) lookup(code uint32) (string, bool) {
	if s, ok := c.text[code]; ok {
		return s, true
	}
	return "", false
}

// decode splits raw string bytes into character codes using the codespace
// ranges, calling fn for each (code, byte-length) pair.
func (c *cmap) decode(raw []byte, fn func(code uint32, n int)) {
	for i := 0; i < len(raw); {
		n := c.codeLen(raw[i:])
		if i+n > len(raw) {
			n = len(raw) - i
		}
		var v uint32
		for j := range n {
			v = v<<8 | uint32(raw[i+j])
		}
		fn(v, n)
		i += n
	}
}

// identityCMap is the CMap for Identity-H and Identity-V: two-byte codes that
// are their own CIDs.
func identityCMap(vertical bool) *cmap {
	return &cmap{
		spaces:   []codespace{{low: 0, high: 0xFFFF, nbytes: 2}},
		text:     map[uint32]string{},
		identity: true,
		vertical: vertical,
	}
}

// parseCMap reads an embedded CMap stream. It understands the codespace,
// bfchar and bfrange constructs that ToUnicode CMaps are built from; cidchar
// and cidrange are accepted too so the same parser serves embedded encoding
// CMaps.
func parseCMap(data []byte) *cmap {
	ops, err := content.Decode(data)
	if err != nil && len(ops) == 0 {
		return nil
	}

	c := &cmap{text: map[uint32]string{}}

	// Operands accumulate across operations because the section body sits
	// between the begin and end operators, which the lexer reports as two
	// separate operations.
	var pending []content.Object

	for _, op := range ops {
		switch op.Operator {
		case "begincodespacerange", "beginbfchar", "beginbfrange",
			"begincidchar", "begincidrange":
			pending = nil

		case "endcodespacerange":
			args := append(pending, op.Operands...)
			for i := 0; i+1 < len(args); i += 2 {
				lo, okLo := args[i].(content.String)
				hi, okHi := args[i+1].(content.String)
				if !okLo || !okHi || len(lo) == 0 {
					continue
				}
				c.spaces = append(c.spaces, codespace{
					low:    beUint(lo),
					high:   beUint(hi),
					nbytes: len(lo),
				})
			}
			pending = nil

		case "endbfchar":
			args := append(pending, op.Operands...)
			for i := 0; i+1 < len(args); i += 2 {
				src, ok := args[i].(content.String)
				if !ok {
					continue
				}
				if dst, ok := args[i+1].(content.String); ok {
					c.text[beUint(src)] = utf16BEString(dst)
				}
			}
			pending = nil

		case "endbfrange":
			args := append(pending, op.Operands...)
			for i := 0; i+2 < len(args); i += 3 {
				lo, okLo := args[i].(content.String)
				hi, okHi := args[i+1].(content.String)
				if !okLo || !okHi {
					continue
				}
				c.addBFRange(beUint(lo), beUint(hi), args[i+2])
			}
			pending = nil

		case "endcidchar":
			args := append(pending, op.Operands...)
			for i := 0; i+1 < len(args); i += 2 {
				src, ok := args[i].(content.String)
				if !ok {
					continue
				}
				if cid, ok := content.Int(args[i+1]); ok {
					c.text[beUint(src)] = string(rune(cid))
				}
			}
			pending = nil

		case "endcidrange":
			args := append(pending, op.Operands...)
			for i := 0; i+2 < len(args); i += 3 {
				lo, okLo := args[i].(content.String)
				hi, okHi := args[i+1].(content.String)
				cid, okCID := content.Int(args[i+2])
				if !okLo || !okHi || !okCID {
					continue
				}
				start, end := beUint(lo), beUint(hi)
				for code := start; code <= end && code-start < cmapRangeLimit; code++ {
					c.text[code] = string(rune(cid + int64(code-start)))
				}
			}
			pending = nil

		case "endcmap":
			pending = nil

		default:
			// Section bodies arrive as operands on an unrecognised operator.
			pending = append(pending, op.Operands...)
		}
	}

	if len(c.text) == 0 && len(c.spaces) == 0 {
		return nil
	}
	return c
}

// cmapRangeLimit caps how many codes a single bfrange/cidrange may expand to,
// so a malformed range cannot exhaust memory.
const cmapRangeLimit = 0x10000

// addBFRange records one bfrange entry, which maps an inclusive code range
// either onto consecutive destinations from a base string, or onto the
// explicit destinations of an array.
func (c *cmap) addBFRange(lo, hi uint32, dst content.Object) {
	if hi < lo {
		return
	}
	switch d := dst.(type) {
	case content.Array:
		for i, o := range d {
			code := lo + uint32(i)
			if code < lo || code > hi || uint32(i) >= cmapRangeLimit {
				break
			}
			s, ok := o.(content.String)
			if !ok {
				continue
			}
			c.text[code] = utf16BEString(s)
		}

	case content.String:
		base := utf16BEUnits(d)
		if len(base) == 0 {
			return
		}
		for code := lo; code <= hi && code-lo < cmapRangeLimit; code++ {
			units := make([]uint16, len(base))
			copy(units, base)
			// Consecutive destinations increment the low-order code unit,
			// which is what the spec defines for a range's base string.
			units[len(units)-1] += uint16(code - lo)
			c.text[code] = string(utf16Runes(units))
		}
	}
}

// beUint reads up to four bytes as a big-endian unsigned integer.
func beUint(b []byte) uint32 {
	var v uint32
	for i, c := range b {
		if i == 4 {
			break
		}
		v = v<<8 | uint32(c)
	}
	return v
}

// utf16BEUnits reinterprets raw bytes as big-endian UTF-16 code units.
func utf16BEUnits(b []byte) []uint16 {
	units := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		units = append(units, uint16(b[i])<<8|uint16(b[i+1]))
	}
	return units
}

// utf16BEString decodes big-endian UTF-16 bytes into a string.
func utf16BEString(b []byte) string {
	// An odd single byte appears in malformed CMaps; treat it as Latin-1 so
	// the mapping is not lost entirely.
	if len(b) == 1 {
		return string(rune(b[0]))
	}
	return string(utf16Runes(utf16BEUnits(b)))
}

// utf16Runes decodes UTF-16 code units, combining surrogate pairs.
func utf16Runes(units []uint16) []rune {
	out := make([]rune, 0, len(units))
	for i := 0; i < len(units); i++ {
		u := units[i]
		if u >= 0xD800 && u <= 0xDBFF && i+1 < len(units) {
			if lo := units[i+1]; lo >= 0xDC00 && lo <= 0xDFFF {
				out = append(out, ((rune(u)-0xD800)<<10|(rune(lo)-0xDC00))+0x10000)
				i++
				continue
			}
		}
		out = append(out, rune(u))
	}
	return out
}

// isIdentityCMapName reports the predefined Identity CMaps, which need no
// stream because the mapping is the identity on two-byte codes.
func isIdentityCMapName(name string) bool {
	return name == "Identity-H" || name == "Identity-V"
}

// isVerticalCMapName reports a vertical writing mode from a CMap name.
func isVerticalCMapName(name string) bool {
	return strings.HasSuffix(name, "-V")
}

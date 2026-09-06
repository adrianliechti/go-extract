package font

import (
	"encoding/binary"
	"errors"
)

var errBadCFF = errors.New("font: malformed CFF")

// cff holds the parts of a CFF font that text extraction needs: the
// PostScript name, glyph count, italic angle, and per-glyph names.
type cff struct {
	name        string
	numGlyphs   uint16
	italicAngle float32
	isCID       bool

	charsetSIDs []uint16 // SID (or CID, when isCID) per glyph
	strings     [][]byte // custom String INDEX entries, SID 391+
}

// cffIndex is a parsed CFF INDEX structure.
type cffIndex struct {
	items [][]byte
	end   int // offset just past the INDEX
}

// readIndex parses a CFF INDEX at off. A count of zero yields an empty INDEX
// occupying just the two count bytes.
func readIndex(data []byte, off int) (*cffIndex, error) {
	if off < 0 || off+2 > len(data) {
		return nil, errBadCFF
	}
	count := int(binary.BigEndian.Uint16(data[off:]))
	if count == 0 {
		return &cffIndex{end: off + 2}, nil
	}
	if off+3 > len(data) {
		return nil, errBadCFF
	}
	offSize := int(data[off+2])
	if offSize < 1 || offSize > 4 {
		return nil, errBadCFF
	}

	offArray := off + 3
	dataStart := offArray + (count+1)*offSize - 1
	if dataStart < 0 || offArray+(count+1)*offSize > len(data) {
		return nil, errBadCFF
	}

	readOff := func(i int) int {
		p := offArray + i*offSize
		var v int
		for k := 0; k < offSize; k++ {
			v = v<<8 | int(data[p+k])
		}
		return v
	}

	idx := &cffIndex{items: make([][]byte, 0, count)}
	for i := 0; i < count; i++ {
		start, end := dataStart+readOff(i), dataStart+readOff(i+1)
		if start < 0 || end < start || end > len(data) {
			idx.items = append(idx.items, nil)
			continue
		}
		idx.items = append(idx.items, data[start:end])
	}
	idx.end = dataStart + readOff(count)
	if idx.end > len(data) || idx.end < 0 {
		return nil, errBadCFF
	}
	return idx, nil
}

// cffDict maps an operator to its operands. Two-byte operators (escape 12) are
// keyed as 1200+op2.
type cffDict map[int][]float64

// parseDict decodes a CFF DICT (spec §4).
func parseDict(b []byte) cffDict {
	d := cffDict{}
	var operands []float64

	for i := 0; i < len(b); {
		v := int(b[i])
		switch {
		case v <= 21: // operator
			op := v
			i++
			if v == 12 {
				if i >= len(b) {
					return d
				}
				op = 1200 + int(b[i])
				i++
			}
			d[op] = operands
			operands = nil

		case v == 28: // 3-byte integer
			if i+3 > len(b) {
				return d
			}
			operands = append(operands, float64(int16(binary.BigEndian.Uint16(b[i+1:]))))
			i += 3

		case v == 29: // 5-byte integer
			if i+5 > len(b) {
				return d
			}
			operands = append(operands, float64(int32(binary.BigEndian.Uint32(b[i+1:]))))
			i += 5

		case v == 30: // real number, nibble-encoded
			f, n := parseCFFReal(b[i+1:])
			operands = append(operands, f)
			i += 1 + n

		case v >= 32 && v <= 246:
			operands = append(operands, float64(v-139))
			i++

		case v >= 247 && v <= 250:
			if i+2 > len(b) {
				return d
			}
			operands = append(operands, float64((v-247)*256+int(b[i+1])+108))
			i += 2

		case v >= 251 && v <= 254:
			if i+2 > len(b) {
				return d
			}
			operands = append(operands, float64(-(v-251)*256-int(b[i+1])-108))
			i += 2

		default:
			i++
		}

		if len(operands) > 48 {
			operands = operands[:48]
		}
	}
	return d
}

// parseCFFReal decodes the nibble-packed real format, returning the value and
// the number of bytes consumed.
func parseCFFReal(b []byte) (float64, int) {
	var buf []byte
	for i := 0; i < len(b); i++ {
		for _, nib := range [2]byte{b[i] >> 4, b[i] & 0x0F} {
			switch {
			case nib <= 9:
				buf = append(buf, '0'+nib)
			case nib == 0x0a:
				buf = append(buf, '.')
			case nib == 0x0b:
				buf = append(buf, 'E')
			case nib == 0x0c:
				buf = append(buf, 'E', '-')
			case nib == 0x0e:
				buf = append(buf, '-')
			case nib == 0x0f:
				return parseFloatBytes(buf), i + 1
			}
		}
		if len(buf) > 64 {
			return 0, i + 1
		}
	}
	return parseFloatBytes(buf), len(b)
}

// parseFloatBytes is a small decimal parser; CFF reals are short and this
// avoids a strconv allocation path for malformed input.
func parseFloatBytes(b []byte) float64 {
	s := string(b)
	if s == "" {
		return 0
	}
	var mantissa float64
	var neg bool
	i := 0
	if s[i] == '-' {
		neg = true
		i++
	}
	for ; i < len(s) && s[i] >= '0' && s[i] <= '9'; i++ {
		mantissa = mantissa*10 + float64(s[i]-'0')
	}
	if i < len(s) && s[i] == '.' {
		i++
		scale := 1.0
		for ; i < len(s) && s[i] >= '0' && s[i] <= '9'; i++ {
			mantissa = mantissa*10 + float64(s[i]-'0')
			scale *= 10
		}
		mantissa /= scale
	}
	if i < len(s) && (s[i] == 'E' || s[i] == 'e') {
		i++
		expNeg := false
		if i < len(s) && s[i] == '-' {
			expNeg = true
			i++
		}
		exp := 0
		for ; i < len(s) && s[i] >= '0' && s[i] <= '9'; i++ {
			exp = exp*10 + int(s[i]-'0')
		}
		for k := 0; k < exp && k < 300; k++ {
			if expNeg {
				mantissa /= 10
			} else {
				mantissa *= 10
			}
		}
	}
	if neg {
		return -mantissa
	}
	return mantissa
}

// CFF Top DICT operators used here.
const (
	opCharset     = 15
	opCharStrings = 17
	opItalicAngle = 1207
	opROS         = 1230
)

func parseCFF(data []byte) (*cff, error) {
	if len(data) < 4 || data[0] != 1 {
		return nil, errBadCFF
	}
	hdrSize := int(data[2])
	if hdrSize < 4 || hdrSize > len(data) {
		return nil, errBadCFF
	}

	nameIdx, err := readIndex(data, hdrSize)
	if err != nil {
		return nil, err
	}
	topIdx, err := readIndex(data, nameIdx.end)
	if err != nil {
		return nil, err
	}
	stringIdx, err := readIndex(data, topIdx.end)
	if err != nil {
		return nil, err
	}
	if len(topIdx.items) == 0 {
		return nil, errBadCFF
	}

	c := &cff{strings: stringIdx.items}
	if len(nameIdx.items) > 0 {
		c.name = string(nameIdx.items[0])
	}

	top := parseDict(topIdx.items[0])
	if v, ok := top[opItalicAngle]; ok && len(v) > 0 {
		c.italicAngle = float32(v[0])
	}
	_, c.isCID = top[opROS]

	// CharStrings INDEX gives the glyph count.
	csOff, ok := top[opCharStrings]
	if !ok || len(csOff) == 0 {
		return c, nil
	}
	csIdx, err := readIndex(data, int(csOff[0]))
	if err != nil {
		return c, nil // usable for name/italic angle even without charstrings
	}
	c.numGlyphs = uint16(len(csIdx.items))

	if off, ok := top[opCharset]; ok && len(off) > 0 {
		c.parseCharset(data, int(off[0]))
	}
	return c, nil
}

// parseCharset reads the glyph-to-SID mapping. Offsets 0, 1 and 2 name the
// predefined ISOAdobe/Expert charsets, where SID == GID for the ISOAdobe case.
func (c *cff) parseCharset(data []byte, off int) {
	n := int(c.numGlyphs)
	if n == 0 {
		return
	}

	if off <= 2 {
		if off == 0 { // ISOAdobe: identity SIDs
			c.charsetSIDs = make([]uint16, n)
			for i := 0; i < n; i++ {
				c.charsetSIDs[i] = uint16(i)
			}
		}
		return
	}
	if off >= len(data) {
		return
	}

	sids := make([]uint16, 1, n) // glyph 0 is always .notdef, SID 0
	format := data[off]
	p := off + 1

	switch format {
	case 0:
		for len(sids) < n && p+2 <= len(data) {
			sids = append(sids, binary.BigEndian.Uint16(data[p:]))
			p += 2
		}
	case 1, 2:
		nLeftSize := 1
		if format == 2 {
			nLeftSize = 2
		}
		for len(sids) < n && p+2+nLeftSize <= len(data) {
			first := binary.BigEndian.Uint16(data[p:])
			var nLeft int
			if format == 1 {
				nLeft = int(data[p+2])
			} else {
				nLeft = int(binary.BigEndian.Uint16(data[p+2:]))
			}
			p += 2 + nLeftSize
			for i := 0; i <= nLeft && len(sids) < n; i++ {
				sids = append(sids, first+uint16(i))
			}
		}
	default:
		return
	}
	c.charsetSIDs = sids
}

// glyphName resolves a glyph ID to its PostScript name. CID-keyed fonts have
// no glyph names — their charset holds CIDs, not SIDs.
func (c *cff) glyphName(gid uint16) (string, bool) {
	if c.isCID || int(gid) >= len(c.charsetSIDs) {
		return "", false
	}
	sid := int(c.charsetSIDs[gid])
	if sid < len(cffStandardStrings) {
		return cffStandardStrings[sid], true
	}
	i := sid - len(cffStandardStrings)
	if i < len(c.strings) && c.strings[i] != nil {
		return string(c.strings[i]), true
	}
	return "", false
}

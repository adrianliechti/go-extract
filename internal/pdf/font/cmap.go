package font

import "encoding/binary"

// Platform IDs from the sfnt cmap table (OpenType spec, "cmap").
const (
	PlatformUnicode   uint16 = 0
	PlatformMacintosh uint16 = 1
	PlatformWindows   uint16 = 3
)

// Windows encoding IDs that matter for text extraction.
const (
	EncodingWindowsSymbol  uint16 = 0
	EncodingWindowsUCS2    uint16 = 1
	EncodingWindowsUCS4    uint16 = 10
	EncodingMacintoshRoman uint16 = 0
)

// Cmap is a font's character-to-glyph mapping table, exposing every subtable
// so callers can pick by platform and encoding.
type Cmap struct {
	Subtables []*Subtable
}

// Subtable is one cmap subtable.
type Subtable struct {
	PlatformID uint16
	EncodingID uint16
	Format     uint16

	data []byte // subtable bytes, starting at its format field
}

// IsUnicode reports whether the subtable maps Unicode codepoints directly.
// Mirrors ttf-parser's Subtable::is_unicode.
func (s *Subtable) IsUnicode() bool {
	switch s.PlatformID {
	case PlatformUnicode:
		return true
	case PlatformWindows:
		return s.EncodingID == EncodingWindowsUCS2 || s.EncodingID == EncodingWindowsUCS4
	}
	return false
}

// IsSymbol reports the Windows Symbol encoding, which maps byte codes into the
// 0xF000 private use range rather than to real Unicode.
func (s *Subtable) IsSymbol() bool {
	return s.PlatformID == PlatformWindows && s.EncodingID == EncodingWindowsSymbol
}

// Cmap parses and returns the font's cmap table, or nil when absent or
// unparseable. The result is cached.
func (f *Face) Cmap() *Cmap {
	if f.cmapOnce {
		return f.cmap
	}
	f.cmapOnce = true

	t, ok := f.tables["cmap"]
	if !ok || len(t) < 4 {
		return nil
	}

	n := int(binary.BigEndian.Uint16(t[2:]))
	c := &Cmap{}
	for i := 0; i < n; i++ {
		rec := 4 + i*8
		if rec+8 > len(t) {
			break
		}
		off := int(binary.BigEndian.Uint32(t[rec+4:]))
		if off < 0 || off+2 > len(t) {
			continue
		}
		sub := &Subtable{
			PlatformID: binary.BigEndian.Uint16(t[rec:]),
			EncodingID: binary.BigEndian.Uint16(t[rec+2:]),
			Format:     binary.BigEndian.Uint16(t[off:]),
			data:       t[off:],
		}
		switch sub.Format {
		case 0, 4, 6, 12, 14:
			c.Subtables = append(c.Subtables, sub)
		}
	}

	if len(c.Subtables) == 0 {
		return nil
	}
	f.cmap = c
	return c
}

// GlyphIndex maps a codepoint to a glyph ID. Glyph 0 (.notdef) is reported as
// absent, matching ttf-parser.
func (s *Subtable) GlyphIndex(cp uint32) (uint16, bool) {
	var gid uint16
	switch s.Format {
	case 0:
		gid = s.glyphIndexFormat0(cp)
	case 4:
		gid = s.glyphIndexFormat4(cp)
	case 6:
		gid = s.glyphIndexFormat6(cp)
	case 12:
		gid = s.glyphIndexFormat12(cp)
	}
	return gid, gid != 0
}

// Codepoints calls fn for every codepoint the subtable maps to a non-zero
// glyph. Iteration order is ascending.
func (s *Subtable) Codepoints(fn func(uint32)) {
	switch s.Format {
	case 0:
		s.codepointsFormat0(fn)
	case 4:
		s.codepointsFormat4(fn)
	case 6:
		s.codepointsFormat6(fn)
	case 12:
		s.codepointsFormat12(fn)
	}
}

// ── format 0: byte encoding table ────────────────────────────────────

func (s *Subtable) glyphIndexFormat0(cp uint32) uint16 {
	if cp > 0xFF || len(s.data) < 6+256 {
		return 0
	}
	return uint16(s.data[6+cp])
}

func (s *Subtable) codepointsFormat0(fn func(uint32)) {
	if len(s.data) < 6+256 {
		return
	}
	for cp := 0; cp < 256; cp++ {
		if s.data[6+cp] != 0 {
			fn(uint32(cp))
		}
	}
}

// ── format 4: segment mapping to delta values ────────────────────────

// segments4 returns the parallel arrays of a format 4 subtable.
func (s *Subtable) segments4() (end, start, delta, rangeOffset []byte, segCount int, ok bool) {
	if len(s.data) < 14 {
		return nil, nil, nil, nil, 0, false
	}
	segCount = int(binary.BigEndian.Uint16(s.data[6:])) / 2
	if segCount == 0 {
		return nil, nil, nil, nil, 0, false
	}

	endOff := 14
	startOff := endOff + segCount*2 + 2 // +2 for reservedPad
	deltaOff := startOff + segCount*2
	rangeOff := deltaOff + segCount*2
	if rangeOff+segCount*2 > len(s.data) {
		return nil, nil, nil, nil, 0, false
	}

	return s.data[endOff:], s.data[startOff:], s.data[deltaOff:], s.data[rangeOff:], segCount, true
}

func (s *Subtable) glyphIndexFormat4(cp uint32) uint16 {
	if cp > 0xFFFF {
		return 0
	}
	end, start, delta, rangeOffset, segCount, ok := s.segments4()
	if !ok {
		return 0
	}
	c := uint16(cp)

	// Segments are sorted by endCode, so binary search applies.
	lo, hi := 0, segCount-1
	for lo <= hi {
		mid := (lo + hi) / 2
		if binary.BigEndian.Uint16(end[mid*2:]) < c {
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	if lo >= segCount {
		return 0
	}
	seg := lo
	if binary.BigEndian.Uint16(start[seg*2:]) > c {
		return 0
	}

	return resolveSegment4(s.data, start, delta, rangeOffset, seg, c)
}

// resolveSegment4 applies the idRangeOffset/idDelta rules for one segment.
func resolveSegment4(data, start, delta, rangeOffset []byte, seg int, c uint16) uint16 {
	ro := binary.BigEndian.Uint16(rangeOffset[seg*2:])
	d := binary.BigEndian.Uint16(delta[seg*2:])

	if ro == 0 {
		return c + d // modular arithmetic is intended here
	}

	// idRangeOffset is a byte offset from the address of the entry itself, so
	// recover the absolute index into the subtable.
	roFieldPos := len(data) - len(rangeOffset) + seg*2
	idx := roFieldPos + int(ro) + 2*int(c-binary.BigEndian.Uint16(start[seg*2:]))
	if idx < 0 || idx+2 > len(data) {
		return 0
	}
	g := binary.BigEndian.Uint16(data[idx:])
	if g == 0 {
		return 0
	}
	return g + d
}

func (s *Subtable) codepointsFormat4(fn func(uint32)) {
	end, start, delta, rangeOffset, segCount, ok := s.segments4()
	if !ok {
		return
	}
	for seg := 0; seg < segCount; seg++ {
		st := binary.BigEndian.Uint16(start[seg*2:])
		en := binary.BigEndian.Uint16(end[seg*2:])
		if st > en {
			continue
		}
		// 0xFFFF is the mandatory terminating segment.
		for c := uint32(st); c <= uint32(en); c++ {
			if c == 0xFFFF {
				continue
			}
			if resolveSegment4(s.data, start, delta, rangeOffset, seg, uint16(c)) != 0 {
				fn(c)
			}
		}
	}
}

// ── format 6: trimmed table mapping ──────────────────────────────────

func (s *Subtable) glyphIndexFormat6(cp uint32) uint16 {
	if len(s.data) < 10 {
		return 0
	}
	first := uint32(binary.BigEndian.Uint16(s.data[6:]))
	count := uint32(binary.BigEndian.Uint16(s.data[8:]))
	if cp < first || cp >= first+count {
		return 0
	}
	idx := 10 + int(cp-first)*2
	if idx+2 > len(s.data) {
		return 0
	}
	return binary.BigEndian.Uint16(s.data[idx:])
}

func (s *Subtable) codepointsFormat6(fn func(uint32)) {
	if len(s.data) < 10 {
		return
	}
	first := uint32(binary.BigEndian.Uint16(s.data[6:]))
	count := uint32(binary.BigEndian.Uint16(s.data[8:]))
	for i := uint32(0); i < count; i++ {
		idx := 10 + int(i)*2
		if idx+2 > len(s.data) {
			return
		}
		if binary.BigEndian.Uint16(s.data[idx:]) != 0 {
			fn(first + i)
		}
	}
}

// ── format 12: segmented coverage ────────────────────────────────────

func (s *Subtable) numGroups12() int {
	if len(s.data) < 16 {
		return 0
	}
	n := int(binary.BigEndian.Uint32(s.data[12:]))
	if max := (len(s.data) - 16) / 12; n > max {
		n = max
	}
	return n
}

func (s *Subtable) glyphIndexFormat12(cp uint32) uint16 {
	n := s.numGroups12()
	lo, hi := 0, n-1
	for lo <= hi {
		mid := (lo + hi) / 2
		g := s.data[16+mid*12:]
		startChar := binary.BigEndian.Uint32(g)
		endChar := binary.BigEndian.Uint32(g[4:])
		switch {
		case cp < startChar:
			hi = mid - 1
		case cp > endChar:
			lo = mid + 1
		default:
			return uint16(binary.BigEndian.Uint32(g[8:]) + (cp - startChar))
		}
	}
	return 0
}

func (s *Subtable) codepointsFormat12(fn func(uint32)) {
	n := s.numGroups12()
	for i := 0; i < n; i++ {
		g := s.data[16+i*12:]
		startChar := binary.BigEndian.Uint32(g)
		endChar := binary.BigEndian.Uint32(g[4:])
		if startChar > endChar {
			continue
		}
		// Guard against absurd ranges in corrupt fonts.
		if endChar-startChar > 0x10FFFF {
			endChar = startChar + 0x10FFFF
		}
		for cp := startChar; cp <= endChar; cp++ {
			fn(cp)
		}
	}
}

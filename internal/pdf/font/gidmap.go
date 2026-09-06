package font

// GIDToUnicode builds the reverse of the font's cmap: glyph ID to Unicode.
//
// This is the mapping PDF extraction actually needs. An Identity-H CID font
// uses CID == GID, so reversing the font's Unicode->GID cmap recovers
// CID->Unicode when the PDF supplies no ToUnicode stream.
//
// Where several codepoints map to the same glyph, the lowest wins, matching
// the reference implementation's first-write-wins behaviour.
//
// It returns nil when neither the cmap nor the glyph names yield anything.
func (f *Face) GIDToUnicode() map[uint16]rune {
	out := map[uint16]rune{}

	if c := f.Cmap(); c != nil {
		for _, sub := range c.Subtables {
			// Symbol subtables are included because they still carry usable
			// glyph coverage, just offset into the private use area.
			if !sub.IsUnicode() && !sub.IsSymbol() {
				continue
			}
			sub.Codepoints(func(cp uint32) {
				r := rune(cp)
				if r < 0 || r > 0x10FFFF {
					return
				}
				gid, ok := sub.GlyphIndex(cp)
				if !ok {
					return
				}
				if _, exists := out[gid]; !exists {
					out[gid] = r
				}
			})
		}
	}

	if len(out) > 0 {
		return out
	}

	// No usable cmap: fall back to glyph names, which is the only route for
	// CFF subsets whose post table is version 3.0.
	for gid := uint16(0); gid < f.numGlyphs; gid++ {
		name, ok := f.GlyphName(gid)
		if !ok {
			continue
		}
		if r, ok := GlyphToRune(name); ok {
			if _, exists := out[gid]; !exists {
				out[gid] = r
			}
		}
	}

	if len(out) == 0 {
		return nil
	}
	return out
}

// HasUnicodeCmap reports whether the font carries a cmap subtable with real
// coverage. The detector uses this to decide whether an Identity-H font can be
// decoded without a ToUnicode stream, or whether the page needs OCR.
func (f *Face) HasUnicodeCmap() bool {
	c := f.Cmap()
	if c == nil {
		return false
	}
	for _, sub := range c.Subtables {
		if !sub.IsUnicode() && !sub.IsSymbol() {
			continue
		}
		found := false
		sub.Codepoints(func(uint32) { found = true })
		if found {
			return true
		}
	}
	return false
}

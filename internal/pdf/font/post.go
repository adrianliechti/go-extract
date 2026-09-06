package font

import "encoding/binary"

// postHeaderSize is the fixed part of the post table preceding the format 2.0
// glyph name arrays.
const postHeaderSize = 32

// GlyphName returns the PostScript name for a glyph.
//
// Names come from the post table (format 2.0) when present, otherwise from the
// CFF charset. Note that a post table of version 3.0 stores no names at all —
// which is the common case in PDF-embedded subsets — so CFF is often the only
// source.
func (f *Face) GlyphName(gid uint16) (string, bool) {
	if names := f.postGlyphNames(); int(gid) < len(names) {
		if n := names[gid]; n != "" {
			return n, true
		}
	}
	if f.cff != nil {
		return f.cff.glyphName(gid)
	}
	return "", false
}

// postGlyphNames parses and caches the post table's per-glyph names. It returns
// nil for any version other than 2.0.
func (f *Face) postGlyphNames() []string {
	if f.postNamesSet {
		return f.postNames
	}
	f.postNamesSet = true

	t, ok := f.tables["post"]
	if !ok || len(t) < postHeaderSize+2 {
		return nil
	}
	// Version is Fixed 16.16; only 2.0 carries names.
	if binary.BigEndian.Uint32(t) != 0x00020000 {
		return nil
	}

	numGlyphs := int(binary.BigEndian.Uint16(t[postHeaderSize:]))
	idxStart := postHeaderSize + 2
	if numGlyphs == 0 || idxStart+numGlyphs*2 > len(t) {
		return nil
	}

	// Names beyond the Macintosh standard set follow as Pascal strings.
	var custom []string
	for p := idxStart + numGlyphs*2; p < len(t); {
		n := int(t[p])
		p++
		if p+n > len(t) {
			break
		}
		custom = append(custom, string(t[p:p+n]))
		p += n
	}

	names := make([]string, numGlyphs)
	for i := 0; i < numGlyphs; i++ {
		idx := int(binary.BigEndian.Uint16(t[idxStart+i*2:]))
		switch {
		case idx < len(macGlyphNames):
			names[i] = macGlyphNames[idx]
		case idx-len(macGlyphNames) < len(custom):
			names[i] = custom[idx-len(macGlyphNames)]
		}
	}
	f.postNames = names
	return names
}

// nameRecord returns a string from the sfnt `name` table by name ID, preferring
// a Windows/Unicode record (UTF-16BE) and falling back to Macintosh (single
// byte).
func (f *Face) nameRecord(nameID uint16) (string, bool) {
	t, ok := f.tables["name"]
	if !ok || len(t) < 6 {
		return "", false
	}
	count := int(binary.BigEndian.Uint16(t[2:]))
	storage := int(binary.BigEndian.Uint16(t[4:]))

	var macFallback string
	for i := 0; i < count; i++ {
		rec := 6 + i*12
		if rec+12 > len(t) {
			break
		}
		if binary.BigEndian.Uint16(t[rec+6:]) != nameID {
			continue
		}
		platform := binary.BigEndian.Uint16(t[rec:])
		length := int(binary.BigEndian.Uint16(t[rec+8:]))
		off := storage + int(binary.BigEndian.Uint16(t[rec+10:]))
		if off < 0 || length < 0 || off+length > len(t) {
			continue
		}
		raw := t[off : off+length]

		switch platform {
		case PlatformWindows, PlatformUnicode:
			return decodeUTF16BE(raw), true
		case PlatformMacintosh:
			if macFallback == "" {
				macFallback = string(raw)
			}
		}
	}
	if macFallback != "" {
		return macFallback, true
	}
	return "", false
}

func decodeUTF16BE(b []byte) string {
	out := make([]rune, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		u := rune(binary.BigEndian.Uint16(b[i:]))
		// Reassemble surrogate pairs.
		if u >= 0xD800 && u <= 0xDBFF && i+3 < len(b) {
			lo := rune(binary.BigEndian.Uint16(b[i+2:]))
			if lo >= 0xDC00 && lo <= 0xDFFF {
				out = append(out, 0x10000+(u-0xD800)<<10+(lo-0xDC00))
				i += 2
				continue
			}
		}
		out = append(out, u)
	}
	return string(out)
}

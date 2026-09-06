package font

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// GlyphToRune resolves a PostScript glyph name to a Unicode rune using the
// Adobe Glyph List, plus the uniXXXX and uXXXX[XX] conventions.
func GlyphToRune(name string) (rune, bool) {
	if r, ok := aglTable[name]; ok {
		return r, true
	}

	// Per the AGL spec, a suffix after '.' is a stylistic variant of the base
	// glyph: "zero.tf" -> "zero", "a.ss01" -> "a".
	if i := strings.IndexByte(name, '.'); i >= 0 {
		if r, ok := aglTable[name[:i]]; ok {
			return r, true
		}
	}

	// uniXXXX: exactly four hex digits.
	if strings.HasPrefix(name, "uni") && len(name) >= 7 {
		if v, err := strconv.ParseUint(name[3:7], 16, 32); err == nil {
			code := rune(v)
			// Windows Symbol fonts map ASCII into the F000 private use block;
			// fold it back so uniF041 reads as 'A'.
			if code >= 0xF000 && code <= 0xF0FF {
				code -= 0xF000
			}
			if utf8.ValidRune(code) {
				return code, true
			}
		}
	}

	// uXXXX / uXXXXX: the whole remainder is hex.
	if strings.HasPrefix(name, "u") && len(name) >= 5 {
		if v, err := strconv.ParseUint(name[1:], 16, 32); err == nil {
			if code := rune(v); utf8.ValidRune(code) {
				return code, true
			}
		}
	}

	return 0, false
}

// GlyphNameToString resolves a glyph name to the text it represents. Unlike
// GlyphToRune it can return multiple runes, because ligature glyphs name their
// components joined by underscores ("f_i" -> "fi").
func GlyphNameToString(name string) (string, bool) {
	base := name
	if i := strings.IndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}

	if r, ok := GlyphToRune(base); ok {
		return string(r), true
	}

	if strings.Contains(base, "_") {
		var b strings.Builder
		for _, part := range strings.Split(base, "_") {
			switch {
			case part == "":
				return "", false
			default:
				if r, ok := GlyphToRune(part); ok {
					b.WriteRune(r)
					continue
				}
				// A single-character component stands for itself.
				if utf8.RuneCountInString(part) == 1 {
					b.WriteString(part)
					continue
				}
				return "", false
			}
		}
		if b.Len() > 0 {
			return b.String(), true
		}
	}

	// Latin transliteration digraphs used by some Type1 fonts.
	switch base {
	case "ti", "tt", "tz":
		return base, true
	}

	return "", false
}

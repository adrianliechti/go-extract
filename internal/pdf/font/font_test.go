package font

import "testing"

func TestGeneratedTableSizes(t *testing.T) {
	// Both counts are fixed by their specifications; a mismatch means the
	// generator picked up the wrong block.
	if got, want := len(cffStandardStrings), 391; got != want {
		t.Errorf("cffStandardStrings = %d entries, want %d", got, want)
	}
	if got, want := len(macGlyphNames), 258; got != want {
		t.Errorf("macGlyphNames = %d entries, want %d", got, want)
	}
	if len(aglTable) < 4000 {
		t.Errorf("aglTable = %d entries, want >= 4000", len(aglTable))
	}

	// Spot-check anchors at both ends of each table.
	if cffStandardStrings[0] != ".notdef" {
		t.Errorf("cffStandardStrings[0] = %q", cffStandardStrings[0])
	}
	if cffStandardStrings[1] != "space" {
		t.Errorf("cffStandardStrings[1] = %q", cffStandardStrings[1])
	}
	if macGlyphNames[0] != ".notdef" {
		t.Errorf("macGlyphNames[0] = %q", macGlyphNames[0])
	}
	if macGlyphNames[1] != ".null" {
		t.Errorf("macGlyphNames[1] = %q", macGlyphNames[1])
	}
}

func TestGlyphToRune(t *testing.T) {
	tests := []struct {
		name string
		want rune
		ok   bool
	}{
		{"A", 'A', true},
		{"space", ' ', true},
		{"ampersand", '&', true},
		{"zero", '0', true},
		{"AE", 'Æ', true},

		// AGL variant suffixes resolve to the base glyph.
		{"zero.tf", '0', true},
		{"a.ss01", 'a', true},
		{"hyphen.case", '-', true},

		// uniXXXX form.
		{"uni0041", 'A', true},
		{"uni20AC", '€', true},
		// Windows Symbol private-use offset folds back to ASCII.
		{"uniF041", 'A', true},

		// uXXXX / uXXXXX form.
		{"u00041", 'A', true},

		{"definitelyNotAGlyphName", 0, false},
		{"", 0, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := GlyphToRune(tc.name)
			if ok != tc.ok {
				t.Fatalf("GlyphToRune(%q) ok = %v, want %v", tc.name, ok, tc.ok)
			}
			if ok && got != tc.want {
				t.Errorf("GlyphToRune(%q) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

func TestGlyphNameToString(t *testing.T) {
	tests := []struct {
		name string
		want string
		ok   bool
	}{
		{"A", "A", true},

		// "f_i" is itself an AGL entry, so the direct lookup wins and yields
		// the precomposed ligature rather than the decomposed components.
		{"f_i", "ﬁ", true},
		{"f_i.alt", "ﬁ", true},

		// Names absent from the AGL fall through to underscore splitting.
		{"A_B", "AB", true},
		{"one_two", "12", true},

		{"ti", "ti", true}, // Latin transliteration digraph
		{"nonsense_gibberish", "", false},
		{"_", "", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := GlyphNameToString(tc.name)
			if ok != tc.ok {
				t.Fatalf("GlyphNameToString(%q) ok = %v, want %v", tc.name, ok, tc.ok)
			}
			if ok && got != tc.want {
				t.Errorf("GlyphNameToString(%q) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"too short", []byte{0, 1}},
		{"bad magic", []byte{'J', 'U', 'N', 'K', 0, 0, 0, 0}},
		{"truncated sfnt", []byte{0, 1, 0, 0, 0, 5}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse(tc.data); err == nil {
				t.Error("Parse should have failed")
			}
		})
	}
}

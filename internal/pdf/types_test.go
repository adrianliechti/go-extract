package pdf

import "testing"

// makeTextItem mirrors the reference suite's helper in tests/integration_tests.rs,
// including its width estimate of len(text) * fontSize * 0.5. Rust's str::len is
// a byte count, which Go's len matches.
func makeTextItem(text string, x, y, fontSize float32, page uint32) TextItem {
	return TextItem{
		Text:     text,
		X:        x,
		Y:        y,
		Width:    float32(len(text)) * fontSize * 0.5,
		Height:   fontSize,
		Font:     "Helvetica",
		FontSize: fontSize,
		Page:     page,
	}
}

func TestTextItemCreation(t *testing.T) {
	item := makeTextItem("Hello", 100.0, 700.0, 12.0, 1)
	if item.Text != "Hello" {
		t.Errorf("Text = %q", item.Text)
	}
	if item.X != 100.0 || item.Y != 700.0 {
		t.Errorf("pos = (%v,%v), want (100,700)", item.X, item.Y)
	}
	if item.FontSize != 12.0 {
		t.Errorf("FontSize = %v", item.FontSize)
	}
	if item.Page != 1 {
		t.Errorf("Page = %v", item.Page)
	}
	if item.Type.Kind != KindText {
		t.Errorf("Kind = %v, want KindText", item.Type.Kind)
	}
	if item.MCID != nil {
		t.Errorf("MCID = %v, want nil", item.MCID)
	}
}

func TestTextLineText(t *testing.T) {
	tests := []struct {
		name  string
		items []TextItem
		want  string
	}{
		{
			name: "two words",
			items: []TextItem{
				makeTextItem("Hello", 100.0, 700.0, 12.0, 1),
				makeTextItem("World", 160.0, 700.0, 12.0, 1),
			},
			want: "Hello World",
		},
		{
			name:  "single item",
			items: []TextItem{makeTextItem("Single", 100.0, 700.0, 12.0, 1)},
			want:  "Single",
		},
		{
			name:  "empty",
			items: nil,
			want:  "",
		},
		{
			name: "three single chars",
			items: []TextItem{
				makeTextItem("A", 100.0, 700.0, 12.0, 1),
				makeTextItem("B", 120.0, 700.0, 12.0, 1),
				makeTextItem("C", 140.0, 700.0, 12.0, 1),
			},
			want: "A B C",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			line := TextLine{
				Items:             tc.items,
				Y:                 700.0,
				Page:              1,
				AdaptiveThreshold: DefaultAdaptiveThreshold,
			}
			if got := line.Text(); got != tc.want {
				t.Errorf("Text() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestShouldJoinItems(t *testing.T) {
	tests := []struct {
		name      string
		prev, cur TextItem
		threshold float32
		want      bool
	}{
		{
			name: "punctuation always joins",
			prev: makeTextItem("www", 100, 700, 12, 1),
			cur:  makeTextItem(".com", 400, 700, 12, 1), // far away; still joins
			want: true,
		},
		{
			name: "colon before alphanumeric takes a space",
			prev: makeTextItem("Clave:", 100, 700, 12, 1),
			cur:  makeTextItem("T9N2I6", 136, 700, 12, 1),
			want: false,
		},
		{
			name: "explicit trailing space is authoritative",
			prev: makeTextItem("word ", 100, 700, 12, 1),
			cur:  makeTextItem("next", 130, 700, 12, 1),
			want: false,
		},
		{
			name: "explicit leading space is authoritative",
			prev: makeTextItem("word", 100, 700, 12, 1),
			cur:  makeTextItem(" next", 130, 700, 12, 1),
			want: false,
		},
		{
			name: "column-scale gap never joins",
			prev: makeTextItem("left", 100, 700, 12, 1),
			cur:  makeTextItem("right", 400, 700, 12, 1),
			want: false,
		},
		{
			name: "adjacent glyphs join",
			prev: makeTextItem("Hel", 100, 700, 12, 1), // width 18 -> ends at 118
			cur:  makeTextItem("lo", 118, 700, 12, 1),  // gap 0
			want: true,
		},
		{
			name: "numeric continuity joins across a small gap",
			prev: makeTextItem("34,20", 100, 700, 12, 1), // width 30 -> ends 130
			cur:  makeTextItem("8", 133, 700, 12, 1),     // gap 3 < 12*0.3
			want: true,
		},
		{
			name: "sign before digit joins",
			prev: makeTextItem("+13.", 100, 700, 12, 1), // width 24 -> ends 124
			cur:  makeTextItem("0", 126, 700, 12, 1),    // gap 2
			want: true,
		},
		{
			name: "lowercase to uppercase never joins in fallback path",
			prev: TextItem{Text: "presente", X: 100, Y: 700, FontSize: 12, Font: "F"},
			cur:  TextItem{Text: "CONSTANCIA", X: 101, Y: 700, FontSize: 12, Font: "F"},
			want: false,
		},
		{
			name: "same case joins in fallback path",
			prev: TextItem{Text: "CONST", X: 100, Y: 700, FontSize: 12, Font: "F"},
			cur:  TextItem{Text: "ANCIA", X: 128, Y: 700, FontSize: 12, Font: "F"},
			want: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			th := tc.threshold
			if th == 0 {
				th = DefaultAdaptiveThreshold
			}
			if got := ShouldJoinItems(&tc.prev, &tc.cur, th); got != tc.want {
				t.Errorf("ShouldJoinItems = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCIDFontWordBoundary(t *testing.T) {
	// CID fonts emit one word per operator with a near-zero gap, so a
	// near-zero gap is a word boundary rather than a glyph boundary.
	prev := TextItem{Text: "Hello", X: 100, Y: 700, Width: 30, FontSize: 12, Font: "C2_0"}
	cur := TextItem{Text: "World", X: 130, Y: 700, Width: 30, FontSize: 12, Font: "C2_0"}
	if ShouldJoinItems(&prev, &cur, DefaultAdaptiveThreshold) {
		t.Error("CID font items with a zero gap must not join")
	}

	// The same geometry in a non-CID font is a glyph boundary and joins.
	prev.Font, cur.Font = "Helvetica", "Helvetica"
	if !ShouldJoinItems(&prev, &cur, DefaultAdaptiveThreshold) {
		t.Error("non-CID items with a zero gap must join")
	}
}

func TestCJKJoins(t *testing.T) {
	// CJK does not space words, so adjacent CJK items join even where the
	// Latin heuristics would insert a space.
	prev := TextItem{Text: "日本", X: 100, Y: 700, FontSize: 12, Font: "F"}
	cur := TextItem{Text: "語", X: 110, Y: 700, FontSize: 12, Font: "F"}
	if !ShouldJoinItems(&prev, &cur, DefaultAdaptiveThreshold) {
		t.Error("adjacent CJK items must join")
	}
}

func TestFormattingMarkers(t *testing.T) {
	bold := makeTextItem("bold", 100, 700, 12, 1)
	bold.IsBold = true
	plain := makeTextItem("plain", 130, 700, 12, 1)

	line := TextLine{
		Items:             []TextItem{bold, plain},
		Y:                 700,
		Page:              1,
		AdaptiveThreshold: DefaultAdaptiveThreshold,
	}

	if got, want := line.TextWithFormatting(true, false, false), "**bold** plain"; got != want {
		t.Errorf("bold: got %q, want %q", got, want)
	}
	if got, want := line.TextWithFormatting(false, false, false), "bold plain"; got != want {
		t.Errorf("plain: got %q, want %q", got, want)
	}
}

// Underline is exclusive: <u> content must carry no ** or * markers.
func TestUnderlineExcludesOtherMarkers(t *testing.T) {
	item := makeTextItem("styled", 100, 700, 12, 1)
	item.IsBold = true
	item.IsItalic = true
	item.IsUnderline = true

	line := TextLine{
		Items:             []TextItem{item},
		Y:                 700,
		Page:              1,
		AdaptiveThreshold: DefaultAdaptiveThreshold,
	}

	if got, want := line.TextWithFormatting(true, true, true), "<u>styled</u>"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

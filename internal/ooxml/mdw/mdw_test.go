package mdw

import (
	"strconv"
	"strings"
	"testing"
)

func TestListItemBoundsIndentation(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	for _, depth := range []int{-maxInt - 1, -1, 0, 8, 9, maxInt} {
		t.Run(strconv.Itoa(depth), func(t *testing.T) {
			w := New()
			w.ListItem(depth, 1, "Item")
			if want := strings.Repeat("    ", min(8, max(0, depth))) + "1. Item\n"; w.String() != want {
				t.Fatalf("ListItem = %q, want %q", w.String(), want)
			}
		})
	}
}

func TestWriterSeparatesBlocksAndKeepsListsContiguous(t *testing.T) {
	w := New()
	w.Block("Intro")
	w.ListItem(0, 0, "first")
	w.ListItem(1, 2, "nested ordered")
	w.EndList()
	w.Block("Done")

	const want = "Intro\n\n- first\n    2. nested ordered\n\nDone\n"
	if got := w.String(); got != want {
		t.Fatalf("Writer output:\n%q\nwant:\n%q", got, want)
	}
}

func TestWriterTableEscapesCellsAndPadsRows(t *testing.T) {
	w := New()
	w.Table([][]string{
		{"Name", "Value"},
		{"pipe", "a|b"},
		{"multiline", "one\ntwo"},
	})

	const want = "| Name      | Value      |\n| --------- | ---------- |\n| pipe      | a\\|b       |\n| multiline | one<br>two |\n"
	if got := w.String(); got != want {
		t.Fatalf("table output:\n%q\nwant:\n%q", got, want)
	}
}

func TestMarkdownEscapingHelpers(t *testing.T) {
	if got, want := EscapeInline(`a*[b]`), `a\*\[b\]`; got != want {
		t.Fatalf("EscapeInline = %q, want %q", got, want)
	}
	if got, want := EscapeCell(" a|b\r\nc "), `a\|b<br>c`; got != want {
		t.Fatalf("EscapeCell = %q, want %q", got, want)
	}
	if got, want := EscapeURL("a b(c)"), "a%20b%28c%29"; got != want {
		t.Fatalf("EscapeURL = %q, want %q", got, want)
	}
	if got, want := FlattenSpace("  alt\n  text\tvalue "), "alt text value"; got != want {
		t.Fatalf("FlattenSpace = %q, want %q", got, want)
	}
}

func TestWriterImageNormalizesAltAndTarget(t *testing.T) {
	w := New()
	w.Image(" Diagram\n preview ", "media/my image(1).png")
	if got, want := w.String(), "![Diagram preview](media/my%20image%281%29.png)\n"; got != want {
		t.Fatalf("Image output = %q, want %q", got, want)
	}
}

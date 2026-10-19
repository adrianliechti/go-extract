package mdw

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/adrianliechti/go-extract/internal/ooxml/opc"
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

func TestWriterTablePreservesEscapedCellsAndPadsMissingCells(t *testing.T) {
	w := New()
	w.Table([][]string{
		{"Name", "Value"},
		{"pipe", EscapeCell("a|b")},
		{"multiline", EscapeCell("one\ntwo")},
		{"short"},
	})

	const want = "| Name | Value |\n| --- | --- |\n| pipe | a\\|b |\n| multiline | one<br>two |\n| short |  |\n"
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

func TestOutputBudgetIsStickyAndIncludesStructure(t *testing.T) {
	w := New()
	w.limit = 6
	w.Block("12345") // The newline consumes the sixth byte.
	if w.Err() != nil || w.String() != "12345\n" {
		t.Fatal("exact boundary rejected")
	}
	w.Block("x")
	w.Raw("later")
	if !errors.Is(w.Err(), opc.ErrResourceLimit) || w.Len() != 6 {
		t.Fatal("output budget did not stop subsequent writes")
	}
	w = New()
	w.limit = 12
	w.Table([][]string{{"a"}}) // Header, separator and delimiters must all count.
	if !errors.Is(w.Err(), opc.ErrResourceLimit) {
		t.Fatal("table syntax bypassed budget")
	}
	w = New()
	if w.AdmitTable(int(^uint(0)>>1), 512) || !errors.Is(w.Err(), opc.ErrResourceLimit) {
		t.Fatal("overflowing table admitted")
	}
}

func TestCompactTableDoesNotMultiplyLongCell(t *testing.T) {
	rows := [][]string{{strings.Repeat("x", 20000)}}
	for i := 0; i < 80; i++ {
		rows = append(rows, []string{"short"})
	}
	w := New()
	w.Table(rows)
	if w.Err() != nil || w.Len() > 22000 {
		t.Fatalf("one long cell inflated table to %d bytes: %v", w.Len(), w.Err())
	}
}

func TestLinkSchemesAndDestinationDelimiters(t *testing.T) {
	for _, target := range []string{"javascript:alert(1)", "java\tscript:alert(1)", "\u00a0\ufeffJAVASCRIPT:alert(1)", "data:text/html,x", "vbscript:x"} {
		if got := Link("label", target); got != "label" {
			t.Errorf("unsafe target %q survived: %q", target, got)
		}
	}
	for _, target := range []string{"https://example.com", "mailto:a@example.com", "tel:1234", "../notes.docx", "#section"} {
		if got := Link("label", target); got != "[label]("+target+")" {
			t.Errorf("allowed target changed: %q", got)
		}
	}
	if got := EscapeURL("https://example.com/a)\n<b>\\c|d&x=1"); got != "https://example.com/a%29%0A%3Cb%3E%5Cc%7Cd&amp;x=1" {
		t.Fatalf("destination = %q", got)
	}
}

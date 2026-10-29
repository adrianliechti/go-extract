// Package mdw builds Markdown output. It centralises block spacing, inline
// escaping and GFM table rendering so all three converters emit consistent
// Markdown.
package mdw

import (
	"fmt"
	"strings"

	"github.com/adrianliechti/go-extract/internal/ooxml/opc"
)

// Writer accumulates Markdown, managing blank lines between blocks so callers
// do not have to track them.
type Writer struct {
	limit uint64
	err   error
	b     strings.Builder
	// pendingBreak records that a blank line is owed before the next block.
	pendingBreak bool
	// wroteAny records whether any block has been written, so the document
	// never starts with a blank line.
	wroteAny bool
	// listPending records that the previous write was a list item, so a run
	// of items stays contiguous but is separated from what follows.
	listPending bool
}

// maxOutputBytes is a fixed internal ceiling on generated Markdown. ZIP size
// limits do not bound repeated shared strings, merged cells or dense tables.
const maxOutputBytes uint64 = 64 << 20

func New() *Writer { return &Writer{limit: maxOutputBytes} }

func (w *Writer) Err() error { return w.err }

// Admit checks projected bytes before callers allocate expanded content.
// It does not reserve bytes; write accounts for the actual emitted output.
func (w *Writer) Admit(n uint64) bool {
	if w.err != nil {
		return false
	}
	if n > w.limit-uint64(w.b.Len()) {
		w.err = fmt.Errorf("%w: Markdown exceeds %d bytes", opc.ErrResourceLimit, w.limit)
		return false
	}
	return true
}

func (w *Writer) write(s string) {
	if w.Admit(uint64(len(s))) {
		w.b.WriteString(s)
	}
}

// AdmitTable rejects a dense table before its empty cells are allocated.
// Every cell needs at least three output bytes, even with no text or padding.
func (w *Writer) AdmitTable(rows, cols int) bool {
	if rows < 0 || cols < 0 {
		return w.Admit(w.limit + 1)
	}
	if uint64(cols) > w.limit/3 || uint64(rows) > w.limit/(uint64(cols)*3+2) {
		return w.Admit(w.limit + 1)
	}
	return w.Admit(uint64(rows) * (uint64(cols)*3 + 2))
}

// String returns the accumulated Markdown with a single trailing newline.
func (w *Writer) String() string {
	s := strings.TrimRight(w.b.String(), "\n")
	if s == "" {
		return ""
	}
	return s + "\n"
}

// Len reports how many bytes have been written.
func (w *Writer) Len() int { return w.b.Len() }

// startBlock emits the blank line separating this block from the previous one.
// A run of list items ends here, so the following block is set off from it.
func (w *Writer) startBlock() {
	if w.listPending {
		w.listPending = false
		w.pendingBreak = true
	}
	if !w.wroteAny {
		w.wroteAny = true
		return
	}
	if w.pendingBreak {
		w.write("\n")
		w.pendingBreak = false
	}
}

// startListItem is startBlock's counterpart for list items, which stay
// contiguous with any immediately preceding item.
func (w *Writer) startListItem() {
	if !w.wroteAny {
		w.wroteAny = true
		return
	}
	if w.pendingBreak && !w.listPending {
		w.write("\n")
	}
	w.pendingBreak = false
}

// Block writes a paragraph-level block, skipping empty content.
func (w *Writer) Block(s string) {
	s = strings.TrimRight(s, " \t")
	if strings.TrimSpace(s) == "" {
		return
	}
	w.startBlock()
	w.write(s)
	w.write("\n")
	w.pendingBreak = true
}

// Heading writes an ATX heading, clamping level to 1..6.
func (w *Writer) Heading(level int, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if level < 1 {
		level = 1
	}
	if level > 6 {
		level = 6
	}
	w.Block(strings.Repeat("#", level) + " " + text)
}

// ListItem writes one list item at the given nesting depth. An ordered item
// carries its number; an unordered item passes number <= 0.
func (w *Writer) ListItem(depth, number int, text string) {
	marker := "-"
	if number > 0 {
		marker = itoa(number) + "."
	}
	w.listItem(depth, marker, text)
}

// OrderedListItem includes a zero start, which is a valid CommonMark ordered
// marker. ListItem retains its zero-as-bullet convention for existing callers.
func (w *Writer) OrderedListItem(depth, number int, text string) {
	w.listItem(depth, itoa(number)+".", text)
}

func (w *Writer) listItem(depth int, marker, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	depth = ClampListDepth(depth)
	indent := strings.Repeat("    ", depth)

	// Consecutive list items form one block, so no blank line between them.
	w.startListItem()
	w.write(indent + marker + " " + text + "\n")
	w.listPending = true
}

// ClampListDepth keeps indentation within Office's nine list levels. Apply it
// before counter arithmetic as well as allocation: malformed levels can
// otherwise overflow counter-reset loops or amplify a tiny input into a huge
// Markdown indentation string.
func ClampListDepth(depth int) int { return min(8, max(0, depth)) }

// EndList closes a run of list items so the next block is separated from it.
func (w *Writer) EndList() {
	if w.listPending {
		w.pendingBreak = true
		w.listPending = false
	}
}

// Raw appends text verbatim, without block handling.
func (w *Writer) Raw(s string) { w.write(s) }

// Rule writes a thematic break.
func (w *Writer) Rule() {
	w.startBlock()
	w.write("---\n")
	w.pendingBreak = true
}

// Table renders already-escaped inline Markdown cells. Callers escape literal
// text before adding their own emphasis, links and line breaks. Re-escaping
// here would corrupt those constructs and turn escaped pipes into delimiters.
// Rows are padded with empty cells, but text is never padded to column width:
// a single long value must not multiply the size of every other row.
func (w *Writer) Table(rows [][]string) {
	if len(rows) == 0 || allEmpty(rows) {
		return
	}
	cols := 0
	for _, row := range rows {
		cols = max(cols, len(row))
	}
	if cols == 0 || !w.AdmitTable(len(rows)+1, cols) {
		return
	}
	w.startBlock()
	writeRow := func(row []string) {
		w.write("|")
		for col := 0; col < cols; col++ {
			w.write(" ")
			if col < len(row) {
				w.write(row[col])
			}
			w.write(" |")
		}
		w.write("\n")
	}
	writeRow(rows[0])
	w.write("|")
	for col := 0; col < cols; col++ {
		w.write(" --- |")
	}
	w.write("\n")
	for _, row := range rows[1:] {
		if w.Err() != nil {
			return
		}
		writeRow(row)
	}
	w.pendingBreak = true
}

// Image writes an image reference as its own block.
func (w *Writer) Image(alt, src string) {
	if target := EscapeURL(src); target != "" {
		w.Block("![" + EscapeInline(FlattenSpace(alt)) + "](" + target + ")")
	} else {
		w.Block(EscapeInline(FlattenSpace(alt)))
	}
}

// FlattenSpace collapses every run of whitespace to a single space and trims
// the ends. Alt text is authored as free-form prose and routinely contains
// newlines, which would split a Markdown image across lines and break it.
func FlattenSpace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\v' || r == '\f' {
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	return b.String()
}

func allEmpty(rows [][]string) bool {
	for _, r := range rows {
		for _, c := range r {
			if strings.TrimSpace(c) != "" {
				return false
			}
		}
	}
	return true
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

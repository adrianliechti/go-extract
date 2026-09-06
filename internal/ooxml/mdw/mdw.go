// Package mdw builds Markdown output. It centralises block spacing, inline
// escaping and GFM table rendering so all three converters emit consistent
// Markdown.
package mdw

import (
	"strings"
	"unicode/utf8"
)

// Writer accumulates Markdown, managing blank lines between blocks so callers
// do not have to track them.
type Writer struct {
	b strings.Builder
	// pendingBreak records that a blank line is owed before the next block.
	pendingBreak bool
	// wroteAny records whether any block has been written, so the document
	// never starts with a blank line.
	wroteAny bool
	// listPending records that the previous write was a list item, so a run
	// of items stays contiguous but is separated from what follows.
	listPending bool
}

// New returns an empty Writer.
func New() *Writer { return &Writer{} }

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
		w.b.WriteString("\n")
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
		w.b.WriteString("\n")
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
	w.b.WriteString(s)
	w.b.WriteString("\n")
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
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	depth = ClampListDepth(depth)
	indent := strings.Repeat("    ", depth)

	marker := "-"
	if number > 0 {
		marker = itoa(number) + "."
	}

	// Consecutive list items form one block, so no blank line between them.
	w.startListItem()
	w.b.WriteString(indent + marker + " " + text + "\n")
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
func (w *Writer) Raw(s string) { w.b.WriteString(s) }

// Rule writes a thematic break.
func (w *Writer) Rule() {
	w.startBlock()
	w.b.WriteString("---\n")
	w.pendingBreak = true
}

// Table renders rows as a GFM table. The first row is the header. Rows with
// differing lengths are padded so the table stays well-formed. An entirely
// empty table is skipped.
func (w *Writer) Table(rows [][]string) {
	if len(rows) == 0 {
		return
	}

	cols := 0
	for _, r := range rows {
		if len(r) > cols {
			cols = len(r)
		}
	}
	if cols == 0 {
		return
	}

	// Normalise shape and escape cell content.
	norm := make([][]string, len(rows))
	for i, r := range rows {
		row := make([]string, cols)
		for j := 0; j < cols; j++ {
			if j < len(r) {
				row[j] = EscapeCell(r[j])
			}
		}
		norm[i] = row
	}

	if allEmpty(norm) {
		return
	}

	// Column widths make the source readable and match the GFM-normalised
	// form used by the reference corpora.
	widths := make([]int, cols)
	for _, r := range norm {
		for j, c := range r {
			if n := utf8.RuneCountInString(c); n > widths[j] {
				widths[j] = n
			}
		}
	}
	for j := range widths {
		if widths[j] < 3 {
			widths[j] = 3
		}
	}

	w.startBlock()

	writeRow := func(cells []string) {
		w.b.WriteString("|")
		for j, c := range cells {
			w.b.WriteString(" ")
			w.b.WriteString(c)
			w.b.WriteString(strings.Repeat(" ", widths[j]-utf8.RuneCountInString(c)))
			w.b.WriteString(" |")
		}
		w.b.WriteString("\n")
	}

	writeRow(norm[0])

	w.b.WriteString("|")
	for j := 0; j < cols; j++ {
		w.b.WriteString(" " + strings.Repeat("-", widths[j]) + " |")
	}
	w.b.WriteString("\n")

	for _, r := range norm[1:] {
		writeRow(r)
	}
	w.pendingBreak = true
}

// Image writes an image reference as its own block.
func (w *Writer) Image(alt, src string) {
	w.Block("![" + EscapeInline(FlattenSpace(alt)) + "](" + EscapeURL(src) + ")")
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

// EscapeInline escapes Markdown control characters in inline text.
func EscapeInline(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\\', '`', '*', '_', '[', ']', '<', '>':
			b.WriteByte('\\')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// EscapeCell prepares text for a table cell: pipes are escaped and newlines
// become <br>, since a GFM cell cannot span lines.
func EscapeCell(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.ReplaceAll(s, "\n", "<br>")
	return strings.TrimSpace(s)
}

// EscapeURL percent-encodes the characters that would break a Markdown link
// target, leaving the rest readable.
func EscapeURL(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case ' ':
			b.WriteString("%20")
		case '(':
			b.WriteString("%28")
		case ')':
			b.WriteString("%29")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
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

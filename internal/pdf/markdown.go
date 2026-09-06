package pdf

import (
	"sort"
	"strings"
	"unicode"
)

// MarkdownOptions controls Markdown emission.
type MarkdownOptions struct {
	// FormatBold emits ** around bold runs.
	FormatBold bool
	// FormatItalic emits * around italic runs.
	FormatItalic bool
	// FormatUnderline emits <u> around underlined runs.
	FormatUnderline bool
	// IncludeImages emits a placeholder for each image XObject.
	IncludeImages bool
	// BaseFontSize overrides the detected body text size. Zero means detect.
	BaseFontSize float32
}

// DefaultMarkdownOptions matches the reference implementation's defaults.
func DefaultMarkdownOptions() MarkdownOptions {
	return MarkdownOptions{
		FormatBold:      true,
		FormatItalic:    true,
		FormatUnderline: true,
	}
}

// block is one emitted unit of Markdown: a heading, paragraph, or list item.
type blockKind uint8

const (
	blockParagraph blockKind = iota
	blockHeading
	blockList
	blockImage
	blockCode
	blockTable
)

type block struct {
	kind  blockKind
	level int // heading level, 1-6
	text  string
	page  uint32
	// y and fontSize carry the source line's geometry so paragraph joining can
	// tell a wrapped line from the start of a new paragraph.
	y        float32
	fontSize float32
	// indent is the line's left edge, used to spot indented paragraph starts.
	indent float32
}

// toMarkdown converts grouped lines into Markdown.
func toMarkdown(lines []TextLine, stats fontStats, opts MarkdownOptions) string {
	blocks := classifyLines(lines, stats, opts)
	blocks = joinParagraphs(blocks)
	return renderBlocks(blocks)
}

// toMarkdownFromPages keeps vector geometry available until tables have been
// reconstructed, then interleaves table placeholders with ordinary text in
// region-aware reading order.
func toMarkdownFromPages(pages []pageExtraction, stats fontStats, opts MarkdownOptions) string {
	type pageContent struct {
		items  []TextItem
		tables []*detectedTable
	}
	contents := make([]pageContent, 0, len(pages))
	var bodyItems []TextItem
	for _, page := range pages {
		tables := detectPageTables(page.items, page.rects, page.lines, page.page)
		claimed := make(map[int]bool)
		for _, table := range tables {
			for _, index := range table.itemIndices {
				claimed[index] = true
			}
		}
		remaining := make([]TextItem, 0, len(page.items)-len(claimed))
		for index, item := range page.items {
			if !claimed[index] {
				remaining = append(remaining, item)
				if item.Type.Kind == KindText || item.Type.Kind == KindFormField {
					bodyItems = append(bodyItems, item)
				}
			}
		}
		contents = append(contents, pageContent{items: remaining, tables: tables})
	}
	if len(bodyItems) > 0 {
		stats = calculateFontStats(bodyItems)
	}
	var lines []TextLine
	for _, content := range contents {
		lines = append(lines, layoutPageLines(content.items, content.tables, DefaultAdaptiveThreshold)...)
	}
	return toMarkdown(lines, stats, opts)
}

// classifyLines turns each line into a block, deciding heading level from the
// line's font size relative to the document's body size.
func classifyLines(lines []TextLine, stats fontStats, opts MarkdownOptions) []block {
	base := stats.bodySize
	if opts.BaseFontSize > 0 {
		base = opts.BaseFontSize
	}

	blocks := make([]block, 0, len(lines))
	for i := range lines {
		line := &lines[i]
		if len(line.Items) == 1 && line.Items[0].layoutTable != nil {
			table := line.Items[0].layoutTable
			text := tableToMarkdown(table)
			if text != "" {
				blocks = append(blocks, block{
					kind: blockTable, text: text, page: line.Page, y: line.Y,
					fontSize: line.Items[0].FontSize, indent: line.Items[0].X,
				})
			}
			continue
		}
		text := line.TextWithFormatting(opts.FormatBold, opts.FormatItalic, opts.FormatUnderline)
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}

		geom := block{
			page:     line.Page,
			y:        line.Y,
			fontSize: line.Items[0].FontSize,
			indent:   line.Items[0].X,
		}

		if isImagePlaceholder(line) {
			if opts.IncludeImages {
				b := geom
				b.kind, b.text = blockImage, text
				blocks = append(blocks, b)
			}
			continue
		}

		if isListItem(text) {
			b := geom
			b.kind, b.text = blockList, formatListItem(text)
			blocks = append(blocks, b)
			continue
		}

		if isCodeLine(line, text) {
			b := geom
			b.kind, b.text = blockCode, stripInlineEmphasis(text)
			blocks = append(blocks, b)
			continue
		}

		if level := headingLevel(line, text, base); level > 0 {
			b := geom
			b.kind, b.level, b.text = blockHeading, level, stripInlineEmphasis(text)
			blocks = append(blocks, b)
			continue
		}

		b := geom
		b.kind, b.text = blockParagraph, text
		blocks = append(blocks, b)
	}
	return blocks
}

// headingLevel returns the heading level for a line, or 0 if it is body text.
//
// Size relative to the body font is the primary signal, since PDFs carry no
// structural markup. A wholly bold line at body size is treated as a minor
// heading only when it is short enough not to be a bold sentence.
func headingLevel(line *TextLine, text string, base float32) int {
	if base <= 0 || len(line.Items) == 0 {
		return 0
	}
	// A line ending in sentence punctuation is prose, however it is set.
	if r, ok := lastRune(text); ok && (r == '.' || r == ',' || r == ';' || r == ':') {
		return 0
	}
	// Headings are short. The bound is generous because wrapped headings and
	// numbered section titles run long.
	if len([]rune(text)) > 120 {
		return 0
	}

	size := line.Items[0].FontSize
	ratio := size / base

	switch {
	case ratio >= 1.8:
		return 1
	case ratio >= 1.5:
		return 2
	case ratio >= 1.25:
		return 3
	case ratio >= 1.1:
		return 4
	}

	// Same-size bold lines read as sub-headings when short and not prose.
	if allBold(line) && len([]rune(text)) <= 60 && countWords(text) <= 10 {
		return 4
	}
	return 0
}

func allBold(line *TextLine) bool {
	for i := range line.Items {
		if strings.TrimSpace(line.Items[i].Text) == "" {
			continue
		}
		if !line.Items[i].IsBold {
			return false
		}
	}
	return len(line.Items) > 0
}

// isImagePlaceholder reports a line consisting solely of an image item.
func isImagePlaceholder(line *TextLine) bool {
	return len(line.Items) == 1 && line.Items[0].Type.Kind == KindImage
}

// isCodeLine recognizes high-confidence source-code syntax. It deliberately
// avoids generic parentheses and punctuation so ordinary prose is not fenced.
func isCodeLine(line *TextLine, text string) bool {
	for i := range line.Items {
		if isMonospaceFont(line.Items[i].baseFont) {
			return true
		}
	}
	t := strings.TrimSpace(stripInlineEmphasis(text))
	if t == "" {
		return false
	}
	for _, prefix := range []string{"print(", "printf(", "fmt.", "console."} {
		if strings.HasPrefix(t, prefix) {
			return true
		}
	}
	return strings.Contains(t, " := ") || strings.Contains(t, " => ") ||
		(strings.HasSuffix(t, ";") && strings.ContainsAny(t, "()={}"))
}

func isMonospaceFont(name string) bool {
	name = strings.ToLower(name)
	for _, pattern := range []string{
		"courier", "consolas", "monaco", "menlo", "mono", "fixed", "terminal",
		"typewriter", "source code", "fira code", "jetbrains", "inconsolata",
		"dejavu sans mono", "liberation mono",
	} {
		if strings.Contains(name, pattern) {
			return true
		}
	}
	return false
}

// isListItem reports text opening with a bullet, number, or letter marker.
func isListItem(text string) bool {
	t := strings.TrimLeft(text, " \t")

	for _, p := range []string{"• ", "- ", "* ", "○ ", "● ", "◦ "} {
		if strings.HasPrefix(t, p) {
			return true
		}
	}

	// Numbered markers: "1.", "1)", "10."
	head := firstRunes(t, 5)
	if strings.ContainsFunc(head, func(r rune) bool { return r >= '0' && r <= '9' }) {
		if i := strings.IndexAny(head, ".)"); i > 0 {
			allDigits := true
			for _, r := range head[:i] {
				if r < '0' || r > '9' {
					allDigits = false
					break
				}
			}
			if allDigits {
				return true
			}
		}
	}

	// Lettered markers: "a.", "a)", "(a)"
	rs := []rune(t)
	if len(rs) >= 2 {
		if isASCIILetter(rs[0]) && (rs[1] == '.' || rs[1] == ')') {
			return true
		}
		if len(rs) >= 3 && rs[0] == '(' && rs[2] == ')' {
			return true
		}
	}
	return false
}

// formatListItem rewrites a bullet marker as a Markdown dash, leaving numbered
// and lettered markers alone because Markdown renders those natively.
func formatListItem(text string) string {
	t := strings.TrimLeft(text, " \t")

	for _, bullet := range []string{"•", "○", "●", "◦"} {
		if rest, ok := strings.CutPrefix(t, bullet); ok {
			return "- " + strings.TrimLeft(rest, " \t")
		}
		// A bullet inside a leading style run ("**● Label:** rest") must move
		// outside the wrapper, or Markdown will not see a list item at all.
		for _, wrapper := range []string{"**", "*", "<u>"} {
			if after, ok := strings.CutPrefix(t, wrapper); ok {
				if rest, ok := strings.CutPrefix(after, bullet); ok {
					return "- " + wrapper + strings.TrimLeft(rest, " \t")
				}
			}
		}
	}

	return t
}

// joinParagraphs merges consecutive paragraph blocks on the same page into one,
// undoing the line wrapping of the original layout. A line ending in a hyphen
// is treated as a split word.
func joinParagraphs(blocks []block) []block {
	leading := typicalLeading(blocks)
	out := make([]block, 0, len(blocks))

	for _, b := range blocks {
		if b.kind != blockParagraph && b.kind != blockList {
			out = append(out, b)
			continue
		}

		n := len(out)
		if n == 0 || out[n-1].kind != b.kind || out[n-1].page != b.page {
			out = append(out, b)
			continue
		}
		// A new list item starts its own block; only continuation lines merge.
		if b.kind == blockList {
			out = append(out, b)
			continue
		}
		if startsNewParagraph(&out[n-1], &b, leading) {
			out = append(out, b)
			continue
		}
		if !continuesParagraph(out[n-1].text, b.text) {
			out = append(out, b)
			continue
		}

		prev := out[n-1].text
		if strings.HasSuffix(prev, "-") {
			out[n-1].text = strings.TrimSuffix(prev, "-") + b.text
		} else {
			out[n-1].text = prev + " " + b.text
		}
		// The accumulating block must carry the baseline of the line just
		// absorbed, so the next gap is measured line-to-line rather than from
		// the paragraph's first line.
		out[n-1].y = b.y
	}
	return out
}

// startsNewParagraph reports a geometric break between two consecutive lines:
// extra leading, a size change, or a first-line indent.
//
// Word processors separate paragraphs by adding space rather than by any
// marker, so the vertical gap is the primary signal. It is measured against the
// larger of the two font sizes, since leading scales with type size.
func startsNewParagraph(prev, next *block, leading float32) bool {
	size := maxF32(prev.fontSize, next.fontSize)
	if size <= 0 {
		return false
	}
	// Reading down one column and returning to the top of the next produces an
	// upward Y jump. Never merge prose across that column-flow boundary.
	if next.y > prev.y+size*.5 {
		return true
	}

	// A size change mid-flow is a new block, not a wrapped line.
	if absF32(prev.fontSize-next.fontSize) > size*0.15 {
		return true
	}

	// Lines run down the page, so the gap is the drop in baseline. Compare it
	// against the document's own typical leading rather than a fixed multiple
	// of the type size: documents set their line spacing very differently, and
	// what marks a paragraph is spacing beyond that document's normal.
	if gap := prev.y - next.y; gap > leading*1.5 {
		return true
	}

	// A first-line indent relative to the previous line also opens a paragraph,
	// but only when the previous line ran full width — otherwise a short
	// wrapped line would look like an indent.
	return next.indent > prev.indent+size*0.75
}

// continuesParagraph reports whether the next line continues the previous one
// rather than starting a new paragraph. A previous line closed by sentence
// punctuation and a next line opening with a capital reads as a new paragraph.
func continuesParagraph(prev, next string) bool {
	last, ok := lastRune(prev)
	if !ok {
		return false
	}
	if last == '-' {
		return true
	}
	if last != '.' && last != '!' && last != '?' {
		return true
	}

	first, ok := firstRune(next)
	return ok && !unicode.IsUpper(first)
}

// renderBlocks emits the final Markdown, separating blocks by blank lines and
// keeping consecutive list items tight.
func renderBlocks(blocks []block) string {
	var b strings.Builder
	inCode := false

	for i, blk := range blocks {
		if inCode && blk.kind != blockCode {
			b.WriteString("\n```")
			inCode = false
		}
		if i > 0 {
			// Consecutive list items form one list, so they take a single
			// newline; every other transition takes a blank line.
			if blk.kind == blockCode && blocks[i-1].kind == blockCode {
				b.WriteByte('\n')
			} else if blk.kind == blockList && blocks[i-1].kind == blockList {
				b.WriteByte('\n')
			} else {
				b.WriteString("\n\n")
			}
		}

		switch blk.kind {
		case blockHeading:
			b.WriteString(strings.Repeat("#", blk.level))
			b.WriteByte(' ')
			b.WriteString(blk.text)
		case blockCode:
			if !inCode {
				b.WriteString("```\n")
				inCode = true
			}
			b.WriteString(blk.text)
		case blockTable:
			b.WriteString(strings.TrimRight(blk.text, "\n"))
		default:
			b.WriteString(blk.text)
		}
	}
	if inCode {
		b.WriteString("\n```")
	}

	if b.Len() > 0 {
		b.WriteByte('\n')
	}
	return b.String()
}

// stripInlineEmphasis removes bold and italic markers from heading text, where
// the heading level already carries the emphasis.
func stripInlineEmphasis(s string) string {
	s = strings.ReplaceAll(s, "**", "")
	// Remove lone asterisks that were italic markers, leaving escaped ones.
	var b strings.Builder
	for i, r := range s {
		if r == '*' && (i == 0 || s[i-1] != '\\') {
			continue
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

func firstRunes(s string, n int) string {
	rs := []rune(s)
	if len(rs) > n {
		rs = rs[:n]
	}
	return string(rs)
}

func isASCIILetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

func countWords(s string) int { return len(strings.Fields(s)) }

// typicalLeading estimates a document's normal line spacing: the median
// baseline drop between consecutive body lines. Paragraph breaks are then
// spacing beyond this document's own normal, rather than a fixed multiple of
// the type size, which varies far too much between documents to be useful.
func typicalLeading(blocks []block) float32 {
	var gaps []float32
	for i := 1; i < len(blocks); i++ {
		prev, cur := &blocks[i-1], &blocks[i]
		if prev.page != cur.page || prev.kind != blockParagraph || cur.kind != blockParagraph {
			continue
		}
		// Ignore column returns (negative drops) and page-scale jumps.
		if gap := prev.y - cur.y; gap > 0 && gap < 200 {
			gaps = append(gaps, gap)
		}
	}
	if len(gaps) == 0 {
		return 0
	}
	sort.Slice(gaps, func(i, j int) bool { return gaps[i] < gaps[j] })
	return gaps[len(gaps)/2]
}

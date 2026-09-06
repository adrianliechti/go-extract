// Package pdf extracts text from PDF documents into structured Markdown.
//
// It is a Go port of the Rust pdf-inspector library. Geometry is carried as
// float32 rather than the more idiomatic float64: the reference implementation
// uses f32 throughout, and the layout heuristics compare accumulated
// coordinates against scaled thresholds (e.g. gap < fontSize*0.15). Matching
// the reference's float width keeps those comparisons from diverging.
package pdf

// ItemKind classifies an extracted item.
type ItemKind uint8

const (
	// KindText is regular text content.
	KindText ItemKind = iota
	// KindImage is an image placeholder.
	KindImage
	// KindLink is a hyperlink; the target is held in ItemType.URL.
	KindLink
	// KindFormField is a form field rendered as "name: value".
	KindFormField
)

// ItemType is the tagged-union equivalent of the reference's ItemType enum.
// URL is meaningful only when Kind is KindLink.
type ItemType struct {
	Kind ItemKind
	URL  string
}

// LinkType returns an ItemType for a hyperlink to url.
func LinkType(url string) ItemType { return ItemType{Kind: KindLink, URL: url} }

// LayoutComplexity reports whether extracted markdown is likely reliable or
// whether the document should be routed to an OCR pipeline instead.
type LayoutComplexity struct {
	// IsComplex is true if any page has tables or multi-column text.
	IsComplex bool
	// PagesWithTables lists 1-indexed pages where a ruled or aligned table was detected.
	PagesWithTables []uint32
	// PagesWithColumns lists 1-indexed pages where 2+ text columns were found.
	PagesWithColumns []uint32
}

// Line is a line segment from PDF path operators (m/l/S).
type Line struct {
	X1, Y1, X2, Y2 float32
	Page           uint32
	// StrokeWidth is the effective line width when the segment was painted.
	StrokeWidth float32
}

// Rect is a rectangle from a PDF `re` operator: a cell boundary, border, or
// background fill.
type Rect struct {
	X, Y          float32
	Width, Height float32
	Page          uint32
}

// TextItem is a run of text with its position on the page.
type TextItem struct {
	// Text is the decoded text content.
	Text string
	// X is the horizontal position on the page.
	X float32
	// Y is the vertical position in PDF coordinates (origin bottom-left).
	Y float32
	// Width is the rendered width of Text.
	Width float32
	// Height is approximated from the font size.
	Height float32
	// Font is the font resource name.
	Font string
	// baseFont is the resolved PostScript name, retained internally for
	// semantic heuristics such as monospace/code detection.
	baseFont string
	// FontSize is the effective font size after the text matrix is applied.
	FontSize float32
	// Page is the 1-indexed page number.
	Page uint32

	// IsBold reports a bold font.
	IsBold bool
	// IsItalic reports an italic font.
	IsItalic bool
	// IsUnderline reports a drawn rule below the baseline. PDFs carry no
	// underline flag, so this is detected geometrically after extraction.
	IsUnderline bool
	// IsStrikeout reports a drawn rule crossing the glyphs at mid x-height.
	IsStrikeout bool

	// Type classifies the item (text, image, link, form field).
	Type ItemType

	// MCID is the Marked Content ID from the content stream's BDC/BMC
	// operator, linking this item to the structure tree of a tagged PDF.
	// Nil when the item is not inside a marked-content sequence.
	MCID *int64

	// layoutTable is a synthetic layout placeholder used while interleaving a
	// detected table with ordinary text. It never escapes the package API.
	layoutTable *detectedTable
}

// DefaultAdaptiveThreshold is the per-page join threshold for normal PDFs.
// Canva-style letter-spaced pages derive a higher value via Otsu thresholding.
const DefaultAdaptiveThreshold float32 = 0.10

// TextLine is a set of text items sharing a baseline.
type TextLine struct {
	Items []TextItem
	Y     float32
	Page  uint32

	// AdaptiveThreshold is the page-level letter-spacing join threshold.
	AdaptiveThreshold float32
}

// Text returns the line's text without style markup.
func (l *TextLine) Text() string {
	return l.TextWithFormatting(false, false, false)
}

// TextWithFormatting renders the line, optionally emitting Markdown bold and
// italic markers and <u> tags.
//
// Underline is exclusive: <u> content carries no ** or * markers, because
// downstream eval harnesses match tag content literally and nested
// <u>**x**</u> breaks that.
func (l *TextLine) TextWithFormatting(formatBold, formatItalic, formatUnderline bool) string {
	if !formatBold && !formatItalic && !formatUnderline {
		return l.textPlain()
	}

	threshold := l.AdaptiveThreshold

	var b []byte
	var curBold, curItalic, curUnderline bool

	for i := range l.Items {
		item := &l.Items[i]
		text := item.Text
		trimmed := trimSpace(text)
		if trimmed == "" {
			continue
		}

		needsSpace := false
		if i > 0 && len(b) > 0 {
			needsSpace = needsSpaceBetween(&l.Items[i-1], item, b, threshold)
		}

		// An item like " means any person" carries a leading space marking a
		// word boundary. needsSpaceBetween returns false for it (a space
		// already exists), but the space must still be emitted because the
		// trimmed text is what gets appended.
		hasLeadingSpace := len(text) > 0 && text[0] == ' '

		itemUnderline := formatUnderline && item.IsUnderline
		itemBold := formatBold && item.IsBold && !itemUnderline
		itemItalic := formatItalic && item.IsItalic && !itemUnderline

		// Close styles that are ending, innermost first.
		if curItalic && !itemItalic {
			b = append(b, '*')
			curItalic = false
		}
		if curBold && !itemBold {
			b = append(b, '*', '*')
			curBold = false
		}
		if curUnderline && !itemUnderline {
			b = append(b, "</u>"...)
			curUnderline = false
		}

		if needsSpace || (hasLeadingSpace && len(b) > 0 && b[len(b)-1] != ' ') {
			b = append(b, ' ')
		}

		if itemUnderline && !curUnderline {
			b = append(b, "<u>"...)
			curUnderline = true
		}
		if itemBold && !curBold {
			b = append(b, '*', '*')
			curBold = true
		}
		if itemItalic && !curItalic {
			b = append(b, '*')
			curItalic = true
		}

		b = append(b, trimmed...)
	}

	if curItalic {
		b = append(b, '*')
	}
	if curBold {
		b = append(b, '*', '*')
	}
	if curUnderline {
		b = append(b, "</u>"...)
	}
	return string(b)
}

func (l *TextLine) textPlain() string {
	threshold := l.AdaptiveThreshold

	var b []byte
	for i := range l.Items {
		item := &l.Items[i]
		if i == 0 {
			b = append(b, item.Text...)
			continue
		}
		if needsSpaceBetween(&l.Items[i-1], item, b, threshold) {
			b = append(b, ' ')
		}
		b = append(b, item.Text...)
	}
	return string(b)
}

// needsSpaceBetween decides whether a space separates two adjacent items.
// `sofar` is the text emitted so far, used to test what the previous item
// actually contributed after trimming.
func needsSpaceBetween(prev, cur *TextItem, sofar []byte, singleCharThreshold float32) bool {
	text := cur.Text

	// Hyphenated words: no space on either side of the hyphen.
	prevEndsHyphen := len(sofar) > 0 && sofar[len(sofar)-1] == '-'
	curIsHyphen := trimSpace(text) == "-"
	curStartsHyphen := len(text) > 0 && text[0] == '-'

	// Sub/superscripts sit at a smaller size and a vertical offset.
	fontRatio := cur.FontSize / prev.FontSize
	reverseFontRatio := prev.FontSize / cur.FontSize
	yDiff := absF32(cur.Y - prev.Y)
	isSubSuper := fontRatio < 0.85 && yDiff > 1.0
	wasSubSuper := reverseFontRatio < 0.85 && yDiff > 1.0

	shouldJoin := ShouldJoinItems(prev, cur, singleCharThreshold)

	prevEndsSpace := len(sofar) > 0 && sofar[len(sofar)-1] == ' '
	curStartsSpace := len(text) > 0 && text[0] == ' '
	spaceExists := prevEndsSpace || curStartsSpace

	return !(prevEndsHyphen ||
		curIsHyphen ||
		curStartsHyphen ||
		isSubSuper ||
		wasSubSuper ||
		shouldJoin ||
		spaceExists)
}

func absF32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

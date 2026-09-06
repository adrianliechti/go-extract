package pdf

import (
	"sort"
	"strings"
)

// Detection thresholds, matching the reference implementation's defaults.
const (
	// minTextItemsPerPage is the item count above which a page counts as
	// carrying real text rather than a stray label on a scan.
	minTextItemsPerPage = 3
	// textPageRatioThreshold is the fraction of pages that must carry text for
	// a document to classify as text-based.
	textPageRatioThreshold = 0.6
	// tableRectThreshold is the painted-rectangle count above which a page is
	// judged to hold ruled tables.
	tableRectThreshold = 6
)

// classify determines the document type, its confidence, and which pages
// should be routed to OCR.
func classify(pages []pageExtraction) (Type, float32, []uint32, []PageOCRReasons) {
	if len(pages) == 0 {
		return TypeScanned, 0.9, nil, nil
	}

	var pagesWithText, pagesWithImages, totalTextItems int
	for _, p := range pages {
		if countTextItems(p) >= minTextItemsPerPage {
			pagesWithText++
		}
		if p.hasImages {
			pagesWithImages++
		}
		totalTextItems += countTextItems(p)
	}

	textRatio := float32(pagesWithText) / float32(len(pages))

	var (
		pdfType    Type
		confidence float32
		needsOCR   bool
	)
	switch {
	case textRatio >= textPageRatioThreshold:
		pdfType, confidence, needsOCR = TypeTextBased, textRatio, false
	case pagesWithText == 0 && pagesWithImages > 0:
		needsOCR = true
		if totalTextItems == 0 {
			pdfType, confidence = TypeScanned, 0.95
		} else {
			pdfType, confidence = TypeImageBased, 0.8
		}
	case pagesWithText > 0 && pagesWithImages > 0:
		pdfType, confidence, needsOCR = TypeMixed, 0.7, true
	case totalTextItems == 0:
		pdfType, confidence, needsOCR = TypeScanned, 0.9, true
	default:
		pdfType, confidence, needsOCR = TypeTextBased, maxF32(textRatio, 0.5), false
	}

	var ocrPages []uint32
	var reasons []PageOCRReasons
	for _, p := range pages {
		// A failed page must remain visible even when the successfully extracted
		// pages make the document text-based overall.
		if !needsOCR && !p.failed {
			continue
		}
		page := pageNumberOf(p)
		if page == 0 {
			continue
		}
		why := ocrReasons(p)
		if len(why) == 0 {
			continue
		}
		ocrPages = append(ocrPages, page)
		reasons = append(reasons, PageOCRReasons{Page: page, Reasons: why})
	}
	return pdfType, confidence, ocrPages, reasons
}

// ocrReasons lists why a page should be routed to OCR, or nothing if it need
// not be.
func ocrReasons(p pageExtraction) []string {
	var out []string
	text := countTextItems(p)

	switch {
	case text == 0:
		if p.hasImages {
			out = append(out, OCRReasonScanned)
		} else {
			out = append(out, OCRReasonNoText)
		}
	case text < minTextItemsPerPage && p.hasImages:
		out = append(out, OCRReasonScanned)
	}
	return out
}

// countTextItems counts the genuine text items on a page, excluding image
// placeholders.
func countTextItems(p pageExtraction) int {
	n := 0
	for _, it := range p.items {
		if it.Type.Kind == KindText || it.Type.Kind == KindFormField {
			n++
		}
	}
	return n
}

// pageNumberOf returns the 1-indexed page number a page extraction belongs to.
func pageNumberOf(p pageExtraction) uint32 { return p.page }

// analyseLayout reports pages carrying reconstructed tables or multiple text
// columns. Table-owned text is removed before looking for a page gutter so a
// wide table is not itself mistaken for a multi-column prose layout.
func analyseLayout(pages []pageExtraction) LayoutComplexity {
	var lc LayoutComplexity

	for _, p := range pages {
		page := pageNumberOf(p)
		if page == 0 {
			continue
		}
		tables := detectPageTables(p.items, p.rects, p.lines, page)
		if len(tables) > 0 {
			lc.PagesWithTables = append(lc.PagesWithTables, page)
		}
		claimed := make(map[int]bool)
		for _, table := range tables {
			for _, index := range table.itemIndices {
				claimed[index] = true
			}
		}
		remaining := make([]TextItem, 0, len(p.items)-len(claimed))
		for index, item := range p.items {
			if !claimed[index] && (item.Type.Kind == KindText || item.Type.Kind == KindFormField) {
				remaining = append(remaining, item)
			}
		}
		if _, ok := sparseVerticalGutter(remaining); ok {
			lc.PagesWithColumns = append(lc.PagesWithColumns, page)
		}
	}

	sort.Slice(lc.PagesWithTables, func(i, j int) bool { return lc.PagesWithTables[i] < lc.PagesWithTables[j] })
	sort.Slice(lc.PagesWithColumns, func(i, j int) bool { return lc.PagesWithColumns[i] < lc.PagesWithColumns[j] })
	lc.IsComplex = len(lc.PagesWithTables) > 0 || len(lc.PagesWithColumns) > 0
	return lc
}

// columnCount estimates how many text columns a page holds, by projecting item
// spans onto the x axis and counting the gutters that split them.
//
// A gutter is a vertical band no item crosses, wide relative to the text size
// and flanked by substantial text on both sides. Narrow gaps between words and
// the ragged right margin do not qualify.
func columnCount(items []TextItem) int {
	type span struct{ lo, hi float32 }
	var spans []span
	var minX, maxX float32
	var fontSum float32
	n := 0

	for _, it := range items {
		if it.Type.Kind != KindText || strings.TrimSpace(it.Text) == "" {
			continue
		}
		w := it.Width
		if w <= 0 {
			w = float32(len([]rune(it.Text))) * it.FontSize * 0.5
		}
		s := span{it.X, it.X + w}
		if n == 0 || s.lo < minX {
			minX = s.lo
		}
		if n == 0 || s.hi > maxX {
			maxX = s.hi
		}
		spans = append(spans, s)
		fontSum += it.FontSize
		n++
	}
	// Too little text to judge; a handful of labels is not a column layout.
	if n < 20 || maxX <= minX {
		return 1
	}
	avgFont := fontSum / float32(n)

	// Occupancy histogram over the text's horizontal extent.
	const bins = 200
	binW := (maxX - minX) / bins
	if binW <= 0 {
		return 1
	}
	occupied := make([]int, bins)
	for _, s := range spans {
		lo := int((s.lo - minX) / binW)
		hi := int((s.hi - minX) / binW)
		lo = max(lo, 0)
		hi = min(hi, bins-1)
		for i := lo; i <= hi; i++ {
			occupied[i]++
		}
	}

	// A gutter must be at least this wide to separate columns rather than
	// words. Two ems is the narrowest inter-column gap in common use.
	minGutter := int(maxF32(avgFont*2.0, binW) / binW)
	if minGutter < 1 {
		minGutter = 1
	}

	columns, runStart := 0, -1
	inText := false
	for i := 0; i <= bins; i++ {
		empty := i == bins || occupied[i] == 0
		if !empty {
			if !inText {
				inText, runStart = true, i
			}
			continue
		}
		if !inText {
			continue
		}
		// Close the current text run and decide whether the following gap is
		// wide enough to be a gutter.
		gapEnd := i
		for gapEnd < bins && occupied[gapEnd] == 0 {
			gapEnd++
		}
		runWide := i-runStart >= minGutter
		if (gapEnd-i >= minGutter && runWide) || i == bins {
			if runWide {
				columns++
			}
		}
		inText = false
	}
	return max(columns, 1)
}

// hasEncodingIssues reports text damaged by a broken font encoding, which
// signals that the document should be routed to OCR rather than trusted.
func hasEncodingIssues(items []TextItem) bool { return hasEncodingIssuesIn(items) }

func hasEncodingIssuesIn(items []TextItem) bool { return analyzeTextQuality(items).hasEncodingIssues }

package pdf

import (
	"sort"
	"strings"
	"unicode"
)

// lineYTolerance is the baseline spread within which items belong to one line.
const lineYTolerance float32 = 3.0

// groupIntoLines assembles text items into lines in reading order, one page at
// a time. Items arrive in content-stream order, which is usually already
// reading order; pages whose stream order is chaotic are sorted by baseline
// first. See shouldSortByY.
func groupIntoLines(items []TextItem, thresholds map[uint32]float32) []TextLine {
	if len(items) == 0 {
		return nil
	}

	byPage := map[uint32][]TextItem{}
	var pages []uint32
	for _, it := range items {
		if _, seen := byPage[it.Page]; !seen {
			pages = append(pages, it.Page)
		}
		byPage[it.Page] = append(byPage[it.Page], it)
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i] < pages[j] })

	var out []TextLine
	for _, page := range pages {
		threshold, ok := thresholds[page]
		if !ok {
			threshold = DefaultAdaptiveThreshold
		}
		out = append(out, groupPageLines(byPage[page], threshold)...)
	}
	return out
}

// groupPageLines groups one page's items into lines.
func groupPageLines(items []TextItem, threshold float32) []TextLine {
	if len(items) == 0 {
		return nil
	}

	if shouldSortByY(items) {
		sort.SliceStable(items, func(i, j int) bool {
			if items[i].Y != items[j].Y {
				return items[i].Y > items[j].Y
			}
			return items[i].X < items[j].X
		})
	}

	var lines []TextLine
	for _, item := range items {
		if n := len(lines); n > 0 && continuesLine(&lines[n-1], &item) {
			lines[n-1].Items = append(lines[n-1].Items, item)
			continue
		}
		lines = append(lines, TextLine{
			Items:             []TextItem{item},
			Y:                 item.Y,
			Page:              item.Page,
			AdaptiveThreshold: threshold,
		})
	}

	for i := range lines {
		sortLineItems(lines[i].Items)
	}
	return lines
}

// layoutPageLines groups one page in logical reading order. A sparse vertical
// gutter creates newspaper-style column flow; items spanning that gutter split
// the page into vertical zones and remain in their visual position between the
// column runs above and below them.
func layoutPageLines(items []TextItem, tables []*detectedTable, threshold float32) []TextLine {
	for _, table := range tables {
		items = append(items, TextItem{
			Text: "[Table]", X: table.x1, Y: table.yTop,
			Width: table.x2 - table.x1, Height: table.yTop - table.yBot,
			FontSize: 10, Page: table.page, Type: ItemType{Kind: KindImage},
			layoutTable: table,
		})
	}
	if len(items) == 0 {
		return nil
	}
	ordered := orderItemsByColumns(append([]TextItem(nil), items...), 0)
	return groupPageLinesInOrder(ordered, threshold)
}

func groupPageLinesInOrder(items []TextItem, threshold float32) []TextLine {
	var lines []TextLine
	for _, item := range items {
		if n := len(lines); n > 0 && item.layoutTable == nil &&
			lines[n-1].Items[0].layoutTable == nil && continuesLine(&lines[n-1], &item) {
			lines[n-1].Items = append(lines[n-1].Items, item)
			continue
		}
		lines = append(lines, TextLine{
			Items: []TextItem{item}, Y: item.Y, Page: item.Page, AdaptiveThreshold: threshold,
		})
	}
	for i := range lines {
		sortLineItems(lines[i].Items)
	}
	return lines
}

func orderItemsByColumns(items []TextItem, depth int) []TextItem {
	if len(items) < 4 || depth >= 3 {
		sortItemsTopDown(items)
		return items
	}
	split, ok := sparseVerticalGutter(items)
	if !ok {
		sortItemsTopDown(items)
		return items
	}

	var left, right, spanning []TextItem
	for _, item := range items {
		x1, x2 := itemHorizontalBounds(item)
		if x1 < split-4 && x2 > split+4 {
			spanning = append(spanning, item)
			continue
		}
		if (x1+x2)/2 < split {
			left = append(left, item)
		} else {
			right = append(right, item)
		}
	}
	if len(left) < 2 || len(right) < 2 {
		sortItemsTopDown(items)
		return items
	}

	sortItemsTopDown(spanning)
	// Coalesce same-baseline spanning fragments into one separator level.
	var levels [][]TextItem
	for _, item := range spanning {
		if len(levels) == 0 || absF32(levels[len(levels)-1][0].Y-item.Y) > lineYTolerance {
			levels = append(levels, []TextItem{item})
		} else {
			levels[len(levels)-1] = append(levels[len(levels)-1], item)
		}
	}
	// A full-width run can be split into differently styled items. Fragments
	// on the same baseline may sit wholly on one side of the gutter; attach
	// them to the spanning level so the zone bounds do not silently drop them.
	attachLevelPeers := func(side []TextItem) []TextItem {
		kept := side[:0]
		for _, item := range side {
			attached := false
			for i := range levels {
				if absF32(levels[i][0].Y-item.Y) <= lineYTolerance {
					levels[i] = append(levels[i], item)
					attached = true
					break
				}
			}
			if !attached {
				kept = append(kept, item)
			}
		}
		return kept
	}
	left = attachLevelPeers(left)
	right = attachLevelPeers(right)

	var out []TextItem
	upper := float32(1e30)
	appendZone := func(lower float32) {
		var zoneLeft, zoneRight []TextItem
		for _, item := range left {
			if item.Y < upper && item.Y > lower {
				zoneLeft = append(zoneLeft, item)
			}
		}
		for _, item := range right {
			if item.Y < upper && item.Y > lower {
				zoneRight = append(zoneRight, item)
			}
		}
		out = append(out, orderItemsByColumns(zoneLeft, depth+1)...)
		out = append(out, orderItemsByColumns(zoneRight, depth+1)...)
	}
	for _, level := range levels {
		y := level[0].Y
		appendZone(y + lineYTolerance)
		sort.SliceStable(level, func(i, j int) bool { return level[i].X < level[j].X })
		out = append(out, level...)
		upper = y - lineYTolerance
	}
	appendZone(-1e30)
	return out
}

func sparseVerticalGutter(items []TextItem) (float32, bool) {
	minX, maxX := float32(0), float32(0)
	set := false
	for _, item := range items {
		x1, x2 := itemHorizontalBounds(item)
		if !set {
			minX, maxX, set = x1, x2, true
		} else {
			minX, maxX = minF32(minX, x1), maxF32(maxX, x2)
		}
	}
	width := maxX - minX
	if !set || width < 80 {
		return 0, false
	}

	const bins = 160
	occupancy := make([]int, bins)
	for _, item := range items {
		x1, x2 := itemHorizontalBounds(item)
		lo := max(0, min(bins-1, int((x1-minX)/width*bins)))
		hi := max(0, min(bins-1, int((x2-minX)/width*bins)))
		for i := lo; i <= hi; i++ {
			occupancy[i]++
		}
	}
	// Full-width headings, captions, and separators legitimately cross a
	// column gutter. Treat a small number of crossings as sparse occupancy.
	maxOccupancy := max(2, len(items)/20)
	minRun := max(2, int(maxF32(15, medianItemFontSize(items)*1.5)/width*bins))
	bestStart, bestEnd := -1, -1
	for i := 1; i < bins-1; {
		if occupancy[i] > maxOccupancy {
			i++
			continue
		}
		start := i
		for i < bins-1 && occupancy[i] <= maxOccupancy {
			i++
		}
		end := i
		if end-start >= minRun && start > bins/10 && end < bins*9/10 && end-start > bestEnd-bestStart {
			bestStart, bestEnd = start, end
		}
	}
	if bestStart < 0 {
		return 0, false
	}
	split := minX + (float32(bestStart+bestEnd)/2)/bins*width

	left, right, crossing := 0, 0, 0
	leftMinY, leftMaxY, rightMinY, rightMaxY := float32(0), float32(0), float32(0), float32(0)
	for _, item := range items {
		x1, x2 := itemHorizontalBounds(item)
		switch {
		case x1 < split-4 && x2 > split+4:
			crossing++
		case (x1+x2)/2 < split:
			left++
			if left == 1 {
				leftMinY, leftMaxY = item.Y, item.Y
			} else {
				leftMinY, leftMaxY = minF32(leftMinY, item.Y), maxF32(leftMaxY, item.Y)
			}
		default:
			right++
			if right == 1 {
				rightMinY, rightMaxY = item.Y, item.Y
			} else {
				rightMinY, rightMaxY = minF32(rightMinY, item.Y), maxF32(rightMaxY, item.Y)
			}
		}
	}
	verticalOverlap := minF32(leftMaxY, rightMaxY) - maxF32(leftMinY, rightMinY)
	if left < 2 || right < 2 || verticalOverlap < medianItemFontSize(items)*2 || crossing > (left+right)/2 {
		return 0, false
	}
	return split, true
}

func itemHorizontalBounds(item TextItem) (float32, float32) {
	w := item.Width
	if w <= 0 {
		w = float32(len([]rune(item.Text))) * maxF32(item.FontSize, 8) * .45
	}
	return item.X, item.X + w
}

func medianItemFontSize(items []TextItem) float32 {
	values := make([]float32, 0, len(items))
	for _, item := range items {
		if item.FontSize > 0 {
			values = append(values, item.FontSize)
		}
	}
	if len(values) == 0 {
		return 10
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	return values[len(values)/2]
}

func sortItemsTopDown(items []TextItem) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Y != items[j].Y {
			return items[i].Y > items[j].Y
		}
		return items[i].X < items[j].X
	})
}

// continuesLine reports whether item belongs to the line under construction.
// Only the most recent line is considered, since items arrive in reading order.
func continuesLine(line *TextLine, item *TextItem) bool {
	if line.Page != item.Page {
		return false
	}
	yDiff := absF32(line.Y - item.Y)
	if yDiff >= lineYTolerance {
		return false
	}

	// A small baseline shift combined with a return to the left margin marks
	// vertically stacked lines rather than one line with out-of-order items.
	if yDiff > 0.5 && len(line.Items) > 0 {
		first := &line.Items[0]
		if absF32(item.X-first.X) < 5.0 {
			return false
		}
		last := &line.Items[len(line.Items)-1]
		if item.X < last.X-10.0 {
			return false
		}
	}

	if len(line.Items) > 0 && splitAcrossGutter(line, item) {
		return false
	}
	return true
}

// splitAcrossGutter reports a same-baseline item separated by a void wide
// enough to be a column gutter that column detection did not resolve.
//
// Both sides must read as prose: table-of-contents page numbers, dot leaders,
// and outline-numbered table cells start with digits and stay joined.
func splitAcrossGutter(line *TextLine, item *TextItem) bool {
	last := &line.Items[len(line.Items)-1]

	gap := item.X - (last.X + last.Width)
	if gap <= maxF32(maxF32(item.FontSize, last.FontSize)*3.0, 30.0) {
		return false
	}
	firstRuneOf, ok := firstRune(strings.TrimSpace(item.Text))
	if !ok || !unicode.IsLetter(firstRuneOf) {
		return false
	}

	// The incoming run must be substantial prose; the line side may be short,
	// as a wrapped heading's last words would be.
	incoming := strings.TrimSpace(item.Text)
	if len(strings.Fields(incoming)) < 3 || countLetters(incoming) < 10 {
		return false
	}

	var sb strings.Builder
	for i := range line.Items {
		if i > 0 {
			sb.WriteByte(' ')
		}
		sb.WriteString(strings.TrimSpace(line.Items[i].Text))
	}
	lineText := sb.String()
	if len(strings.Fields(lineText)) < 2 || countLetters(lineText) < 8 {
		return false
	}

	// A lowercase start is a mid-sentence continuation and splits on the prose
	// signals alone. An uppercase start additionally needs a style mismatch —
	// a wholly bold heading beside regular body text — or same-style label rows
	// such as feature tiles and legends would shatter.
	startsLower := unicode.IsLower(firstRuneOf)
	allBold := true
	for i := range line.Items {
		if !line.Items[i].IsBold {
			allBold = false
			break
		}
	}
	styleMismatch := allBold && !item.IsBold

	return startsLower || styleMismatch
}

// shouldSortByY reports whether a page's stream order is too chaotic to use as
// reading order. A well-ordered document progresses down the page, so a high
// proportion of large upward jumps means the stream order cannot be trusted.
func shouldSortByY(items []TextItem) bool {
	if len(items) < 5 {
		return false
	}

	const jumpThreshold = 50.0
	var up, down int
	for i := 1; i < len(items); i++ {
		switch delta := items[i].Y - items[i-1].Y; {
		case delta > jumpThreshold:
			up++
		case delta < -jumpThreshold:
			down++
		}
	}

	total := up + down
	if total < 3 {
		return false
	}
	return float32(up) > float32(total)*0.4
}

// sortLineItems orders a line's items along its reading direction.
func sortLineItems(items []TextItem) {
	refs := make([]*TextItem, len(items))
	for i := range items {
		refs[i] = &items[i]
	}
	if isRTLText(refs) {
		sort.SliceStable(items, func(i, j int) bool { return items[i].X > items[j].X })
		return
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].X < items[j].X })
}

func countLetters(s string) int {
	n := 0
	for _, r := range s {
		if unicode.IsLetter(r) {
			n++
		}
	}
	return n
}

// fontStats summarises the font sizes in a document, used to tell body text
// from headings.
type fontStats struct {
	// bodySize is the most common size at or above 9pt, the presumed body text.
	bodySize float32
	// sizeCounts maps a size in tenths of a point to how often it appears.
	sizeCounts map[int]int
}

// calculateFontStats counts font sizes across items. Sizes below 9pt are
// ignored so that footnotes and captions cannot become the body size.
func calculateFontStats(items []TextItem) fontStats {
	counts := map[int]int{}
	for _, it := range items {
		if it.FontSize >= 9.0 {
			counts[int(it.FontSize*10)]++
		}
	}

	body := float32(12.0)
	best := -1
	for size, n := range counts {
		// Ties resolve to the smaller size, so output stays deterministic.
		if n > best || (n == best && float32(size)/10 < body) {
			best, body = n, float32(size)/10
		}
	}
	return fontStats{bodySize: body, sizeCounts: counts}
}

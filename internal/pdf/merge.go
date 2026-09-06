package pdf

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

// yTolerance groups items onto the same line for merging.
const yTolerance float32 = 5.0

// mergeTextItems joins adjacent items on the same line into single items.
//
// Items are grouped by (page, y) within yTolerance, ordered within each group
// by x, then consecutive items sharing a font size and style are concatenated
// when they sit close enough horizontally. The result is ordered page-major,
// then top of page first.
func mergeTextItems(items []TextItem) []TextItem {
	if len(items) == 0 {
		return items
	}

	groups := groupItemsByBaseline(items)

	type orderedGroup struct {
		page          uint32
		y             float32
		items         []*TextItem
		preserveOrder bool
	}
	ordered := make([]orderedGroup, 0, len(groups))

	for _, g := range groups {
		rtl := isRTLText(g.items)
		// Some content streams backtrack to overlay fragments; sorting those
		// by x would scramble text the stream deliberately interleaved.
		preserve := !rtl && shouldPreserveStreamOrder(g.items)
		switch {
		case rtl:
			sort.SliceStable(g.items, func(i, j int) bool { return g.items[i].X > g.items[j].X })
		case !preserve:
			sort.SliceStable(g.items, func(i, j int) bool { return g.items[i].X < g.items[j].X })
		}
		ordered = append(ordered, orderedGroup{g.page, g.y, g.items, preserve})
	}

	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].page != ordered[j].page {
			return ordered[i].page < ordered[j].page
		}
		return ordered[i].y > ordered[j].y
	})

	var merged []TextItem
	for _, g := range ordered {
		merged = append(merged, mergeLine(g.items, g.preserveOrder)...)
	}
	return merged
}

type lineGroup struct {
	page  uint32
	y     float32
	items []*TextItem
}

// groupItemsByBaseline buckets items by page and baseline within yTolerance.
// This is the merge stage's own coarse grouping; the layout stage groups lines
// again later with a tighter tolerance and reading-order rules.
func groupItemsByBaseline(items []TextItem) []lineGroup {
	var groups []lineGroup
	for i := range items {
		it := &items[i]
		placed := false
		for g := range groups {
			if groups[g].page == it.Page && absF32(it.Y-groups[g].y) < yTolerance {
				groups[g].items = append(groups[g].items, it)
				placed = true
				break
			}
		}
		if !placed {
			groups = append(groups, lineGroup{page: it.Page, y: it.Y, items: []*TextItem{it}})
		}
	}
	return groups
}

// mergeLine concatenates mergeable runs within one ordered line group.
func mergeLine(group []*TextItem, preserveOrder bool) []TextItem {
	var out []TextItem

	for i := 0; i < len(group); {
		first := group[i]
		text := first.Text
		endX := first.X + effectiveMergeWidth(first)

		// Display type set with tracking needs a run-local space floor; the
		// fixed thresholds below would read every letter gap as a word break.
		trackedEnd, trackedFloor, tracked := -1, float32(0), false
		if !preserveOrder {
			trackedEnd, trackedFloor, tracked = trackedRunSpaceFloor(group, i)
		}

		j := i + 1
		for ; j < len(group); j++ {
			next := group[j]

			if absF32(next.FontSize-first.FontSize) > first.FontSize*0.20 {
				break
			}
			// Never merge across a style boundary: the merged item carries
			// first's flags, so absorbing a styled run into a plain neighbour
			// would silently erase the styling downstream emission needs.
			if next.IsBold != first.IsBold ||
				next.IsItalic != first.IsItalic ||
				next.IsUnderline != first.IsUnderline ||
				next.IsStrikeout != first.IsStrikeout {
				break
			}

			gap := next.X - endX
			xGapMax := first.FontSize * 0.5
			if preserveOrder && isStandaloneBullet(text) {
				xGapMax = first.FontSize * 1.2
			}
			if gap > xGapMax {
				break
			}
			if gap < -first.FontSize*0.5 && !preserveOrder {
				break
			}

			threshold := wordBoundaryThreshold(text, next.Text, first.FontSize)
			if tracked && j <= trackedEnd {
				threshold = trackedFloor
			}
			needsBulletSpace := preserveOrder &&
				isStandaloneBullet(text) &&
				strings.TrimSpace(next.Text) != ""

			if needsBulletSpace || gap > threshold {
				text += " "
			}
			text += next.Text

			nextEnd := next.X + effectiveMergeWidth(next)
			if preserveOrder {
				endX = maxF32(endX, nextEnd)
			} else {
				endX = nextEnd
			}
		}

		out = append(out, TextItem{
			Text:        text,
			X:           first.X,
			Y:           first.Y,
			Width:       endX - first.X,
			Height:      first.Height,
			Font:        first.Font,
			baseFont:    first.baseFont,
			FontSize:    first.FontSize,
			Page:        first.Page,
			IsBold:      first.IsBold,
			IsItalic:    first.IsItalic,
			IsUnderline: first.IsUnderline,
			IsStrikeout: first.IsStrikeout,
			Type:        first.Type,
			MCID:        first.MCID,
		})

		i = j
	}
	return out
}

// wordBoundaryThreshold is the gap above which a space is inserted between two
// items. The base 0.08em rises to 0.13em at lowercase-to-lowercase junctions,
// where Tc/Tw adjustments shift advance widths relative to Td positioning, and
// falls away entirely before joining punctuation.
func wordBoundaryThreshold(sofar, next string, fontSize float32) float32 {
	prevLast, hasPrev := lastRune(trimSpaceRight(sofar))
	nextFirst, hasNext := firstRune(trimSpaceLeft(next))

	if hasNext {
		switch nextFirst {
		case '.', ',', ';', ')', ']', '}':
			return fontSize * 0.25
		}
	}
	if hasPrev && hasNext && unicode.IsLower(prevLast) && unicode.IsLower(nextFirst) {
		return fontSize * 0.13
	}
	return fontSize * 0.08
}

// effectiveMergeWidth caps an item's width for gap computation, guarding
// against word-spacing inflation.
//
// Large Tw (used to justify text) extends the advance width of strings
// containing spaces well past the visible glyphs. That inflated width closes
// inter-column gaps and would merge items from different table columns.
func effectiveMergeWidth(item *TextItem) float32 {
	if item.Width <= 0 || item.FontSize <= 0 {
		return item.Width
	}
	// Tw only inflates strings that actually contain spaces.
	if !strings.Contains(item.Text, " ") {
		return item.Width
	}
	// CJK glyphs are naturally about one em wide, so the cap does not apply.
	for _, r := range item.Text {
		if IsCJK(r) {
			return item.Width
		}
	}

	n := float32(len([]rune(item.Text)))
	if n == 0 {
		return item.Width
	}
	// Proportional text averages ~0.5em per character and monospace ~0.6em,
	// so 0.85em signals inflation rather than genuinely wide glyphs.
	if avg := item.Width / n; avg > item.FontSize*0.85 {
		return minF32(n*item.FontSize*0.6, item.Width)
	}
	return item.Width
}

func isStandaloneBullet(text string) bool {
	switch strings.TrimSpace(text) {
	case "•", "○", "●", "◦":
		return true
	}
	return false
}

func isShortAlphaFragment(text string) bool {
	t := strings.TrimSpace(text)
	n := 0
	for _, r := range t {
		if !unicode.IsLetter(r) {
			return false
		}
		n++
	}
	return n >= 1 && n <= 4
}

// hasPhraseContinuationShape reports text that reads as the continuation of a
// phrase rather than a standalone fragment: it contains a space or hyphen
// early on.
func hasPhraseContinuationShape(text string) bool {
	for i, r := range []rune(trimSpaceLeft(text)) {
		if i >= 24 {
			break
		}
		if unicode.IsSpace(r) || r == '-' {
			return true
		}
	}
	return false
}

// isRTLText reports whether a line reads right-to-left, by majority vote of its
// strongly-directional characters.
func isRTLText(items []*TextItem) bool {
	var rtl, ltr int
	for _, it := range items {
		for _, r := range it.Text {
			switch {
			case IsRTL(r):
				rtl++
			case unicode.IsLetter(r) && !IsCJK(r):
				ltr++
			}
		}
	}
	return rtl > 0 && rtl > ltr
}

// shouldPreserveStreamOrder reports a line whose content stream deliberately
// backtracks to overlay fragments, where sorting by x would scramble the text.
//
// The signature is a tagged line of uniform size whose items cluster tightly,
// containing a backtrack that resumes inside an earlier item and reads as a
// phrase continuation (or follows a bullet).
func shouldPreserveStreamOrder(group []*TextItem) bool {
	if len(group) < 3 {
		return false
	}

	var first *TextItem
	for _, it := range group {
		if strings.TrimSpace(it.Text) != "" {
			first = it
			break
		}
	}
	if first == nil {
		return false
	}

	// Only tagged content interleaves this way.
	tagged := false
	for _, it := range group {
		if it.MCID != nil {
			tagged = true
			break
		}
	}
	if !tagged {
		return false
	}

	nonEmpty, nonSpaceChars, mathChars := 0, 0, 0
	maxFontSize := first.FontSize

	for _, it := range group {
		if strings.TrimSpace(it.Text) != "" {
			nonEmpty++
		}
		if absF32(it.FontSize-first.FontSize) > first.FontSize*0.25 {
			return false
		}
		maxFontSize = maxF32(maxFontSize, it.FontSize)
		for _, r := range it.Text {
			if unicode.IsSpace(r) {
				continue
			}
			nonSpaceChars++
			switch r {
			case '*', 'ˆ', '^', '=', '+', '_', '[', ']', '{', '}', '|', '<', '>':
				mathChars++
			}
		}
	}

	if nonEmpty < 2 {
		return false
	}
	// A symbol-dense line is set mathematics, not overlaid prose.
	if nonSpaceChars > 0 && mathChars*4 > nonSpaceChars {
		return false
	}

	byX := make([]*TextItem, len(group))
	copy(byX, group)
	sort.SliceStable(byX, func(i, j int) bool { return byX[i].X < byX[j].X })

	clusterStart := byX[0].X
	clusterEnd := clusterStart + effectiveMergeWidth(byX[0])
	for _, it := range byX[1:] {
		if it.X-clusterEnd > maxFontSize*2.5 {
			return false
		}
		clusterEnd = maxF32(clusterEnd, it.X+effectiveMergeWidth(it))
	}
	if clusterEnd-clusterStart > maxFontSize*36.0 {
		return false
	}

	for i := 0; i+1 < len(group); i++ {
		prev, next := group[i], group[i+1]
		fontSize := maxF32(prev.FontSize, next.FontSize)
		backtrack := fontSize * 0.25
		nextEnd := next.X + effectiveMergeWidth(next)

		// A backtrack that starts before the previous item and extends past
		// its start is an overlay, not a new column.
		if !(next.X < prev.X-backtrack && nextEnd > prev.X+backtrack) {
			continue
		}

		hasNearPrefix := false
		for k := i; k >= 0 && k > i-4; k-- {
			it := group[k]
			if isShortAlphaFragment(it.Text) &&
				it.X >= next.X-fontSize*0.5 && it.X <= next.X+fontSize*4.0 {
				hasNearPrefix = true
				break
			}
		}
		startsLower := false
		if r, ok := firstRune(trimSpaceLeft(next.Text)); ok {
			startsLower = unicode.IsLower(r)
		}
		if hasNearPrefix && startsLower && hasPhraseContinuationShape(next.Text) {
			return true
		}

		if hasNearBullet(group, i, next.X, fontSize) {
			return true
		}
	}
	return false
}

// hasNearBullet reports a bullet earlier in the line that the item at index
// idx+1 backtracks under, with only a short fragment in between.
func hasNearBullet(group []*TextItem, idx int, nextX, fontSize float32) bool {
	bulletIndex := -1
	for k := 0; k <= idx; k++ {
		if isStandaloneBullet(group[k].Text) && nextX <= group[k].X+fontSize*3.0 {
			bulletIndex = k
			break
		}
	}
	if bulletIndex < 0 || bulletIndex >= idx {
		return false
	}

	for k := idx; k > bulletIndex; k-- {
		t := strings.TrimSpace(group[k].Text)
		if t == "" {
			continue
		}
		return len([]rune(t)) <= 8 &&
			hasPhraseContinuationShape(group[idx+1].Text)
	}
	return false
}

// minTrackedGaps is the gap count above which a tracked run is judged by its
// median alone; shorter runs must additionally be uniform and all-caps.
const minTrackedGaps = 4

// trackedRunSpaceFloor detects a tracked (letter-spaced) run of single-glyph
// items starting at index start, and derives the gap above which a space
// belongs.
//
// Display type set with tracking renders one glyph per show operation. The
// merge loop's fixed thresholds would then read every letter gap as a word
// boundary and emit "H O W" for "HOW". Within such a run the gaps carry the
// signal: letter gaps cluster just above the fixed threshold while word gaps
// sit clearly higher, so the split point is the largest relative jump.
//
// Returns the run's last index and its space floor. An infinite floor means
// the whole run is one word.
func trackedRunSpaceFloor(group []*TextItem, start int) (end int, floor float32, ok bool) {
	first := group[start]
	if len([]rune(strings.TrimSpace(first.Text))) != 1 {
		return 0, 0, false
	}
	fs := first.FontSize
	if fs <= 0 {
		return 0, 0, false
	}

	// Walk the run under the same break conditions as the merge loop so the
	// returned index stays aligned with it.
	var gaps []float32
	endX := first.X + effectiveMergeWidth(first)
	end = start

	for offset, next := range group[start+1:] {
		if len([]rune(strings.TrimSpace(next.Text))) != 1 {
			break
		}
		if absF32(next.FontSize-fs) > fs*0.20 {
			break
		}
		if next.IsBold != first.IsBold ||
			next.IsItalic != first.IsItalic ||
			next.IsUnderline != first.IsUnderline ||
			next.IsStrikeout != first.IsStrikeout {
			break
		}
		gap := next.X - endX
		if gap > fs*0.5 || gap < -fs*0.5 {
			break
		}
		gaps = append(gaps, gap/fs)
		endX = next.X + effectiveMergeWidth(next)
		end = start + 1 + offset
	}
	if len(gaps) < 2 {
		return 0, 0, false
	}

	sorted := make([]float32, len(gaps))
	copy(sorted, gaps)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	median := sorted[len(sorted)/2]

	// Typographic convention gate: display tracking is an all-caps habit, and
	// Han/Kana never space between glyphs. Mixed- or lowercase Latin runs keep
	// their boundaries, because geometry alone cannot tell spaced singles
	// ("A b c d e") from a tracked title-case word ("B u f f a l o").
	spacelessCJK, allCaps := runScriptShape(group[start : end+1])
	if !spacelessCJK && !allCaps {
		return 0, 0, false
	}

	if len(gaps) >= minTrackedGaps {
		if median <= 0.075 {
			return 0, 0, false
		}
	} else {
		// Short runs demand a stricter shape, since a genuine sequence of
		// spaced single letters has the same gap count.
		uniform := sorted[len(sorted)-1] <= maxF32(sorted[0], 0.01)*1.4
		if median < 0.09 || !uniform {
			return 0, 0, false
		}
	}

	// Han/Kana take no inter-glyph spaces at all, so a nonuniform gap
	// distribution must not manufacture word boundaries.
	if spacelessCJK {
		return end, float32(math.Inf(1)), true
	}

	// Word gaps, when present, form a second mode above the letter-gap
	// cluster: split at the largest relative jump. A unimodal run is one word.
	bestJump := float32(1.0)
	floor = float32(math.Inf(1))
	for i := 0; i+1 < len(sorted); i++ {
		lo, hi := maxF32(sorted[i], 0.01), maxF32(sorted[i+1], 0.01)
		if jump := hi / lo; jump > bestJump {
			bestJump = jump
			floor = (lo + hi) / 2
		}
	}
	if bestJump < 1.4 {
		floor = float32(math.Inf(1))
	}
	return end, floor * fs, true
}

// runScriptShape classifies a tracked run's script: whether it is entirely
// spaceless CJK, and whether it is entirely uppercase.
func runScriptShape(items []*TextItem) (spacelessCJK, allCaps bool) {
	spacelessCJK, allCaps = true, true
	sawSpaceless := false

	for _, it := range items {
		for _, r := range strings.TrimSpace(it.Text) {
			if isSpacelessCJK(r) {
				sawSpaceless = true
			} else if isAlphanumeric(r) {
				spacelessCJK = false
			}
			if !unicode.IsUpper(r) && !IsCJK(r) && unicode.IsLetter(r) {
				allCaps = false
			}
		}
	}
	return spacelessCJK && sawSpaceless, allCaps
}

// isSpacelessCJK reports scripts written without inter-word spaces. Hangul is
// deliberately excluded: Korean does space between words, so a tracked Hangul
// run keeps normal word-boundary handling.
func isSpacelessCJK(r rune) bool {
	switch {
	case r >= 0x3000 && r <= 0x303F, // CJK Symbols and Punctuation
		r >= 0x3040 && r <= 0x309F, // Hiragana
		r >= 0x30A0 && r <= 0x30FF, // Katakana
		r >= 0x4E00 && r <= 0x9FFF, // CJK Unified Ideographs
		r >= 0xF900 && r <= 0xFAFF, // CJK Compatibility Ideographs
		r >= 0xFF00 && r <= 0xFFEF: // Halfwidth and Fullwidth Forms
		return true
	}
	return false
}

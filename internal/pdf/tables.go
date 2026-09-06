package pdf

import (
	"html"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// detectedTable is an internal, geometry-backed table. colEdges and rowEdges
// are physical cell boundaries; cells may contain another independently drawn
// grid. Markdown has no nested-table syntax, so nested grids render as compact
// HTML inside their owning cell.
type detectedTable struct {
	page        uint32
	colEdges    []float32
	rowEdges    []float32 // descending, top to bottom
	cells       [][]tableCell
	itemIndices []int
	x1, x2      float32
	yTop, yBot  float32
	confidence  float32
}

type tableCell struct {
	text        string
	itemIndices []int
	nested      []*detectedTable
	rowSpan     int
	colSpan     int
	covered     bool
}

type gridSegment struct {
	horizontal bool
	boxEdge    bool
	pos        float32
	lo, hi     float32
}

type borderlessVisualRow struct {
	y       float32
	indices []int
}

type borderlessGap struct{ lo, hi float32 }

func (s gridSegment) length() float32 { return s.hi - s.lo }

// detectPageTables runs strong vector-grid detection first, then an alignment
// fallback on text not claimed by a physical grid.
func detectPageTables(items []TextItem, rects []Rect, lines []Line, page uint32) []*detectedTable {
	segments := collectGridSegments(rects, lines, page)
	candidates := physicalTableCandidates(items, segments, page)
	candidates = append(candidates, sparseRuleTableCandidates(items, segments, page)...)
	tables := selectAndNestTables(candidates, items)

	claimed := make(map[int]bool)
	for _, table := range tables {
		for _, index := range table.itemIndices {
			claimed[index] = true
		}
	}
	for _, table := range detectBorderlessTables(items, page, claimed) {
		if tableOverlapsClaimed(table, claimed) {
			continue
		}
		tables = append(tables, table)
		for _, index := range table.itemIndices {
			claimed[index] = true
		}
	}

	sort.SliceStable(tables, func(i, j int) bool {
		if absF32(tables[i].yTop-tables[j].yTop) > 2 {
			return tables[i].yTop > tables[j].yTop
		}
		return tables[i].x1 < tables[j].x1
	})
	return tables
}

func collectGridSegments(rects []Rect, lines []Line, page uint32) []gridSegment {
	var raw []gridSegment
	for _, line := range lines {
		if line.Page != page {
			continue
		}
		dx, dy := absF32(line.X2-line.X1), absF32(line.Y2-line.Y1)
		switch {
		case dx >= 6 && dy <= maxF32(1.5, dx*0.04):
			raw = append(raw, gridSegment{horizontal: true, pos: (line.Y1 + line.Y2) / 2, lo: minF32(line.X1, line.X2), hi: maxF32(line.X1, line.X2)})
		case dy >= 6 && dx <= maxF32(1.5, dy*0.04):
			raw = append(raw, gridSegment{pos: (line.X1 + line.X2) / 2, lo: minF32(line.Y1, line.Y2), hi: maxF32(line.Y1, line.Y2)})
		}
	}

	type normalizedRect struct{ x, y, w, h float32 }
	var boxes []normalizedRect
	for _, rect := range rects {
		if rect.Page != page {
			continue
		}
		x, y, w, h := rect.X, rect.Y, rect.Width, rect.Height
		if w < 0 {
			x, w = x+w, -w
		}
		if h < 0 {
			y, h = y+h, -h
		}
		switch {
		case h <= 2.5 && w >= 6:
			raw = append(raw, gridSegment{horizontal: true, pos: y + h/2, lo: x, hi: x + w})
		case w <= 2.5 && h >= 6:
			raw = append(raw, gridSegment{pos: x + w/2, lo: y, hi: y + h})
		case w >= 5 && h >= 5:
			boxes = append(boxes, normalizedRect{x, y, w, h})
		}
	}

	// Filled cell backgrounds and stroked `re` paths both arrive as Rects.
	// Repeated boxes are strong cell evidence. A single outer rectangle is also
	// structural when several long internal row and column rules terminate at
	// it; this covers tables drawn as one `re` plus internal `l` segments.
	if len(boxes) > 0 {
		widths, heights := make([]float32, len(boxes)), make([]float32, len(boxes))
		for i, box := range boxes {
			widths[i], heights[i] = box.w, box.h
		}
		medianW, medianH := medianFloat32(widths), medianFloat32(heights)
		for _, box := range boxes {
			repeatedEvidence := len(boxes) >= 4 && !(medianW > 0 && box.w > medianW*10 || medianH > 0 && box.h > medianH*15)
			if !repeatedEvidence && !boxEnclosesGridRules(box, raw) {
				continue
			}
			raw = append(raw,
				gridSegment{horizontal: true, boxEdge: true, pos: box.y, lo: box.x, hi: box.x + box.w},
				gridSegment{horizontal: true, boxEdge: true, pos: box.y + box.h, lo: box.x, hi: box.x + box.w},
				gridSegment{boxEdge: true, pos: box.x, lo: box.y, hi: box.y + box.h},
				gridSegment{boxEdge: true, pos: box.x + box.w, lo: box.y, hi: box.y + box.h},
			)
		}
	}
	return mergeCollinearSegments(raw, 2.5, 3.0)
}

func boxEnclosesGridRules(box struct{ x, y, w, h float32 }, segments []gridSegment) bool {
	var vertical, horizontal []float32
	for _, segment := range segments {
		if segment.horizontal {
			if segment.pos <= box.y+2 || segment.pos >= box.y+box.h-2 {
				continue
			}
			overlap := minF32(segment.hi, box.x+box.w) - maxF32(segment.lo, box.x)
			if overlap >= box.w*.55 {
				horizontal = append(horizontal, segment.pos)
			}
			continue
		}
		if segment.pos <= box.x+2 || segment.pos >= box.x+box.w-2 {
			continue
		}
		overlap := minF32(segment.hi, box.y+box.h) - maxF32(segment.lo, box.y)
		if overlap >= box.h*.55 {
			vertical = append(vertical, segment.pos)
		}
	}
	return len(snapTableEdges(vertical, 3)) >= 2 && len(snapTableEdges(horizontal, 3)) >= 2
}

func mergeCollinearSegments(segments []gridSegment, posTolerance, gapTolerance float32) []gridSegment {
	sort.SliceStable(segments, func(i, j int) bool {
		if segments[i].horizontal != segments[j].horizontal {
			return segments[i].horizontal
		}
		if segments[i].pos != segments[j].pos {
			return segments[i].pos < segments[j].pos
		}
		return segments[i].lo < segments[j].lo
	})

	var out []gridSegment
	for start := 0; start < len(segments); {
		end := start + 1
		posSum := segments[start].pos
		for end < len(segments) && segments[end].horizontal == segments[start].horizontal && absF32(segments[end].pos-segments[start].pos) <= posTolerance {
			posSum += segments[end].pos
			end++
		}
		group := append([]gridSegment(nil), segments[start:end]...)
		sort.Slice(group, func(i, j int) bool { return group[i].lo < group[j].lo })
		pos := posSum / float32(len(group))
		cur := gridSegment{horizontal: group[0].horizontal, boxEdge: group[0].boxEdge, pos: pos, lo: group[0].lo, hi: group[0].hi}
		for _, next := range group[1:] {
			if next.lo <= cur.hi+gapTolerance {
				cur.hi = maxF32(cur.hi, next.hi)
				cur.boxEdge = cur.boxEdge && next.boxEdge
				continue
			}
			out = append(out, cur)
			cur = gridSegment{horizontal: next.horizontal, boxEdge: next.boxEdge, pos: pos, lo: next.lo, hi: next.hi}
		}
		out = append(out, cur)
		start = end
	}
	return out
}

func physicalTableCandidates(items []TextItem, segments []gridSegment, page uint32) []*detectedTable {
	if len(segments) < 6 {
		return nil
	}
	parent := make([]int, len(segments))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(x int) int {
		if parent[x] != x {
			parent[x] = find(parent[x])
		}
		return parent[x]
	}
	union := func(a, b int) {
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[rb] = ra
		}
	}
	for i := range segments {
		for j := i + 1; j < len(segments); j++ {
			if gridSegmentsTouch(segments[i], segments[j], 3) {
				union(i, j)
			}
		}
	}
	groups := map[int][]gridSegment{}
	for i, segment := range segments {
		groups[find(i)] = append(groups[find(i)], segment)
	}

	var candidates []*detectedTable
	for _, group := range groups {
		if table := tableFromSegmentGroup(items, group, page); table != nil {
			candidates = append(candidates, table)
		}
	}
	return candidates
}

// sparseRuleTableCandidates handles booktabs-style tables: a top rule, a
// header separator, and a bottom rule, but no vertical dividers. These are
// common in papers and cannot be recovered by connected-grid detection.
func sparseRuleTableCandidates(items []TextItem, segments []gridSegment, page uint32) []*detectedTable {
	type ruleGroup struct {
		lo, hi float32
		rules  []gridSegment
	}
	var groups []ruleGroup
	for _, segment := range segments {
		if !segment.horizontal || segment.boxEdge || segment.length() < 30 {
			continue
		}
		best := -1
		bestDistance := float32(0)
		for i := range groups {
			tolerance := maxF32(8, minF32(segment.length(), groups[i].hi-groups[i].lo)*0.06)
			distance := absF32(segment.lo-groups[i].lo) + absF32(segment.hi-groups[i].hi)
			if absF32(segment.lo-groups[i].lo) <= tolerance && absF32(segment.hi-groups[i].hi) <= tolerance && (best < 0 || distance < bestDistance) {
				best, bestDistance = i, distance
			}
		}
		if best < 0 {
			groups = append(groups, ruleGroup{lo: segment.lo, hi: segment.hi, rules: []gridSegment{segment}})
			continue
		}
		group := &groups[best]
		n := float32(len(group.rules))
		group.lo = (group.lo*n + segment.lo) / (n + 1)
		group.hi = (group.hi*n + segment.hi) / (n + 1)
		group.rules = append(group.rules, segment)
	}

	var tables []*detectedTable
	for _, group := range groups {
		if len(group.rules) < 3 || len(group.rules) > 101 {
			continue
		}
		sort.Slice(group.rules, func(i, j int) bool { return group.rules[i].pos > group.rules[j].pos })
		rowEdges := make([]float32, 0, len(group.rules))
		for _, rule := range group.rules {
			if len(rowEdges) == 0 || absF32(rowEdges[len(rowEdges)-1]-rule.pos) > 2.5 {
				rowEdges = append(rowEdges, rule.pos)
			}
		}
		if len(rowEdges) < 3 {
			continue
		}
		bandItems := sparseRuleBandItems(items, page, group.lo, group.hi, rowEdges)
		if len(bandItems) != len(rowEdges)-1 {
			continue
		}
		// Equal-width rules from separate tables form one horizontal group.
		// Empty bands split that group into independently scored hypotheses.
		for bandStart := 0; bandStart < len(bandItems); {
			for bandStart < len(bandItems) && len(bandItems[bandStart]) == 0 {
				bandStart++
			}
			bandEnd := bandStart
			for bandEnd < len(bandItems) && len(bandItems[bandEnd]) > 0 {
				bandEnd++
			}
			if bandEnd-bandStart < 2 {
				bandStart = bandEnd + 1
				continue
			}
			candidateRows := rowEdges[bandStart : bandEnd+1]
			candidateBands := bandItems[bandStart:bandEnd]
			if sparseRulesBelongToGrid(segments, group.lo, group.hi, candidateRows[0], candidateRows[len(candidateRows)-1]) {
				bandStart = bandEnd + 1
				continue
			}
			colEdges := sparseRuleColumnEdges(items, candidateBands, group.lo, group.hi)
			if len(colEdges) < 2 {
				bandStart = bandEnd + 1
				continue
			}
			// Repeated boxes around a single visual column expose many horizontal
			// edges but are not a one-column table. A two-band key/value construct
			// remains useful and is the only one-column sparse form we accept.
			if len(colEdges) == 2 && len(candidateRows) > 3 {
				bandStart = bandEnd + 1
				continue
			}
			// Rules establish trustworthy bounds, but booktabs-style tables do
			// not draw a rule for every body row. Reconstruct logical rows from
			// repeated text alignments before falling back to rule bands.
			table := alignedSparseRuleTable(items, candidateBands, group.lo, group.hi, candidateRows, page)
			if table == nil {
				table = buildTableFromEdges(items, colEdges, candidateRows, page, true)
			}
			if table != nil && tableCellDensity(table.cells) >= .38 && tableContentLooksDataLike(table.cells) {
				if table.confidence == 0 {
					table.confidence = .70 + .15*tableCellDensity(table.cells)
				}
				tables = append(tables, table)
			}
			bandStart = bandEnd + 1
		}
	}
	return tables
}

func alignedSparseRuleTable(items []TextItem, bands [][]int, x1, x2 float32, ruleRows []float32, page uint32) *detectedTable {
	var indices []int
	for _, band := range bands {
		indices = append(indices, band...)
	}
	rows, rowGaps, _ := makeBorderlessVisualRows(items, uniqueSortedInts(indices))
	if len(rows) < 3 {
		return nil
	}
	table := borderlessTableForRows(items, rows, rowGaps, page)
	if table == nil {
		return nil
	}
	table.colEdges[0] = x1
	table.colEdges[len(table.colEdges)-1] = x2
	table.rowEdges[0] = ruleRows[0]
	table.rowEdges[len(table.rowEdges)-1] = ruleRows[len(ruleRows)-1]
	table.x1, table.x2 = x1, x2
	table.yTop, table.yBot = ruleRows[0], ruleRows[len(ruleRows)-1]
	// Alignment plus a few horizontal rules is weaker evidence than a
	// connected row-and-column grid. Keep its score below the minimum score of
	// a physical grid so a denser, coarser sparse hypothesis cannot displace a
	// complete five-column table.
	table.confidence = .76 + .10*tableCellDensity(table.cells)
	return table
}

func sparseRulesBelongToGrid(segments []gridSegment, x1, x2, yTop, yBot float32) bool {
	var axes []float32
	height := yTop - yBot
	for _, segment := range segments {
		if segment.horizontal || segment.pos < x1-3 || segment.pos > x2+3 || segment.length() < height*.55 {
			continue
		}
		if segment.lo <= yBot+3 && segment.hi >= yTop-3 {
			axes = append(axes, segment.pos)
		}
	}
	return len(snapTableEdges(axes, 3)) >= 3
}

func sparseRuleBandItems(items []TextItem, page uint32, x1, x2 float32, rowEdges []float32) [][]int {
	bands := make([][]int, len(rowEdges)-1)
	for index := range items {
		item := &items[index]
		if item.Page != page || item.Type.Kind != KindText || trimSpace(item.Text) == "" {
			continue
		}
		cx := item.X + maxF32(item.Width, 0)/2
		if cx < x1-3 || cx > x2+3 {
			continue
		}
		for row := range bands {
			if item.Y <= rowEdges[row]+2 && item.Y >= rowEdges[row+1]-2 {
				bands[row] = append(bands[row], index)
				break
			}
		}
	}
	return bands
}

func sparseRuleColumnEdges(items []TextItem, bands [][]int, x1, x2 float32) []float32 {
	var best []int
	for _, band := range bands {
		indices := append([]int(nil), band...)
		sort.SliceStable(indices, func(i, j int) bool {
			if absF32(items[indices[i]].Y-items[indices[j]].Y) > 2.5 {
				return items[indices[i]].Y > items[indices[j]].Y
			}
			return items[indices[i]].X < items[indices[j]].X
		})
		for start := 0; start < len(indices); {
			end := start + 1
			for end < len(indices) && absF32(items[indices[end]].Y-items[indices[start]].Y) <= 2.5 {
				end++
			}
			if end-start > len(best) {
				best = append(best[:0], indices[start:end]...)
			}
			start = end
		}
	}
	if len(best) < 2 {
		return []float32{x1, x2}
	}
	centers := make([]float32, 0, len(best))
	for _, index := range best {
		item := &items[index]
		centers = append(centers, item.X+maxF32(item.Width, 0)/2)
	}
	centers = snapTableEdges(centers, 6)
	sort.Slice(centers, func(i, j int) bool { return centers[i] < centers[j] })
	if len(centers) < 2 || len(centers) > 25 {
		return []float32{x1, x2}
	}
	edges := make([]float32, 0, len(centers)+1)
	edges = append(edges, x1)
	for i := 1; i < len(centers); i++ {
		edges = append(edges, (centers[i-1]+centers[i])/2)
	}
	edges = append(edges, x2)
	return edges
}

func gridSegmentsTouch(a, b gridSegment, tolerance float32) bool {
	if a.horizontal == b.horizontal {
		return absF32(a.pos-b.pos) <= tolerance && a.lo <= b.hi+tolerance && b.lo <= a.hi+tolerance
	}
	h, v := a, b
	if !h.horizontal {
		h, v = b, a
	}
	return v.pos >= h.lo-tolerance && v.pos <= h.hi+tolerance && h.pos >= v.lo-tolerance && h.pos <= v.hi+tolerance
}

func tableFromSegmentGroup(items []TextItem, segments []gridSegment, page uint32) *detectedTable {
	var h, v []gridSegment
	minX, maxX := float32(0), float32(0)
	minY, maxY := float32(0), float32(0)
	set := false
	for _, segment := range segments {
		if segment.horizontal {
			h = append(h, segment)
			if !set {
				minX, maxX, minY, maxY, set = segment.lo, segment.hi, segment.pos, segment.pos, true
			} else {
				minX, maxX = minF32(minX, segment.lo), maxF32(maxX, segment.hi)
				minY, maxY = minF32(minY, segment.pos), maxF32(maxY, segment.pos)
			}
		} else {
			v = append(v, segment)
			if !set {
				minX, maxX, minY, maxY, set = segment.pos, segment.pos, segment.lo, segment.hi, true
			} else {
				minX, maxX = minF32(minX, segment.pos), maxF32(maxX, segment.pos)
				minY, maxY = minF32(minY, segment.lo), maxF32(maxY, segment.hi)
			}
		}
	}
	width, height := maxX-minX, maxY-minY
	if len(h) < 3 || len(v) < 3 || width < 30 || height < 15 {
		return nil
	}

	// Local decorative rules inside a cell must not become page-wide axes.
	// Besides rules spanning most of the component, accept a partial rule when
	// it intersects at least two long perpendicular axes. That is how grouped
	// headers and merged cells express a boundary that exists for only part of
	// the table.
	var longH, longV []gridSegment
	for _, segment := range h {
		if segment.length() >= width*0.55 {
			longH = append(longH, segment)
		}
	}
	for _, segment := range v {
		if segment.length() >= height*0.55 {
			longV = append(longV, segment)
		}
	}
	var xs, ys []float32
	for _, segment := range h {
		if segment.length() >= width*0.55 || (!segment.boxEdge && gridSegmentIntersectionCount(segment, longV) >= 2) {
			ys = append(ys, segment.pos)
		}
	}
	for _, segment := range v {
		if segment.length() >= height*0.55 || (!segment.boxEdge && gridSegmentIntersectionCount(segment, longH) >= 2) {
			xs = append(xs, segment.pos)
		}
	}
	xs = snapTableEdges(xs, 3)
	ys = snapTableEdges(ys, 3)
	if len(xs) < 3 || len(ys) < 3 || len(xs) > 26 || len(ys) > 101 {
		return nil
	}
	sort.Slice(xs, func(i, j int) bool { return xs[i] < xs[j] })
	sort.Slice(ys, func(i, j int) bool { return ys[i] > ys[j] })
	table := buildTableFromEdges(items, xs, ys, page, true)
	if table != nil {
		inferMergedTableCells(table, segments, items)
		table.confidence = .85 + .15*tableCellDensity(table.cells)
	}
	return table
}

func gridSegmentIntersectionCount(segment gridSegment, perpendicular []gridSegment) int {
	count := 0
	for _, other := range perpendicular {
		if segment.horizontal == other.horizontal {
			continue
		}
		if gridSegmentsTouch(segment, other, 3) {
			count++
		}
	}
	return count
}

func inferMergedTableCells(table *detectedTable, segments []gridSegment, items []TextItem) {
	rows, cols := len(table.cells), len(table.cells[0])
	if rows == 0 || cols == 0 {
		return
	}
	parent := make([]int, rows*cols)
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(x int) int {
		if parent[x] != x {
			parent[x] = find(parent[x])
		}
		return parent[x]
	}
	union := func(a, b int) {
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[rb] = ra
		}
	}
	for row := 0; row < rows; row++ {
		y := (table.rowEdges[row] + table.rowEdges[row+1]) / 2
		for col := 0; col+1 < cols; col++ {
			if !tableBoundaryCovers(segments, false, table.colEdges[col+1], y) {
				union(row*cols+col, row*cols+col+1)
			}
		}
	}
	for row := 0; row+1 < rows; row++ {
		for col := 0; col < cols; col++ {
			x := (table.colEdges[col] + table.colEdges[col+1]) / 2
			if !tableBoundaryCovers(segments, true, table.rowEdges[row+1], x) {
				union(row*cols+col, (row+1)*cols+col)
			}
		}
	}

	groups := make(map[int][]int)
	for i := range parent {
		groups[find(i)] = append(groups[find(i)], i)
	}
	for _, group := range groups {
		if len(group) < 2 {
			continue
		}
		minRow, maxRow, minCol, maxCol := rows, -1, cols, -1
		for _, index := range group {
			row, col := index/cols, index%cols
			minRow, maxRow = min(minRow, row), max(maxRow, row)
			minCol, maxCol = min(minCol, col), max(maxCol, col)
		}
		// Only rectangular unions map to valid HTML spans. Irregular missing
		// strokes are left as ordinary cells rather than inventing a shape.
		if len(group) != (maxRow-minRow+1)*(maxCol-minCol+1) {
			continue
		}
		owner := &table.cells[minRow][minCol]
		owner.rowSpan, owner.colSpan = maxRow-minRow+1, maxCol-minCol+1
		for _, index := range group {
			row, col := index/cols, index%cols
			if row == minRow && col == minCol {
				continue
			}
			cell := &table.cells[row][col]
			owner.itemIndices = append(owner.itemIndices, cell.itemIndices...)
			owner.nested = append(owner.nested, cell.nested...)
			cell.covered = true
			cell.text, cell.itemIndices, cell.nested = "", nil, nil
		}
		owner.itemIndices = uniqueSortedInts(owner.itemIndices)
		owner.text = joinTableCellItems(items, owner.itemIndices)
	}
}

func tableBoundaryCovers(segments []gridSegment, horizontal bool, position, point float32) bool {
	for _, segment := range segments {
		if segment.horizontal != horizontal || absF32(segment.pos-position) > 3 {
			continue
		}
		if point >= segment.lo-3 && point <= segment.hi+3 {
			return true
		}
	}
	return false
}

func buildTableFromEdges(items []TextItem, colEdges, rowEdges []float32, page uint32, strongGeometry bool) *detectedTable {
	if len(colEdges) < 2 || len(rowEdges) < 3 {
		return nil
	}
	rows, cols := len(rowEdges)-1, len(colEdges)-1
	cells := make([][]tableCell, rows)
	for r := range cells {
		cells[r] = make([]tableCell, cols)
	}
	var claimed []int
	for index := range items {
		item := &items[index]
		if item.Page != page || (item.Type.Kind != KindText && item.Type.Kind != KindFormField) || trimSpace(item.Text) == "" {
			continue
		}
		cx := item.X + item.Width/2
		if item.Width <= 0 {
			cx = item.X
		}
		col, row := -1, -1
		for c := 0; c < cols; c++ {
			left, right := colEdges[c], colEdges[c+1]
			if c == 0 {
				left -= 2
			}
			if c == cols-1 {
				right += 2
			}
			// Tolerance belongs only on the table's outer boundary. Applying
			// it to both sides of every internal edge creates overlapping
			// cells and assigns text just right of an edge to the left cell.
			if cx >= left && (cx < right || c == cols-1 && cx <= right) {
				col = c
				break
			}
		}
		for r := 0; r < rows; r++ {
			top, bottom := rowEdges[r], rowEdges[r+1]
			if r == 0 {
				top += 2
			}
			if r == rows-1 {
				bottom -= 2
			}
			if item.Y <= top && (item.Y > bottom || r == rows-1 && item.Y >= bottom) {
				row = r
				break
			}
		}
		if row >= 0 && col >= 0 {
			cells[row][col].itemIndices = append(cells[row][col].itemIndices, index)
			claimed = append(claimed, index)
		}
	}
	for r := range cells {
		for c := range cells[r] {
			cells[r][c].text = joinTableCellItems(items, cells[r][c].itemIndices)
		}
	}

	nonEmptyRows, nonEmptyCells := 0, 0
	columnsWithContent := make([]bool, cols)
	for _, row := range cells {
		rowHasContent := false
		for c, cell := range row {
			if cell.text != "" {
				rowHasContent = true
				nonEmptyCells++
				columnsWithContent[c] = true
			}
		}
		if rowHasContent {
			nonEmptyRows++
		}
	}
	contentColumns := 0
	for _, yes := range columnsWithContent {
		if yes {
			contentColumns++
		}
	}
	minimumContentColumns := 2
	if cols == 1 && strongGeometry {
		minimumContentColumns = 1
	}
	if nonEmptyRows < 2 || contentColumns < minimumContentColumns || len(claimed) < 4 {
		return nil
	}
	density := float32(nonEmptyCells) / float32(rows*cols)
	// A connected vector grid is explicit structural evidence and remains a
	// table even when almost every cell is empty. Keep the higher threshold for
	// alignment-only hypotheses, where sparse prose would otherwise be noisy.
	minDensity := float32(0.05)
	if !strongGeometry {
		minDensity = 0.35
	}
	if density < minDensity {
		return nil
	}

	claimed = uniqueSortedInts(claimed)
	return &detectedTable{
		page: page, colEdges: colEdges, rowEdges: rowEdges, cells: cells, itemIndices: claimed,
		x1: colEdges[0], x2: colEdges[len(colEdges)-1], yTop: rowEdges[0], yBot: rowEdges[len(rowEdges)-1],
	}
}

func joinTableCellItems(items []TextItem, indices []int) string {
	indices = append([]int(nil), indices...)
	sort.SliceStable(indices, func(i, j int) bool {
		a, b := items[indices[i]], items[indices[j]]
		if absF32(a.Y-b.Y) > 1.5 {
			return a.Y > b.Y
		}
		return a.X < b.X
	})
	var parts []string
	for _, index := range indices {
		if text := trimSpace(items[index].Text); text != "" {
			parts = append(parts, text)
		}
	}
	return normalizeTableCellText(strings.Join(parts, " "))
}

func normalizeTableCellText(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	for _, pair := range [][2]string{{"( ", "("}, {"[ ", "["}, {"{ ", "{"}, {" )", ")"}, {" ]", "]"}, {" }", "}"}} {
		text = strings.ReplaceAll(text, pair[0], pair[1])
	}
	return text
}

func selectAndNestTables(candidates []*detectedTable, items []TextItem) []*detectedTable {
	if len(candidates) == 0 {
		return nil
	}
	sort.SliceStable(candidates, func(i, j int) bool { return tableArea(candidates[i]) > tableArea(candidates[j]) })
	var roots []*detectedTable
	for _, candidate := range candidates {
		var immediateParent *detectedTable
		parentRow, parentCol := 0, 0
		for _, parent := range candidates {
			if parent == candidate || tableArea(parent) <= tableArea(candidate) {
				continue
			}
			if row, col, ok := containingTableCell(parent, candidate); ok {
				if immediateParent == nil || tableArea(parent) < tableArea(immediateParent) {
					immediateParent, parentRow, parentCol = parent, row, col
				}
			}
		}
		if immediateParent == nil {
			roots = append(roots, candidate)
			continue
		}
		// A one-column child adds no tabular relationship and is commonly a
		// bordered note or callout. Keep its text in the owning cell instead of
		// emitting a misleading table-inside-table wrapper.
		if len(candidate.cells) > 0 && len(candidate.cells[0]) == 1 {
			continue
		}
		cell := &immediateParent.cells[parentRow][parentCol]
		cell.nested = append(cell.nested, candidate)
		remove := make(map[int]bool, len(candidate.itemIndices))
		for _, index := range candidate.itemIndices {
			remove[index] = true
		}
		kept := cell.itemIndices[:0]
		for _, index := range cell.itemIndices {
			if !remove[index] {
				kept = append(kept, index)
			}
		}
		cell.itemIndices = kept
		cell.text = joinTableCellItems(items, kept)
	}

	// Competing hypotheses may overlap without true containment. Prefer the
	// hypothesis with stronger geometric evidence, then the one explaining
	// more items. This mirrors Camelot's accuracy/whitespace arbitration and
	// prevents a broad sparse-rule guess from displacing a complete grid.
	sort.SliceStable(roots, func(i, j int) bool {
		if absF32(roots[i].confidence-roots[j].confidence) > .001 {
			return roots[i].confidence > roots[j].confidence
		}
		if len(roots[i].itemIndices) != len(roots[j].itemIndices) {
			return len(roots[i].itemIndices) > len(roots[j].itemIndices)
		}
		return tableArea(roots[i]) < tableArea(roots[j])
	})
	var selected []*detectedTable
	claimed := map[int]bool{}
	for _, table := range roots {
		overlap := 0
		for _, index := range table.itemIndices {
			if claimed[index] {
				overlap++
			}
		}
		if overlap > len(table.itemIndices)/3 {
			continue
		}
		selected = append(selected, table)
		for _, index := range table.itemIndices {
			claimed[index] = true
		}
	}
	return selected
}

func containingTableCell(parent, child *detectedTable) (int, int, bool) {
	for r, row := range parent.cells {
		for c, cell := range row {
			if cell.covered {
				continue
			}
			rowSpan, colSpan := max(cell.rowSpan, 1), max(cell.colSpan, 1)
			if r+rowSpan >= len(parent.rowEdges) || c+colSpan >= len(parent.colEdges) {
				continue
			}
			if child.yTop <= parent.rowEdges[r]+2 && child.yBot >= parent.rowEdges[r+rowSpan]-2 &&
				child.x1 >= parent.colEdges[c]-2 && child.x2 <= parent.colEdges[c+colSpan]+2 {
				return r, c, true
			}
		}
	}
	return 0, 0, false
}

func tableArea(table *detectedTable) float32 {
	return maxF32(table.x2-table.x1, 0) * maxF32(table.yTop-table.yBot, 0)
}

func tableOverlapsClaimed(table *detectedTable, claimed map[int]bool) bool {
	for _, index := range table.itemIndices {
		if claimed[index] {
			return true
		}
	}
	return false
}

// detectBorderlessTables finds consecutive visual rows with stable whitespace
// separators. It intentionally demands data-like content, which prevents a
// normal pair of prose columns from becoming a table.
func detectBorderlessTables(items []TextItem, page uint32, blocked map[int]bool) []*detectedTable {
	var indices []int
	for i, item := range items {
		if item.Page == page && !blocked[i] && item.Type.Kind == KindText && trimSpace(item.Text) != "" {
			indices = append(indices, i)
		}
	}
	rows, rowGaps, anchor := makeBorderlessVisualRows(items, indices)

	var tables []*detectedTable
	for start := 0; start < len(rows); {
		for start < len(rows) && !anchor[start] {
			start++
		}
		if start >= len(rows) {
			break
		}
		end := start + 1
		anchorCount := 1
		for end < len(rows) {
			gapY := rows[end-1].y - rows[end].y
			fontScale := borderlessRowFontScale(items, rows[end-1], rows[end])
			if gapY > maxF32(26, fontScale*2.6) {
				break
			}
			if anchor[end] {
				anchorCount++
			}
			end++
		}
		if anchorCount >= 3 {
			if table := borderlessTableForRows(items, rows[start:end], rowGaps[start:end], page); table != nil {
				tables = append(tables, table)
			}
		}
		start = end
	}
	return tables
}

func makeBorderlessVisualRows(items []TextItem, indices []int) ([]borderlessVisualRow, [][]borderlessGap, []bool) {
	sort.SliceStable(indices, func(i, j int) bool {
		a, b := items[indices[i]], items[indices[j]]
		if absF32(a.Y-b.Y) > 2.5 {
			return a.Y > b.Y
		}
		return a.X < b.X
	})
	var rows []borderlessVisualRow
	for _, index := range indices {
		item := &items[index]
		if len(rows) == 0 || absF32(rows[len(rows)-1].y-item.Y) > 3 {
			rows = append(rows, borderlessVisualRow{y: item.Y, indices: []int{index}})
		} else {
			rows[len(rows)-1].indices = append(rows[len(rows)-1].indices, index)
		}
	}

	rowGaps := make([][]borderlessGap, len(rows))
	anchor := make([]bool, len(rows))
	for r := range rows {
		sort.SliceStable(rows[r].indices, func(i, j int) bool { return items[rows[r].indices[i]].X < items[rows[r].indices[j]].X })
		for i := 1; i < len(rows[r].indices); i++ {
			left, right := &items[rows[r].indices[i-1]], &items[rows[r].indices[i]]
			lo, hi := left.X+maxF32(left.Width, 0), right.X
			if hi-lo >= maxF32(maxF32(left.FontSize, right.FontSize)*1.2, 12) {
				rowGaps[r] = append(rowGaps[r], borderlessGap{lo, hi})
			}
		}
		anchor[r] = len(rowGaps[r]) > 0
	}
	return rows, rowGaps, anchor
}

func borderlessRowFontScale(items []TextItem, a, b borderlessVisualRow) float32 {
	fontScale := float32(0)
	for _, row := range []borderlessVisualRow{a, b} {
		for _, index := range row.indices {
			fontScale = maxF32(fontScale, items[index].FontSize)
		}
	}
	return fontScale
}

func borderlessTableForRows(items []TextItem, rows []borderlessVisualRow, rowGaps [][]borderlessGap, page uint32) *detectedTable {
	if len(rows) < 3 || len(rows) != len(rowGaps) {
		return nil
	}
	anchors := 0
	for _, gaps := range rowGaps {
		if len(gaps) > 0 {
			anchors++
		}
	}
	if anchors < 3 {
		return nil
	}

	// Cluster the midpoint of each whitespace separator. A real column gap is
	// repeated down the table; incidental word gaps are not.
	var midpoints []float32
	for _, gaps := range rowGaps {
		for _, gap := range gaps {
			midpoints = append(midpoints, (gap.lo+gap.hi)/2)
		}
	}
	midpoints = snapTableEdges(midpoints, 12)
	type supportedSeparator struct {
		x       float32
		support int
	}
	var separators []supportedSeparator
	minimumSupport := max(3, (anchors*3+4)/5)
	for _, x := range midpoints {
		support := 0
		for _, gaps := range rowGaps {
			for _, gap := range gaps {
				if x >= gap.lo-2 && x <= gap.hi+2 {
					support++
					break
				}
			}
		}
		if support >= minimumSupport {
			separators = append(separators, supportedSeparator{x, support})
		}
	}
	if len(separators) == 0 {
		return nil
	}
	sort.Slice(separators, func(i, j int) bool { return separators[i].x < separators[j].x })
	var xs []float32
	for _, separator := range separators {
		if len(xs) == 0 || separator.x-xs[len(xs)-1] > 18 {
			xs = append(xs, separator.x)
		} else if separator.support > 0 {
			xs[len(xs)-1] = (xs[len(xs)-1] + separator.x) / 2
		}
	}
	if len(xs) > 24 {
		return nil
	}

	minX, maxX := float32(0), float32(0)
	maxFont := float32(10)
	set := false
	for _, row := range rows {
		for _, index := range row.indices {
			item := &items[index]
			right := item.X + maxF32(item.Width, item.FontSize*0.3)
			if !set {
				minX, maxX, set = item.X, right, true
			} else {
				minX, maxX = minF32(minX, item.X), maxF32(maxX, right)
			}
			maxFont = maxF32(maxFont, item.FontSize)
		}
	}
	if !set || maxX-minX < 40 {
		return nil
	}
	colEdges := make([]float32, 0, len(xs)+2)
	if xAnchors := dominantTableXAnchors(items, rows); len(xAnchors) >= 3 && len(xAnchors) <= 25 {
		colEdges = append(colEdges, minX-2)
		for i := 1; i < len(xAnchors); i++ {
			colEdges = append(colEdges, (xAnchors[i-1]+xAnchors[i])/2)
		}
		colEdges = append(colEdges, maxX+2)
	} else {
		colEdges = append(colEdges, minX-2)
		for _, x := range xs {
			if x > minX+5 && x < maxX-5 {
				colEdges = append(colEdges, x)
			}
		}
		colEdges = append(colEdges, maxX+2)
	}
	if len(colEdges) < 3 {
		return nil
	}

	rowEdges := make([]float32, 0, len(rows)+1)
	rowEdges = append(rowEdges, rows[0].y+maxFont*.8)
	for i := 1; i < len(rows); i++ {
		rowEdges = append(rowEdges, (rows[i-1].y+rows[i].y)/2)
	}
	rowEdges = append(rowEdges, rows[len(rows)-1].y-maxFont*.7)
	table := buildTableFromEdges(items, colEdges, rowEdges, page, false)
	if table == nil {
		return nil
	}
	pruneEmptyBorderlessColumns(table)
	if !tableContentLooksDataLike(table.cells) {
		return nil
	}
	collapseBorderlessContinuationRows(table, items)
	table.confidence = .55 + .25*tableCellDensity(table.cells)
	return table
}

func pruneEmptyBorderlessColumns(table *detectedTable) {
	if len(table.cells) == 0 || len(table.cells[0]) <= 2 {
		return
	}
	cols := len(table.cells[0])
	keep := make([]int, 0, cols)
	for col := 0; col < cols; col++ {
		nonEmpty := false
		for _, row := range table.cells {
			cell := row[col]
			if cell.text != "" || len(cell.itemIndices) > 0 || len(cell.nested) > 0 {
				nonEmpty = true
				break
			}
		}
		if nonEmpty {
			keep = append(keep, col)
		}
	}
	if len(keep) < 2 || len(keep) == cols {
		return
	}
	for row := range table.cells {
		compacted := make([]tableCell, 0, len(keep))
		for _, col := range keep {
			compacted = append(compacted, table.cells[row][col])
		}
		table.cells[row] = compacted
	}
	edges := make([]float32, 0, len(keep)+1)
	edges = append(edges, table.colEdges[keep[0]])
	for _, col := range keep[1:] {
		edges = append(edges, table.colEdges[col])
	}
	edges = append(edges, table.colEdges[keep[len(keep)-1]+1])
	table.colEdges = edges
	table.x1, table.x2 = edges[0], edges[len(edges)-1]
}

func dominantTableXAnchors(items []TextItem, rows []borderlessVisualRow) []float32 {
	type cluster struct {
		sum   float32
		count int
	}
	var values []float32
	for _, row := range rows {
		for _, index := range row.indices {
			values = append(values, items[index].X)
		}
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	var clusters []cluster
	for _, value := range values {
		if len(clusters) == 0 {
			clusters = append(clusters, cluster{sum: value, count: 1})
			continue
		}
		last := &clusters[len(clusters)-1]
		if absF32(value-last.sum/float32(last.count)) > 6 {
			clusters = append(clusters, cluster{sum: value, count: 1})
		} else {
			last.sum += value
			last.count++
		}
	}
	minimum := max(3, len(rows)/10)
	var anchors []float32
	for _, cluster := range clusters {
		if cluster.count >= minimum {
			anchors = append(anchors, cluster.sum/float32(cluster.count))
		}
	}
	return anchors
}

// Borderless layouts position wrapped cell lines independently. A first-column
// value opens a logical record; a sufficiently separated second-column value
// opens a sub-record inside a vertically merged first column.
func collapseBorderlessContinuationRows(table *detectedTable, items []TextItem) {
	if len(table.cells) < 4 || len(table.cells[0]) < 3 {
		return
	}
	result := [][]tableCell{cloneTableRow(table.cells[0])}
	resultYs := []float32{table.rowEdges[0]}
	current := -1
	lastAnchorY := float32(0)
	var pending []tableCell
	for r := 1; r < len(table.cells); r++ {
		row := table.cells[r]
		y := tableRowBaseline(row, items, (table.rowEdges[r]+table.rowEdges[r+1])/2)
		previousY := tableRowBaseline(table.cells[r-1], items, (table.rowEdges[r-1]+table.rowEdges[r])/2)
		hasFirst := row[0].text != ""
		hasSecond := row[1].text != ""
		nonEmpty := 0
		for _, cell := range row {
			if cell.text != "" {
				nonEmpty++
			}
		}
		fontScale := tableRowFontScale(row, items)
		continuationGap := fontScale*1.8 + 1
		firstContinues := hasFirst && current >= 0 && lastAnchorY-y < continuationGap
		// A continuation line normally uses ordinary text leading. A larger
		// gap denotes another sparse record even when its leading cells are
		// empty; merging it upward would silently invent values for that row.
		separateSparseRow := current >= 0 && previousY-y > continuationGap
		// A populated leading cell plus values across more than half the columns
		// is a new record even at tight leading. Treating it as wrapped prose
		// silently folds every other row in compact financial tables.
		denseLeadingRow := hasFirst && nonEmpty*2 > len(row)
		anchor := denseLeadingRow || separateSparseRow || (hasFirst && !firstContinues) || (hasSecond && (current < 0 || lastAnchorY-y > 18))
		if anchor {
			copyRow := cloneTableRow(row)
			if len(pending) > 0 {
				mergeTableRows(copyRow, pending, items, true)
				pending = nil
			}
			result = append(result, copyRow)
			resultYs = append(resultYs, y)
			current = len(result) - 1
			lastAnchorY = y
			continue
		}
		if current < 0 {
			if pending == nil {
				pending = make([]tableCell, len(row))
			}
			mergeTableRows(pending, row, items, false)
			continue
		}
		mergeTableRows(result[current], row, items, false)
	}
	if len(pending) > 0 {
		result = append(result, pending)
		resultYs = append(resultYs, table.yBot)
	}
	if len(result) >= len(table.cells) || len(result) < 2 {
		return
	}
	for r := range result {
		for c := range result[r] {
			result[r][c].text = joinTableCellItems(items, result[r][c].itemIndices)
		}
	}
	table.cells = result
	table.rowEdges = append(resultYs, table.yBot)
}

func tableRowFontScale(row []tableCell, items []TextItem) float32 {
	fontScale := float32(0)
	for _, cell := range row {
		for _, index := range cell.itemIndices {
			fontScale = maxF32(fontScale, items[index].FontSize)
		}
	}
	return fontScale
}

func tableRowBaseline(row []tableCell, items []TextItem, fallback float32) float32 {
	y, set := fallback, false
	for _, cell := range row {
		for _, index := range cell.itemIndices {
			if !set || items[index].Y > y {
				y, set = items[index].Y, true
			}
		}
	}
	return y
}

func cloneTableRow(row []tableCell) []tableCell {
	out := make([]tableCell, len(row))
	for i, cell := range row {
		out[i] = cell
		out[i].itemIndices = append([]int(nil), cell.itemIndices...)
		out[i].nested = append([]*detectedTable(nil), cell.nested...)
	}
	return out
}

func mergeTableRows(target []tableCell, addition []tableCell, items []TextItem, prepend bool) {
	for c := range target {
		if c >= len(addition) {
			break
		}
		if prepend {
			target[c].itemIndices = append(append([]int(nil), addition[c].itemIndices...), target[c].itemIndices...)
		} else {
			target[c].itemIndices = append(target[c].itemIndices, addition[c].itemIndices...)
		}
		target[c].nested = append(target[c].nested, addition[c].nested...)
		target[c].text = joinTableCellItems(items, target[c].itemIndices)
	}
}

func tableToMarkdown(table *detectedTable) string {
	if table == nil || len(table.cells) == 0 || len(table.cells[0]) == 0 {
		return ""
	}
	if tableRequiresHTML(table) {
		return tableToHTML(table)
	}
	var b strings.Builder
	headerWritten := false
	for _, row := range table.cells {
		if tableRowEmpty(row) {
			continue
		}
		b.WriteByte('|')
		for _, cell := range row {
			b.WriteString(renderMarkdownTableCell(cell))
			b.WriteByte('|')
		}
		b.WriteByte('\n')
		if !headerWritten {
			b.WriteByte('|')
			for range row {
				b.WriteString("---|")
			}
			b.WriteByte('\n')
			headerWritten = true
		}
	}
	return b.String()
}

func tableRequiresHTML(table *detectedTable) bool {
	for _, row := range table.cells {
		for _, cell := range row {
			// Nested block-level HTML is not portable inside a Markdown pipe
			// cell. Render the containing table as HTML as well so the hierarchy
			// remains valid across Markdown implementations.
			if cell.rowSpan > 1 || cell.colSpan > 1 || len(cell.nested) > 0 {
				return true
			}
		}
	}
	return false
}

func tableRowEmpty(row []tableCell) bool {
	for _, cell := range row {
		if cell.text != "" || len(cell.nested) > 0 {
			return false
		}
	}
	return true
}

func renderMarkdownTableCell(cell tableCell) string {
	text := strings.ReplaceAll(cell.text, "\\", "\\\\")
	text = strings.ReplaceAll(text, "|", "\\|")
	if len(cell.nested) == 0 {
		return text
	}
	var parts []string
	if text != "" {
		parts = append(parts, text)
	}
	for _, nested := range cell.nested {
		parts = append(parts, tableToHTML(nested))
	}
	return strings.Join(parts, "<br>")
}

func tableToHTML(table *detectedTable) string {
	var b strings.Builder
	b.WriteString("<table>")
	for r, row := range table.cells {
		if tableRowEmpty(row) {
			continue
		}
		b.WriteString("<tr>")
		tag := "td"
		if r == 0 {
			tag = "th"
		}
		for _, cell := range row {
			if cell.covered {
				continue
			}
			b.WriteByte('<')
			b.WriteString(tag)
			if cell.rowSpan > 1 {
				b.WriteString(` rowspan="`)
				b.WriteString(strconv.Itoa(cell.rowSpan))
				b.WriteByte('"')
			}
			if cell.colSpan > 1 {
				b.WriteString(` colspan="`)
				b.WriteString(strconv.Itoa(cell.colSpan))
				b.WriteByte('"')
			}
			b.WriteByte('>')
			b.WriteString(html.EscapeString(cell.text))
			for _, nested := range cell.nested {
				b.WriteString(tableToHTML(nested))
			}
			b.WriteString("</")
			b.WriteString(tag)
			b.WriteByte('>')
		}
		b.WriteString("</tr>")
	}
	b.WriteString("</table>")
	return b.String()
}

func snapTableEdges(values []float32, tolerance float32) []float32 {
	if len(values) == 0 {
		return nil
	}
	values = append([]float32(nil), values...)
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	var out []float32
	for i := 0; i < len(values); {
		j, sum := i+1, values[i]
		for j < len(values) && values[j]-values[i] <= tolerance {
			sum += values[j]
			j++
		}
		out = append(out, sum/float32(j-i))
		i = j
	}
	return out
}

func medianFloat32(values []float32) float32 {
	if len(values) == 0 {
		return 0
	}
	values = append([]float32(nil), values...)
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	return values[len(values)/2]
}

func uniqueSortedInts(values []int) []int {
	sort.Ints(values)
	out := values[:0]
	for _, value := range values {
		if len(out) == 0 || out[len(out)-1] != value {
			out = append(out, value)
		}
	}
	return out
}

func tableContentLooksDataLike(cells [][]tableCell) bool {
	var total, numeric, compact, prose int
	for _, row := range cells {
		for _, cell := range row {
			text := trimSpace(cell.text)
			if text == "" {
				continue
			}
			total++
			words := strings.Fields(text)
			if len(words) <= 4 {
				compact++
			}
			if len(words) >= 9 && endsSentence(text) {
				prose++
			}
			if strings.IndexFunc(text, unicode.IsDigit) >= 0 {
				numeric++
			}
		}
	}
	if total == 0 {
		return false
	}
	return numeric*5 >= total || compact*2 >= total || prose*3 < total*2
}

func tableCellDensity(cells [][]tableCell) float32 {
	total, populated := 0, 0
	for _, row := range cells {
		for _, cell := range row {
			total++
			if trimSpace(cell.text) != "" || len(cell.nested) > 0 {
				populated++
			}
		}
	}
	if total == 0 {
		return 0
	}
	return float32(populated) / float32(total)
}

func endsSentence(text string) bool {
	r, ok := lastRune(text)
	return ok && (r == '.' || r == '!' || r == '?')
}

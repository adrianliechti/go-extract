package xlsx

import "sort"

type mergeEvent struct {
	row, left, right, delta int
}

// mergeCoverage sweeps authored merge ranges across the rows actually rendered.
// A range contributes at most four events, regardless of its area. Column
// differences keep updates constant-time, and the coverage scan is bounded by
// the existing output width. No absent worksheet coordinates are materialized.
type mergeCoverage struct {
	events  []mergeEvent
	next    int
	changes []int
	counts  []int
}

func newMergeCoverage(merges []struct {
	Ref string `xml:"ref,attr"`
}, minCol, maxCol int) *mergeCoverage {
	m := &mergeCoverage{
		changes: make([]int, maxCol-minCol+1),
		counts:  make([]int, maxCol-minCol),
	}
	add := func(left, top, right, bottom int) {
		// Range coordinates are 1-based; output bounds are 0-based and
		// half-open. Clip before allocating or scanning any columns.
		left, right = max(left, minCol+1), min(right, maxCol)
		if left > right || top > bottom {
			return
		}
		left, right = left-minCol-1, right-minCol
		m.events = append(m.events, mergeEvent{top, left, right, 1})
		if bottom < int(^uint(0)>>1) {
			m.events = append(m.events, mergeEvent{bottom + 1, left, right, -1})
		}
	}
	for _, merge := range merges {
		left, top, right, bottom, ok := parseRange(merge.Ref)
		if !ok {
			continue
		}
		// Keep only the top-left anchor visible (ECMA-376 §18.3.1.55).
		// Splitting here also preserves overlap semantics: an anchor can
		// still be a continuation of another malformed overlapping range.
		add(left+1, top, right, top)
		if top < bottom {
			add(left, top+1, right, bottom)
		}
	}
	sort.Slice(m.events, func(i, j int) bool { return m.events[i].row < m.events[j].row })
	return m
}

// advance updates coverage for a row. Call in nondecreasing worksheet order;
// repeated row numbers are allowed and do not apply an event twice.
func (m *mergeCoverage) advance(row int) {
	for m.next < len(m.events) && m.events[m.next].row <= row {
		event := m.events[m.next]
		m.changes[event.left] += event.delta
		m.changes[event.right] -= event.delta
		m.next++
	}
	count := 0
	for col := range m.counts {
		count += m.changes[col]
		m.counts[col] = count
	}
}

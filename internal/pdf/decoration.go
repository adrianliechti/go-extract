package pdf

import "sort"

const (
	maxDecorationThickness = float32(2.0)
	minDecorationOverlap   = float32(0.6)
)

type decorationRule struct {
	x1, x2, y float32
}

func (r decorationRule) width() float32 { return r.x2 - r.x1 }

// markTextDecorations correlates painted horizontal rules with text. PDF has
// no underline or strikeout text flag; both are separate path operations.
func markTextDecorations(items []TextItem, rects []Rect, lines []Line, page uint32) {
	rules := decorationRules(rects, lines, page)
	if len(rules) == 0 {
		return
	}
	tabular := make([]bool, len(rules))
	for i, rule := range rules {
		tabular[i] = isTabularDecorationRule(rule, rules, items, page)
	}

	for i := range items {
		item := &items[i]
		if item.Page != page || item.Type.Kind != KindText || trimSpace(item.Text) == "" || item.Width <= 0 {
			continue
		}
		for ruleIndex, rule := range rules {
			if tabular[ruleIndex] {
				continue
			}
			if ruleUnderlinesItem(rule, item) {
				item.IsUnderline = true
			}
			if ruleStrikesItem(rule, item) {
				item.IsStrikeout = true
			}
		}
	}
}

func decorationRules(rects []Rect, lines []Line, page uint32) []decorationRule {
	rules := make([]decorationRule, 0, len(rects)+len(lines))
	for _, line := range lines {
		if line.Page != page || absF32(line.Y1-line.Y2) > maxDecorationThickness ||
			(line.StrokeWidth > maxDecorationThickness && line.StrokeWidth != 0) {
			continue
		}
		x1, x2 := line.X1, line.X2
		if x2 < x1 {
			x1, x2 = x2, x1
		}
		if x2-x1 > 1 {
			rules = append(rules, decorationRule{x1: x1, x2: x2, y: (line.Y1 + line.Y2) / 2})
		}
	}
	for _, rect := range rects {
		if rect.Page != page || absF32(rect.Height) > maxDecorationThickness {
			continue
		}
		x1, x2 := rect.X, rect.X+rect.Width
		if x2 < x1 {
			x1, x2 = x2, x1
		}
		if x2-x1 > 1 {
			rules = append(rules, decorationRule{x1: x1, x2: x2, y: rect.Y + rect.Height/2})
		}
	}
	return rules
}

func ruleHorizontalOverlap(rule decorationRule, item *TextItem) float32 {
	return minF32(rule.x2, item.X+item.Width) - maxF32(rule.x1, item.X)
}

func ruleUnderlinesItem(rule decorationRule, item *TextItem) bool {
	below := maxF32(item.FontSize*0.72, 3)
	if rule.y < item.Y-below || rule.y > item.Y+1 {
		return false
	}
	return ruleHorizontalOverlap(rule, item) >= item.Width*minDecorationOverlap
}

func ruleStrikesItem(rule decorationRule, item *TextItem) bool {
	if rule.y < item.Y+item.FontSize*0.12 || rule.y > item.Y+item.FontSize*0.55 {
		return false
	}
	return ruleHorizontalOverlap(rule, item) >= item.Width*minDecorationOverlap
}

// isTabularDecorationRule rejects row separators: either the same horizontal
// span repeats at several y-levels, or one rule crosses several column-sized
// gaps between text items.
func isTabularDecorationRule(rule decorationRule, rules []decorationRule, items []TextItem, page uint32) bool {
	var levels []float32
	for _, other := range rules {
		minWidth := minF32(rule.width(), other.width())
		maxWidth := maxF32(rule.width(), other.width())
		if minWidth <= 1 || maxWidth/minWidth > 1.5 {
			continue
		}
		overlap := minF32(rule.x2, other.x2) - maxF32(rule.x1, other.x1)
		if overlap >= minWidth*0.8 {
			levels = append(levels, other.y)
		}
	}
	sort.Slice(levels, func(i, j int) bool { return levels[i] < levels[j] })
	uniqueLevels := 0
	last := float32(0)
	for _, y := range levels {
		if uniqueLevels == 0 || absF32(y-last) > 2 {
			uniqueLevels++
			last = y
		}
	}
	if uniqueLevels >= 3 {
		return true
	}

	var matched []*TextItem
	for i := range items {
		item := &items[i]
		if item.Page == page && item.Type.Kind == KindText && ruleUnderlinesItem(rule, item) {
			matched = append(matched, item)
		}
	}
	if len(matched) < 3 {
		return false
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].X < matched[j].X })
	largeGaps := 0
	for i := 1; i < len(matched); i++ {
		gap := matched[i].X - (matched[i-1].X + matched[i-1].Width)
		fontSize := maxF32(matched[i].FontSize, matched[i-1].FontSize)
		if gap > fontSize*2 {
			largeGaps++
		}
	}
	return largeGaps >= 2
}

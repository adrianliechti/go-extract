package docx

import (
	"strconv"

	"github.com/adrianliechti/go-extract/internal/ooxml/opc"
)

// numbering resolves a paragraph's numbering id and indent level to a list
// format. WordprocessingML indirects twice: w:num maps a numId to an
// abstractNumId, and w:abstractNum holds the per-level formats.
type numbering struct {
	styles     *styleTable
	styleLinks map[string]string
	// numToAbstract maps numId -> abstractNumId.
	numToAbstract map[string]string
	// levels maps abstractNumId -> level -> format.
	levels map[string]map[int]numLevel
	// overrides maps numId -> level -> format, taking precedence over the
	// abstract definition.
	overrides map[string]map[int]numLevel
	starts    map[string]map[int]int
}

type numLevel struct {
	Format  string // decimal, bullet, lowerLetter, ...
	Start   int
	Restart int // one-based ancestor threshold; zero means never restart
}

type xmlNumLevel struct {
	ILvl    string `xml:"ilvl,attr"`
	Start   *val   `xml:"start"`
	NumFmt  *val   `xml:"numFmt"`
	Restart *val   `xml:"lvlRestart"`
}

func (lv xmlNumLevel) definition(index int) numLevel {
	restart := intOr(lv.Restart, index)
	if restart < 0 || restart > index {
		restart = index
	}
	return numLevel{Format: valOr(lv.NumFmt, "decimal"), Start: listStart(lv.Start), Restart: restart}
}

func listStart(value *val) int {
	start := intOr(value, 1)
	if start < 0 {
		return 1
	}
	return start
}

type xmlNumbering struct {
	AbstractNums []struct {
		ID        string        `xml:"abstractNumId,attr"`
		StyleLink *val          `xml:"numStyleLink"`
		Levels    []xmlNumLevel `xml:"lvl"`
	} `xml:"abstractNum"`
	Nums []struct {
		ID          string `xml:"numId,attr"`
		AbstractNum *val   `xml:"abstractNumId"`
		Overrides   []struct {
			ILvl  string       `xml:"ilvl,attr"`
			Lvl   *xmlNumLevel `xml:"lvl"`
			Start *val         `xml:"startOverride"`
		} `xml:"lvlOverride"`
	} `xml:"num"`
}

func loadNumbering(pkg *opc.Package, mainPart string) *numbering {
	n := &numbering{
		numToAbstract: map[string]string{},
		styleLinks:    map[string]string{},
		levels:        map[string]map[int]numLevel{},
		overrides:     map[string]map[int]numLevel{},
		starts:        map[string]map[int]int{},
	}

	part := pkg.RelatedPart(mainPart, opc.RelNumbering, "word/numbering.xml")
	if part == "" {
		return n
	}

	var x xmlNumbering
	if err := unmarshalPart(pkg, part, &x); err != nil {
		return n
	}

	for _, an := range x.AbstractNums {
		if an.StyleLink != nil {
			n.styleLinks[an.ID] = an.StyleLink.Val
		}
		levels := map[int]numLevel{}
		for _, lv := range an.Levels {
			idx, err := strconv.Atoi(lv.ILvl)
			if err != nil || idx < 0 || idx > 8 {
				continue
			}
			levels[idx] = lv.definition(idx)
		}
		n.levels[an.ID] = levels
	}

	for _, num := range x.Nums {
		if num.AbstractNum != nil {
			n.numToAbstract[num.ID] = num.AbstractNum.Val
		}
		for _, ov := range num.Overrides {
			idx, err := strconv.Atoi(ov.ILvl)
			if err != nil || idx < 0 || idx > 8 {
				continue
			}
			if ov.Start != nil {
				if n.starts[num.ID] == nil {
					n.starts[num.ID] = map[int]int{}
				}
				n.starts[num.ID][idx] = listStart(ov.Start)
			}
			if ov.Lvl == nil {
				continue
			}
			if n.overrides[num.ID] == nil {
				n.overrides[num.ID] = map[int]numLevel{}
			}
			n.overrides[num.ID][idx] = ov.Lvl.definition(idx)
		}
	}
	return n
}

// format reports whether a list level is ordered, and the number it starts at.
// An unknown numId defaults to an unordered list, which renders acceptably
// either way and avoids inventing numbers.
func (n *numbering) format(numID string, level int) (ordered bool, start int) {
	lv, ok := n.lookup(numID, level)
	if !ok {
		return false, 1
	}
	start = lv.Start
	if override, ok := n.starts[numID][level]; ok {
		start = override
	}
	return lv.Format != "bullet" && lv.Format != "none" && lv.Format != "", start
}

// ECMA-376 §17.9.21: numStyleLink delegates level definitions through a
// numbering style's numId. Resolve each hop with its overrides, nearest first;
// malformed cycles and dangling links degrade to the existing unordered fallback.
func (n *numbering) lookup(numID string, level int) (numLevel, bool) {
	seen := map[string]bool{}
	var start *int
	for numID != "" && len(seen) < 16 && !seen[numID] {
		seen[numID] = true
		if value, ok := n.starts[numID][level]; ok && start == nil {
			start = &value
		}
		lv, ok := n.overrides[numID][level]
		abstract := n.numToAbstract[numID]
		if !ok {
			lv, ok = n.levels[abstract][level]
		}
		if ok {
			if start != nil {
				lv.Start = *start
			}
			return lv, true
		}
		if n.styles == nil {
			break
		}
		style := n.styles.byID[n.styleLinks[abstract]]
		if style == nil || (style.Type != "" && style.Type != "numbering") {
			break
		}
		props := n.styles.numbering(style.ID)
		if props.NumID == nil {
			break
		}
		numID = props.NumID.Val
	}
	return numLevel{}, false
}

func valOr(v *val, def string) string {
	if v == nil || v.Val == "" {
		return def
	}
	return v.Val
}

func intOr(v *val, def int) int {
	if v == nil {
		return def
	}
	n, err := strconv.Atoi(v.Val)
	if err != nil {
		return def
	}
	return n
}

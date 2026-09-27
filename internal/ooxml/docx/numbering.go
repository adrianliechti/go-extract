package docx

import (
	"strconv"

	"github.com/adrianliechti/go-extract/internal/ooxml/opc"
)

// numbering resolves a paragraph's numbering id and indent level to a list
// format. WordprocessingML indirects twice: w:num maps a numId to an
// abstractNumId, and w:abstractNum holds the per-level formats.
type numbering struct {
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
		ID     string        `xml:"abstractNumId,attr"`
		Levels []xmlNumLevel `xml:"lvl"`
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

func (n *numbering) lookup(numID string, level int) (numLevel, bool) {
	if ov, ok := n.overrides[numID]; ok {
		if lv, ok := ov[level]; ok {
			return lv, true
		}
	}
	abstract, ok := n.numToAbstract[numID]
	if !ok {
		return numLevel{}, false
	}
	levels, ok := n.levels[abstract]
	if !ok {
		return numLevel{}, false
	}
	lv, ok := levels[level]
	return lv, ok
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

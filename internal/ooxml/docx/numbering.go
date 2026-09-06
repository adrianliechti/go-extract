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
}

type numLevel struct {
	Format string // decimal, bullet, lowerLetter, ...
	Start  int
}

type xmlNumbering struct {
	AbstractNums []struct {
		ID     string `xml:"abstractNumId,attr"`
		Levels []struct {
			ILvl   string `xml:"ilvl,attr"`
			Start  *val   `xml:"start"`
			NumFmt *val   `xml:"numFmt"`
		} `xml:"lvl"`
	} `xml:"abstractNum"`
	Nums []struct {
		ID          string `xml:"numId,attr"`
		AbstractNum *val   `xml:"abstractNumId"`
		Overrides   []struct {
			ILvl string `xml:"ilvl,attr"`
			Lvl  *struct {
				Start  *val `xml:"start"`
				NumFmt *val `xml:"numFmt"`
			} `xml:"lvl"`
		} `xml:"lvlOverride"`
	} `xml:"num"`
}

func loadNumbering(pkg *opc.Package, mainPart string) *numbering {
	n := &numbering{
		numToAbstract: map[string]string{},
		levels:        map[string]map[int]numLevel{},
		overrides:     map[string]map[int]numLevel{},
	}

	part := relatedPart(pkg, mainPart, opc.RelNumbering, "word/numbering.xml")
	if part == "" {
		return n
	}

	var x xmlNumbering
	if err := pkg.UnmarshalPart(part, &x); err != nil {
		return n
	}

	for _, an := range x.AbstractNums {
		levels := map[int]numLevel{}
		for _, lv := range an.Levels {
			idx, err := strconv.Atoi(lv.ILvl)
			if err != nil {
				continue
			}
			levels[idx] = numLevel{
				Format: valOr(lv.NumFmt, "decimal"),
				Start:  intOr(lv.Start, 1),
			}
		}
		n.levels[an.ID] = levels
	}

	for _, num := range x.Nums {
		if num.AbstractNum != nil {
			n.numToAbstract[num.ID] = num.AbstractNum.Val
		}
		for _, ov := range num.Overrides {
			if ov.Lvl == nil {
				continue
			}
			idx, err := strconv.Atoi(ov.ILvl)
			if err != nil {
				continue
			}
			if n.overrides[num.ID] == nil {
				n.overrides[num.ID] = map[int]numLevel{}
			}
			n.overrides[num.ID][idx] = numLevel{
				Format: valOr(ov.Lvl.NumFmt, "decimal"),
				Start:  intOr(ov.Lvl.Start, 1),
			}
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
	switch lv.Format {
	case "bullet", "none", "":
		return false, 1
	}
	if lv.Start < 1 {
		return true, 1
	}
	return true, lv.Start
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

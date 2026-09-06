package docx

import (
	"encoding/xml"
	"io"
	"strconv"
)

// The XML types below cover the WordprocessingML subset that carries visible
// content. Attributes are matched on local name only, because producers vary
// in which namespace prefix they bind.

type val struct {
	Val string `xml:"val,attr"`
}

// paragraph is a w:p element. Its inline children are decoded through a
// custom unmarshaller because runs and hyperlinks interleave, and struct tags
// alone would group them by type and lose document order.
type paragraph struct {
	PPr     *paraProps
	Content []inline
}

// inline is one ordered piece of paragraph content: exactly one field is set.
type inline struct {
	Run  *run
	Link *hyperlink
	Math *mathRun
}

// mathRun is an equation rendered to LaTeX. display marks block-level maths,
// which Word wraps in m:oMathPara.
type mathRun struct {
	latex   string
	display bool
}

// UnmarshalXML walks a w:p element, preserving the order of its children.
// Wrapper elements that are visible in Word's final view — smartTag, ins,
// moveTo, bookmarks, and content controls — are descended into so their runs
// appear inline. Deleted and moved-from content is skipped.
func (p *paragraph) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	for {
		tok, err := d.Token()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		switch t := tok.(type) {
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return nil
			}
		case xml.StartElement:
			switch t.Name.Local {
			case "pPr":
				var pr paraProps
				if err := d.DecodeElement(&pr, &t); err == nil {
					p.PPr = &pr
				}
			case "r":
				var r run
				if err := d.DecodeElement(&r, &t); err == nil {
					p.Content = append(p.Content, inline{Run: &r})
				}
			case "hyperlink":
				var h hyperlink
				if err := d.DecodeElement(&h, &t); err == nil {
					p.Content = append(p.Content, inline{Link: &h})
				}
			case "oMath", "oMathPara":
				// Equations sit beside runs at paragraph level. Rendering to
				// LaTeX preserves structure that raw m:t text would lose.
				if latex, display := parseOMML(d, t); latex != "" {
					p.Content = append(p.Content, inline{Math: &mathRun{latex: latex, display: display}})
				}

			case "smartTag", "ins", "moveTo", "sdt", "sdtContent", "bdo", "dir":
				// Transparent wrappers: keep walking at this level so nested
				// runs stay in order.
				continue
			case "del", "moveFrom":
				// ECMA-376 §17.13.5: deleted text and the source side of a
				// tracked move are not visible in the document's final view.
				if err := d.Skip(); err != nil {
					return err
				}
			}
		}
	}
}

type paraProps struct {
	PStyle     *val    `xml:"pStyle"`
	NumPr      *numPr  `xml:"numPr"`
	Ind        *indent `xml:"ind"`
	OutlineLvl *val    `xml:"outlineLvl"`
}

type indent struct {
	Left      string `xml:"left,attr"`
	LeftChars string `xml:"leftChars,attr"`
}

type numPr struct {
	ILvl  *val `xml:"ilvl"`
	NumID *val `xml:"numId"`
}

type empty struct{}

type text struct {
	Space string `xml:"space,attr"`
	Value string `xml:",chardata"`
}

type runProps struct {
	Bold      *onOff  `xml:"b"`
	Italic    *onOff  `xml:"i"`
	Underline *val    `xml:"u"`
	Strike    *onOff  `xml:"strike"`
	VertAlign *val    `xml:"vertAlign"`
	Style     *val    `xml:"rStyle"`
	RFonts    *rFonts `xml:"rFonts"`
}

type rFonts struct {
	ASCII string `xml:"ascii,attr"`
}

// onOff is a w:b-style toggle. An absent val attribute means "on".
type onOff struct {
	Val string `xml:"val,attr"`
}

func (o *onOff) on() bool {
	if o == nil {
		return false
	}
	switch o.Val {
	case "", "1", "true", "on":
		return true
	}
	return false
}

// hyperlink is a w:hyperlink element.
type hyperlink struct {
	ID     string `xml:"id,attr"`
	Anchor string `xml:"anchor,attr"`
	Runs   []run  `xml:"r"`
}

// ── tables ───────────────────────────────────────────────────────────

type table struct {
	Rows []tableRow `xml:"tr"`
}

type tableRow struct {
	Cells []tableCell `xml:"tc"`
}

type tableCell struct {
	TcPr   *tcProps
	Blocks []block
}

// A cell's paragraphs and nested tables interleave just like body blocks.
// Separate slices would move every nested table after the cell's final text.
func (c *tableCell) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	return walkChildren(d, start, func(t xml.StartElement) (bool, error) {
		switch t.Name.Local {
		case "tcPr":
			var pr tcProps
			if err := d.DecodeElement(&pr, &t); err != nil {
				return true, err
			}
			c.TcPr = &pr
		case "p":
			var p paragraph
			if err := d.DecodeElement(&p, &t); err != nil {
				return true, err
			}
			c.Blocks = append(c.Blocks, block{Para: &p})
		case "tbl":
			var tbl table
			if err := d.DecodeElement(&tbl, &t); err != nil {
				return true, err
			}
			c.Blocks = append(c.Blocks, block{Table: &tbl})
		default:
			// Match the previous struct decoder's treatment of unknown
			// children; only direct cell blocks belong to this sequence.
			return true, d.Skip()
		}
		return true, nil
	})
}

type tcProps struct {
	GridSpan *val    `xml:"gridSpan"`
	VMerge   *vMerge `xml:"vMerge"`
}

// vMerge marks vertical merges. val="restart" begins a merge; an absent val
// continues the merge from the row above.
type vMerge struct {
	Val string `xml:"val,attr"`
}

func (c *tableCell) gridSpan() int {
	if c.TcPr == nil || c.TcPr.GridSpan == nil {
		return 1
	}
	n, err := strconv.Atoi(c.TcPr.GridSpan.Val)
	if err != nil || n < 1 {
		return 1
	}
	if n > 64 {
		n = 64 // guard against absurd values in malformed documents
	}
	return n
}

func (c *tableCell) isVerticalContinuation() bool {
	if c.TcPr == nil || c.TcPr.VMerge == nil {
		return false
	}
	return c.TcPr.VMerge.Val != "restart"
}

func (p *paragraph) styleID() string {
	if p.PPr == nil || p.PPr.PStyle == nil {
		return ""
	}
	return p.PPr.PStyle.Val
}

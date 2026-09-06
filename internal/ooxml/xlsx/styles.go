package xlsx

import (
	"strings"

	"github.com/adrianliechti/go-extract/internal/ooxml/opc"
)

// cellStyles resolves a cell's style index to its character formatting.
// SpreadsheetML indirects twice: a cell's s attribute indexes cellXfs, and
// each xf names a font in the fonts table.
type cellStyles struct {
	// bold, italic and formats are indexed by cellXfs position.
	bold    []bool
	italic  []bool
	formats []numFormat

	// date1904 selects the 1904 date system, used by workbooks authored on
	// classic Mac Excel.
	date1904 bool
}

type xmlStyleSheet struct {
	Fonts []struct {
		B *struct{} `xml:"b"`
		I *struct{} `xml:"i"`
	} `xml:"fonts>font"`
	NumFmts []struct {
		ID   string `xml:"numFmtId,attr"`
		Code string `xml:"formatCode,attr"`
	} `xml:"numFmts>numFmt"`
	CellXfs []struct {
		FontID    string `xml:"fontId,attr"`
		NumFmtID  string `xml:"numFmtId,attr"`
		ApplyFont string `xml:"applyFont,attr"`
	} `xml:"cellXfs>xf"`
}

func loadStyles(pkg *opc.Package, mainPart string) *cellStyles {
	s := &cellStyles{}

	part := ""
	for _, rel := range pkg.Rels(mainPart) {
		if strings.HasSuffix(rel.Type, "/styles") {
			if t := rel.Resolve(); pkg.Has(t) {
				part = t
				break
			}
		}
	}
	if part == "" {
		if !pkg.Has("xl/styles.xml") {
			return s
		}
		part = "xl/styles.xml"
	}

	var ss xmlStyleSheet
	if err := pkg.UnmarshalPart(part, &ss); err != nil {
		return s
	}

	fontBold := make([]bool, len(ss.Fonts))
	fontItalic := make([]bool, len(ss.Fonts))
	for i, f := range ss.Fonts {
		fontBold[i] = f.B != nil
		fontItalic[i] = f.I != nil
	}

	// Custom format codes override the reserved ids they reuse.
	codes := make(map[int]string, len(ss.NumFmts))
	for _, nf := range ss.NumFmts {
		if id := atoiSafe(nf.ID); id >= 0 {
			codes[id] = nf.Code
		}
	}

	s.bold = make([]bool, len(ss.CellXfs))
	s.italic = make([]bool, len(ss.CellXfs))
	s.formats = make([]numFormat, len(ss.CellXfs))
	for i, xf := range ss.CellXfs {
		if id := atoiSafe(xf.FontID); id >= 0 && id < len(fontBold) {
			s.bold[i] = fontBold[id]
			s.italic[i] = fontItalic[id]
		}
		if id := atoiSafe(xf.NumFmtID); id >= 0 {
			s.formats[i] = classifyFormat(id, codes[id])
		}
	}
	return s
}

// numberFormat returns the format governing a cell's style index.
func (s *cellStyles) numberFormat(styleIndex string) numFormat {
	i := atoiSafe(styleIndex)
	if i < 0 || i >= len(s.formats) {
		return numFormat{}
	}
	return s.formats[i]
}

// renderValue applies a cell's number format to its stored value.
func (s *cellStyles) renderValue(styleIndex, raw string) string {
	return s.numberFormat(styleIndex).render(raw, s.date1904)
}

// format wraps text in the emphasis markers a cell's style calls for. An
// out-of-range index yields the text unchanged, which is what unstyled cells
// and malformed workbooks both need.
func (s *cellStyles) format(styleIndex string, text string) string {
	if text == "" || styleIndex == "" {
		return text
	}
	i := atoiSafe(styleIndex)
	if i < 0 {
		return text
	}

	bold := i < len(s.bold) && s.bold[i]
	italic := i < len(s.italic) && s.italic[i]

	if bold {
		text = "**" + text + "**"
	}
	if italic {
		text = "*" + text + "*"
	}
	return text
}

func atoiSafe(s string) int {
	if s == "" {
		return -1
	}
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return -1
		}
		n = n*10 + int(s[i]-'0')
		if n > 1<<20 {
			return -1
		}
	}
	return n
}

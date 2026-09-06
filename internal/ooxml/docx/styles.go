package docx

import (
	"strconv"
	"strings"

	"github.com/adrianliechti/go-extract/internal/ooxml/opc"
)

// styleTable resolves paragraph style ids to their semantic meaning: heading
// level, quote, or list membership. Styles form an inheritance chain through
// basedOn, so lookups walk upward until a definition is found.
type styleTable struct {
	byID map[string]*style
}

type style struct {
	ID      string
	Name    string
	BasedOn string
	// OutlineLevel is the 0-based w:outlineLvl, where 0 means Heading 1.
	OutlineLevel int
	HasOutline   bool
	NumID        string
	NumLevel     int
	HasNum       bool
}

type xmlStyles struct {
	Styles []struct {
		StyleID string `xml:"styleId,attr"`
		Type    string `xml:"type,attr"`
		Name    *val   `xml:"name"`
		BasedOn *val   `xml:"basedOn"`
		PPr     *struct {
			OutlineLvl *val   `xml:"outlineLvl"`
			NumPr      *numPr `xml:"numPr"`
		} `xml:"pPr"`
	} `xml:"style"`
}

func loadStyles(pkg *opc.Package, mainPart string) *styleTable {
	t := &styleTable{byID: map[string]*style{}}

	part := relatedPart(pkg, mainPart, opc.RelStyles, "word/styles.xml")
	if part == "" {
		return t
	}

	var x xmlStyles
	if err := pkg.UnmarshalPart(part, &x); err != nil {
		return t
	}

	for _, s := range x.Styles {
		if s.StyleID == "" {
			continue
		}
		st := &style{ID: s.StyleID}
		if s.Name != nil {
			st.Name = s.Name.Val
		}
		if s.BasedOn != nil {
			st.BasedOn = s.BasedOn.Val
		}
		if s.PPr != nil {
			if s.PPr.OutlineLvl != nil {
				if n, err := strconv.Atoi(s.PPr.OutlineLvl.Val); err == nil {
					st.OutlineLevel, st.HasOutline = n, true
				}
			}
			if np := s.PPr.NumPr; np != nil {
				if np.NumID != nil && np.NumID.Val != "" && np.NumID.Val != "0" {
					st.NumID, st.HasNum = np.NumID.Val, true
				}
				if np.ILvl != nil {
					st.NumLevel, _ = strconv.Atoi(np.ILvl.Val)
				}
			}
		}
		t.byID[s.StyleID] = st
	}
	return t
}

// walk follows the basedOn chain from id, calling fn at each step until fn
// returns true. The depth cap guards against cyclic definitions.
func (t *styleTable) walk(id string, fn func(*style) bool) {
	seen := map[string]bool{}
	for depth := 0; id != "" && depth < 16; depth++ {
		if seen[id] {
			return
		}
		seen[id] = true
		s, ok := t.byID[id]
		if !ok {
			return
		}
		if fn(s) {
			return
		}
		id = s.BasedOn
	}
}

// headingLevel reports the Markdown heading level for a style, from an
// explicit outline level or a "Heading N" style name.
func (t *styleTable) headingLevel(id string) (int, bool) {
	if id == "" {
		return 0, false
	}

	var level int
	var found bool
	t.walk(id, func(s *style) bool {
		if n, ok := headingFromName(s.Name); ok {
			level, found = n, true
			return true
		}
		if n, ok := headingFromName(s.ID); ok {
			level, found = n, true
			return true
		}
		if s.HasOutline && s.OutlineLevel >= 0 && s.OutlineLevel <= 8 {
			level, found = s.OutlineLevel+1, true
			return true
		}
		return false
	})
	return level, found
}

// headingFromName matches the conventional heading style names, which vary by
// producer and locale ("Heading 1", "heading1", "Title").
func headingFromName(name string) (int, bool) {
	n := strings.ToLower(strings.TrimSpace(name))
	n = strings.ReplaceAll(n, "-", "")
	n = strings.ReplaceAll(n, "_", "")

	switch n {
	case "title":
		return 1, true
	case "subtitle":
		return 2, true
	}

	n = strings.ReplaceAll(n, " ", "")
	if !strings.HasPrefix(n, "heading") {
		return 0, false
	}
	digits := strings.TrimPrefix(n, "heading")
	if digits == "" {
		return 0, false
	}
	level, err := strconv.Atoi(digits)
	if err != nil || level < 1 || level > 9 {
		return 0, false
	}
	if level > 6 {
		level = 6
	}
	return level, true
}

// isQuote reports a block-quote style.
func (t *styleTable) isQuote(id string) bool {
	if id == "" {
		return false
	}
	var quote bool
	t.walk(id, func(s *style) bool {
		n := strings.ToLower(strings.ReplaceAll(s.Name, " ", ""))
		i := strings.ToLower(strings.ReplaceAll(s.ID, " ", ""))
		if n == "quote" || n == "blockquote" || n == "intensequote" ||
			i == "quote" || i == "blockquote" || i == "intensequote" {
			quote = true
			return true
		}
		return false
	})
	return quote
}

// numbering reports the list a style places its paragraphs in.
func (t *styleTable) numbering(id string) (string, int, bool) {
	if id == "" {
		return "", 0, false
	}
	var numID string
	var level int
	var found bool
	t.walk(id, func(s *style) bool {
		if s.HasNum {
			numID, level, found = s.NumID, s.NumLevel, true
			return true
		}
		return false
	})
	return numID, level, found
}

// relatedPart resolves a part by relationship type, falling back to the
// conventional location when the relationship is absent.
func relatedPart(pkg *opc.Package, from, relType, fallback string) string {
	for _, rel := range pkg.Rels(from).ByType(relType) {
		if target := rel.Resolve(); pkg.Has(target) {
			return target
		}
	}
	if pkg.Has(fallback) {
		return fallback
	}
	return ""
}

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
	byID             map[string]*style
	defaultParagraph string
	defaultCharacter string
	defaultPPr       *paraProps
	defaultRPr       *runProps
}

type style struct {
	ID      string
	Type    string
	Name    string
	BasedOn string
	PPr     *paraProps
	RPr     *runProps
}

type xmlStyles struct {
	Defaults struct {
		PPr *paraProps `xml:"pPrDefault>pPr"`
		RPr *runProps  `xml:"rPrDefault>rPr"`
	} `xml:"docDefaults"`
	Styles []struct {
		StyleID string     `xml:"styleId,attr"`
		Type    string     `xml:"type,attr"`
		Default string     `xml:"default,attr"`
		Name    *val       `xml:"name"`
		BasedOn *val       `xml:"basedOn"`
		PPr     *paraProps `xml:"pPr"`
		RPr     *runProps  `xml:"rPr"`
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

	t.defaultPPr, t.defaultRPr = x.Defaults.PPr, x.Defaults.RPr
	for _, s := range x.Styles {
		if s.StyleID == "" {
			continue
		}
		st := &style{ID: s.StyleID, Type: s.Type, PPr: s.PPr, RPr: s.RPr}
		if s.Name != nil {
			st.Name = s.Name.Val
		}
		if s.BasedOn != nil {
			st.BasedOn = s.BasedOn.Val
		}
		// ECMA-376 §17.7.4.17: CT_Style@default is ST_OnOff. An omitted
		// attribute is false, unlike a present run-property toggle without val.
		if s.Default != "" && (&onOff{Val: s.Default}).on() {
			switch s.Type {
			case "paragraph":
				t.defaultParagraph = s.StyleID
			case "character":
				t.defaultCharacter = s.StyleID
			}
		}
		t.byID[s.StyleID] = st
	}
	return t
}

// walk follows the basedOn chain from id, calling fn at each step until fn
// returns true. The depth cap guards against cyclic definitions.
func (t *styleTable) walk(id string, fn func(*style) bool) {
	if t == nil {
		return
	}
	seen := map[string]bool{}
	var styleType string
	for depth := 0; id != "" && depth < 16; depth++ {
		if seen[id] {
			return
		}
		seen[id] = true
		s, ok := t.byID[id]
		if !ok {
			return
		}
		if styleType != "" && s.Type != "" && s.Type != styleType {
			return // basedOn cannot cross style families (§17.7.4.3).
		}
		styleType = s.Type
		if fn(s) {
			return
		}
		id = s.BasedOn
	}
}

func (t *styleTable) paragraphStyle(id string) string {
	if t == nil {
		return id
	}
	if s := t.byID[id]; s != nil && (s.Type == "" || s.Type == "paragraph") {
		return id
	}
	return t.defaultParagraph
}

func outlineLevel(p *paraProps) (int, bool) {
	if p != nil && p.OutlineLvl != nil {
		n, err := strconv.Atoi(p.OutlineLvl.Val)
		if err == nil && n >= 0 && n <= 9 {
			return n, true
		}
	}
	return 0, false
}

func (t *styleTable) paragraphHeading(p *paragraph, id string) (int, bool) {
	if n, ok := outlineLevel(p.PPr); ok {
		return n + 1, n < 9
	}
	return t.headingLevel(id)
}

// headingLevel reports the Markdown heading level for a style, from an
// explicit outline level or a "Heading N" style name.
func (t *styleTable) headingLevel(id string) (int, bool) {
	var level int
	var found bool
	// Explicit outline properties outrank conventional style names. Level 9
	// removes a heading inherited through basedOn (§17.3.1.20).
	t.walk(id, func(s *style) bool {
		level, found = outlineLevel(s.PPr)
		return found
	})
	if !found && t != nil {
		level, found = outlineLevel(t.defaultPPr)
	}
	if found {
		return level + 1, level < 9
	}
	t.walk(id, func(s *style) bool {
		if n, ok := headingFromName(s.Name); ok {
			level, found = n, true
			return true
		}
		if n, ok := headingFromName(s.ID); ok {
			level, found = n, true
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

// numbering resolves individual numPr properties from nearest to farthest.
// Preserve numId=0: it explicitly removes inherited numbering (§17.9.18).
func (t *styleTable) numbering(id string) numPr {
	var out numPr
	inherit := func(p *paraProps) {
		if p == nil || p.NumPr == nil {
			return
		}
		if out.NumID == nil {
			out.NumID = p.NumPr.NumID
		}
		if out.ILvl == nil {
			out.ILvl = p.NumPr.ILvl
		}
	}
	t.walk(id, func(s *style) bool {
		inherit(s.PPr)
		return out.NumID != nil && out.ILvl != nil
	})
	if t != nil {
		inherit(t.defaultPPr)
	}
	return out
}

// runFormatting follows the style hierarchy for the formatting Markdown can
// express. Style-level b/i/strike are toggles (§17.7.3), while docDefaults and
// direct run properties set absolute values. Paragraph-mark rPr and table
// formatting are intentionally not treated as run formatting here.
func (t *styleTable) runFormatting(paragraphID string, direct *runProps) runProps {
	var out runProps
	if t != nil {
		out.apply(t.defaultRPr, false)
	}
	applyStyle := func(id string) {
		var chain []*style
		t.walk(id, func(s *style) bool {
			chain = append(chain, s)
			return false
		})
		for i := len(chain) - 1; i >= 0; i-- {
			out.apply(chain[i].RPr, true)
		}
	}
	applyStyle(paragraphID)
	if t != nil {
		id := t.defaultCharacter
		if direct != nil && direct.Style != nil {
			if s := t.byID[direct.Style.Val]; s != nil && s.Type == "character" {
				id = s.ID
			}
		}
		applyStyle(id)
	}
	out.apply(direct, false)
	return out
}

func (p *runProps) apply(src *runProps, toggle bool) {
	if src == nil {
		return
	}
	apply := func(dst **onOff, value *onOff) {
		if value == nil || (toggle && !value.on()) {
			return
		}
		if toggle && (*dst).on() {
			*dst = &onOff{Val: "0"}
		} else {
			*dst = value
		}
	}
	apply(&p.Bold, src.Bold)
	apply(&p.Italic, src.Italic)
	apply(&p.Strike, src.Strike)
	if src.RFonts != nil && src.RFonts.ASCII != "" {
		p.RFonts = src.RFonts
	}
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

package pptx

import (
	"encoding/xml"
	"io"
	"strconv"
	"strings"

	"github.com/adrianliechti/go-extract/internal/ooxml/mdw"
	"github.com/adrianliechti/go-extract/internal/ooxml/opc"
)

// slide is the extracted content of one slide part.
type slide struct {
	Shapes []shape
	Tables [][][]string
	Pics   []slidePic
	hidden bool
}

// slidePic is a picture placed on a slide, with the alt text authored for it.
type slidePic struct {
	relID string
	alt   string
	x, y  int64
}

// shape is a text-bearing shape with its position, used to recover reading
// order.
type shape struct {
	paras          []slidePara
	x, y           int64
	isTitle        bool
	isTextBox      bool
	inheritsBullet bool
	hidden         bool
}

type bulletKind uint8

const (
	bulletInherit bulletKind = iota
	bulletNone
	bulletUnordered
	bulletOrdered
)

// slidePara is one paragraph inside a shape.
type slidePara struct {
	text    string
	level   int
	bullet  bulletKind
	startAt int
}

// parseSlide walks a slide part, collecting text bodies, their positions, and
// any tables. DrawingML nests deeply and varies by producer, so the part is
// read token-wise rather than through struct tags.
func parseSlide(pkg *opc.Package, part string) (*slide, error) {
	data, err := pkg.ReadPart(part)
	if err != nil {
		return nil, err
	}
	dec := xml.NewDecoder(strings.NewReader(string(data)))
	dec.Strict = false
	dec.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }

	s := &slide{}
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch se.Name.Local {
		case "sld":
			s.hidden = attr(se, "show") == "0" || attr(se, "show") == "false"
		case "sp":
			if sh, ok := parseShape(dec, se); ok {
				s.Shapes = append(s.Shapes, sh)
			}
		case "graphicFrame":
			if tbl, ok := parseGraphicFrame(dec, se); ok {
				s.Tables = append(s.Tables, tbl)
			}
		case "pic":
			if pic, ok := parsePic(dec, se); ok {
				s.Pics = append(s.Pics, pic)
			}
		}
	}
	return s, nil
}

// parseShape decodes a p:sp element into a shape.
func parseShape(dec *xml.Decoder, start xml.StartElement) (shape, bool) {
	var sh shape
	depth := 1

	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return sh, !sh.hidden && len(sh.paras) > 0
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			switch t.Name.Local {
			case "cNvPr":
				sh.hidden = on(attr(t, "hidden"))
			case "cNvSpPr":
				sh.isTextBox = on(attr(t, "txBox"))
			case "ph":
				// A placeholder of type title or ctrTitle marks the slide title.
				phType := ""
				for _, a := range t.Attr {
					if a.Name.Local == "type" {
						phType = a.Value
					}
				}
				sh.isTitle = phType == "title" || phType == "ctrTitle"
				// Only body/object placeholders inherit list markers by default.
				sh.inheritsBullet = phType == "" || phType == "body" || phType == "obj"
			case "off":
				for _, a := range t.Attr {
					switch a.Name.Local {
					case "x":
						sh.x, _ = strconv.ParseInt(a.Value, 10, 64)
					case "y":
						sh.y, _ = strconv.ParseInt(a.Value, 10, 64)
					}
				}
			case "p":
				if p, ok := parseTextParagraph(dec, t); ok {
					sh.paras = append(sh.paras, p)
				}
				depth-- // parseTextParagraph consumed the matching end element
			}
		case xml.EndElement:
			depth--
		}
	}
	return sh, !sh.hidden && len(sh.paras) > 0
}

// parseTextParagraph decodes an a:p element, concatenating its runs.
func parseTextParagraph(dec *xml.Decoder, start xml.StartElement) (slidePara, bool) {
	var p slidePara

	var b strings.Builder
	depth := 1

	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			switch t.Name.Local {
			case "pPr":
				for _, a := range t.Attr {
					if a.Name.Local == "lvl" {
						p.level, _ = strconv.Atoi(a.Value)
					}
				}
			case "buNone":
				p.bullet = bulletNone
			case "buChar", "buBlip":
				p.bullet = bulletUnordered
			case "buAutoNum":
				p.bullet = bulletOrdered
				p.startAt, _ = strconv.Atoi(attr(t, "startAt"))
			case "t":
				var s string
				if err := dec.DecodeElement(&s, &t); err == nil {
					b.WriteString(s)
				}
				depth--
			case "br":
				b.WriteString(" ")
			}
		case xml.EndElement:
			depth--
		}
	}

	p.text = normalizeSpace(b.String())
	p.level = mdw.ClampListDepth(p.level)
	if strings.TrimSpace(p.text) == "" {
		return p, false
	}
	return p, true
}

// parseGraphicFrame extracts a table from a p:graphicFrame element.
func parseGraphicFrame(dec *xml.Decoder, start xml.StartElement) ([][]string, bool) {
	var rows [][]string
	hidden := false
	depth := 1

	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if t.Name.Local == "cNvPr" {
				hidden = on(attr(t, "hidden"))
			}
			if t.Name.Local == "tr" {
				if row, ok := parseTableRow(dec, t); ok {
					rows = append(rows, row)
				}
				depth--
			}
		case xml.EndElement:
			depth--
		}
	}
	return rows, !hidden && len(rows) > 0
}

func parseTableRow(dec *xml.Decoder, start xml.StartElement) ([]string, bool) {
	var cells []string
	depth := 1

	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if t.Name.Local == "tc" {
				cells = append(cells, parseTableCell(dec, t))
				depth--
			}
		case xml.EndElement:
			depth--
		}
	}
	return cells, len(cells) > 0
}

func parseTableCell(dec *xml.Decoder, start xml.StartElement) string {
	var parts []string
	depth := 1

	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if t.Name.Local == "p" {
				if p, ok := parseTextParagraph(dec, t); ok {
					parts = append(parts, p.text)
				}
				depth--
			}
		case xml.EndElement:
			depth--
		}
	}
	return strings.Join(parts, " ")
}

// normalizeSpace collapses the zero-width and non-breaking characters
// PowerPoint emits.
func normalizeSpace(s string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\u00A0', '\u2007', '\u202F':
			b.WriteRune(' ')
		case '\u200B', '\uFEFF', '\u200C', '\u200D':
			// zero-width characters carry no meaning in Markdown
		case '\r', '\n', '\t', '\v':
			b.WriteRune(' ')
		default:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

// parsePic decodes a p:pic element, recovering the relationship id, the alt
// text authored on it, and its position.
//
// Alt text matters more here than elsewhere: a slide is often a single image
// whose description is the only text content the deck carries.
func parsePic(dec *xml.Decoder, start xml.StartElement) (slidePic, bool) {
	var p slidePic
	hidden := false
	depth := 1

	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			switch t.Name.Local {
			case "cNvPr":
				hidden = on(attr(t, "hidden"))
				p.alt = firstNonEmpty(attr(t, "descr"), attr(t, "title"), attr(t, "name"))
			case "blip":
				if id := firstNonEmpty(attr(t, "embed"), attr(t, "link")); id != "" {
					p.relID = id
				}
			case "off":
				p.x = parseInt(attr(t, "x"))
				p.y = parseInt(attr(t, "y"))
			}
		case xml.EndElement:
			depth--
		}
	}
	return p, !hidden && p.relID != ""
}

// attr returns an attribute by local name, ignoring its namespace.
func attr(e xml.StartElement, name string) string {
	for _, a := range e.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func parseInt(s string) int64 {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func on(value string) bool {
	switch strings.ToLower(value) {
	case "1", "true", "on":
		return true
	}
	return false
}

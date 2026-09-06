package docx

import (
	"encoding/xml"
	"io"
	"strings"
)

// run is a w:r element: a span of text sharing formatting, plus anything the
// run anchors — images and text boxes.
//
// It is decoded by a token walk rather than struct tags because the elements
// that matter are nested at varying depths inside DrawingML and VML, and
// because mc:AlternateContent needs explicit branch selection.
type run struct {
	RPr *runProps

	// Text is the run's visible text, with tabs and breaks folded to spaces.
	Text string

	// Images lists every image the run anchors, in document order.
	Images []runImage

	// TxbxParas holds paragraphs from any text box the run anchors. Their
	// content belongs to the document flow, not to this run's text.
	TxbxParas []paragraph
}

// runImage is an image reference discovered inside a run.
type runImage struct {
	RelID string
	Alt   string
}

// UnmarshalXML walks a w:r element.
func (r *run) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	var text strings.Builder
	// pendingAlt carries alt text from a docPr/cNvPr seen before the blip that
	// it describes, which is the order DrawingML uses.
	var pendingAlt string

	err := walkChildren(d, start, func(t xml.StartElement) (handled bool, err error) {
		switch t.Name.Local {
		case "rPr":
			var pr runProps
			if err := d.DecodeElement(&pr, &t); err == nil {
				r.RPr = &pr
			}
			return true, nil

		case "t":
			var s string
			if err := d.DecodeElement(&s, &t); err == nil {
				text.WriteString(s)
			}
			return true, nil

		case "delText", "instrText":
			// Deleted text and field instructions are not visible content.
			return true, d.Skip()

		case "tab", "br", "cr":
			text.WriteString(" ")
			return false, nil

		case "noBreakHyphen":
			text.WriteString("-")
			return false, nil

		case "AlternateContent":
			// Choice and Fallback describe the same content in different
			// dialects. Taking both would duplicate every text box.
			paras, imgs, err := r.parseAlternateContent(d, t)
			if err != nil {
				return true, err
			}
			r.TxbxParas = append(r.TxbxParas, paras...)
			r.Images = append(r.Images, imgs...)
			return true, nil

		case "oMath", "oMathPara":
			if latex, _ := parseOMML(d, t); latex != "" {
				text.WriteString("$" + latex + "$")
			}
			return true, nil

		case "txbxContent":
			paras, err := parseTxbxContent(d, t)
			if err != nil {
				return true, err
			}
			r.TxbxParas = append(r.TxbxParas, paras...)
			return true, nil

		case "docPr", "cNvPr":
			pendingAlt = firstNonEmpty(attr(t, "descr"), attr(t, "title"), attr(t, "name"))
			return false, nil

		case "blip":
			if id := firstNonEmpty(attr(t, "embed"), attr(t, "link")); id != "" {
				r.Images = append(r.Images, runImage{RelID: id, Alt: pendingAlt})
				pendingAlt = ""
			}
			return false, nil

		case "imagedata":
			// Legacy VML image reference.
			if id := firstNonEmpty(attr(t, "id"), attr(t, "relid")); id != "" {
				r.Images = append(r.Images, runImage{RelID: id, Alt: firstNonEmpty(attr(t, "title"), pendingAlt)})
				pendingAlt = ""
			}
			return false, nil
		}
		return false, nil
	})

	r.Text = normalizeSpace(text.String())
	return err
}

// parseAlternateContent processes an mc:AlternateContent element, taking the
// first mc:Choice and discarding mc:Fallback. A Fallback is used only when no
// Choice was present, which happens in documents targeting older producers.
func (r *run) parseAlternateContent(d *xml.Decoder, start xml.StartElement) ([]paragraph, []runImage, error) {
	var chosen, fallback *run
	tookChoice := false

	err := walkChildren(d, start, func(t xml.StartElement) (bool, error) {
		switch t.Name.Local {
		case "Choice":
			if tookChoice {
				return true, d.Skip()
			}
			tookChoice = true
			nested := &run{}
			if err := nested.UnmarshalXML(d, t); err != nil {
				return true, err
			}
			chosen = nested
			return true, nil

		case "Fallback":
			nested := &run{}
			if err := nested.UnmarshalXML(d, t); err != nil {
				return true, err
			}
			fallback = nested
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		return nil, nil, err
	}

	if chosen != nil {
		return chosen.TxbxParas, chosen.Images, nil
	}
	if fallback != nil {
		return fallback.TxbxParas, fallback.Images, nil
	}
	return nil, nil, nil
}

// parseTxbxContent decodes the paragraphs inside a w:txbxContent element.
func parseTxbxContent(d *xml.Decoder, start xml.StartElement) ([]paragraph, error) {
	var out []paragraph
	err := walkChildren(d, start, func(t xml.StartElement) (bool, error) {
		switch t.Name.Local {
		case "p":
			var p paragraph
			if err := d.DecodeElement(&p, &t); err == nil {
				out = append(out, p)
			}
			return true, nil
		case "tbl":
			// A table inside a text box is flattened to its cell paragraphs,
			// since the text box already breaks the document's block flow.
			var tbl table
			if err := d.DecodeElement(&tbl, &t); err == nil {
				out = appendTableParagraphs(out, &tbl)
			}
			return true, nil
		}
		return false, nil
	})
	return out, err
}

func appendTableParagraphs(out []paragraph, tbl *table) []paragraph {
	for _, row := range tbl.Rows {
		for _, cell := range row.Cells {
			for _, b := range cell.Blocks {
				switch {
				case b.Para != nil:
					out = append(out, *b.Para)
				case b.Table != nil:
					out = appendTableParagraphs(out, b.Table)
				}
			}
		}
	}
	return out
}

// walkChildren iterates the direct and nested children of an element, calling
// fn for each start element. fn reports whether it consumed the element
// (including its end tag); unconsumed elements are descended into, so callers
// see constructs at any depth without tracking it themselves.
func walkChildren(d *xml.Decoder, start xml.StartElement, fn func(xml.StartElement) (bool, error)) error {
	depth := 0
	for {
		tok, err := d.Token()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			handled, err := fn(t)
			if err != nil {
				return err
			}
			if !handled {
				depth++
			}
		case xml.EndElement:
			if depth == 0 {
				if t.Name.Local == start.Name.Local {
					return nil
				}
				// A stray end tag at depth zero closes our element in
				// malformed input; stop rather than run past it.
				return nil
			}
			depth--
		}
	}
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

// normalizeSpace folds the non-breaking and zero-width characters Word emits
// into ordinary spaces, so downstream Markdown stays clean.
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
	return b.String()
}

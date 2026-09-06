// Package docx converts WordprocessingML (.docx) documents to Markdown.
package docx

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/adrianliechti/go-extract/internal/ooxml/mdw"
	"github.com/adrianliechti/go-extract/internal/ooxml/media"
	"github.com/adrianliechti/go-extract/internal/ooxml/model"
	"github.com/adrianliechti/go-extract/internal/ooxml/opc"
	"github.com/adrianliechti/go-extract/internal/ooxml/props"
)

// Convert renders a WordprocessingML package as Markdown.
func Convert(pkg *opc.Package, mainPart string, opts model.Options) (*model.Document, error) {
	doc := &model.Document{Format: model.FormatDocx}
	doc.Title = props.Title(pkg)

	body, err := parseBody(pkg, mainPart)
	if err != nil {
		return nil, err
	}

	c := &converter{
		pkg:    pkg,
		part:   mainPart,
		opts:   opts,
		rels:   pkg.Rels(mainPart),
		styles: loadStyles(pkg, mainPart),
		num:    loadNumbering(pkg, mainPart),
		images: media.NewCollector(pkg, opts),
		w:      mdw.New(),
	}
	c.renderBlocks(body.Blocks)

	doc.Markdown = c.w.String()
	doc.Images = c.images.Images()
	return doc, nil
}

type converter struct {
	pkg    *opc.Package
	part   string
	opts   model.Options
	rels   opc.Relationships
	styles *styleTable
	num    *numbering
	images *media.Collector
	w      *mdw.Writer

	// listCounters tracks the running number for each (numId, level) so
	// ordered lists restart and continue correctly.
	listCounters map[string]int

	// textboxDepth bounds recursion through nested text boxes.
	textboxDepth int
}

// maxTextboxDepth caps how deeply nested text boxes are followed. Word allows
// a shape inside a shape; a malformed document could otherwise cycle.
const maxTextboxDepth = 8

// ── document body ────────────────────────────────────────────────────

// body is the ordered sequence of block-level elements. Paragraphs and tables
// interleave, so they are decoded through a single any-slice to preserve order.
type body struct {
	Blocks []block
}

type block struct {
	Para  *paragraph
	Table *table
}

func parseBody(pkg *opc.Package, part string) (*body, error) {
	data, err := pkg.ReadPart(part)
	if err != nil {
		return nil, err
	}
	dec := newDecoder(data)

	var b body
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
		case "p":
			var p paragraph
			if err := dec.DecodeElement(&p, &se); err == nil {
				b.Blocks = append(b.Blocks, block{Para: &p})
			}
		case "tbl":
			var t table
			if err := dec.DecodeElement(&t, &se); err == nil {
				b.Blocks = append(b.Blocks, block{Table: &t})
			}
		}
	}
	if len(b.Blocks) == 0 {
		return &b, nil
	}
	return &b, nil
}

func newDecoder(data []byte) *xml.Decoder {
	dec := xml.NewDecoder(bytes.NewReader(data))
	// Producers vary in how strictly they conform; be permissive so a stray
	// entity or encoding declaration does not discard the whole document.
	dec.Strict = false
	dec.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
	return dec
}

// ── rendering ────────────────────────────────────────────────────────

func (c *converter) renderBlocks(blocks []block) {
	if c.listCounters == nil {
		c.listCounters = map[string]int{}
	}
	for _, b := range blocks {
		switch {
		case b.Para != nil:
			c.renderParagraph(b.Para)
		case b.Table != nil:
			c.renderTable(b.Table)
		}
	}
	c.w.EndList()
}

func (c *converter) renderParagraph(p *paragraph) {
	text := c.inlineText(p)

	// A paragraph holding only images emits them as blocks rather than as an
	// empty paragraph.
	images := c.paragraphImages(p)

	// Text boxes anchored in this paragraph carry their own block content.
	// It belongs to the document flow, so it is emitted alongside — after the
	// paragraph's own text when there is any, in its place when there is not.
	defer c.renderTextboxes(p)

	if strings.TrimSpace(text) == "" {
		if len(images) > 0 {
			c.w.EndList()
			for _, img := range images {
				c.w.Image(img.alt, img.src)
			}
		}
		return
	}

	styleID := p.styleID()

	// Headings come from the paragraph style, either a numeric outline level
	// or a named Heading style.
	if level, ok := c.styles.headingLevel(styleID); ok {
		c.w.EndList()
		c.w.Heading(level, text)
		c.emitImages(images)
		return
	}

	// List membership comes from numbering properties, either directly on the
	// paragraph or inherited from its style.
	if numID, level, ok := c.listInfo(p, styleID); ok {
		ordered, start := c.num.format(numID, level)
		number := 0
		if ordered {
			number = c.nextCounter(numID, level, start)
		}
		c.w.ListItem(level, number, text)
		c.emitImages(images)
		return
	}

	c.w.EndList()
	if c.styles.isQuote(styleID) {
		c.w.Block("> " + text)
	} else {
		c.w.Block(text)
	}
	c.emitImages(images)
}

// renderTextboxes emits the block content of every text box anchored in a
// paragraph. Nesting is bounded because a text box may itself anchor another.
func (c *converter) renderTextboxes(p *paragraph) {
	if c.textboxDepth >= maxTextboxDepth {
		return
	}
	c.textboxDepth++
	defer func() { c.textboxDepth-- }()

	for _, item := range p.Content {
		if item.Run == nil {
			continue
		}
		for i := range item.Run.TxbxParas {
			c.w.EndList()
			c.renderParagraph(&item.Run.TxbxParas[i])
		}
	}
}

func (c *converter) emitImages(images []imageRef) {
	if len(images) == 0 {
		return
	}
	c.w.EndList()
	for _, img := range images {
		c.w.Image(img.alt, img.src)
	}
}

// listInfo resolves a paragraph's numbering id and indent level, falling back
// to the numbering declared by its style.
func (c *converter) listInfo(p *paragraph, styleID string) (numID string, level int, ok bool) {
	if p.PPr != nil && p.PPr.NumPr != nil {
		np := p.PPr.NumPr
		if np.NumID != nil {
			numID = np.NumID.Val
		}
		if np.ILvl != nil {
			level, _ = strconv.Atoi(np.ILvl.Val)
		}
		if numID != "" && numID != "0" {
			return numID, mdw.ClampListDepth(level), true
		}
	}
	if n, lv, ok := c.styles.numbering(styleID); ok {
		return n, mdw.ClampListDepth(lv), true
	}
	return "", 0, false
}

// nextCounter advances the running number for an ordered list level and
// resets any deeper levels, matching how Word renumbers nested lists.
func (c *converter) nextCounter(numID string, level, start int) int {
	key := numID + ":" + strconv.Itoa(level)
	cur, seen := c.listCounters[key]
	if !seen {
		cur = start - 1
	}
	cur++
	c.listCounters[key] = cur

	for deeper := level + 1; deeper < 10; deeper++ {
		delete(c.listCounters, numID+":"+strconv.Itoa(deeper))
	}
	return cur
}

// ── tables ───────────────────────────────────────────────────────────

func (c *converter) renderTable(t *table) {
	c.w.EndList()

	rows := make([][]string, 0, len(t.Rows))
	for i := range t.Rows {
		row := &t.Rows[i]
		cells := make([]string, 0, len(row.Cells))
		for j := range row.Cells {
			cell := &row.Cells[j]
			text := c.cellText(cell)
			cells = append(cells, text)

			// A horizontally merged cell repeats its content across the span,
			// matching the reference corpora's rendering of merged cells.
			if span := cell.gridSpan(); span > 1 {
				for k := 1; k < span; k++ {
					cells = append(cells, text)
				}
			}
		}
		rows = append(rows, cells)
	}

	// Vertically merged cells continue the value from the row above.
	propagateVerticalMerges(t, rows)
	c.w.Table(rows)
}

// propagateVerticalMerges copies a cell's text downward through the rows it
// spans, so a vertical merge reads the same as a horizontal one.
func propagateVerticalMerges(t *table, rows [][]string) {
	for i := range t.Rows {
		if i == 0 || i >= len(rows) {
			continue
		}
		col := 0
		for j := range t.Rows[i].Cells {
			cell := &t.Rows[i].Cells[j]
			span := cell.gridSpan()
			if cell.isVerticalContinuation() && col < len(rows[i]) && col < len(rows[i-1]) {
				for k := 0; k < span && col+k < len(rows[i]) && col+k < len(rows[i-1]); k++ {
					rows[i][col+k] = rows[i-1][col+k]
				}
			}
			col += span
		}
	}
}

func (c *converter) cellText(cell *tableCell) string {
	var parts []string
	for _, b := range cell.Blocks {
		switch {
		case b.Para != nil:
			if s := strings.TrimSpace(c.inlineText(b.Para)); s != "" {
				parts = append(parts, s)
			}
		case b.Table != nil:
			// GFM cells cannot contain tables. Flatten each nested table at
			// its authored position between the surrounding paragraphs.
			for _, r := range b.Table.Rows {
				for j := range r.Cells {
					if s := strings.TrimSpace(c.cellText(&r.Cells[j])); s != "" {
						parts = append(parts, s)
					}
				}
			}
		}
	}
	return strings.Join(parts, "\n")
}

// ── errors ───────────────────────────────────────────────────────────

// ErrBadDocument reports a structurally unusable document part.
var ErrBadDocument = fmt.Errorf("docx: unreadable document part")

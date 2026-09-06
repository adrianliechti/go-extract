// Package pptx converts PresentationML (.pptx) decks to Markdown, one
// section per slide.
package pptx

import (
	"encoding/xml"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/adrianliechti/go-extract/internal/ooxml/mdw"
	"github.com/adrianliechti/go-extract/internal/ooxml/media"
	"github.com/adrianliechti/go-extract/internal/ooxml/model"
	"github.com/adrianliechti/go-extract/internal/ooxml/opc"
	"github.com/adrianliechti/go-extract/internal/ooxml/props"
)

// Convert renders a presentation as Markdown.
func Convert(pkg *opc.Package, mainPart string, opts model.Options) (*model.Document, error) {
	doc := &model.Document{Format: model.FormatPptx}
	doc.Title = props.Title(pkg)

	images := media.NewCollector(pkg, opts)
	w := mdw.New()

	slides := slideOrder(pkg, mainPart)

	for i, part := range slides {
		c := &slideConverter{pkg: pkg, part: part, opts: opts, images: images, w: w}
		if !c.render(i + 1) {
			continue
		}
		doc.SlideCount++

		if opts.SlideNotes {
			if notes := notesText(pkg, part); notes != "" {
				w.Block("> **Notes:** " + notes)
			}
		}
	}

	doc.Markdown = w.String()
	doc.Images = images.Images()
	return doc, nil
}

// slideOrder returns slide parts in presentation order. The order is given by
// the sldIdLst in presentation.xml, not by part name, because slides can be
// reordered without renaming.
func slideOrder(pkg *opc.Package, mainPart string) []string {
	// A sldId carries both a numeric id and a relationship r:id. encoding/xml
	// matches attributes on local name alone and cannot tell them apart, so
	// the list is read token-wise instead.
	ids := slideRelIDs(pkg, mainPart)
	rels := pkg.Rels(mainPart)

	var out []string
	for _, id := range ids {
		rel, ok := rels[id]
		if !ok {
			continue
		}
		if part := rel.Resolve(); pkg.Has(part) {
			out = append(out, part)
		}
	}

	if len(out) > 0 {
		return out
	}

	// Fall back to every slide part, ordered by the numeric suffix producers
	// conventionally use.
	var fallback []string
	for _, name := range pkg.Parts() {
		if strings.HasPrefix(name, "ppt/slides/slide") && strings.HasSuffix(name, ".xml") {
			fallback = append(fallback, name)
		}
	}
	sort.Slice(fallback, func(i, j int) bool {
		return slideNumber(fallback[i]) < slideNumber(fallback[j])
	})
	return fallback
}

// slideRelIDs reads the r:id of each sldId in document order.
func slideRelIDs(pkg *opc.Package, mainPart string) []string {
	data, err := pkg.ReadPart(mainPart)
	if err != nil {
		return nil
	}
	dec := xml.NewDecoder(strings.NewReader(string(data)))
	dec.Strict = false
	dec.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }

	var out []string
	inList := false
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "sldIdLst":
				inList = true
			case "sldId":
				if !inList {
					continue
				}
				for _, a := range t.Attr {
					// Match the relationship-namespace id, not the numeric one.
					if a.Name.Local == "id" && strings.Contains(a.Name.Space, "relationships") {
						out = append(out, a.Value)
					}
				}
			}
		case xml.EndElement:
			if t.Name.Local == "sldIdLst" {
				inList = false
			}
		}
	}
	return out
}

func slideNumber(part string) int {
	base := strings.TrimSuffix(strings.TrimPrefix(part, "ppt/slides/slide"), ".xml")
	n, err := strconv.Atoi(base)
	if err != nil {
		return 1 << 30
	}
	return n
}

// ── slide rendering ──────────────────────────────────────────────────

type slideConverter struct {
	pkg    *opc.Package
	part   string
	opts   model.Options
	images *media.Collector
	w      *mdw.Writer
}

func (c *slideConverter) render(number int) bool {
	sl, err := parseSlide(c.pkg, c.part)
	if err != nil {
		return false
	}
	if sl.hidden && !c.opts.IncludeHidden {
		return false
	}

	// A slide with nothing on it contributes no structure, so it gets no
	// heading — an empty "## Slide 4" is noise to a reader and to an agent.
	if len(sl.Shapes) == 0 && len(sl.Tables) == 0 && len(sl.Pics) == 0 {
		return true
	}

	// Order shapes by their position on the slide (top to bottom, then left to
	// right) so reading order matches what a viewer sees. Shapes carry no
	// inherent order in the XML.
	sort.SliceStable(sl.Shapes, func(i, j int) bool {
		a, b := sl.Shapes[i], sl.Shapes[j]
		if a.isTitle != b.isTitle {
			return a.isTitle // the title always leads
		}
		if a.y != b.y {
			return a.y < b.y
		}
		return a.x < b.x
	})

	// Emit the fallback boundary before any body text, otherwise an untitled
	// slide's text ends up under the preceding slide's heading in Markdown.
	if len(sl.Shapes) == 0 || !sl.Shapes[0].isTitle || joinParaText(sl.Shapes[0].paras) == "" {
		c.w.Heading(2, "Slide "+strconv.Itoa(number))
	}
	titled := false
	for _, sh := range sl.Shapes {
		if sh.isTitle && !titled && len(sh.paras) > 0 {
			if text := joinParaText(sh.paras); text != "" {
				c.w.Heading(2, text)
				titled = true
				continue
			}
		}
		c.renderShape(&sh)
	}

	for _, tbl := range sl.Tables {
		c.w.Table(tbl)
	}

	c.renderPics(sl.Pics)
	return true
}

// renderPics emits a slide's pictures in position order, carrying the alt text
// authored on each. A slide is often a single image whose description is the
// only text the deck holds, so that alt text is content rather than decoration.
func (c *slideConverter) renderPics(pics []slidePic) {
	if c.opts.SkipImages {
		return
	}

	sort.SliceStable(pics, func(i, j int) bool {
		if pics[i].y != pics[j].y {
			return pics[i].y < pics[j].y
		}
		return pics[i].x < pics[j].x
	})

	// Only p:pic elements are emitted. An image reachable solely through a
	// relationship is a shape fill or slide background — decoration rather
	// than content — and emitting it adds a generic, position-less reference
	// for something the reader never perceives as a picture.
	rels := c.pkg.Rels(c.part)
	for _, pic := range pics {
		rel, ok := rels[pic.relID]
		if !ok {
			continue
		}
		name, ok := c.images.Add(rel)
		if !ok {
			continue
		}
		alt := pic.alt
		if alt == "" {
			alt = "image"
		}
		c.w.Image(alt, c.images.Link(name))
	}
}

func (c *slideConverter) renderShape(sh *shape) {
	c.w.EndList()
	orderedCounters := map[int]int{}
	for _, p := range sh.paras {
		text := strings.TrimSpace(p.text)
		if text == "" {
			continue
		}
		bullet := p.bullet
		if bullet == bulletInherit {
			if sh.inheritsBullet && !sh.isTextBox {
				bullet = bulletUnordered
			} else {
				bullet = bulletNone
			}
		}
		switch bullet {
		case bulletUnordered:
			for level := p.level; level < 10; level++ {
				delete(orderedCounters, level)
			}
			c.w.ListItem(p.level, 0, text)
		case bulletOrdered:
			n := orderedCounters[p.level] + 1
			if p.startAt > 0 && orderedCounters[p.level] == 0 {
				n = p.startAt
			}
			orderedCounters[p.level] = n
			for level := p.level + 1; level < 10; level++ {
				delete(orderedCounters, level)
			}
			c.w.ListItem(p.level, n, text)
		default:
			clear(orderedCounters)
			c.w.EndList()
			c.w.Block(text)
		}
	}
	c.w.EndList()
}

func joinParaText(paras []slidePara) string {
	var parts []string
	for _, p := range paras {
		if s := strings.TrimSpace(p.text); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, " ")
}

// notesText returns the speaker notes attached to a slide.
func notesText(pkg *opc.Package, slidePart string) string {
	for _, rel := range pkg.Rels(slidePart).ByType(opc.RelNotesSlide) {
		part := rel.Resolve()
		if !pkg.Has(part) {
			continue
		}
		sl, err := parseSlide(pkg, part)
		if err != nil {
			continue
		}
		var parts []string
		for _, sh := range sl.Shapes {
			for _, p := range sh.paras {
				if s := strings.TrimSpace(p.text); s != "" {
					parts = append(parts, s)
				}
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, " ")
		}
	}
	return ""
}

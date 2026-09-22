package docx

import (
	"strings"

	"github.com/adrianliechti/go-extract/internal/ooxml/mdw"
	"github.com/adrianliechti/go-extract/internal/ooxml/media"
	"github.com/adrianliechti/go-extract/internal/ooxml/opc"
)

// imageRef is an image discovered while rendering a paragraph.
type imageRef struct {
	alt string
	src string
}

// inlineText renders a paragraph's runs to Markdown, emitting emphasis markers
// around contiguous spans that share formatting rather than per run, so
// "**bo**​**ld**" collapses to "**bold**".
func (c *converter) inlineText(p *paragraph) string {
	var b strings.Builder
	styleID := c.styles.paragraphStyle(p.styleID())

	// Track the currently open emphasis so adjacent runs with matching
	// formatting share one pair of markers.
	var openBold, openItalic, openStrike bool
	var pendingSpace strings.Builder
	flushSpace := func() {
		b.WriteString(pendingSpace.String())
		pendingSpace.Reset()
	}

	closeAll := func() {
		if openStrike {
			b.WriteString("~~")
			openStrike = false
		}
		if openItalic {
			b.WriteString("*")
			openItalic = false
		}
		if openBold {
			b.WriteString("**")
			openBold = false
		}
	}

	emit := func(source *run, linkTarget string) {
		r := *source
		formatting := c.styles.runFormatting(styleID, source.RPr)
		r.RPr = &formatting
		txt := runText(&r)
		if txt == "" {
			return
		}

		// Emphasis markers must sit inside the text, not around whitespace, or
		// Markdown will not recognise them.
		lead, core, trail := splitSpace(txt)
		if core == "" {
			pendingSpace.WriteString(txt)
			return
		}

		code := r.isCode()
		wantBold := r.bold() && linkTarget == "" && !code
		wantItalic := r.italic() && linkTarget == "" && !code
		wantStrike := r.strike() && linkTarget == "" && !code

		if openBold != wantBold || openItalic != wantItalic || openStrike != wantStrike {
			closeAll()
		}
		// Delay trailing whitespace until the next run's formatting is known:
		// it belongs inside a continuing span, outside a closing delimiter.
		flushSpace()
		b.WriteString(lead)
		if wantBold && !openBold {
			b.WriteString("**")
			openBold = true
		}
		if wantItalic && !openItalic {
			b.WriteString("*")
			openItalic = true
		}
		if wantStrike && !openStrike {
			b.WriteString("~~")
			openStrike = true
		}

		if code {
			core = codeSpan(core)
		} else {
			core = mdw.EscapeInline(core)
		}

		if linkTarget != "" {
			core = "[" + core + "](" + mdw.EscapeURL(linkTarget) + ")"
		}
		b.WriteString(core)
		pendingSpace.WriteString(trail)
	}

	for _, item := range p.Content {
		switch {
		case item.Run != nil:
			emit(item.Run, "")
		case item.Link != nil:
			closeAll()
			target := c.linkTarget(item.Link)
			for i := range item.Link.Runs {
				emit(&item.Link.Runs[i], target)
			}
		case item.Math != nil:
			closeAll()
			flushSpace()
			delim := "$"
			if item.Math.display {
				delim = "$$"
			}
			if b.Len() > 0 && !strings.HasSuffix(b.String(), " ") {
				b.WriteString(" ")
			}
			b.WriteString(delim + item.Math.latex + delim)
		}
	}
	closeAll()
	flushSpace()

	return strings.TrimRight(b.String(), " \t")
}

// Code spans contain literal text: backslash escaping would change their
// contents. A longer backtick delimiter (and boundary padding when needed)
// keeps authored backticks inside the span under CommonMark's code-span rules.
func codeSpan(text string) string {
	longest, current := 0, 0
	for _, r := range text {
		if r == '`' {
			current++
			longest = max(longest, current)
		} else {
			current = 0
		}
	}
	delimiter := strings.Repeat("`", longest+1)
	if strings.HasPrefix(text, "`") || strings.HasSuffix(text, "`") {
		text = " " + text + " "
	}
	return delimiter + text + delimiter
}

// splitSpace separates leading and trailing whitespace from the core text.
func splitSpace(s string) (lead, core, trail string) {
	core = strings.TrimLeft(s, " \t")
	lead = s[:len(s)-len(core)]
	trimmed := strings.TrimRight(core, " \t")
	trail = core[len(trimmed):]
	return lead, trimmed, trail
}

// runText returns a run's visible text, already normalised during parsing.
func runText(r *run) string { return r.Text }

func (r *run) bold() bool {
	return r.RPr != nil && r.RPr.Bold.on()
}

func (r *run) italic() bool {
	return r.RPr != nil && r.RPr.Italic.on()
}

func (r *run) strike() bool {
	return r.RPr != nil && r.RPr.Strike.on()
}

// isCode reports a monospace run, which Word expresses through the font
// rather than a dedicated style.
func (r *run) isCode() bool {
	if r.RPr == nil || r.RPr.RFonts == nil {
		return false
	}
	switch strings.ToLower(r.RPr.RFonts.ASCII) {
	case "consolas", "courier", "courier new", "menlo", "monaco", "sf mono":
		return true
	}
	return false
}

// linkTarget resolves a hyperlink to a URL or an in-document anchor.
func (c *converter) linkTarget(h *hyperlink) string {
	if h.ID != "" {
		if rel, ok := c.rels[h.ID]; ok && rel.Type == opc.RelHyperlink {
			return rel.Resolve()
		}
	}
	if h.Anchor != "" {
		return "#" + h.Anchor
	}
	return ""
}

// paragraphImages collects every image referenced by a paragraph's runs.
func (c *converter) paragraphImages(p *paragraph) []imageRef {
	if c.opts.SkipImages {
		return nil
	}
	var out []imageRef
	for _, item := range p.Content {
		runs := []*run{}
		switch {
		case item.Run != nil:
			runs = append(runs, item.Run)
		case item.Link != nil:
			for i := range item.Link.Runs {
				runs = append(runs, &item.Link.Runs[i])
			}
		}
		for _, r := range runs {
			out = append(out, c.runImages(r)...)
		}
	}
	return out
}

func (c *converter) runImages(r *run) []imageRef {
	var out []imageRef
	for _, img := range r.Images {
		if ref, ok := c.resolveImage(img.Blip, img.Alt); ok {
			out = append(out, ref)
		}
	}
	return out
}

// resolveImage selects an available blip source and collects the image.
func (c *converter) resolveImage(blip media.Blip, alt string) (imageRef, bool) {
	name, ok := c.images.AddBlip(blip, c.rels)
	if !ok {
		return imageRef{}, false
	}
	if alt == "" {
		alt = "image"
	}
	return imageRef{alt: alt, src: c.images.Link(name)}, true
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

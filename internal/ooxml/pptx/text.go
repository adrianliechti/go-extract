package pptx

import (
	"strings"

	"github.com/adrianliechti/go-extract/internal/ooxml/mdw"
	"github.com/adrianliechti/go-extract/internal/ooxml/opc"
)

type clickLink struct {
	ID     string `xml:"id,attr"`
	Action string `xml:"action,attr"`
}
type slideRun struct {
	text string
	link *clickLink
}

func (l *clickLink) target(rels opc.Relationships) string {
	if l == nil {
		return ""
	}
	// Slide jumps, macros and other ppaction commands are not URL hyperlinks.
	if l.Action != "" && l.Action != "ppaction://hlinkfile" {
		return ""
	}
	if rel, ok := rels[l.ID]; ok && rel.Type == opc.RelHyperlink {
		return rel.Resolve()
	}
	return ""
}

func (p slidePara) markdown(rels opc.Relationships, fallback *clickLink, context mdw.TextContext) string {
	runs := p.runs
	if len(runs) == 0 {
		runs = []slideRun{{text: p.text}}
	}
	var out strings.Builder
	lineStart := true
	for i := 0; i < len(runs); {
		link := runs[i].link
		if link == nil {
			link = fallback
		}
		target := link.target(rels)
		var text strings.Builder
		for i < len(runs) {
			next := runs[i].link
			if next == nil {
				next = fallback
			}
			if next.target(rels) != target {
				break
			}
			text.WriteString(runs[i].text)
			i++
		}
		// Normalize spaces on the assembled text, without trimming the spaces
		// that separate adjacent runs with different hyperlink destinations.
		raw := strings.Map(func(r rune) rune {
			switch r {
			case '\u00a0', '\u2007', '\u202f', '\r', '\n', '\t', '\v':
				return ' '
			case '\u200b', '\ufeff':
				return -1
			}
			return r
		}, text.String())
		out.WriteString(mdw.Link(mdw.EscapeFragment(raw, context, &lineStart), target))
	}
	return strings.TrimSpace(out.String())
}

package media

import (
	"encoding/xml"

	"github.com/adrianliechti/go-extract/internal/ooxml/opc"
)

// Blip retains both sources of a DrawingML image. MS-ODRAWXML §2.26.1.1
// identifies svgBlip as the vector resource; the ordinary blip is the
// compatibility raster and need not resolve to an available package part.
type Blip struct {
	RelID    string
	SVGRelID string
}

func (b *Blip) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	var link string
	for _, a := range start.Attr {
		switch a.Name.Local {
		case "embed":
			b.RelID = a.Value
		case "link":
			link = a.Value
		}
	}
	if b.RelID == "" {
		b.RelID = link
	}
	for depth := 1; depth > 0; {
		tok, err := d.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if t.Name.Local == "svgBlip" && t.Name.Space == "http://schemas.microsoft.com/office/drawing/2016/SVG/main" {
				for _, a := range t.Attr {
					if a.Name.Local == "embed" {
						b.SVGRelID = a.Value
					}
				}
			}
		case xml.EndElement:
			depth--
		}
	}
	return nil
}

// AddBlip emits one image per authored blip. Keep the existing raster output
// when it is readable (broad Markdown-client compatibility), falling back to
// the SVG for missing/empty/unreadable rasters. External resources are never
// fetched. Both paths retain Add's resource limits and part deduplication.
func (c *Collector) AddBlip(blip Blip, rels opc.Relationships) (string, bool) {
	for _, id := range []string{blip.RelID, blip.SVGRelID} {
		if rel, ok := rels[id]; ok && rel.Type == opc.RelImage {
			if name, ok := c.Add(rel); ok {
				return name, true
			}
		}
	}
	return "", false
}

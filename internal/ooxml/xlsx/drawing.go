package xlsx

import (
	"bytes"
	"encoding/xml"

	"github.com/adrianliechti/go-extract/internal/ooxml/media"
	"github.com/adrianliechti/go-extract/internal/ooxml/opc"
)

type sheetImage struct {
	name string
	alt  string
}

// sheetImages follows the worksheet's drawing references and their authored
// blips in XML order. Relationship maps also contain unused media and alternate
// versions of the same image, so enumerating them duplicates or invents images.
func sheetImages(pkg *opc.Package, sheetPart string, images *media.Collector) []sheetImage {
	var sheet struct {
		Drawings []struct {
			ID string `xml:"id,attr"`
		} `xml:"drawing"`
	}
	if err := pkg.UnmarshalPart(sheetPart, &sheet); err != nil {
		return nil
	}
	var out []sheetImage
	rels := pkg.Rels(sheetPart)
	for _, ref := range sheet.Drawings {
		rel, ok := rels[ref.ID]
		if !ok || rel.External || rel.Type != opc.RelDrawing {
			continue
		}
		part := rel.Resolve()
		data, err := pkg.ReadPart(part)
		if err != nil {
			continue
		}
		dec := xml.NewDecoder(bytes.NewReader(data))
		dec.Strict = false
		for {
			tok, err := dec.Token()
			if err != nil {
				break
			}
			start, ok := tok.(xml.StartElement)
			if !ok || (start.Name.Local != "pic" && start.Name.Local != "sp") {
				continue
			}
			out = append(out, drawingImages(dec, start, images, pkg.Rels(part))...)
		}
	}
	return out
}

func drawingImages(dec *xml.Decoder, start xml.StartElement, images *media.Collector, rels opc.Relationships) []sheetImage {
	var out []sheetImage
	alt := "image"
	hidden := false
	var blips []media.Blip
	for depth := 1; depth > 0; {
		tok, err := dec.Token()
		if err != nil {
			return nil
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			switch t.Name.Local {
			case "cNvPr":
				var props struct {
					Descr  string `xml:"descr,attr"`
					Title  string `xml:"title,attr"`
					Name   string `xml:"name,attr"`
					Hidden bool   `xml:"hidden,attr"`
				}
				if err := dec.DecodeElement(&props, &t); err != nil {
					return nil
				}
				depth--
				hidden = props.Hidden
				for _, text := range []string{props.Descr, props.Title, props.Name} {
					if text != "" {
						alt = text
						break
					}
				}
			case "blip":
				var blip media.Blip
				if err := dec.DecodeElement(&blip, &t); err != nil {
					return nil
				}
				depth--
				blips = append(blips, blip)
			}
		case xml.EndElement:
			depth--
		}
	}
	if !hidden {
		for _, blip := range blips {
			if name, ok := images.AddBlip(blip, rels); ok {
				out = append(out, sheetImage{name: name, alt: alt})
			}
		}
	}
	return out
}

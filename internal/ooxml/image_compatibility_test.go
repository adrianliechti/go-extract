package ooxml_test

import (
	"strings"
	"testing"

	"github.com/adrianliechti/go-extract/internal/ooxml"
)

const svgImage = `<svg xmlns="http://www.w3.org/2000/svg" width="2" height="2"><rect width="2" height="2"/></svg>`

// office-open-xml-viewer b6c8ac7f preserves a shape's SVG when its compatibility
// raster is missing. Exercise the distinct extraction paths in all three hosts.
func TestDrawingImagesUseSVGFallback(t *testing.T) {
	for _, format := range []string{"docx", "pptx", "pptx-shape", "xlsx"} {
		t.Run(format, func(t *testing.T) {
			data := buildOOXML(t, imagePackage(format, svgBlip(`r:embed="rRaster"`), false))
			doc, err := ooxml.Convert(data, ooxml.Options{ImagePrefix: "assets"})
			if err != nil {
				t.Fatal(err)
			}
			if len(doc.Images) != 1 || doc.Images[0].Name != "vector.svg" || doc.Images[0].ContentType != "image/svg+xml" || string(doc.Images[0].Data) != svgImage {
				t.Fatalf("images = %#v, want the surviving SVG", doc.Images)
			}
			if strings.Count(doc.Markdown, "![Diagram](assets/vector.svg)") != 1 {
				t.Fatalf("image description/reference missing or duplicated:\n%s", doc.Markdown)
			}
			doc, err = ooxml.Convert(data, ooxml.Options{SkipImages: true})
			if err != nil {
				t.Fatal(err)
			}
			if len(doc.Images) != 0 || strings.Contains(doc.Markdown, "![") {
				t.Fatalf("SkipImages emitted images: %#v", doc)
			}
		})
	}
}

func TestDrawingImageSourceSelection(t *testing.T) {
	for _, tc := range []struct {
		name, blip, want string
		raster           bool
	}{
		{"SVG only", svgBlip(""), "vector.svg", false},
		{"missing raster relationship", svgBlip(`r:embed="missing"`), "vector.svg", false},
		{"retain available raster", svgBlip(`r:embed="rRaster"`), "raster.png", true},
		{"missing SVG", strings.ReplaceAll(svgBlip(`r:embed="rRaster"`), `r:embed="rSVG"`, `r:embed="missing"`), "raster.png", true},
		{"neither source available", `<a:blip r:embed="missing"/>`, "", false},
		{"unrelated extension", strings.ReplaceAll(svgBlip(""), "http://schemas.microsoft.com/office/drawing/2016/SVG/main", "urn:unrelated"), "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := ooxml.Convert(buildOOXML(t, imagePackage("docx", tc.blip, tc.raster)), ooxml.Options{})
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if len(doc.Images) != 0 || strings.Contains(doc.Markdown, "![") {
					t.Fatalf("unexpected image: %#v", doc)
				}
			} else if len(doc.Images) != 1 || doc.Images[0].Name != tc.want || strings.Count(doc.Markdown, "![") != 1 {
				t.Fatalf("images = %#v, Markdown = %q, want one %s", doc.Images, doc.Markdown, tc.want)
			}
		})
	}
}

func TestXlsxDrawingUsesAuthoredImageReferences(t *testing.T) {
	parts := imagePackage("xlsx", svgBlip(`r:embed="rRaster"`), true)
	// A populated sheet must use the drawing's image order/selection too,
	// rather than exporting both versions and an unreferenced relationship.
	for i := range parts {
		if parts[i].name == "xl/worksheets/sheet1.xml" {
			parts[i].text = strings.ReplaceAll(parts[i].text, `<sheetData/>`, `<sheetData><row r="1"><c r="A1" t="inlineStr"><is><t>Data</t></is></c></row></sheetData>`)
		}
	}
	doc, err := ooxml.Convert(buildOOXML(t, parts), ooxml.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Images) != 1 || doc.Images[0].Name != "raster.png" || strings.Count(doc.Markdown, "![Diagram](raster.png)") != 1 || !strings.Contains(doc.Markdown, "Data") {
		t.Fatalf("unexpected worksheet extraction: %#v", doc)
	}
}

func TestPptxHiddenImageFillIsOmitted(t *testing.T) {
	parts := imagePackage("pptx-shape", svgBlip(`r:embed="rRaster"`), false)
	for i := range parts {
		if parts[i].name == "ppt/slides/slide1.xml" {
			parts[i].text = strings.ReplaceAll(parts[i].text, `descr="Diagram"`, `descr="Diagram" hidden="true"`)
		}
	}
	doc, err := ooxml.Convert(buildOOXML(t, parts), ooxml.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Images) != 0 || strings.Contains(doc.Markdown, "![") {
		t.Fatalf("hidden shape emitted: %#v", doc)
	}
}

func svgBlip(attributes string) string {
	return `<a:blip ` + attributes + `><a:extLst><a:ext uri="{96DAC541-7B7A-43D3-8B79-37D633B846F1}"><svg:svgBlip xmlns:svg="http://schemas.microsoft.com/office/drawing/2016/SVG/main" r:embed="rSVG"/></a:ext></a:extLst></a:blip>`
}

func imagePackage(format, blip string, raster bool) []testPackagePart {
	const ns = ` xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="` + officeRelsNS + `"`
	var main, prefix, imageRels, imageTarget string
	var parts []testPackagePart
	switch format {
	case "docx":
		main, prefix, imageRels, imageTarget = "word/document.xml", "word/", "word/_rels/document.xml.rels", "media/"
		parts = append(parts, testPackagePart{name: main, text: `<w:document` + ns + `><w:body><w:p><w:r><w:drawing><a:cNvPr descr="Diagram"/>` + blip + `</w:drawing></w:r></w:p></w:body></w:document>`})
	case "pptx", "pptx-shape":
		main, prefix, imageRels, imageTarget = "ppt/presentation.xml", "ppt/", "ppt/slides/_rels/slide1.xml.rels", "../media/"
		object := `<p:pic><p:nvPicPr><p:cNvPr descr="Diagram"/></p:nvPicPr><p:blipFill>` + blip + `</p:blipFill></p:pic>`
		if format == "pptx-shape" {
			object = `<p:sp><p:nvSpPr><p:cNvPr descr="Diagram"/></p:nvSpPr><p:spPr><a:blipFill>` + blip + `</a:blipFill></p:spPr></p:sp>`
		}
		parts = append(parts,
			testPackagePart{name: main, text: `<p:presentation` + ns + `/>`},
			testPackagePart{name: "ppt/slides/slide1.xml", text: `<p:sld` + ns + `><p:cSld><p:spTree>` + object + `</p:spTree></p:cSld></p:sld>`})
	case "xlsx":
		main, prefix, imageRels, imageTarget = "xl/workbook.xml", "xl/", "xl/drawings/_rels/drawing1.xml.rels", "../media/"
		parts = append(parts,
			testPackagePart{name: main, text: `<workbook` + ns + `><sheets><sheet name="Images" r:id="rSheet"/></sheets></workbook>`},
			testPackagePart{name: "xl/_rels/workbook.xml.rels", text: `<Relationships xmlns="` + packageRelsNS + `"><Relationship Id="rSheet" Type="` + officeRelsNS + `/worksheet" Target="worksheets/sheet1.xml"/></Relationships>`},
			testPackagePart{name: "xl/worksheets/sheet1.xml", text: `<worksheet` + ns + `><sheetData/><drawing r:id="rDrawing"/></worksheet>`},
			testPackagePart{name: "xl/worksheets/_rels/sheet1.xml.rels", text: `<Relationships xmlns="` + packageRelsNS + `"><Relationship Id="rDrawing" Type="` + officeRelsNS + `/drawing" Target="../drawings/drawing1.xml"/></Relationships>`},
			testPackagePart{name: "xl/drawings/drawing1.xml", text: `<xdr:wsDr xmlns:xdr="http://schemas.openxmlformats.org/drawingml/2006/spreadsheetDrawing"` + ns + `><xdr:oneCellAnchor><xdr:pic><xdr:nvPicPr><xdr:cNvPr descr="Diagram"/></xdr:nvPicPr><xdr:blipFill>` + blip + `</xdr:blipFill></xdr:pic></xdr:oneCellAnchor></xdr:wsDr>`})
	}
	parts = append(parts,
		testPackagePart{name: "[Content_Types].xml", text: strings.ReplaceAll(basicContentTypes(), "</Types>", `<Default Extension="svg" ContentType="image/svg+xml"/><Default Extension="png" ContentType="image/png"/></Types>`)},
		testPackagePart{name: "_rels/.rels", text: rootDocumentRels(main)},
		testPackagePart{name: imageRels, text: `<Relationships xmlns="` + packageRelsNS + `"><Relationship Id="rRaster" Type="` + officeRelsNS + `/image" Target="` + imageTarget + `raster.png"/><Relationship Id="rSVG" Type="` + officeRelsNS + `/image" Target="` + imageTarget + `vector.svg"/><Relationship Id="rUnused" Type="` + officeRelsNS + `/image" Target="` + imageTarget + `unused.png"/></Relationships>`},
		testPackagePart{name: prefix + "media/vector.svg", text: svgImage},
		testPackagePart{name: prefix + "media/unused.png", text: "unused"})
	if raster {
		parts = append(parts, testPackagePart{name: prefix + "media/raster.png", text: "raster"})
	}
	return parts
}

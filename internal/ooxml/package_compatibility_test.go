package ooxml_test

import (
	"strings"
	"testing"

	"github.com/adrianliechti/go-extract/internal/ooxml"
)

func TestStrictOOXMLRelationships(t *testing.T) {
	strict := strings.NewReplacer(
		officeRelsNS, "http://purl.oclc.org/ooxml/officeDocument/relationships",
		"http://schemas.openxmlformats.org/wordprocessingml/2006/main", "http://purl.oclc.org/ooxml/wordprocessingml/main",
		"http://schemas.openxmlformats.org/spreadsheetml/2006/main", "http://purl.oclc.org/ooxml/spreadsheetml/main",
		"http://schemas.openxmlformats.org/presentationml/2006/main", "http://purl.oclc.org/ooxml/presentationml/main",
		"http://schemas.openxmlformats.org/drawingml/2006/main", "http://purl.oclc.org/ooxml/drawingml/main",
		"http://schemas.openxmlformats.org/drawingml/2006/spreadsheetDrawing", "http://purl.oclc.org/ooxml/drawingml/spreadsheetDrawing",
	)
	for _, format := range []string{"docx", "xlsx", "pptx"} {
		t.Run(format, func(t *testing.T) {
			parts := imagePackage(format, `<a:blip r:embed="rRaster"/>`, true)
			// Rename the main part as well: otherwise the conventional-location
			// fallback could conceal a broken Strict root relationship.
			main := map[string]string{"docx": "document.xml", "xlsx": "workbook.xml", "pptx": "presentation.xml"}[format]
			for i := range parts {
				parts[i].name = strings.ReplaceAll(parts[i].name, main, "main.xml")
				parts[i].text = strings.ReplaceAll(strict.Replace(parts[i].text), main, "main.xml")
			}
			doc, err := ooxml.Convert(buildOOXML(t, parts), ooxml.Options{})
			if err != nil {
				t.Fatal(err)
			}
			if len(doc.Images) != 1 || !strings.Contains(doc.Markdown, "![Diagram](raster.png)") {
				t.Fatalf("Strict relationships lost content: %#v", doc)
			}
		})
	}
}

func TestMainPartContentTypeOverridesLocation(t *testing.T) {
	for _, tc := range []struct {
		format, prefix, main, contentType string
		want                              ooxml.Format
	}{
		{"docx", "word/", "document.xml", "application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml", ooxml.FormatDocx},
		{"xlsx", "xl/", "workbook.xml", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml", ooxml.FormatXlsx},
		{"pptx", "ppt/", "presentation.xml", "application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml", ooxml.FormatPptx},
	} {
		t.Run(tc.format, func(t *testing.T) {
			parts := imagePackage(tc.format, `<a:blip r:embed="rRaster"/>`, true)
			for i := range parts {
				parts[i].name = strings.ReplaceAll(parts[i].name, tc.prefix, "content/")
				parts[i].text = strings.ReplaceAll(parts[i].text, tc.prefix, "content/")
				if parts[i].name == "[Content_Types].xml" {
					parts[i].text = strings.ReplaceAll(parts[i].text, "</Types>", `<Override PartName="/content/`+tc.main+`" ContentType="`+tc.contentType+`"/></Types>`)
				}
			}
			data := buildOOXML(t, parts)
			if got, err := ooxml.Detect(data); err != nil || got != tc.want {
				t.Fatalf("Detect = %v, %v; want %v", got, err, tc.want)
			}
			doc, err := ooxml.Convert(data, ooxml.Options{})
			if err != nil {
				t.Fatal(err)
			}
			if doc.Format != tc.want || len(doc.Images) != 1 {
				t.Fatalf("relocated main part lost content: %#v", doc)
			}
		})
	}
}

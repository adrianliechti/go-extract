package ooxml_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrianliechti/go-extract"
)

func TestOfficeTextCompatibilityEndToEnd(t *testing.T) {
	wordBody := `<mc:AlternateContent xmlns:mc="http://schemas.openxmlformats.org/markup-compatibility/2006" xmlns:new="urn:unsupported"><mc:Choice Requires="new"><w:p><w:r><w:t>Hidden branch</w:t></w:r></w:p></mc:Choice><mc:Fallback>` +
		listParagraph("1", 0, "First") + listParagraph("1", 0, "Second") + `</mc:Fallback></mc:AlternateContent>`
	word := []testPackagePart{
		{name: "[Content_Types].xml", text: basicContentTypes()},
		{name: "_rels/.rels", text: rootDocumentRels("WORD/%64OCUMENT.xml")},
		{name: "word/document.xml", text: `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` + wordBody + `</w:body></w:document>`},
		{name: "word/_rels/document.xml.rels", text: `<Relationships><Relationship Id="n" Type="` + officeRelsNS + `/numbering" Target="%2E%2E/../%6Eumbering.xml"/></Relationships>`},
		numberingPart(`<w:abstractNum w:abstractNumId="0"><w:lvl w:ilvl="0"><w:numFmt w:val="decimal"/></w:lvl></w:abstractNum><w:num w:numId="1"><w:abstractNumId w:val="0"/><w:lvlOverride w:ilvl="0"><w:startOverride w:val="7"/></w:lvlOverride></w:num>`),
	}
	excel := []testPackagePart{
		{name: "[Content_Types].xml", text: basicContentTypes()},
		{name: "_rels/.rels", text: rootDocumentRels("xl/workbook.xml")},
		{name: "xl/workbook.xml", text: `<workbook xmlns:r="` + officeRelsNS + `"><sheets><sheet name="Times" r:id="s"/></sheets></workbook>`},
		{name: "xl/_rels/workbook.xml.rels", text: `<Relationships><Relationship Id="s" Type="` + officeRelsNS + `/worksheet" Target="worksheets/%73HEET1.xml"/><Relationship Id="styles" Type="` + officeRelsNS + `/styles" Target="./styles.xml#formats"/></Relationships>`},
		{name: "xl/styles.xml", text: `<styleSheet><numFmts><numFmt numFmtId="164" formatCode="[&gt;=1]0.00;h:mm"/><numFmt numFmtId="165" formatCode="[h]:mm:ss"/></numFmts><cellXfs><xf numFmtId="164"/><xf numFmtId="165"/><xf numFmtId="14"/><xf numFmtId="22"/></cellXfs></styleSheet>`},
		{name: "xl/worksheets/sheet1.xml", text: `<worksheet><sheetData><row r="1"><c r="A1" t="inlineStr"><is><t>Value</t></is></c></row><row r="2"><c r="A2" s="0"><v>0.5</v></c></row><row r="3"><c r="A3" s="0"><v>100</v></c></row><row r="4"><c r="A4" s="1"><v>1.5</v></c></row><row r="5"><c r="A5" s="2"><f>TODAY()</f><v>45292</v></c></row><row r="6"><c r="A6" s="3"><f>NOW()</f><v>45292.5</v></c></row></sheetData></worksheet>`},
	}
	for _, tc := range []struct {
		format string
		parts  []testPackagePart
		want   string
	}{
		{"docx", word, "7. First\n8. Second\n"},
		{"xlsx", excel, "## Times\n\n| Value               |\n| ------------------- |\n| 12:00:00            |\n| 100                 |\n| 36:00:00            |\n| 2024-01-01          |\n| 2024-01-01 12:00:00 |\n"},
	} {
		t.Run(tc.format, func(t *testing.T) {
			data := buildOOXML(t, tc.parts)
			for _, route := range []string{"bytes", "file", "archive"} {
				t.Run(route, func(t *testing.T) {
					var doc *extract.Document
					var err error
					switch route {
					case "bytes":
						doc, err = extract.Bytes(context.Background(), data, extract.Options{})
					case "file":
						path := filepath.Join(t.TempDir(), "sample."+tc.format)
						if err = os.WriteFile(path, data, 0o600); err == nil {
							doc, err = extract.File(context.Background(), path, extract.Options{})
						}
					case "archive":
						archive := buildOOXML(t, []testPackagePart{{name: "sample." + tc.format, data: data}})
						doc, err = extract.Extract(context.Background(), extract.Input{Name: "documents.zip", Data: archive}, extract.Options{})
						if err == nil {
							if len(doc.Attachments) != 1 || doc.Attachments[0].Document == nil {
								t.Fatalf("Office attachment was not extracted: %#v", doc.Attachments)
							}
							doc = doc.Attachments[0].Document
						}
					}
					if err != nil {
						t.Fatal(err)
					}
					if string(doc.Format) != tc.format || doc.Markdown != tc.want {
						t.Fatalf("format=%q Markdown=%q, want %q", doc.Format, doc.Markdown, tc.want)
					}
				})
			}
		})
	}
}

func TestOfficeEquivalentImageTargetsAreDeduplicated(t *testing.T) {
	parts := imagePackage("docx", `<a:blip r:embed="rSVG"/><a:blip r:embed="alias"/>`, false)
	for i := range parts {
		if parts[i].name == "word/_rels/document.xml.rels" {
			parts[i].text = strings.ReplaceAll(parts[i].text, "</Relationships>", `<Relationship Id="alias" Type="`+officeRelsNS+`/image" Target="/WORD/media/%76ECTOR.svg"/></Relationships>`)
		}
	}
	doc, err := extract.Bytes(context.Background(), buildOOXML(t, parts), extract.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Attachments) != 1 || strings.Count(doc.Markdown, "(vector.svg)") != 2 {
		t.Fatalf("equivalent images were not shared: attachments=%d Markdown=%q", len(doc.Attachments), doc.Markdown)
	}
}

// Exercise ZIP detection, relationship traversal, Markdown and attachment
// emission through the public API, using entirely synthetic Office files.
// Reference: office-open-xml-viewer 481504ec through 9a8729ed.
func TestOfficeRelationshipCompatibilityEndToEnd(t *testing.T) {
	for _, format := range []string{"docx", "xlsx", "pptx", "pptx-shape"} {
		t.Run(format, func(t *testing.T) {
			parts := imagePackage(format, svgBlip(""), false)
			for i := range parts {
				if strings.HasSuffix(parts[i].name, ".rels") {
					parts[i].text = strings.ReplaceAll(parts[i].text, "vector.svg", "%76ECTOR.svg#view")
				}
				parts[i].name = strings.ReplaceAll(parts[i].name, "vector.svg", "vector.%73vg")
			}
			data := buildOOXML(t, parts)
			path := filepath.Join(t.TempDir(), "sample."+strings.TrimSuffix(format, "-shape"))
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			for _, route := range []string{"bytes", "file"} {
				t.Run(route, func(t *testing.T) {
					var doc *extract.Document
					var err error
					if route == "bytes" {
						doc, err = extract.Bytes(context.Background(), data, extract.Options{})
					} else {
						doc, err = extract.File(context.Background(), path, extract.Options{})
					}
					if err != nil {
						t.Fatal(err)
					}
					if string(doc.Format) != strings.TrimSuffix(format, "-shape") || strings.Count(doc.Markdown, "![Diagram](vector.svg)") != 1 {
						t.Fatalf("format=%q Markdown=%q", doc.Format, doc.Markdown)
					}
					if len(doc.Attachments) != 1 || doc.Attachments[0].MediaType != "image/svg+xml" || string(doc.Attachments[0].Data) != svgImage {
						t.Fatalf("attachments = %#v, want one SVG", doc.Attachments)
					}
				})
			}
		})
	}
}

func TestDocxAuthoredMissingStylesDoNotUseUnrelatedFallback(t *testing.T) {
	for _, target := range []string{`Target="missing.xml"`, `Target="styles.xml" TargetMode="External"`, `Target="%GG/../styles.xml"`} {
		t.Run(target, func(t *testing.T) {
			doc := convertDocxBody(t, `<w:p><w:r><w:t>Plain</w:t></w:r></w:p>`,
				testPackagePart{name: "word/styles.xml", text: `<w:styles xmlns:w="urn:w"><w:docDefaults><w:rPrDefault><w:rPr><w:b/></w:rPr></w:rPrDefault></w:docDefaults></w:styles>`},
				testPackagePart{name: "word/_rels/document.xml.rels", text: `<Relationships xmlns="` + packageRelsNS + `"><Relationship Id="styles" Type="` + officeRelsNS + `/styles" ` + target + `/></Relationships>`},
			)
			if doc.Markdown != "Plain\n" {
				t.Fatalf("unrelated fallback styles changed text: %q", doc.Markdown)
			}
		})
	}
}

func TestOfficeExternalOrForeignStoryRelationshipsAreIgnored(t *testing.T) {
	for _, format := range []string{"xlsx", "pptx"} {
		for _, foreign := range []bool{false, true} {
			t.Run(format+"/"+map[bool]string{false: "external", true: "foreign type"}[foreign], func(t *testing.T) {
				parts := imagePackage(format, svgBlip(""), false)
				kind := map[string]string{"xlsx": "worksheet", "pptx": "slide"}[format]
				for i := range parts {
					if strings.Contains(parts[i].text, officeRelsNS+"/"+kind+`"`) {
						if foreign {
							parts[i].text = strings.ReplaceAll(parts[i].text, officeRelsNS+"/"+kind, "urn:foreign/"+kind)
						} else {
							parts[i].text = strings.ReplaceAll(parts[i].text, `Target=`, `TargetMode="External" Target=`)
						}
					}
				}
				doc, err := extract.Bytes(context.Background(), buildOOXML(t, parts), extract.Options{})
				if err != nil {
					t.Fatal(err)
				}
				if len(doc.Attachments) != 0 || strings.Contains(doc.Markdown, "![") {
					t.Fatalf("invalid story relationship loaded content: %q", doc.Markdown)
				}
			})
		}
	}
}

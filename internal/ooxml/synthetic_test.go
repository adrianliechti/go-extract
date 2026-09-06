package ooxml_test

import (
	"archive/zip"
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/adrianliechti/go-extract/internal/ooxml"
)

func TestConvertAppliesPublicArchiveLimits(t *testing.T) {
	data := buildOOXML(t, []testPackagePart{
		{name: "[Content_Types].xml", text: basicContentTypes()},
		{name: "_rels/.rels", text: rootDocumentRels("word/document.xml")},
		{name: "word/document.xml", text: `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body/></w:document>`},
	})

	_, err := ooxml.Convert(data, ooxml.Options{MaxArchiveEntries: 2})
	if !errors.Is(err, ooxml.ErrResourceLimit) {
		t.Fatalf("Convert error = %v, want ErrResourceLimit", err)
	}
}

const (
	packageRelsNS = "http://schemas.openxmlformats.org/package/2006/relationships"
	officeRelsNS  = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
	contentTypeNS = "http://schemas.openxmlformats.org/package/2006/content-types"
)

func TestSyntheticDocxContentAndMedia(t *testing.T) {
	data := buildOOXML(t, []testPackagePart{
		{name: "[Content_Types].xml", text: `<Types xmlns="` + contentTypeNS + `"><Default Extension="xml" ContentType="application/xml"/><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="png" ContentType="image/png"/></Types>`},
		{name: "_rels/.rels", text: `<Relationships xmlns="` + packageRelsNS + `"><Relationship Id="rDoc" Type="` + officeRelsNS + `/officeDocument" Target="word/document.xml"/><Relationship Id="rCore" Type="` + packageRelsNS + `/metadata/core-properties" Target="docProps/core.xml"/></Relationships>`},
		{name: "docProps/core.xml", text: `<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>Synthetic report</dc:title></cp:coreProperties>`},
		{name: "word/document.xml", text: `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="` + officeRelsNS + `" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing"><w:body>
<w:p><w:r><w:t xml:space="preserve">Keep </w:t></w:r><w:ins><w:r><w:t>added</w:t></w:r></w:ins><w:moveFrom><w:r><w:t> old place</w:t></w:r></w:moveFrom><w:moveTo><w:r><w:t> moved here</w:t></w:r></w:moveTo><w:del><w:r><w:delText> removed</w:delText></w:r></w:del><w:hyperlink r:id="rLink"><w:r><w:t xml:space="preserve"> site</w:t></w:r></w:hyperlink></w:p>
<w:p><w:r><w:drawing><wp:inline><wp:docPr id="1" descr="Architecture diagram"/><a:graphic><a:graphicData><a:blip r:embed="rImage"/></a:graphicData></a:graphic></wp:inline></w:drawing></w:r></w:p>
</w:body></w:document>`},
		{name: "word/_rels/document.xml.rels", text: `<Relationships xmlns="` + packageRelsNS + `"><Relationship Id="rLink" Type="` + officeRelsNS + `/hyperlink" Target="https://example.com/reference page" TargetMode="External"/><Relationship Id="rImage" Type="` + officeRelsNS + `/image" Target="media/diagram.png"/></Relationships>`},
		{name: "word/media/diagram.png", data: []byte("synthetic-png")},
	})

	doc, err := ooxml.Convert(data, ooxml.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if doc.Title != "Synthetic report" {
		t.Fatalf("Title = %q, want Synthetic report", doc.Title)
	}
	if !strings.Contains(doc.Markdown, "Keep added moved here [site](https://example.com/reference%20page)") {
		t.Fatalf("Markdown did not preserve final-view text and hyperlink:\n%s", doc.Markdown)
	}
	if strings.Contains(doc.Markdown, "old place") || strings.Contains(doc.Markdown, "removed") {
		t.Fatalf("Markdown contains revision text hidden from final view:\n%s", doc.Markdown)
	}
	if !strings.Contains(doc.Markdown, "![Architecture diagram](diagram.png)") {
		t.Fatalf("Markdown image missing:\n%s", doc.Markdown)
	}
	if len(doc.Images) != 1 || doc.Images[0].ContentType != "image/png" || string(doc.Images[0].Data) != "synthetic-png" {
		t.Fatalf("Images = %#v, want one extracted PNG", doc.Images)
	}
}

func TestSyntheticXlsxHyperlinksMergesAndHiddenSheets(t *testing.T) {
	data := buildOOXML(t, []testPackagePart{
		{name: "[Content_Types].xml", text: basicContentTypes()},
		{name: "_rels/.rels", text: rootDocumentRels("xl/workbook.xml")},
		{name: "xl/workbook.xml", text: `<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="` + officeRelsNS + `"><sheets><sheet name="Visible" sheetId="1" r:id="rVisible"/><sheet name="Hidden" sheetId="2" state="hidden" r:id="rHidden"/></sheets></workbook>`},
		{name: "xl/_rels/workbook.xml.rels", text: `<Relationships xmlns="` + packageRelsNS + `"><Relationship Id="rVisible" Type="` + officeRelsNS + `/worksheet" Target="worksheets/visible.xml"/><Relationship Id="rHidden" Type="` + officeRelsNS + `/worksheet" Target="worksheets/hidden.xml"/></Relationships>`},
		{name: "xl/worksheets/visible.xml", text: `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="` + officeRelsNS + `"><sheetData>
<row r="1"><c r="A1" t="inlineStr"><is><t>Name</t></is></c><c r="B1" t="inlineStr"><is><t>Website</t></is></c><c r="C1" t="inlineStr"><is><t>Jump</t></is></c></row>
<row r="3"><c r="A3" t="inlineStr"><is><t>Merged value</t></is></c><c r="B3" t="inlineStr"><is><t>stale continuation</t></is></c><c r="C3"/></row>
</sheetData><mergeCells count="1"><mergeCell ref="A3:B3"/></mergeCells><hyperlinks><hyperlink ref="B1" r:id="rWeb"/><hyperlink ref="C1" location="'Hidden'!A1"/><hyperlink ref="C3" location="Visible!A1" display="Back to top"/></hyperlinks></worksheet>`},
		{name: "xl/worksheets/_rels/visible.xml.rels", text: `<Relationships xmlns="` + packageRelsNS + `"><Relationship Id="rWeb" Type="` + officeRelsNS + `/hyperlink" Target="https://example.com/docs home" TargetMode="External"/></Relationships>`},
		{name: "xl/worksheets/hidden.xml", text: `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData><row r="1"><c r="A1" t="inlineStr"><is><t>Secret sheet value</t></is></c></row></sheetData></worksheet>`},
	})

	doc, err := ooxml.Convert(data, ooxml.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(doc.SheetNames, []string{"Visible"}) {
		t.Fatalf("SheetNames = %#v, want only visible sheet", doc.SheetNames)
	}
	if !strings.Contains(doc.Markdown, "[Website](https://example.com/docs%20home)") {
		t.Fatalf("external cell hyperlink missing:\n%s", doc.Markdown)
	}
	if !strings.Contains(doc.Markdown, "[Jump](#'Hidden'!A1)") || !strings.Contains(doc.Markdown, "[Back to top](#Visible!A1)") {
		t.Fatalf("internal or display-only cell hyperlink missing:\n%s", doc.Markdown)
	}
	if strings.Contains(doc.Markdown, "stale continuation") {
		t.Fatalf("sparse-row merged continuation was not suppressed:\n%s", doc.Markdown)
	}
	if strings.Contains(doc.Markdown, "Secret sheet value") {
		t.Fatalf("hidden worksheet leaked into default output:\n%s", doc.Markdown)
	}

	withHidden, err := ooxml.Convert(data, ooxml.Options{IncludeHidden: true})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(withHidden.SheetNames, []string{"Visible", "Hidden"}) {
		t.Fatalf("SheetNames with IncludeHidden = %#v", withHidden.SheetNames)
	}
	if !strings.Contains(withHidden.Markdown, "Secret sheet value") {
		t.Fatalf("IncludeHidden did not emit hidden worksheet:\n%s", withHidden.Markdown)
	}
}

func TestSyntheticPptxVisibilityBulletsNotesAndMedia(t *testing.T) {
	data := buildOOXML(t, []testPackagePart{
		{name: "[Content_Types].xml", text: `<Types xmlns="` + contentTypeNS + `"><Default Extension="xml" ContentType="application/xml"/><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="png" ContentType="image/png"/></Types>`},
		{name: "_rels/.rels", text: rootDocumentRels("ppt/presentation.xml")},
		{name: "ppt/presentation.xml", text: `<p:presentation xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:r="` + officeRelsNS + `"><p:sldIdLst><p:sldId id="256" r:id="rSlide1"/><p:sldId id="257" r:id="rSlide2"/></p:sldIdLst></p:presentation>`},
		{name: "ppt/_rels/presentation.xml.rels", text: `<Relationships xmlns="` + packageRelsNS + `"><Relationship Id="rSlide1" Type="` + officeRelsNS + `/slide" Target="slides/slide1.xml"/><Relationship Id="rSlide2" Type="` + officeRelsNS + `/slide" Target="slides/slide2.xml"/></Relationships>`},
		{name: "ppt/slides/slide1.xml", text: visibleSlideXML()},
		{name: "ppt/slides/_rels/slide1.xml.rels", text: `<Relationships xmlns="` + packageRelsNS + `"><Relationship Id="rNotes" Type="` + officeRelsNS + `/notesSlide" Target="../notesSlides/notesSlide1.xml"/><Relationship Id="rImage" Type="` + officeRelsNS + `/image" Target="../media/diagram.png"/></Relationships>`},
		{name: "ppt/notesSlides/notesSlide1.xml", text: `<p:notes xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"><p:cSld><p:spTree><p:sp><p:nvSpPr><p:cNvPr id="1" name="Notes"/><p:cNvSpPr/><p:nvPr/></p:nvSpPr><p:txBody><a:p><a:r><a:t>Speaker detail</a:t></a:r></a:p></p:txBody></p:sp></p:spTree></p:cSld></p:notes>`},
		{name: "ppt/slides/slide2.xml", text: `<p:sld xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" show="false"><p:cSld><p:spTree><p:sp><p:nvSpPr><p:cNvPr id="1" name="Hidden title"/><p:cNvSpPr/><p:nvPr><p:ph type="title"/></p:nvPr></p:nvSpPr><p:txBody><a:p><a:r><a:t>Appendix secret</a:t></a:r></a:p></p:txBody></p:sp></p:spTree></p:cSld></p:sld>`},
		{name: "ppt/media/diagram.png", data: []byte("slide-png")},
	})

	doc, err := ooxml.Convert(data, ooxml.Options{SlideNotes: true})
	if err != nil {
		t.Fatal(err)
	}
	if doc.SlideCount != 1 {
		t.Fatalf("SlideCount = %d, want 1 visible slide", doc.SlideCount)
	}
	for _, want := range []string{"## Quarterly update", "Plain text box", "- Explicit text-box bullet", "- Inherited bullet", "3. Third item", "4. Fourth item", "Closing prose", "> **Notes:** Speaker detail", "![Architecture diagram](diagram.png)"} {
		if !strings.Contains(doc.Markdown, want) {
			t.Errorf("Markdown missing %q:\n%s", want, doc.Markdown)
		}
	}
	if strings.Contains(doc.Markdown, "Hidden shape text") || strings.Contains(doc.Markdown, "Appendix secret") {
		t.Fatalf("hidden PPTX content leaked into default output:\n%s", doc.Markdown)
	}
	if len(doc.Images) != 1 || string(doc.Images[0].Data) != "slide-png" {
		t.Fatalf("Images = %#v, want one slide image", doc.Images)
	}

	withHidden, err := ooxml.Convert(data, ooxml.Options{IncludeHidden: true})
	if err != nil {
		t.Fatal(err)
	}
	if withHidden.SlideCount != 2 || !strings.Contains(withHidden.Markdown, "Appendix secret") {
		t.Fatalf("IncludeHidden did not include hidden slide: count=%d\n%s", withHidden.SlideCount, withHidden.Markdown)
	}
}

func visibleSlideXML() string {
	return `<p:sld xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="` + officeRelsNS + `"><p:cSld><p:spTree>
<p:sp><p:nvSpPr><p:cNvPr id="1" name="Title"/><p:cNvSpPr/><p:nvPr><p:ph type="title"/></p:nvPr></p:nvSpPr><p:spPr><a:xfrm><a:off x="0" y="0"/></a:xfrm></p:spPr><p:txBody><a:p><a:r><a:t>Quarterly update</a:t></a:r></a:p></p:txBody></p:sp>
<p:sp><p:nvSpPr><p:cNvPr id="2" name="Text Box"/><p:cNvSpPr txBox="1"/><p:nvPr/></p:nvSpPr><p:spPr><a:xfrm><a:off x="0" y="100"/></a:xfrm></p:spPr><p:txBody><a:p><a:r><a:t>Plain text box</a:t></a:r></a:p><a:p><a:pPr><a:buChar char="•"/></a:pPr><a:r><a:t>Explicit text-box bullet</a:t></a:r></a:p></p:txBody></p:sp>
<p:sp><p:nvSpPr><p:cNvPr id="3" name="Body"/><p:cNvSpPr/><p:nvPr><p:ph type="body"/></p:nvPr></p:nvSpPr><p:spPr><a:xfrm><a:off x="0" y="200"/></a:xfrm></p:spPr><p:txBody>
<a:p><a:r><a:t>Inherited bullet</a:t></a:r></a:p>
<a:p><a:pPr><a:buAutoNum type="arabicPeriod" startAt="3"/></a:pPr><a:r><a:t>Third item</a:t></a:r></a:p>
<a:p><a:pPr><a:buAutoNum type="arabicPeriod"/></a:pPr><a:r><a:t>Fourth item</a:t></a:r></a:p>
<a:p><a:pPr><a:buNone/></a:pPr><a:r><a:t>Closing prose</a:t></a:r></a:p>
</p:txBody></p:sp>
<p:sp><p:nvSpPr><p:cNvPr id="4" name="Hidden" hidden="1"/><p:cNvSpPr/><p:nvPr/></p:nvSpPr><p:txBody><a:p><a:r><a:t>Hidden shape text</a:t></a:r></a:p></p:txBody></p:sp>
<p:pic><p:nvPicPr><p:cNvPr id="5" name="Diagram" descr="Architecture diagram"/><p:cNvPicPr/></p:nvPicPr><p:blipFill><a:blip r:embed="rImage"/></p:blipFill><p:spPr><a:xfrm><a:off x="0" y="400"/></a:xfrm></p:spPr></p:pic>
</p:spTree></p:cSld></p:sld>`
}

type testPackagePart struct {
	name string
	text string
	data []byte
}

func buildOOXML(t *testing.T, parts []testPackagePart) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, part := range parts {
		w, err := zw.Create(part.name)
		if err != nil {
			t.Fatal(err)
		}
		data := part.data
		if data == nil {
			data = []byte(part.text)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func basicContentTypes() string {
	return `<Types xmlns="` + contentTypeNS + `"><Default Extension="xml" ContentType="application/xml"/><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/></Types>`
}

func rootDocumentRels(target string) string {
	return `<Relationships xmlns="` + packageRelsNS + `"><Relationship Id="rDoc" Type="` + officeRelsNS + `/officeDocument" Target="` + target + `"/></Relationships>`
}

package ooxml_test

import (
	"strings"
	"testing"

	"github.com/adrianliechti/go-extract/internal/ooxml"
)

func convertDocxBody(t *testing.T, body string, extra ...testPackagePart) *ooxml.Document {
	t.Helper()
	parts := []testPackagePart{
		{name: "[Content_Types].xml", text: basicContentTypes()},
		{name: "_rels/.rels", text: rootDocumentRels("word/document.xml")},
		{name: "word/document.xml", text: `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` + body + `</w:body></w:document>`},
	}
	doc, err := ooxml.Convert(buildOOXML(t, append(parts, extra...)), ooxml.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

// The reference viewer's same-run field fix is a useful extraction contract:
// retain cached results exactly once, without reordering surrounding text or
// exposing the instructions. Extraction does not recompute page numbers.
func TestDocxComplexFieldsPreserveContentOrder(t *testing.T) {
	const content = `<w:t>Before </w:t><w:fldChar w:fldCharType="begin"/><w:instrText> PAGE </w:instrText><w:fldChar w:fldCharType="separate"/><w:t>7</w:t><w:fldChar w:fldCharType="end"/><w:t> of </w:t><w:fldChar w:fldCharType="begin"/><w:instrText> NUMPAGES </w:instrText><w:fldChar w:fldCharType="separate"/><w:t>12</w:t><w:fldChar w:fldCharType="end"/><w:t> after</w:t>`
	for _, tc := range []struct{ name, content string }{
		{"one run", content},
		{"separate runs", strings.ReplaceAll(content, "><w:", "></w:r><w:r><w:")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := convertDocxBody(t, `<w:p><w:r>`+tc.content+`</w:r></w:p>`)
			if want := "Before 7 of 12 after\n"; doc.Markdown != want {
				t.Fatalf("Markdown = %q, want %q", doc.Markdown, want)
			}
		})
	}
}

func TestDocxNestedTablePreservesCellOrder(t *testing.T) {
	doc := convertDocxBody(t, `<w:tbl><w:tr><w:tc>
<w:p><w:r><w:t>Before</w:t></w:r></w:p>
<w:tbl><w:tr><w:tc><w:p><w:r><w:t>Nested</w:t></w:r></w:p></w:tc></w:tr></w:tbl>
<w:p><w:r><w:t>After</w:t></w:r></w:p>
</w:tc></w:tr></w:tbl>`)
	if !strings.Contains(doc.Markdown, "Before<br>Nested<br>After") {
		t.Fatalf("nested table moved out of document order:\n%s", doc.Markdown)
	}
}

func TestDocxTextboxPreservesNestedTableContent(t *testing.T) {
	doc := convertDocxBody(t, `<w:p><w:r><w:drawing><w:txbxContent><w:tbl><w:tr><w:tc>
<w:p><w:r><w:t>Before</w:t></w:r></w:p>
<w:tbl><w:tr><w:tc><w:p><w:r><w:t>Nested</w:t></w:r></w:p></w:tc></w:tr></w:tbl>
<w:p><w:r><w:t>After</w:t></w:r></w:p>
</w:tc></w:tr></w:tbl></w:txbxContent></w:drawing></w:r></w:p>`)
	if want := "Before\n\nNested\n\nAfter\n"; doc.Markdown != want {
		t.Fatalf("textbox content = %q, want %q", doc.Markdown, want)
	}
}

func TestDocxVerticalMergeWithShortPreviousRow(t *testing.T) {
	doc := convertDocxBody(t, `<w:tbl>
<w:tr><w:tc><w:tcPr><w:vMerge w:val="restart"/></w:tcPr><w:p><w:r><w:t>Anchor</w:t></w:r></w:p></w:tc></w:tr>
<w:tr><w:tc><w:tcPr><w:gridSpan w:val="2"/><w:vMerge/></w:tcPr><w:p/></w:tc></w:tr>
</w:tbl>`)
	if !strings.Contains(doc.Markdown, "Anchor") {
		t.Fatalf("merge lost its available anchor:\n%s", doc.Markdown)
	}
}

func TestDocxListIndentationIsBounded(t *testing.T) {
	for _, inherited := range []bool{false, true} {
		name := "direct"
		props := `<w:numPr><w:ilvl w:val="64"/><w:numId w:val="1"/></w:numPr>`
		var extra []testPackagePart
		if inherited {
			name = "inherited"
			extra = append(extra, testPackagePart{name: "word/styles.xml", text: `<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:style w:styleId="List"><w:pPr>` + props + `</w:pPr></w:style></w:styles>`})
			props = `<w:pStyle w:val="List"/>`
		}
		t.Run(name, func(t *testing.T) {
			doc := convertDocxBody(t, `<w:p><w:pPr>`+props+`</w:pPr><w:r><w:t>Item</w:t></w:r></w:p>`, extra...)
			if want := strings.Repeat("    ", 8) + "- Item\n"; doc.Markdown != want {
				t.Fatalf("Markdown = %q, want %q", doc.Markdown, want)
			}
		})
	}
}

func TestPptxUntitledSlidesStartWithTheirHeading(t *testing.T) {
	parts := []testPackagePart{
		{name: "[Content_Types].xml", text: basicContentTypes()},
		{name: "_rels/.rels", text: rootDocumentRels("ppt/presentation.xml")},
		{name: "ppt/presentation.xml", text: `<p:presentation xmlns:p="urn:p"/>`},
	}
	for _, tc := range []struct{ part, text string }{
		{"ppt/slides/slide1.xml", "First body"},
		{"ppt/slides/slide2.xml", "Second body"},
	} {
		parts = append(parts, testPackagePart{name: tc.part, text: `<p:sld xmlns:p="urn:p" xmlns:a="urn:a"><p:cSld><p:spTree><p:sp><p:txBody><a:p><a:r><a:t>` + tc.text + `</a:t></a:r></a:p></p:txBody></p:sp></p:spTree></p:cSld></p:sld>`})
	}
	doc, err := ooxml.Convert(buildOOXML(t, parts), ooxml.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if want := "## Slide 1\n\nFirst body\n\n## Slide 2\n\nSecond body\n"; doc.Markdown != want {
		t.Fatalf("Markdown = %q, want %q", doc.Markdown, want)
	}
}

func TestXlsxLargeSparseMergeSuppressesOnlyContinuations(t *testing.T) {
	parts := []testPackagePart{
		{name: "[Content_Types].xml", text: basicContentTypes()},
		{name: "_rels/.rels", text: rootDocumentRels("xl/workbook.xml")},
		{name: "xl/workbook.xml", text: `<workbook xmlns:r="` + officeRelsNS + `"><sheets><sheet name="Sparse" r:id="rSheet"/></sheets></workbook>`},
		{name: "xl/_rels/workbook.xml.rels", text: `<Relationships xmlns="` + packageRelsNS + `"><Relationship Id="rSheet" Type="` + officeRelsNS + `/worksheet" Target="worksheets/sheet1.xml"/></Relationships>`},
		{name: "xl/worksheets/sheet1.xml", text: `<worksheet><sheetData>
<row r="1"><c r="A1" t="inlineStr"><is><t>Anchor</t></is></c><c r="D1" t="inlineStr"><is><t>Outside</t></is></c></row>
<row r="1048576"><c r="B1048576" t="inlineStr"><is><t>Stale continuation</t></is></c><c r="D1048576" t="inlineStr"><is><t>Last row</t></is></c></row>
</sheetData><mergeCells><mergeCell ref="A1:C1048576"/></mergeCells></worksheet>`},
	}
	doc, err := ooxml.Convert(buildOOXML(t, parts), ooxml.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"Anchor", "Outside", "Last row"} {
		if !strings.Contains(doc.Markdown, text) {
			t.Errorf("missing %q:\n%s", text, doc.Markdown)
		}
	}
	if strings.Contains(doc.Markdown, "Stale continuation") {
		t.Fatalf("large valid merge was ignored:\n%s", doc.Markdown)
	}

	// A producer may serialize rows out of order. Merge queries must still
	// use worksheet coordinates without reordering the extracted rows.
	part := &parts[len(parts)-1]
	start := strings.Index(part.text, `<row r="1">`)
	middle := strings.Index(part.text, `<row r="1048576">`)
	end := strings.Index(part.text, `</sheetData>`)
	part.text = part.text[:start] + part.text[middle:end] + part.text[start:middle] + part.text[end:]
	doc, err = ooxml.Convert(buildOOXML(t, parts), ooxml.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(doc.Markdown, "Stale continuation") || strings.Index(doc.Markdown, "Last row") > strings.Index(doc.Markdown, "Anchor") {
		t.Fatalf("out-of-order sparse rows changed merge semantics or authored order:\n%s", doc.Markdown)
	}
}

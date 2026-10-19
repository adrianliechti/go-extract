package ooxml_test

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrianliechti/go-extract/internal/ooxml"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
)

// These synthetic cases exercise the contracts fixed in office-open-xml-viewer
// 5e31e9dbd/36b9b281c and Docling ffce1917/04eefdd3/85081410. They do not use
// either converter's output as an oracle: expected text and coordinates come
// from the authored XML, and a separate GFM parser checks Markdown semantics.
func renderGFM(t *testing.T, markdown string) string {
	t.Helper()
	var out bytes.Buffer
	md := goldmark.New(goldmark.WithExtensions(extension.GFM), goldmark.WithRendererOptions(html.WithUnsafe()))
	if err := md.Convert([]byte(markdown), &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestUpstreamDocxLiteralTextAndLinkPolicy(t *testing.T) {
	body := `<w:p><w:r><w:t># Literal heading</w:t></w:r></w:p>
<w:p><w:r><w:t>1</w:t></w:r><w:r><w:t>. split</w:t></w:r></w:p>
<w:p><w:r><w:t>~~literal~~ &amp;copy;</w:t></w:r></w:p>
<w:p><w:hyperlink xmlns:r="` + officeRelsNS + `" r:id="bad"><w:r><w:t>blocked</w:t></w:r></w:hyperlink></w:p>
<w:p><w:r><w:t>!</w:t></w:r><w:hyperlink xmlns:r="` + officeRelsNS + `" r:id="ok"><w:r><w:t>linked</w:t></w:r></w:hyperlink></w:p>`
	rels := testPackagePart{name: "word/_rels/document.xml.rels", text: `<Relationships xmlns="` + packageRelsNS + `"><Relationship Id="bad" Type="` + officeRelsNS + `/hyperlink" TargetMode="External" Target="javascript:alert(1)"/><Relationship Id="ok" Type="` + officeRelsNS + `/hyperlink" TargetMode="External" Target="https://example.com/"/></Relationships>`}
	got := renderGFM(t, convertDocxBody(t, body, rels).Markdown)
	want := "<p># Literal heading</p>\n<p>1. split</p>\n<p>~~literal~~ &amp;copy;</p>\n<p>blocked</p>\n<p>!<a href=\"https://example.com/\">linked</a></p>\n"
	if got != want {
		t.Fatalf("rendered document:\n%s\nwant:\n%s", got, want)
	}
}

func TestUpstreamLinkedNumbering(t *testing.T) {
	for _, tc := range []struct{ name, link, override, want string }{
		{"linked", "Linked", "", "7. Item\n"},
		{"override", "Linked", `<w:lvlOverride w:ilvl="0"><w:startOverride w:val="3"/></w:lvlOverride>`, "3. Item\n"},
		{"cycle", "Cycle", "", "- Item\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			num := `<w:abstractNum w:abstractNumId="1"><w:numStyleLink w:val="` + tc.link + `"/></w:abstractNum><w:abstractNum w:abstractNumId="2"><w:lvl w:ilvl="0"><w:start w:val="7"/><w:numFmt w:val="decimal"/></w:lvl></w:abstractNum><w:num w:numId="1"><w:abstractNumId w:val="1"/>` + tc.override + `</w:num><w:num w:numId="2"><w:abstractNumId w:val="2"/></w:num>`
			styles := testPackagePart{name: "word/styles.xml", text: `<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:style w:type="numbering" w:styleId="Linked"><w:pPr><w:numPr><w:numId w:val="2"/></w:numPr></w:pPr></w:style><w:style w:type="numbering" w:styleId="Cycle"><w:pPr><w:numPr><w:numId w:val="1"/></w:numPr></w:pPr></w:style></w:styles>`}
			if got := convertDocxBody(t, listParagraph("1", 0, "Item"), numberingPart(num), styles).Markdown; got != tc.want {
				t.Fatalf("Markdown = %q, want %q", got, tc.want)
			}
		})
	}
}

func upstreamSlide(t *testing.T, shapes string, extra ...testPackagePart) *ooxml.Document {
	t.Helper()
	parts := []testPackagePart{
		{name: "[Content_Types].xml", text: basicContentTypes()},
		{name: "_rels/.rels", text: rootDocumentRels("ppt/presentation.xml")},
		{name: "ppt/presentation.xml", text: `<p:presentation xmlns:p="urn:p"/>`},
		{name: "ppt/slides/slide1.xml", text: `<p:sld xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="` + officeRelsNS + `"><p:cSld><p:spTree>` + shapes + `</p:spTree></p:cSld></p:sld>`},
	}
	doc, err := ooxml.Convert(buildOOXML(t, append(parts, extra...)), ooxml.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func upstreamShape(text, y, link string) string {
	return `<p:sp><p:nvSpPr><p:cNvPr id="1" name="Text">` + link + `</p:cNvPr><p:cNvSpPr txBox="1"/><p:nvPr/></p:nvSpPr><p:spPr><a:xfrm><a:off x="0" y="` + y + `"/><a:ext cx="100" cy="100"/></a:xfrm></p:spPr><p:txBody><a:p><a:r><a:t>` + text + `</a:t></a:r></a:p></p:txBody></p:sp>`
}

func TestUpstreamPptxGroupCoordinatesAndShapeLink(t *testing.T) {
	group := `<p:grpSp><p:grpSpPr><a:xfrm><a:off x="0" y="900"/><a:ext cx="200" cy="200"/><a:chOff x="0" y="0"/><a:chExt cx="100" cy="100"/></a:xfrm></p:grpSpPr>` + upstreamShape("Bottom", "0", "") + `</p:grpSp>`
	rels := testPackagePart{name: "ppt/slides/_rels/slide1.xml.rels", text: `<Relationships xmlns="` + packageRelsNS + `"><Relationship Id="link" Type="` + officeRelsNS + `/hyperlink" Target="https://example.com/" TargetMode="External"/></Relationships>`}
	doc := upstreamSlide(t, group+upstreamShape("Top", "100", `<a:hlinkClick r:id="link"/>`), rels)
	if want := "## Slide 1\n\n[Top](https://example.com/)\n\nBottom\n"; doc.Markdown != want {
		t.Fatalf("Markdown = %q, want %q", doc.Markdown, want)
	}
}

func TestUpstreamPptxLiteralText(t *testing.T) {
	doc := upstreamSlide(t, upstreamShape("&lt;b&gt;literal&lt;/b&gt; **plain**", "0", ""))
	got := renderGFM(t, doc.Markdown)
	if !strings.Contains(got, "<p>&lt;b&gt;literal&lt;/b&gt; **plain**</p>") {
		t.Fatalf("document text became markup:\n%s", got)
	}
}

func TestUpstreamDocxHeadingTableAndSplitIndentation(t *testing.T) {
	body := `<w:p><w:pPr><w:outlineLvl w:val="0"/></w:pPr><w:r><w:t>Topic ###</w:t><w:br/><w:t>continued</w:t></w:r></w:p>
<w:p><w:r><w:t xml:space="preserve">  </w:t></w:r><w:r><w:t xml:space="preserve">  &lt;b&gt;literal&lt;/b&gt;</w:t></w:r></w:p>
<w:tbl><w:tr><w:tc><w:p><w:r><w:rPr><w:b/></w:rPr><w:t>a|b</w:t><w:br/><w:t># literal</w:t></w:r></w:p></w:tc></w:tr></w:tbl>`
	got := renderGFM(t, convertDocxBody(t, body).Markdown)
	for _, want := range []string{"<h1>Topic ### continued</h1>", "&lt;b&gt;literal&lt;/b&gt;", "<strong>a|b # literal</strong>"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "<pre>") || strings.Count(got, "<th>") != 1 {
		t.Fatalf("text introduced structure:\n%s", got)
	}
}

func TestUpstreamPptxRunLinksOverrideShapeLink(t *testing.T) {
	shape := upstreamShape("outer", "0", `<a:hlinkClick r:id="outer"/>`)
	shape = strings.Replace(shape, "</a:p>", `<a:r><a:rPr><a:hlinkClick r:id="inner"/></a:rPr><a:t>inner</a:t></a:r><a:r><a:rPr><a:hlinkClick r:id="jump" action="ppaction://hlinksldjump"/></a:rPr><a:t>jump</a:t></a:r></a:p>`, 1)
	rels := testPackagePart{name: "ppt/slides/_rels/slide1.xml.rels", text: `<Relationships><Relationship Id="outer" Type="` + officeRelsNS + `/hyperlink" TargetMode="External" Target="https://example.com/outer"/><Relationship Id="inner" Type="` + officeRelsNS + `/hyperlink" TargetMode="External" Target="https://example.com/inner"/><Relationship Id="jump" Type="` + officeRelsNS + `/slide" Target="slide2.xml"/></Relationships>`}
	got := upstreamSlide(t, shape, rels).Markdown
	if want := "## Slide 1\n\n[outer](https://example.com/outer)[inner](https://example.com/inner)jump\n"; got != want {
		t.Fatalf("Markdown = %q, want %q", got, want)
	}
}

func TestUpstreamNestedFlippedGroups(t *testing.T) {
	// Inner coordinates 10..110 are shifted to 200..300; the outer vertical
	// flip maps them to 700..800. An ordinary shape at 600 must precede it.
	inner := `<p:grpSp><p:grpSpPr><a:xfrm><a:off x="0" y="200"/><a:ext cx="100" cy="100"/><a:chOff x="0" y="10"/><a:chExt cx="100" cy="100"/></a:xfrm></p:grpSpPr>` + upstreamShape("Nested", "10", "") + `</p:grpSp>`
	outer := `<p:grpSp><p:grpSpPr><a:xfrm flipV="1"><a:off x="0" y="0"/><a:ext cx="1000" cy="1000"/><a:chOff x="0" y="0"/><a:chExt cx="1000" cy="1000"/></a:xfrm></p:grpSpPr>` + inner + `</p:grpSp>`
	hidden := `<p:grpSp><p:nvGrpSpPr><p:cNvPr hidden="1"/></p:nvGrpSpPr>` + upstreamShape("Hidden", "0", "") + `</p:grpSp>`
	got := upstreamSlide(t, outer+hidden+upstreamShape("Plain", "600", "")).Markdown
	if want := "## Slide 1\n\nPlain\n\nNested\n"; got != want {
		t.Fatalf("Markdown = %q, want %q", got, want)
	}
}

func upstreamWorkbook(sheet string, extra ...testPackagePart) []testPackagePart {
	return append([]testPackagePart{
		{name: "[Content_Types].xml", text: basicContentTypes()},
		{name: "_rels/.rels", text: rootDocumentRels("xl/workbook.xml")},
		{name: "xl/workbook.xml", text: `<workbook xmlns:r="` + officeRelsNS + `"><sheets><sheet name="Data ###" r:id="s"/></sheets></workbook>`},
		{name: "xl/_rels/workbook.xml.rels", text: `<Relationships><Relationship Id="s" Type="` + officeRelsNS + `/worksheet" Target="worksheets/sheet1.xml"/></Relationships>`},
		{name: "xl/worksheets/sheet1.xml", text: sheet},
	}, extra...)
}

func TestUpstreamXlsxLiteralCellsAndAuthoredFormatting(t *testing.T) {
	parts := upstreamWorkbook(`<worksheet xmlns:r="`+officeRelsNS+`"><sheetData><row><c r="A1" t="inlineStr" s="0"><is><t>&lt;b&gt;literal&lt;/b&gt; **plain** | &amp;copy;</t></is></c><c r="B1" t="inlineStr"><is><t>linked</t></is></c></row></sheetData><hyperlinks><hyperlink ref="B1" r:id="link"/></hyperlinks></worksheet>`,
		testPackagePart{name: "xl/styles.xml", text: `<styleSheet><fonts><font><b/></font></fonts><cellXfs><xf fontId="0"/></cellXfs></styleSheet>`},
		testPackagePart{name: "xl/worksheets/_rels/sheet1.xml.rels", text: `<Relationships><Relationship Id="link" Type="` + officeRelsNS + `/hyperlink" TargetMode="External" Target="https://example.com/a|b"/></Relationships>`})
	doc, err := ooxml.Convert(buildOOXML(t, parts), ooxml.Options{})
	if err != nil {
		t.Fatal(err)
	}
	got := renderGFM(t, doc.Markdown)
	for _, want := range []string{"<h2>Data ###</h2>", "<strong>&lt;b&gt;literal&lt;/b&gt; **plain** | &amp;copy;</strong>", `<a href="https://example.com/a%7Cb">linked</a>`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "<th>") != 2 {
		t.Fatalf("pipe escaped its cell:\n%s", got)
	}
}

func TestUpstreamSharedStringExpansionReturnsResourceError(t *testing.T) {
	// A small ZIP can repeat a large shared string many times. The internal
	// output ceiling must reach the caller as an error, not a partial document.
	rows := strings.Repeat(`<row><c t="s"><v>0</v></c></row>`, 65)
	parts := upstreamWorkbook(`<worksheet><sheetData>`+rows+`</sheetData></worksheet>`, testPackagePart{name: "xl/sharedStrings.xml", text: `<sst><si><t>` + strings.Repeat("x", 1<<20) + `</t></si></sst>`})
	doc, err := ooxml.Convert(buildOOXML(t, parts), ooxml.Options{})
	if !errors.Is(err, ooxml.ErrResourceLimit) || doc != nil {
		t.Fatalf("conversion = %v, %v; want resource error and no partial document", doc, err)
	}
}

// Optional integration with public, tracked fixtures in the two source repos.
// Explicit paths must work; a missing checkout is only skipped when unconfigured.
// These are content checks, not fidelity scores or no-panic-only sweeps.
func TestSourceRepositoryFixtures(t *testing.T) {
	for _, source := range []struct {
		env   string
		files []struct {
			path string
			want []string
		}
	}{
		{"GO_EXTRACT_DOCLING", []struct {
			path string
			want []string
		}{
			{"tests/data/docx/sources/unit_test_formatting.docx", []string{"<em>italic</em>", "<strong>bold</strong>", `href="https://github.com/DS4SD/docling"`}},
			{"tests/data/docx/sources/docx_rich_cells.docx", []string{"<table>", "Column A", "Column B", "Paragraph"}},
			{"tests/data/xlsx/sources/xlsx_01.xlsx", []string{"<table>", "col-1", "col-4"}},
			{"tests/data/pptx/sources/powerpoint_sample.pptx", []string{"Test Table Slide", "<table>", "Class1", "Class2"}},
		}},
		{"GO_EXTRACT_OOXML_VIEWER", []struct {
			path string
			want []string
		}{
			{"packages/docx/public/demo/sample-1.docx", []string{"CANOPY", "Cathedral", "Beneath the Canopy"}},
			{"packages/xlsx/public/demo/sample-1.xlsx", []string{"Evergreen Research Reserve", "<table>", "Regional Overview"}},
			{"packages/pptx/public/demo/sample-1.pptx", []string{"STATE OF THE FOREST", "Annual Ecological Review"}},
		}},
	} {
		t.Run(source.env, func(t *testing.T) {
			root := os.Getenv(source.env)
			if root == "" {
				t.Skip("set " + source.env + " to run source-repository fixtures")
			}
			for _, fixture := range source.files {
				t.Run(fixture.path, func(t *testing.T) {
					doc, err := ooxml.ConvertFile(filepath.Join(root, fixture.path), ooxml.Options{})
					if err != nil {
						t.Fatal(err)
					}
					rendered := renderGFM(t, doc.Markdown)
					for _, want := range fixture.want {
						if !strings.Contains(rendered, want) {
							t.Errorf("missing %q in %s", want, fixture.path)
						}
					}
				})
			}
		})
	}
}

// Unlike the general no-panic corpus sweep, every tracked Office fixture in
// these explicitly configured repos must convert successfully. Content fidelity
// is checked separately by the assertions above and the synthetic regressions.
func TestSourceRepositoryCorpus(t *testing.T) {
	for _, env := range []string{"GO_EXTRACT_DOCLING", "GO_EXTRACT_OOXML_VIEWER"} {
		t.Run(env, func(t *testing.T) {
			root := os.Getenv(env)
			if root == "" {
				t.Skip("set " + env + " to run the complete tracked corpus")
			}
			files, err := exec.Command("git", "-C", root, "ls-files", "-z").Output()
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, name := range strings.Split(string(files), "\x00") {
				switch strings.ToLower(filepath.Ext(name)) {
				case ".docx", ".docm", ".dotx", ".dotm", ".xlsx", ".xlsm", ".xltx", ".xltm", ".pptx", ".pptm", ".ppsx", ".ppsm", ".potx", ".potm":
				default:
					continue
				}
				count++
				t.Run(name, func(t *testing.T) {
					doc, err := ooxml.ConvertFile(filepath.Join(root, name), ooxml.Options{SlideNotes: true})
					if err != nil {
						t.Fatal(err)
					}
					if doc.Format == ooxml.FormatUnknown {
						t.Fatal("format not detected")
					}
				})
			}
			if count == 0 {
				t.Fatal("configured source repository contains no tracked Office files")
			}
			t.Logf("checked %d tracked Office files", count)
		})
	}
}

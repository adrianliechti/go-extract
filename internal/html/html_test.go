package html_test

import (
	"strings"
	"testing"

	"github.com/adrianliechti/go-extract/internal/html"
)

func TestConvertDocumentWithTitleBaseURLAndTable(t *testing.T) {
	source := `<!doctype html>
<html>
  <head><title>Quarterly Report</title><base href="https://example.com/docs/"></head>
  <body>
    <h1>Report</h1>
    <p>An <strong>important</strong> <a href="details">update</a>.</p>
    <table><thead><tr><th>Name</th><th>Value</th></tr></thead><tbody><tr><td>Count</td><td>42</td></tr></tbody></table>
  </body>
</html>`

	doc, err := html.ConvertString(source, html.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if doc.Title != "Quarterly Report" {
		t.Fatalf("Title = %q", doc.Title)
	}
	for _, want := range []string{
		"# Report",
		"**important**",
		"[update](https://example.com/docs/details)",
		"| Name",
		"| Count",
	} {
		if !strings.Contains(doc.Markdown, want) {
			t.Errorf("Markdown missing %q:\n%s", want, doc.Markdown)
		}
	}
}

func TestConvertDecodesDeclaredCharset(t *testing.T) {
	doc, err := html.Convert(
		[]byte("<p>Gr\xfc\xdfe aus Z\xfcrich</p>"),
		html.Options{ContentType: "text/html; charset=iso-8859-1"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Markdown != "Grüße aus Zürich" {
		t.Fatalf("Markdown = %q", doc.Markdown)
	}
}

func TestConvertPreservesStrikethrough(t *testing.T) {
	for _, tag := range []string{"del", "s", "strike"} {
		t.Run(tag, func(t *testing.T) {
			doc, err := html.ConvertString("<p>An <"+tag+">old</"+tag+"> update.</p>", html.Options{})
			if err != nil {
				t.Fatal(err)
			}
			if doc.Markdown != "An ~~old~~ update." {
				t.Fatalf("Markdown = %q", doc.Markdown)
			}
		})
	}
}

func TestConvertPrependsTitleUnlessBodyStartsWithH1(t *testing.T) {
	for _, tc := range []struct {
		name  string
		title string
		body  string
		want  string
	}{
		{"paragraph", "Docs", "<p>Introduction</p>", "# Docs\n\nIntroduction"},
		{"leading H1", "Docs", "<h1>Guide</h1><p>Introduction</p>", "# Guide\n\nIntroduction"},
		{"leading H2", "Docs", "<h2>Details</h2>", "# Docs\n\n## Details"},
		{"later H1", "Docs", "<p>Introduction</p><h1>Guide</h1>", "# Docs\n\nIntroduction\n\n# Guide"},
		{"empty body", "Docs", "", "# Docs"},
		{"no title", "", "<p>Introduction</p>", "Introduction"},
		{"empty title", " \n\t ", "<p>Introduction</p>", "Introduction"},
		{"title whitespace and entities", "  Docs\n &amp;\t Guides  ", "<p>Introduction</p>", "# Docs & Guides\n\nIntroduction"},
		{"removed content before H1", "Docs", "<svg><text>Icon</text></svg><h1>Guide</h1>", "# Guide"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := "<html><head><title>" + tc.title + "</title></head><body>" + tc.body + "</body></html>"
			doc, err := html.ConvertString(source, html.Options{})
			if err != nil {
				t.Fatal(err)
			}
			if doc.Markdown != tc.want {
				t.Fatalf("Markdown = %q, want %q", doc.Markdown, tc.want)
			}
		})
	}
}

func TestConvertRemovesEmbeddedContent(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source string
	}{
		{"svg", `<svg><title>Icon title</title><text>Icon label</text></svg>`},
		{"template", `<template><title>Template title</title><base href="https://noise.example/"><p>Template text</p></template>`},
		{"object", `<object data="widget"><p>Object fallback</p></object>`},
		{"embed", `<embed src="widget" type="application/pdf">`},
		{"script", `<script>alert("noise")</script>`},
		{"style", `<style>body { color: red; }</style>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := html.ConvertString(`<p>Before</p>`+tc.source+`<p><a href="next">After</a></p>`, html.Options{})
			if err != nil {
				t.Fatal(err)
			}
			if doc.Markdown != "Before\n\n[After](next)" || doc.Title != "" {
				t.Fatalf("Document = %#v", doc)
			}
		})
	}
}

func TestDetectHTMLAndRejectPlainText(t *testing.T) {
	if !html.Detect([]byte("<section><p>content</p></section>")) {
		t.Error("HTML fragment was not detected")
	}
	if html.Detect([]byte("plain text with 1 < 2")) {
		t.Error("plain text was detected as HTML")
	}
}

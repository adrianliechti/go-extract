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

func TestDetectHTMLAndRejectPlainText(t *testing.T) {
	if !html.Detect([]byte("<section><p>content</p></section>")) {
		t.Error("HTML fragment was not detected")
	}
	if html.Detect([]byte("plain text with 1 < 2")) {
		t.Error("plain text was detected as HTML")
	}
}

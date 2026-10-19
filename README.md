# go-extract

`go-extract` is a unified, local text-extraction library for:

- PDF
- Word (`.docx`, `.docm`, `.dotx`, `.dotm`)
- Excel (`.xlsx`, `.xlsm`, `.xltx`, `.xltm`)
- PowerPoint (`.pptx`, `.pptm`, `.ppsx`, `.ppsm`, `.potx`, `.potm`)
- Rich Text Format (`.rtf`)
- ZIP, TAR, and GZIP archives, including nested archives
- Plain text and Markdown (`.txt`, `.md`, `.markdown`)
- HTML documents and fragments
- Internet email (`.eml`)
- Outlook messages (`.msg`)

All formats produce Markdown through one interface. Attachments and archive
entries are recursively dispatched through the same extractors, so chains such
as EML → ZIP → TAR/GZIP → Office document yield a document tree.

Office extraction accepts Strict and Transitional relationship namespaces and
uses the main part's content type to recognize packages with nonstandard part
locations. Word output preserves default and inherited heading, list, and run
formatting, including explicit overrides. Embedded images use an available
raster copy or fall back to SVG; Excel drawings are retained even on empty sheets.
Relationship targets support equivalent case and percent encodings. Word selects
supported compatibility branches and honors list start/restart rules. Excel
selects number-format sections by value, emits ISO dates and clock times,
and preserves elapsed durations longer than a day.

Office output escapes literal Markdown and HTML characters while retaining
authored formatting. Hyperlinks allow HTTP, HTTPS, mailto, tel, and relative
targets; other schemes retain their text without a link. Tables use compact
Markdown without column padding. Generated Office Markdown has a fixed 64 MiB
limit, separate from archive limits; exceeding it returns an error rather than
a partial document.

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/adrianliechti/go-extract"
)

func main() {
	doc, err := extract.File(context.Background(), "message.eml", extract.Options{})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Print(doc.Markdown)
	for _, attachment := range doc.Attachments {
		if attachment.Document != nil {
			fmt.Printf("%s -> %s\n", attachment.Name, attachment.Document.Format)
		}
	}
}
```

Use `extract.Bytes` for a document already in memory:

```go
doc, err := extract.Bytes(ctx, data, extract.Options{})
```

For inputs that need filename or media-type hints, such as plain text and
Markdown, use `extract.Extract` with an `extract.Input`:

```go
doc, err := extract.Extract(ctx, extract.Input{
	Name: "notes.md",
	Data: data,
}, extract.Options{})
```

Reuse a configured dispatcher for multiple documents:

```go
dispatcher := extract.New(extract.Options{MaxDepth: 4})
doc, err := dispatcher.File(ctx, "message.eml")
doc, err = dispatcher.Bytes(ctx, data)
```

Recursive extraction is on by default and bounded by depth, document-count,
and per-attachment size limits. Unsupported attachments remain available as
raw `Attachment.Data`; a supported attachment that fails extraction records a
non-fatal `Attachment.Error`. Archive extraction additionally limits entry
count, per-entry inflated bytes, and total inflated bytes.

Configure individual formats through `extract.ArchiveOptions`, `PDFOptions`,
`OOXMLOptions`, `RTFOptions`, `HTMLOptions`, and `MessageOptions`. For example,
resolve relative HTML links against a base URL:

```go
doc, err := extract.Extract(ctx, extract.Input{
	MediaType: "text/html",
	Data:      []byte(`<h1>Report</h1><p><a href="/details">Details</a></p>`),
}, extract.Options{
	HTML: extract.HTMLOptions{BaseURL: "https://example.com/"},
})
```

HTML conversion preserves tables and strikethrough, removes `svg`, `template`,
`object`, and `embed` elements along with scripts and styles, and prepends the
page title as an H1 unless the body already starts with an H1. The title remains
available in `doc.Metadata["title"]`. Pass the HTTP `Content-Type` as
`Input.MediaType` to decode a declared charset; otherwise the encoding is sniffed
from the document.

Custom format adapters implement `extract.Extractor` and are registered in
`extract.Options.Extractors`. They are tried before the built-in extractors.

## Project structure

The root `extract` package is the public API. Format implementations live in
`internal/<format>`, with helpers directly below each format, such as
`internal/ooxml/docx` and `internal/pdf/font`. Shared document types live in
`internal/model` and are exposed through root aliases such as `extract.Document`.

## WebAssembly example

The complete extractor can run locally in a browser through Go WebAssembly.
Build and serve the included file-upload and HTML demo with:

```sh
task --dir examples/wasm serve
```

Then open <http://localhost:8080>. See [`examples/wasm`](examples/wasm) for
details.

Run the synthetic Office end-to-end tests with `task test-ooxml-e2e` (or
`go test ./internal/ooxml -run EndToEnd -count=1`). They exercise DOCX, XLSX,
and PPTX through the public API, including local files, in-memory documents,
embedded images, and Office documents inside ZIP archives.

To also check all tracked Office fixtures in local source repositories, run:

```sh
GO_EXTRACT_DOCLING=/path/to/docling \
GO_EXTRACT_OOXML_VIEWER=/path/to/office-open-xml-viewer \
go test ./internal/ooxml -run TestSourceRepository -count=1
```

These checks require every tracked Office file to convert successfully and
verify selected fixtures' text, tables, formatting, and links. Synthetic
regressions derived from both projects run with the normal test suite.

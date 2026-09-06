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

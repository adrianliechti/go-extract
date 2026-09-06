# WebAssembly example

This example compiles the unified `go-extract` extractor to WebAssembly and
runs it entirely in the browser. The page can convert its built-in HTML sample
or a local PDF, DOCX, XLSX, PPTX, HTML, EML, or MSG file to Markdown.

Build the module, copy Go's matching JavaScript runtime, and start the local
server:

```sh
task --dir examples/wasm serve
```

Then open <http://localhost:8080>. The server is needed instead of opening the
HTML file directly because browsers load WebAssembly over HTTP. Stop it with
<kbd>Ctrl</kbd>+<kbd>C</kbd>.

To create the assets without starting the server:

```sh
task --dir examples/wasm build
```

Generated files are written to `examples/wasm/dist/`. The checked-in page uses
only browser APIs; no JavaScript dependencies or backend extraction service are
required.

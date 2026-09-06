package font

// The static name tables in this package are generated from upstream Rust
// sources: the Adobe Glyph List from pdf-inspector's glyph_names.rs, and the
// CFF standard strings and Macintosh glyph order from the ttf-parser crate.
//
// Both inputs are auto-discovered, so this needs no machine-specific paths.
// Generating requires a pdf-inspector checkout alongside this repo and the
// ttf-parser crate in the cargo registry (run `cargo fetch` there first).
//
// The generated files are committed, so ordinary builds need none of that.
//
//go:generate go run ./gen -out .

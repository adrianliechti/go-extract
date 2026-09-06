package font

// BaseEncoding resolves a PDF base encoding name to its code-to-glyph-name
// table. The recognised names are those the spec permits as a font's /Encoding
// or as an encoding dictionary's /BaseEncoding.
func BaseEncoding(name string) (*[256]string, bool) {
	switch name {
	case "StandardEncoding":
		return &standardEncoding, true
	case "WinAnsiEncoding":
		return &winAnsiEncoding, true
	case "MacRomanEncoding":
		return &macRomanEncoding, true
	case "MacExpertEncoding":
		return &macExpertEncoding, true
	case "PDFDocEncoding":
		return &pdfDocEncoding, true
	}
	return nil, false
}

// StandardEncoding is the implicit encoding for a simple font that names none.
func StandardEncoding() *[256]string { return &standardEncoding }

// SymbolEncoding is the built-in encoding of the Symbol font, used when a
// symbolic font supplies no encoding of its own.
func SymbolEncoding() *[256]string { return &symbolEncoding }

// Package model holds the types shared between the format-specific
// converters and the public API.
package model

// Format identifies which OOXML document type a package holds.
type Format int

// Supported formats.
const (
	FormatUnknown Format = iota
	FormatDocx
	FormatXlsx
	FormatPptx
)

func (f Format) String() string {
	switch f {
	case FormatDocx:
		return "docx"
	case FormatXlsx:
		return "xlsx"
	case FormatPptx:
		return "pptx"
	}
	return "unknown"
}

// Image is an embedded image recovered from a document.
type Image struct {
	// Name is the filename used on disk and in Markdown links. It is unique
	// within a document.
	Name string
	// Part is the original OOXML part name the image came from.
	Part string
	// ContentType is the image media type declared by the package.
	ContentType string
	// Data holds the raw image bytes.
	Data []byte
}

// Document is the result of a conversion.
type Document struct {
	// Format is the detected document type.
	Format Format
	// Markdown is the converted content.
	Markdown string
	// Title comes from the package's core properties when present.
	Title string
	// Images lists every embedded image referenced by the Markdown.
	Images []Image
	// SheetNames lists spreadsheet sheet names, in workbook order. Empty for
	// other formats.
	SheetNames []string
	// SlideCount is the number of slides converted. Zero for other formats.
	SlideCount int
}

// Options configures a format-specific converter.
type Options struct {
	// SkipImages omits images from the output entirely.
	SkipImages bool
	// ImagePrefix is prepended to image filenames in Markdown links.
	ImagePrefix string
	// SheetNames emits a heading per spreadsheet sheet.
	SheetNames bool
	// SlideNotes includes PowerPoint speaker notes.
	SlideNotes bool
	// IncludeHidden includes hidden worksheets and slides.
	IncludeHidden bool
}

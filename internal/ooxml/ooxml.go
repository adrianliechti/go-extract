// Package ooxml converts Office Open XML Word, Excel, and PowerPoint
// documents, templates, and slide shows into Markdown. Both macro-free and
// macro-enabled packages are supported; embedded macros are never executed.
//
// Embedded images can be extracted to a directory and referenced from the
// generated Markdown. The package depends only on the standard library.
package ooxml

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/adrianliechti/go-extract/internal/ooxml/docx"
	"github.com/adrianliechti/go-extract/internal/ooxml/model"
	"github.com/adrianliechti/go-extract/internal/ooxml/opc"
	"github.com/adrianliechti/go-extract/internal/ooxml/pptx"
	"github.com/adrianliechti/go-extract/internal/ooxml/xlsx"
)

// Format identifies which OOXML document type a package holds.
type Format = model.Format

// Supported formats.
const (
	FormatUnknown = model.FormatUnknown
	FormatDocx    = model.FormatDocx
	FormatXlsx    = model.FormatXlsx
	FormatPptx    = model.FormatPptx
)

// Image is an embedded image recovered from a document.
type Image = model.Image

// Document is the result of a conversion.
type Document = model.Document

// Options configures a conversion. The zero value converts to Markdown
// without extracting images, replacing each with a placeholder reference.
type Options struct {
	// ImageDir names a directory to write extracted images into. When empty,
	// image bytes are still returned in Document.Images but nothing is
	// written to disk.
	ImageDir string

	// ImagePrefix is prepended to image filenames in the generated Markdown
	// links. It defaults to the base name of ImageDir when that is set, so
	// the Markdown and the image directory stay relative to each other.
	ImagePrefix string

	// SkipImages omits images from the output entirely.
	SkipImages bool

	// SheetNames is retained for compatibility and no longer changes
	// behaviour: sheet names are always emitted, because a name such as
	// "Duck Observations" is often the only description of the table below it.
	SheetNames bool

	// SlideNotes includes PowerPoint speaker notes after each slide.
	SlideNotes bool

	// IncludeHidden includes worksheets and slides marked hidden. By default,
	// conversion follows the visible workbook or slide-show content.
	IncludeHidden bool

	// Archive limits may be raised for unusually large documents, up to
	// non-disableable internal hard ceilings.
	//
	// MaxArchiveEntryBytes bounds the inflated size of one ZIP part. Zero uses
	// the safe default of 128 MiB.
	MaxArchiveEntryBytes uint64

	// MaxTotalInflatedBytes bounds the sum of inflated ZIP parts. Zero uses
	// the safe default of 256 MiB.
	MaxTotalInflatedBytes uint64

	// MaxArchiveEntries bounds the number of ZIP entries. Zero uses the safe
	// default of 4096.
	MaxArchiveEntries uint64
}

// Errors returned by this package.
var (
	// ErrNotOOXML means the input is not an Office Open XML package.
	ErrNotOOXML = opc.ErrNotOOXML
	// ErrUnsupportedFormat means the package is OOXML but not one of the
	// three supported document types.
	ErrUnsupportedFormat = errors.New("ooxml: unsupported document format")
	// ErrResourceLimit means the package crossed an archive safety limit.
	ErrResourceLimit = opc.ErrResourceLimit
)

// ConvertFile reads an OOXML document from disk and converts it to Markdown.
func ConvertFile(path string, opts Options) (*Document, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	doc, err := Convert(data, opts)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	if doc.Title == "" {
		doc.Title = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	return doc, nil
}

// Convert converts an in-memory OOXML document to Markdown.
func Convert(data []byte, opts Options) (*Document, error) {
	pkg, err := opc.OpenWithLimits(data, opc.Limits{
		MaxArchiveEntryBytes:  opts.MaxArchiveEntryBytes,
		MaxTotalInflatedBytes: opts.MaxTotalInflatedBytes,
		MaxArchiveEntries:     opts.MaxArchiveEntries,
	})
	if err != nil {
		return nil, err
	}

	main, err := pkg.MainDocument()
	if err != nil {
		return nil, err
	}

	copts := model.Options{
		SkipImages:    opts.SkipImages,
		ImagePrefix:   imagePrefix(opts),
		SheetNames:    opts.SheetNames,
		SlideNotes:    opts.SlideNotes,
		IncludeHidden: opts.IncludeHidden,
	}

	var doc *Document
	switch detectFormat(main) {
	case FormatDocx:
		doc, err = docx.Convert(pkg, main, copts)
	case FormatXlsx:
		doc, err = xlsx.Convert(pkg, main, copts)
	case FormatPptx:
		doc, err = pptx.Convert(pkg, main, copts)
	default:
		return nil, fmt.Errorf("%w: main part %q", ErrUnsupportedFormat, main)
	}
	if err != nil {
		return nil, err
	}

	if opts.ImageDir != "" && !opts.SkipImages {
		if err := writeImages(doc.Images, opts.ImageDir); err != nil {
			return nil, err
		}
	}
	return doc, nil
}

// Detect reports the OOXML format of an in-memory document.
func Detect(data []byte) (Format, error) {
	pkg, err := opc.Open(data)
	if err != nil {
		return FormatUnknown, err
	}
	main, err := pkg.MainDocument()
	if err != nil {
		return FormatUnknown, err
	}
	format := detectFormat(main)
	if format == FormatUnknown {
		return FormatUnknown, fmt.Errorf("%w: main part %q", ErrUnsupportedFormat, main)
	}
	return format, nil
}

// DetectFile reports the format of an OOXML file without converting it.
func DetectFile(path string) (Format, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return FormatUnknown, err
	}
	return Detect(data)
}

// detectFormat classifies a package by the location of its main part, which
// is fixed per format by the OOXML conventions.
func detectFormat(mainPart string) Format {
	switch {
	case strings.HasPrefix(mainPart, "word/"):
		return FormatDocx
	case strings.HasPrefix(mainPart, "xl/"):
		return FormatXlsx
	case strings.HasPrefix(mainPart, "ppt/"):
		return FormatPptx
	}
	return FormatUnknown
}

// imagePrefix resolves the prefix used in generated Markdown image links.
func imagePrefix(opts Options) string {
	if opts.ImagePrefix != "" {
		return strings.TrimSuffix(opts.ImagePrefix, "/")
	}
	if opts.ImageDir != "" {
		return filepath.Base(strings.TrimSuffix(opts.ImageDir, string(filepath.Separator)))
	}
	return ""
}

// writeImages saves extracted images into dir, creating it if needed.
func writeImages(images []Image, dir string) error {
	if len(images) == 0 {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, img := range images {
		if len(img.Data) == 0 {
			continue
		}
		dst := filepath.Join(dir, img.Name)
		if err := os.WriteFile(dst, img.Data, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", dst, err)
		}
	}
	return nil
}

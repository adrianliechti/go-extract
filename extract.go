// Package extract provides unified, recursive text extraction for documents,
// email, archives, plain text, and Markdown.
package extract

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/adrianliechti/go-extract/internal/archive"
	"github.com/adrianliechti/go-extract/internal/eml"
	"github.com/adrianliechti/go-extract/internal/html"
	"github.com/adrianliechti/go-extract/internal/model"
	"github.com/adrianliechti/go-extract/internal/msg"
	"github.com/adrianliechti/go-extract/internal/ooxml"
	"github.com/adrianliechti/go-extract/internal/pdf"
	"github.com/adrianliechti/go-extract/internal/rtf"
	"github.com/adrianliechti/go-extract/internal/text"
)

type (
	Format     = model.Format
	Input      = model.Input
	Document   = model.Document
	Attachment = model.Attachment
	Extractor  = model.Extractor

	// ArchiveOptions configures archive entry and size limits.
	ArchiveOptions = archive.Options
	// PDFOptions configures PDF processing, page selection, and decryption.
	PDFOptions = pdf.Options
	// OOXMLOptions configures Office document conversion and archive limits.
	OOXMLOptions = ooxml.Options
	// RTFOptions configures Rich Text Format decoding.
	RTFOptions = rtf.Options
	// HTMLOptions configures HTML decoding and relative URL resolution.
	HTMLOptions = html.Options
	// MessageOptions configures EML and Outlook message conversion.
	MessageOptions = msg.Options
	// PDFMode controls how far the PDF processing pipeline runs.
	PDFMode = pdf.Mode
)

const (
	FormatUnknown  = model.FormatUnknown
	FormatPDF      = model.FormatPDF
	FormatDOCX     = model.FormatDOCX
	FormatXLSX     = model.FormatXLSX
	FormatPPTX     = model.FormatPPTX
	FormatRTF      = model.FormatRTF
	FormatZIP      = model.FormatZIP
	FormatTAR      = model.FormatTAR
	FormatGZIP     = model.FormatGZIP
	FormatText     = model.FormatText
	FormatMarkdown = model.FormatMarkdown
	FormatHTML     = model.FormatHTML
	FormatEML      = model.FormatEML
	FormatMSG      = model.FormatMSG
)

const (
	// PDFModeFull detects, extracts, and converts a PDF to Markdown.
	PDFModeFull = pdf.ModeFull
	// PDFModeDetectOnly classifies a PDF without converting it to Markdown.
	PDFModeDetectOnly = pdf.ModeDetectOnly
	// PDFModeAnalyze extracts text and analyzes layout without Markdown conversion.
	PDFModeAnalyze = pdf.ModeAnalyze
)

var (
	ErrUnsupportedFormat = model.ErrUnsupportedFormat
	ErrResourceLimit     = model.ErrResourceLimit
)

const (
	defaultMaxDepth           = 8
	defaultMaxDocuments       = 1_000
	defaultMaxAttachmentBytes = 128 << 20
)

// Options configures the unified dispatcher and its built-in extractors.
// Recursive attachment extraction is enabled by default.
type Options struct {
	Archive ArchiveOptions
	PDF     PDFOptions
	OOXML   OOXMLOptions
	RTF     RTFOptions
	HTML    HTMLOptions
	Message MessageOptions

	// Extractors are tried before the built-ins and can add or override format
	// support. The first extractor whose Supports method returns true wins.
	Extractors []Extractor

	// DisableRecursion leaves supported attachments as raw bytes only.
	DisableRecursion bool

	// MaxDepth bounds recursive attachment nesting. Zero uses 8.
	MaxDepth int

	// MaxDocuments bounds the total extracted document count. Zero uses 1000.
	MaxDocuments int

	// MaxAttachmentBytes bounds a single recursively extracted attachment.
	// Zero uses 128 MiB. A negative value disables this limit.
	MaxAttachmentBytes int64

	// DiscardAttachmentData removes raw attachment bytes after recursive
	// extraction. Attachment metadata and extracted child documents remain.
	DiscardAttachmentData bool
}

// Dispatcher dispatches inputs to format extractors and recursively processes
// supported attachments.
type Dispatcher struct {
	extractors         []Extractor
	disableRecursion   bool
	maxDepth           int
	maxDocuments       int
	maxAttachmentBytes int64
	discardData        bool
}

// New constructs a dispatcher with the built-in format extractors.
func New(opts Options) *Dispatcher {
	extractors := append([]Extractor(nil), opts.Extractors...)
	extractors = append(extractors,
		pdf.NewExtractor(opts.PDF),
		ooxml.NewExtractor(opts.OOXML),
		archive.NewExtractor(opts.Archive),
		rtf.NewExtractor(opts.RTF),
		html.NewExtractor(opts.HTML),
		eml.NewExtractor(opts.Message),
		msg.NewExtractor(opts.Message),
		text.NewExtractor(),
	)

	maxDepth := opts.MaxDepth
	if maxDepth == 0 {
		maxDepth = defaultMaxDepth
	}
	maxDocuments := opts.MaxDocuments
	if maxDocuments == 0 {
		maxDocuments = defaultMaxDocuments
	}
	maxAttachmentBytes := opts.MaxAttachmentBytes
	if maxAttachmentBytes == 0 {
		maxAttachmentBytes = defaultMaxAttachmentBytes
	}

	return &Dispatcher{
		extractors:         extractors,
		disableRecursion:   opts.DisableRecursion,
		maxDepth:           maxDepth,
		maxDocuments:       maxDocuments,
		maxAttachmentBytes: maxAttachmentBytes,
		discardData:        opts.DiscardAttachmentData,
	}
}

// Extract uses the built-in dispatcher for one in-memory input.
func Extract(ctx context.Context, input Input, opts Options) (*Document, error) {
	return New(opts).Extract(ctx, input)
}

// File reads and recursively extracts a file, using its name as a format hint.
func File(ctx context.Context, path string, opts Options) (*Document, error) {
	return New(opts).File(ctx, path)
}

// Bytes detects and recursively extracts an in-memory document. Use Extract
// with an Input when a filename or media-type hint is needed, such as for plain
// text or Markdown.
func Bytes(ctx context.Context, data []byte, opts Options) (*Document, error) {
	return New(opts).Bytes(ctx, data)
}

// File reads and recursively extracts a file, using its name as a format hint.
func (d *Dispatcher) File(ctx context.Context, path string) (*Document, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	doc, err := d.Extract(ctx, Input{Name: filepath.Base(path), Data: data})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return doc, nil
}

// Bytes detects and recursively extracts an in-memory document. Use Extract
// with an Input when a filename or media-type hint is needed.
func (d *Dispatcher) Bytes(ctx context.Context, data []byte) (*Document, error) {
	return d.Extract(ctx, Input{Data: data})
}

// Extract dispatches one input and recursively extracts supported attachments.
func (d *Dispatcher) Extract(ctx context.Context, input Input) (*Document, error) {
	state := extractionState{}
	return d.extractAt(ctx, input, &state, 0)
}

type extractionState struct {
	documents int
}

func (d *Dispatcher) extractAt(ctx context.Context, input Input, state *extractionState, depth int) (*Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	extractor := d.match(input)
	if extractor == nil {
		return nil, model.ErrUnsupportedFormat
	}
	if err := d.claimDocument(state); err != nil {
		return nil, err
	}
	doc, err := extractor.Extract(ctx, input)
	if err != nil {
		return nil, err
	}
	if doc.Name == "" {
		doc.Name = input.Name
	}
	if doc.MediaType == "" {
		doc.MediaType = input.MediaType
	}
	if !d.disableRecursion {
		if err := d.enrichAttachments(ctx, doc, state, depth); err != nil {
			return nil, err
		}
	} else {
		clearAttachmentDocuments(doc)
		if d.discardData {
			clearAttachmentData(doc)
		}
	}
	return doc, nil
}

func (d *Dispatcher) enrichAttachments(ctx context.Context, doc *Document, state *extractionState, depth int) error {
	for i := range doc.Attachments {
		if err := ctx.Err(); err != nil {
			return err
		}
		attachment := &doc.Attachments[i]
		if attachment.Document != nil {
			if d.maxAttachmentBytes >= 0 && int64(len(attachment.Data)) > d.maxAttachmentBytes {
				attachment.Document = nil
				attachment.Error = fmt.Errorf("%w: attachment %q is %d bytes (maximum %d)", model.ErrResourceLimit, attachment.Name, len(attachment.Data), d.maxAttachmentBytes).Error()
			} else if depth >= d.maxDepth {
				attachment.Document = nil
				attachment.Error = d.depthError().Error()
			} else if err := d.claimDocument(state); err != nil {
				attachment.Document = nil
				attachment.Error = err.Error()
			} else if err := d.enrichAttachments(ctx, attachment.Document, state, depth+1); err != nil {
				return err
			}
		} else if len(attachment.Data) > 0 {
			input := Input{Name: attachment.Name, MediaType: attachment.MediaType, Data: attachment.Data}
			if d.match(input) != nil {
				switch {
				case depth >= d.maxDepth:
					attachment.Error = d.depthError().Error()
				case d.maxAttachmentBytes >= 0 && int64(len(attachment.Data)) > d.maxAttachmentBytes:
					attachment.Error = fmt.Errorf("%w: attachment %q is %d bytes (maximum %d)", model.ErrResourceLimit, attachment.Name, len(attachment.Data), d.maxAttachmentBytes).Error()
				default:
					child, err := d.extractAt(ctx, input, state, depth+1)
					if err != nil {
						if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
							return err
						}
						attachment.Error = err.Error()
					} else {
						attachment.Document = child
					}
				}
			}
		}
		if d.discardData {
			attachment.Data = nil
		}
	}
	return nil
}

func (d *Dispatcher) match(input Input) Extractor {
	for _, extractor := range d.extractors {
		if extractor != nil && extractor.Supports(input) {
			return extractor
		}
	}
	return nil
}

func (d *Dispatcher) claimDocument(state *extractionState) error {
	if state.documents >= d.maxDocuments {
		return fmt.Errorf("%w: document count exceeds %d", model.ErrResourceLimit, d.maxDocuments)
	}
	state.documents++
	return nil
}

func (d *Dispatcher) depthError() error {
	return fmt.Errorf("%w: attachment depth exceeds %d", model.ErrResourceLimit, d.maxDepth)
}

func clearAttachmentData(doc *Document) {
	for i := range doc.Attachments {
		doc.Attachments[i].Data = nil
		if doc.Attachments[i].Document != nil {
			clearAttachmentData(doc.Attachments[i].Document)
		}
	}
}

func clearAttachmentDocuments(doc *Document) {
	for i := range doc.Attachments {
		doc.Attachments[i].Document = nil
	}
}

// Package model defines the format-neutral document extraction contract.
package model

import (
	"context"
	"errors"
)

// Format identifies the source format of an extracted document.
type Format string

// Formats supported by the built-in extractors.
const (
	FormatUnknown  Format = ""
	FormatPDF      Format = "pdf"
	FormatDOCX     Format = "docx"
	FormatXLSX     Format = "xlsx"
	FormatPPTX     Format = "pptx"
	FormatRTF      Format = "rtf"
	FormatZIP      Format = "zip"
	FormatTAR      Format = "tar"
	FormatGZIP     Format = "gzip"
	FormatText     Format = "text"
	FormatMarkdown Format = "markdown"
	FormatHTML     Format = "html"
	FormatEML      Format = "eml"
	FormatMSG      Format = "msg"
)

// Input is an in-memory document presented for extraction.
//
// Name and MediaType are detection hints. Binary extractors inspect Data;
// text formats may additionally use the media type or filename to distinguish
// ambiguous fragments.
type Input struct {
	Name      string
	MediaType string
	Data      []byte
}

// Document is the format-neutral result returned by every extractor.
// Attachments form a recursive tree: when an attachment is a supported
// document, Document contains its extracted representation.
type Document struct {
	Name        string
	Format      Format
	MediaType   string
	Markdown    string
	Metadata    map[string]string
	Attachments []Attachment
}

// Attachment is a file embedded in a document.
type Attachment struct {
	Name      string
	MediaType string
	Inline    bool
	Data      []byte
	Metadata  map[string]string

	// Document is populated when the attachment itself was successfully
	// extracted. Its attachments may in turn contain more Documents.
	Document *Document

	// Error records a non-fatal recursive extraction failure. An unsupported
	// attachment is not an error and leaves both Document and Error empty.
	Error string
}

// Extractor is implemented by every format-specific extraction adapter.
type Extractor interface {
	Supports(Input) bool
	Extract(context.Context, Input) (*Document, error)
}

// ErrUnsupportedFormat means no registered extractor recognizes an input.
var ErrUnsupportedFormat = errors.New("extract: unsupported document format")

// ErrResourceLimit means a recursive extraction safety limit was reached.
var ErrResourceLimit = errors.New("extract: resource limit exceeded")

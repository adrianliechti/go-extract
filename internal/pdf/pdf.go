package pdf

import (
	"errors"
	"time"
)

// Type classifies a PDF by how its content is stored.
type Type int

const (
	// TypeTextBased has extractable text (Tj/TJ operators present).
	TypeTextBased Type = iota
	// TypeScanned appears to be scanned: images only, no text operators.
	TypeScanned
	// TypeImageBased is mostly images with minimal or no text.
	TypeImageBased
	// TypeMixed has both text pages and image-heavy pages.
	TypeMixed
)

func (t Type) String() string {
	switch t {
	case TypeTextBased:
		return "TextBased"
	case TypeScanned:
		return "Scanned"
	case TypeImageBased:
		return "ImageBased"
	case TypeMixed:
		return "Mixed"
	}
	return "Unknown"
}

// Mode controls how far the processing pipeline runs.
type Mode int

const (
	// ModeFull runs the whole pipeline: detect, extract, convert to Markdown.
	ModeFull Mode = iota
	// ModeDetectOnly classifies the PDF without Markdown conversion.
	ModeDetectOnly
	// ModeAnalyze detects, extracts text and computes layout complexity, but
	// skips Markdown conversion.
	ModeAnalyze
)

// PageOCRReasons lists why a given page was flagged as needing OCR.
type PageOCRReasons struct {
	// Page is the 1-indexed page number.
	Page uint32
	// Reasons holds machine-readable reason identifiers.
	Reasons []string
}

// Machine-readable OCR reason identifiers. These match the reference
// implementation so callers can share routing logic across ports.
const (
	OCRReasonSuspectedGarbledText = "suspected_garbled_text"
	OCRReasonScanned              = "scanned"
	OCRReasonNoText               = "no_text"
	OCRReasonVectorText           = "vector_text"
)

// Result is the outcome of processing a PDF.
type Result struct {
	// Type is the detected PDF type.
	Type Type
	// Markdown is populated in ModeFull and empty otherwise.
	Markdown string
	// PageCount is the number of pages in the document.
	PageCount uint32
	// ProcessingTime is how long processing took.
	ProcessingTime time.Duration
	// PagesNeedingOCR lists 1-indexed pages that should be routed to OCR.
	PagesNeedingOCR []uint32
	// OCRReasonsByPage explains the PagesNeedingOCR entries.
	OCRReasonsByPage []PageOCRReasons
	// Title comes from the PDF metadata, when present.
	Title string
	// Confidence is the detection confidence, from 0.0 to 1.0.
	Confidence float32
	// Layout reports detected tables and multi-column text.
	Layout LayoutComplexity
	// HasEncodingIssues is true when broken font encodings produced garbled
	// text or replacement characters. Callers should fall back to OCR.
	HasEncodingIssues bool
}

// Options configures processing. The zero value is valid and runs the full
// pipeline with default detection settings.
type Options struct {
	// Mode selects how far the pipeline runs.
	Mode Mode
	// Pages limits processing to these 1-indexed pages. Empty means all pages.
	Pages []uint32
	// Password decrypts an encrypted PDF.
	Password string
}

// Errors returned by the package.
var (
	// ErrEncrypted is returned when a PDF needs a password that was not
	// supplied, or the supplied password was rejected.
	ErrEncrypted = errors.New("pdf: document is encrypted")
	// ErrNotAPDF is returned when the input is not a PDF at all.
	ErrNotAPDF = errors.New("pdf: not a PDF file")
	// ErrNotImplemented marks pipeline stages still being ported.
	ErrNotImplemented = errors.New("pdf: not implemented")
)

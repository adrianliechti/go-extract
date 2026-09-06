// Package eml provides the EML-specific facade over the shared email parser.
package eml

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/adrianliechti/go-extract/internal/model"
	"github.com/adrianliechti/go-extract/internal/msg"
)

type (
	Address    = msg.Address
	Attachment = msg.Attachment
	Message    = msg.Message
	Document   = msg.Document
	Options    = msg.Options
	Format     = msg.Format
)

const (
	FormatUnknown = msg.FormatUnknown
	FormatEML     = msg.FormatEML
)

var (
	ErrNotEML        = msg.ErrNotEML
	ErrResourceLimit = msg.ErrResourceLimit
)

// Detect reports whether data is an Internet Message Format message.
func Detect(data []byte) Format {
	if msg.Detect(data) == msg.FormatEML {
		return FormatEML
	}
	return FormatUnknown
}

// DetectFile reports whether a file is an Internet Message Format message.
func DetectFile(path string) (Format, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return FormatUnknown, err
	}
	return Detect(data), nil
}

// Convert extracts an in-memory EML message.
func Convert(data []byte, opts Options) (*Message, error) {
	return msg.ConvertEML(data, opts)
}

// ConvertFile reads and extracts an EML message from disk.
func ConvertFile(path string, opts Options) (*Message, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	message, err := Convert(data, opts)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return message, nil
}

// Extractor adapts EML conversion to the unified extraction interface.
type Extractor struct {
	inner *msg.Extractor
}

// NewExtractor returns a unified EML extractor.
func NewExtractor(opts Options) *Extractor {
	return &Extractor{inner: msg.NewFormatExtractor(msg.FormatEML, opts)}
}

func (e *Extractor) Supports(input model.Input) bool {
	return e.inner.Supports(input)
}

func (e *Extractor) Extract(ctx context.Context, input model.Input) (*model.Document, error) {
	return e.inner.Extract(ctx, input)
}

var _ model.Extractor = (*Extractor)(nil)

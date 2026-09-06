// Package text passes UTF-8 plain-text and Markdown documents through the
// unified extraction interface without rewriting their content.
package text

import (
	"bytes"
	"context"
	"errors"
	"mime"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/adrianliechti/go-extract/internal/model"
)

// Format identifies the supported text flavor.
type Format string

// Supported text formats.
const (
	FormatUnknown  Format = ""
	FormatText     Format = "text"
	FormatMarkdown Format = "markdown"
)

// ErrNotText means the input is not valid, explicitly identified UTF-8 text.
var ErrNotText = errors.New("text: input is not identified UTF-8 text")

// Detect reports whether data is UTF-8 text without binary control bytes.
func Detect(data []byte) bool {
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return false
	}
	for _, value := range data {
		if value < 0x20 && value != '\n' && value != '\r' && value != '\t' && value != '\f' && value != '\b' {
			return false
		}
	}
	return true
}

// DetectFormat classifies textual input using its media type and filename.
func DetectFormat(input model.Input) Format {
	if !Detect(input.Data) {
		return FormatUnknown
	}
	mediaType, _, _ := mime.ParseMediaType(input.MediaType)
	extension := strings.ToLower(filepath.Ext(input.Name))
	if strings.EqualFold(mediaType, "text/markdown") || strings.EqualFold(mediaType, "text/x-markdown") {
		return FormatMarkdown
	}
	switch extension {
	case ".md", ".markdown", ".mdown", ".mkd", ".mkdn":
		return FormatMarkdown
	}
	if strings.EqualFold(mediaType, "text/plain") {
		return FormatText
	}
	switch extension {
	case ".txt", ".text", ".log":
		return FormatText
	default:
		return FormatUnknown
	}
}

// Extractor passes text through to a format-neutral document.
type Extractor struct{}

// NewExtractor returns a unified text and Markdown extractor.
func NewExtractor() *Extractor {
	return &Extractor{}
}

// Supports reports whether input is UTF-8 plain text or Markdown.
func (e *Extractor) Supports(input model.Input) bool {
	return DetectFormat(input) != FormatUnknown
}

// Extract returns the original UTF-8 content unchanged.
func (e *Extractor) Extract(ctx context.Context, input model.Input) (*model.Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	format := DetectFormat(input)
	if format == FormatUnknown {
		return nil, ErrNotText
	}
	unified, mediaType := model.FormatText, "text/plain; charset=utf-8"
	if format == FormatMarkdown {
		unified, mediaType = model.FormatMarkdown, "text/markdown; charset=utf-8"
	}
	return &model.Document{
		Name:      input.Name,
		Format:    unified,
		MediaType: mediaType,
		Markdown:  string(input.Data),
	}, nil
}

var _ model.Extractor = (*Extractor)(nil)

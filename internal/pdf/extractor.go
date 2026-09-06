package pdf

import (
	"bytes"
	"context"
	"strconv"
	"strings"

	"github.com/adrianliechti/go-extract/internal/model"
)

// Extractor adapts the PDF processor to the unified extraction interface.
type Extractor struct {
	Options Options
}

// NewExtractor returns a unified PDF extractor.
func NewExtractor(opts Options) *Extractor {
	return &Extractor{Options: opts}
}

// Supports reports whether input has a PDF header near its beginning.
func (e *Extractor) Supports(input model.Input) bool {
	head := input.Data
	if len(head) > 1024 {
		head = head[:1024]
	}
	return bytes.Contains(head, []byte("%PDF-"))
}

// Extract processes a PDF into a format-neutral document.
func (e *Extractor) Extract(ctx context.Context, input model.Input) (*model.Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result, err := Process(input.Data, e.Options)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	metadata := map[string]string{
		"page_count": strconv.FormatUint(uint64(result.PageCount), 10),
		"pdf_type":   result.Type.String(),
		"confidence": strconv.FormatFloat(float64(result.Confidence), 'f', -1, 32),
	}
	if result.Title != "" {
		metadata["title"] = result.Title
	}
	if len(result.PagesNeedingOCR) > 0 {
		pages := make([]string, len(result.PagesNeedingOCR))
		for i, page := range result.PagesNeedingOCR {
			pages[i] = strconv.FormatUint(uint64(page), 10)
		}
		metadata["pages_needing_ocr"] = strings.Join(pages, ",")
	}

	return &model.Document{
		Name:      input.Name,
		Format:    model.FormatPDF,
		MediaType: "application/pdf",
		Markdown:  result.Markdown,
		Metadata:  metadata,
	}, nil
}

var _ model.Extractor = (*Extractor)(nil)

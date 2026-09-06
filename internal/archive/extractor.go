package archive

import (
	"context"
	"strconv"

	"github.com/adrianliechti/go-extract/internal/model"
)

// Extractor adapts archive extraction to the unified extraction interface.
type Extractor struct {
	Options Options
}

// NewExtractor returns a unified ZIP, TAR, and GZIP extractor.
func NewExtractor(opts Options) *Extractor {
	return &Extractor{Options: opts}
}

// Supports reports whether input has a recognizable supported archive format.
func (e *Extractor) Supports(input model.Input) bool {
	return Detect(input.Data) != FormatUnknown
}

// Extract exposes regular archive entries as recursively dispatched
// attachments.
func (e *Extractor) Extract(ctx context.Context, input model.Input) (*model.Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	doc, err := convert(input.Data, input.Name, e.Options)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	attachments := make([]model.Attachment, 0, len(doc.Entries))
	for _, entry := range doc.Entries {
		attachment := model.Attachment{
			Name:      entry.Name,
			MediaType: entry.MediaType,
			Data:      entry.Data,
		}
		if entry.OriginalName != "" {
			attachment.Metadata = map[string]string{"original_name": entry.OriginalName}
		}
		attachments = append(attachments, attachment)
	}

	return &model.Document{
		Name:      input.Name,
		Format:    unifiedFormat(doc.Format),
		MediaType: mediaType(doc.Format),
		Markdown:  doc.Markdown,
		Metadata: map[string]string{
			"entry_count":          strconv.Itoa(len(doc.Entries)),
			"total_inflated_bytes": strconv.FormatUint(doc.TotalBytes, 10),
		},
		Attachments: attachments,
	}, nil
}

func unifiedFormat(format Format) model.Format {
	switch format {
	case FormatZIP:
		return model.FormatZIP
	case FormatTAR:
		return model.FormatTAR
	case FormatGZIP:
		return model.FormatGZIP
	default:
		return model.FormatUnknown
	}
}

func mediaType(format Format) string {
	switch format {
	case FormatZIP:
		return "application/zip"
	case FormatTAR:
		return "application/x-tar"
	case FormatGZIP:
		return "application/gzip"
	default:
		return "application/octet-stream"
	}
}

var _ model.Extractor = (*Extractor)(nil)

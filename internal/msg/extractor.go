package msg

import (
	"context"
	"strings"

	"github.com/adrianliechti/go-extract/internal/model"
)

// Extractor adapts Outlook MSG conversion to the unified extraction
// interface. Use NewFormatExtractor to select EML or auto-detection instead.
type Extractor struct {
	Options Options
	Only    Format
}

// NewExtractor returns a unified Outlook MSG extractor.
func NewExtractor(opts Options) *Extractor {
	return NewFormatExtractor(FormatMSG, opts)
}

// NewFormatExtractor returns an extractor restricted to format. Passing
// FormatUnknown accepts either EML or MSG.
func NewFormatExtractor(format Format, opts Options) *Extractor {
	return &Extractor{Options: opts, Only: format}
}

// Supports reports whether input is a message in the configured format.
func (e *Extractor) Supports(input model.Input) bool {
	format := Detect(input.Data)
	if e.Only == FormatUnknown {
		return format != FormatUnknown
	}
	return format == e.Only
}

// Extract converts an email into a format-neutral document.
func (e *Extractor) Extract(ctx context.Context, input model.Input) (*model.Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var (
		message *Message
		err     error
	)
	switch e.Only {
	case FormatEML:
		message, err = ConvertEML(input.Data, e.Options)
	case FormatMSG:
		message, err = ConvertMSG(input.Data, e.Options)
	default:
		message, err = Convert(input.Data, e.Options)
	}
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return unifiedDocument(input.Name, message), nil
}

func unifiedDocument(name string, message *Message) *model.Document {
	format, mediaType := model.FormatMSG, "application/vnd.ms-outlook"
	if message.Format == FormatEML {
		format, mediaType = model.FormatEML, "message/rfc822"
	}
	metadata := make(map[string]string)
	setMetadata := func(key, value string) {
		if value = strings.TrimSpace(value); value != "" {
			metadata[key] = value
		}
	}
	setMetadata("subject", message.Subject)
	setMetadata("from", message.From.String())
	setMetadata("to", joinUnifiedAddresses(message.To))
	setMetadata("cc", joinUnifiedAddresses(message.CC))
	setMetadata("bcc", joinUnifiedAddresses(message.BCC))
	setMetadata("reply_to", joinUnifiedAddresses(message.ReplyTo))
	setMetadata("message_id", message.MessageID)
	setMetadata("in_reply_to", message.InReplyTo)
	if !message.Date.IsZero() {
		metadata["date"] = message.Date.Format("2006-01-02T15:04:05Z07:00")
	}

	attachments := make([]model.Attachment, 0, len(message.Attachments))
	for i := range message.Attachments {
		attachment := &message.Attachments[i]
		item := model.Attachment{
			Name:      attachment.Name,
			MediaType: attachment.ContentType,
			Inline:    attachment.Inline,
			Data:      attachment.Data,
		}
		if attachment.ContentID != "" {
			item.Metadata = make(map[string]string)
			item.Metadata["content_id"] = attachment.ContentID
		}
		if attachment.ContentLocation != "" {
			if item.Metadata == nil {
				item.Metadata = make(map[string]string)
			}
			item.Metadata["content_location"] = attachment.ContentLocation
		}
		if attachment.Embedded != nil {
			item.Document = unifiedDocument(attachment.Name, attachment.Embedded)
		}
		attachments = append(attachments, item)
	}

	return &model.Document{
		Name:        name,
		Format:      format,
		MediaType:   mediaType,
		Markdown:    message.Markdown,
		Metadata:    metadata,
		Attachments: attachments,
	}
}

func joinUnifiedAddresses(addresses []Address) string {
	values := make([]string, 0, len(addresses))
	for _, address := range addresses {
		if value := strings.TrimSpace(address.String()); value != "" {
			values = append(values, value)
		}
	}
	return strings.Join(values, ", ")
}

var _ model.Extractor = (*Extractor)(nil)

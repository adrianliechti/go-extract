package ooxml

import (
	"context"
	"mime"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/adrianliechti/go-extract/internal/model"
)

// Extractor adapts the OOXML converter to the unified extraction interface.
type Extractor struct {
	Options Options
}

// NewExtractor returns a unified DOCX, XLSX, and PPTX extractor.
func NewExtractor(opts Options) *Extractor {
	return &Extractor{Options: opts}
}

// Supports reports whether input is a supported Office Open XML package.
func (e *Extractor) Supports(input model.Input) bool {
	_, err := Detect(input.Data)
	return err == nil
}

// Extract converts an OOXML package into a format-neutral document.
func (e *Extractor) Extract(ctx context.Context, input model.Input) (*model.Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	doc, err := Convert(input.Data, e.Options)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	format, mediaType := unifiedFormat(doc.Format, input)
	metadata := make(map[string]string)
	if doc.Title != "" {
		metadata["title"] = doc.Title
	}
	if len(doc.SheetNames) > 0 {
		metadata["sheet_names"] = strings.Join(doc.SheetNames, ",")
	}
	if doc.SlideCount > 0 {
		metadata["slide_count"] = strconv.Itoa(doc.SlideCount)
	}
	attachments := make([]model.Attachment, 0, len(doc.Images))
	for _, image := range doc.Images {
		attachments = append(attachments, model.Attachment{
			Name:      image.Name,
			MediaType: image.ContentType,
			Inline:    true,
			Data:      image.Data,
			Metadata:  map[string]string{"source_part": image.Part},
		})
	}

	return &model.Document{
		Name:        input.Name,
		Format:      format,
		MediaType:   mediaType,
		Markdown:    doc.Markdown,
		Metadata:    metadata,
		Attachments: attachments,
	}, nil
}

type officeFileType struct {
	format    Format
	extension string
	mediaType string
}

// officeFileTypes covers the XML-based document, template, and slide-show
// extensions handled by the three OOXML converters. Macro-enabled packages
// use the same document XML; their VBA project is simply an opaque package
// part and is never executed.
var officeFileTypes = []officeFileType{
	{FormatDocx, ".docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
	{FormatDocx, ".docm", "application/vnd.ms-word.document.macroEnabled.12"},
	{FormatDocx, ".dotx", "application/vnd.openxmlformats-officedocument.wordprocessingml.template"},
	{FormatDocx, ".dotm", "application/vnd.ms-word.template.macroEnabled.12"},
	{FormatXlsx, ".xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"},
	{FormatXlsx, ".xlsm", "application/vnd.ms-excel.sheet.macroEnabled.12"},
	{FormatXlsx, ".xltx", "application/vnd.openxmlformats-officedocument.spreadsheetml.template"},
	{FormatXlsx, ".xltm", "application/vnd.ms-excel.template.macroEnabled.12"},
	{FormatPptx, ".pptx", "application/vnd.openxmlformats-officedocument.presentationml.presentation"},
	{FormatPptx, ".pptm", "application/vnd.ms-powerpoint.presentation.macroEnabled.12"},
	{FormatPptx, ".ppsx", "application/vnd.openxmlformats-officedocument.presentationml.slideshow"},
	{FormatPptx, ".ppsm", "application/vnd.ms-powerpoint.slideshow.macroEnabled.12"},
	{FormatPptx, ".potx", "application/vnd.openxmlformats-officedocument.presentationml.template"},
	{FormatPptx, ".potm", "application/vnd.ms-powerpoint.template.macroEnabled.12"},
}

func unifiedFormat(format Format, input model.Input) (model.Format, string) {
	mediaType := officeMediaType(format, input)
	switch format {
	case FormatDocx:
		return model.FormatDOCX, mediaType
	case FormatXlsx:
		return model.FormatXLSX, mediaType
	case FormatPptx:
		return model.FormatPPTX, mediaType
	default:
		return model.FormatUnknown, "application/zip"
	}
}

func officeMediaType(format Format, input model.Input) string {
	extension := strings.ToLower(filepath.Ext(input.Name))
	for _, fileType := range officeFileTypes {
		if fileType.format == format && fileType.extension == extension {
			return fileType.mediaType
		}
	}

	mediaType, _, _ := mime.ParseMediaType(input.MediaType)
	for _, fileType := range officeFileTypes {
		if fileType.format == format && strings.EqualFold(fileType.mediaType, mediaType) {
			return fileType.mediaType
		}
	}

	for _, fileType := range officeFileTypes {
		if fileType.format == format {
			return fileType.mediaType
		}
	}
	return "application/zip"
}

var _ model.Extractor = (*Extractor)(nil)

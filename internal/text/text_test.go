package text_test

import (
	"context"
	"errors"
	"testing"

	"github.com/adrianliechti/go-extract/internal/model"
	"github.com/adrianliechti/go-extract/internal/text"
)

func TestExtractorPassesTextThroughUnchanged(t *testing.T) {
	tests := []struct {
		input     model.Input
		format    model.Format
		mediaType string
	}{
		{model.Input{Name: "notes.txt", Data: []byte("one\r\ntwo\n")}, model.FormatText, "text/plain; charset=utf-8"},
		{model.Input{Name: "README.md", Data: []byte("# Heading\n\nBody\n")}, model.FormatMarkdown, "text/markdown; charset=utf-8"},
		{model.Input{Name: "README.md", MediaType: "text/plain", Data: []byte("Markdown by extension")}, model.FormatMarkdown, "text/markdown; charset=utf-8"},
		{model.Input{Name: "upload", MediaType: "text/x-markdown; charset=utf-8", Data: []byte("**bold**")}, model.FormatMarkdown, "text/markdown; charset=utf-8"},
	}
	extractor := text.NewExtractor()
	for _, test := range tests {
		doc, err := extractor.Extract(context.Background(), test.input)
		if err != nil {
			t.Fatal(err)
		}
		if doc.Format != test.format || doc.MediaType != test.mediaType || doc.Markdown != string(test.input.Data) {
			t.Fatalf("Document = %#v", doc)
		}
	}
}

func TestExtractorDoesNotClaimBinaryOrUnhintedFiles(t *testing.T) {
	extractor := text.NewExtractor()
	for _, input := range []model.Input{
		{Name: "data.bin", Data: []byte("looks textual")},
		{Name: "notes.txt", Data: []byte{'a', 0, 'b'}},
		{Name: "notes.txt", Data: []byte{0xff}},
	} {
		if extractor.Supports(input) {
			t.Fatalf("Supports(%q) = true", input.Name)
		}
		if _, err := extractor.Extract(context.Background(), input); !errors.Is(err, text.ErrNotText) {
			t.Fatalf("Extract(%q) error = %v, want ErrNotText", input.Name, err)
		}
	}
}

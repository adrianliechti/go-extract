package rtf_test

import (
	"context"
	"errors"
	"testing"

	"github.com/adrianliechti/go-extract/internal/model"
	"github.com/adrianliechti/go-extract/internal/rtf"
)

func TestConvertExtractsReadableText(t *testing.T) {
	source := []byte(`{\rtf1\ansi\ansicpg1252{\fonttbl{\f0 Arial;}}{\info{\title Hidden title}}Hello \b world\b0\par Caf\'e9 \u10003?\tab \emdash done}`)

	doc, err := rtf.Convert(source, rtf.Options{})
	if err != nil {
		t.Fatal(err)
	}
	const want = "Hello world\nCafé ✓\t—done"
	if doc.Markdown != want {
		t.Fatalf("Markdown = %q, want %q", doc.Markdown, want)
	}
}

func TestConvertHonorsDeclaredCodePage(t *testing.T) {
	source := []byte(`{\rtf1\ansi\ansicpg1251 \'cf\'f0\'e8\'e2\'e5\'f2}`)

	doc, err := rtf.Convert(source, rtf.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if doc.Markdown != "Привет" {
		t.Fatalf("Markdown = %q, want Привет", doc.Markdown)
	}
}

func TestDetectAndRejectNonRTF(t *testing.T) {
	if !rtf.Detect([]byte("\ufeff{\\RTF1 document}")) {
		t.Error("Detect rejected an RTF header")
	}
	if rtf.Detect([]byte("plain text")) {
		t.Error("Detect accepted plain text")
	}
	if _, err := rtf.Convert([]byte("plain text"), rtf.Options{}); !errors.Is(err, rtf.ErrNotRTF) {
		t.Fatalf("Convert error = %v, want ErrNotRTF", err)
	}
}

func TestExtractor(t *testing.T) {
	input := model.Input{Name: "notes.rtf", MediaType: "text/rtf", Data: []byte(`{\rtf1\ansi Extracted text}`)}
	extractor := rtf.NewExtractor(rtf.Options{})
	if !extractor.Supports(input) {
		t.Fatal("Supports rejected RTF")
	}
	doc, err := extractor.Extract(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Format != model.FormatRTF || doc.MediaType != "application/rtf" || doc.Markdown != "Extracted text" {
		t.Fatalf("Document = %#v", doc)
	}
}

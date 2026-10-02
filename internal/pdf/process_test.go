package pdf

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/adrianliechti/go-extract/internal/model"
)

func TestProcessingContextErrors(t *testing.T) {
	const path = "testdata/fixtures/encrypted-secret123.pdf"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	opts := Options{Password: "secret123"}
	doc, err := load(t.Context(), data, opts.Password)
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	expired, cancel := context.WithDeadline(t.Context(), time.Time{})
	defer cancel()

	for _, ctx := range []context.Context{canceled, expired} {
		t.Run(ctx.Err().Error(), func(t *testing.T) {
			for name, process := range map[string]func() error{
				"extractor": func() error {
					_, err := NewExtractor(opts).Extract(ctx, model.Input{Data: data})
					return err
				},
				"bytes": func() error {
					_, err := Process(ctx, data, opts)
					return err
				},
				"file": func() error {
					_, err := ProcessFile(ctx, path, opts)
					return err
				},
				"reader": func() error {
					r := bytes.NewReader(data)
					_, err := ProcessReader(ctx, r, opts)
					if r.Len() != len(data) {
						t.Error("canceled processing consumed input")
					}
					return err
				},
				"pages": func() error {
					pages, err := doc.extract(ctx, nil, false)
					if len(pages) != 0 {
						t.Error("canceled extraction returned pages for OCR")
					}
					return err
				},
				"page lookup": func() error {
					_, err := extractPage(ctx, doc.xref, 1, false)
					return err
				},
			} {
				t.Run(name, func(t *testing.T) {
					if err := process(); !errors.Is(err, ctx.Err()) {
						t.Fatalf("error = %v, want %v", err, ctx.Err())
					}
				})
			}
		})
	}
}

func TestProcessReaderCancellationDuringRead(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r := &cancelingReader{Reader: bytes.NewReader([]byte("%PDF-1.7\n")), cancel: cancel}
	result, err := ProcessReader(ctx, r, Options{})
	if result != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("ProcessReader = (%v, %v), want (nil, context.Canceled)", result, err)
	}
}

type cancelingReader struct {
	*bytes.Reader
	cancel context.CancelFunc
}

func (r *cancelingReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.cancel()
	return n, err
}

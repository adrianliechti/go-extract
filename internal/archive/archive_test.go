package archive_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"testing"

	"github.com/adrianliechti/go-extract/internal/archive"
	"github.com/adrianliechti/go-extract/internal/model"
)

func TestDetect(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want archive.Format
	}{
		{"zip", buildZIP(t, []testFile{{"file.txt", []byte("zip")}}), archive.FormatZIP},
		{"empty zip", buildZIP(t, nil), archive.FormatZIP},
		{"tar", buildTAR(t, []testFile{{"file.txt", []byte("tar")}}), archive.FormatTAR},
		{"empty tar", make([]byte, 1_024), archive.FormatTAR},
		{"gzip", buildGZIP(t, "file.txt", []byte("gzip")), archive.FormatGZIP},
		{"truncated zip", []byte{'P', 'K', 0x03, 0x04}, archive.FormatZIP},
		{"plain", []byte("not an archive"), archive.FormatUnknown},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := archive.Detect(test.data); got != test.want {
				t.Fatalf("Detect = %q, want %q", got, test.want)
			}
		})
	}
}

func TestConvertRejectsTruncatedZIP(t *testing.T) {
	if _, err := archive.Convert([]byte{'P', 'K', 0x03, 0x04}, archive.Options{}); err == nil {
		t.Fatal("Convert accepted a truncated ZIP")
	}
}

func TestConvertZIPCleansNamesAndPreservesMediaTypes(t *testing.T) {
	data := buildZIP(t, []testFile{
		{"../report.docx", []byte("first")},
		{"../report.docx", []byte("second")},
		{"folder/notes.rtf", []byte("third")},
	})
	doc, err := archive.Convert(data, archive.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if doc.Format != archive.FormatZIP || doc.TotalBytes != 16 || len(doc.Entries) != 3 {
		t.Fatalf("Document = %#v", doc)
	}
	if doc.Entries[0].Name != "report.docx" || doc.Entries[0].OriginalName != "../report.docx" {
		t.Fatalf("first entry = %#v", doc.Entries[0])
	}
	if doc.Entries[1].Name != "report (2).docx" {
		t.Fatalf("duplicate entry = %#v", doc.Entries[1])
	}
	if doc.Entries[0].MediaType != "application/vnd.openxmlformats-officedocument.wordprocessingml.document" || doc.Entries[2].MediaType != "application/rtf" {
		t.Fatalf("entry media types = %q, %q", doc.Entries[0].MediaType, doc.Entries[2].MediaType)
	}
}

func TestConvertTARAndGZIP(t *testing.T) {
	tarData := buildTAR(t, []testFile{{"folder/readme.md", []byte("# Nested")}})
	gzipData := buildGZIP(t, "", tarData)

	gzipInput := model.Input{Name: "bundle.tar.gz", Data: gzipData}
	gzipDoc, err := archive.NewExtractor(archive.Options{}).Extract(context.Background(), gzipInput)
	if err != nil {
		t.Fatal(err)
	}
	if gzipDoc.Format != model.FormatGZIP || len(gzipDoc.Attachments) != 1 || gzipDoc.Attachments[0].Name != "bundle.tar" {
		t.Fatalf("GZIP document = %#v", gzipDoc)
	}

	tarDoc, err := archive.Convert(gzipDoc.Attachments[0].Data, archive.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if tarDoc.Format != archive.FormatTAR || len(tarDoc.Entries) != 1 || tarDoc.Entries[0].Name != "folder/readme.md" || string(tarDoc.Entries[0].Data) != "# Nested" {
		t.Fatalf("TAR document = %#v", tarDoc)
	}
}

func TestConvertAppliesResourceLimits(t *testing.T) {
	data := buildZIP(t, []testFile{
		{"one.txt", []byte("1234")},
		{"two.txt", []byte("5678")},
	})
	tests := []archive.Options{
		{MaxEntries: 1},
		{MaxEntryBytes: 3},
		{MaxTotalBytes: 7},
	}
	for _, opts := range tests {
		if _, err := archive.Convert(data, opts); !errors.Is(err, archive.ErrResourceLimit) {
			t.Fatalf("Convert(%+v) error = %v, want ErrResourceLimit", opts, err)
		}
	}
	gzipData := buildGZIP(t, "large.txt", []byte("12345678"))
	if _, err := archive.Convert(gzipData, archive.Options{MaxEntryBytes: 4}); !errors.Is(err, archive.ErrResourceLimit) {
		t.Fatalf("GZIP limit error = %v, want ErrResourceLimit", err)
	}
}

type testFile struct {
	name string
	data []byte
}

func buildZIP(t *testing.T, files []testFile) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	for _, file := range files {
		entry, err := writer.Create(file.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(file.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func buildTAR(t *testing.T, files []testFile) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := tar.NewWriter(&output)
	for _, file := range files {
		if err := writer.WriteHeader(&tar.Header{Name: file.name, Mode: 0o600, Size: int64(len(file.data))}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(file.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func buildGZIP(t *testing.T, name string, data []byte) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := gzip.NewWriter(&output)
	writer.Name = name
	if _, err := writer.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

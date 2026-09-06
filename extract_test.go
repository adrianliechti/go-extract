package extract_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrianliechti/go-extract"
)

func TestFilePreservesFilenameHintsAndErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.md")
	const content = "# Notes\n\nReady to extract.\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	unsupported := filepath.Join(dir, "unknown.bin")
	if err := os.WriteFile(unsupported, []byte{0, 1, 2, 3}, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		file func(context.Context, string) (*extract.Document, error)
	}{
		{"package", func(ctx context.Context, path string) (*extract.Document, error) {
			return extract.File(ctx, path, extract.Options{})
		}},
		{"dispatcher", extract.New(extract.Options{}).File},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := tc.file(context.Background(), path)
			if err != nil {
				t.Fatal(err)
			}
			if doc.Name != "notes.md" || doc.Format != extract.FormatMarkdown || doc.Markdown != content {
				t.Fatalf("File = %#v, want named Markdown document", doc)
			}
			if _, err := tc.file(context.Background(), filepath.Join(dir, "missing.md")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("missing file error = %v, want os.ErrNotExist", err)
			}
			if _, err := tc.file(context.Background(), unsupported); !errors.Is(err, extract.ErrUnsupportedFormat) || !strings.Contains(err.Error(), "unknown.bin") {
				t.Fatalf("unsupported file error = %v, want filename and ErrUnsupportedFormat", err)
			}
		})
	}
}

func TestBytesDetectsDocumentsWithoutFilenameHints(t *testing.T) {
	data := buildDOCX(t, "In-memory Word text")
	for _, tc := range []struct {
		name  string
		bytes func(context.Context, []byte) (*extract.Document, error)
	}{
		{"package", func(ctx context.Context, data []byte) (*extract.Document, error) {
			return extract.Bytes(ctx, data, extract.Options{})
		}},
		{"dispatcher", extract.New(extract.Options{}).Bytes},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := tc.bytes(context.Background(), data)
			if err != nil {
				t.Fatal(err)
			}
			if doc.Format != extract.FormatDOCX || !strings.Contains(doc.Markdown, "In-memory Word text") {
				t.Fatalf("Bytes = %#v, want detected DOCX content", doc)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := tc.bytes(ctx, data); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelled extraction error = %v, want context.Canceled", err)
			}
		})
	}
}

func TestExtractRecursivelyExtractsEmailAttachments(t *testing.T) {
	pdfData := buildPDF("Attached PDF text")
	docxData := buildDOCX(t, "Attached Word text")
	nested := buildEML("Nested message", []mailAttachment{
		{name: "inside.pdf", mediaType: "application/pdf", data: pdfData},
	})
	outer := buildEML("Outer message", []mailAttachment{
		{name: "report.docx", mediaType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document", data: docxData},
		{name: "notes.rtf", mediaType: "application/rtf", data: []byte(`{\rtf1\ansi Attached RTF text}`)},
		{name: "page.html", mediaType: "text/html", data: []byte("<html><head><title>Attached page</title></head><body><h2>HTML attachment text</h2></body></html>")},
		{name: "nested.eml", mediaType: "message/rfc822", data: nested},
		{name: "notes.bin", mediaType: "application/octet-stream", data: []byte("unsupported")},
	})

	doc, err := extract.Extract(context.Background(), extract.Input{Name: "outer.eml", Data: outer}, extract.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if doc.Format != extract.FormatEML {
		t.Fatalf("Format = %q, want eml", doc.Format)
	}

	docx := attachmentNamed(t, doc, "report.docx")
	if docx.Document == nil || docx.Document.Format != extract.FormatDOCX {
		t.Fatalf("DOCX attachment document = %#v", docx.Document)
	}
	if !strings.Contains(docx.Document.Markdown, "Attached Word text") {
		t.Fatalf("DOCX Markdown = %q", docx.Document.Markdown)
	}

	rtf := attachmentNamed(t, doc, "notes.rtf")
	if rtf.Document == nil || rtf.Document.Format != extract.FormatRTF {
		t.Fatalf("RTF attachment document = %#v", rtf.Document)
	}
	if rtf.Document.Markdown != "Attached RTF text" {
		t.Fatalf("RTF Markdown = %q", rtf.Document.Markdown)
	}

	htmlAttachment := attachmentNamed(t, doc, "page.html")
	if htmlAttachment.Document == nil || htmlAttachment.Document.Format != extract.FormatHTML {
		t.Fatalf("HTML attachment document = %#v", htmlAttachment.Document)
	}
	if !strings.Contains(htmlAttachment.Document.Markdown, "## HTML attachment text") {
		t.Fatalf("HTML Markdown = %q", htmlAttachment.Document.Markdown)
	}

	nestedMessage := attachmentNamed(t, doc, "nested.eml")
	if nestedMessage.Document == nil || nestedMessage.Document.Format != extract.FormatEML {
		t.Fatalf("nested EML document = %#v", nestedMessage.Document)
	}
	nestedPDF := attachmentNamed(t, nestedMessage.Document, "inside.pdf")
	if nestedPDF.Document == nil || nestedPDF.Document.Format != extract.FormatPDF {
		t.Fatalf("nested PDF document = %#v, error = %q", nestedPDF.Document, nestedPDF.Error)
	}
	if !strings.Contains(nestedPDF.Document.Markdown, "Attached PDF text") {
		t.Fatalf("PDF Markdown = %q", nestedPDF.Document.Markdown)
	}

	unsupported := attachmentNamed(t, doc, "notes.bin")
	if unsupported.Document != nil || unsupported.Error != "" || string(unsupported.Data) != "unsupported" {
		t.Fatalf("unsupported attachment = %#v", unsupported)
	}
}

func TestExtractAppliesRecursiveDepthLimitNonFatally(t *testing.T) {
	nested := buildEML("Nested message", []mailAttachment{
		{name: "inside.pdf", mediaType: "application/pdf", data: buildPDF("too deep")},
	})
	outer := buildEML("Outer message", []mailAttachment{
		{name: "nested.eml", mediaType: "message/rfc822", data: nested},
	})

	doc, err := extract.Extract(context.Background(), extract.Input{Name: "outer.eml", Data: outer}, extract.Options{MaxDepth: 1})
	if err != nil {
		t.Fatal(err)
	}
	nestedMessage := attachmentNamed(t, doc, "nested.eml")
	if nestedMessage.Document == nil {
		t.Fatal("first attachment level was not extracted")
	}
	nestedPDF := attachmentNamed(t, nestedMessage.Document, "inside.pdf")
	if nestedPDF.Document != nil || !strings.Contains(nestedPDF.Error, "attachment depth exceeds 1") {
		t.Fatalf("depth-limited attachment = %#v", nestedPDF)
	}
}

func TestBytesCanDisableRecursionAndDiscardBytes(t *testing.T) {
	outer := buildEML("Outer message", []mailAttachment{
		{name: "nested.eml", mediaType: "message/rfc822", data: buildEML("Nested", nil)},
	})

	doc, err := extract.Bytes(context.Background(), outer, extract.Options{
		DisableRecursion:      true,
		DiscardAttachmentData: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	attachment := attachmentNamed(t, doc, "nested.eml")
	if attachment.Document != nil || attachment.Data != nil {
		t.Fatalf("attachment = %#v, want metadata only", attachment)
	}
}

func TestExtractRecursesThroughNestedZIPs(t *testing.T) {
	docxData := buildDOCX(t, "Deeply archived Word text")
	inner := buildZIP(t, []mailAttachment{
		{name: "report.docx", data: docxData},
		{name: "README.md", data: []byte("# Archive notes\n")},
	})
	outer := buildZIP(t, []mailAttachment{{name: "nested.zip", data: inner}})
	email := buildEML("Archives", []mailAttachment{
		{name: "bundle.zip", mediaType: "application/zip", data: outer},
	})

	doc, err := extract.Extract(context.Background(), extract.Input{Name: "archives.eml", Data: email}, extract.Options{})
	if err != nil {
		t.Fatal(err)
	}
	bundle := attachmentNamed(t, doc, "bundle.zip")
	if bundle.Document == nil || bundle.Document.Format != extract.FormatZIP {
		t.Fatalf("bundle document = %#v", bundle.Document)
	}
	nested := attachmentNamed(t, bundle.Document, "nested.zip")
	if nested.Document == nil || nested.Document.Format != extract.FormatZIP {
		t.Fatalf("nested document = %#v", nested.Document)
	}
	report := attachmentNamed(t, nested.Document, "report.docx")
	if report.Document == nil || report.Document.Format != extract.FormatDOCX || !strings.Contains(report.Document.Markdown, "Deeply archived Word text") {
		t.Fatalf("report document = %#v", report.Document)
	}
	readme := attachmentNamed(t, nested.Document, "README.md")
	if readme.Document == nil || readme.Document.Format != extract.FormatMarkdown || readme.Document.Markdown != "# Archive notes\n" {
		t.Fatalf("README document = %#v", readme.Document)
	}
}

func TestExtractRecursesThroughTarGzip(t *testing.T) {
	tarData := buildTAR(t, []mailAttachment{{name: "notes.txt", data: []byte("Text inside tar.gz\n")}})
	gzipData := buildGZIP(t, tarData)

	doc, err := extract.Extract(context.Background(), extract.Input{Name: "bundle.tar.gz", Data: gzipData}, extract.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if doc.Format != extract.FormatGZIP {
		t.Fatalf("GZIP format = %q", doc.Format)
	}
	tarAttachment := attachmentNamed(t, doc, "bundle.tar")
	if tarAttachment.Document == nil || tarAttachment.Document.Format != extract.FormatTAR {
		t.Fatalf("TAR document = %#v", tarAttachment.Document)
	}
	notes := attachmentNamed(t, tarAttachment.Document, "notes.txt")
	if notes.Document == nil || notes.Document.Format != extract.FormatText || notes.Document.Markdown != "Text inside tar.gz\n" {
		t.Fatalf("text document = %#v", notes.Document)
	}
}

func attachmentNamed(t *testing.T, doc *extract.Document, name string) *extract.Attachment {
	t.Helper()
	for i := range doc.Attachments {
		if doc.Attachments[i].Name == name {
			return &doc.Attachments[i]
		}
	}
	t.Fatalf("attachment %q not found in %#v", name, doc.Attachments)
	return nil
}

type mailAttachment struct {
	name      string
	mediaType string
	data      []byte
}

func buildEML(subject string, attachments []mailAttachment) []byte {
	const boundary = "go-extract-boundary"
	var message strings.Builder
	fmt.Fprintf(&message, "From: sender@example.com\r\nTo: recipient@example.com\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=%q\r\n\r\n", subject, boundary)
	fmt.Fprintf(&message, "--%s\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nBody for %s\r\n", boundary, subject)
	for _, attachment := range attachments {
		fmt.Fprintf(&message, "--%s\r\nContent-Type: %s; name=%q\r\nContent-Disposition: attachment; filename=%q\r\nContent-Transfer-Encoding: base64\r\n\r\n%s\r\n", boundary, attachment.mediaType, attachment.name, attachment.name, base64.StdEncoding.EncodeToString(attachment.data))
	}
	fmt.Fprintf(&message, "--%s--\r\n", boundary)
	return []byte(message.String())
}

func buildDOCX(t *testing.T, text string) []byte {
	t.Helper()
	var data bytes.Buffer
	writer := zip.NewWriter(&data)
	parts := map[string]string{
		"[Content_Types].xml": `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="xml" ContentType="application/xml"/><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/></Types>`,
		"_rels/.rels":         `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rDoc" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
		"word/document.xml":   `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>` + text + `</w:t></w:r></w:p></w:body></w:document>`,
	}
	for name, content := range parts {
		part, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func buildZIP(t *testing.T, files []mailAttachment) []byte {
	t.Helper()
	var data bytes.Buffer
	writer := zip.NewWriter(&data)
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
	return data.Bytes()
}

func buildTAR(t *testing.T, files []mailAttachment) []byte {
	t.Helper()
	var data bytes.Buffer
	writer := tar.NewWriter(&data)
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
	return data.Bytes()
}

func buildGZIP(t *testing.T, source []byte) []byte {
	t.Helper()
	var data bytes.Buffer
	writer := gzip.NewWriter(&data)
	if _, err := writer.Write(source); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func buildPDF(text string) []byte {
	content := fmt.Sprintf("BT\n/F1 18 Tf\n72 720 Td\n(%s) Tj\nET\n", strings.NewReplacer("\\", "\\\\", "(", "\\(", ")", "\\)").Replace(text))
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(content), content),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}

	var pdf bytes.Buffer
	pdf.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects)+1)
	for i, object := range objects {
		offsets[i+1] = pdf.Len()
		fmt.Fprintf(&pdf, "%d 0 obj\n%s\nendobj\n", i+1, object)
	}
	xref := pdf.Len()
	fmt.Fprintf(&pdf, "xref\n0 %d\n0000000000 65535 f \n", len(offsets))
	for i := 1; i < len(offsets); i++ {
		fmt.Fprintf(&pdf, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&pdf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets), xref)
	return pdf.Bytes()
}

package ooxml_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrianliechti/go-extract/internal/ooxml"
)

// corpusRoot locates the external corpus, which is fetched on demand and not
// committed. Tests skip cleanly when it is absent.
func corpusRoot(t *testing.T) string {
	t.Helper()
	configured := os.Getenv("GO_EXTRACT_CORPUS")
	candidates := []string{configured}
	if configured != "" && !filepath.IsAbs(configured) {
		candidates = append(candidates, filepath.Join("..", "..", configured))
	}
	candidates = append(candidates,
		"../pdf/testdata/external/test_documents",
		"testdata/external/test_documents",
	)
	for _, c := range candidates {
		if c != "" {
			if fi, err := os.Stat(c); err == nil && fi.IsDir() {
				return c
			}
		}
	}
	t.Skip("external corpus not fetched")
	return ""
}

// find returns the first file with the given extension, preferring one whose
// name contains hint.
func find(t *testing.T, root, ext, hint string) string {
	t.Helper()
	var fallback string
	var found string
	_ = filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() || found != "" {
			return nil
		}
		if !strings.EqualFold(filepath.Ext(p), ext) || strings.Contains(p, "/.git/") {
			return nil
		}
		if hint != "" && strings.Contains(strings.ToLower(filepath.Base(p)), hint) {
			found = p
		} else if fallback == "" {
			fallback = p
		}
		return nil
	})
	if found != "" {
		return found
	}
	if fallback == "" {
		t.Skipf("no %s file in corpus", ext)
	}
	return fallback
}

func TestConvertDocx(t *testing.T) {
	root := corpusRoot(t)
	doc, err := ooxml.ConvertFile(find(t, root, ".docx", "word_tables"), ooxml.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if doc.Format != ooxml.FormatDocx {
		t.Errorf("Format = %v, want docx", doc.Format)
	}
	if strings.TrimSpace(doc.Markdown) == "" {
		t.Error("Markdown is empty")
	}
	if !strings.Contains(doc.Markdown, "|") {
		t.Error("expected a Markdown table in word_tables output")
	}
}

func TestConvertXlsx(t *testing.T) {
	root := corpusRoot(t)
	doc, err := ooxml.ConvertFile(find(t, root, ".xlsx", ""), ooxml.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if doc.Format != ooxml.FormatXlsx {
		t.Errorf("Format = %v, want xlsx", doc.Format)
	}
	if len(doc.SheetNames) == 0 {
		t.Error("expected at least one sheet name")
	}
}

func TestConvertPptx(t *testing.T) {
	root := corpusRoot(t)
	doc, err := ooxml.ConvertFile(find(t, root, ".pptx", ""), ooxml.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if doc.Format != ooxml.FormatPptx {
		t.Errorf("Format = %v, want pptx", doc.Format)
	}
	if doc.SlideCount == 0 {
		t.Error("expected at least one slide")
	}
}

// TestImageExtraction checks that images are written to disk and that every
// Markdown reference resolves to a written file.
func TestImageExtraction(t *testing.T) {
	root := corpusRoot(t)
	src := find(t, root, ".docx", "image")

	dir := t.TempDir()
	imgDir := filepath.Join(dir, "media")

	doc, err := ooxml.ConvertFile(src, ooxml.Options{ImageDir: imgDir})
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Images) == 0 {
		t.Skip("chosen fixture embeds no images")
	}

	for _, img := range doc.Images {
		p := filepath.Join(imgDir, img.Name)
		fi, err := os.Stat(p)
		if err != nil {
			t.Errorf("image %s was not written: %v", img.Name, err)
			continue
		}
		if fi.Size() == 0 {
			t.Errorf("image %s is empty", img.Name)
		}
		// The Markdown must reference the file under the prefix.
		if !strings.Contains(doc.Markdown, img.Name) {
			t.Errorf("image %s is not referenced in the Markdown", img.Name)
		}
	}
}

func TestSkipImages(t *testing.T) {
	root := corpusRoot(t)
	doc, err := ooxml.ConvertFile(find(t, root, ".docx", "image"), ooxml.Options{SkipImages: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Images) != 0 {
		t.Errorf("got %d images, want 0 with SkipImages", len(doc.Images))
	}
	if strings.Contains(doc.Markdown, "![") {
		t.Error("Markdown still contains an image reference")
	}
}

func TestRejectsNonOOXML(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"not a zip", []byte("plain text, definitely not a zip archive")},
		{"truncated", []byte("PK\x03\x04garbage")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ooxml.Convert(tc.data, ooxml.Options{}); err == nil {
				t.Error("Convert should have failed")
			}
		})
	}
}

// TestCorpusNoPanics converts every OOXML file in the corpus, asserting the
// converters degrade gracefully rather than panicking on unusual input.
func TestCorpusNoPanics(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping corpus sweep in short mode")
	}
	root := corpusRoot(t)

	var converted, failed int
	_ = filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() || strings.Contains(p, "/.git/") {
			return nil
		}
		switch strings.ToLower(filepath.Ext(p)) {
		case ".docx", ".docm", ".dotx", ".dotm",
			".xlsx", ".xlsm", ".xltx", ".xltm",
			".pptx", ".pptm", ".ppsx", ".ppsm", ".potx", ".potm":
		default:
			return nil
		}

		doc, err := ooxml.ConvertFile(p, ooxml.Options{})
		if err != nil {
			failed++
			t.Logf("failed: %s: %v", filepath.Base(p), err)
			return nil
		}
		converted++
		if doc.Format == ooxml.FormatUnknown {
			t.Errorf("%s: format not detected", filepath.Base(p))
		}
		return nil
	})

	t.Logf("converted=%d failed=%d", converted, failed)
	if converted == 0 {
		t.Skip("no OOXML files in corpus")
	}
}

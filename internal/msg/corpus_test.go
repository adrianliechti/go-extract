package msg_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrianliechti/go-extract/internal/msg"
)

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
	for _, root := range candidates {
		if root != "" {
			if info, err := os.Stat(root); err == nil && info.IsDir() {
				return root
			}
		}
	}
	t.Skip("external email corpus not fetched")
	return ""
}

func TestMarkItDownOutlookFixture(t *testing.T) {
	root := corpusRoot(t)
	path := filepath.Join(root, "vendored", "markitdown", "test_outlook_msg.msg")
	if _, err := os.Stat(path); err != nil {
		path = filepath.Join(root, "vendored", "markitdown", "msg", "test_outlook_msg.msg")
	}
	m, err := msg.ConvertFile(path, msg.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if m.Format != msg.FormatMSG || m.Subject != "Test Email Message" {
		t.Fatalf("incomplete conversion: format=%v subject=%q markdown=%d", m.Format, m.Subject, len(m.Markdown))
	}
	if m.From.Address == "" || len(m.To) == 0 || m.To[0].Address == "" {
		t.Fatalf("Outlook addresses missing: from=%#v to=%#v", m.From, m.To)
	}
	if !strings.Contains(m.BodyMarkdown, "body of the test email message") {
		t.Fatalf("Outlook body missing:\n%s", m.BodyMarkdown)
	}
}

func TestRealWorldHTMLUnicodeAndAttachments(t *testing.T) {
	root := corpusRoot(t)
	tests := []struct {
		path           string
		wantSubject    string
		wantBody       string
		minAttachments int
	}{
		{"email/html_only.eml", "HTML Only Email", "Welcome to Our Service", 0},
		{"vendored/unstructured/eml/fake-email-utf-16-be.eml", "Test Email", "Roses are red", 0},
		{"vendored/unstructured/msg/fake-email-attachment.msg", "Fake email with attachment", "Here's the attachments", 1},
		{"vendored/unstructured/msg/fake-email-with-cc-and-bcc.msg", "Fake email with cc and bcc recipients", "Please ignore", 0},
	}
	for _, tc := range tests {
		t.Run(filepath.Base(tc.path), func(t *testing.T) {
			m, err := msg.ConvertFile(filepath.Join(root, filepath.FromSlash(tc.path)), msg.Options{})
			if err != nil {
				t.Fatal(err)
			}
			if m.Subject != tc.wantSubject || !strings.Contains(m.BodyMarkdown, tc.wantBody) {
				t.Fatalf("subject=%q body=\n%s", m.Subject, m.BodyMarkdown)
			}
			if len(m.Attachments) < tc.minAttachments {
				t.Fatalf("attachments=%d, want at least %d", len(m.Attachments), tc.minAttachments)
			}
			if strings.Contains(tc.path, "cc-and-bcc") && (len(m.CC) == 0 || len(m.BCC) == 0) {
				t.Fatalf("CC/BCC missing: cc=%#v bcc=%#v", m.CC, m.BCC)
			}
		})
	}
}

func TestExternalEmailCorpusNoPanics(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping external corpus sweep")
	}
	root := corpusRoot(t)
	var converted, failed int
	paths := []string{
		filepath.Join(root, "email"),
		filepath.Join(root, "vendored", "unstructured", "eml"),
		filepath.Join(root, "vendored", "unstructured", "msg"),
	}
	for _, dir := range paths {
		_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(path))
			if ext != ".eml" && ext != ".msg" {
				return nil
			}
			m, err := msg.ConvertFile(path, msg.Options{})
			if err != nil {
				failed++
				t.Logf("unsupported fixture %s: %v", filepath.Base(path), err)
				return nil
			}
			converted++
			if strings.TrimSpace(m.Markdown) == "" {
				t.Errorf("%s produced empty Markdown", filepath.Base(path))
			}
			return nil
		})
	}
	t.Logf("external email corpus: converted=%d failed=%d", converted, failed)
	if converted < 20 {
		t.Errorf("converted only %d corpus messages; want at least 20", converted)
	}
}

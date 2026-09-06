package msg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAttachmentNamesAreSafeAndUnique(t *testing.T) {
	attachments := []Attachment{
		{Name: "../../report.pdf", ContentType: "application/pdf", Data: []byte("one")},
		{Name: "REPORT.PDF", ContentType: "application/pdf", Data: []byte("two")},
		{Name: "..", ContentType: "text/plain", Data: []byte("three")},
	}
	makeAttachmentNamesUnique(attachments)
	want := []string{"report.pdf", "REPORT-2.PDF", "attachment-3.txt"}
	for i := range want {
		if attachments[i].Name != want[i] {
			t.Fatalf("attachment %d name = %q, want %q", i, attachments[i].Name, want[i])
		}
	}
	dir := t.TempDir()
	if err := writeAttachments(attachments, dir); err != nil {
		t.Fatal(err)
	}
	for _, name := range want {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("attachment %s not written: %v", name, err)
		}
	}
}

func TestCIDRewriteOnlyChangesHTMLReferences(t *testing.T) {
	source := `<p>logo.png is mentioned as text.</p><img src="logo.png"><a href="cid:document-id">open</a>`
	attachments := []Attachment{
		{Name: "inline logo.png", ContentLocation: "logo.png"},
		{Name: "document.pdf", ContentID: "document-id"},
	}
	got := rewriteCIDReferences(source, attachments, "files")
	if !strings.Contains(got, "logo.png is mentioned as text") {
		t.Fatalf("body text was rewritten:\n%s", got)
	}
	if !strings.Contains(got, "files/inline%20logo.png") || !strings.Contains(got, "files/document.pdf") {
		t.Fatalf("HTML references were not rewritten:\n%s", got)
	}
}

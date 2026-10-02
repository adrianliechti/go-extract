package pdf

import (
	"os"
	"testing"

	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

func TestLoadUsesStatelessConfiguration(t *testing.T) {
	data, err := os.ReadFile("testdata/fixtures/encrypted-secret123.pdf")
	if err != nil {
		t.Fatal(err)
	}
	configPath := model.ConfigPath
	doc, err := load(t.Context(), data, "secret123")
	if err != nil {
		t.Fatal(err)
	}
	if dir, available := doc.xref.Conf.UserFontStore(); dir != "" || available {
		t.Fatalf("user font store = (%q, %v), want disabled", dir, available)
	}
	if dir, available := doc.xref.Conf.TrustedCertificateStore(); dir != "" || available {
		t.Fatalf("certificate store = (%q, %v), want disabled", dir, available)
	}
	if model.ConfigPath != configPath {
		t.Fatalf("pdfcpu config path changed from %q to %q", configPath, model.ConfigPath)
	}
}

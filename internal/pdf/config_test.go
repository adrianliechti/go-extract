package pdf

import (
	"testing"

	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

func TestConfigDirDisabled(t *testing.T) {
	if model.ConfigPath != "disable" {
		t.Fatalf("pdfcpu config path = %q, want %q", model.ConfigPath, "disable")
	}
}

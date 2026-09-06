package pdf

import "github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"

func init() {
	// Extraction only needs pdfcpu's built-in defaults. Avoid reading or
	// creating process-wide configuration, font, and certificate directories.
	model.ConfigPath = "disable"
}

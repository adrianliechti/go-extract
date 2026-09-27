// Package props reads OOXML core document properties.
package props

import (
	"strings"

	"github.com/adrianliechti/go-extract/internal/ooxml/opc"
)

// coreProperties mirrors docProps/core.xml. The Dublin Core elements live in
// their own namespaces, matched here on local name.
type coreProperties struct {
	Title       string `xml:"title"`
	Subject     string `xml:"subject"`
	Creator     string `xml:"creator"`
	Description string `xml:"description"`
	Keywords    string `xml:"keywords"`
}

// Title returns the document title from core properties, or "" when absent.
func Title(pkg *opc.Package) string {
	part := pkg.RelatedPart("", opc.RelCoreProperties, "docProps/core.xml")
	if part == "" {
		return ""
	}

	var cp coreProperties
	if err := pkg.UnmarshalPart(part, &cp); err != nil {
		return ""
	}
	return strings.TrimSpace(cp.Title)
}

package ooxml_test

import (
	"strings"
	"testing"
)

// Reference: office-open-xml-viewer bef9a7dc. Choice/Fallback are meaningful
// only in the MC namespace, and Requires is a list of namespace prefixes.
func TestDocxCompatibilityBranches(t *testing.T) {
	const mc = ` xmlns:mc="http://schemas.openxmlformats.org/markup-compatibility/2006" xmlns:future="urn:future"`
	for _, location := range []string{"body", "paragraph", "run", "cell", "textbox"} {
		for _, scenario := range []string{"supported", "unsupported", "foreign choice", "shadowed prefix", "missing requires", "all required", "first supported"} {
			t.Run(location+"/"+scenario, func(t *testing.T) {
				content := func(text string) string {
					t := `<w:t>` + text + `</w:t>`
					if location != "run" {
						t = `<w:r>` + t + `</w:r>`
					}
					if location != "run" && location != "paragraph" {
						t = `<w:p>` + t + `</w:p>`
					}
					return t
				}
				choice, want := `<mc:Choice Requires="w">`+content("Selected")+`</mc:Choice>`, "Selected\n"
				switch scenario {
				case "unsupported":
					choice = `<mc:Choice Requires="future">` + content("Hidden") + `</mc:Choice>`
					want = "Fallback\n"
				case "foreign choice":
					choice = `<future:Choice Requires="w">` + content("Hidden") + `</future:Choice>`
					want = "Fallback\n"
				case "shadowed prefix":
					choice = `<mc:Choice xmlns:w="urn:future" Requires="w">` + content("Hidden") + `</mc:Choice>`
					want = "Fallback\n"
				case "missing requires":
					choice = `<mc:Choice>` + content("Hidden") + `</mc:Choice>`
					want = "Fallback\n"
				case "all required":
					choice = `<mc:Choice Requires="w future">` + content("Hidden") + `</mc:Choice>`
					want = "Fallback\n"
				case "first supported":
					choice = `<mc:Choice Requires="future">` + content("Hidden") + `</mc:Choice>` + choice + `<mc:Choice Requires="w">` + content("Also hidden") + `</mc:Choice>`
				}
				body := `<mc:AlternateContent` + mc + `>` + choice + `<mc:Fallback>` + content("Fallback") + `</mc:Fallback></mc:AlternateContent>`
				switch location {
				case "paragraph":
					body = `<w:p>` + body + `</w:p>`
				case "run":
					body = `<w:p><w:r>` + body + `</w:r></w:p>`
				case "cell":
					body = `<w:tbl><w:tr><w:tc>` + body + `</w:tc></w:tr></w:tbl>`
				case "textbox":
					body = `<w:p><w:r><w:drawing><w:txbxContent>` + body + `</w:txbxContent></w:drawing></w:r></w:p>`
				}
				got := convertDocxBody(t, body).Markdown
				if location == "cell" {
					if !strings.Contains(got, strings.TrimSpace(want)) || strings.Contains(got, "Hidden") || strings.Contains(got, "Also hidden") || strings.Contains(got, "Fallback") && want != "Fallback\n" {
						t.Fatalf("cell Markdown = %q, want only %q", got, want)
					}
				} else if got != want {
					t.Fatalf("Markdown = %q, want %q", got, want)
				}
			})
		}
	}
}

func TestDocxCompatibilityFiltersStylesAndNumbering(t *testing.T) {
	const namespaces = ` xmlns:mc="http://schemas.openxmlformats.org/markup-compatibility/2006" xmlns:future="urn:future"`
	choice := func(hidden, selected string) string {
		return `<mc:AlternateContent` + namespaces + `><future:Choice Requires="w">` + hidden + `</future:Choice><mc:Choice Requires="future">` + hidden + `</mc:Choice><mc:Fallback>` + selected + `</mc:Fallback></mc:AlternateContent>`
	}
	style := testPackagePart{name: "word/styles.xml", text: `<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:docDefaults><w:rPrDefault><w:rPr>` + choice(`<w:b/>`, `<w:i/>`) + `</w:rPr></w:rPrDefault></w:docDefaults></w:styles>`}
	numbering := numberingPart(`<w:abstractNum w:abstractNumId="0"><w:lvl w:ilvl="0">` + choice(`<w:start w:val="9"/><w:numFmt w:val="bullet"/>`, `<w:start w:val="3"/><w:numFmt w:val="decimal"/>`) + `</w:lvl></w:abstractNum><w:num w:numId="1"><w:abstractNumId w:val="0"/></w:num>`)
	if got := convertDocxBody(t, listParagraph("1", 0, "Item"), style, numbering).Markdown; got != "3. *Item*\n" {
		t.Fatalf("styles or numbering used the wrong branch: %q", got)
	}
}

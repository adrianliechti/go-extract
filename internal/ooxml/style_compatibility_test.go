package ooxml_test

import (
	"strings"
	"testing"
)

// Adapted from office-open-xml-viewer 4b1885db's default-style lexical
// regression, asserting extracted Markdown rather than renderer properties.
func TestDocxDefaultParagraphStyleLexicals(t *testing.T) {
	for _, lexical := range []string{"1", "true", "on", " true ", "0", "false", "off", " false ", ""} {
		t.Run(lexical, func(t *testing.T) {
			styles := `<w:style w:type="paragraph" w:default="` + lexical + `" w:styleId="Default"><w:basedOn w:val="Base"/></w:style>
<w:style w:type="paragraph" w:styleId="Base"><w:pPr><w:outlineLvl w:val="1"/></w:pPr></w:style>
<w:style w:type="table" w:default="true" w:styleId="Table"><w:pPr><w:outlineLvl w:val="0"/></w:pPr></w:style>`
			doc := convertDocxBody(t, `<w:p><w:r><w:t>Implicit style</w:t></w:r></w:p>`, stylePart(styles))
			want := "Implicit style\n"
			switch strings.TrimSpace(lexical) {
			case "1", "true", "on":
				want = "## " + want
			}
			if doc.Markdown != want {
				t.Fatalf("Markdown = %q, want %q", doc.Markdown, want)
			}
		})
	}
}

func TestDocxStylePrecedence(t *testing.T) {
	const styles = `
<w:docDefaults><w:rPrDefault><w:rPr><w:i/></w:rPr></w:rPrDefault></w:docDefaults>
<w:style w:type="paragraph" w:default="true" w:styleId="Default"><w:pPr><w:outlineLvl w:val="1"/></w:pPr><w:rPr><w:b/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="Heading1"><w:pPr><w:outlineLvl w:val="2"/></w:pPr></w:style>
<w:style w:type="paragraph" w:styleId="Body"><w:basedOn w:val="Heading1"/><w:pPr><w:outlineLvl w:val="9"/></w:pPr></w:style>
<w:style w:type="paragraph" w:styleId="Plain"/>
<w:style w:type="character" w:styleId="Toggle"><w:rPr><w:b/><w:i w:val="false"/></w:rPr></w:style>
<w:style w:type="character" w:styleId="Twice"><w:basedOn w:val="Toggle"/><w:rPr><w:b/></w:rPr></w:style>`
	for _, tc := range []struct{ name, ppr, rpr, want string }{
		{"default run formatting", "", "", "## ***Text***\n"},
		{"explicit style replaces default", `<w:pStyle w:val="Plain"/>`, "", "*Text*\n"},
		{"outline overrides name", `<w:pStyle w:val="Heading1"/>`, "", "### *Text*\n"},
		{"inherited heading cancelled", `<w:pStyle w:val="Body"/>`, "", "*Text*\n"},
		{"direct heading cancelled", `<w:pStyle w:val="Heading1"/><w:outlineLvl w:val="9"/>`, "", "*Text*\n"},
		{"direct outline", `<w:pStyle w:val="Plain"/><w:outlineLvl w:val="0"/>`, "", "# *Text*\n"},
		{"direct formatting off", "", `<w:b w:val="0"/><w:i w:val=" off "/>`, "## Text\n"},
		{"character style toggles", "", `<w:rStyle w:val="Toggle"/>`, "## *Text*\n"},
		{"inherited toggle twice", "", `<w:rStyle w:val="Twice"/>`, "## ***Text***\n"},
		{"direct formatting overrides toggle", "", `<w:rStyle w:val="Toggle"/><w:b w:val=" true "/>`, "## ***Text***\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := `<w:p><w:pPr>` + tc.ppr + `</w:pPr><w:r><w:rPr>` + tc.rpr + `</w:rPr><w:t>Text</w:t></w:r></w:p>`
			doc := convertDocxBody(t, body, stylePart(styles))
			if doc.Markdown != tc.want {
				t.Fatalf("Markdown = %q, want %q", doc.Markdown, tc.want)
			}
		})
	}
}

func TestDocxNumberingOverrides(t *testing.T) {
	const styles = `
<w:style w:type="paragraph" w:default="on" w:styleId="List"><w:pPr><w:numPr><w:numId w:val="1"/><w:ilvl w:val="1"/></w:numPr></w:pPr></w:style>
<w:style w:type="paragraph" w:styleId="Cancelled"><w:basedOn w:val="List"/><w:pPr><w:numPr><w:numId w:val="0"/></w:numPr></w:pPr></w:style>
<w:style w:type="paragraph" w:styleId="Deeper"><w:basedOn w:val="List"/><w:pPr><w:numPr><w:ilvl w:val="2"/></w:numPr></w:pPr></w:style>`
	for _, tc := range []struct{ name, ppr, want string }{
		{"default list", "", "    - Item\n"},
		{"direct cancellation", `<w:numPr><w:numId w:val="0"/></w:numPr>`, "Item\n"},
		{"style cancellation", `<w:pStyle w:val="Cancelled"/>`, "Item\n"},
		{"direct level with inherited id", `<w:numPr><w:ilvl w:val="2"/></w:numPr>`, "        - Item\n"},
		{"style level with inherited id", `<w:pStyle w:val="Deeper"/>`, "        - Item\n"},
		{"direct id with inherited level", `<w:numPr><w:numId w:val="2"/></w:numPr>`, "    - Item\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := convertDocxBody(t, `<w:p><w:pPr>`+tc.ppr+`</w:pPr><w:r><w:t>Item</w:t></w:r></w:p>`, stylePart(styles))
			if doc.Markdown != tc.want {
				t.Fatalf("Markdown = %q, want %q", doc.Markdown, tc.want)
			}
		})
	}
}

func stylePart(styles string) testPackagePart {
	return testPackagePart{name: "word/styles.xml", text: `<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">` + styles + `</w:styles>`}
}

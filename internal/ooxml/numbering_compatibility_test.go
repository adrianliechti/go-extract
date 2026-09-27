package ooxml_test

import (
	"fmt"
	"strings"
	"testing"
)

// Reference: office-open-xml-viewer 8fbcedd1 and its Word-measured counter
// controls. Markdown uses decimal markers but must retain the same counts.
func TestDocxNumberingRestartAndOverrides(t *testing.T) {
	for _, tc := range []struct {
		name, restart, override string
		want                    string
	}{
		{"default restart", "", "", "1. Parent\n    1. Child\n2. Parent\n    1. Child\n"},
		{"never restart", `<w:lvlRestart w:val="0"/>`, "", "1. Parent\n    1. Child\n2. Parent\n    2. Child\n"},
		{"start override", "", `<w:startOverride w:val="7"/>`, "1. Parent\n    7. Child\n2. Parent\n    7. Child\n"},
		{"override without restart", `<w:lvlRestart w:val="0"/>`, `<w:startOverride w:val="7"/>`, "1. Parent\n    7. Child\n2. Parent\n    8. Child\n"},
		{"replacement restores restart", `<w:lvlRestart w:val="0"/>`, `<w:lvl w:ilvl="1"><w:start w:val="3"/><w:numFmt w:val="decimal"/></w:lvl>`, "1. Parent\n    3. Child\n2. Parent\n    3. Child\n"},
		{"replacement disables restart", "", `<w:lvl w:ilvl="1"><w:start w:val="3"/><w:numFmt w:val="decimal"/><w:lvlRestart w:val="0"/></w:lvl>`, "1. Parent\n    3. Child\n2. Parent\n    4. Child\n"},
		{"start override wins replacement", "", `<w:startOverride w:val="7"/><w:lvl w:ilvl="1"><w:start w:val="3"/><w:numFmt w:val="decimal"/></w:lvl>`, "1. Parent\n    7. Child\n2. Parent\n    7. Child\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			numbering := `<w:abstractNum w:abstractNumId="0"><w:lvl w:ilvl="0"><w:start w:val="1"/><w:numFmt w:val="decimal"/></w:lvl><w:lvl w:ilvl="1"><w:start w:val="1"/><w:numFmt w:val="decimal"/>` + tc.restart + `</w:lvl></w:abstractNum><w:num w:numId="1"><w:abstractNumId w:val="0"/><w:lvlOverride w:ilvl="1">` + tc.override + `</w:lvlOverride></w:num>`
			body := listParagraph("1", 0, "Parent") + listParagraph("1", 1, "Child")
			doc := convertDocxBody(t, body+body, numberingPart(numbering))
			if doc.Markdown != tc.want {
				t.Fatalf("Markdown = %q, want %q", doc.Markdown, tc.want)
			}
		})
	}
}

func TestDocxSharedAbstractCounter(t *testing.T) {
	for _, override := range []string{"", `<w:lvlOverride w:ilvl="0"><w:startOverride w:val="1"/></w:lvlOverride>`} {
		numbering := `<w:abstractNum w:abstractNumId="0"><w:lvl w:ilvl="0"><w:start w:val="1"/><w:numFmt w:val="decimal"/></w:lvl></w:abstractNum><w:num w:numId="1"><w:abstractNumId w:val="0"/></w:num><w:num w:numId="2"><w:abstractNumId w:val="0"/>` + override + `</w:num>`
		body := listParagraph("1", 0, "Item") + listParagraph("1", 0, "Item") + listParagraph("2", 0, "Item") + listParagraph("1", 0, "Item")
		want := "1. Item\n2. Item\n3. Item\n4. Item\n"
		if override != "" {
			want = "1. Item\n2. Item\n1. Item\n2. Item\n"
		}
		if got := convertDocxBody(t, body, numberingPart(numbering)).Markdown; got != want {
			t.Errorf("override=%q: Markdown=%q, want %q", override, got, want)
		}
	}
}

func TestDocxNumberingZeroAndNone(t *testing.T) {
	for _, tc := range []struct{ format, want string }{
		{"decimal", "0. Item\n1. Item\n"},
		{"none", "Item\n\nItem\n"},
	} {
		numbering := `<w:abstractNum w:abstractNumId="0"><w:lvl w:ilvl="0"><w:start w:val="0"/><w:numFmt w:val="` + tc.format + `"/></w:lvl></w:abstractNum><w:num w:numId="1"><w:abstractNumId w:val="0"/></w:num>`
		body := strings.Repeat(listParagraph("1", 0, "Item"), 2)
		if got := convertDocxBody(t, body, numberingPart(numbering)).Markdown; got != tc.want {
			t.Errorf("format=%q: Markdown=%q, want %q", tc.format, got, tc.want)
		}
	}
}

func listParagraph(id string, level int, text string) string {
	return fmt.Sprintf(`<w:p><w:pPr><w:numPr><w:ilvl w:val="%d"/><w:numId w:val="%s"/></w:numPr></w:pPr><w:r><w:t>%s</w:t></w:r></w:p>`, level, id, text)
}

func numberingPart(body string) testPackagePart {
	return testPackagePart{name: "word/numbering.xml", text: `<w:numbering xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">` + body + `</w:numbering>`}
}

package pptx

import (
	"encoding/xml"
	"strconv"
	"strings"
	"testing"
)

func TestParseTextParagraphBoundsListLevels(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	for _, level := range []int{-maxInt - 1, -1, 0, 8, 9, maxInt} {
		t.Run(strconv.Itoa(level), func(t *testing.T) {
			input := `<a:p xmlns:a="urn:a"><a:pPr lvl="` + strconv.Itoa(level) + `"><a:buChar char="•"/></a:pPr><a:r><a:t>Item</a:t></a:r></a:p>`
			p, ok := parseParagraphFixture(t, input)
			if want := min(8, max(0, level)); !ok || p.level != want {
				t.Fatalf("paragraph level = %d, ok = %v, want %d", p.level, ok, want)
			}
		})
	}
}

func TestParseTextParagraphBulletKinds(t *testing.T) {
	tests := []struct {
		name    string
		xml     string
		bullet  bulletKind
		level   int
		startAt int
		text    string
	}{
		{name: "inherit", xml: `<a:p xmlns:a="urn:a"><a:r><a:t>plain</a:t></a:r></a:p>`, bullet: bulletInherit, text: "plain"},
		{name: "none", xml: `<a:p xmlns:a="urn:a"><a:pPr><a:buNone/></a:pPr><a:r><a:t>none</a:t></a:r></a:p>`, bullet: bulletNone, text: "none"},
		{name: "character", xml: `<a:p xmlns:a="urn:a"><a:pPr lvl="1"><a:buChar char="•"/></a:pPr><a:r><a:t>bullet</a:t></a:r></a:p>`, bullet: bulletUnordered, level: 1, text: "bullet"},
		{name: "automatic number", xml: `<a:p xmlns:a="urn:a"><a:pPr lvl="2"><a:buAutoNum type="arabicPeriod" startAt="3"/></a:pPr><a:r><a:t>numbered</a:t></a:r></a:p>`, bullet: bulletOrdered, level: 2, startAt: 3, text: "numbered"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseParagraphFixture(t, tc.xml)
			if !ok {
				t.Fatal("paragraph was not parsed")
			}
			if got.bullet != tc.bullet || got.level != tc.level || got.startAt != tc.startAt || got.text != tc.text {
				t.Fatalf("paragraph = %#v, want bullet=%v level=%d startAt=%d text=%q", got, tc.bullet, tc.level, tc.startAt, tc.text)
			}
		})
	}
}

func TestParseShapeVisibilityAndTextBoxMarker(t *testing.T) {
	visible, ok := parseShapeFixture(t, `<p:sp xmlns:p="urn:p" xmlns:a="urn:a"><p:nvSpPr><p:cNvPr id="1"/><p:cNvSpPr txBox="1"/><p:nvPr/></p:nvSpPr><p:txBody><a:p><a:r><a:t>text</a:t></a:r></a:p></p:txBody></p:sp>`)
	if !ok || !visible.isTextBox || visible.inheritsBullet {
		t.Fatalf("text box shape = %#v, want visible text box without inherited bullets", visible)
	}

	body, ok := parseShapeFixture(t, `<p:sp xmlns:p="urn:p" xmlns:a="urn:a"><p:nvSpPr><p:cNvPr id="2"/><p:cNvSpPr/><p:nvPr><p:ph type="body"/></p:nvPr></p:nvSpPr><p:txBody><a:p><a:r><a:t>body</a:t></a:r></a:p></p:txBody></p:sp>`)
	if !ok || !body.inheritsBullet || body.isTitle {
		t.Fatalf("body shape = %#v, want inherited bullets", body)
	}

	hidden, ok := parseShapeFixture(t, `<p:sp xmlns:p="urn:p" xmlns:a="urn:a"><p:nvSpPr><p:cNvPr id="3" hidden="1"/><p:cNvSpPr/><p:nvPr/></p:nvSpPr><p:txBody><a:p><a:r><a:t>hidden</a:t></a:r></a:p></p:txBody></p:sp>`)
	if ok || !hidden.hidden {
		t.Fatalf("hidden shape = %#v, ok=%v; want omitted", hidden, ok)
	}
}

func parseParagraphFixture(t *testing.T, input string) (slidePara, bool) {
	t.Helper()
	dec := xml.NewDecoder(strings.NewReader(input))
	tok, err := dec.Token()
	if err != nil {
		t.Fatal(err)
	}
	start, ok := tok.(xml.StartElement)
	if !ok {
		t.Fatal("fixture does not start with an element")
	}
	return parseTextParagraph(dec, start)
}

func parseShapeFixture(t *testing.T, input string) (shape, bool) {
	t.Helper()
	dec := xml.NewDecoder(strings.NewReader(input))
	tok, err := dec.Token()
	if err != nil {
		t.Fatal(err)
	}
	start, ok := tok.(xml.StartElement)
	if !ok {
		t.Fatal("fixture does not start with an element")
	}
	return parseShape(dec, start)
}

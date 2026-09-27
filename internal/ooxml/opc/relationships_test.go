package opc

import (
	"errors"
	"testing"
)

// Reference: office-open-xml-viewer 481504ec, 4e3fd99d, ecf98f6d and
// 9a8729ed. Resolution precedes percent normalization; invalid references
// must not become valid by dropping their malformed segments.
func TestRelationshipPartResolution(t *testing.T) {
	for _, tc := range []struct{ target, want string }{
		{"./styles.xml", "word/styles.xml"},
		{"../word/styles.xml", "word/styles.xml"},
		{"/word/styles.xml", "word/styles.xml"},
		{"%73tyles.xml#bookmark", "word/styles.xml"},
		{"%2E%2E/word/styles.xml", "word/styles.xml"},
		{"%2E%2E/../styles.xml", "word/styles.xml"},
		{"../../styles.xml", "styles.xml"},
		{"", "word/document.xml"},
		{"#bookmark", "word/document.xml"},
		{"../media/日本.png", "media/%E6%97%A5%E6%9C%AC.png"},
		{"../media/a%20b.png", "media/a%20b.png"},
		{"https://example.com/styles.xml", ""},
		{"//example.com/styles.xml", ""},
		{"styles.xml?version=1", ""},
		{"styles.xml/", ""},
		{"dir//styles.xml", ""},
		{"dir./styles.xml", ""},
		{"dir\\styles.xml", ""},
		{"bad space/../styles.xml", ""},
		{"%GG/../styles.xml", ""},
		{"%/../styles.xml", ""},
		{"%2/../styles.xml", ""},
		{"dir%2Fstyles.xml", ""},
		{"dir%5cstyles.xml", ""},
		{"/[Content_Types].xml", ""},
		{"./\ue000.xml", ""},
	} {
		t.Run(tc.target, func(t *testing.T) {
			r := Relationship{SourcePart: "word/document.xml", Target: tc.target}
			if got := r.ResolvePart(); got != tc.want {
				t.Fatalf("ResolvePart() = %q, want %q", got, tc.want)
			}
		})
	}
	r := Relationship{Target: "word/styles.xml", External: true}
	if r.ResolvePart() != "" || r.Resolve() != r.Target {
		t.Fatal("external target must remain a URL and must not name a package part")
	}
}

func TestEquivalentPartLookupAndContentTypes(t *testing.T) {
	pkg, err := Open(testArchive(t, []testPart{
		{"[Content_Types].xml", `<Types><Default Extension="svg" ContentType="image/svg+xml"/><Override PartName="/WORD/%64ocument.xml" ContentType="document-type"/></Types>`},
		{"word/Document.xml", "document"},
		{"word/media/日本.%73vg", "image"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, data, contentType string }{
		{"WORD/%64OCUMENT.XML", "document", "document-type"},
		{"word/media/%e6%97%a5%e6%9c%ac.svg", "image", "image/svg+xml"},
	} {
		got, err := pkg.ReadPart(tc.name)
		if err != nil || string(got) != tc.data || pkg.ContentType(tc.name) != tc.contentType {
			t.Errorf("lookup %q: data=%q error=%v contentType=%q", tc.name, got, err, pkg.ContentType(tc.name))
		}
	}
}

func TestEquivalentDuplicatePartsAreRejected(t *testing.T) {
	for _, alias := range []string{"WORD/document.xml", "word/%64ocument.xml", "word/document.xml"} {
		_, err := Open(testArchive(t, []testPart{
			{"[Content_Types].xml", "<Types/>"},
			{"word/document.xml", "first"},
			{alias, "second"},
		}))
		if !errors.Is(err, ErrNotOOXML) {
			t.Errorf("duplicate %q: error = %v, want ErrNotOOXML", alias, err)
		}
	}
}

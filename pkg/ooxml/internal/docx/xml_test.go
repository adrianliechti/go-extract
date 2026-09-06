package docx

import (
	"encoding/xml"
	"strconv"
	"strings"
	"testing"
)

func TestListInfoBoundsLevelsBeforeCounterArithmetic(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	for _, level := range []int{-maxInt - 1, -1, 0, 8, 9, maxInt} {
		t.Run(strconv.Itoa(level), func(t *testing.T) {
			c := &converter{}
			p := &paragraph{PPr: &paraProps{NumPr: &numPr{
				ILvl: &val{Val: strconv.Itoa(level)}, NumID: &val{Val: "1"},
			}}}
			_, got, ok := c.listInfo(p, "")
			if want := min(8, max(0, level)); !ok || got != want {
				t.Fatalf("listInfo level = %d, ok = %v, want %d", got, ok, want)
			}
		})
	}
}

func TestParagraphTrackedMovesUseFinalView(t *testing.T) {
	const input = `<w:p xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
		<w:r><w:t xml:space="preserve">Keep </w:t></w:r>
		<w:ins w:id="1"><w:r><w:t>added</w:t></w:r></w:ins>
		<w:moveFrom w:id="2"><w:r><w:t> moved away</w:t></w:r></w:moveFrom>
		<w:moveTo w:id="2"><w:r><w:t> and moved here</w:t></w:r></w:moveTo>
		<w:del w:id="3"><w:r><w:delText> removed</w:delText></w:r></w:del>
	</w:p>`

	var p paragraph
	if err := xml.NewDecoder(strings.NewReader(input)).Decode(&p); err != nil {
		t.Fatal(err)
	}

	var got strings.Builder
	for _, item := range p.Content {
		if item.Run != nil {
			got.WriteString(item.Run.Text)
		}
	}
	if want := "Keep added and moved here"; got.String() != want {
		t.Fatalf("tracked-change text = %q, want %q", got.String(), want)
	}
}

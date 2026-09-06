package pdf

// Differential harness for the extraction stage.
//
// testdata/golden/items holds the reference implementation's positioned text
// items for every fixture. Comparison is positional and tolerant: coordinates
// are matched within a small epsilon because the two implementations
// accumulate float32 in a different order.
//
// Like the Markdown harness this reports a fidelity score rather than failing
// outright while the port is in progress. Set ITEMS_STRICT=1 to turn a drop in
// fidelity into a failure.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

const itemsGoldenDir = "testdata/golden/items"

func TestClipAndNormalizePage(t *testing.T) {
	page := pageExtraction{
		items: []TextItem{
			{Text: "inside", X: 110, Y: 220, Width: 20, Height: 10, Page: 1},
			{Text: "partial", X: 290, Y: 220, Width: 30, Height: 10, Page: 1},
			{Text: "outside", X: 310, Y: 220, Width: 20, Height: 10, Page: 1},
		},
		rects: []Rect{
			{X: 90, Y: 190, Width: 30, Height: 30, Page: 1},
			{X: 310, Y: 210, Width: 10, Height: 10, Page: 1},
		},
		lines: []Line{
			{X1: 50, Y1: 250, X2: 350, Y2: 250, Page: 1},
			{X1: 50, Y1: 100, X2: 50, Y2: 500, Page: 1},
		},
	}
	attrs := &model.InheritedPageAttrs{
		MediaBox: types.NewRectangle(0, 0, 500, 500),
		CropBox:  types.NewRectangle(100, 200, 300, 400),
	}

	clipAndNormalizePage(&page, attrs)

	if len(page.items) != 2 || page.items[0].Text != "inside" || page.items[0].X != 10 || page.items[0].Y != 20 ||
		page.items[1].Text != "partial" || page.items[1].X != 190 || page.items[1].Y != 20 {
		t.Fatalf("items = %#v", page.items)
	}
	if len(page.rects) != 1 || page.rects[0].X != 0 || page.rects[0].Y != 0 || page.rects[0].Width != 20 || page.rects[0].Height != 20 {
		t.Fatalf("rects = %#v", page.rects)
	}
	if len(page.lines) != 1 || page.lines[0].X1 != 0 || page.lines[0].Y1 != 50 || page.lines[0].X2 != 200 || page.lines[0].Y2 != 50 {
		t.Fatalf("lines = %#v", page.lines)
	}
}

// goldenItems mirrors the JSON emitted by scripts/gen-golden.sh.
type goldenItems struct {
	TotalItems int          `json:"total_items"`
	Items      []goldenItem `json:"items"`
}

type goldenItem struct {
	Text     string  `json:"text"`
	Page     uint32  `json:"page"`
	X        float32 `json:"x"`
	Y        float32 `json:"y"`
	Width    float32 `json:"width"`
	Height   float32 `json:"height"`
	Font     string  `json:"font"`
	FontSize float32 `json:"font_size"`
	IsBold   bool    `json:"is_bold"`
	IsItalic bool    `json:"is_italic"`
	ItemType string  `json:"item_type"`
}

func TestExtractionFidelity(t *testing.T) {
	entries, err := os.ReadDir(itemsGoldenDir)
	if err != nil {
		t.Skipf("goldens unavailable (run scripts/gen-golden.sh): %v", err)
	}

	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			names = append(names, strings.TrimSuffix(e.Name(), ".json"))
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		t.Skip("no item goldens found")
	}

	var totalWant, totalGot, totalMatched int
	var report []string
	strict := os.Getenv("ITEMS_STRICT") != ""

	for _, name := range names {
		want, err := loadGoldenItems(filepath.Join(itemsGoldenDir, name+".json"))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}

		got, err := extractFixture(name)
		if err != nil {
			report = append(report, fmt.Sprintf("  %-46s ERROR %v", name, err))
			totalWant += len(want.Items)
			if strict {
				t.Errorf("%s: extraction failed: %v", name, err)
			}
			continue
		}

		matched := countMatchingText(want.Items, got)
		totalWant += len(want.Items)
		totalGot += len(got)
		totalMatched += matched

		pct := 0.0
		if len(want.Items) > 0 {
			pct = 100 * float64(matched) / float64(len(want.Items))
		}
		report = append(report, fmt.Sprintf("  %-46s %5.1f%%  want=%-6d got=%-6d matched=%d",
			name, pct, len(want.Items), len(got), matched))
		if strict && matched != len(want.Items) {
			t.Errorf("%s: extraction mismatch: matched %d/%d reference items", name, matched, len(want.Items))
		}
	}

	pct := 0.0
	if totalWant > 0 {
		pct = 100 * float64(totalMatched) / float64(totalWant)
	}
	t.Logf("extraction fidelity: %.1f%% (%d/%d items matched, %d extracted)",
		pct, totalMatched, totalWant, totalGot)
	for _, line := range report {
		t.Log(line)
	}
}

func loadGoldenItems(path string) (*goldenItems, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var g goldenItems
	if err := json.Unmarshal(data, &g); err != nil {
		return nil, err
	}
	return &g, nil
}

// extractFixture runs the extractor over a fixture and returns its text items.
func extractFixture(name string) ([]TextItem, error) {
	data, err := os.ReadFile(filepath.Join("testdata/fixtures", name+".pdf"))
	if err != nil {
		return nil, err
	}
	doc, err := load(data, fixturePassword(name))
	if err != nil {
		return nil, err
	}
	pages, err := doc.extract(nil, false)
	if err != nil {
		return nil, err
	}
	var items []TextItem
	for _, p := range pages {
		items = append(items, p.items...)
	}
	return items, nil
}

func fixturePassword(name string) string {
	if name == "encrypted-secret123" {
		return "secret123"
	}
	return ""
}

// countMatchingText counts reference items for which an extracted item on the
// same page carries the same text at approximately the same position. Matching
// is greedy and one-to-one, so duplicated text cannot inflate the score.
func countMatchingText(want []goldenItem, got []TextItem) int {
	// Index candidates by page and text to keep the comparison linear.
	type key struct {
		page uint32
		text string
	}
	index := map[key][]int{}
	for i, g := range got {
		k := key{g.Page, strings.TrimSpace(g.Text)}
		index[k] = append(index[k], i)
	}

	used := make([]bool, len(got))
	matched := 0

	for _, w := range want {
		k := key{w.Page, strings.TrimSpace(w.Text)}
		best, bestDist := -1, float32(0)
		for _, i := range index[k] {
			if used[i] {
				continue
			}
			d := absF32(got[i].X-w.X) + absF32(got[i].Y-w.Y)
			if best < 0 || d < bestDist {
				best, bestDist = i, d
			}
		}
		// 2pt of slack absorbs float32 accumulation order differences without
		// letting a genuinely misplaced item count as a match.
		if best >= 0 && bestDist <= 2.0 {
			used[best] = true
			matched++
		}
	}
	return matched
}

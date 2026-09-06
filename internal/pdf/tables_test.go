package pdf

import (
	"strings"
	"testing"
)

func tableText(text string, x, y, width float32) TextItem {
	return TextItem{
		Text: text, X: x, Y: y, Width: width, Height: 10, FontSize: 10, Page: 1,
		Type: ItemType{Kind: KindText},
	}
}

func horizontalRule(x1, x2, y float32) Line {
	return Line{X1: x1, Y1: y, X2: x2, Y2: y, Page: 1, StrokeWidth: 1}
}

func verticalRule(x, y1, y2 float32) Line {
	return Line{X1: x, Y1: y1, X2: x, Y2: y2, Page: 1, StrokeWidth: 1}
}

func TestDetectRuledTableJoinsMultilineCells(t *testing.T) {
	items := []TextItem{
		tableText("Name", 10, 90, 30), tableText("Value", 110, 90, 30),
		tableText("Long", 10, 70, 25), tableText("label", 10, 60, 25), tableText("42", 110, 65, 12),
		tableText("Other", 10, 30, 30), tableText("7", 110, 30, 8),
	}
	lines := []Line{
		horizontalRule(0, 200, 100), horizontalRule(0, 200, 80),
		horizontalRule(0, 200, 50), horizontalRule(0, 200, 20),
		verticalRule(0, 20, 100), verticalRule(100, 20, 100), verticalRule(200, 20, 100),
	}

	tables := detectPageTables(items, nil, lines, 1)
	if len(tables) != 1 {
		t.Fatalf("detected %d tables: %#v", len(tables), tables)
	}
	got := tables[0]
	if len(got.cells) != 3 || len(got.cells[0]) != 2 {
		t.Fatalf("grid = %dx%d", len(got.cells), len(got.cells[0]))
	}
	if got.cells[1][0].text != "Long label" || got.cells[1][1].text != "42" {
		t.Fatalf("second row = %#v", got.cells[1])
	}
	want := "|Name|Value|\n|---|---|\n|Long label|42|\n|Other|7|\n"
	if md := tableToMarkdown(got); md != want {
		t.Fatalf("markdown = %q, want %q", md, want)
	}
}

func TestMergedHeaderCellRendersWithColspan(t *testing.T) {
	items := []TextItem{
		tableText("Identity", 20, 105, 120), tableText("Value", 220, 105, 40),
		tableText("First", 20, 75, 30), tableText("A", 120, 75, 10), tableText("1", 220, 75, 10),
		tableText("Second", 20, 45, 35), tableText("B", 120, 45, 10), tableText("2", 220, 45, 10),
	}
	lines := []Line{
		horizontalRule(0, 300, 120), horizontalRule(0, 300, 90), horizontalRule(0, 300, 60), horizontalRule(0, 300, 30),
		verticalRule(0, 30, 120), verticalRule(100, 30, 90), verticalRule(200, 30, 120), verticalRule(300, 30, 120),
	}
	tables := detectPageTables(items, nil, lines, 1)
	if len(tables) != 1 {
		t.Fatalf("detected %d tables", len(tables))
	}
	got := tableToMarkdown(tables[0])
	if !strings.Contains(got, `<th colspan="2">Identity</th>`) || !strings.Contains(got, "<td>Second</td>") {
		t.Fatalf("merged table HTML = %q", got)
	}
}

func TestPartialHeaderRuleCreatesGroupedSpans(t *testing.T) {
	items := []TextItem{
		tableText("ID", 20, 105, 20), tableText("Metrics", 140, 105, 60), tableText("Note", 320, 105, 30),
		tableText("Left", 120, 75, 30), tableText("Right", 220, 75, 35),
		tableText("A", 20, 45, 10), tableText("1", 120, 45, 10), tableText("2", 220, 45, 10), tableText("ok", 320, 45, 15),
	}
	lines := []Line{
		horizontalRule(0, 400, 120), horizontalRule(100, 300, 90), horizontalRule(0, 400, 60), horizontalRule(0, 400, 30),
		verticalRule(0, 30, 120), verticalRule(100, 30, 120), verticalRule(200, 30, 90), verticalRule(300, 30, 120), verticalRule(400, 30, 120),
	}
	tables := detectPageTables(items, nil, lines, 1)
	if len(tables) != 1 {
		t.Fatalf("detected %d tables", len(tables))
	}
	got := tableToMarkdown(tables[0])
	for _, want := range []string{`<th rowspan="2">ID</th>`, `<th colspan="2">Metrics</th>`, `<th rowspan="2">Note</th>`} {
		if !strings.Contains(got, want) {
			t.Fatalf("grouped header missing %q in %q", want, got)
		}
	}
}

func TestContainingTableUsesFullSpannedCellBounds(t *testing.T) {
	parent := &detectedTable{
		colEdges: []float32{0, 50, 100}, rowEdges: []float32{100, 0},
		cells: [][]tableCell{{{colSpan: 2}, {covered: true}}},
	}
	child := &detectedTable{x1: 30, x2: 70, yTop: 80, yBot: 20}
	row, col, ok := containingTableCell(parent, child)
	if !ok || row != 0 || col != 0 {
		t.Fatalf("containing spanned cell = (%d, %d, %v)", row, col, ok)
	}
}

func TestDetectBorderlessAlignedTable(t *testing.T) {
	items := []TextItem{
		tableText("Year", 10, 100, 30), tableText("Revenue", 110, 100, 45), tableText("Margin", 210, 100, 35),
		tableText("2024", 10, 80, 25), tableText("100", 110, 80, 20), tableText("20%", 210, 80, 20),
		tableText("2025", 10, 60, 25), tableText("120", 110, 60, 20), tableText("22%", 210, 60, 20),
		tableText("2026", 10, 40, 25), tableText("140", 110, 40, 20), tableText("25%", 210, 40, 20),
	}
	tables := detectPageTables(items, nil, nil, 1)
	if len(tables) != 1 {
		t.Fatalf("detected %d tables", len(tables))
	}
	if got := tableToMarkdown(tables[0]); !strings.Contains(got, "|2025|120|22%|") {
		t.Fatalf("markdown = %q", got)
	}
}

func TestDetectSparseRuleTableWithoutVerticalDividers(t *testing.T) {
	items := []TextItem{
		tableText("Year", 14, 90, 28), tableText("Revenue", 94, 90, 44), tableText("Margin", 176, 90, 36),
		tableText("2025", 15, 65, 25), tableText("120", 103, 65, 18), tableText("22%", 184, 65, 18),
	}
	lines := []Line{
		horizontalRule(0, 240, 105),
		horizontalRule(0, 240, 80),
		horizontalRule(0, 240, 50),
	}
	tables := detectPageTables(items, nil, lines, 1)
	if len(tables) != 1 {
		t.Fatalf("detected %d tables", len(tables))
	}
	want := "|Year|Revenue|Margin|\n|---|---|---|\n|2025|120|22%|\n"
	if got := tableToMarkdown(tables[0]); got != want {
		t.Fatalf("markdown = %q, want %q", got, want)
	}
}

func TestSparseRuleTableReconstructsRowsBetweenRules(t *testing.T) {
	items := []TextItem{
		tableText("Year", 14, 110, 28), tableText("Revenue", 94, 110, 44), tableText("Margin", 176, 110, 36),
		tableText("2024", 14, 82, 25), tableText("100", 103, 82, 18), tableText("20%", 184, 82, 18),
		tableText("2025", 14, 62, 25), tableText("120", 103, 62, 18), tableText("22%", 184, 62, 18),
		tableText("2026", 14, 42, 25), tableText("140", 103, 42, 18), tableText("25%", 184, 42, 18),
	}
	lines := []Line{
		horizontalRule(0, 240, 125),
		horizontalRule(0, 240, 98),
		horizontalRule(0, 240, 28),
	}
	tables := detectPageTables(items, nil, lines, 1)
	if len(tables) != 1 {
		t.Fatalf("detected %d tables", len(tables))
	}
	if len(tables[0].cells) != 4 {
		t.Fatalf("rows = %d, want header plus three body rows", len(tables[0].cells))
	}
	if got := tableToMarkdown(tables[0]); !strings.Contains(got, "|2025|120|22%|") {
		t.Fatalf("markdown = %q", got)
	}
}

func TestBuildTableDoesNotOverlapInternalCellTolerance(t *testing.T) {
	items := []TextItem{
		tableText("A", 10, 75, 10),
		// Its center is one point to the right of x=50. The old symmetric
		// edge tolerance also admitted it to the left cell.
		tableText("B", 48, 75, 6),
		tableText("C", 10, 25, 10),
		tableText("D", 60, 25, 10),
	}
	table := buildTableFromEdges(items, []float32{0, 50, 100}, []float32{100, 50, 0}, 1, true)
	if table == nil {
		t.Fatal("expected table")
	}
	if got, want := table.cells[0][0].text, "A"; got != want {
		t.Fatalf("left header cell = %q, want %q", got, want)
	}
	if got, want := table.cells[0][1].text, "B"; got != want {
		t.Fatalf("right header cell = %q, want %q", got, want)
	}
}

func TestStrongGridAcceptsVerySparseCells(t *testing.T) {
	items := []TextItem{
		tableText("A", 5, 95, 5),
		tableText("B", 85, 75, 5),
		tableText("C", 25, 35, 5),
		tableText("D", 65, 15, 5),
	}
	cols := []float32{0, 20, 40, 60, 80, 100}
	rows := []float32{100, 80, 60, 40, 20, 0}
	table := buildTableFromEdges(items, cols, rows, 1, true)
	if table == nil {
		t.Fatal("an explicit grid remains a table below 18% cell density")
	}
	if got := tableCellDensity(table.cells); got >= 0.18 {
		t.Fatalf("test setup density = %.2f, want below old threshold", got)
	}
}

func TestSparseRulesSplitAtUnexplainedEmptyBand(t *testing.T) {
	items := []TextItem{
		tableText("Year", 14, 190, 28), tableText("Value", 120, 190, 35),
		tableText("2025", 14, 170, 25), tableText("10", 120, 170, 15),
		tableText("Name", 14, 90, 30), tableText("Score", 120, 90, 35),
		tableText("Ada", 14, 70, 20), tableText("99", 120, 70, 15),
	}
	lines := []Line{
		horizontalRule(0, 240, 200), horizontalRule(0, 240, 180), horizontalRule(0, 240, 160),
		horizontalRule(0, 240, 100), horizontalRule(0, 240, 80), horizontalRule(0, 240, 60),
	}
	tables := detectPageTables(items, nil, lines, 1)
	if len(tables) != 2 {
		t.Fatalf("detected %d tables, want two independent sparse tables", len(tables))
	}
	if got := tableToMarkdown(tables[0]); !strings.Contains(got, "|2025|10|") {
		t.Fatalf("first table = %q", got)
	}
	if got := tableToMarkdown(tables[1]); !strings.Contains(got, "|Ada|99|") {
		t.Fatalf("second table = %q", got)
	}
}

func TestBorderlessWrappedFirstCellStaysInLogicalRow(t *testing.T) {
	items := []TextItem{
		tableText("Stage", 10, 100, 35), tableText("Name", 110, 100, 30), tableText("Detail", 210, 100, 35), tableText("Value", 310, 100, 35),
		tableText("First part", 10, 80, 45), tableText("A", 110, 80, 10), tableText("top", 210, 80, 20), tableText("1", 310, 80, 10),
		tableText("continued", 10, 69, 40), tableText("wrapped", 210, 69, 35),
		tableText("Second", 10, 45, 40), tableText("B", 110, 45, 10), tableText("bottom", 210, 45, 35), tableText("2", 310, 45, 10),
	}
	for i := range items {
		items[i].FontSize = 6
	}
	tables := detectPageTables(items, nil, nil, 1)
	if len(tables) != 1 {
		t.Fatalf("detected %d tables", len(tables))
	}
	if len(tables[0].cells) != 3 {
		t.Fatalf("logical rows = %d, want 3", len(tables[0].cells))
	}
	if got := tables[0].cells[1][0].text; got != "First part continued" {
		t.Fatalf("wrapped first cell = %q", got)
	}
}

func TestAnalyseLayoutUsesReconstructedTablesAndProseGutters(t *testing.T) {
	items := []TextItem{
		tableText("Year", 10, 100, 30), tableText("Value", 110, 100, 35),
		tableText("2024", 10, 80, 25), tableText("10", 110, 80, 12),
		tableText("2025", 10, 60, 25), tableText("12", 110, 60, 12),
		tableText("2026", 10, 40, 25), tableText("14", 110, 40, 12),
	}
	page := pageExtraction{page: 1, items: items}
	layout := analyseLayout([]pageExtraction{page})
	if !layout.IsComplex || len(layout.PagesWithTables) != 1 || layout.PagesWithTables[0] != 1 {
		t.Fatalf("layout = %#v", layout)
	}
	if len(layout.PagesWithColumns) != 0 {
		t.Fatalf("table columns reported as prose columns: %#v", layout)
	}
}

func TestNestedGridAttachesToOwningCell(t *testing.T) {
	items := []TextItem{
		tableText("Outer", 20, 320, 40),
		tableText("A", 230, 350, 10), tableText("B", 315, 350, 10),
		tableText("C", 230, 265, 10), tableText("D", 315, 265, 10),
		tableText("Bottom left", 20, 100, 60), tableText("Bottom right", 220, 100, 70),
	}
	lines := []Line{
		horizontalRule(0, 400, 400), horizontalRule(0, 400, 200), horizontalRule(0, 400, 0),
		verticalRule(0, 0, 400), verticalRule(200, 0, 400), verticalRule(400, 0, 400),
		// A disconnected 2x2 grid inside the outer table's upper-right cell.
		horizontalRule(220, 380, 380), horizontalRule(220, 380, 300), horizontalRule(220, 380, 220),
		verticalRule(220, 220, 380), verticalRule(300, 220, 380), verticalRule(380, 220, 380),
	}

	tables := detectPageTables(items, nil, lines, 1)
	if len(tables) != 1 {
		t.Fatalf("top-level tables = %d, want 1", len(tables))
	}
	cell := tables[0].cells[0][1]
	if len(cell.nested) != 1 {
		t.Fatalf("nested tables = %d, cell=%#v", len(cell.nested), cell)
	}
	md := tableToMarkdown(tables[0])
	if !strings.HasPrefix(md, "<table>") || !strings.Contains(md, "<td>D</td>") {
		t.Fatalf("nested markdown = %q", md)
	}
}

func TestNestedGridUsesImmediateParentAtEveryDepth(t *testing.T) {
	items := []TextItem{
		tableText("outer", 20, 420, 35), tableText("outer right", 270, 420, 55),
		tableText("outer bottom", 20, 80, 60), tableText("outer bottom right", 270, 80, 80),
		tableText("middle", 280, 360, 35), tableText("middle right", 380, 385, 55),
		tableText("middle bottom", 280, 250, 60), tableText("middle bottom right", 390, 250, 75),
		tableText("inner A", 400, 365, 35), tableText("inner B", 445, 365, 35),
		tableText("inner C", 400, 330, 35), tableText("inner D", 445, 330, 35),
	}
	lines := []Line{
		horizontalRule(0, 500, 500), horizontalRule(0, 500, 200), horizontalRule(0, 500, 0),
		verticalRule(0, 0, 500), verticalRule(250, 0, 500), verticalRule(500, 0, 500),
		horizontalRule(270, 480, 390), horizontalRule(270, 480, 300), horizontalRule(270, 480, 220),
		verticalRule(270, 220, 390), verticalRule(375, 220, 390), verticalRule(480, 220, 390),
		horizontalRule(390, 470, 380), horizontalRule(390, 470, 345), horizontalRule(390, 470, 310),
		verticalRule(390, 310, 380), verticalRule(430, 310, 380), verticalRule(470, 310, 380),
	}
	tables := detectPageTables(items, nil, lines, 1)
	if len(tables) != 1 {
		t.Fatalf("top-level tables = %d", len(tables))
	}
	outerNested := tables[0].cells[0][1].nested
	if len(outerNested) != 1 {
		t.Fatalf("outer nested tables = %d, want only the immediate child", len(outerNested))
	}
	middleNested := outerNested[0].cells[0][1].nested
	if len(middleNested) != 1 || middleNested[0].cells[1][1].text != "inner D" {
		t.Fatalf("middle nested hierarchy = %#v", middleNested)
	}
}

func TestAdvancedReadingOrderUsesColumnFlowAroundSpanningLines(t *testing.T) {
	items := []TextItem{
		tableText("Heading across page", 0, 700, 300),
		tableText("tail", 305, 700, 25),
		tableText("Left one has prose", 0, 650, 120),
		tableText("Right one has prose", 180, 650, 120),
		tableText("Left two has prose", 0, 620, 120),
		tableText("Right two has prose", 180, 620, 120),
		tableText("Footer across page", 0, 560, 300),
	}
	lines := layoutPageLines(items, nil, DefaultAdaptiveThreshold)
	var got []string
	for _, line := range lines {
		got = append(got, line.Text())
	}
	want := []string{
		"Heading across page tail", "Left one has prose", "Left two has prose",
		"Right one has prose", "Right two has prose", "Footer across page",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("order = %q, want %q", got, want)
	}
}

func TestAdvancedReadingOrderKeepsItemsOnZoneBoundary(t *testing.T) {
	items := []TextItem{
		tableText("Heading across page", 0, 700, 300),
		tableText("Boundary peer", 10, 697, 70),
		tableText("Left one has prose", 0, 650, 120),
		tableText("Right one has prose", 180, 650, 120),
		tableText("Left two has prose", 0, 620, 120),
		tableText("Right two has prose", 180, 620, 120),
		tableText("Footer across page", 0, 560, 300),
	}
	ordered := orderItemsByColumns(items, 0)
	if len(ordered) != len(items) {
		t.Fatalf("ordered items = %d, want %d", len(ordered), len(items))
	}
	found := false
	for _, item := range ordered {
		if item.Text == "Boundary peer" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("item exactly one line tolerance from a spanning level was dropped")
	}
}

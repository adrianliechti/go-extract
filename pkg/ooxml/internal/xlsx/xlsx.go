// Package xlsx converts SpreadsheetML (.xlsx) workbooks to Markdown, one
// table per worksheet.
package xlsx

import (
	"sort"
	"strconv"
	"strings"

	"github.com/adrianliechti/go-kernel/pkg/ooxml/internal/mdw"
	"github.com/adrianliechti/go-kernel/pkg/ooxml/internal/media"
	"github.com/adrianliechti/go-kernel/pkg/ooxml/internal/model"
	"github.com/adrianliechti/go-kernel/pkg/ooxml/internal/opc"
	"github.com/adrianliechti/go-kernel/pkg/ooxml/internal/props"
)

// maxColumns bounds how wide a rendered sheet may get. Spreadsheets can
// declare enormous sparse ranges; beyond this the table stops being useful.
const maxColumns = 512

// Convert renders a workbook as Markdown.
func Convert(pkg *opc.Package, mainPart string, opts model.Options) (*model.Document, error) {
	doc := &model.Document{Format: model.FormatXlsx}
	doc.Title = props.Title(pkg)

	shared := loadSharedStrings(pkg, mainPart)
	styles := loadStyles(pkg, mainPart)
	styles.date1904 = uses1904(pkg, mainPart)
	sheets := loadSheets(pkg, mainPart, opts.IncludeHidden)
	images := media.NewCollector(pkg, opts)
	w := mdw.New()

	for _, sh := range sheets {
		doc.SheetNames = append(doc.SheetNames, sh.Name)

		rows := readSheet(pkg, sh.Part, shared, styles)
		// The sheet name is document content, not decoration: it often carries
		// the only description of what the table holds. It is emitted even for
		// a single-sheet workbook.
		w.Heading(2, sh.Name)
		if len(rows) == 0 {
			continue
		}
		w.Table(rows)

		// Images anchored to this sheet follow its table.
		if !opts.SkipImages {
			for _, name := range sheetImages(pkg, sh.Part, images) {
				w.Image("image", images.Link(name))
			}
		}
	}

	doc.Markdown = w.String()
	doc.Images = images.Images()
	return doc, nil
}

// ── workbook ─────────────────────────────────────────────────────────

type sheetRef struct {
	Name string
	Part string
}

type xmlWorkbook struct {
	Pr struct {
		Date1904 string `xml:"date1904,attr"`
	} `xml:"workbookPr"`
	Sheets []struct {
		Name    string `xml:"name,attr"`
		SheetID string `xml:"sheetId,attr"`
		ID      string `xml:"id,attr"`
		State   string `xml:"state,attr"`
	} `xml:"sheets>sheet"`
}

// loadSheets returns the selected sheets in workbook order, which is the order
// users see in Excel. Hidden sheets are omitted unless explicitly requested.
func loadSheets(pkg *opc.Package, mainPart string, includeHidden bool) []sheetRef {
	var wb xmlWorkbook
	if err := pkg.UnmarshalPart(mainPart, &wb); err != nil {
		return nil
	}
	rels := pkg.Rels(mainPart)

	var out []sheetRef
	for _, s := range wb.Sheets {
		// Hidden and very-hidden sheets are not part of the visible document.
		if !includeHidden && (s.State == "hidden" || s.State == "veryHidden") {
			continue
		}
		rel, ok := rels[s.ID]
		if !ok {
			continue
		}
		part := rel.Resolve()
		if !pkg.Has(part) {
			continue
		}
		out = append(out, sheetRef{Name: s.Name, Part: part})
	}
	return out
}

// ── shared strings ───────────────────────────────────────────────────

type xmlSST struct {
	Items []struct {
		// A shared string is either a single t, or rich-text runs each with
		// their own t. Both are concatenated.
		T    string   `xml:"t"`
		Runs []string `xml:"r>t"`
	} `xml:"si"`
}

func loadSharedStrings(pkg *opc.Package, mainPart string) []string {
	part := ""
	for _, rel := range pkg.Rels(mainPart).ByType(opc.RelSharedStrings) {
		if t := rel.Resolve(); pkg.Has(t) {
			part = t
			break
		}
	}
	if part == "" {
		if !pkg.Has("xl/sharedStrings.xml") {
			return nil
		}
		part = "xl/sharedStrings.xml"
	}

	var sst xmlSST
	if err := pkg.UnmarshalPart(part, &sst); err != nil {
		return nil
	}

	out := make([]string, len(sst.Items))
	for i, si := range sst.Items {
		if len(si.Runs) > 0 {
			out[i] = strings.Join(si.Runs, "")
		} else {
			out[i] = si.T
		}
	}
	return out
}

// ── worksheet ────────────────────────────────────────────────────────

type xmlSheet struct {
	Rows []struct {
		R     string `xml:"r,attr"`
		Cells []struct {
			R string `xml:"r,attr"`
			T string `xml:"t,attr"`
			S string `xml:"s,attr"`
			V string `xml:"v"`
			// IS is an inline string, used when shared strings are disabled.
			IS struct {
				T    string   `xml:"t"`
				Runs []string `xml:"r>t"`
			} `xml:"is"`
			F string `xml:"f"`
		} `xml:"c"`
	} `xml:"sheetData>row"`
	Merges []struct {
		Ref string `xml:"ref,attr"`
	} `xml:"mergeCells>mergeCell"`
	Hyperlinks []xmlHyperlink `xml:"hyperlinks>hyperlink"`
}

type xmlHyperlink struct {
	Ref      string `xml:"ref,attr"`
	ID       string `xml:"id,attr"`
	Location string `xml:"location,attr"`
	Display  string `xml:"display,attr"`
}

type sheetHyperlink struct {
	target  string
	display string
}

// readSheet renders a worksheet into a dense rectangular grid, filling gaps
// left by sparse cell references.
func readSheet(pkg *opc.Package, part string, shared []string, styles *cellStyles) [][]string {
	var sh xmlSheet
	if err := pkg.UnmarshalPart(part, &sh); err != nil {
		return nil
	}

	type placed struct {
		col int
		val string
	}
	type placedRow struct {
		row   int
		cells []placed
	}
	var grid []placedRow
	maxCol := 0
	links := resolveHyperlinks(pkg.Rels(part), sh.Hyperlinks)
	nextRow := 1

	for _, row := range sh.Rows {
		rowNum := nextRow
		if n, err := strconv.Atoi(row.R); err == nil && n > 0 {
			rowNum = n
		}
		nextRow = rowNum + 1

		var cells []placed
		next := 0
		for _, c := range row.Cells {
			col := next
			if c.R != "" {
				if n, ok := columnIndex(c.R); ok {
					col = n
				}
			}
			next = col + 1

			var v string
			switch c.T {
			case "s":
				if i, err := strconv.Atoi(strings.TrimSpace(c.V)); err == nil && i >= 0 && i < len(shared) {
					v = shared[i]
				}
			case "inlineStr":
				if len(c.IS.Runs) > 0 {
					v = strings.Join(c.IS.Runs, "")
				} else {
					v = c.IS.T
				}
			case "b":
				// Booleans are stored as 0/1.
				if strings.TrimSpace(c.V) == "1" {
					v = "TRUE"
				} else if strings.TrimSpace(c.V) == "0" {
					v = "FALSE"
				}
			case "e":
				v = c.V // error value, e.g. #DIV/0!
			default:
				// Numbers and cached formula results arrive as plain text.
				// The number format decides whether that text is a quantity,
				// a date or a percentage.
				v = styles.renderValue(c.S, strings.TrimSpace(c.V))
			}

			link, linked := links[cellCoord{row: rowNum, col: col + 1}]
			if v == "" && linked {
				v = link.display
			}
			if v == "" {
				continue
			}
			v = styles.format(c.S, v)
			if linked && link.target != "" {
				v = "[" + v + "](" + mdw.EscapeURL(link.target) + ")"
			}
			cells = append(cells, placed{col: col, val: v})
			if col+1 > maxCol {
				maxCol = col + 1
			}
		}
		grid = append(grid, placedRow{row: rowNum, cells: cells})
	}

	if maxCol == 0 {
		return nil
	}
	if maxCol > maxColumns {
		maxCol = maxColumns
	}

	// A sheet whose data starts away from column A would otherwise render with
	// empty leading columns, so the table spans only the populated bounding
	// box (ECMA-376 leaves the used range implicit).
	minCol := maxCol
	for _, row := range grid {
		for _, c := range row.cells {
			if c.col < minCol {
				minCol = c.col
			}
		}
	}
	if minCol >= maxCol {
		minCol = 0
	}
	width := maxCol - minCol
	if width <= 0 {
		return nil
	}

	rows := make([][]string, len(grid))
	for index, placedRow := range grid {
		row := make([]string, width)
		for _, c := range placedRow.cells {
			col := c.col - minCol
			if col < 0 || col >= width {
				continue
			}
			row[col] = c.val
		}
		rows[index] = row
	}
	if len(sh.Merges) > 0 {
		// Visit sparse coordinates in row order for the merge sweep, while
		// retaining the authored row order in the resulting Markdown.
		continuation := newMergeCoverage(sh.Merges, minCol, maxCol)
		order := make([]int, len(grid))
		for i := range order {
			order[i] = i
		}
		sort.SliceStable(order, func(i, j int) bool { return grid[order[i]].row < grid[order[j]].row })
		for _, index := range order {
			continuation.advance(grid[index].row)
			for col, count := range continuation.counts {
				if count > 0 {
					rows[index][col] = ""
				}
			}
		}
	}

	return trimEmpty(rows)
}

// resolveHyperlinks maps the first cell of each worksheet hyperlink to a
// Markdown target. ECMA-376 §18.3.1.47 stores external targets in sheet
// relationships and internal workbook locations directly on the element.
func resolveHyperlinks(rels opc.Relationships, links []xmlHyperlink) map[cellCoord]sheetHyperlink {
	if len(links) == 0 {
		return nil
	}
	out := make(map[cellCoord]sheetHyperlink, len(links))
	for _, link := range links {
		ref := strings.SplitN(link.Ref, ":", 2)[0]
		col, row, ok := parseCellRef(ref)
		if !ok {
			continue
		}

		target := ""
		if rel, ok := rels[link.ID]; ok && rel.Type == opc.RelHyperlink {
			target = rel.Resolve()
		}
		if link.Location != "" {
			if target == "" {
				target = "#" + link.Location
			} else {
				target += "#" + link.Location
			}
		}
		if target == "" {
			continue
		}
		out[cellCoord{row: row, col: col}] = sheetHyperlink{
			target:  target,
			display: strings.TrimSpace(link.Display),
		}
	}
	return out
}

// cellCoord is a 1-based worksheet coordinate.
type cellCoord struct{ row, col int }

// parseRange decodes a range reference such as "B2:D5" into 1-based bounds.
func parseRange(ref string) (left, top, right, bottom int, ok bool) {
	parts := strings.SplitN(ref, ":", 2)
	if len(parts) != 2 {
		return 0, 0, 0, 0, false
	}
	c1, r1, ok1 := parseCellRef(parts[0])
	c2, r2, ok2 := parseCellRef(parts[1])
	if !ok1 || !ok2 {
		return 0, 0, 0, 0, false
	}
	if c1 > c2 {
		c1, c2 = c2, c1
	}
	if r1 > r2 {
		r1, r2 = r2, r1
	}
	return c1, r1, c2, r2, true
}

// parseCellRef decodes "B12" into 1-based column and row numbers.
func parseCellRef(ref string) (col, row int, ok bool) {
	i := 0
	for i < len(ref) {
		c := ref[i]
		if c == '$' {
			ref = ref[:i] + ref[i+1:]
			continue
		}
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
			i++
			continue
		}
		break
	}
	if i == 0 || i >= len(ref) {
		return 0, 0, false
	}
	n, ok2 := columnIndex(ref[:i])
	if !ok2 {
		return 0, 0, false
	}
	r, err := strconv.Atoi(ref[i:])
	if err != nil || r < 1 {
		return 0, 0, false
	}
	return n + 1, r, true
}

// trimEmpty removes leading and trailing all-empty rows, which spreadsheets
// accumulate as formatting artefacts.
func trimEmpty(rows [][]string) [][]string {
	isEmpty := func(r []string) bool {
		for _, c := range r {
			if strings.TrimSpace(c) != "" {
				return false
			}
		}
		return true
	}
	start, end := 0, len(rows)
	for start < end && isEmpty(rows[start]) {
		start++
	}
	for end > start && isEmpty(rows[end-1]) {
		end--
	}
	if start >= end {
		return nil
	}
	rows = rows[start:end]

	// Blank interior rows are spacing, not content; dropping them keeps the
	// table compact. The header row is always kept.
	out := make([][]string, 0, len(rows))
	for i, r := range rows {
		if i > 0 && isEmpty(r) {
			continue
		}
		out = append(out, r)
	}
	return out
}

// columnIndex converts a cell reference such as "BC12" to a zero-based column
// index.
func columnIndex(ref string) (int, bool) {
	n := 0
	seen := false
	for i := 0; i < len(ref); i++ {
		ch := ref[i]
		switch {
		case ch >= 'A' && ch <= 'Z':
			n = n*26 + int(ch-'A') + 1
			seen = true
		case ch >= 'a' && ch <= 'z':
			n = n*26 + int(ch-'a') + 1
			seen = true
		default:
			// Digits begin the row part; the column is complete.
			if !seen {
				return 0, false
			}
			return n - 1, true
		}
		if n > 1<<20 {
			return 0, false
		}
	}
	if !seen {
		return 0, false
	}
	return n - 1, true
}

// sheetImages collects images anchored to a worksheet via its drawing part.
func sheetImages(pkg *opc.Package, sheetPart string, images *media.Collector) []string {
	var out []string
	for _, rel := range pkg.Rels(sheetPart) {
		if !strings.HasSuffix(rel.Type, "/drawing") {
			continue
		}
		drawingPart := rel.Resolve()
		for _, dr := range pkg.Rels(drawingPart).ByType(opc.RelImage) {
			if name, ok := images.Add(dr); ok {
				out = append(out, name)
			}
		}
	}
	return out
}

// uses1904 reports whether the workbook uses the 1904 date system, which
// shifts every date serial by four years and a day.
func uses1904(pkg *opc.Package, mainPart string) bool {
	var wb xmlWorkbook
	if err := pkg.UnmarshalPart(mainPart, &wb); err != nil {
		return false
	}
	switch strings.ToLower(wb.Pr.Date1904) {
	case "1", "true":
		return true
	}
	return false
}

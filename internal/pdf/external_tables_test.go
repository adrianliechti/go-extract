package pdf

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func externalFixturePath(parts ...string) string {
	return filepath.Join(append([]string{"testdata", "external"}, parts...)...)
}

func TestPinnedExternalTableFixtures(t *testing.T) {
	tests := []struct {
		name           string
		path           string
		contains       []string
		notContains    []string
		counts         map[string]int
		wantTablePages []uint32
	}{
		{
			name: "camelot tableception",
			path: externalFixturePath("camelot", "tableception.pdf"),
			contains: []string{
				`<th colspan="9">DISEASE OUTBREAKS OF PREVIOUS WEEKS REPORTED LATE</th>`,
				`IEC done.<table><tr><th>Name of village</th><th>HI</th><th>BI</th></tr>`,
				`<td>Islaampura</td><td>55.00</td><td>70.00</td>`,
			},
		},
		{
			name: "camelot column spans",
			path: externalFixturePath("camelot", "column_span_1.pdf"),
			contains: []string{
				`<th rowspan="2">Sl. No.</th>`,
				`<th colspan="2">Accidental Deaths</th><th colspan="2">Suicides</th>`,
			},
		},
		{
			name: "camelot row spans",
			path: externalFixturePath("camelot", "row_span_1.pdf"),
			contains: []string{
				`<td rowspan="10">GMC</td>`,
				`<td colspan="3">All Models Total Enrollments</td>`,
				`<td colspan="4">Source: Data Warehouse 12/14/15</td>`,
			},
		},
		{
			name: "markitdown sparse borderless",
			path: externalFixturePath("markitdown", "sparse_borderless_table.pdf"),
			contains: []string{
				"|Product Code|Location|Expected|Actual|Variance|Status|",
				"|SKU-4563|D-22||156||CRITICAL|\n|||180||-24||",
				"|SKU-7728|A-08|920||||\n||||935|+15|OK|",
			},
			notContains: []string{"|SKU-4563|D-22|180|156|-24|CRITICAL|"},
		},
		{
			name: "markitdown mixed booking layout",
			path: externalFixturePath("markitdown", "movie-theater-booking-2024.pdf"),
			contains: []string{
				"|Order / Rev:|2024-12-5678|Cinema:|Downtown Multiplex|",
				"|Name:|Universal Studios Distribution|",
				"#### Show Schedule Details",
			},
		},
		{
			name: "pdfplumber nonzero page boxes",
			path: externalFixturePath("pdfplumber", "issue-1181.pdf"),
			contains: []string{
				"|FooCol1|FooCol2|FooCol3|\n|---|---|---|\n|Foo4|Foo5|Foo6|\n|Foo7|Foo8|Foo9|\n|Foo10|Foo11|Foo12|",
				"|BarCol1|BarCol2|BarCol3|\n|---|---|---|\n|Bar4|Bar5|Bar6|\n|Bar7|Bar8|Bar9|\n|Bar10|Bar11|Bar12|",
			},
			counts:         map[string]int{"FooCol1": 1, "BarCol1": 1},
			wantTablePages: []uint32{1, 2},
		},
		{
			name: "pdfplumber three table ordering",
			path: externalFixturePath("pdfplumber", "issue-336-example.pdf"),
			contains: []string{
				`<th rowspan="2">公路技术 等级</th>`,
				`<th colspan="5">大型车比例 μ （ % ）</th>`,
				"|路段监控通信分中心|路段监控通信站|桥隧监控通信站|",
			},
			counts: map[string]int{"<table>": 2, "|路段监控通信分中心|": 1},
		},
		{
			name: "pdfplumber curve rules",
			path: externalFixturePath("pdfplumber", "table-curves-example.pdf"),
			contains: []string{
				`<th>System organ class</th>`,
				`<td colspan="4">Blood and lymphatic system disorders</td>`,
				`<td>Brain haemorrhage†</td><td>Not known</td><td>Uncommon</td><td>Rare</td>`,
			},
		},
		{
			name: "tabula two tables",
			path: externalFixturePath("tabula", "twotables.pdf"),
			contains: []string{
				`<th colspan="5">株主資本</th>`,
				`<th colspan="5">その他の包括利益累計額</th>`,
				`<td>当期末残高</td><td>△113</td><td>142</td><td>△104</td>`,
			},
			counts: map[string]int{"<table>": 2},
		},
		{
			name: "tabula spanning cells",
			path: externalFixturePath("tabula", "spanning_cells.pdf"),
			contains: []string{
				`<th colspan="6">Improved operation scenario</th>`,
				`<td colspan="6">Best practice scenario</td>`,
				`<th colspan="6">All alternative scenarios</th>`,
			},
			counts: map[string]int{"<table>": 2},
		},
		{
			name: "tabula sparse disclosure rows",
			path: externalFixturePath("tabula", "frx_2012_disclosure.pdf"),
			contains: []string{
				`<td>Physician</td><td>Related Entity (if applicable)</td><td>City / State</td><td>Purpose of Payment</td><td>Amount ($USD) * **</td>`,
				`<td>AARON, JOSHUA, N</td><td>REGIONAL PULMONARY &amp; SLEEP MEDICINE</td><td>WEST GROVE, PA</td><td>SPEAKING FEES</td><td>$4,700.00</td>`,
				`<td>TOTAL</td><td></td><td></td><td></td><td>$5,010.33</td>`,
			},
			notContains: []string{"AALAEI, BEHZAD AAMODT, DENISE"},
		},
		{
			name: "pymupdf4llm two compact tables",
			path: externalFixturePath("pymupdf4llm", "test_sce_150_1.pdf"),
			contains: []string{
				"|FIXED_INV_INC_FACTOR|8.39%|\n|---|---|\n|VAR_INV_INC_FACTOR|5.38%|",
				"|CHANGE_AT_MIN|-15.77%|\n|CHANGE_AT_MAX|25.16%|",
				"|Alternate Calculation with Reinsurance||||",
			},
			notContains: []string{"|FIXED_INV_INC_FACTOR||8.39%|"},
		},
		{
			name: "synthetic borderless rows",
			path: externalFixturePath("synthetic-table-bench", "edge_borders_none.pdf"),
			contains: []string{
				"|Col A|Col B|Col C|Col D|Col E|",
				"|28,319|93,246|94,098|96,186|4,092|",
				"|28,467|17,539|36,684|42,192|12,626|",
			},
			notContains: []string{"|Col A|Col B|Col C|Col D Col E||"},
		},
		{
			name: "synthetic row and column merges",
			path: externalFixturePath("synthetic-table-bench", "edge_merge_both.pdf"),
			contains: []string{
				`<td rowspan="4">Category 1</td>`,
				`<td rowspan="4">Category 2</td>`,
				`<td>52,392</td><td>63,623</td><td>74,818</td><td>7,360</td><td>8,631</td>`,
			},
		},
		{
			name: "synthetic three level header",
			path: externalFixturePath("synthetic-table-bench", "edge_header_three_level_20x9.pdf"),
			contains: []string{
				`<th colspan="3">Group 1</th><th colspan="3">Group 2</th><th colspan="3">Group 3</th>`,
				`<td>Col A</td><td>Col B</td><td>Col C</td><td>Col D</td><td>Col E</td><td>Col F</td><td>Col G</td><td>Col H</td><td>Col I</td>`,
			},
		},
		{
			name: "synthetic fifty percent sparse",
			path: externalFixturePath("synthetic-table-bench", "edge_sparse_20x8_50pct.pdf"),
			contains: []string{
				"|Col A|Col B|Col C|Col D|Col E|Col F|Col G|Col H|",
				"|52,429||29,956|19,919|4,219||82,859|4,047|",
				"|||||51,178|37,739||42,562|",
			},
		},
		{
			name: "synthetic ninety percent sparse grid",
			path: externalFixturePath("synthetic-table-bench", "edge_sparse_30x8_90pct.pdf"),
			contains: []string{
				"|Col A|Col B|Col C|Col D|Col E|Col F|Col G|Col H|",
				"|38,546||||||||",
				"||15,286||91,415|||||",
				"||||18,503|||||",
			},
		},
		{
			name: "olmocr verified relationships",
			path: externalFixturePath("olmocr-bench", "b5c5b8661b5a272e7a175cdb20d49e67ba0d_pg4.pdf"),
			contains: []string{
				"|BO|4.39|0.200|_____|_____|_____|0.569|",
				"|IM|4.11|* 0.31|** 0.77|** 0.78|_____|0.658|",
				"|H1|BRDORTINTERDVU|.236|2.069|Supported|",
				"|H4|JOBSATINTERDVU|-0.039|-0.579|Not supported|",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := ProcessFile(test.path, Options{})
			if err != nil {
				t.Fatal(err)
			}
			if !result.Layout.IsComplex || len(result.Layout.PagesWithTables) == 0 {
				t.Fatalf("table layout not reported: %#v", result.Layout)
			}
			if test.wantTablePages != nil && !reflect.DeepEqual(result.Layout.PagesWithTables, test.wantTablePages) {
				t.Fatalf("table pages = %v, want %v", result.Layout.PagesWithTables, test.wantTablePages)
			}
			for _, want := range test.contains {
				if !strings.Contains(result.Markdown, want) {
					t.Errorf("markdown missing %q", want)
				}
			}
			for _, invalid := range test.notContains {
				if strings.Contains(result.Markdown, invalid) {
					t.Errorf("markdown contains invalid output %q", invalid)
				}
			}
			for text, want := range test.counts {
				if got := strings.Count(result.Markdown, text); got != want {
					t.Errorf("count of %q = %d, want %d", text, got, want)
				}
			}
		})
	}
}

func TestPinnedExternalOCRFixturesAreRouted(t *testing.T) {
	tests := []struct {
		path      string
		wantPages []uint32
	}{
		{externalFixturePath("markitdown", "MEDRPT-2024-PAT-3847_medical_report_scan.pdf"), []uint32{1, 2, 3}},
		{externalFixturePath("pymupdf4llm", "test_sce_150_2.pdf"), []uint32{1}},
		{externalFixturePath("pymupdf4llm", "test_sce_150_3.pdf"), []uint32{1}},
	}
	for _, test := range tests {
		t.Run(filepath.Base(test.path), func(t *testing.T) {
			result, err := ProcessFile(test.path, Options{Mode: ModeDetectOnly})
			if err != nil {
				t.Fatal(err)
			}
			if result.Type != TypeScanned {
				t.Fatalf("type = %v, want %v", result.Type, TypeScanned)
			}
			if !reflect.DeepEqual(result.PagesNeedingOCR, test.wantPages) {
				t.Fatalf("OCR pages = %v, want %v", result.PagesNeedingOCR, test.wantPages)
			}
		})
	}
}

func TestPinnedExternalDocumentFixtures(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		contains    []string
		notContains []string
	}{
		{
			name:     "markitdown receipt",
			path:     externalFixturePath("markitdown", "RECEIPT-2024-TXN-98765_retail_purchase.pdf"),
			contains: []string{"TECHMART ELECTRONICS", "TOTAL $821.14", "TXN98765202411231432"},
		},
		{
			name:     "markitdown multipage repair",
			path:     externalFixturePath("markitdown", "REPAIR-2022-INV-001_multipage.pdf"),
			contains: []string{"Gabriel Diaz", "Bruce Wayne", `<td>GRAND TOTAL</td><td>211,522</td>`},
		},
		{
			name:     "markitdown prose",
			path:     externalFixturePath("markitdown", "test.pdf"),
			contains: []string{"#### 1 Introduction", "multi-agent conversations", "Customizable and conversable agents"},
		},
		{
			name:     "pymupdf4llm scientific multicolumn",
			path:     externalFixturePath("pymupdf4llm", "test_370.pdf"),
			contains: []string{"Synthesis of Silyl Dienol Ethers", "Peterson Olefination", "#### Table 1"},
		},
		{
			name:        "pymupdf4llm hostile link",
			path:        externalFixturePath("pymupdf4llm", "test_to_markdown_link_malicious.pdf"),
			contains:    []string{"Outlook: Negative", "Visit our investor relations page"},
			notContains: []string{"IMPORTANT SYSTEM UPDATE", "recommend investing", "%0x29", "https://acme.com/report"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := ProcessFile(test.path, Options{})
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range test.contains {
				if !strings.Contains(result.Markdown, want) {
					t.Errorf("markdown missing %q", want)
				}
			}
			for _, invalid := range test.notContains {
				if strings.Contains(result.Markdown, invalid) {
					t.Errorf("markdown contains unsafe or invalid text %q", invalid)
				}
			}
		})
	}
}

func TestPinnedOlmOCROracleIsComplete(t *testing.T) {
	path := externalFixturePath("olmocr-bench", "b5c5b8661b5a272e7a175cdb20d49e67ba0d_pg4.table_tests.jsonl")
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	type tableCheck struct {
		PDF     string `json:"pdf"`
		Type    string `json:"type"`
		Checked string `json:"checked"`
		Cell    string `json:"cell"`
	}
	scanner := bufio.NewScanner(f)
	count := 0
	for scanner.Scan() {
		var check tableCheck
		if err := json.Unmarshal(scanner.Bytes(), &check); err != nil {
			t.Fatalf("oracle line %d: %v", count+1, err)
		}
		if check.PDF == "" || check.Type != "table" || check.Checked != "verified" || check.Cell == "" {
			t.Fatalf("invalid oracle line %d: %#v", count+1, check)
		}
		count++
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if count != 7 {
		t.Fatalf("oracle checks = %d, want 7", count)
	}
}

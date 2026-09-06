package pdf

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrianliechti/go-extract/internal/pdf/content"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

const shiftedCipherText = `8VceZWZTReVW9VdZXReZdhZeYcVdaVTeeHVcZVd8EcVWVccVUHeT:iYZSZe-(,e2'WZ]V
;VScfRcj2&,*,*$ .'R CZdecfVehYZTYUVWZVdeYVcZXYedWY]UVcdW]X'eVcUVSeYVcVXZdecReRUR]]ed
TCBGC@ZUReVUdfSdZUZRcZVdZdWZ]VUYVcVhZeYafcdfReeVXf]ReZV0*S$.$ZZZ$6$&ViTVae
WceYVZdecfVedcVWVccVUe.'S&.'T&.'U&.'V&.'WSV]h(EfcdfReeYZdcVXf]ReZYV
cVXZdecReYVcVSjRXcVVdeWfcZdYRTajWRjdfTYZdecfVeWZ]VUYVcVhZeYeYVH:8
cVbfVde( .'S fRcRejWTVceRZZXReZTZWZT7V]]IV]VaYV8(RUHfeYhVdeVc7V]]IV]VaYV8
:iYZSZe.'TeYVaVcZUVUZX9VTVSVc-&,*$`

func qualityItem(page uint32, text string) TextItem {
	return TextItem{Page: page, Text: text, Type: ItemType{Kind: KindText}}
}

func TestAnalyzeTextQualityDetectsShiftedCipher(t *testing.T) {
	var items []TextItem
	for _, chunk := range strings.Fields(shiftedCipherText) {
		items = append(items, qualityItem(1, chunk))
	}
	items = append(items, qualityItem(2, "A clean second page should not be routed to OCR."))

	got := analyzeTextQuality(items)
	if !got.hasEncodingIssues || len(got.pages) != 1 || got.pages[0] != 1 {
		t.Fatalf("quality report = %#v, want only page 1", got)
	}
	if reasons := got.reasons[1]; len(reasons) != 1 || reasons[0] != OCRReasonSuspectedGarbledText {
		t.Fatalf("page 1 reasons = %v", reasons)
	}
}

func TestAnalyzeTextQualityDoesNotFlagCleanProse(t *testing.T) {
	prose := strings.Repeat("Certificate of Designations with respect to Series C Preferred Stock. ", 8)
	if got := analyzeTextQuality([]TextItem{qualityItem(1, prose)}); got.hasEncodingIssues {
		t.Fatalf("clean prose flagged: %#v", got)
	}
}

func TestGarbledFixturesAreRoutedToOCR(t *testing.T) {
	for _, name := range []string{"shifted_cipher_tounicode", "shinagawa_identity_h"} {
		t.Run(name, func(t *testing.T) {
			result, err := ProcessFile(
				filepath.Join("testdata", "fixtures", name+".pdf"),
				Options{Mode: ModeAnalyze},
			)
			if err != nil {
				t.Fatal(err)
			}
			if !result.HasEncodingIssues {
				t.Fatal("HasEncodingIssues is false")
			}
			if len(result.PagesNeedingOCR) != 1 || result.PagesNeedingOCR[0] != 1 {
				t.Fatalf("OCR pages = %v, want [1]", result.PagesNeedingOCR)
			}
			if len(result.OCRReasonsByPage) != 1 ||
				len(result.OCRReasonsByPage[0].Reasons) != 1 ||
				result.OCRReasonsByPage[0].Reasons[0] != OCRReasonSuspectedGarbledText {
				t.Fatalf("OCR reasons = %#v", result.OCRReasonsByPage)
			}
		})
	}
}

func TestClassifyKeepsFailedTextPageVisible(t *testing.T) {
	textPage := func(page uint32) pageExtraction {
		return pageExtraction{page: page, items: []TextItem{
			qualityItem(page, "one"), qualityItem(page, "two"), qualityItem(page, "three"),
		}}
	}
	pages := []pageExtraction{textPage(1), {page: 2, failed: true}, textPage(3)}

	typ, _, ocrPages, reasons := classify(pages)
	if typ != TypeTextBased {
		t.Fatalf("type = %v, want TextBased", typ)
	}
	if len(ocrPages) != 1 || ocrPages[0] != 2 {
		t.Fatalf("OCR pages = %v, want [2]", ocrPages)
	}
	if len(reasons) != 1 || len(reasons[0].Reasons) != 1 || reasons[0].Reasons[0] != OCRReasonNoText {
		t.Fatalf("OCR reasons = %#v", reasons)
	}
}

func TestInlineImageOperationMarksAndPositionsImage(t *testing.T) {
	e := extractor{page: 2, gs: graphicsState{ctm: matrix{20, 0, 0, 30, 5, 7}}}
	e.run(content.Operation{Operator: "INLINE_IMAGE"})
	if !e.out.hasImages || len(e.out.items) != 1 {
		t.Fatalf("inline image output = %#v", e.out)
	}
	item := e.out.items[0]
	if item.Type.Kind != KindImage || item.Page != 2 || item.X != 5 || item.Y != 7 || item.Width != 20 || item.Height != 30 {
		t.Fatalf("inline image item = %#v", item)
	}
}

func TestBeginTextPreservesRenderingMode(t *testing.T) {
	e := extractor{gs: graphicsState{text: textState{renderMode: 3}}}
	e.run(content.Operation{Operator: "BT"})
	if e.gs.text.renderMode != 3 {
		t.Fatalf("BT reset rendering mode to %d", e.gs.text.renderMode)
	}
}

func TestHorizontalScalingAffectsTextAdvance(t *testing.T) {
	f := &pageFont{widths: &fontWidths{
		widths: map[uint16]uint16{'A': 500}, unitsScale: 0.001,
	}}
	e := extractor{
		page:       1,
		fonts:      map[string]*pageFont{"F1": f},
		gs:         graphicsState{ctm: identity, text: textState{font: "F1", fontSize: 10, horizontalScale: 0.5}},
		textMatrix: identity,
		lineMatrix: identity,
		inText:     true,
	}
	e.showText([]byte("AA"))
	if got := e.textMatrix[4]; absF32(got-5) > 0.001 {
		t.Fatalf("advance = %v, want 5", got)
	}
	if got := e.out.items[0].Width; absF32(got-5) > 0.001 {
		t.Fatalf("item width = %v, want 5", got)
	}
}

func TestTJAdjustmentAdvancesWithoutResolvedFont(t *testing.T) {
	e := extractor{
		gs:         graphicsState{ctm: identity, text: textState{fontSize: 10, horizontalScale: 0.5}},
		textMatrix: identity,
		lineMatrix: identity,
		inText:     true,
	}
	e.showTextArray(content.Array{content.Integer(-200)})
	if got := e.textMatrix[4]; absF32(got-1) > 0.001 {
		t.Fatalf("advance = %v, want 1", got)
	}
}

func TestCoreFontMetricsFallback(t *testing.T) {
	fw := coreFontWidths("Courier")
	if fw == nil {
		t.Fatal("Courier core metrics unavailable")
	}
	if got := fw.stringWidth([]byte("AB"), 12, 0, 0); absF32(got-14.4) > 0.001 {
		t.Fatalf("Courier width = %v, want 14.4", got)
	}
}

func TestSimpleFontMetricsRejectInvalidValues(t *testing.T) {
	xref := &model.XRefTable{}
	invalidRange := types.Dict{
		"FirstChar": types.Integer(-1), "LastChar": types.Integer(1),
		"Widths": types.Array{types.Integer(500), types.Integer(500), types.Integer(500)},
	}
	if got := parseSimpleWidths(xref, invalidRange); got != nil {
		t.Fatalf("invalid character range produced metrics: %#v", got)
	}

	invalidWidth := types.Dict{
		"FirstChar": types.Integer(65), "LastChar": types.Integer(65),
		"Widths": types.Array{types.Integer(-1)},
	}
	got := parseSimpleWidths(xref, invalidWidth)
	if got == nil || got.widthFor(65) != 0 {
		t.Fatalf("invalid width wrapped: %#v", got)
	}
}

func TestCIDDecodeRejectsMostlyUnmappedPartialCMap(t *testing.T) {
	f := &pageFont{
		cid: true,
		toUnicode: &cmap{
			spaces: []codespace{{low: 0, high: 0xFFFF, nbytes: 2}},
			text:   map[uint32]string{1: "A"},
		},
	}
	got := f.decode([]byte{0, 1, 0x81, 2, 0x81, 3})
	if got != "���" {
		t.Fatalf("decode = %q, want one replacement per CID", got)
	}
}

func TestFormXObjectExtractsTextWithMatrix(t *testing.T) {
	xref := &model.XRefTable{}
	fontDict := types.Dict{
		"Type": types.Name("Font"), "Subtype": types.Name("Type1"), "BaseFont": types.Name("Helvetica"),
	}
	formResources := types.Dict{"Font": types.Dict{"F1": fontDict}}
	form := types.StreamDict{
		Dict: types.Dict{
			"Type": types.Name("XObject"), "Subtype": types.Name("Form"),
			"Resources": formResources,
			"Matrix":    types.Array{types.Integer(1), types.Integer(0), types.Integer(0), types.Integer(1), types.Integer(100), types.Integer(200)},
		},
		Raw: []byte("BT /F1 12 Tf 1 0 0 1 10 20 Tm (Hello) Tj ET"),
	}
	resources := types.Dict{"XObject": types.Dict{"Fm1": form}}
	e := extractor{
		xref: xref, page: 1, resources: resources,
		xobjects: buildXObjects(xref, resources), fonts: map[string]*pageFont{},
		gs:         graphicsState{ctm: identity, lineWidth: 1, text: textState{fontSize: 12, horizontalScale: 1}},
		textMatrix: identity, lineMatrix: identity,
	}
	e.doXObject("Fm1")

	if len(e.out.items) != 1 || e.out.items[0].Text != "Hello" {
		t.Fatalf("form items = %#v", e.out.items)
	}
	item := e.out.items[0]
	if absF32(item.X-110) > 0.001 || absF32(item.Y-220) > 0.001 {
		t.Fatalf("form text position = (%v,%v), want (110,220)", item.X, item.Y)
	}
}

func TestActualTextUsesSequenceStartAndOwnMCID(t *testing.T) {
	f := &pageFont{widths: &fontWidths{widths: map[uint16]uint16{'A': 500}, unitsScale: 0.001}}
	e := extractor{
		page: 1, fonts: map[string]*pageFont{"F1": f},
		gs:         graphicsState{ctm: identity, text: textState{font: "F1", fontSize: 10, horizontalScale: 1}},
		textMatrix: matrix{1, 0, 0, 1, 10, 20}, lineMatrix: identity, inText: true,
	}
	e.beginMarkedContent(content.Operation{Operator: "BDC", Operands: []content.Object{
		content.Name("Span"), content.Dict{"MCID": content.Integer(7), "ActualText": content.String("replacement")},
	}})
	e.showText([]byte("AA"))
	e.endMarkedContent()

	if len(e.out.items) != 1 {
		t.Fatalf("ActualText items = %#v", e.out.items)
	}
	item := e.out.items[0]
	if item.X != 10 || item.Y != 20 || item.MCID == nil || *item.MCID != 7 {
		t.Fatalf("ActualText item = %#v", item)
	}
}

func TestMarkTextDecorations(t *testing.T) {
	items := []TextItem{{
		Text: "underlined", X: 100, Y: 500, Width: 60, FontSize: 10, Page: 1,
		Type: ItemType{Kind: KindText},
	}}
	lines := []Line{{X1: 100, Y1: 498, X2: 160, Y2: 498, StrokeWidth: 1, Page: 1}}
	markTextDecorations(items, nil, lines, 1)
	if !items[0].IsUnderline || items[0].IsStrikeout {
		t.Fatalf("decoration flags = underline:%v strike:%v", items[0].IsUnderline, items[0].IsStrikeout)
	}
}

func TestMarkdownGroupsCodeLinesIntoFence(t *testing.T) {
	lines := []TextLine{
		{Page: 1, Y: 30, Items: []TextItem{{Text: "Heading", FontSize: 20, IsBold: true, Type: ItemType{Kind: KindText}}}},
		{Page: 1, Y: 20, Items: []TextItem{{Text: "print('hello')", FontSize: 10, Type: ItemType{Kind: KindText}}}},
		{Page: 1, Y: 10, Items: []TextItem{{Text: "print('world')", FontSize: 10, Type: ItemType{Kind: KindText}}}},
	}
	stats := calculateFontStats([]TextItem{
		{FontSize: 20}, {FontSize: 10}, {FontSize: 10},
	})
	want := "# Heading\n\n```\nprint('hello')\nprint('world')\n```\n"
	if got := toMarkdown(lines, stats, DefaultMarkdownOptions()); got != want {
		t.Fatalf("markdown:\n%q\nwant:\n%q", got, want)
	}
}

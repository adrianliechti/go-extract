package pdf

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// ProcessFile reads a PDF from disk and processes it according to opts.
func ProcessFile(path string, opts Options) (*Result, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Process(data, opts)
}

// Process processes a PDF held in memory.
func Process(data []byte, opts Options) (*Result, error) {
	start := time.Now()

	doc, err := load(data, opts.Password)
	if err != nil {
		return nil, err
	}

	res := &Result{
		PageCount: uint32(doc.xref.PageCount),
		Title:     doc.title(),
	}

	// Keep classification independent of the requested pipeline depth. Hidden
	// OCR layers can be added later as an explicit Mixed-document fallback.
	pages, err := doc.extract(opts.Pages, false)
	if err != nil {
		return nil, err
	}

	var items []TextItem
	for _, p := range pages {
		items = append(items, p.items...)
	}

	res.Type, res.Confidence, res.PagesNeedingOCR, res.OCRReasonsByPage = classify(pages)
	res.Layout = analyseLayout(pages)
	quality := analyzeTextQuality(items)
	res.HasEncodingIssues = quality.hasEncodingIssues
	mergeOCRResults(res, quality)

	if opts.Mode == ModeDetectOnly {
		res.ProcessingTime = time.Since(start)
		return res, nil
	}

	textItems := items[:0:0]
	for _, it := range items {
		if it.Type.Kind == KindText || it.Type.Kind == KindFormField {
			textItems = append(textItems, it)
		}
	}

	stats := calculateFontStats(textItems)

	if opts.Mode == ModeAnalyze {
		res.ProcessingTime = time.Since(start)
		return res, nil
	}

	mdOpts := DefaultMarkdownOptions()
	res.Markdown = toMarkdownFromPages(pages, stats, mdOpts)

	res.ProcessingTime = time.Since(start)
	return res, nil
}

// extract runs the content stream of every selected page. pages holds 1-indexed
// page numbers; an empty slice means the whole document.
func (d *document) extract(pages []uint32, includeInvisible bool) ([]pageExtraction, error) {
	selected := map[uint32]bool{}
	for _, p := range pages {
		selected[p] = true
	}

	out := make([]pageExtraction, 0, d.xref.PageCount)
	for n := 1; n <= d.xref.PageCount; n++ {
		if len(selected) > 0 && !selected[uint32(n)] {
			continue
		}
		// A page that fails to parse should not sink the whole document:
		// real-world PDFs routinely carry one corrupt page among good ones.
		pe, err := extractPage(d.xref, n, includeInvisible)
		if err != nil {
			pe.failed = true
			out = append(out, pe)
			continue
		}
		out = append(out, pe)
	}
	return out, nil
}

// mergeOCRResults folds extracted-text quality signals into detector output.
// A text-based document can still contain individual pages with broken CMaps.
func mergeOCRResults(res *Result, quality textQualityReport) {
	reasons := map[uint32][]string{}
	for _, entry := range res.OCRReasonsByPage {
		for _, reason := range entry.Reasons {
			addOCRReason(reasons, entry.Page, reason)
		}
	}
	for page, pageReasons := range quality.reasons {
		for _, reason := range pageReasons {
			addOCRReason(reasons, page, reason)
		}
	}

	pages := make([]uint32, 0, len(reasons))
	for page := range reasons {
		pages = append(pages, page)
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i] < pages[j] })

	res.PagesNeedingOCR = pages
	res.OCRReasonsByPage = make([]PageOCRReasons, 0, len(pages))
	for _, page := range pages {
		res.OCRReasonsByPage = append(res.OCRReasonsByPage, PageOCRReasons{
			Page: page, Reasons: reasons[page],
		})
	}
}

// document wraps a loaded PDF.
type document struct {
	xref *model.XRefTable
}

// load opens a PDF, mapping pdfcpu's failure modes onto this package's errors.
func load(data []byte, password string) (*document, error) {
	if !looksLikePDF(data) {
		return nil, ErrNotAPDF
	}

	conf := model.NewDefaultConfiguration()
	// Real-world PDFs frequently violate the spec in ways that do not affect
	// text extraction, so validation is deliberately permissive.
	conf.ValidationMode = model.ValidationRelaxed
	conf.UserPW = password
	conf.OwnerPW = password

	ctx, err := pdfcpu.Read(bytes.NewReader(data), conf)
	if err != nil {
		if isEncryptionError(err) {
			return nil, ErrEncrypted
		}
		return nil, fmt.Errorf("pdf: %w", err)
	}

	if err := ctx.XRefTable.EnsurePageCount(); err != nil {
		return nil, fmt.Errorf("pdf: page count: %w", err)
	}
	return &document{xref: ctx.XRefTable}, nil
}

// looksLikePDF checks for the %PDF- header. Some generators emit leading
// whitespace or a byte-order mark, so the header is searched for near the
// start rather than required at offset zero.
func looksLikePDF(data []byte) bool {
	const window = 1024
	head := data
	if len(head) > window {
		head = head[:window]
	}
	return bytes.Contains(head, []byte("%PDF-"))
}

func isEncryptionError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, s := range []string{"password", "encrypt", "decrypt", "permission"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// title returns the document title from the Info dictionary, if present.
func (d *document) title() string {
	if d.xref.Info == nil {
		return ""
	}
	info, err := d.xref.DereferenceDict(*d.xref.Info)
	if err != nil || info == nil {
		return ""
	}
	s, err := d.xref.DereferenceText(info["Title"])
	if err != nil {
		return ""
	}
	return strings.TrimSpace(s)
}

// ProcessReader processes a PDF read from r.
func ProcessReader(r io.Reader, opts Options) (*Result, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return Process(data, opts)
}

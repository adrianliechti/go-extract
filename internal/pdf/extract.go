package pdf

import (
	"github.com/adrianliechti/go-extract/internal/pdf/content"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// maxOperations caps content stream size. A page beyond this is pathological
// (generated vector art, usually) and extracting it would not terminate in any
// useful time.
const maxOperations = 1_000_000

// pageExtraction is everything one page yields.
type pageExtraction struct {
	// page is the 1-indexed page number. It is recorded explicitly because a
	// page may yield no items, rects or lines at all, and the classifier still
	// has to name it when routing the page to OCR.
	page  uint32
	items []TextItem
	rects []Rect
	lines []Line
	// hasImages records whether the page invoked any image XObject, which the
	// classifier needs in order to tell a scanned page from an empty one.
	hasImages bool
	// operations is the content stream operator count, used to distinguish a
	// genuinely blank page from one this package failed to read.
	operations int
	// failed keeps a page in classification and OCR routing when its content
	// stream could not be read.
	failed bool
}

// textState is the PDF text state, saved and restored alongside the graphics
// state by q and Q.
type textState struct {
	font            string
	fontSize        float32
	leading         float32
	charSpacing     float32
	wordSpacing     float32
	horizontalScale float32
	rise            float32
	renderMode      int
}

// graphicsState is the subset of the graphics state this extractor tracks.
type graphicsState struct {
	ctm       matrix
	lineWidth float32
	text      textState
}

// markedContent is one level of the BDC/BMC nesting stack.
type markedContent struct {
	actualText string
	hasActual  bool
	mcid       *int64
	hasMCID    bool
	start      matrix
	font       string
	fontSize   float32
	pageFont   *pageFont
}

// extractor holds the state machine for one page.
type extractor struct {
	xref      *model.XRefTable
	page      uint32
	fonts     map[string]*pageFont
	resources types.Dict
	// xobjects keeps the resource object as well as its subtype so Form
	// XObjects can execute their own content streams and nested resources.
	xobjects  map[string]xObjectResource
	formDepth int
	// includeInvisible extracts Tr 3 text, which carries the OCR layer of a
	// scanned page.
	includeInvisible bool

	gs    graphicsState
	stack []graphicsState

	textMatrix matrix
	lineMatrix matrix
	inText     bool

	marked []markedContent
	// suppressGlyphs is set inside a BDC whose ActualText replaces the glyphs.
	suppressGlyphs bool

	// Direction votes, one per show-text operator. Text whose combined matrix
	// has a dominant x-component runs horizontally; otherwise the page sets
	// its text rotated and the coordinates need swapping afterwards.
	votesHorizontal int
	votesRotated    int

	out pageExtraction

	// Path construction state, accumulated by m/l/re and flushed by the paint
	// operators. Only painted geometry is recorded: a `re W n` clip path draws
	// nothing, and treating it as ink would underline text that merely sits
	// near an invisible clip boundary.
	pathStart   *point
	pathCurrent *point
	pendingSegs []Line
	pendingRe   []Rect
}

type point struct{ x, y float32 }

type xObjectResource struct {
	subtype string
	object  types.Object
}

const maxFormXObjectDepth = 5

// extractPage runs the content stream of one page and returns its text items
// and painted geometry.
func extractPage(xref *model.XRefTable, pageNr int, includeInvisible bool) (pageExtraction, error) {
	empty := pageExtraction{page: uint32(pageNr)}

	d, _, attrs, err := xref.PageDict(pageNr, false)
	if err != nil {
		return empty, err
	}
	if d == nil {
		return empty, nil
	}

	var resources types.Dict
	if attrs != nil {
		resources = attrs.Resources
	}

	data, err := xref.PageContent(d, pageNr)
	if err != nil {
		// A page with no content stream is legal and simply has no text.
		if err == model.ErrNoContent {
			return empty, nil
		}
		return empty, err
	}

	// The lexer is inline-image aware and skips comments only between tokens.
	// A byte-level comment pre-pass would corrupt arbitrary binary image data
	// containing '%'.
	ops, err := content.Decode(data)
	if err != nil && len(ops) == 0 {
		return empty, err
	}
	// A page beyond this operator count is pathological generated vector art;
	// extracting it would not terminate in any useful time.
	if len(ops) > maxOperations {
		return empty, nil
	}

	e := &extractor{
		xref:             xref,
		page:             uint32(pageNr),
		fonts:            buildPageFonts(xref, resources),
		resources:        resources,
		xobjects:         buildXObjects(xref, resources),
		includeInvisible: includeInvisible,
		gs: graphicsState{ctm: identity, lineWidth: 1, text: textState{
			fontSize: 12, horizontalScale: 1,
		}},
		textMatrix: identity,
		lineMatrix: identity,
	}
	e.out.page = uint32(pageNr)
	e.out.operations = len(ops)

	for _, op := range ops {
		e.run(op)
	}

	clipAndNormalizePage(&e.out, attrs)
	e.correctRotation()
	markTextDecorations(e.out.items, e.out.rects, e.out.lines, e.page)
	e.out.items = mergeTextItems(e.out.items)
	return e.out, nil
}

// clipAndNormalizePage drops content outside the effective page boundary and
// translates the visible page to a zero-based coordinate system. PDF content
// streams are not implicitly clipped to their MediaBox: a file may reuse one
// stream for several pages and expose a different region on each page. Keeping
// off-page objects duplicates text and tables, while leaving a non-zero lower
// left corner shifts every downstream layout calculation.
func clipAndNormalizePage(page *pageExtraction, attrs *model.InheritedPageAttrs) {
	if attrs == nil {
		return
	}
	box := attrs.CropBox
	if box == nil {
		box = attrs.MediaBox
	}
	if box == nil {
		return
	}

	llx, lly := float32(box.LL.X), float32(box.LL.Y)
	urx, ury := float32(box.UR.X), float32(box.UR.Y)
	if urx <= llx || ury <= lly {
		return
	}

	items := page.items[:0]
	for _, item := range page.items {
		x1, x2 := item.X, item.X+item.Width
		y1, y2 := item.Y, item.Y+item.Height
		if x2 < x1 {
			x1, x2 = x2, x1
		}
		if y2 < y1 {
			y1, y2 = y2, y1
		}
		// Keep partially visible objects. A long text run or image may cross a
		// page boundary while still contributing visible content inside it.
		if x2 < llx || x1 > urx || y2 < lly || y1 > ury {
			continue
		}
		item.X -= llx
		item.Y -= lly
		items = append(items, item)
	}
	page.items = items

	rects := page.rects[:0]
	for _, rect := range page.rects {
		x1 := maxF32(rect.X, llx)
		y1 := maxF32(rect.Y, lly)
		x2 := minF32(rect.X+rect.Width, urx)
		y2 := minF32(rect.Y+rect.Height, ury)
		if x2 <= x1 || y2 <= y1 {
			continue
		}
		rect.X, rect.Y = x1-llx, y1-lly
		rect.Width, rect.Height = x2-x1, y2-y1
		rects = append(rects, rect)
	}
	page.rects = rects

	lines := page.lines[:0]
	for _, line := range page.lines {
		if !clipLineToBox(&line, llx, lly, urx, ury) {
			continue
		}
		line.X1, line.X2 = line.X1-llx, line.X2-llx
		line.Y1, line.Y2 = line.Y1-lly, line.Y2-lly
		lines = append(lines, line)
	}
	page.lines = lines
}

// clipLineToBox applies Liang-Barsky clipping and updates line in place.
func clipLineToBox(line *Line, llx, lly, urx, ury float32) bool {
	dx, dy := line.X2-line.X1, line.Y2-line.Y1
	t0, t1 := float32(0), float32(1)
	ps := [4]float32{-dx, dx, -dy, dy}
	qs := [4]float32{line.X1 - llx, urx - line.X1, line.Y1 - lly, ury - line.Y1}
	for i, p := range ps {
		q := qs[i]
		if absF32(p) < 0.0001 {
			if q < 0 {
				return false
			}
			continue
		}
		r := q / p
		if p < 0 {
			if r > t1 {
				return false
			}
			if r > t0 {
				t0 = r
			}
		} else {
			if r < t0 {
				return false
			}
			if r < t1 {
				t1 = r
			}
		}
	}
	x1, y1 := line.X1, line.Y1
	line.X1, line.Y1 = x1+t0*dx, y1+t0*dy
	line.X2, line.Y2 = x1+t1*dx, y1+t1*dy
	return true
}

// buildXObjects maps resource names to the streams invoked by Do.
func buildXObjects(xref *model.XRefTable, resources types.Dict) map[string]xObjectResource {
	out := map[string]xObjectResource{}
	xo := dictOf(xref, resources["XObject"])
	for name, ref := range xo {
		if d := dictOf(xref, ref); d != nil {
			out[name] = xObjectResource{subtype: nameOf(xref, d["Subtype"]), object: ref}
		}
	}
	return out
}

// run dispatches one content stream operation.
func (e *extractor) run(op content.Operation) {
	switch op.Operator {
	case "q":
		e.stack = append(e.stack, e.gs)
	case "Q":
		if n := len(e.stack); n > 0 {
			e.gs = e.stack[n-1]
			e.stack = e.stack[:n-1]
		}
	case "cm":
		if m, ok := matrixOperands(op.Operands); ok {
			e.gs.ctm = m.mul(e.gs.ctm)
		}
	case "w":
		if v, ok := numAt(op.Operands, 0); ok {
			e.gs.lineWidth = v
		}

	case "BT":
		e.inText = true
		e.textMatrix = identity
		e.lineMatrix = identity
	case "ET":
		e.inText = false

	case "Tf":
		if len(op.Operands) >= 2 {
			if n, ok := op.Operands[0].(content.Name); ok {
				e.gs.text.font = string(n)
			}
			if v, ok := numAt(op.Operands, 1); ok {
				e.gs.text.fontSize = v
			}
		}
	case "TL":
		if v, ok := numAt(op.Operands, 0); ok {
			e.gs.text.leading = v
		}
	case "Tr":
		if v, ok := numAt(op.Operands, 0); ok {
			e.gs.text.renderMode = int(v)
		}
	case "Tc":
		if v, ok := numAt(op.Operands, 0); ok {
			e.gs.text.charSpacing = v
		}
	case "Tw":
		if v, ok := numAt(op.Operands, 0); ok {
			e.gs.text.wordSpacing = v
		}
	case "Tz":
		if v, ok := numAt(op.Operands, 0); ok {
			e.gs.text.horizontalScale = v / 100
		}
	case "Ts":
		if v, ok := numAt(op.Operands, 0); ok {
			e.gs.text.rise = v
		}

	case "Td", "TD":
		tx, okX := numAt(op.Operands, 0)
		ty, okY := numAt(op.Operands, 1)
		if okX && okY {
			e.nextLineAt(tx, ty)
			if op.Operator == "TD" {
				e.gs.text.leading = -ty
			}
		}
	case "Tm":
		if m, ok := matrixOperands(op.Operands); ok {
			e.textMatrix = m
			e.lineMatrix = m
		}
	case "T*":
		e.nextLine()

	case "Tj":
		if e.inText && len(op.Operands) > 0 {
			e.showText(bytesOperand(op.Operands[0]))
		}
	case "TJ":
		if e.inText && len(op.Operands) > 0 {
			if arr, ok := op.Operands[0].(content.Array); ok {
				e.showTextArray(arr)
			}
		}
	case "'":
		if !e.inText {
			return
		}
		e.nextLine()
		if len(op.Operands) > 0 {
			e.showText(bytesOperand(op.Operands[0]))
		}
	case "\"":
		if !e.inText {
			return
		}
		// aw ac string " — set word and char spacing, then show on a new line.
		if v, ok := numAt(op.Operands, 0); ok {
			e.gs.text.wordSpacing = v
		}
		if v, ok := numAt(op.Operands, 1); ok {
			e.gs.text.charSpacing = v
		}
		e.nextLine()
		if len(op.Operands) >= 3 {
			e.showText(bytesOperand(op.Operands[2]))
		}

	case "Do":
		if len(op.Operands) > 0 {
			if n, ok := op.Operands[0].(content.Name); ok {
				e.doXObject(string(n))
			}
		}
	case "INLINE_IMAGE":
		// An inline image is ink on the page just as an image XObject is.
		e.emitImage("inline")

	case "BDC", "BMC":
		e.beginMarkedContent(op)
	case "EMC":
		e.endMarkedContent()

	// Path construction and painting.
	case "m":
		if x, y, ok := pointOperands(op.Operands); ok {
			p := e.devicePoint(x, y)
			e.pathStart, e.pathCurrent = &p, &p
		}
	case "l":
		if x, y, ok := pointOperands(op.Operands); ok {
			p := e.devicePoint(x, y)
			if e.pathCurrent != nil {
				e.pendingSegs = append(e.pendingSegs, Line{
					X1: e.pathCurrent.x, Y1: e.pathCurrent.y,
					X2: p.x, Y2: p.y, Page: e.page, StrokeWidth: e.gs.lineWidth,
				})
			}
			e.pathCurrent = &p
		}
	case "h":
		if e.pathStart != nil && e.pathCurrent != nil {
			e.pendingSegs = append(e.pendingSegs, Line{
				X1: e.pathCurrent.x, Y1: e.pathCurrent.y,
				X2: e.pathStart.x, Y2: e.pathStart.y, Page: e.page, StrokeWidth: e.gs.lineWidth,
			})
			e.pathCurrent = e.pathStart
		}
	case "re":
		e.appendRect(op.Operands)

	case "S", "s", "f", "F", "f*", "B", "B*", "b", "b*":
		e.paintPath()
	case "n":
		e.clearPath()
	}
}

// nextLine advances to the start of the next line, using the current leading.
func (e *extractor) nextLine() {
	tl := e.gs.text.leading
	if tl == 0 {
		tl = e.gs.text.fontSize * 1.2
	}
	e.nextLineAt(0, -tl)
}

// nextLineAt applies Td: TLM = translate(tx, ty) × TLM, then Tm = TLM. The
// offsets are in text space, so they are scaled by the line matrix.
func (e *extractor) nextLineAt(tx, ty float32) {
	e.lineMatrix[4] += tx*e.lineMatrix[0] + ty*e.lineMatrix[2]
	e.lineMatrix[5] += tx*e.lineMatrix[1] + ty*e.lineMatrix[3]
	e.textMatrix = e.lineMatrix
}

// advance moves the text matrix along its own axis by a text-space width.
func (e *extractor) advance(w float32) {
	e.textMatrix[4] += w * e.textMatrix[0]
	e.textMatrix[5] += w * e.textMatrix[1]
}

// currentFont returns the font selected by the last Tf, if it resolves.
func (e *extractor) currentFont() *pageFont {
	return e.fonts[e.gs.text.font]
}

// invisible reports whether glyphs should be measured but not emitted.
func (e *extractor) invisible() bool {
	return (e.gs.text.renderMode == 3 && !e.includeInvisible) || e.suppressGlyphs
}

// showText handles Tj and the show-text half of ' and ".
func (e *extractor) showText(raw []byte) {
	if raw == nil {
		return
	}
	f := e.currentFont()

	var width float32
	if f != nil && f.widths != nil {
		width = f.widths.stringWidth(raw, e.gs.text.fontSize,
			e.gs.text.charSpacing, e.gs.text.wordSpacing)
		width *= e.gs.text.horizontalScale
	}

	if e.invisible() || f == nil {
		e.advance(width)
		return
	}

	text := expandLigatures(f.decode(raw))
	combined := e.textMatrix.riseAdjusted(e.gs.text.rise).mul(e.gs.ctm)
	scaleX := e.textMatrix[0]*e.gs.ctm[0] + e.textMatrix[1]*e.gs.ctm[2]
	e.voteDirection(combined)

	e.advance(width)

	// Whitespace still advances the matrix so gap detection sees it, but
	// carries no item of its own.
	if trimSpace(text) == "" {
		return
	}
	e.emit(text, combined, absF32(width*scaleX), f)
}

// showTextArray handles TJ, splitting the array at column-sized gaps so that
// text set across a table row does not become one run.
func (e *extractor) showTextArray(arr content.Array) {
	f := e.currentFont()
	invisible := e.invisible() || f == nil

	// The split thresholds are in the same thousandths-of-em units as the TJ
	// number operands, derived from the font's own space width where known.
	spaceThreshold := float32(120)
	if f != nil && f.widths != nil {
		spaceEm := float32(f.widths.spaceWidth) * f.widths.unitsScale
		spaceThreshold = maxF32(spaceEm*1000*0.4, 80)
	}
	columnGapThreshold := spaceThreshold * 4

	type sub struct {
		text   string
		startW float32
		endW   float32
	}
	var subs []sub
	var cur string
	var subStartW, totalW float32

	for _, el := range arr {
		if n, ok := content.Float(el); ok {
			nv := float32(n)
			displacement := -nv / 1000 * e.gs.text.fontSize * e.gs.text.horizontalScale
			switch {
			case !invisible && nv < -columnGapThreshold && cur != "":
				subs = append(subs, sub{text: cur, startW: subStartW, endW: totalW})
				cur = ""
				totalW += displacement
				subStartW = totalW
			default:
				totalW += displacement
				if !invisible && nv < -spaceThreshold && cur != "" && !endsWithSpace(cur) {
					cur += " "
				}
			}
			continue
		}

		raw := bytesOperand(el)
		if raw == nil {
			continue
		}
		if f != nil && f.widths != nil {
			totalW += f.widths.stringWidth(raw, e.gs.text.fontSize,
				e.gs.text.charSpacing, e.gs.text.wordSpacing) * e.gs.text.horizontalScale
		}
		if !invisible {
			cur += f.decode(raw)
		}
	}

	if !invisible && trimSpace(cur) != "" {
		subs = append(subs, sub{text: cur, startW: subStartW, endW: totalW})
	}

	if len(subs) > 0 {
		e.voteDirection(e.textMatrix.mul(e.gs.ctm))
		scaleX := e.textMatrix[0]*e.gs.ctm[0] + e.textMatrix[1]*e.gs.ctm[2]
		for _, s := range subs {
			offset := e.textMatrix
			offset[4] += s.startW * e.textMatrix[0]
			offset[5] += s.startW * e.textMatrix[1]
			combined := offset.riseAdjusted(e.gs.text.rise).mul(e.gs.ctm)

			var width float32
			if f != nil && f.widths != nil {
				width = absF32((s.endW - s.startW) * scaleX)
			}
			e.emit(expandLigatures(s.text), combined, width, f)
		}
	}

	// Numeric TJ adjustments are meaningful even when the font resource is
	// missing. Keeping them preserves the position of text that follows after
	// another Tf selects a usable font.
	e.advance(totalW)
}

// voteDirection records whether a show-text operator ran horizontally or
// rotated, judged by which component of its combined matrix dominates.
func (e *extractor) voteDirection(combined matrix) {
	if absF32(combined[0]) >= absF32(combined[1]) {
		e.votesHorizontal++
		return
	}
	e.votesRotated++
}

// correctRotation swaps coordinates on pages that set their text rotated.
//
// Some generators put landscape content on a portrait page with a rotated text
// matrix (Tm = [0 b -b 0 tx ty] for 90° CCW). The layout engine assumes x runs
// horizontally and y vertically, so the axes are exchanged here. Device x
// increasing is visually downward, so it is negated: the layout sorts by y
// descending, and visual-top must end up with the highest y.
func (e *extractor) correctRotation() bool {
	if len(e.out.items) < 2 {
		return false
	}
	total := e.votesHorizontal + e.votesRotated
	// Require a clear two-thirds majority before rewriting the whole page.
	if total == 0 || e.votesRotated*3 < total*2 {
		return false
	}

	for i := range e.out.items {
		it := &e.out.items[i]
		it.X, it.Y = it.Y, -it.X
		// Width along the reading direction was lost during extraction, where
		// the x scale is ~0. Estimate it from the glyph count instead: for a
		// 90° rotation the rendered font size is the horizontal extent of one em.
		if it.Width < 0.5 {
			it.Width = float32(len([]rune(it.Text))) * it.FontSize * 0.5
		}
	}

	for i := range e.out.rects {
		r := &e.out.rects[i]
		r.X, r.Y = r.Y, -(r.X + absF32(r.Width))
		r.Width, r.Height = r.Height, r.Width
	}

	for i := range e.out.lines {
		l := &e.out.lines[i]
		l.X1, l.Y1 = l.Y1, -l.X1
		l.X2, l.Y2 = l.Y2, -l.X2
	}
	return true
}

// emit records one text item at the position given by the combined matrix.
func (e *extractor) emit(text string, combined matrix, width float32, f *pageFont) {
	e.emitWithState(text, combined, width, f, e.gs.text.font, e.gs.text.fontSize, e.currentMCID())
}

func (e *extractor) emitWithState(
	text string,
	combined matrix,
	width float32,
	f *pageFont,
	fontName string,
	fontSize float32,
	mcid *int64,
) {
	if text == "" {
		return
	}
	size := effectiveFontSize(fontSize, combined)

	item := TextItem{
		Text:     text,
		X:        combined[4],
		Y:        combined[5],
		Width:    width,
		Height:   size,
		Font:     fontName,
		FontSize: size,
		Page:     e.page,
		Type:     ItemType{Kind: KindText},
		MCID:     mcid,
	}
	if f != nil {
		item.IsBold = f.isBold
		item.IsItalic = f.isItalic
		item.baseFont = f.baseFont
	}
	e.out.items = append(e.out.items, item)
}

// doXObject handles an XObject invocation. Images become positioned
// placeholders so downstream consumers can locate figures without reparsing.
func (e *extractor) doXObject(name string) {
	xo, ok := e.xobjects[name]
	if !ok {
		return
	}
	switch xo.subtype {
	case "Image":
		e.emitImage(name)
	case "Form":
		e.runFormXObject(xo.object)
	}
}

// runFormXObject executes a Form's content with its Matrix and resource scope.
// Forms are isolated graphics objects: their state and unbalanced q/Q or text
// operators must not leak back into the invoking page stream.
func (e *extractor) runFormXObject(object types.Object) {
	if e.formDepth >= maxFormXObjectDepth {
		return
	}
	d := dictOf(e.xref, object)
	data := streamBytesOf(e.xref, object)
	if d == nil || len(data) == 0 {
		return
	}
	ops, err := content.Decode(data)
	if err != nil && len(ops) == 0 {
		return
	}
	if e.out.operations+len(ops) > maxOperations {
		return
	}
	e.out.operations += len(ops)

	formMatrix := identity
	if a := arrayOf(e.xref, d["Matrix"]); len(a) >= 6 {
		for i := range 6 {
			v, ok := floatOf(e.xref, a[i])
			if !ok {
				formMatrix = identity
				break
			}
			formMatrix[i] = v
		}
	}
	resources := dictOf(e.xref, d["Resources"])
	if resources == nil {
		resources = e.resources
	}

	savedGS := e.gs
	savedStack := e.stack
	savedTextMatrix, savedLineMatrix := e.textMatrix, e.lineMatrix
	savedInText := e.inText
	savedFonts, savedResources, savedXObjects := e.fonts, e.resources, e.xobjects
	savedMarked := e.marked
	savedSuppress := e.suppressGlyphs
	savedPathStart, savedPathCurrent := e.pathStart, e.pathCurrent
	savedPendingSegs, savedPendingRects := e.pendingSegs, e.pendingRe

	e.gs.ctm = formMatrix.mul(e.gs.ctm)
	e.stack = nil
	e.textMatrix, e.lineMatrix = identity, identity
	e.inText = false
	e.fonts = buildPageFonts(e.xref, resources)
	e.resources = resources
	e.xobjects = buildXObjects(e.xref, resources)
	e.formDepth++
	e.pathStart, e.pathCurrent = nil, nil
	e.pendingSegs, e.pendingRe = nil, nil

	for _, op := range ops {
		e.run(op)
	}

	e.formDepth--
	e.gs = savedGS
	e.stack = savedStack
	e.textMatrix, e.lineMatrix = savedTextMatrix, savedLineMatrix
	e.inText = savedInText
	e.fonts, e.resources, e.xobjects = savedFonts, savedResources, savedXObjects
	e.marked, e.suppressGlyphs = savedMarked, savedSuppress
	e.pathStart, e.pathCurrent = savedPathStart, savedPathCurrent
	e.pendingSegs, e.pendingRe = savedPendingSegs, savedPendingRects
}

func (e *extractor) emitImage(name string) {
	e.out.hasImages = true

	x, y, w, h := e.gs.ctm.imageBBox()
	e.out.items = append(e.out.items, TextItem{
		Text:   "[Image: " + name + "]",
		X:      x,
		Y:      y,
		Width:  w,
		Height: h,
		Page:   e.page,
		Type:   ItemType{Kind: KindImage},
		MCID:   e.currentMCID(),
	})
}

// beginMarkedContent pushes a BDC/BMC level, capturing its MCID and any
// ActualText that replaces the enclosed glyphs.
func (e *extractor) beginMarkedContent(op content.Operation) {
	mc := markedContent{
		start:    e.textMatrix.riseAdjusted(e.gs.text.rise).mul(e.gs.ctm),
		font:     e.gs.text.font,
		fontSize: e.gs.text.fontSize,
		pageFont: e.currentFont(),
	}

	if len(op.Operands) >= 2 {
		if props, ok := op.Operands[1].(content.Dict); ok {
			if v, ok := props["MCID"]; ok {
				if id, ok := content.Int(v); ok {
					mc.mcid, mc.hasMCID = &id, true
				}
			}
			if v, ok := props["ActualText"]; ok {
				if s, ok := v.(content.String); ok {
					mc.actualText, mc.hasActual = decodeTextString(s), true
				}
			}
		}
	}

	e.marked = append(e.marked, mc)
	if mc.hasActual {
		e.suppressGlyphs = true
	}
}

// endMarkedContent pops a level, emitting any ActualText the level carried in
// place of the glyphs it suppressed.
func (e *extractor) endMarkedContent() {
	n := len(e.marked)
	if n == 0 {
		return
	}
	mc := e.marked[n-1]
	e.marked = e.marked[:n-1]

	if !mc.hasActual {
		return
	}

	e.suppressGlyphs = false
	parentHasActual := false
	for _, m := range e.marked {
		if m.hasActual {
			e.suppressGlyphs = true
			parentHasActual = true
			break
		}
	}
	// The outer replacement covers the entire nested sequence, including any
	// inner ActualText values.
	if parentHasActual {
		return
	}

	text := expandLigatures(mc.actualText)
	if trimSpace(text) == "" {
		return
	}
	mcid := mc.mcid
	if !mc.hasMCID {
		mcid = e.currentMCID()
	}
	e.emitWithState(text, mc.start, 0, mc.pageFont, mc.font, mc.fontSize, mcid)
}

// currentMCID returns the innermost marked-content id in scope.
func (e *extractor) currentMCID() *int64 {
	for i := len(e.marked) - 1; i >= 0; i-- {
		if e.marked[i].hasMCID {
			return e.marked[i].mcid
		}
	}
	return nil
}

// devicePoint maps a point from user space to device space through the CTM.
func (e *extractor) devicePoint(x, y float32) point {
	dx, dy := e.gs.ctm.apply(x, y)
	return point{dx, dy}
}

// appendRect records a `re` rectangle, transformed into device space. It is
// held pending until a paint operator confirms it is actually drawn.
func (e *extractor) appendRect(operands []content.Object) {
	if len(operands) < 4 {
		return
	}
	x, ok1 := numAt(operands, 0)
	y, ok2 := numAt(operands, 1)
	w, ok3 := numAt(operands, 2)
	h, ok4 := numAt(operands, 3)
	if !ok1 || !ok2 || !ok3 || !ok4 {
		return
	}

	// Transform all four corners so rotated and mirrored CTMs still yield an
	// upright rectangle.
	m := matrix{w, 0, 0, h, x, y}.mul(e.gs.ctm)
	rx, ry, rw, rh := m.imageBBox()
	e.pendingRe = append(e.pendingRe, Rect{X: rx, Y: ry, Width: rw, Height: rh, Page: e.page})

	p := e.devicePoint(x, y)
	e.pathStart, e.pathCurrent = &p, &p
}

// paintPath commits the pending path geometry as ink on the page.
func (e *extractor) paintPath() {
	e.out.lines = append(e.out.lines, e.pendingSegs...)
	e.out.rects = append(e.out.rects, e.pendingRe...)
	e.clearPath()
}

// clearPath discards pending geometry, as `n` does for a path used only to
// set a clip region.
func (e *extractor) clearPath() {
	e.pendingSegs = nil
	e.pendingRe = nil
	e.pathStart = nil
	e.pathCurrent = nil
}

// numAt reads operand i as a number.
func numAt(operands []content.Object, i int) (float32, bool) {
	if i >= len(operands) {
		return 0, false
	}
	v, ok := content.Float(operands[i])
	return float32(v), ok
}

// matrixOperands reads six numeric operands as a matrix.
func matrixOperands(operands []content.Object) (matrix, bool) {
	if len(operands) < 6 {
		return identity, false
	}
	var m matrix
	for i := range 6 {
		v, ok := numAt(operands, i)
		if !ok {
			return identity, false
		}
		m[i] = v
	}
	return m, true
}

// pointOperands reads two numeric operands as a point.
func pointOperands(operands []content.Object) (x, y float32, ok bool) {
	x, okX := numAt(operands, 0)
	y, okY := numAt(operands, 1)
	return x, y, okX && okY
}

// bytesOperand returns the raw bytes of a string operand.
func bytesOperand(o content.Object) []byte {
	if s, ok := o.(content.String); ok {
		return s
	}
	return nil
}

func endsWithSpace(s string) bool {
	return len(s) > 0 && s[len(s)-1] == ' '
}

package pdf

import (
	"strings"

	"github.com/adrianliechti/go-extract/internal/pdf/font"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// pageFont is a font resource prepared for decoding: everything needed to turn
// the raw bytes of a show-text operand into text, and to advance the text
// matrix by the right amount.
type pageFont struct {
	// name is the resource name the content stream selects with Tf.
	name string
	// baseFont is the /BaseFont value, used for the style heuristics.
	baseFont string

	widths *fontWidths

	// toUnicode is the font's ToUnicode CMap, the most reliable decoder.
	toUnicode *cmap
	// encoding maps a byte code to a glyph name, for simple fonts.
	encoding *[256]string
	// gidToRune resolves glyph IDs through the embedded font program, for
	// CID fonts whose codes are glyph indices and that carry no ToUnicode.
	gidToRune map[uint16]rune

	// cid marks a composite (Type0) font with multi-byte codes.
	cid bool
	// vertical marks writing mode 1.
	vertical bool

	isBold   bool
	isItalic bool
}

// buildPageFonts prepares every font in a page's resource dictionary.
func buildPageFonts(xref *model.XRefTable, resources types.Dict) map[string]*pageFont {
	out := map[string]*pageFont{}
	fonts := dictOf(xref, resources["Font"])
	for name, ref := range fonts {
		fd := dictOf(xref, ref)
		if fd == nil {
			continue
		}
		out[name] = buildPageFont(xref, name, fd)
	}
	return out
}

func buildPageFont(xref *model.XRefTable, name string, fd types.Dict) *pageFont {
	f := &pageFont{
		name:     name,
		baseFont: nameOf(xref, fd["BaseFont"]),
	}

	subtype := nameOf(xref, fd["Subtype"])
	f.cid = subtype == "Type0"

	f.buildEncoding(xref, fd, subtype)
	f.widths = parseFontWidths(xref, fd)
	if f.widths == nil {
		f.widths = coreFontWidths(f.baseFont)
	}
	f.toUnicode = buildToUnicode(xref, fd)
	f.buildStyle(xref, fd)

	return f
}

// buildEncoding resolves the font's character-code interpretation: a base
// encoding plus Differences for simple fonts, or the CMap named by /Encoding
// for composite ones.
func (f *pageFont) buildEncoding(xref *model.XRefTable, fd types.Dict, subtype string) {
	enc := deref(xref, fd["Encoding"])

	if f.cid {
		switch e := enc.(type) {
		case types.Name:
			f.vertical = isVerticalCMapName(e.Value())
		case types.StreamDict:
			if c := parseCMap(streamBytesOf(xref, fd["Encoding"])); c != nil {
				f.vertical = c.vertical
			}
		}
		// Composite fonts always use multi-byte codes; the CID-to-glyph
		// mapping only matters when a ToUnicode CMap is absent, which
		// gidToRune covers below.
		f.loadEmbeddedGIDMap(xref, descriptorOf(xref, fd))
		return
	}

	// Simple fonts: start from a base encoding, then apply Differences.
	var table [256]string
	symbolic := f.symbolic(xref, fd)

	switch e := enc.(type) {
	case types.Name:
		if base, ok := font.BaseEncoding(e.Value()); ok {
			table = *base
		} else {
			table = *font.StandardEncoding()
		}
	case types.Dict:
		if base, ok := font.BaseEncoding(nameOf(xref, e["BaseEncoding"])); ok {
			table = *base
		} else if symbolic {
			table = *font.SymbolEncoding()
		} else {
			table = *font.StandardEncoding()
		}
		applyDifferences(xref, arrayOf(xref, e["Differences"]), &table)
	default:
		if symbolic {
			table = *font.SymbolEncoding()
		} else {
			table = *font.StandardEncoding()
		}
	}

	f.encoding = &table

	// Symbolic TrueType fonts commonly ignore the encoding entirely and index
	// the embedded font's (3,0) cmap directly, so keep the glyph map to hand.
	if subtype == "TrueType" && symbolic {
		f.loadEmbeddedGIDMap(xref, descriptorOf(xref, fd))
	}
}

// symbolic reports the descriptor's symbolic flag (bit 3).
func (f *pageFont) symbolic(xref *model.XRefTable, fd types.Dict) bool {
	desc := descriptorOf(xref, fd)
	if desc == nil {
		return false
	}
	flags, _ := intOf(xref, desc["Flags"])
	return flags&(1<<2) != 0
}

// applyDifferences overlays an encoding dictionary's Differences array, which
// is a sequence of starting codes each followed by the glyph names to assign
// from that code onward.
func applyDifferences(xref *model.XRefTable, diff types.Array, table *[256]string) {
	code := 0
	for _, o := range diff {
		switch v := deref(xref, o).(type) {
		case types.Integer:
			code = int(v)
		case types.Float:
			code = int(v)
		case types.Name:
			if code >= 0 && code < 256 {
				table[code] = v.Value()
			}
			code++
		}
	}
}

// descriptorOf returns a font's descriptor, following Type0 fonts through to
// their descendant CIDFont.
func descriptorOf(xref *model.XRefTable, fd types.Dict) types.Dict {
	if d := dictOf(xref, fd["FontDescriptor"]); d != nil {
		return d
	}
	desc := arrayOf(xref, fd["DescendantFonts"])
	if len(desc) == 0 {
		return nil
	}
	cidFont := dictOf(xref, desc[0])
	if cidFont == nil {
		return nil
	}
	return dictOf(xref, cidFont["FontDescriptor"])
}

// fontFileOf returns the embedded font program from a descriptor, whichever of
// the three FontFile keys carries it.
func fontFileOf(xref *model.XRefTable, desc types.Dict) []byte {
	for _, key := range []string{"FontFile2", "FontFile3", "FontFile"} {
		if b := streamBytesOf(xref, desc[key]); len(b) > 0 {
			return b
		}
	}
	return nil
}

// loadEmbeddedGIDMap parses the embedded font program and keeps its glyph-to
// -Unicode mapping, the last resort for fonts with no usable ToUnicode CMap.
func (f *pageFont) loadEmbeddedGIDMap(xref *model.XRefTable, desc types.Dict) {
	if desc == nil {
		return
	}
	data := fontFileOf(xref, desc)
	if len(data) == 0 {
		return
	}
	face, err := font.Parse(data)
	if err != nil {
		return
	}
	if m := face.GIDToUnicode(); len(m) > 0 {
		f.gidToRune = m
	}
}

// buildToUnicode loads the font's ToUnicode CMap, if it has one.
func buildToUnicode(xref *model.XRefTable, fd types.Dict) *cmap {
	if b := streamBytesOf(xref, fd["ToUnicode"]); len(b) > 0 {
		return parseCMap(b)
	}
	return nil
}

// buildStyle determines bold and italic from the base font name, falling back
// to the descriptor flags and then to the embedded font program. Subset
// generators routinely write an upright descriptor for a genuinely italic
// face, so the embedded program is consulted whenever a flag is still unset.
func (f *pageFont) buildStyle(xref *model.XRefTable, fd types.Dict) {
	f.isBold = isBoldFont(f.baseFont)
	f.isItalic = isItalicFont(f.baseFont)

	desc := descriptorOf(xref, fd)
	if desc == nil {
		return
	}

	if angle, ok := floatOf(xref, desc["ItalicAngle"]); ok && absF32(angle) >= 4.0 {
		f.isItalic = true
	}
	if flags, ok := intOf(xref, desc["Flags"]); ok {
		if flags&(1<<6) != 0 {
			f.isItalic = true
		}
		if flags&(1<<18) != 0 {
			f.isBold = true
		}
	}

	if f.isBold && f.isItalic {
		return
	}
	if data := fontFileOf(xref, desc); len(data) > 0 {
		if face, err := font.Parse(data); err == nil {
			f.isItalic = f.isItalic || face.IsItalic() || absF32(face.ItalicAngle()) >= 4.0
			f.isBold = f.isBold || face.IsBold()
			// A bare CFF program (FontFile3) has no OS/2 table, but its Name
			// INDEX keeps the real PostScript name even when the descriptor
			// was rewritten to claim upright.
			if ps, ok := face.PostScriptName(); ok {
				f.isItalic = f.isItalic || isItalicFont(ps)
				f.isBold = f.isBold || isBoldFont(ps)
			}
		}
	}
}

// decode turns the raw bytes of a show-text operand into text.
func (f *pageFont) decode(raw []byte) string {
	var b strings.Builder
	b.Grow(len(raw))

	codes, unmapped := 0, 0
	singleByteCMap := f.cid && f.toUnicode != nil && cmapUsesOnlyWidth(f.toUnicode, 1)
	f.forEachCode(raw, func(code uint32) {
		codes++
		decoded := f.decodeCode(code)
		if decoded == "" && singleByteCMap && code >= 0x20 && code <= 0xFF {
			decoded = string(rune(code))
		}
		if decoded == "" {
			unmapped++
			return
		}
		b.WriteString(decoded)
	})
	out := b.String()

	// A composite font whose CMap resolved nothing has genuinely unmapped
	// CIDs. Emitting one replacement character per CID keeps the item alive
	// with its geometry intact, so the downstream encoding-issue check fires
	// and the page can be routed to OCR. Falling through to a byte-oriented
	// interpretation instead would read CID 0x01A9 as Latin-1 "©".
	if f.cid && hasHighByte(raw) && codes > 0 && unmapped > codes/2 {
		return strings.Repeat("�", max(codes, 1))
	}
	return out
}

func cmapUsesOnlyWidth(c *cmap, width int) bool {
	if c == nil || len(c.spaces) == 0 {
		return false
	}
	for _, space := range c.spaces {
		if space.nbytes != width {
			return false
		}
	}
	return true
}

func hasHighByte(raw []byte) bool {
	for _, b := range raw {
		if b > 0x7F {
			return true
		}
	}
	return false
}

// forEachCode splits raw bytes into character codes.
//
// Simple fonts always use single-byte codes, whatever their ToUnicode CMap
// declares: generators routinely emit a generic two-byte codespace for a
// single-byte font, and honouring it would fuse adjacent letters into one
// bogus code ("DI" becoming U+4449). Composite fonts take their code widths
// from the CMap's codespace ranges, defaulting to two bytes.
func (f *pageFont) forEachCode(raw []byte, fn func(code uint32)) {
	if !f.cid {
		for _, c := range raw {
			fn(uint32(c))
		}
		return
	}

	if f.toUnicode != nil && len(f.toUnicode.spaces) > 0 {
		f.toUnicode.decode(raw, func(code uint32, _ int) { fn(code) })
		return
	}

	for i := 0; i+1 < len(raw); i += 2 {
		fn(uint32(raw[i])<<8 | uint32(raw[i+1]))
	}
	if len(raw)%2 == 1 {
		fn(uint32(raw[len(raw)-1]))
	}
}

// decodeCode resolves one character code to text, trying each mapping in turn:
// the ToUnicode CMap, the encoding's glyph name, then the embedded font's
// glyph-to-Unicode map. Codes that resolve nowhere fall back to Latin-1 for
// simple fonts and are dropped for composite ones, where a raw CID is
// meaningless as text.
func (f *pageFont) decodeCode(code uint32) string {
	if f.toUnicode != nil {
		if s, ok := f.toUnicode.lookup(code); ok && s != "" && !strings.ContainsRune(s, 0xFFFD) {
			return s
		}
	}

	if f.encoding != nil && code < 256 {
		if name := f.encoding[code]; name != "" {
			if s, ok := font.GlyphNameToString(name); ok {
				return s
			}
		}
	}

	if f.gidToRune != nil {
		if r, ok := f.gidToRune[uint16(code)]; ok {
			return string(r)
		}
	}

	if f.cid {
		return ""
	}
	return string(rune(code))
}

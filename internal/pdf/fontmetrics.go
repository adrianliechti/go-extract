package pdf

import (
	"github.com/pdfcpu/pdfcpu/pkg/font"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// fontWidths holds the glyph metrics needed to advance the text matrix.
type fontWidths struct {
	// widths maps a character code (or CID) to its width in font units.
	widths map[uint16]uint16
	// defaultWidth applies to codes absent from widths (CID fonts' DW).
	defaultWidth uint16
	// spaceWidth is the width of code 32, measured or estimated.
	spaceWidth uint16
	// isCID marks 2-byte big-endian character codes.
	isCID bool
	// unitsScale converts font units to text space: 0.001 for the standard
	// 1000-unit grid, FontMatrix[0] for Type3 fonts.
	unitsScale float32
	// wmode is 0 for horizontal writing, 1 for vertical.
	wmode uint8
}

// stringWidth returns the advance of raw in text-space units.
//
// Per the spec the per-glyph advance is w0×Tfs + Tc, plus Tw on every space,
// so character spacing accumulates per glyph and word spacing per space byte.
func (fw *fontWidths) stringWidth(raw []byte, fontSize, charSpacing, wordSpacing float32) float32 {
	var total float32
	var numChars, numSpaces int

	if fw.isCID {
		for j := 0; j+1 < len(raw); j += 2 {
			cid := uint16(raw[j])<<8 | uint16(raw[j+1])
			total += float32(fw.widthFor(cid))
			// CID 32 is the space in most CID fonts.
			if cid == 32 {
				numSpaces++
			}
			numChars++
		}
	} else {
		for _, b := range raw {
			total += float32(fw.widthFor(uint16(b)))
			if b == 0x20 {
				numSpaces++
			}
		}
		numChars = len(raw)
	}

	return total*fw.unitsScale*fontSize +
		float32(numChars)*charSpacing +
		float32(numSpaces)*wordSpacing
}

func (fw *fontWidths) widthFor(code uint16) uint16 {
	if w, ok := fw.widths[code]; ok {
		return w
	}
	return fw.defaultWidth
}

// coreFontWidths supplies the metrics that standard Type 1 fonts are allowed
// to omit from their font dictionary. pdfcpu ships the AFM data for all 14
// core fonts; using it avoids zero-width text and a stalled text matrix.
func coreFontWidths(name string) *fontWidths {
	if !font.IsCoreFont(name) {
		return nil
	}
	widths := make(map[uint16]uint16, 256)
	for code := 0; code < 256; code++ {
		w, err := font.CharWidth(name, rune(code))
		if err != nil {
			return nil
		}
		if w < 0 {
			w = 0
		}
		widths[uint16(code)] = uint16(min(w, 0xFFFF))
	}
	return &fontWidths{
		widths:     widths,
		spaceWidth: widths[32],
		unitsScale: 0.001,
	}
}

// parseFontWidths reads glyph metrics from a font dictionary, dispatching on
// Subtype. Fonts whose subtype carries no width table return nil.
func parseFontWidths(xref *model.XRefTable, font types.Dict) *fontWidths {
	switch nameOf(xref, font["Subtype"]) {
	case "Type0":
		return parseType0Widths(xref, font)
	case "Type1", "TrueType", "MMType1", "Type3":
		return parseSimpleWidths(xref, font)
	}
	return nil
}

// parseSimpleWidths reads FirstChar/LastChar/Widths for a simple font, and
// FontMatrix for Type3 fonts whose glyph space is not the 1000-unit grid.
func parseSimpleWidths(xref *model.XRefTable, font types.Dict) *fontWidths {
	first, ok := intOf(xref, font["FirstChar"])
	if !ok {
		return nil
	}
	last, ok := intOf(xref, font["LastChar"])
	if !ok {
		return nil
	}
	if first < 0 || first > 0xFF || last < first || last > 0xFF {
		return nil
	}
	arr := arrayOf(xref, font["Widths"])
	if arr == nil {
		return nil
	}

	widths := make(map[uint16]uint16, len(arr))
	var spaceWidth uint16

	for i, o := range arr {
		code := first + int64(i)
		if code > last {
			break
		}
		w, ok := intOf(xref, o)
		if !ok || w < 0 || w > 0xFFFF {
			continue
		}
		if code == 32 {
			spaceWidth = uint16(w)
		}
		widths[uint16(code)] = uint16(w)
	}

	unitsScale := float32(0.001)
	if fm := arrayOf(xref, font["FontMatrix"]); len(fm) > 0 {
		if v, ok := floatOf(xref, fm[0]); ok {
			unitsScale = absF32(v)
		}
	}

	// Estimate the space width when the table does not carry code 32. The
	// 250 default is calibrated for the standard 1000-unit grid; Type3 fonts
	// on another grid get a fraction of their average glyph width instead.
	if spaceWidth == 0 {
		if len(widths) > 0 && absF32(unitsScale-0.001) > 0.0005 {
			var sum uint32
			for _, w := range widths {
				sum += uint32(w)
			}
			avg := float32(sum) / float32(len(widths))
			spaceWidth = uint16(maxF32(avg*0.45, 1))
		} else {
			spaceWidth = 250
		}
	}

	return &fontWidths{
		widths:     widths,
		spaceWidth: spaceWidth,
		unitsScale: unitsScale,
	}
}

// parseType0Widths reads the descendant CIDFont's W array and DW default.
func parseType0Widths(xref *model.XRefTable, font types.Dict) *fontWidths {
	desc := arrayOf(xref, font["DescendantFonts"])
	if len(desc) == 0 {
		return nil
	}
	cidFont := dictOf(xref, desc[0])
	if cidFont == nil {
		return nil
	}

	defaultWidth := uint16(1000)
	if v, ok := intOf(xref, cidFont["DW"]); ok && v >= 0 && v <= 0xFFFF {
		defaultWidth = uint16(v)
	}

	widths := map[uint16]uint16{}
	if w := arrayOf(xref, cidFont["W"]); w != nil {
		parseCIDWidths(xref, w, widths)
	}

	// CID 32 is the usual space; CID 3 is the convention in the Adobe
	// character collections. Fall back to a quarter of the default advance.
	spaceWidth, ok := widths[32]
	if !ok {
		if spaceWidth, ok = widths[3]; !ok {
			if defaultWidth > 0 {
				spaceWidth = defaultWidth / 4
			} else {
				spaceWidth = 250
			}
		}
	}

	var wmode uint8
	if v, ok := intOf(xref, font["WMode"]); ok {
		wmode = uint8(v)
	}

	return &fontWidths{
		widths:       widths,
		defaultWidth: defaultWidth,
		spaceWidth:   spaceWidth,
		isCID:        true,
		unitsScale:   0.001, // CID fonts always use the 1000-unit grid.
		wmode:        wmode,
	}
}

// parseCIDWidths expands a CIDFont W array. Entries take one of two forms:
// `c [w1 w2 …]` assigns consecutive widths from c, and `cFirst cLast w`
// assigns a uniform width across an inclusive range.
func parseCIDWidths(xref *model.XRefTable, w types.Array, out map[uint16]uint16) {
	for i := 0; i < len(w); {
		startCID, ok := intOf(xref, w[i])
		if !ok {
			i++
			continue
		}
		i++
		if i >= len(w) {
			return
		}

		if arr := arrayOf(xref, w[i]); arr != nil {
			for j, o := range arr {
				cid := startCID + int64(j)
				if cid < 0 || cid > 0xFFFF {
					continue
				}
				v, ok := intOf(xref, o)
				if !ok || v < 0 || v > 0xFFFF {
					continue
				}
				out[uint16(cid)] = uint16(v)
			}
			i++
			continue
		}

		endCID, ok := intOf(xref, w[i])
		if !ok {
			i++
			continue
		}
		i++
		if i >= len(w) {
			return
		}
		width, ok := intOf(xref, w[i])
		i++
		if !ok || startCID < 0 || endCID < startCID || endCID > 0xFFFF || width < 0 || width > 0xFFFF {
			continue
		}
		for cid := startCID; cid <= endCID; cid++ {
			out[uint16(cid)] = uint16(width)
		}
	}
}

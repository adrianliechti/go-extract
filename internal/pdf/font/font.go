// Package font reads the parts of embedded font programs that PDF text
// extraction depends on: cmap subtables (enumerated by platform/encoding),
// glyph names, and style flags.
//
// It exists because golang.org/x/image/font/sfnt cannot serve this purpose.
// That package keeps platformID/encodingID unexported, auto-selects a single
// "best" cmap subtable, and exposes only the forward rune -> GlyphIndex
// direction. Extraction needs the reverse (GID -> Unicode), needs to choose
// subtables by platform, and must handle bare CFF programs (PDF FontFile3)
// that have no sfnt wrapper at all.
package font

import (
	"encoding/binary"
	"errors"
)

// Errors returned by Parse.
var (
	ErrTooShort    = errors.New("font: data too short")
	ErrBadMagic    = errors.New("font: unrecognised font format")
	ErrBadTableDir = errors.New("font: malformed table directory")
)

// sfnt version tags.
const (
	tagTrueType = 0x00010000 // TrueType outlines
	tagTrue     = 0x74727565 // 'true' — legacy Apple TrueType
	tagOTTO     = 0x4F54544F // 'OTTO' — CFF outlines in an sfnt wrapper
	tagTTCF     = 0x74746366 // 'ttcf' — TrueType collection
)

// Face is a parsed font program.
type Face struct {
	data   []byte
	tables map[string][]byte

	// cff holds the parsed CFF, for OTTO faces and bare CFF programs.
	cff *cff

	numGlyphs uint16

	cmap     *Cmap
	cmapOnce bool

	postNames    []string
	postNamesSet bool

	italicAngle float32
	fsSelection uint16
	macStyle    uint16
	hasOS2      bool
	hasHead     bool
}

// Parse reads a font program. It accepts sfnt-wrapped TrueType and CFF
// ('OTTO'), TrueType collections (first face only), and bare CFF as found in a
// PDF FontFile3 stream.
func Parse(data []byte) (*Face, error) {
	if len(data) < 4 {
		return nil, ErrTooShort
	}

	tag := binary.BigEndian.Uint32(data)
	switch tag {
	case tagTrueType, tagTrue, tagOTTO:
		return parseSfnt(data, 0)
	case tagTTCF:
		// Collection header: tag(4) version(4) numFonts(4) offsets[numFonts].
		if len(data) < 16 {
			return nil, ErrTooShort
		}
		if binary.BigEndian.Uint32(data[8:]) == 0 {
			return nil, ErrBadTableDir
		}
		return parseSfnt(data, int(binary.BigEndian.Uint32(data[12:])))
	}

	// Bare CFF: header is major(1) minor(1) hdrSize(1) offSize(1), major == 1.
	if data[0] == 1 && data[1] == 0 {
		c, err := parseCFF(data)
		if err != nil {
			return nil, err
		}
		f := &Face{data: data, tables: map[string][]byte{}, cff: c}
		f.numGlyphs = c.numGlyphs
		f.italicAngle = c.italicAngle
		return f, nil
	}

	return nil, ErrBadMagic
}

func parseSfnt(data []byte, offset int) (*Face, error) {
	if offset < 0 || offset+12 > len(data) {
		return nil, ErrTooShort
	}
	numTables := int(binary.BigEndian.Uint16(data[offset+4:]))
	if numTables > 512 {
		return nil, ErrBadTableDir
	}

	f := &Face{data: data, tables: make(map[string][]byte, numTables)}
	for i := 0; i < numTables; i++ {
		rec := offset + 12 + i*16
		if rec+16 > len(data) {
			break // truncated directory: keep whatever parsed cleanly
		}
		name := string(data[rec : rec+4])
		off := int(binary.BigEndian.Uint32(data[rec+8:]))
		length := int(binary.BigEndian.Uint32(data[rec+12:]))
		if off < 0 || length < 0 || off > len(data) {
			continue
		}
		// Clamp rather than reject: real fonts overstate table lengths.
		if off+length > len(data) {
			length = len(data) - off
		}
		f.tables[name] = data[off : off+length]
	}

	if len(f.tables) == 0 {
		return nil, ErrBadTableDir
	}

	f.parseMaxp()
	f.parseHead()
	f.parseOS2()
	f.parsePostHeader()

	// OTTO stores outlines in a CFF table; its charset carries glyph names.
	if raw, ok := f.tables["CFF "]; ok {
		if c, err := parseCFF(raw); err == nil {
			f.cff = c
			if f.numGlyphs == 0 {
				f.numGlyphs = c.numGlyphs
			}
		}
	}

	return f, nil
}

func (f *Face) parseMaxp() {
	if t, ok := f.tables["maxp"]; ok && len(t) >= 6 {
		f.numGlyphs = binary.BigEndian.Uint16(t[4:])
	}
}

func (f *Face) parseHead() {
	if t, ok := f.tables["head"]; ok && len(t) >= 46 {
		f.macStyle = binary.BigEndian.Uint16(t[44:])
		f.hasHead = true
	}
}

func (f *Face) parseOS2() {
	if t, ok := f.tables["OS/2"]; ok && len(t) >= 64 {
		f.fsSelection = binary.BigEndian.Uint16(t[62:])
		f.hasOS2 = true
	}
}

func (f *Face) parsePostHeader() {
	// post: version(4) italicAngle(4, Fixed 16.16) ...
	if t, ok := f.tables["post"]; ok && len(t) >= 8 {
		f.italicAngle = float32(int32(binary.BigEndian.Uint32(t[4:]))) / 65536.0
	}
}

// NumGlyphs returns the glyph count.
func (f *Face) NumGlyphs() uint16 { return f.numGlyphs }

// ItalicAngle returns the post table's italic angle in degrees. It is negative
// for right-leaning (conventional) italics.
func (f *Face) ItalicAngle() float32 { return f.italicAngle }

// IsItalic reports the OS/2 fsSelection ITALIC bit, falling back to the head
// table's macStyle when OS/2 is absent.
func (f *Face) IsItalic() bool {
	if f.hasOS2 {
		return f.fsSelection&0x0001 != 0
	}
	if f.hasHead {
		return f.macStyle&0x0002 != 0
	}
	return false
}

// IsBold reports the OS/2 fsSelection BOLD bit, falling back to macStyle.
func (f *Face) IsBold() bool {
	if f.hasOS2 {
		return f.fsSelection&0x0020 != 0
	}
	if f.hasHead {
		return f.macStyle&0x0001 != 0
	}
	return false
}

// Table returns a raw sfnt table by tag.
func (f *Face) Table(tag string) ([]byte, bool) {
	t, ok := f.tables[tag]
	return t, ok
}

// PostScriptName returns the font's PostScript name. For bare CFF programs it
// comes from the Name INDEX, which retains the real name (e.g.
// "XXXXXX+Amplitude-LightItalic") even when the PDF font descriptor has been
// rewritten to claim an upright style.
func (f *Face) PostScriptName() (string, bool) {
	if f.cff != nil && f.cff.name != "" {
		return f.cff.name, true
	}
	return f.nameRecord(6)
}

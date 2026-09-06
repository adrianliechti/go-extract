package pdf

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// isBoldFont reports whether a font name indicates a bold weight.
func isBoldFont(name string) bool {
	l := strings.ToLower(name)
	switch {
	case strings.Contains(l, "bold"),
		strings.Contains(l, "-bd"),
		strings.Contains(l, "_bd"),
		strings.Contains(l, "black"),
		strings.Contains(l, "heavy"):
		return true
	}
	// Some families use Medium for semi-bold, but MediumItalic is not bold.
	if strings.Contains(l, "medium") && !strings.Contains(l, "mediumitalic") {
		return true
	}
	// URW Type 1 fonts abbreviate Medium as "Medi" (NimbusRomNo9L-Medi is the
	// Times-Bold substitute in LaTeX output; -MediItal is bold italic).
	return strings.Contains(l, "-medi") && !strings.Contains(l, "mediumital")
}

// isItalicFont reports whether a font name indicates an italic or oblique style.
func isItalicFont(name string) bool {
	l := strings.ToLower(name)
	return strings.Contains(l, "italic") ||
		strings.Contains(l, "oblique") ||
		strings.Contains(l, "-it") ||
		strings.Contains(l, "_it") ||
		strings.Contains(l, "slant") ||
		strings.Contains(l, "inclined") ||
		strings.Contains(l, "kursiv")
}

// Ligatures expanded during extraction. Fonts with custom ToUnicode maps that
// point at PUA codepoints bypass NFKC, so these are handled explicitly.
var ligatures = map[rune]string{
	'ﬀ': "ff",
	'ﬁ': "fi",
	'ﬂ': "fl",
	'ﬃ': "ffi",
	'ﬄ': "ffl",
	'ﬅ': "st",
	'ﬆ': "st",
}

// isInvisibleFormat reports codepoints that carry no glyph and would otherwise
// pollute the Markdown output.
func isInvisibleFormat(r rune) bool {
	switch r {
	case 0x00AD, // soft hyphen
		0x200B, // zero-width space
		0xFEFF, // BOM / zero-width no-break space
		0x200C, // ZWNJ
		0x200D, // ZWJ
		0x2060: // word joiner
		return true
	}
	return false
}

// expandLigatures normalises extracted text: it strips control characters and
// invisible formatting codepoints, expands Latin ligatures, folds typographic
// spaces to ASCII, and restores logical order for visually-stored Arabic.
func expandLigatures(text string) string {
	if hasControlChars(text) {
		text = stripControlChars(text)
	}

	// Arabic presentation forms signal visual-order storage. NFKC folds them
	// back to base Arabic, but is applied only in that case: a blanket NFKC
	// would also turn NBSP into a plain space and break the spacing heuristics.
	presentation := false
	for _, r := range text {
		if isArabicPresentationForm(r) {
			presentation = true
			break
		}
	}
	if presentation {
		text = norm.NFKC.String(text)
	}

	var b strings.Builder
	b.Grow(len(text))
	for _, r := range text {
		switch {
		case ligatures[r] != "":
			b.WriteString(ligatures[r])
		case isInvisibleFormat(r):
			// dropped
		case r >= 0x2000 && r <= 0x200A:
			// Fold en/em/thin/hair spaces to ASCII so the coordinate-based
			// spacing logic can see word boundaries. NBSP (U+00A0) is
			// deliberately excluded — it is common in PDFs and the existing
			// spacing heuristics already handle it.
			b.WriteByte(' ')
		default:
			b.WriteRune(r)
		}
	}

	out := b.String()
	if presentation {
		out = reverseVisualArabic(out)
	}
	return out
}

func hasControlChars(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 && c != '\n' && c != '\r' && c != '\t' {
			return true
		}
	}
	return false
}

func stripControlChars(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= ' ' || r == '\n' || r == '\r' || r == '\t' {
			return r
		}
		return -1
	}, s)
}

// isArabicPresentationForm reports whether r lies in either Arabic
// Presentation Forms block (A: U+FB50–FDFF, B: U+FE70–FEFF).
func isArabicPresentationForm(r rune) bool {
	return (r >= 0xFB50 && r <= 0xFDFF) || (r >= 0xFE70 && r <= 0xFEFF)
}

// reverseVisualArabic restores logical reading order for text stored in visual
// order. Pure RTL text is reversed wholesale; mixed content is split into LTR
// and non-LTR runs, the run order reversed, and only non-LTR runs reversed
// internally so embedded numbers and Latin words stay readable.
func reverseVisualArabic(text string) string {
	chars := []rune(text)

	hasLTR := false
	for _, r := range chars {
		if isASCIIAlnum(r) {
			hasLTR = true
			break
		}
	}
	if !hasLTR {
		return reverseRunes(chars)
	}

	type run struct {
		ltr     bool
		content []rune
	}
	var runs []run

	for i := 0; i < len(chars); {
		ltr := runIsLTR(chars, i)
		start := i
		for i < len(chars) && runIsLTR(chars, i) == ltr {
			i++
		}
		runs = append(runs, run{ltr: ltr, content: chars[start:i]})
	}

	var b strings.Builder
	b.Grow(len(text))
	for i := len(runs) - 1; i >= 0; i-- {
		if runs[i].ltr {
			b.WriteString(string(runs[i].content))
			continue
		}
		b.WriteString(reverseRunes(runs[i].content))
	}
	return b.String()
}

// runIsLTR classifies the character at idx. Punctuation counts as LTR only when
// it abuts an ASCII alphanumeric, so "12.5" stays intact but a trailing Arabic
// full stop does not get pulled into the Latin run.
func runIsLTR(chars []rune, idx int) bool {
	r := chars[idx]
	if isASCIIAlnum(r) {
		return true
	}
	return isASCIIPunct(r) && adjacentToASCIIAlnum(chars, idx)
}

func adjacentToASCIIAlnum(chars []rune, idx int) bool {
	return (idx > 0 && isASCIIAlnum(chars[idx-1])) ||
		(idx+1 < len(chars) && isASCIIAlnum(chars[idx+1]))
}

func isASCIIAlnum(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

func isASCIIPunct(r rune) bool {
	return r < unicode.MaxASCII && r > ' ' && !isASCIIAlnum(r)
}

func reverseRunes(rs []rune) string {
	out := make([]rune, len(rs))
	for i, r := range rs {
		out[len(rs)-1-i] = r
	}
	return string(out)
}

// decodeTextString decodes a PDF text string (ActualText and friends), which is
// UTF-16BE when it carries a BOM and PDFDocEncoding otherwise.
func decodeTextString(b []byte) string {
	if len(b) >= 2 && b[0] == 0xFE && b[1] == 0xFF {
		var sb strings.Builder
		for i := 2; i+1 < len(b); i += 2 {
			sb.WriteRune(rune(uint16(b[i])<<8 | uint16(b[i+1])))
		}
		return sb.String()
	}
	// PDFDocEncoding matches Latin-1 across the range that matters here.
	var sb strings.Builder
	sb.Grow(len(b))
	for _, c := range b {
		sb.WriteRune(rune(c))
	}
	return sb.String()
}

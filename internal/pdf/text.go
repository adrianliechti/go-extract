package pdf

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

func trimSpace(s string) string      { return strings.TrimFunc(s, unicode.IsSpace) }
func trimSpaceRight(s string) string { return strings.TrimRightFunc(s, unicode.IsSpace) }
func trimSpaceLeft(s string) string  { return strings.TrimLeftFunc(s, unicode.IsSpace) }

// lastRune returns the final rune of s, or ok=false when s is empty.
func lastRune(s string) (rune, bool) {
	if s == "" {
		return 0, false
	}
	r, _ := utf8.DecodeLastRuneInString(s)
	return r, true
}

// firstRune returns the leading rune of s, or ok=false when s is empty.
func firstRune(s string) (rune, bool) {
	if s == "" {
		return 0, false
	}
	r, _ := utf8.DecodeRuneInString(s)
	return r, true
}

// IsCJK reports whether r belongs to a CJK or Hangul block. CJK scripts do not
// separate words with spaces, so the Latin spacing heuristics must not apply.
func IsCJK(r rune) bool {
	switch {
	case r >= 0x1100 && r <= 0x11FF, // Hangul Jamo
		r >= 0x3000 && r <= 0x303F, // CJK Symbols and Punctuation
		r >= 0x3040 && r <= 0x309F, // Hiragana
		r >= 0x30A0 && r <= 0x30FF, // Katakana
		r >= 0x3130 && r <= 0x318F, // Hangul Compatibility Jamo
		r >= 0x4E00 && r <= 0x9FFF, // CJK Unified Ideographs
		r >= 0xAC00 && r <= 0xD7AF, // Hangul Syllables
		r >= 0xF900 && r <= 0xFAFF, // CJK Compatibility Ideographs
		r >= 0xFF00 && r <= 0xFFEF: // Halfwidth and Fullwidth Forms
		return true
	}
	return false
}

// IsRTL reports whether r belongs to a right-to-left script.
func IsRTL(r rune) bool {
	switch {
	case r >= 0x0590 && r <= 0x05FF, // Hebrew
		r >= 0x0600 && r <= 0x06FF, // Arabic
		r >= 0x0700 && r <= 0x074F, // Syriac
		r >= 0x0750 && r <= 0x077F, // Arabic Supplement
		r >= 0x0780 && r <= 0x07BF, // Thaana
		r >= 0x07C0 && r <= 0x07FF, // NKo
		r >= 0x0800 && r <= 0x083F, // Samaritan
		r >= 0x0840 && r <= 0x085F, // Mandaic
		r >= 0x08A0 && r <= 0x08FF, // Arabic Extended-A
		r >= 0xFB1D && r <= 0xFB4F, // Hebrew Presentation Forms
		r >= 0xFB50 && r <= 0xFDFF, // Arabic Presentation Forms-A
		r >= 0xFE70 && r <= 0xFEFF: // Arabic Presentation Forms-B
		return true
	}
	return false
}

// isCIDFont reports the subset-prefix convention used for CID-keyed fonts,
// which emit one word per text operator.
func isCIDFont(font string) bool {
	return strings.HasPrefix(font, "C2_") || strings.HasPrefix(font, "C0_")
}

func isAlphanumeric(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }
func isASCIIDigit(r rune) bool   { return r >= '0' && r <= '9' }

// ShouldJoinItems reports whether two adjacent text items belong to the same
// word and must be concatenated without an intervening space.
//
// singleCharThreshold is the page-level adaptive threshold; values above 0.20
// signal Canva-style letter-spacing, which needs character-width-based joining
// rather than font-size-relative gaps.
func ShouldJoinItems(prev, cur *TextItem, singleCharThreshold float32) bool {
	// Explicit leading/trailing spaces in the source text are authoritative.
	if strings.HasSuffix(prev.Text, " ") || strings.HasPrefix(cur.Text, " ") {
		return false
	}

	prevLast, prevOK := lastRune(trimSpaceRight(prev.Text))
	curFirst, curOK := firstRune(trimSpaceLeft(cur.Text))

	// Punctuation that always hugs the preceding token: "www" + ".com".
	if curOK {
		switch curFirst {
		case '.', ',', ';', '!', '?', ')', ']', '}', '\'':
			return true
		}
	}

	// "Clave:" + "T9N2I6" is a label/value pair and takes a space.
	if prevOK && curOK && prevLast == ':' && isAlphanumeric(curFirst) {
		return false
	}

	if prev.Width > 0.0 {
		return joinWithMetrics(prev, cur, prevLast, prevOK, curFirst, curOK, singleCharThreshold)
	}
	return joinWithoutMetrics(prev, cur, prevLast, prevOK, curFirst, curOK)
}

// joinWithMetrics is the accurate path, taken when font metrics gave the
// previous item a real width.
func joinWithMetrics(prev, cur *TextItem, prevLast rune, prevOK bool, curFirst rune, curOK bool, singleCharThreshold float32) bool {
	var gap float32
	if prev.X <= cur.X {
		gap = cur.X - (prev.X + prev.Width) // LTR
	} else {
		gap = prev.X - (cur.X + cur.Width) // RTL
	}
	fontSize := prev.FontSize

	// Never join across column-scale gaps or large overlaps. Large negative
	// gaps arise when Tc/Tw inflate item widths past where the next item
	// actually starts.
	if gap > fontSize*3.0 || gap < -fontSize {
		return false
	}

	prevTrimmed := trimSpace(prev.Text)
	curTrimmed := trimSpace(cur.Text)
	prevChars := utf8.RuneCountInString(prevTrimmed)
	curChars := utf8.RuneCountInString(curTrimmed)

	prevLastChar, prevLastOK := lastRune(prevTrimmed)
	curFirstChar, curFirstOK := firstRune(curTrimmed)
	isCJK := (prevLastOK && IsCJK(prevLastChar)) || (curFirstOK && IsCJK(curFirstChar))

	// CID fonts emit one word per text operator with gaps near zero, so a
	// near-zero gap there is a word boundary, not a glyph boundary. CJK is
	// excluded because it does not space words.
	if !isCJK && gap >= 0.0 && gap < fontSize*0.01 && isCIDFont(prev.Font) {
		if len(strings.Fields(prev.Text)) >= 3 {
			// A multi-word phrase came from a line-level operator, so this is
			// more likely a mid-word boundary.
			return gap < fontSize*0.15
		}
		return false
	}

	// Numeric continuity: "34,20" + "8" is one number, as is "+13." + "0".
	// Word spaces inside numbers are rare, so the threshold is generous.
	if prevOK && curOK {
		prevNumeric := isASCIIDigit(prevLast) || prevLast == ',' || prevLast == '.'
		curNumeric := isASCIIDigit(curFirst) || curFirst == '%' || curFirst == '.'
		if prevNumeric && curNumeric {
			return gap > -fontSize && gap < fontSize*0.3
		}
		if (prevLast == '+' || prevLast == '-') && isASCIIDigit(curFirst) {
			return gap > -fontSize && gap < fontSize*0.3
		}
	}

	// Canva-style letter-spacing: every gap is wide, so compare against
	// character width rather than font size.
	if singleCharThreshold > 0.20 {
		if prevChars == 1 {
			// A single glyph's rendered width is an accurate reference.
			return gap < prev.Width*1.25
		}
		if curChars == 1 {
			// Average width normalises for a wide/narrow character mix.
			return gap < (prev.Width/float32(prevChars))*1.25
		}
		return gap < fontSize*singleCharThreshold
	}

	// A single-character fragment beside a multi-character item is usually a
	// split word: "b" + "illion", "C" + "ultural".
	if (prevChars == 1) != (curChars == 1) {
		return gap < fontSize*0.20
	}

	// Both single-character: per-glyph positioning. Intra-word gaps are ~0 and
	// word boundaries ~0.15x font size. Digits get a looser threshold so
	// "100,000" survives.
	if prevChars == 1 && curChars == 1 {
		if prevOK && curOK {
			pNum := isASCIIDigit(prevLast) || prevLast == ',' || prevLast == '.' ||
				prevLast == '%' || prevLast == '+' || prevLast == '-'
			cNum := isASCIIDigit(curFirst) || curFirst == ',' || curFirst == '.' || curFirst == '%'
			if pNum && cNum {
				return gap < fontSize*0.25
			}
		}
		return gap < fontSize*singleCharThreshold
	}

	// Multi-character on both sides. A lowercase-to-lowercase junction gets a
	// slightly wider threshold to avoid splitting "enterta"+"inment" under
	// imprecise CID metrics; caps junctions keep the tighter bound so
	// "LCOE"+"WITH" stays separated.
	if prevChars >= 2 && curChars >= 2 {
		prevEndsLower := prevLastOK && unicode.IsLower(prevLastChar)
		curStartsLower := curFirstOK && unicode.IsLower(curFirstChar)
		if prevEndsLower && curStartsLower {
			return gap < fontSize*0.18
		}
	}
	return gap < fontSize*0.15
}

// joinWithoutMetrics is the fallback path, used when no font width was
// available and the previous item's extent must be estimated.
func joinWithoutMetrics(prev, cur *TextItem, prevLast rune, prevOK bool, curFirst rune, curOK bool) bool {
	charWidth := prev.FontSize * 0.45
	estimatedWidth := float32(utf8.RuneCountInString(prev.Text)) * charWidth
	gap := cur.X - (prev.X + estimatedWidth)

	if gap > charWidth*6.0 {
		return false
	}

	// CJK does not space words, so join adjacent items directly. The
	// case-based heuristics below would inject spaces inside CJK words.
	if (prevOK && IsCJK(prevLast)) || (curOK && IsCJK(curFirst)) {
		return gap < charWidth*0.8
	}

	if prevOK && curOK && unicode.IsLetter(prevLast) && unicode.IsLetter(curFirst) {
		sameCase := (unicode.IsUpper(prevLast) && unicode.IsUpper(curFirst)) ||
			(unicode.IsLower(prevLast) && unicode.IsLower(curFirst))
		switch {
		case sameCase:
			// Same case suggests a split word: "CONST" + "ANCIA".
			return gap < charWidth*0.8
		case unicode.IsLower(prevLast) && unicode.IsUpper(curFirst):
			// Words do not go lowercase to uppercase mid-word, so this is
			// always a boundary regardless of spacing.
			return false
		default:
			// Uppercase to lowercase: "REGISTRO" + "para" is a boundary.
			return gap < charWidth*0.3
		}
	}
	return gap < charWidth*0.5
}

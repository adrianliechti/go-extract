package pdf

import (
	"math"
	"sort"
	"unicode"
)

// textQualityReport is the extracted-text backstop for detector-only signals.
// Font dictionaries can look valid while their CMaps still produce replacement
// characters, CID mojibake, or printable substitution-cipher garbage.
type textQualityReport struct {
	pages             []uint32
	hasEncodingIssues bool
	reasons           map[uint32][]string
}

type pageTextQualityEvidence struct {
	chars                 int
	replacementChars      int
	replacementSpans      int
	longestReplacementRun int
	cipher                cipherGarbleStats
}

type textSpanIssueKind uint8

const (
	textSpanReplacement textSpanIssueKind = iota + 1
	textSpanStrong
)

// English letter frequencies, in percent. A substitution cipher preserves the
// shape of this distribution while moving the peaks to different letters.
var englishLetterFrequency = [26]float64{
	8.2, 1.5, 2.8, 4.3, 12.7, 2.2, 2.0, 6.1, 7.0, 0.15, 0.8, 4.0, 2.4,
	6.7, 7.5, 1.9, 0.1, 6.0, 6.3, 9.1, 2.8, 1.0, 2.4, 0.15, 2.0, 0.07,
}

type cipherGarbleStats struct {
	letterCounts     [26]uint32
	asciiLetters     int
	asciiVowels      int
	latinExtLetters  int
	nonLatinLetters  int
	letterBigrams    int
	caseShiftBigrams int
}

func (s *cipherGarbleStats) addText(text string) {
	var prev rune
	hasPrev := false
	for _, r := range text {
		if r <= unicode.MaxASCII && ((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
			lower := r
			if lower >= 'A' && lower <= 'Z' {
				lower += 'a' - 'A'
			}
			s.letterCounts[lower-'a']++
			s.asciiLetters++
			if lower == 'a' || lower == 'e' || lower == 'i' || lower == 'o' || lower == 'u' {
				s.asciiVowels++
			}
			if hasPrev {
				s.letterBigrams++
				if prev >= 'a' && prev <= 'z' && r >= 'A' && r <= 'Z' {
					s.caseShiftBigrams++
				}
			}
			prev, hasPrev = r, true
			continue
		}

		if unicode.IsLetter(r) {
			if (r >= 0x00C0 && r <= 0x024F) || (r >= 0x1E00 && r <= 0x1EFF) {
				s.latinExtLetters++
			} else {
				s.nonLatinLetters++
			}
		}
		hasPrev = false
	}
}

func (s *cipherGarbleStats) englishCosine() float64 {
	if s.asciiLetters == 0 {
		return 1
	}
	n := float64(s.asciiLetters)
	var dot, observedNorm, englishNorm float64
	for i, frequency := range englishLetterFrequency {
		p := float64(s.letterCounts[i]) / n
		dot += p * frequency
		observedNorm += p * p
		englishNorm += frequency * frequency
	}
	if observedNorm == 0 || englishNorm == 0 {
		return 1
	}
	return dot / (math.Sqrt(observedNorm) * math.Sqrt(englishNorm))
}

func (s *cipherGarbleStats) englishShapeCosine() float64 {
	if s.asciiLetters == 0 {
		return 1
	}
	n := float64(s.asciiLetters)
	observed := make([]float64, 26)
	english := make([]float64, 26)
	for i := range observed {
		observed[i] = float64(s.letterCounts[i]) / n
		english[i] = englishLetterFrequency[i]
	}
	sort.Slice(observed, func(i, j int) bool { return observed[i] > observed[j] })
	sort.Slice(english, func(i, j int) bool { return english[i] > english[j] })

	var dot, observedNorm, englishNorm float64
	for i := range observed {
		dot += observed[i] * english[i]
		observedNorm += observed[i] * observed[i]
		englishNorm += english[i] * english[i]
	}
	if observedNorm == 0 || englishNorm == 0 {
		return 1
	}
	return dot / (math.Sqrt(observedNorm) * math.Sqrt(englishNorm))
}

func (s *cipherGarbleStats) looksGarbled() bool {
	if s.asciiLetters < 200 || s.nonLatinLetters > s.asciiLetters+s.latinExtLetters {
		return false
	}
	if float64(s.asciiVowels)/float64(s.asciiLetters) > 0.30 {
		return false
	}
	caseShifts := s.letterBigrams >= 100 &&
		float64(s.caseShiftBigrams) >= float64(s.letterBigrams)*0.10
	permutedLanguage := s.englishCosine() < 0.60 && s.englishShapeCosine() >= 0.90
	return caseShifts || permutedLanguage
}

func analyzeTextQuality(items []TextItem) textQualityReport {
	evidence := map[uint32]*pageTextQualityEvidence{}
	reasons := map[uint32][]string{}

	for i := range items {
		item := &items[i]
		if item.Type.Kind != KindText {
			continue
		}
		e := evidence[item.Page]
		if e == nil {
			e = &pageTextQualityEvidence{}
			evidence[item.Page] = e
		}
		for _, r := range item.Text {
			if !unicode.IsSpace(r) {
				e.chars++
			}
		}
		e.cipher.addText(item.Text)

		switch textSpanIssue(item.Text) {
		case textSpanStrong:
			addOCRReason(reasons, item.Page, OCRReasonSuspectedGarbledText)
		case textSpanReplacement:
			replacements, longest := replacementTextStats(item.Text)
			e.replacementChars += replacements
			e.replacementSpans++
			e.longestReplacementRun = max(e.longestReplacementRun, longest)
		}
	}

	for page, e := range evidence {
		if len(reasons[page]) > 0 {
			continue
		}
		if replacementEvidenceNeedsOCR(e) || e.cipher.looksGarbled() {
			addOCRReason(reasons, page, OCRReasonSuspectedGarbledText)
		}
	}

	pages := make([]uint32, 0, len(reasons))
	for page := range reasons {
		pages = append(pages, page)
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i] < pages[j] })
	return textQualityReport{
		pages:             pages,
		hasEncodingIssues: len(pages) > 0,
		reasons:           reasons,
	}
}

func textSpanIssue(text string) textSpanIssueKind {
	text = trimSpace(text)
	if text == "" {
		return 0
	}
	if hasDollarAsSpacePattern(text) || hasPrivateUseTextRun(text) ||
		isCIDGarbage(text) || hasCIDControlToken(text) {
		return textSpanStrong
	}
	replacements, longest := replacementTextStats(text)
	if longest >= 2 || replacements >= 3 {
		return textSpanReplacement
	}
	return 0
}

func replacementTextStats(text string) (total, longest int) {
	current := 0
	for _, r := range text {
		if r == 0xFFFD {
			total++
			current++
			longest = max(longest, current)
		} else {
			current = 0
		}
	}
	return total, longest
}

func replacementEvidenceNeedsOCR(e *pageTextQualityEvidence) bool {
	if e.replacementChars == 0 || e.chars == 0 {
		return false
	}
	if e.chars <= 80 && e.longestReplacementRun >= 2 {
		return true
	}
	densityBPS := e.replacementChars * 10_000 / e.chars
	return (e.replacementChars >= 12 && densityBPS >= 500) ||
		(e.replacementSpans >= 3 && densityBPS >= 250) ||
		(e.longestReplacementRun >= 8 && densityBPS >= 250)
}

func hasDollarAsSpacePattern(text string) bool {
	bytes := []byte(text)
	total := 0
	betweenLetters := 0
	for i, b := range bytes {
		if b != '$' {
			continue
		}
		total++
		if i > 0 && i+1 < len(bytes) && isASCIIAlpha(bytes[i-1]) && isASCIIAlpha(bytes[i+1]) {
			betweenLetters++
		}
	}
	return total > 10 && (betweenLetters > 20 || betweenLetters*2 > total)
}

func isASCIIAlpha(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func hasPrivateUseTextRun(text string) bool {
	total, privateUse, current, longest := 0, 0, 0, 0
	for _, r := range text {
		if unicode.IsSpace(r) {
			current = 0
			continue
		}
		total++
		if isPrivateUseRune(r) {
			privateUse++
			current++
			longest = max(longest, current)
		} else {
			current = 0
		}
	}
	return privateUse > 0 && (longest >= 3 || (total >= 5 && privateUse >= 2 && privateUse*2 >= total))
}

func isPrivateUseRune(r rune) bool {
	return (r >= 0xE000 && r <= 0xF8FF) ||
		(r >= 0xF0000 && r <= 0xFFFFD) ||
		(r >= 0x100000 && r <= 0x10FFFD)
}

func hasCIDControlToken(text string) bool {
	start := -1
	runes := []rune(text)
	for i := 0; i <= len(runes); i++ {
		if i < len(runes) && !unicode.IsSpace(runes[i]) {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 && tokenHasCIDControl(runes[start:i]) {
			return true
		}
		start = -1
	}
	return false
}

func tokenHasCIDControl(token []rune) bool {
	controls := 0
	for _, r := range token {
		if r >= 0x80 && r <= 0x9F {
			controls++
		}
	}
	return len(token) >= 5 && controls >= 2 && controls*20 >= len(token)
}

func isCIDGarbage(text string) bool {
	if isGarbageText(text) {
		return true
	}
	total, controls, highLatin, asciiLetters := 0, 0, 0, 0
	for _, r := range text {
		if unicode.IsSpace(r) {
			continue
		}
		total++
		if r != '·' && r >= 0x80 && r <= 0x9F {
			controls++
		}
		if r >= 0xA0 && r <= 0xFF {
			highLatin++
		}
		if r <= unicode.MaxASCII && unicode.IsLetter(r) {
			asciiLetters++
		}
	}
	if total < 5 {
		return false
	}
	if controls >= 2 && controls*20 >= total {
		return true
	}
	return total >= 20 && highLatin*5 >= total*2 && asciiLetters*3 < total
}

func isGarbageText(text string) bool {
	runes := []rune(text)
	alphanumeric, other := 0, 0
	for i := 0; i < len(runes); {
		j := i + 1
		for j < len(runes) && runes[j] == runes[i] {
			j++
		}
		decorativeLeader := (runes[i] == '.' || runes[i] == '_' || runes[i] == '·') && j-i >= 3
		if !decorativeLeader {
			for _, r := range runes[i:j] {
				if unicode.IsSpace(r) || r == '#' || r == '*' || r == '|' || r == '-' {
					continue
				}
				if unicode.IsLetter(r) || unicode.IsDigit(r) {
					alphanumeric++
				} else {
					other++
				}
			}
		}
		i = j
	}
	total := alphanumeric + other
	return total >= 50 && alphanumeric*2 < total
}

func addOCRReason(reasons map[uint32][]string, page uint32, reason string) {
	for _, existing := range reasons[page] {
		if existing == reason {
			return
		}
	}
	reasons[page] = append(reasons[page], reason)
}

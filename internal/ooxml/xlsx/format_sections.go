package xlsx

import (
	"strconv"
	"strings"
)

type formatSection struct {
	formatKind
	text     bool
	op       string
	boundary float64
}

type formatToken struct {
	kind byte
	text string
}

// formatTokens consumes quoting, escaping and padding in one pass. Their
// operands cannot subsequently become section delimiters or date tokens.
func formatTokens(code string) []formatToken {
	var out []formatToken
	for i := 0; i < len(code); {
		start := i
		kind := lower(code[i])
		i++
		switch kind {
		case '\\', '_', '*':
			if i < len(code) {
				i++
			}
			if kind == '\\' {
				out = append(out, formatToken{'l', code[start+1 : i]})
			}
			continue
		case '"', '[':
			end := byte('"')
			if kind == '[' {
				end = ']'
			}
			for i < len(code) && code[i] != end {
				i++
			}
			text := code[start+1 : i]
			if i < len(code) {
				i++
			}
			if kind == '"' {
				kind = 'l'
			}
			out = append(out, formatToken{kind, text})
			continue
		}
		switch {
		case len(code)-start >= 7 && strings.EqualFold(code[start:start+7], "General"):
			kind, i = 'G', start+7
		case len(code)-start >= 5 && strings.EqualFold(code[start:start+5], "AM/PM"):
			kind, i = 't', start+5
		case len(code)-start >= 3 && strings.EqualFold(code[start:start+3], "A/P"):
			kind, i = 't', start+3
		default:
			if strings.ContainsRune("ymdhsegra", rune(kind)) {
				for i < len(code) && lower(code[i]) == kind {
					i++
				}
			}
		}
		out = append(out, formatToken{kind, code[start:i]})
	}
	return out
}

func parseFormatSections(code string) []formatSection {
	tokens := formatTokens(code)
	var sections []formatSection
	start := 0
	for i := 0; i <= len(tokens); i++ {
		if i == len(tokens) || tokens[i].kind == ';' {
			sections = append(sections, classifySection(tokens[start:i]))
			start = i + 1
			if len(sections) == 4 {
				break
			}
		}
	}
	// The last section containing a live @ is the text section, even in
	// the common two-section h:mm;@ format. Earlier @ tokens cannot claim
	// numeric values either. Four-section formats reserve the fourth.
	if len(sections) == 4 {
		sections[3].text = true
	}
	return sections
}

func classifySection(tokens []formatToken) formatSection {
	var section formatSection
	var numeric strings.Builder
	sawNumber := false
	for i, token := range tokens {
		if strings.ContainsRune("0#?.", rune(token.kind)) {
			numeric.WriteString(token.text)
		} else {
			numeric.WriteByte(' ')
		}
		switch token.kind {
		case 'y', 'd', 'g', 'r':
			section.hasDate = true
		case 'e':
			// E following numeric placeholders belongs to scientific notation.
			if !sawNumber {
				section.hasDate = true
			}
		case 'a':
			section.hasDate = section.hasDate || len(token.text) >= 3
		case 'h', 's', 't':
			section.hasTime = true
		case 'm':
			if neighboringTime(tokens, i, -1, 'h') || neighboringTime(tokens, i, 1, 's') {
				section.hasTime = true
			} else {
				section.hasDate = true
			}
		case '[':
			inner := strings.ToLower(token.text)
			if inner != "" && strings.ContainsRune("hms", rune(inner[0])) && strings.Trim(inner, inner[:1]) == "" {
				section.elapsed, section.hasTime = true, true
			} else {
				for _, op := range []string{">=", "<=", "<>", ">", "<", "="} {
					if value, ok := strings.CutPrefix(inner, op); ok {
						if boundary, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil {
							section.op, section.boundary = op, boundary
						}
						break
					}
				}
			}
		case '@':
			section.text = true
		case '%':
			section.isPercent = true
		case '0', '#', '?':
			sawNumber = true
		}
	}
	section.isDate = section.hasDate || section.hasTime
	if section.isDate {
		section.isPercent = false
	}
	section.decimals = decimalsIn(numeric.String())
	return section
}

func neighboringTime(tokens []formatToken, at, direction int, kind byte) bool {
	for i := at + direction; i >= 0 && i < len(tokens); i += direction {
		token := tokens[i]
		if token.kind == kind {
			return true
		}
		if token.kind == '[' {
			inner := strings.ToLower(token.text)
			if inner != "" && strings.Trim(inner, string(kind)) == "" {
				return true
			}
			continue // color, locale, condition
		}
		if strings.Trim(token.text, ":/-. ") != "" {
			return false
		}
	}
	return false
}

func (s formatSection) matches(value float64) bool {
	switch s.op {
	case ">=":
		return value >= s.boundary
	case "<=":
		return value <= s.boundary
	case "<>":
		return value != s.boundary
	case ">":
		return value > s.boundary
	case "<":
		return value < s.boundary
	case "=":
		return value == s.boundary
	}
	return true
}

func (f numFormat) selectSection(value float64) (formatKind, bool) {
	sections := f.sections
	if len(sections) == 0 {
		return f.formatKind, false
	}
	if sections[len(sections)-1].text {
		sections = sections[:len(sections)-1]
	}
	conditional := false
	for _, s := range sections {
		conditional = conditional || s.op != ""
	}
	if conditional {
		for _, s := range sections {
			if !s.text && s.matches(value) {
				return s.formatKind, value < 0 && s.op != ""
			}
		}
		return formatKind{}, false
	}
	index := 0
	if value < 0 && len(sections) >= 2 {
		index = 1
	} else if value == 0 && len(sections) >= 3 {
		index = 2
	}
	if index >= len(sections) || sections[index].text {
		return formatKind{}, false
	}
	return sections[index].formatKind, index == 1
}

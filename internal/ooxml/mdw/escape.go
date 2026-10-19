package mdw

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// TextContext describes the Markdown construct surrounding literal text.
type TextContext uint8

const (
	Inline TextContext = iota
	Heading
	TableCell
)

func EscapeInline(s string) string { return EscapeText(s, Inline) }
func EscapeCell(s string) string   { return EscapeText(strings.TrimSpace(s), TableCell) }

func EscapeText(s string, context TextContext) string {
	lineStart := true
	return EscapeFragment(s, context, &lineStart)
}

// EscapeFragment preserves text as text under CommonMark/GFM. Converter-owned
// Markdown is added afterwards. Track indentation across run boundaries, and
// check possible block delimiters at every fragment boundary ("1" + ". item").
// In headings # cannot close the heading; table newlines cannot end the row.
func EscapeFragment(s string, context TextContext, lineStart *bool) string {
	var b strings.Builder
	scan := context == Inline
	indent := scan && *lineStart
	wCount, onlyW := 0, true
	for s != "" {
		if scan {
			scan = false
			if indent {
				s = strings.TrimLeft(s, " \t\ufeff")
			}
			body := strings.TrimLeftFunc(s, func(r rune) bool { return r != '\r' && r != '\n' && (unicode.IsSpace(r) || r == '\ufeff') })
			if len(body) != len(s) {
				b.WriteString(s[:len(s)-len(body)])
				indent = false
			}
			s = body
			if s == "" {
				break
			}
			indent = false
			if offset := blockMarker(s); offset >= 0 {
				b.WriteString(s[:offset])
				b.WriteByte('\\')
				b.WriteByte(s[offset])
				s = s[offset+1:]
				wCount, onlyW = 0, false
			}
			continue
		}
		r, size := utf8.DecodeRuneInString(s)
		s = s[size:]
		if r == '\r' || r == '\n' {
			if r == '\r' {
				s = strings.TrimPrefix(s, "\n")
			}
			switch context {
			case TableCell:
				b.WriteString("<br>")
			case Heading:
				b.WriteByte(' ')
			default:
				b.WriteByte('\n')
				scan, indent = true, true
			}
			wCount, onlyW = 0, false
			continue
		}
		if strings.ContainsRune("\\`*_[]<>!~&|:@", r) || (r == '#' && context == Heading) || (r == '.' && (wCount >= 3 || onlyW)) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
		if r == 'w' || r == 'W' {
			wCount++
		} else {
			wCount, onlyW = 0, false
		}
	}
	*lineStart = context == Inline && indent
	return b.String()
}

func blockMarker(s string) int {
	ends := func(i int) bool {
		if i == len(s) {
			return true
		}
		r, _ := utf8.DecodeRuneInString(s[i:])
		return unicode.IsSpace(r) || r == '\ufeff'
	}
	switch s[0] {
	case '#':
		n := len(s) - len(strings.TrimLeft(s, "#"))
		if n <= 6 && ends(n) {
			return 0
		}
	case '-', '=':
		if ends(1) || len(s) > 1 && s[1] == s[0] {
			return 0
		}
	case '+':
		if ends(1) {
			return 0
		}
	default:
		i := 0
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i <= 9 && i < len(s) && (s[i] == '.' || s[i] == ')') && ends(i+1) {
			return i
		}
	}
	return -1
}

// EscapeURL returns an inert Markdown destination or an empty string when its
// scheme is disallowed. Keep the label when rejecting a hyperlink. Controls,
// Unicode whitespace and BOM cannot conceal a scheme. Entity escaping is
// necessary because Markdown decodes entities in destinations after parsing.
func EscapeURL(s string) string {
	ignorable := func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) || r == '\ufeff' }
	clean := strings.Map(func(r rune) rune {
		if ignorable(r) {
			return -1
		}
		return r
	}, s)
	if scheme, _, found := strings.Cut(clean, ":"); found && validScheme(scheme) {
		switch strings.ToLower(scheme) {
		case "http", "https", "mailto", "tel":
		default:
			return ""
		}
	}
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for _, r := range s {
		if ignorable(r) || strings.ContainsRune("()<>\\|\"", r) {
			for _, c := range []byte(string(r)) {
				b.WriteByte('%')
				b.WriteByte(hex[c>>4])
				b.WriteByte(hex[c&15])
			}
		} else if r == '&' {
			b.WriteString("&amp;")
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func validScheme(s string) bool {
	for i, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
			continue
		}
		if i == 0 || !(r >= '0' && r <= '9' || r == '+' || r == '-' || r == '.') {
			return false
		}
	}
	return s != ""
}

// Link wraps already-escaped label text. A rejected destination keeps the text.
func Link(label, target string) string {
	if target = EscapeURL(target); target != "" {
		return "[" + label + "](" + target + ")"
	}
	return label
}

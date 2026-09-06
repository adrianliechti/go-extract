// Package rtf extracts readable text from Rich Text Format (.rtf) documents.
// Formatting and embedded objects are discarded; paragraph, line, and tab
// structure is retained as plain Markdown-compatible text.
package rtf

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/adrianliechti/go-extract/internal/textutil"
)

// ErrNotRTF means the input does not have an RTF document header.
var ErrNotRTF = errors.New("rtf: input is not a Rich Text Format document")

// Options configures RTF conversion.
type Options struct {
	// CodePage is the fallback character encoding for non-Unicode text. Zero
	// uses Windows-1252. An \ansicpg control word in the document overrides it.
	CodePage int
}

// Document is the result of converting an RTF document.
type Document struct {
	Markdown string
}

// Detect reports whether data begins with an RTF document header.
func Detect(data []byte) bool {
	data = bytes.TrimLeftFunc(data, unicode.IsSpace)
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	return len(data) >= len(`{\rtf`) && strings.EqualFold(string(data[:len(`{\rtf`)]), `{\rtf`)
}

// DetectFile reports whether a file is an RTF document.
func DetectFile(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	return Detect(data), nil
}

// Convert extracts an in-memory RTF document.
func Convert(data []byte, opts Options) (*Document, error) {
	if !Detect(data) {
		return nil, ErrNotRTF
	}
	return &Document{Markdown: ToText(data, opts)}, nil
}

// ConvertFile reads and extracts an RTF document from disk.
func ConvertFile(path string, opts Options) (*Document, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	doc, err := Convert(data, opts)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return doc, nil
}

// ToMarkdown extracts only the Markdown-compatible text from an RTF document.
func ToMarkdown(data []byte, opts Options) (string, error) {
	doc, err := Convert(data, opts)
	if err != nil {
		return "", err
	}
	return doc.Markdown, nil
}

type state struct {
	skip   bool
	ucSkip int
}

// ToText strips RTF markup. It is also used for decompressed RTF bodies
// embedded in Outlook MSG files, whose fallback code page comes from MAPI.
func ToText(data []byte, opts Options) string {
	codePage := opts.CodePage
	if codePage <= 0 {
		codePage = 1252
	}
	states := []state{{ucSkip: 1}}
	var out strings.Builder
	for i := 0; i < len(data); {
		switch data[i] {
		case '{':
			states = append(states, states[len(states)-1])
			i++
		case '}':
			if len(states) > 1 {
				states = states[:len(states)-1]
			}
			i++
		case '\\':
			i++
			if i >= len(data) {
				break
			}
			current := &states[len(states)-1]
			if strings.ContainsRune("{}\\", rune(data[i])) {
				if !current.skip {
					out.WriteByte(data[i])
				}
				i++
				continue
			}
			switch data[i] {
			case '*':
				current.skip = true
				i++
				continue
			case '~':
				if !current.skip {
					out.WriteRune('\u00a0')
				}
				i++
				continue
			case '_':
				if !current.skip {
					out.WriteRune('\u2011')
				}
				i++
				continue
			case '-':
				// An optional hyphen is intentionally omitted unless a renderer
				// needs to break the word at this location.
				i++
				continue
			case '\'':
				if i+2 < len(data) {
					if value, err := strconv.ParseUint(string(data[i+1:i+3]), 16, 8); err == nil && !current.skip {
						out.WriteString(textutil.DecodeCodePage([]byte{byte(value)}, codePage))
					}
					i += 3
					continue
				}
			}
			start := i
			for i < len(data) && ((data[i] >= 'a' && data[i] <= 'z') || (data[i] >= 'A' && data[i] <= 'Z')) {
				i++
			}
			word := strings.ToLower(string(data[start:i]))
			sign := 1
			if i < len(data) && data[i] == '-' {
				sign = -1
				i++
			}
			numStart := i
			for i < len(data) && data[i] >= '0' && data[i] <= '9' {
				i++
			}
			hasNumber := i > numStart
			number := 0
			if hasNumber {
				number, _ = strconv.Atoi(string(data[numStart:i]))
				number *= sign
			}
			if i < len(data) && data[i] == ' ' {
				i++
			}
			current = &states[len(states)-1]
			switch word {
			case "fonttbl", "colortbl", "stylesheet", "info", "pict", "object", "header", "footer", "generator", "datastore", "themedata", "xmlnstbl":
				current.skip = true
			case "ansicpg":
				if hasNumber && number > 0 {
					codePage = number
				}
			case "uc":
				if hasNumber && number >= 0 {
					current.ucSkip = number
				}
			case "u":
				if !current.skip && hasNumber {
					value := number
					if value < 0 {
						value += 65536
					}
					out.WriteRune(rune(value))
				}
				for skipped := 0; skipped < current.ucSkip && i < len(data); skipped++ {
					if data[i] == '\\' && i+3 < len(data) && data[i+1] == '\'' {
						i += 4
					} else {
						i++
					}
				}
			case "par", "line", "sect", "page":
				if !current.skip {
					out.WriteByte('\n')
				}
			case "tab":
				if !current.skip {
					out.WriteByte('\t')
				}
			case "bullet":
				if !current.skip {
					out.WriteRune('\u2022')
				}
			case "emdash":
				if !current.skip {
					out.WriteRune('\u2014')
				}
			case "endash":
				if !current.skip {
					out.WriteRune('\u2013')
				}
			case "lquote":
				if !current.skip {
					out.WriteRune('\u2018')
				}
			case "rquote":
				if !current.skip {
					out.WriteRune('\u2019')
				}
			case "ldblquote":
				if !current.skip {
					out.WriteRune('\u201c')
				}
			case "rdblquote":
				if !current.skip {
					out.WriteRune('\u201d')
				}
			}
		case '\r', '\n':
			i++
		default:
			start := i
			for i < len(data) && !bytes.ContainsRune([]byte("{}\\\r\n"), rune(data[i])) {
				i++
			}
			if !states[len(states)-1].skip {
				chunk := data[start:i]
				if utf8.Valid(chunk) {
					out.Write(chunk)
				} else {
					out.WriteString(textutil.DecodeCodePage(chunk, codePage))
				}
			}
		}
	}
	return normalize(out.String())
}

func normalize(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.TrimPrefix(text, "\ufeff")
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = strings.TrimRightFunc(lines[i], unicode.IsSpace)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

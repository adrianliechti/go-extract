package msg

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/adrianliechti/go-extract/internal/html"
)

func messageBodyMarkdown(m *Message, opts Options) (string, error) {
	if opts.PreferPlainText && strings.TrimSpace(m.TextBody) != "" {
		return normalizePlainText(m.TextBody), nil
	}
	if strings.TrimSpace(m.HTMLBody) != "" {
		source := rewriteCIDReferences(m.HTMLBody, m.Attachments, attachmentPrefix(opts))
		md, err := html.ToMarkdown([]byte(source), html.Options{ContentType: "text/html; charset=utf-8"})
		if err != nil {
			return "", fmt.Errorf("msg: convert HTML body: %w", err)
		}
		return strings.TrimSpace(md), nil
	}
	return normalizePlainText(m.TextBody), nil
}

func normalizePlainText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.TrimPrefix(s, "\ufeff")
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimRightFunc(lines[i], unicode.IsSpace)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

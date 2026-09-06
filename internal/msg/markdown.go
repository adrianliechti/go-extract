package msg

import (
	"fmt"
	"html"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func renderMarkdown(m *Message, opts Options) string {
	var b strings.Builder
	title := strings.TrimSpace(m.Subject)
	if title == "" {
		title = "Email message"
	}
	b.WriteString("# ")
	b.WriteString(escapeHeading(title))
	b.WriteString("\n\n")

	writeMetadata := func(label, value string) {
		if strings.TrimSpace(value) == "" {
			return
		}
		b.WriteString("**")
		b.WriteString(label)
		b.WriteString(":** ")
		b.WriteString(escapeMetadata(value))
		b.WriteString("  \n")
	}
	writeMetadata("From", m.From.String())
	writeMetadata("To", joinAddresses(m.To))
	writeMetadata("Cc", joinAddresses(m.CC))
	writeMetadata("Bcc", joinAddresses(m.BCC))
	writeMetadata("Reply-To", joinAddresses(m.ReplyTo))
	if !m.Date.IsZero() {
		writeMetadata("Date", m.Date.Format("2006-01-02 15:04:05 -0700"))
	}
	writeMetadata("Message-ID", m.MessageID)

	if strings.TrimSpace(m.BodyMarkdown) != "" {
		b.WriteString("\n")
		b.WriteString(strings.TrimSpace(m.BodyMarkdown))
		b.WriteString("\n")
	}

	if len(m.Attachments) > 0 && !opts.SkipAttachments {
		b.WriteString("\n## Attachments\n\n")
		for i := range m.Attachments {
			a := &m.Attachments[i]
			b.WriteString("- ")
			if a.Embedded != nil && len(a.Data) == 0 {
				b.WriteString(escapeMetadata(a.Name))
				b.WriteString(" (embedded message)")
			} else {
				b.WriteString("[")
				b.WriteString(escapeLinkText(a.Name))
				b.WriteString("](")
				b.WriteString(attachmentURL(attachmentPrefix(opts), a.Name))
				b.WriteString(")")
				var details []string
				if a.ContentType != "" {
					details = append(details, a.ContentType)
				}
				details = append(details, formatBytes(len(a.Data)))
				b.WriteString(" (")
				b.WriteString(strings.Join(details, ", "))
				b.WriteString(")")
			}
			b.WriteString("\n")
		}
	}

	return strings.TrimSpace(b.String()) + "\n"
}

func escapeHeading(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\\", "\\\\")
	return strings.ReplaceAll(s, "#", "\\#")
}

func escapeMetadata(s string) string {
	s = strings.Join(strings.Fields(strings.ReplaceAll(s, "\x00", "")), " ")
	s = html.EscapeString(s)
	return strings.ReplaceAll(s, "\\", "\\\\")
}

func escapeLinkText(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "[", "\\[")
	return strings.ReplaceAll(s, "]", "\\]")
}

func joinAddresses(addresses []Address) string {
	values := make([]string, 0, len(addresses))
	for _, a := range addresses {
		if s := strings.TrimSpace(a.String()); s != "" {
			values = append(values, s)
		}
	}
	return strings.Join(values, ", ")
}

func formatBytes(n int) string {
	const unit = 1024
	if n < unit {
		return strconv.Itoa(n) + " B"
	}
	div, exp := int64(unit), 0
	for value := int64(n) / unit; value >= unit && exp < 3; value /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGT"[exp])
}

func attachmentURL(prefix, name string) string {
	parts := []string{}
	if prefix != "" {
		parts = append(parts, strings.Split(strings.Trim(prefix, "/"), "/")...)
	}
	parts = append(parts, name)
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}

func makeAttachmentNamesUnique(attachments []Attachment) {
	used := make(map[string]struct{}, len(attachments))
	for i := range attachments {
		name := safeFilename(attachments[i].Name)
		if name == "" {
			name = fmt.Sprintf("attachment-%d%s", i+1, extensionForType(attachments[i].ContentType))
		}
		base, ext := strings.TrimSuffix(name, filepath.Ext(name)), filepath.Ext(name)
		candidate := name
		for suffix := 2; ; suffix++ {
			key := strings.ToLower(candidate)
			if _, exists := used[key]; !exists {
				used[key] = struct{}{}
				break
			}
			candidate = fmt.Sprintf("%s-%d%s", base, suffix, ext)
		}
		attachments[i].Name = candidate
	}
}

func safeFilename(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = filepath.Base(name)
	name = strings.Map(func(r rune) rune {
		if r == 0 || r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "." || name == ".." {
		return ""
	}
	return name
}

func extensionForType(contentType string) string {
	switch strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0])) {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "application/pdf":
		return ".pdf"
	case "message/rfc822":
		return ".eml"
	case "text/plain":
		return ".txt"
	case "text/html":
		return ".html"
	}
	return ""
}

func writeAttachments(attachments []Attachment, dir string) error {
	if len(attachments) == 0 {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for i := range attachments {
		a := &attachments[i]
		if len(a.Data) == 0 {
			continue
		}
		dst := filepath.Join(dir, a.Name)
		if err := os.WriteFile(dst, a.Data, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", dst, err)
		}
	}
	return nil
}

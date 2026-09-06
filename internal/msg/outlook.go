package msg

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"net/mail"
	"strings"
	"unicode/utf8"

	"github.com/abemedia/go-cfb"
	"golang.org/x/net/html/charset"
)

const (
	attachByValue      = 1
	attachEmbeddedMSG  = 5
	attachOle          = 6
	attachMHTMLRefFlag = 0x00000004
)

func parseMSG(data []byte, opts Options) (*Message, error) {
	r, err := cfb.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotMSG, err)
	}
	m, err := parseMSGStorage(r.Storage, int64(len(data)), opts, 0)
	if err != nil {
		return nil, err
	}
	m.Format = FormatMSG
	return m, nil
}

func parseMSGStorage(storage *cfb.Storage, maxStream int64, opts Options, depth int) (*Message, error) {
	if depth > mimeDepthLimit(opts) {
		return nil, fmt.Errorf("%w: embedded MSG nesting exceeds %d", ErrResourceLimit, mimeDepthLimit(opts))
	}
	o, err := readMAPIObject(storage, true, maxStream)
	if err != nil {
		return nil, err
	}
	m := &Message{Format: FormatMSG}

	if transport := o.string(0x007d); transport != "" {
		if headers, err := mail.ReadMessage(strings.NewReader(transport + "\r\n\r\n")); err == nil {
			populateFromHeaders(m, headers.Header)
		}
	}
	if m.Headers == nil {
		m.Headers = make(mail.Header)
	}

	if m.Subject == "" {
		m.Subject = o.string(0x0037)
	}
	if m.From.String() == "" {
		name := firstNonEmpty(o.string(0x0042), o.string(0x0c1a))
		address := firstSMTP(
			o.string(0x5d02), // PR_SENT_REPRESENTING_SMTP_ADDRESS
			o.string(0x5d01), // PR_SENDER_SMTP_ADDRESS
			o.string(0x0065), // PR_SENT_REPRESENTING_EMAIL_ADDRESS
			o.string(0x0c1f), // PR_SENDER_EMAIL_ADDRESS
		)
		m.From = Address{Name: name, Address: address}
	}
	if m.MessageID == "" {
		m.MessageID = o.string(0x1035)
	}
	if m.InReplyTo == "" {
		m.InReplyTo = o.string(0x1042)
	}
	if m.Date.IsZero() {
		for _, id := range []uint16{0x0039, 0x0e06, 0x3007, 0x3008} {
			if date := o.time(id); !date.IsZero() {
				m.Date = date
				break
			}
		}
	}

	parseMSGRecipients(m, o, maxStream)
	if len(m.To) == 0 {
		m.To = parseDisplayAddresses(o.string(0x0e04))
	}
	if len(m.CC) == 0 {
		m.CC = parseDisplayAddresses(o.string(0x0e03))
	}
	if len(m.BCC) == 0 {
		m.BCC = parseDisplayAddresses(o.string(0x0e02))
	}

	m.TextBody = o.string(0x1000)
	m.HTMLBody = o.string(0x1013)
	if m.HTMLBody == "" {
		if rawHTML := o.binary(0x1013); len(rawHTML) > 0 {
			m.HTMLBody = decodeMSGHTML(rawHTML, o.codePage)
		}
	}
	if m.TextBody == "" && m.HTMLBody == "" {
		if compressed := o.binary(0x1009); len(compressed) > 0 {
			if rtf, err := decompressRTF(compressed); err == nil {
				m.TextBody = rtfToText(rtf, o.codePage)
			}
		}
	}

	attachments, err := parseMSGAttachments(o, maxStream, opts, depth)
	if err != nil {
		return nil, err
	}
	m.Attachments = attachments
	return m, nil
}

func parseMSGRecipients(m *Message, root *mapiObject, maxStream int64) {
	hasTo, hasCC, hasBCC := len(m.To) > 0, len(m.CC) > 0, len(m.BCC) > 0
	for _, storage := range root.childStorages("__recip_version1.0_") {
		r, err := readMAPIObject(storage, false, maxStream)
		if err != nil {
			continue
		}
		name := r.string(0x3001)
		address := firstSMTP(r.string(0x39fe), r.string(0x3003))
		if name == "" && address == "" {
			continue
		}
		recipient := Address{Name: name, Address: address}
		typeValue, _ := r.integer(0x0c15)
		switch typeValue {
		case 2:
			if !hasCC {
				m.CC = append(m.CC, recipient)
			}
		case 3:
			if !hasBCC {
				m.BCC = append(m.BCC, recipient)
			}
		default:
			if !hasTo {
				m.To = append(m.To, recipient)
			}
		}
	}
}

func parseMSGAttachments(root *mapiObject, maxStream int64, opts Options, depth int) ([]Attachment, error) {
	var attachments []Attachment
	for _, storage := range root.childStorages("__attach_version1.0_") {
		ao, err := readMAPIObject(storage, false, maxStream)
		if err != nil {
			return nil, err
		}
		method, _ := ao.integer(0x3705)
		name := firstNonEmpty(ao.string(0x3707), ao.string(0x3704), ao.string(0x3001))
		contentType := strings.TrimSpace(ao.string(0x370e))
		contentID := cleanContentID(ao.string(0x3712))
		contentLocation := strings.TrimSpace(ao.string(0x3713))
		flags, _ := ao.integer(0x3714)
		a := Attachment{
			Name:            name,
			ContentType:     contentType,
			ContentID:       contentID,
			ContentLocation: contentLocation,
			Inline:          contentID != "" || contentLocation != "" || flags&attachMHTMLRefFlag != 0,
		}

		switch method {
		case attachEmbeddedMSG:
			if embeddedStorage := ao.childStorage("__substg1.0_3701000D"); embeddedStorage != nil {
				embedded, err := parseMSGStorage(embeddedStorage, maxStream, opts, depth+1)
				if err == nil {
					a.Embedded = embedded
					a.ContentType = "application/vnd.ms-outlook"
				}
			}
		case attachByValue, attachOle, 0:
			a.Data = append([]byte(nil), ao.binary(0x3701)...)
		default:
			a.Data = append([]byte(nil), ao.binary(0x3701)...)
		}
		if a.ContentType == "" {
			a.ContentType = contentTypeByName(a.Name, a.Data)
		}
		attachments = append(attachments, a)
	}
	return attachments, nil
}

func decodeMSGHTML(data []byte, codePage int) string {
	if len(data) >= 2 {
		if bytes.HasPrefix(data, []byte{0xff, 0xfe}) {
			return decodeUTF16LE(data[2:])
		}
		if bytes.HasPrefix(data, []byte{0xfe, 0xff}) {
			return decodeCodePage(data[2:], 1201)
		}
	}
	if utf8.Valid(data) {
		return strings.TrimRight(strings.TrimPrefix(string(data), "\ufeff"), "\x00")
	}
	if r, err := charset.NewReader(bytes.NewReader(data), "text/html"); err == nil {
		if decoded, err := io.ReadAll(r); err == nil && utf8.Valid(decoded) {
			return strings.TrimRight(string(decoded), "\x00")
		}
	}
	return decodeCodePage(data, codePage)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func firstSMTP(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || strings.HasPrefix(strings.ToUpper(value), "/O=") {
			continue
		}
		if strings.Contains(value, "@") {
			return value
		}
	}
	return ""
}

func parseDisplayAddresses(value string) []Address {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if parsed := parseAddressHeader(strings.ReplaceAll(value, ";", ",")); len(parsed) > 0 {
		return parsed
	}
	return []Address{{Name: value}}
}

func contentTypeByName(name string, data []byte) string {
	if ext := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(extension(name), "."))); ext != "" {
		if value := mime.TypeByExtension("." + ext); value != "" {
			mediaType, _, err := mime.ParseMediaType(value)
			if err == nil {
				return mediaType
			}
		}
	}
	if len(data) > 0 {
		return sniffContentType(data)
	}
	return "application/octet-stream"
}

func extension(name string) string {
	if dot := strings.LastIndexByte(name, '.'); dot >= 0 {
		return name[dot:]
	}
	return ""
}

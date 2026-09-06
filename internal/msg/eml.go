package msg

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/net/html/charset"
	"golang.org/x/text/encoding/charmap"
)

const (
	defaultMaxMIMEParts = 10_000
	defaultMaxMIMEDepth = 32
)

type emlParser struct {
	opts        Options
	parts       int
	plainBodies []string
	htmlBodies  []string
	attachments []Attachment
}

func parseEML(data []byte, opts Options, depth int) (*Message, error) {
	if depth > mimeDepthLimit(opts) {
		return nil, fmt.Errorf("%w: MIME nesting exceeds %d", ErrResourceLimit, mimeDepthLimit(opts))
	}
	data = prepareEMLData(data)
	raw, err := mail.ReadMessage(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotEML, err)
	}

	m := &Message{Format: FormatEML}
	populateFromHeaders(m, raw.Header)
	p := &emlParser{opts: opts}
	if err := p.walk(textproto.MIMEHeader(raw.Header), raw.Body, depth); err != nil {
		return nil, err
	}
	m.TextBody = strings.TrimSpace(strings.Join(p.plainBodies, "\n\n"))
	m.HTMLBody = strings.TrimSpace(strings.Join(p.htmlBodies, "\n"))
	m.Attachments = p.attachments
	return m, nil
}

func (p *emlParser) walk(header textproto.MIMEHeader, body io.Reader, depth int) error {
	p.parts++
	if p.parts > mimePartLimit(p.opts) {
		return fmt.Errorf("%w: MIME part count exceeds %d", ErrResourceLimit, mimePartLimit(p.opts))
	}
	if depth > mimeDepthLimit(p.opts) {
		return fmt.Errorf("%w: MIME nesting exceeds %d", ErrResourceLimit, mimeDepthLimit(p.opts))
	}

	contentType := header.Get("Content-Type")
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType == "" {
		mediaType = "text/plain"
		params = map[string]string{}
	}
	mediaType = strings.ToLower(mediaType)

	if strings.HasPrefix(mediaType, "multipart/") {
		if transferEncoding := strings.TrimSpace(header.Get("Content-Transfer-Encoding")); transferEncoding != "" && !strings.EqualFold(transferEncoding, "7bit") && !strings.EqualFold(transferEncoding, "8bit") && !strings.EqualFold(transferEncoding, "binary") {
			decoded, err := decodeTransferBody(transferEncoding, body)
			if err != nil {
				return fmt.Errorf("msg: decode multipart body: %w", err)
			}
			body = bytes.NewReader(decoded)
		}
		boundary := params["boundary"]
		if boundary == "" {
			return fmt.Errorf("msg: multipart entity has no boundary")
		}
		mr := multipart.NewReader(body, boundary)
		seen := 0
		for {
			part, err := mr.NextRawPart()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				// Generated mail occasionally has a malformed final boundary.
				// Preserve parts already decoded instead of losing the email.
				if seen > 0 {
					return nil
				}
				return fmt.Errorf("msg: read MIME part: %w", err)
			}
			seen++
			if err := p.walk(part.Header, part, depth+1); err != nil {
				_ = part.Close()
				return err
			}
			if err := part.Close(); err != nil {
				return err
			}
		}
	}

	decoded, err := decodeTransferBody(header.Get("Content-Transfer-Encoding"), body)
	if err != nil {
		return fmt.Errorf("msg: decode MIME body: %w", err)
	}

	disposition, dispParams, _ := mime.ParseMediaType(header.Get("Content-Disposition"))
	name := dispParams["filename"]
	if name == "" {
		name = params["name"]
	}
	name = decodeHeaderText(name)
	isAttachment := strings.EqualFold(disposition, "attachment") || name != ""

	if mediaType == "message/rfc822" {
		a := Attachment{
			Name:        name,
			ContentType: mediaType,
			ContentID:   cleanContentID(header.Get("Content-ID")),
			Data:        decoded,
			Inline:      strings.EqualFold(disposition, "inline"),
		}
		if embedded, err := parseEML(decoded, p.opts, depth+1); err == nil {
			a.Embedded = embedded
		}
		p.attachments = append(p.attachments, a)
		return nil
	}

	if !isAttachment && (mediaType == "text/plain" || mediaType == "text/html") {
		text := decodeMIMEText(decoded, contentType, params["charset"], mediaType == "text/html")
		if mediaType == "text/html" {
			p.htmlBodies = append(p.htmlBodies, text)
		} else {
			p.plainBodies = append(p.plainBodies, text)
		}
		return nil
	}

	inline := strings.EqualFold(disposition, "inline") || header.Get("Content-ID") != ""
	p.attachments = append(p.attachments, Attachment{
		Name:            name,
		ContentType:     mediaType,
		ContentID:       cleanContentID(header.Get("Content-ID")),
		ContentLocation: strings.TrimSpace(header.Get("Content-Location")),
		Inline:          inline,
		Data:            decoded,
	})
	return nil
}

func decodeTransferBody(transferEncoding string, r io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(r)
	if err != nil && len(raw) == 0 {
		return nil, err
	}
	switch strings.ToLower(strings.TrimSpace(transferEncoding)) {
	case "base64":
		compact := make([]byte, 0, len(raw))
		for _, b := range raw {
			if b != ' ' && b != '\t' && b != '\r' && b != '\n' {
				compact = append(compact, b)
			}
		}
		decoded := make([]byte, base64.StdEncoding.DecodedLen(len(compact)))
		n, decodeErr := base64.StdEncoding.Decode(decoded, compact)
		if decodeErr == nil {
			return decoded[:n], nil
		}
		// Malformed and deliberately abbreviated fixtures still represent a
		// useful message. Keep their original attachment bytes.
		return raw, nil
	case "quoted-printable":
		decoded, decodeErr := io.ReadAll(quotedprintable.NewReader(bytes.NewReader(raw)))
		if decodeErr == nil {
			return decoded, nil
		}
		return raw, nil
	default:
		return raw, nil
	}
}

func decodeMIMEText(data []byte, contentType, label string, isHTML bool) string {
	if isHTML {
		if r, err := charset.NewReader(bytes.NewReader(data), contentType); err == nil {
			if decoded, err := io.ReadAll(r); err == nil {
				return strings.TrimPrefix(string(decoded), "\ufeff")
			}
		}
	}
	if label != "" {
		if r, err := charset.NewReaderLabel(label, bytes.NewReader(data)); err == nil {
			if decoded, err := io.ReadAll(r); err == nil {
				return strings.TrimPrefix(string(decoded), "\ufeff")
			}
		}
	}
	if utf8.Valid(data) {
		return strings.TrimPrefix(string(data), "\ufeff")
	}
	decoded, err := io.ReadAll(charmap.Windows1252.NewDecoder().Reader(bytes.NewReader(data)))
	if err == nil {
		return string(decoded)
	}
	return strings.ToValidUTF8(string(data), "\ufffd")
}

func populateFromHeaders(m *Message, raw mail.Header) {
	m.Headers = make(mail.Header, len(raw))
	for key, values := range raw {
		decoded := make([]string, len(values))
		for i, value := range values {
			decoded[i] = decodeHeaderText(value)
		}
		m.Headers[key] = decoded
	}
	m.Subject = decodeHeaderText(raw.Get("Subject"))
	if values := parseAddressHeader(raw.Get("From")); len(values) > 0 {
		m.From = values[0]
	}
	m.To = parseAddressHeader(raw.Get("To"))
	m.CC = parseAddressHeader(raw.Get("Cc"))
	m.BCC = parseAddressHeader(raw.Get("Bcc"))
	m.ReplyTo = parseAddressHeader(raw.Get("Reply-To"))
	m.Date = parseMessageDate(raw.Get("Date"))
	m.MessageID = strings.TrimSpace(raw.Get("Message-ID"))
	m.InReplyTo = strings.TrimSpace(raw.Get("In-Reply-To"))
}

func parseMessageDate(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	if date, err := mail.ParseDate(value); err == nil {
		return date
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05 -0700"} {
		if date, err := time.Parse(layout, value); err == nil {
			return date
		}
	}
	return time.Time{}
}

func decodeHeaderText(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	decoder := &mime.WordDecoder{CharsetReader: func(charsetName string, input io.Reader) (io.Reader, error) {
		return charset.NewReaderLabel(charsetName, input)
	}}
	if decoded, err := decoder.DecodeHeader(value); err == nil {
		return decoded
	}
	return value
}

func parseAddressHeader(value string) []Address {
	value = decodeHeaderText(value)
	if value == "" {
		return nil
	}
	parsed, err := mail.ParseAddressList(value)
	if err != nil {
		if one, oneErr := mail.ParseAddress(value); oneErr == nil {
			parsed = []*mail.Address{one}
		} else {
			return []Address{{Name: value}}
		}
	}
	result := make([]Address, 0, len(parsed))
	for _, a := range parsed {
		result = append(result, Address{Name: a.Name, Address: a.Address})
	}
	return result
}

func cleanContentID(value string) string {
	return strings.Trim(strings.TrimSpace(value), "<>")
}

func mimePartLimit(opts Options) int {
	if opts.MaxMIMEParts > 0 {
		return opts.MaxMIMEParts
	}
	return defaultMaxMIMEParts
}

func mimeDepthLimit(opts Options) int {
	if opts.MaxMIMEDepth > 0 {
		return opts.MaxMIMEDepth
	}
	return defaultMaxMIMEDepth
}

func prepareEMLData(data []byte) []byte {
	if bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}) {
		return data[3:]
	}
	var order binary.ByteOrder
	switch {
	case bytes.HasPrefix(data, []byte{0xff, 0xfe}):
		order = binary.LittleEndian
		data = data[2:]
	case bytes.HasPrefix(data, []byte{0xfe, 0xff}):
		order = binary.BigEndian
		data = data[2:]
	default:
		// Some exporters encode the complete RFC 5322 entity as UTF-16 but
		// omit a BOM. Header names are ASCII, so their alternating NUL bytes
		// provide a reliable signal without guessing from message content.
		sample := data
		if len(sample) > 512 {
			sample = sample[:512]
		}
		var evenNUL, oddNUL int
		for i, value := range sample {
			if value != 0 {
				continue
			}
			if i%2 == 0 {
				evenNUL++
			} else {
				oddNUL++
			}
		}
		pairs := len(sample) / 2
		switch {
		case pairs > 8 && oddNUL > pairs/2 && evenNUL < pairs/8:
			order = binary.LittleEndian
		case pairs > 8 && evenNUL > pairs/2 && oddNUL < pairs/8:
			order = binary.BigEndian
		default:
			return data
		}
	}
	if len(data)%2 != 0 {
		data = data[:len(data)-1]
	}
	units := make([]uint16, len(data)/2)
	for i := range units {
		units[i] = order.Uint16(data[i*2 : i*2+2])
	}
	return []byte(string(utf16.Decode(units)))
}

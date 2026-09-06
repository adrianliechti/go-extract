// Package msg extracts Internet Message Format (.eml) and Microsoft Outlook
// (.msg) email files into structured data and readable Markdown.
//
// Attachments are always returned in memory unless SkipAttachments is set.
// They can additionally be written to a directory and referenced from the
// generated Markdown. Parsing and conversion are entirely local.
package msg

import (
	"bytes"
	"errors"
	"fmt"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Format identifies the source message format.
type Format int

// Supported message formats.
const (
	FormatUnknown Format = iota
	FormatEML
	FormatMSG
)

func (f Format) String() string {
	switch f {
	case FormatEML:
		return "eml"
	case FormatMSG:
		return "msg"
	default:
		return "unknown"
	}
}

// Address is an email address and its optional display name.
type Address struct {
	Name    string
	Address string
}

// String formats an address in the conventional RFC 5322 display form.
func (a Address) String() string {
	if a.Name == "" {
		return a.Address
	}
	if a.Address == "" {
		return a.Name
	}
	return a.Name + " <" + a.Address + ">"
}

// Attachment is an attachment recovered from a message.
type Attachment struct {
	// Name is the safe, unique filename used on disk and in Markdown links.
	Name string
	// ContentType is the attachment's MIME media type, when known.
	ContentType string
	// ContentID is the identifier used by inline cid: references.
	ContentID string
	// ContentLocation is the original content location, when supplied.
	ContentLocation string
	// Inline reports whether the attachment is referenced from the body.
	Inline bool
	// Data holds the original attachment bytes. Embedded Outlook messages can
	// instead be represented by Embedded when MSG does not expose raw bytes.
	Data []byte
	// Embedded is populated for a successfully parsed attached email.
	Embedded *Message
}

// Message is the result of converting an email.
type Message struct {
	// Format is the detected source format.
	Format Format
	// Markdown is the complete rendered message, including metadata and the
	// attachment list.
	Markdown string
	// Subject is the decoded message subject.
	Subject string
	// From is the author or sender presented by the message.
	From Address
	// To, CC, and BCC are the decoded recipient groups.
	To  []Address
	CC  []Address
	BCC []Address
	// ReplyTo lists explicit reply destinations.
	ReplyTo []Address
	// Date is the message date, or the zero time when it is unavailable.
	Date time.Time
	// MessageID and InReplyTo hold the corresponding Internet message IDs.
	MessageID string
	InReplyTo string
	// Headers contains decoded RFC headers. For MSG input it is populated
	// when Outlook stored the transport-header MAPI property.
	Headers mail.Header
	// TextBody and HTMLBody retain the decoded source bodies.
	TextBody string
	HTMLBody string
	// BodyMarkdown is the selected body converted without the surrounding
	// message metadata or attachment list.
	BodyMarkdown string
	// Attachments contains extracted attachment metadata and bytes.
	Attachments []Attachment
}

// Document is an alias for Message, matching the result naming used by the
// sibling document-to-Markdown packages.
type Document = Message

// Options configures conversion. The zero value is valid: it keeps
// attachments in memory, prefers an HTML body when present, and writes
// nothing to disk.
type Options struct {
	// AttachmentDir names a directory to write extracted attachments into.
	AttachmentDir string

	// AttachmentPrefix is prepended to attachment names in Markdown links. It
	// defaults to the base name of AttachmentDir when that is set.
	AttachmentPrefix string

	// SkipAttachments removes attachments from both the result and the
	// Markdown attachment list.
	SkipAttachments bool

	// PreferPlainText selects text/plain over text/html when both bodies are
	// available. By default the HTML body is converted to Markdown.
	PreferPlainText bool

	// MaxMIMEParts limits the number of MIME entities in an EML message. Zero
	// uses the safe default of 10,000.
	MaxMIMEParts int

	// MaxMIMEDepth limits nested multipart and message/rfc822 entities. Zero
	// uses the safe default of 32.
	MaxMIMEDepth int
}

// Errors returned by this package.
var (
	ErrNotMessage    = errors.New("msg: input is not an EML or Outlook MSG message")
	ErrNotEML        = errors.New("msg: input is not an EML message")
	ErrNotMSG        = errors.New("msg: input is not an Outlook MSG message")
	ErrResourceLimit = errors.New("msg: resource limit exceeded")
)

var cfbMagic = []byte{0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1}

// Detect reports the format of an in-memory message.
func Detect(data []byte) Format {
	if len(data) >= len(cfbMagic) && bytes.Equal(data[:len(cfbMagic)], cfbMagic) {
		return FormatMSG
	}
	data = prepareEMLData(data)
	m, err := mail.ReadMessage(bytes.NewReader(data))
	if err == nil && len(m.Header) > 0 {
		return FormatEML
	}
	return FormatUnknown
}

// DetectFile reports the format of a message file without converting it.
func DetectFile(path string) (Format, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return FormatUnknown, err
	}
	return Detect(data), nil
}

// Convert auto-detects and converts an in-memory EML or MSG message.
func Convert(data []byte, opts Options) (*Message, error) {
	var (
		m   *Message
		err error
	)
	switch Detect(data) {
	case FormatEML:
		m, err = parseEML(data, opts, 0)
	case FormatMSG:
		m, err = parseMSG(data, opts)
	default:
		return nil, ErrNotMessage
	}
	if err != nil {
		return nil, err
	}
	if err := finishMessage(m, opts); err != nil {
		return nil, err
	}
	return m, nil
}

// ConvertEML converts an in-memory EML message.
func ConvertEML(data []byte, opts Options) (*Message, error) {
	if Detect(data) != FormatEML {
		return nil, ErrNotEML
	}
	m, err := parseEML(data, opts, 0)
	if err != nil {
		return nil, err
	}
	if err := finishMessage(m, opts); err != nil {
		return nil, err
	}
	return m, nil
}

// ConvertMSG converts an in-memory Outlook MSG message.
func ConvertMSG(data []byte, opts Options) (*Message, error) {
	if Detect(data) != FormatMSG {
		return nil, ErrNotMSG
	}
	m, err := parseMSG(data, opts)
	if err != nil {
		return nil, err
	}
	if err := finishMessage(m, opts); err != nil {
		return nil, err
	}
	return m, nil
}

// ConvertFile reads and converts an EML or MSG file from disk.
func ConvertFile(path string, opts Options) (*Message, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	m, err := Convert(data, opts)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return m, nil
}

func finishMessage(m *Message, opts Options) error {
	if err := prepareMessage(m, opts); err != nil {
		return err
	}
	if opts.AttachmentDir != "" && !opts.SkipAttachments {
		if err := writeAttachments(m.Attachments, opts.AttachmentDir); err != nil {
			return err
		}
	}
	return nil
}

func prepareMessage(m *Message, opts Options) error {
	if opts.SkipAttachments {
		m.Attachments = nil
	} else {
		makeAttachmentNamesUnique(m.Attachments)
		for i := range m.Attachments {
			if embedded := m.Attachments[i].Embedded; embedded != nil {
				if err := prepareMessage(embedded, opts); err != nil {
					return err
				}
			}
		}
	}

	body, err := messageBodyMarkdown(m, opts)
	if err != nil {
		return err
	}
	m.BodyMarkdown = body
	m.Markdown = renderMarkdown(m, opts)
	return nil
}

func attachmentPrefix(opts Options) string {
	if opts.AttachmentPrefix != "" {
		return strings.Trim(opts.AttachmentPrefix, "/")
	}
	if opts.AttachmentDir != "" {
		return filepath.Base(filepath.Clean(opts.AttachmentDir))
	}
	return ""
}

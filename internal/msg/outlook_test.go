package msg_test

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/abemedia/go-cfb"
	"github.com/adrianliechti/go-extract/internal/msg"
)

func TestConvertSyntheticMSG(t *testing.T) {
	data := buildSyntheticMSG(t)
	dir := t.TempDir()
	m, err := msg.ConvertMSG(data, msg.Options{
		AttachmentDir:    filepath.Join(dir, "files"),
		AttachmentPrefix: "assets/mail",
	})
	if err != nil {
		t.Fatal(err)
	}
	if m.Format != msg.FormatMSG || m.Subject != "Outlook HTML message" {
		t.Fatalf("message = format %v, subject %q", m.Format, m.Subject)
	}
	if m.From.Address != "alice@example.com" || len(m.To) != 1 || m.To[0].Address != "bob@example.com" {
		t.Fatalf("addresses = from %#v, to %#v", m.From, m.To)
	}
	if m.Date.UTC() != time.Date(2026, 8, 24, 8, 15, 0, 0, time.UTC) {
		t.Fatalf("Date = %v", m.Date)
	}
	if !strings.Contains(m.BodyMarkdown, "**rich HTML**") || !strings.Contains(m.BodyMarkdown, "assets/mail/chart.png") {
		t.Fatalf("HTML Markdown =\n%s", m.BodyMarkdown)
	}
	if len(m.Attachments) != 1 || m.Attachments[0].ContentType != "image/png" || !m.Attachments[0].Inline {
		t.Fatalf("Attachments = %#v", m.Attachments)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "files", "chart.png")); err != nil || string(data) != "png-data" {
		t.Fatalf("written attachment = %q, %v", data, err)
	}
}

func buildSyntheticMSG(t *testing.T) []byte {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "synthetic-*.msg")
	if err != nil {
		t.Fatal(err)
	}
	w := cfb.NewWriterV3(file)
	writeCFBStream(t, w.StorageWriter, "__properties_version1.0", properties(32,
		fixedProperty(0x3fde0003, 65001),
		fixedProperty(0x00390040, windowsFiletime(time.Date(2026, 8, 24, 8, 15, 0, 0, time.UTC))),
	))
	writeUnicodeProperty(t, w.StorageWriter, 0x0037, "Outlook HTML message")
	writeUnicodeProperty(t, w.StorageWriter, 0x0042, "Alice Example")
	writeUnicodeProperty(t, w.StorageWriter, 0x5d02, "alice@example.com")
	writeUnicodeProperty(t, w.StorageWriter, 0x1013, `<p>This is <strong>rich HTML</strong>.</p><img alt="Chart" src="cid:chart-id">`)

	recipient, err := w.CreateStorage("__recip_version1.0_#00000000")
	if err != nil {
		t.Fatal(err)
	}
	writeCFBStream(t, recipient, "__properties_version1.0", properties(8, fixedProperty(0x0c150003, 1)))
	writeUnicodeProperty(t, recipient, 0x3001, "Bob Example")
	writeUnicodeProperty(t, recipient, 0x39fe, "bob@example.com")

	attachment, err := w.CreateStorage("__attach_version1.0_#00000000")
	if err != nil {
		t.Fatal(err)
	}
	writeCFBStream(t, attachment, "__properties_version1.0", properties(8,
		fixedProperty(0x37050003, 1),
		fixedProperty(0x37140003, 4),
	))
	writeUnicodeProperty(t, attachment, 0x3707, "chart.png")
	writeUnicodeProperty(t, attachment, 0x370e, "image/png")
	writeUnicodeProperty(t, attachment, 0x3712, "chart-id")
	writeCFBStream(t, attachment, "__substg1.0_37010102", []byte("png-data"))

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func writeUnicodeProperty(t *testing.T, storage *cfb.StorageWriter, id uint16, value string) {
	t.Helper()
	units := append(utf16.Encode([]rune(value)), 0)
	data := make([]byte, len(units)*2)
	for i, unit := range units {
		binary.LittleEndian.PutUint16(data[i*2:], unit)
	}
	writeCFBStream(t, storage, "__substg1.0_"+strings.ToUpper(hex4(id))+"001F", data)
}

func hex4(value uint16) string {
	const digits = "0123456789ABCDEF"
	return string([]byte{digits[value>>12], digits[value>>8&15], digits[value>>4&15], digits[value&15]})
}

func writeCFBStream(t *testing.T, storage *cfb.StorageWriter, name string, data []byte) {
	t.Helper()
	stream, err := storage.CreateStream(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
}

func properties(headerSize int, entries ...[]byte) []byte {
	data := make([]byte, headerSize, headerSize+16*len(entries))
	for _, entry := range entries {
		data = append(data, entry...)
	}
	return data
}

func fixedProperty(tag uint32, value uint64) []byte {
	entry := make([]byte, 16)
	binary.LittleEndian.PutUint32(entry[0:4], tag)
	binary.LittleEndian.PutUint64(entry[8:16], value)
	return entry
}

func windowsFiletime(value time.Time) uint64 {
	const unixEpoch = uint64(116444736000000000)
	return unixEpoch + uint64(value.Unix())*10_000_000 + uint64(value.Nanosecond()/100)
}

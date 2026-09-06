package msg_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrianliechti/go-extract/internal/msg"
)

func TestConvertEMLHTMLAndAttachments(t *testing.T) {
	eml := strings.Join([]string{
		"From: =?UTF-8?Q?Alice_Example?= <alice@example.com>",
		"To: Bob <bob@example.com>",
		"Subject: =?UTF-8?Q?Quarterly_=E2=9C=93?=",
		"Date: Mon, 24 Aug 2026 10:15:00 +0200",
		"Message-ID: <message@example.com>",
		"MIME-Version: 1.0",
		`Content-Type: multipart/related; boundary="outer"`,
		"",
		"--outer",
		`Content-Type: multipart/alternative; boundary="alternative"`,
		"",
		"--alternative",
		"Content-Type: text/plain; charset=utf-8",
		"",
		"Plain fallback",
		"--alternative",
		"Content-Type: text/html; charset=utf-8",
		"Content-Transfer-Encoding: quoted-printable",
		"",
		`<h2>Summary</h2><p>Revenue is <strong>up 12%</strong>.</p><table><tr><th>Item</th><th>Value</th></tr><tr><td>Sales</td><td>42</td></tr></table><img alt=3D"Logo" src=3D"cid:logo@example">`,
		"--alternative--",
		"--outer",
		"Content-Type: image/png",
		"Content-Transfer-Encoding: base64",
		"Content-ID: <logo@example>",
		`Content-Disposition: inline; filename="logo.png"`,
		"",
		"iVBORw0KGgo=",
		"--outer",
		"Content-Type: application/pdf",
		"Content-Transfer-Encoding: base64",
		`Content-Disposition: attachment; filename="report.pdf"`,
		"",
		"JVBERi0xLjQK",
		"--outer--",
		"",
	}, "\r\n")

	dir := t.TempDir()
	m, err := msg.Convert([]byte(eml), msg.Options{AttachmentDir: filepath.Join(dir, "media")})
	if err != nil {
		t.Fatal(err)
	}
	if m.Format != msg.FormatEML {
		t.Fatalf("Format = %v, want eml", m.Format)
	}
	if m.Subject != "Quarterly ✓" || m.From.Name != "Alice Example" {
		t.Fatalf("decoded metadata = subject %q, from %#v", m.Subject, m.From)
	}
	if !strings.Contains(m.BodyMarkdown, "## Summary") || !strings.Contains(m.BodyMarkdown, "**up 12%**") {
		t.Fatalf("HTML body was not converted:\n%s", m.BodyMarkdown)
	}
	if !strings.Contains(m.BodyMarkdown, "| Item") || !strings.Contains(m.BodyMarkdown, "media/logo.png") {
		t.Fatalf("HTML table or CID image missing:\n%s", m.BodyMarkdown)
	}
	if len(m.Attachments) != 2 || !m.Attachments[0].Inline {
		t.Fatalf("Attachments = %#v", m.Attachments)
	}
	for _, name := range []string{"logo.png", "report.pdf"} {
		data, err := os.ReadFile(filepath.Join(dir, "media", name))
		if err != nil || len(data) == 0 {
			t.Errorf("written attachment %s: data=%d err=%v", name, len(data), err)
		}
	}
}

func TestConvertEMLPreferPlainTextAndCharset(t *testing.T) {
	eml := "From: sender@example.com\r\n" +
		"To: recipient@example.com\r\n" +
		"Subject: Charset\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/alternative; boundary=x\r\n\r\n" +
		"--x\r\nContent-Type: text/plain; charset=iso-8859-1\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\nGr=FC=DFe aus Z=FCrich\r\n" +
		"--x\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<p>HTML choice</p>\r\n--x--\r\n"

	m, err := msg.ConvertEML([]byte(eml), msg.Options{PreferPlainText: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(m.BodyMarkdown, "Grüße aus Zürich") {
		t.Fatalf("plain charset body = %q", m.BodyMarkdown)
	}
}

func TestDetectionAndRejection(t *testing.T) {
	if got := msg.Detect([]byte("not a message")); got != msg.FormatUnknown {
		t.Fatalf("Detect = %v, want unknown", got)
	}
	if _, err := msg.Convert([]byte("not a message"), msg.Options{}); !errors.Is(err, msg.ErrNotMessage) {
		t.Fatalf("Convert error = %v, want ErrNotMessage", err)
	}
}

func TestMIMEPartLimit(t *testing.T) {
	eml := "From: a@example.com\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=x\r\n\r\n" +
		"--x\r\nContent-Type: text/plain\r\n\r\none\r\n" +
		"--x\r\nContent-Type: text/plain\r\n\r\ntwo\r\n--x--\r\n"
	_, err := msg.ConvertEML([]byte(eml), msg.Options{MaxMIMEParts: 2})
	if !errors.Is(err, msg.ErrResourceLimit) {
		t.Fatalf("error = %v, want ErrResourceLimit", err)
	}
}

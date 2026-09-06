package msg

import (
	"encoding/binary"
	"strings"
	"testing"
)

func TestCompressedRTFFallback(t *testing.T) {
	const source = `{\rtf1\ansi Hello \b world\b0\par Unicode: \u10003?}`
	raw := []byte(source)
	var compressed []byte
	for len(raw) > 0 {
		compressed = append(compressed, 0) // up to eight literal tokens
		n := min(8, len(raw))
		compressed = append(compressed, raw[:n]...)
		raw = raw[n:]
	}
	header := make([]byte, 16)
	binary.LittleEndian.PutUint32(header[0:4], uint32(len(compressed)+12))
	binary.LittleEndian.PutUint32(header[4:8], uint32(len(source)))
	binary.LittleEndian.PutUint32(header[8:12], rtfMagicCompressed)
	decoded, err := decompressRTF(append(header, compressed...))
	if err != nil {
		t.Fatal(err)
	}
	text := rtfToText(decoded, 1252)
	if !strings.Contains(text, "Hello world") || !strings.Contains(text, "✓") {
		t.Fatalf("RTF text = %q", text)
	}
}

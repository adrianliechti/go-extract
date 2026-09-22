package opc

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

// Adapted from office-open-xml-viewer 9010ef34. Go's ZIP reader already
// tolerates this metadata mismatch; keep payload integrity in the same test.
func TestLocalTimestampMismatchRetainsPayloadValidation(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, part := range []testPart{{"[Content_Types].xml", "<Types/>"}, {"word/document.xml", "document"}} {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: part.name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(part.data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()
	local := bytes.Index(data, []byte("PK\x03\x04"))
	binary.LittleEndian.PutUint16(data[local+10:], 1)
	binary.LittleEndian.PutUint16(data[local+12:], 34)
	pkg, err := Open(data)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pkg.ReadPart("word/document.xml")
	if err != nil || string(got) != "document" {
		t.Fatalf("ReadPart = %q, %v", got, err)
	}
	offset, err := pkg.files["word/document.xml"].DataOffset()
	if err != nil {
		t.Fatal(err)
	}
	data[offset] ^= 1
	if _, err := pkg.ReadPart("word/document.xml"); !errors.Is(err, zip.ErrChecksum) {
		t.Fatalf("corrupt payload error = %v, want checksum failure", err)
	}
}

func TestOpenWithLimitsRejectsArchiveEntryCount(t *testing.T) {
	data := testArchive(t, []testPart{
		{name: "[Content_Types].xml", data: "<Types/>"},
		{name: "word/document.xml", data: "<document/>"},
		{name: "word/styles.xml", data: "<styles/>"},
	})

	_, err := OpenWithLimits(data, Limits{MaxArchiveEntries: 2})
	if !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("OpenWithLimits error = %v, want ErrResourceLimit", err)
	}
}

func TestOpenWithLimitsRejectsDeclaredEntrySize(t *testing.T) {
	data := testArchive(t, []testPart{
		{name: "[Content_Types].xml", data: "<Types/>"},
		{name: "word/document.xml", data: "123456789"},
	})

	_, err := OpenWithLimits(data, Limits{MaxArchiveEntryBytes: 8})
	if !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("OpenWithLimits error = %v, want ErrResourceLimit", err)
	}
}

func TestOpenWithLimitsRejectsDeclaredTotalSize(t *testing.T) {
	data := testArchive(t, []testPart{
		{name: "[Content_Types].xml", data: "<Types/>"},
		{name: "word/document.xml", data: "1234"},
	})

	_, err := OpenWithLimits(data, Limits{MaxTotalInflatedBytes: 11})
	if !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("OpenWithLimits error = %v, want ErrResourceLimit", err)
	}
}

func TestReadPartEnforcesActualInflationLimit(t *testing.T) {
	data := testArchive(t, []testPart{
		{name: "[Content_Types].xml", data: "<Types/>"},
		{name: "word/document.xml", data: "12345"},
	})
	pkg, err := Open(data)
	if err != nil {
		t.Fatal(err)
	}

	// Tightening the policy after metadata admission models an entry whose
	// declared size understated the decompressor's actual output.
	pkg.limits.MaxArchiveEntryBytes = 4
	_, err = pkg.ReadPart("word/document.xml")
	if !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("ReadPart error = %v, want ErrResourceLimit", err)
	}
}

func TestReadPartDoesNotDoubleCountRepeatedPart(t *testing.T) {
	data := testArchive(t, []testPart{
		{name: "[Content_Types].xml", data: "<Types/>"},
		{name: "word/document.xml", data: "1234"},
	})
	pkg, err := OpenWithLimits(data, Limits{MaxTotalInflatedBytes: 12})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := pkg.ReadPart("word/document.xml"); err != nil {
			t.Fatal(err)
		}
	}
	if pkg.totalInflated != 12 {
		t.Fatalf("total inflated bytes = %d, want 12", pkg.totalInflated)
	}
}

type testPart struct {
	name string
	data string
}

func testArchive(t *testing.T, parts []testPart) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, part := range parts {
		w, err := zw.Create(part.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(part.data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

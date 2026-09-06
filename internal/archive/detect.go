package archive

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
)

var (
	zipLocalMagic   = []byte{'P', 'K', 0x03, 0x04}
	zipEmptyMagic   = []byte{'P', 'K', 0x05, 0x06}
	zipSpannedMagic = []byte{'P', 'K', 0x07, 0x08}
	gzipMagic       = []byte{0x1f, 0x8b}
)

// Detect reports the format of an in-memory archive.
func Detect(data []byte) Format {
	if bytes.HasPrefix(data, gzipMagic) {
		if reader, err := gzip.NewReader(bytes.NewReader(data)); err == nil {
			_ = reader.Close()
			return FormatGZIP
		}
	}
	if hasZIPMagic(data) {
		return FormatZIP
	}
	if isTAR(data) {
		return FormatTAR
	}
	return FormatUnknown
}

// DetectFile reports the format of an archive file without extracting it.
func DetectFile(filePath string) (Format, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return FormatUnknown, err
	}
	return Detect(data), nil
}

func hasZIPMagic(data []byte) bool {
	return bytes.HasPrefix(data, zipLocalMagic) ||
		bytes.HasPrefix(data, zipEmptyMagic) ||
		bytes.HasPrefix(data, zipSpannedMagic)
}

func isTAR(data []byte) bool {
	if len(data) < 512 {
		return false
	}
	reader := tar.NewReader(bytes.NewReader(data))
	_, err := reader.Next()
	if err == nil {
		return true
	}
	if err != io.EOF || len(data) < 1_024 {
		return false
	}
	return allZero(data[:1_024])
}

func allZero(data []byte) bool {
	for _, value := range data {
		if value != 0 {
			return false
		}
	}
	return true
}

package archive

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"
	"path"
	"strings"
)

func extractZIP(data []byte, state *extractionState) error {
	if entries, ok := declaredZIPEntries(data); ok && entries > state.limits.entries {
		return fmt.Errorf("%w: archive has more than %d entries", ErrResourceLimit, state.limits.entries)
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return fmt.Errorf("archive: open ZIP: %w", err)
	}
	if uint64(len(reader.File)) > state.limits.entries {
		return fmt.Errorf("%w: archive has more than %d entries", ErrResourceLimit, state.limits.entries)
	}

	for _, file := range reader.File {
		if err := state.countRecord(); err != nil {
			return err
		}
		if file.FileInfo().IsDir() || !file.Mode().IsRegular() {
			continue
		}
		stream, err := file.Open()
		if err != nil {
			return fmt.Errorf("archive: open ZIP entry %q: %w", file.Name, err)
		}
		err = state.addFile(file.Name, file.UncompressedSize64, stream)
		closeErr := stream.Close()
		if err != nil {
			return fmt.Errorf("archive: read ZIP entry %q: %w", file.Name, err)
		}
		if closeErr != nil {
			return fmt.Errorf("archive: close ZIP entry %q: %w", file.Name, closeErr)
		}
	}
	return nil
}

func declaredZIPEntries(data []byte) (uint64, bool) {
	const (
		endSize        = 22
		maxCommentSize = 65_535
	)
	start := max(0, len(data)-endSize-maxCommentSize)
	for offset := len(data) - endSize; offset >= start; offset-- {
		if !bytes.Equal(data[offset:offset+4], zipEmptyMagic) {
			continue
		}
		commentSize := int(binary.LittleEndian.Uint16(data[offset+20 : offset+22]))
		if offset+endSize+commentSize != len(data) {
			continue
		}
		entries := binary.LittleEndian.Uint16(data[offset+10 : offset+12])
		if entries != 0xffff {
			return uint64(entries), true
		}
		return declaredZIP64Entries(data, offset)
	}
	return 0, false
}

func declaredZIP64Entries(data []byte, endOffset int) (uint64, bool) {
	const locatorSize = 20
	locatorOffset := endOffset - locatorSize
	if locatorOffset < 0 || !bytes.Equal(data[locatorOffset:locatorOffset+4], []byte{'P', 'K', 0x06, 0x07}) {
		return 0, false
	}
	recordOffset := binary.LittleEndian.Uint64(data[locatorOffset+8 : locatorOffset+16])
	if recordOffset > uint64(len(data)) || recordOffset+40 > uint64(len(data)) {
		return 0, false
	}
	offset := int(recordOffset)
	if !bytes.Equal(data[offset:offset+4], []byte{'P', 'K', 0x06, 0x06}) {
		return 0, false
	}
	return binary.LittleEndian.Uint64(data[offset+32 : offset+40]), true
}

func extractTAR(data []byte, state *extractionState) error {
	reader := tar.NewReader(bytes.NewReader(data))
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("archive: read TAR header: %w", err)
		}
		if err := state.countRecord(); err != nil {
			return err
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			continue
		}
		if header.Size < 0 {
			return fmt.Errorf("archive: TAR entry %q has a negative size", header.Name)
		}
		if err := state.addFile(header.Name, uint64(header.Size), reader); err != nil {
			return fmt.Errorf("archive: read TAR entry %q: %w", header.Name, err)
		}
	}
}

func extractGZIP(data []byte, sourceName string, state *extractionState) error {
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("archive: open GZIP: %w", err)
	}
	if err := state.countRecord(); err != nil {
		_ = reader.Close()
		return err
	}
	name := gzipEntryName(reader.Name, sourceName)
	err = state.addFile(name, 0, reader)
	closeErr := reader.Close()
	if err != nil {
		return fmt.Errorf("archive: read GZIP stream: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("archive: close GZIP stream: %w", closeErr)
	}
	return nil
}

func gzipEntryName(headerName, sourceName string) string {
	if strings.TrimSpace(headerName) != "" {
		return headerName
	}
	name := path.Base(strings.ReplaceAll(sourceName, "\\", "/"))
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".tgz"):
		return name[:len(name)-len(".tgz")] + ".tar"
	case strings.HasSuffix(lower, ".tar.gz"):
		return name[:len(name)-len(".gz")]
	case strings.HasSuffix(lower, ".gzip"):
		return name[:len(name)-len(".gzip")]
	case strings.HasSuffix(lower, ".gz"):
		return name[:len(name)-len(".gz")]
	case name != "" && name != ".":
		return name + ".contents"
	default:
		return "contents"
	}
}

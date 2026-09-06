// Package archive extracts files from ZIP, TAR, and GZIP archives.
// Archive entries are returned in memory so the unified dispatcher can recursively
// dispatch nested archives and supported documents.
package archive

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/adrianliechti/go-extract/internal/model"
)

// Format identifies a supported archive format.
type Format string

// Supported archive formats.
const (
	FormatUnknown Format = ""
	FormatZIP     Format = "zip"
	FormatTAR     Format = "tar"
	FormatGZIP    Format = "gzip"
)

// Default resource limits for inflated archive contents.
const (
	DefaultMaxEntries    uint64 = 4_096
	DefaultMaxEntryBytes        = 128 << 20
	DefaultMaxTotalBytes        = 256 << 20

	hardMaxEntries    uint64 = 20_000
	hardMaxEntryBytes        = 512 << 20
	hardMaxTotalBytes        = 1 << 30
)

// Errors returned by this package.
var (
	ErrNotArchive    = errors.New("archive: input is not a supported archive")
	ErrResourceLimit = model.ErrResourceLimit
)

// Options configures archive extraction. Zero fields use safe defaults.
// Values above the internal hard ceilings are capped.
type Options struct {
	// MaxEntries bounds the number of records in one archive.
	MaxEntries uint64

	// MaxEntryBytes bounds the inflated size of one file.
	MaxEntryBytes uint64

	// MaxTotalBytes bounds the total inflated file data in one archive.
	MaxTotalBytes uint64
}

// Entry is a regular file recovered from an archive.
type Entry struct {
	// Name is a traversal-safe, unique, slash-separated path.
	Name string

	// OriginalName is populated when Name had to be cleaned or made unique.
	OriginalName string

	MediaType string
	Data      []byte
}

// Document is the result of extracting an archive.
type Document struct {
	Format     Format
	Markdown   string
	Entries    []Entry
	TotalBytes uint64
}

// Convert extracts an in-memory ZIP, TAR, or GZIP archive.
func Convert(data []byte, opts Options) (*Document, error) {
	return convert(data, "", opts)
}

// ConvertFile reads and extracts an archive from disk.
func ConvertFile(filePath string, opts Options) (*Document, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	doc, err := convert(data, filepath.Base(filePath), opts)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(filePath), err)
	}
	return doc, nil
}

func convert(data []byte, sourceName string, opts Options) (*Document, error) {
	format := Detect(data)
	limits := resolveLimits(opts)
	state := extractionState{
		limits: limits,
		used:   make(map[string]uint64),
	}

	var err error
	switch format {
	case FormatZIP:
		err = extractZIP(data, &state)
	case FormatTAR:
		err = extractTAR(data, &state)
	case FormatGZIP:
		err = extractGZIP(data, sourceName, &state)
	default:
		return nil, ErrNotArchive
	}
	if err != nil {
		return nil, err
	}

	doc := &Document{
		Format:     format,
		Entries:    state.entries,
		TotalBytes: state.total,
	}
	doc.Markdown = renderMarkdown(doc)
	return doc, nil
}

type limits struct {
	entries uint64
	entry   uint64
	total   uint64
}

func resolveLimits(opts Options) limits {
	resolved := limits{
		entries: opts.MaxEntries,
		entry:   opts.MaxEntryBytes,
		total:   opts.MaxTotalBytes,
	}
	if resolved.entries == 0 {
		resolved.entries = DefaultMaxEntries
	}
	if resolved.entry == 0 {
		resolved.entry = DefaultMaxEntryBytes
	}
	if resolved.total == 0 {
		resolved.total = DefaultMaxTotalBytes
	}
	resolved.entries = min(resolved.entries, hardMaxEntries)
	resolved.entry = min(resolved.entry, uint64(hardMaxEntryBytes))
	resolved.total = min(resolved.total, uint64(hardMaxTotalBytes))
	return resolved
}

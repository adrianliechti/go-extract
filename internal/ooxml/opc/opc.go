// Package opc implements the Open Packaging Conventions container that
// WordprocessingML, SpreadsheetML and PresentationML all sit on (ECMA-376
// Part 2): a ZIP archive holding XML parts, a content-type map, and a
// relationship graph linking parts to one another.
package opc

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
)

// Errors returned by this package.
var (
	ErrNotOOXML      = errors.New("opc: not an OOXML package")
	ErrPartNotFound  = errors.New("opc: part not found")
	ErrResourceLimit = errors.New("opc: resource limit exceeded")
)

// Default resource limits follow the reference viewer's standard OOXML
// package policy. They are deliberately generous for ordinary Office files
// while bounding decompression of untrusted archives.
const (
	DefaultMaxArchiveEntryBytes  uint64 = 128 << 20
	DefaultMaxTotalInflatedBytes uint64 = 256 << 20
	DefaultMaxArchiveEntries     uint64 = 4096

	hardMaxArchiveEntryBytes  uint64 = 512 << 20
	hardMaxTotalInflatedBytes uint64 = 1 << 30
	hardMaxArchiveEntries     uint64 = 20_000
)

// Limits controls resource admission for an OOXML ZIP package. Zero fields
// use the standard defaults. Values above the internal hard ceilings are
// capped so callers cannot accidentally disable the safety boundary.
type Limits struct {
	MaxArchiveEntryBytes  uint64
	MaxTotalInflatedBytes uint64
	MaxArchiveEntries     uint64
}

func (l Limits) resolved() Limits {
	if l.MaxArchiveEntryBytes == 0 {
		l.MaxArchiveEntryBytes = DefaultMaxArchiveEntryBytes
	}
	if l.MaxTotalInflatedBytes == 0 {
		l.MaxTotalInflatedBytes = DefaultMaxTotalInflatedBytes
	}
	if l.MaxArchiveEntries == 0 {
		l.MaxArchiveEntries = DefaultMaxArchiveEntries
	}
	l.MaxArchiveEntryBytes = min(l.MaxArchiveEntryBytes, hardMaxArchiveEntryBytes)
	l.MaxTotalInflatedBytes = min(l.MaxTotalInflatedBytes, hardMaxTotalInflatedBytes)
	l.MaxArchiveEntries = min(l.MaxArchiveEntries, hardMaxArchiveEntries)
	return l
}

// Relationship types used across the OOXML formats.
const (
	RelOfficeDocument = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument"
	RelImage          = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/image"
	RelHyperlink      = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/hyperlink"
	RelStyles         = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles"
	RelNumbering      = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/numbering"
	RelWorksheet      = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet"
	RelSharedStrings  = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/sharedStrings"
	RelSlide          = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide"
	RelNotesSlide     = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/notesSlide"
	RelSlideLayout    = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideLayout"
	RelCoreProperties = "http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties"
)

// Package is an opened OOXML container.
type Package struct {
	zr     *zip.Reader
	files  map[string]*zip.File
	limits Limits

	// inflatedByPart and totalInflated account actual decompressor output by
	// normalized part identity. Re-reading a part replaces its prior charge
	// rather than counting the same package bytes twice.
	inflatedByPart map[string]uint64
	totalInflated  uint64

	contentTypes *contentTypes
	relCache     map[string]Relationships
}

// Relationship links a source part to a target part or external resource.
type Relationship struct {
	ID     string
	Type   string
	Target string
	// External is true when Target is a URI rather than a part name, which is
	// how hyperlinks and linked (non-embedded) images are stored.
	External bool
	// SourcePart is the part whose _rels file declared this relationship,
	// needed to resolve Target, which is relative to that part's directory.
	SourcePart string
}

// Relationships is a set of relationships keyed by ID.
type Relationships map[string]Relationship

// ByType returns every relationship of the given type, in file order.
func (r Relationships) ByType(relType string) []Relationship {
	var out []Relationship
	for _, rel := range r {
		if rel.Type == relType {
			out = append(out, rel)
		}
	}
	return out
}

// Open reads an OOXML package from an in-memory archive.
func Open(data []byte) (*Package, error) {
	return OpenWithLimits(data, Limits{})
}

// OpenWithLimits reads an OOXML package using the supplied resource policy.
func OpenWithLimits(data []byte, limits Limits) (*Package, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotOOXML, err)
	}
	limits = limits.resolved()
	if uint64(len(zr.File)) > limits.MaxArchiveEntries {
		return nil, fmt.Errorf("%w: archive has more than %d entries", ErrResourceLimit, limits.MaxArchiveEntries)
	}

	p := &Package{
		zr:             zr,
		files:          make(map[string]*zip.File, len(zr.File)),
		limits:         limits,
		inflatedByPart: map[string]uint64{},
		relCache:       map[string]Relationships{},
	}
	var declaredTotal uint64
	for _, f := range zr.File {
		if f.UncompressedSize64 > limits.MaxArchiveEntryBytes {
			return nil, fmt.Errorf("%w: part %q declares more than %d inflated bytes", ErrResourceLimit, f.Name, limits.MaxArchiveEntryBytes)
		}
		if f.UncompressedSize64 > limits.MaxTotalInflatedBytes-declaredTotal {
			return nil, fmt.Errorf("%w: archive declares more than %d inflated bytes", ErrResourceLimit, limits.MaxTotalInflatedBytes)
		}
		declaredTotal += f.UncompressedSize64
		p.files[normalize(f.Name)] = f
	}

	// Every conforming package has a content-type map at the root.
	if _, ok := p.files["[Content_Types].xml"]; !ok {
		return nil, fmt.Errorf("%w: missing [Content_Types].xml", ErrNotOOXML)
	}
	if err := p.loadContentTypes(); err != nil {
		return nil, err
	}
	return p, nil
}

// normalize canonicalises a part name to the package-relative form used as a
// map key: no leading slash, forward slashes only.
func normalize(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = strings.TrimPrefix(name, "/")
	return path.Clean(name)
}

// Has reports whether a part exists.
func (p *Package) Has(name string) bool {
	_, ok := p.files[normalize(name)]
	return ok
}

// ReadPart returns the decompressed bytes of a part.
func (p *Package) ReadPart(name string) ([]byte, error) {
	n := normalize(name)
	f, ok := p.files[n]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrPartNotFound, name)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	previous := p.inflatedByPart[n]
	accountedWithoutPart := p.totalInflated - previous
	totalAllowance := p.limits.MaxTotalInflatedBytes - accountedWithoutPart
	allowance := min(p.limits.MaxArchiveEntryBytes, totalAllowance)
	data, err := io.ReadAll(io.LimitReader(rc, int64(allowance)+1))
	if err != nil {
		return nil, err
	}
	actual := uint64(len(data))
	if actual > p.limits.MaxArchiveEntryBytes {
		return nil, fmt.Errorf("%w: part %q exceeds %d inflated bytes", ErrResourceLimit, n, p.limits.MaxArchiveEntryBytes)
	}
	if actual > totalAllowance {
		return nil, fmt.Errorf("%w: archive exceeds %d inflated bytes", ErrResourceLimit, p.limits.MaxTotalInflatedBytes)
	}
	p.inflatedByPart[n] = actual
	p.totalInflated = accountedWithoutPart + actual
	return data, nil
}

// Parts returns every part name in the package, in archive order.
func (p *Package) Parts() []string {
	out := make([]string, 0, len(p.zr.File))
	for _, f := range p.zr.File {
		out = append(out, normalize(f.Name))
	}
	return out
}

// UnmarshalPart decodes a part as XML into v.
func (p *Package) UnmarshalPart(name string, v any) error {
	data, err := p.ReadPart(name)
	if err != nil {
		return err
	}
	dec := xml.NewDecoder(bytes.NewReader(data))
	// OOXML parts occasionally carry entity references and non-UTF-8
	// declarations; be permissive rather than reject the document.
	dec.Strict = false
	dec.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
	return dec.Decode(v)
}

// ── content types ────────────────────────────────────────────────────

type contentTypes struct {
	defaults  map[string]string // extension (lowercase) -> content type
	overrides map[string]string // part name -> content type
}

type xmlTypes struct {
	Defaults []struct {
		Extension   string `xml:"Extension,attr"`
		ContentType string `xml:"ContentType,attr"`
	} `xml:"Default"`
	Overrides []struct {
		PartName    string `xml:"PartName,attr"`
		ContentType string `xml:"ContentType,attr"`
	} `xml:"Override"`
}

func (p *Package) loadContentTypes() error {
	var t xmlTypes
	if err := p.UnmarshalPart("[Content_Types].xml", &t); err != nil {
		return fmt.Errorf("opc: content types: %w", err)
	}
	ct := &contentTypes{
		defaults:  make(map[string]string, len(t.Defaults)),
		overrides: make(map[string]string, len(t.Overrides)),
	}
	for _, d := range t.Defaults {
		ct.defaults[strings.ToLower(d.Extension)] = d.ContentType
	}
	for _, o := range t.Overrides {
		ct.overrides[normalize(o.PartName)] = o.ContentType
	}
	p.contentTypes = ct
	return nil
}

// ContentType returns the media type declared for a part, preferring an
// explicit Override over the extension Default.
func (p *Package) ContentType(name string) string {
	n := normalize(name)
	if ct, ok := p.contentTypes.overrides[n]; ok {
		return ct
	}
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(n), "."))
	return p.contentTypes.defaults[ext]
}

// PartsByContentType returns every part with the given content type, in
// archive order.
func (p *Package) PartsByContentType(ct string) []string {
	var out []string
	for _, name := range p.Parts() {
		if p.ContentType(name) == ct {
			out = append(out, name)
		}
	}
	return out
}

// ── relationships ────────────────────────────────────────────────────

type xmlRels struct {
	Rels []struct {
		ID         string `xml:"Id,attr"`
		Type       string `xml:"Type,attr"`
		Target     string `xml:"Target,attr"`
		TargetMode string `xml:"TargetMode,attr"`
	} `xml:"Relationship"`
}

// relsPartFor returns the _rels part name holding a part's relationships.
// The package-level relationships live in "_rels/.rels".
func relsPartFor(part string) string {
	if part == "" {
		return "_rels/.rels"
	}
	dir, file := path.Split(normalize(part))
	return path.Join(dir, "_rels", file+".rels")
}

// Rels returns the relationships declared by a part. Pass "" for the
// package-level relationships. A part with no _rels file yields an empty set
// rather than an error, which is the common case.
func (p *Package) Rels(part string) Relationships {
	key := normalize(part)
	if cached, ok := p.relCache[key]; ok {
		return cached
	}

	out := Relationships{}
	relsPart := relsPartFor(part)
	if p.Has(relsPart) {
		var x xmlRels
		if err := p.UnmarshalPart(relsPart, &x); err == nil {
			for _, r := range x.Rels {
				out[r.ID] = Relationship{
					ID:         r.ID,
					Type:       r.Type,
					Target:     r.Target,
					External:   strings.EqualFold(r.TargetMode, "External"),
					SourcePart: key,
				}
			}
		}
	}
	p.relCache[key] = out
	return out
}

// Resolve turns a relationship's Target into an absolute part name. External
// targets are returned unchanged, since they are URIs rather than parts.
func (r Relationship) Resolve() string {
	if r.External {
		return r.Target
	}
	target := strings.ReplaceAll(r.Target, "\\", "/")
	if strings.HasPrefix(target, "/") {
		return normalize(target)
	}
	// Targets are relative to the directory of the part that declared them.
	base := path.Dir(r.SourcePart)
	if base == "." || r.SourcePart == "" {
		return normalize(target)
	}
	return normalize(path.Join(base, target))
}

// MainDocument returns the package's primary part, found by following the
// officeDocument relationship from the package root.
func (p *Package) MainDocument() (string, error) {
	for _, rel := range p.Rels("").ByType(RelOfficeDocument) {
		if target := rel.Resolve(); p.Has(target) {
			return target, nil
		}
	}
	// Some producers omit the relationship; fall back to the conventional
	// locations for each format.
	for _, guess := range []string{
		"word/document.xml",
		"xl/workbook.xml",
		"ppt/presentation.xml",
	} {
		if p.Has(guess) {
			return guess, nil
		}
	}
	return "", fmt.Errorf("%w: no office document part", ErrNotOOXML)
}

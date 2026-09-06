// Command gen produces the static name tables used by package font:
//
//   - agl_table.go        Adobe Glyph List, from pdf-inspector's glyph_names.rs
//   - cff_strings.go      CFF standard strings (SID 0-390), from ttf-parser
//   - mac_names.go        Macintosh standard glyph order, from ttf-parser
//   - encodings.go        PDF base encodings (Annex D), from lopdf
//
// The generated files are committed, so building package font needs neither
// the Rust sources nor this tool. Re-run only when upstream tables change:
//
//	go run ./internal/pdf/font/gen \
//	  -rust  ../pdf-inspector/src/glyph_names.rs \
//	  -crate ~/.cargo/registry/src/<hash>/ttf-parser-0.25.1 \
//	  -lopdf ~/.cargo/registry/src/<hash>/lopdf-0.41.0 \
//	  -out   internal/pdf/font
package main

import (
	"bufio"
	"flag"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// writeGo formats src as Go source and writes it to path.
func writeGo(path, src string) error {
	out, err := format.Source([]byte(src))
	if err != nil {
		return fmt.Errorf("format %s: %w", path, err)
	}
	return os.WriteFile(path, out, 0o644)
}

// Crate versions the tables are extracted from. Keep in sync with
// pdf-inspector's Cargo.toml and with Taskfile.yml.
const (
	ttfParserVersion = "0.25.1"
	lopdfVersion     = "0.41.0"
)

var (
	rustPath  = flag.String("rust", "", "path to pdf-inspector src/glyph_names.rs (default: search common locations)")
	cratePath = flag.String("crate", "", "path to the ttf-parser crate source root (default: search the cargo registry)")
	lopdfPath = flag.String("lopdf", "", "path to the lopdf crate source root (default: search the cargo registry)")
	outDir    = flag.String("out", ".", "directory to write generated Go files into")
)

// baseEncodings names the PDF Annex D encodings extracted from lopdf, paired
// with the Go identifier each table gets.
var baseEncodings = []struct{ rustConst, goName, doc string }{
	{"STANDARD_ENCODING", "standardEncoding", "StandardEncoding, the built-in encoding for most Type 1 fonts."},
	{"WIN_ANSI_ENCODING", "winAnsiEncoding", "WinAnsiEncoding, the Windows code page 1252 superset."},
	{"MAC_ROMAN_ENCODING", "macRomanEncoding", "MacRomanEncoding, the Mac OS standard Roman character set."},
	{"PDF_DOC_ENCODING", "pdfDocEncoding", "PDFDocEncoding, used for text strings outside content streams."},
	{"SYMBOL_ENCODING", "symbolEncoding", "The built-in encoding of the Symbol font."},
	{"MAC_EXPERT_ENCODING", "macExpertEncoding", "MacExpertEncoding, for expert (small-caps and old-style) sets."},
}

func main() {
	flag.Parse()

	// Both inputs are discoverable, which keeps the go:generate directive free
	// of machine-specific paths.
	if *rustPath == "" {
		p, err := findGlyphNames()
		check(err)
		*rustPath = p
	}
	if *cratePath == "" {
		p, err := findTTFParser()
		check(err)
		*cratePath = p
	}
	if *lopdfPath == "" {
		p, err := findCrate("lopdf", lopdfVersion)
		check(err)
		*lopdfPath = p
	}
	fmt.Printf("agl source:   %s\ncrate source: %s\nlopdf source: %s\n",
		*rustPath, *cratePath, *lopdfPath)

	agl, err := parseAGL(*rustPath)
	check(err)
	check(writeAGL(filepath.Join(*outDir, "agl_table.go"), agl))
	fmt.Printf("agl_table.go:   %d entries\n", len(agl))

	std, err := parseRustStrList(
		filepath.Join(*cratePath, "src/tables/cff/std_names.rs"), "STANDARD_NAMES")
	check(err)
	check(writeStrList(filepath.Join(*outDir, "cff_strings.go"),
		"cffStandardStrings",
		"cffStandardStrings holds the CFF standard strings, SID 0 through 390.\n// A charset SID below len(cffStandardStrings) names a glyph directly; higher\n// SIDs index the font's own String INDEX.",
		std))
	fmt.Printf("cff_strings.go: %d entries\n", len(std))

	mac, err := parseRustStrList(
		filepath.Join(*cratePath, "src/tables/post.rs"), "MACINTOSH_NAMES")
	check(err)
	check(writeStrList(filepath.Join(*outDir, "mac_names.go"),
		"macGlyphNames",
		"macGlyphNames is the Macintosh standard glyph order. A post format 2.0\n// name index below len(macGlyphNames) resolves here; higher indices refer to\n// the table's own embedded Pascal strings.",
		mac))
	fmt.Printf("mac_names.go:   %d entries\n", len(mac))

	encSrc := filepath.Join(*lopdfPath, "src/encodings/mappings.rs")
	tables := make([]encodingTable, 0, len(baseEncodings))
	for _, e := range baseEncodings {
		names, err := parseEncodingTable(encSrc, e.rustConst)
		check(err)
		tables = append(tables, encodingTable{goName: e.goName, doc: e.doc, names: names})
	}
	check(writeEncodings(filepath.Join(*outDir, "encodings.go"), tables))
	fmt.Printf("encodings.go:   %d tables\n", len(tables))
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
}

// findGlyphNames locates pdf-inspector's glyph_names.rs relative to this repo.
func findGlyphNames() (string, error) {
	candidates := []string{
		"../pdf-inspector/src/glyph_names.rs",
		"../../pdf-inspector/src/glyph_names.rs",
		"../../../../pdf-inspector/src/glyph_names.rs", // go generate in internal/pdf/font
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filepath.Join(home, "Projects/pdf-inspector/src/glyph_names.rs"))
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return "", fmt.Errorf("glyph_names.rs not found; pass -rust explicitly (tried %v)", candidates)
}

// findTTFParser locates the vendored ttf-parser crate in the cargo registry.
// The registry directory carries a content hash, so it must be globbed.
func findTTFParser() (string, error) {
	return findCrate("ttf-parser", ttfParserVersion)
}

// findCrate locates a vendored crate in the cargo registry. The registry
// directory carries a content hash, so it must be globbed.
func findCrate(name, version string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	pattern := filepath.Join(home, ".cargo/registry/src/*/"+name+"-"+version)
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("%s %s not in the cargo registry; "+
			"run 'cargo fetch' in the pdf-inspector checkout, or pass the path explicitly",
			name, version)
	}
	return matches[0], nil
}

// encodingTable is one generated 256-entry code-to-glyph-name table.
type encodingTable struct {
	goName string
	doc    string
	names  []string
}

var encEntryRe = regexp.MustCompile(`\bNone\b|\bSome\(Glyph::([A-Za-z0-9_]+)\)`)

// parseEncodingTable extracts a `pub const NAME: CodedCharacterSet = [ … ];`
// block from lopdf's mappings.rs into 256 glyph names, with unmapped codes
// left as the empty string.
func parseEncodingTable(path, constName string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	src := string(data)

	i := strings.Index(src, "const "+constName)
	if i < 0 {
		return nil, fmt.Errorf("%s not found in %s", constName, path)
	}
	start := strings.Index(src[i:], "[")
	if start < 0 {
		return nil, fmt.Errorf("no opening bracket after %s", constName)
	}
	start += i
	end := strings.Index(src[start:], "];")
	if end < 0 {
		return nil, fmt.Errorf("no closing bracket for %s", constName)
	}

	var out []string
	for _, m := range encEntryRe.FindAllStringSubmatch(src[start:start+end], -1) {
		out = append(out, m[1]) // empty for the None alternative
	}
	if len(out) != 256 {
		return nil, fmt.Errorf("%s: got %d entries, want 256", constName, len(out))
	}
	return out, nil
}

// writeEncodings emits the base encoding tables as Go arrays.
func writeEncodings(path string, tables []encodingTable) error {
	var b strings.Builder
	b.WriteString(header)
	b.WriteString("// encoding is a PDF base encoding: a character code indexes it to yield a\n")
	b.WriteString("// glyph name, or the empty string where the code is unmapped.\n")
	b.WriteString("type encoding = [256]string\n\n")

	for _, t := range tables {
		fmt.Fprintf(&b, "// %s is %s\nvar %s = encoding{\n", t.goName, t.doc, t.goName)
		for code, name := range t.names {
			if name == "" {
				continue
			}
			fmt.Fprintf(&b, "\t%d: %q,\n", code, name)
		}
		b.WriteString("}\n\n")
	}
	return writeGo(path, b.String())
}

type aglEntry struct {
	name string
	r    rune
}

var insertRe = regexp.MustCompile(`m\.insert\("((?:[^"\\]|\\.)*)",\s*'((?:[^'\\]|\\.)*)'\s*\)`)

// parseAGL reads the `m.insert("name", 'char');` lines out of glyph_names.rs.
// Later inserts overwrite earlier ones, matching the Rust HashMap build order
// so the file's trailing local overrides win.
func parseAGL(path string) ([]aglEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	index := map[string]int{}
	var out []aglEntry

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for sc.Scan() {
		m := insertRe.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		name, err := unquoteRustStr(m[1])
		if err != nil {
			return nil, fmt.Errorf("name %q: %w", m[1], err)
		}
		r, err := unquoteRustChar(m[2])
		if err != nil {
			return nil, fmt.Errorf("char for %q: %w", name, err)
		}
		if i, ok := index[name]; ok {
			out[i].r = r
			continue
		}
		index[name] = len(out)
		out = append(out, aglEntry{name, r})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no entries parsed from %s", path)
	}
	return out, nil
}

// parseRustStrList extracts a `const NAME: &[&str] = &[ "a", "b", ... ];` block.
func parseRustStrList(path, constName string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	src := string(data)

	i := strings.Index(src, constName)
	if i < 0 {
		return nil, fmt.Errorf("%s not found in %s", constName, path)
	}
	start := strings.Index(src[i:], "[")
	if start < 0 {
		return nil, fmt.Errorf("no opening bracket after %s", constName)
	}
	start += i
	end := strings.Index(src[start:], "];")
	if end < 0 {
		return nil, fmt.Errorf("no closing bracket for %s", constName)
	}

	strRe := regexp.MustCompile(`"((?:[^"\\]|\\.)*)"`)
	var out []string
	for _, m := range strRe.FindAllStringSubmatch(src[start:start+end], -1) {
		s, err := unquoteRustStr(m[1])
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no strings parsed for %s", constName)
	}
	return out, nil
}

// unquoteRustStr resolves Rust string escapes.
func unquoteRustStr(s string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '\\' {
			b.WriteByte(s[i])
			i++
			continue
		}
		r, n, err := decodeEscape(s[i:])
		if err != nil {
			return "", err
		}
		b.WriteRune(r)
		i += n
	}
	return b.String(), nil
}

// unquoteRustChar resolves a Rust char literal body to a single rune.
func unquoteRustChar(s string) (rune, error) {
	if s == "" {
		return 0, fmt.Errorf("empty char literal")
	}
	if s[0] != '\\' {
		rs := []rune(s)
		if len(rs) != 1 {
			return 0, fmt.Errorf("expected one rune, got %d in %q", len(rs), s)
		}
		return rs[0], nil
	}
	r, n, err := decodeEscape(s)
	if err != nil {
		return 0, err
	}
	if n != len(s) {
		return 0, fmt.Errorf("trailing data after escape in %q", s)
	}
	return r, nil
}

// decodeEscape decodes one Rust escape sequence, returning the rune and the
// number of bytes consumed.
func decodeEscape(s string) (rune, int, error) {
	if len(s) < 2 {
		return 0, 0, fmt.Errorf("truncated escape %q", s)
	}
	switch s[1] {
	case 'n':
		return '\n', 2, nil
	case 'r':
		return '\r', 2, nil
	case 't':
		return '\t', 2, nil
	case '0':
		return 0, 2, nil
	case '\\':
		return '\\', 2, nil
	case '\'':
		return '\'', 2, nil
	case '"':
		return '"', 2, nil
	case 'x':
		if len(s) < 4 {
			return 0, 0, fmt.Errorf("truncated \\x escape %q", s)
		}
		v, err := strconv.ParseUint(s[2:4], 16, 8)
		if err != nil {
			return 0, 0, err
		}
		return rune(v), 4, nil
	case 'u':
		if len(s) < 4 || s[2] != '{' {
			return 0, 0, fmt.Errorf("malformed \\u escape %q", s)
		}
		end := strings.IndexByte(s, '}')
		if end < 0 {
			return 0, 0, fmt.Errorf("unterminated \\u escape %q", s)
		}
		v, err := strconv.ParseUint(s[3:end], 16, 32)
		if err != nil {
			return 0, 0, err
		}
		return rune(v), end + 1, nil
	}
	return 0, 0, fmt.Errorf("unknown escape %q", s[:2])
}

const header = "// Code generated by internal/pdf/font/gen. DO NOT EDIT.\n\npackage font\n\n"

func writeAGL(path string, entries []aglEntry) error {
	var b strings.Builder
	b.WriteString(header)
	b.WriteString("// aglTable maps Adobe Glyph List names to their Unicode values.\n")
	b.WriteString("var aglTable = map[string]rune{\n")
	for _, e := range entries {
		fmt.Fprintf(&b, "\t%q: %s,\n", e.name, runeLit(e.r))
	}
	b.WriteString("}\n")
	return writeGo(path, b.String())
}

func writeStrList(path, varName, doc string, items []string) error {
	var b strings.Builder
	b.WriteString(header)
	fmt.Fprintf(&b, "// %s\nvar %s = [...]string{\n", doc, varName)
	for _, s := range items {
		fmt.Fprintf(&b, "\t%q,\n", s)
	}
	b.WriteString("}\n")
	return writeGo(path, b.String())
}

// runeLit formats a rune as a Go literal, preferring a readable quoted form
// and falling back to a numeric literal for unprintable or surrogate values.
func runeLit(r rune) string {
	if r >= 0x20 && r < 0x7F && r != '\'' && r != '\\' {
		return "'" + string(r) + "'"
	}
	return fmt.Sprintf("0x%04X", r)
}

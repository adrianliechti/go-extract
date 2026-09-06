package font_test

// This is an external test package so the pdfcpu dependency needed to pull
// font programs out of the fixture PDFs stays a test-only concern.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrianliechti/go-extract/internal/pdf/font"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

const fixtureDir = "../testdata/fixtures"

// fixturePasswords maps fixtures that need a password to open.
var fixturePasswords = map[string]string{
	"encrypted-secret123.pdf": "secret123",
}

type program struct {
	fixture string
	key     string // FontFile, FontFile2 or FontFile3
	data    []byte
}

// collectPrograms extracts every distinct embedded font program in the corpus.
func collectPrograms(t *testing.T) []program {
	t.Helper()

	entries, err := os.ReadDir(fixtureDir)
	if err != nil {
		t.Skipf("fixtures unavailable: %v", err)
	}

	var out []program
	seen := map[string]bool{}

	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".pdf") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(fixtureDir, e.Name()))
		if err != nil {
			continue
		}

		conf := model.NewDefaultConfiguration()
		conf.ValidationMode = model.ValidationRelaxed
		conf.UserPW = fixturePasswords[e.Name()]

		ctx, err := pdfcpu.Read(bytes.NewReader(data), conf)
		if err != nil || ctx.XRefTable.EnsurePageCount() != nil {
			continue
		}
		xt := ctx.XRefTable

		for i := 1; i <= xt.PageCount; i++ {
			d, _, _, err := xt.PageDict(i, false)
			if err != nil || d == nil {
				continue
			}
			res, _ := xt.DereferenceDict(d["Resources"])
			if res == nil {
				continue
			}
			fonts, _ := xt.DereferenceDict(res["Font"])
			for _, fv := range fonts {
				fd, _ := xt.DereferenceDict(fv)
				if fd == nil {
					continue
				}
				for _, desc := range descriptors(xt, fd) {
					for _, key := range []string{"FontFile", "FontFile2", "FontFile3"} {
						sd, _, err := xt.DereferenceStreamDict(desc[key])
						if err != nil || sd == nil {
							continue
						}
						if err := sd.Decode(); err != nil || len(sd.Content) == 0 {
							continue
						}
						sig := key + ":" + string(sd.Content[:min(32, len(sd.Content))])
						if seen[sig] {
							continue
						}
						seen[sig] = true
						out = append(out, program{e.Name(), key, sd.Content})
					}
				}
			}
		}
	}
	return out
}

func descriptors(xt *model.XRefTable, fontDict types.Dict) []types.Dict {
	var out []types.Dict
	if fd, _ := xt.DereferenceDict(fontDict["FontDescriptor"]); fd != nil {
		out = append(out, fd)
	}
	// CIDFontType0/2 keep the descriptor under DescendantFonts.
	if arr, _ := xt.DereferenceArray(fontDict["DescendantFonts"]); arr != nil {
		for _, e := range arr {
			df, _ := xt.DereferenceDict(e)
			if df == nil {
				continue
			}
			if fd, _ := xt.DereferenceDict(df["FontDescriptor"]); fd != nil {
				out = append(out, fd)
			}
		}
	}
	return out
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestCorpusFontsParse checks that every embedded font program in the fixture
// corpus parses, and reports the aggregate cmap/glyph-name coverage achieved.
func TestCorpusFontsParse(t *testing.T) {
	programs := collectPrograms(t)
	if len(programs) == 0 {
		t.Skip("no font programs found in the corpus")
	}
	t.Logf("collected %d distinct font programs", len(programs))

	var (
		parsed, withCmap, withNames, withGIDMap int
		type1Skipped                            int
		failures                                []string
	)

	for _, p := range programs {
		face, err := font.Parse(p.data)
		if err != nil {
			// Raw Type1 (FontFile) is a PostScript format, not sfnt or CFF;
			// it is out of scope for this package.
			if p.key == "FontFile" {
				type1Skipped++
				continue
			}
			failures = append(failures, p.fixture+" "+p.key+": "+err.Error())
			continue
		}
		parsed++

		if c := face.Cmap(); c != nil && len(c.Subtables) > 0 {
			withCmap++
		}
		if m := face.GIDToUnicode(); len(m) > 0 {
			withGIDMap++
		}
		// Sample glyph names rather than walking every glyph.
		for gid := uint16(0); gid < face.NumGlyphs() && gid < 64; gid++ {
			if n, ok := face.GlyphName(gid); ok && n != "" {
				withNames++
				break
			}
		}
	}

	t.Logf("parsed=%d cmap=%d gidmap=%d names=%d type1Skipped=%d",
		parsed, withCmap, withGIDMap, withNames, type1Skipped)

	for _, f := range failures {
		t.Errorf("parse failed: %s", f)
	}

	if parsed == 0 {
		t.Fatal("no font programs parsed")
	}
	// The corpus probe measured 64 TrueType and 15 bare-CFF programs; a
	// collapse in either capability means a regression.
	if withGIDMap == 0 {
		t.Error("no font yielded a GID->Unicode map")
	}
	if withCmap == 0 {
		t.Error("no font yielded a cmap")
	}
}

// TestCorpusCmapSubtables asserts the platform/encoding/format combinations the
// corpus probe found are all handled, and that each subtable round-trips.
func TestCorpusCmapSubtables(t *testing.T) {
	programs := collectPrograms(t)
	if len(programs) == 0 {
		t.Skip("no font programs found in the corpus")
	}

	type key struct{ pid, eid, format uint16 }
	counts := map[key]int{}
	roundTripped := 0

	for _, p := range programs {
		face, err := font.Parse(p.data)
		if err != nil {
			continue
		}
		c := face.Cmap()
		if c == nil {
			continue
		}
		for _, sub := range c.Subtables {
			counts[key{sub.PlatformID, sub.EncodingID, sub.Format}]++

			// Every codepoint the subtable enumerates must resolve back to a
			// non-zero glyph through GlyphIndex. Format 14 carries variation
			// selectors and enumerates nothing.
			if sub.Format == 14 {
				continue
			}
			bad := 0
			checked := 0
			sub.Codepoints(func(cp uint32) {
				if checked >= 512 {
					return
				}
				checked++
				if _, ok := sub.GlyphIndex(cp); !ok {
					bad++
				}
			})
			if bad > 0 {
				t.Errorf("%s: pid=%d eid=%d fmt=%d: %d/%d enumerated codepoints did not resolve",
					p.fixture, sub.PlatformID, sub.EncodingID, sub.Format, bad, checked)
			}
			if checked > 0 {
				roundTripped++
			}
		}
	}

	for k, n := range counts {
		t.Logf("pid=%-2d eid=%-2d format=%-3d x%d", k.pid, k.eid, k.format, n)
	}
	if roundTripped == 0 {
		t.Error("no cmap subtable produced any codepoints")
	}

	// Formats the corpus probe reported. Their absence means the fixtures or
	// the parser changed materially.
	for _, want := range []key{
		{0, 3, 4},   // Unicode BMP
		{1, 0, 0},   // Macintosh Roman, byte encoding
		{3, 1, 4},   // Windows UCS-2
		{3, 0, 4},   // Windows Symbol
		{3, 10, 12}, // Windows UCS-4
	} {
		if counts[want] == 0 {
			t.Errorf("expected at least one pid=%d eid=%d format=%d subtable",
				want.pid, want.eid, want.format)
		}
	}
}

// Command fontprobe enumerates every embedded font program in the fixture
// corpus and reports the sfnt/CFF features a replacement for ttf-parser must
// support: cmap subtable (platformID, encodingID, format) combinations, post
// table versions, and bare-CFF (FontFile3) prevalence.
//
// Throwaway: its output scopes internal/pdf/font, then it gets deleted.
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

type cmapKey struct{ pid, eid, format uint16 }

var (
	cmapHits  = map[cmapKey]int{}
	postVer   = map[string]int{}
	kindCount = map[string]int{}
	sfntTags  = map[string]int{}
	failures  = map[string]int{}
)

func main() {
	entries, err := os.ReadDir("internal/pdf/testdata/fixtures")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".pdf") {
			scan(filepath.Join("internal/pdf/testdata/fixtures", e.Name()))
		}
	}

	report("cmap subtables (platformID, encodingID, format)", func() []string {
		var out []string
		for k, n := range cmapHits {
			out = append(out, fmt.Sprintf("  pid=%-2d eid=%-2d format=%-3d  x%d", k.pid, k.eid, k.format, n))
		}
		return out
	}())
	report("font program kinds", mapLines(kindCount))
	report("post table versions", mapLines(postVer))
	report("sfnt tables present", mapLines(sfntTags))
	report("parse failures", mapLines(failures))
}

func mapLines(m map[string]int) []string {
	var out []string
	for k, n := range m {
		out = append(out, fmt.Sprintf("  %-28s x%d", k, n))
	}
	return out
}

func report(title string, lines []string) {
	sort.Strings(lines)
	fmt.Printf("\n== %s ==\n", title)
	if len(lines) == 0 {
		fmt.Println("  (none)")
		return
	}
	for _, l := range lines {
		fmt.Println(l)
	}
}

func scan(path string) {
	defer func() { recover() }()

	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	conf := model.NewDefaultConfiguration()
	conf.ValidationMode = model.ValidationRelaxed
	if strings.Contains(path, "secret123") {
		conf.UserPW = "secret123"
	}
	ctx, err := pdfcpu.Read(bytes.NewReader(data), conf)
	if err != nil {
		return
	}
	xt := ctx.XRefTable
	if err := xt.EnsurePageCount(); err != nil {
		return
	}

	seen := map[string]bool{}
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
					// Dedupe identical programs shared across pages.
					sig := fmt.Sprintf("%s:%d:%x", key, len(sd.Content), sd.Content[:min(16, len(sd.Content))])
					if seen[sig] {
						continue
					}
					seen[sig] = true
					inspect(key, sd.Content)
				}
			}
		}
	}
}

func descriptors(xt *model.XRefTable, font types.Dict) []types.Dict {
	var out []types.Dict
	if fd, _ := xt.DereferenceDict(font["FontDescriptor"]); fd != nil {
		out = append(out, fd)
	}
	if arr, _ := xt.DereferenceArray(font["DescendantFonts"]); arr != nil {
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

func inspect(key string, b []byte) {
	if len(b) < 4 {
		failures["too short"]++
		return
	}
	tag := binary.BigEndian.Uint32(b)

	switch {
	case tag == 0x00010000 || tag == 0x74727565: // TrueType outlines
		kindCount[key+" sfnt/truetype"]++
		parseSfnt(b)
	case tag == 0x4F54544F: // 'OTTO' — CFF outlines in an sfnt wrapper
		kindCount[key+" sfnt/OTTO(CFF)"]++
		parseSfnt(b)
	case tag == 0x74746366: // 'ttcf'
		kindCount[key+" ttc"]++
	case b[0] == 0x01 && b[1] == 0x00: // bare CFF: major=1 minor=0
		kindCount[key+" bare CFF"]++
	case b[0] == 0x80: // PFB segmented Type1
		kindCount[key+" Type1 PFB"]++
	case bytes.HasPrefix(b, []byte("%!")): // raw Type1
		kindCount[key+" Type1 raw"]++
	default:
		kindCount[fmt.Sprintf("%s unknown(%08x)", key, tag)]++
	}
}

func parseSfnt(b []byte) {
	if len(b) < 12 {
		failures["sfnt header short"]++
		return
	}
	numTables := int(binary.BigEndian.Uint16(b[4:]))
	tables := map[string][]byte{}
	for i := 0; i < numTables; i++ {
		rec := 12 + i*16
		if rec+16 > len(b) {
			failures["sfnt table dir truncated"]++
			return
		}
		name := string(b[rec : rec+4])
		off := int(binary.BigEndian.Uint32(b[rec+8:]))
		ln := int(binary.BigEndian.Uint32(b[rec+12:]))
		if off < 0 || ln < 0 || off+ln > len(b) {
			failures["sfnt table out of range: "+name]++
			continue
		}
		tables[name] = b[off : off+ln]
		sfntTags[name]++
	}

	if c, ok := tables["cmap"]; ok {
		parseCmap(c)
	}
	if p, ok := tables["post"]; ok && len(p) >= 4 {
		postVer[fmt.Sprintf("%d.%d", binary.BigEndian.Uint16(p), binary.BigEndian.Uint16(p[2:]))]++
	}
}

func parseCmap(c []byte) {
	if len(c) < 4 {
		failures["cmap short"]++
		return
	}
	n := int(binary.BigEndian.Uint16(c[2:]))
	for i := 0; i < n; i++ {
		rec := 4 + i*8
		if rec+8 > len(c) {
			failures["cmap record truncated"]++
			return
		}
		pid := binary.BigEndian.Uint16(c[rec:])
		eid := binary.BigEndian.Uint16(c[rec+2:])
		off := int(binary.BigEndian.Uint32(c[rec+4:]))
		if off+2 > len(c) {
			failures["cmap subtable out of range"]++
			continue
		}
		cmapHits[cmapKey{pid, eid, binary.BigEndian.Uint16(c[off:])}]++
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

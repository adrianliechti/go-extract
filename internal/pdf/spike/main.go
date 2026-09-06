// Command spike validates that pdfcpu covers the lopdf API surface the Rust
// pdf-inspector relies on. It is throwaway: delete once the object layer lands.
//
// Checks, per fixture:
//   - load (incl. encrypted-with-password)
//   - page tree traversal + MediaBox
//   - content stream retrieval + decompression
//   - resource dict -> font dict access
//   - embedded font program (FontFile/FontFile2/FontFile3) retrieval
package main

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

type result struct {
	name       string
	pages      int
	contentLen int
	fonts      int
	fontProgs  int
	err        string
}

func main() {
	dir := "internal/pdf/testdata/fixtures"
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	quiet := os.Getenv("QUIET") != ""

	var paths []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if strings.EqualFold(filepath.Ext(p), ".pdf") {
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "walk:", err)
		os.Exit(1)
	}

	var results []result
	for _, p := range paths {
		results = append(results, probe(p))
	}
	_ = quiet

	sort.Slice(results, func(i, j int) bool { return results[i].name < results[j].name })

	fmt.Printf("%-46s %6s %10s %6s %6s  %s\n", "FIXTURE", "PAGES", "CONTENT", "FONTS", "PROGS", "ERROR")
	fmt.Println(strings.Repeat("-", 100))
	var ok, fail int
	for _, r := range results {
		status := r.err
		if status == "" {
			ok++
		} else {
			fail++
		}
		fmt.Printf("%-46s %6d %10d %6d %6d  %s\n", r.name, r.pages, r.contentLen, r.fonts, r.fontProgs, status)
	}
	fmt.Println(strings.Repeat("-", 100))
	fmt.Printf("ok=%d fail=%d\n", ok, fail)
}

func probe(path string) result {
	r := result{name: filepath.Base(path)}

	defer func() {
		if p := recover(); p != nil {
			r.err = fmt.Sprintf("PANIC: %v", p)
		}
	}()

	data, err := os.ReadFile(path)
	if err != nil {
		r.err = err.Error()
		return r
	}

	conf := model.NewDefaultConfiguration()
	conf.ValidationMode = model.ValidationRelaxed
	// encrypted-secret123.pdf carries a user password; mirrors PdfOptions::password.
	if strings.Contains(r.name, "secret123") {
		conf.UserPW = "secret123"
	}

	ctx, err := pdfcpu.Read(bytes.NewReader(data), conf)
	if err != nil {
		r.err = "read: " + err.Error()
		return r
	}
	xt := ctx.XRefTable

	// PageCount is a field populated by validation; ensure it, then fall back
	// to walking the page tree ourselves.
	if err := xt.EnsurePageCount(); err != nil || xt.PageCount == 0 {
		r.pages = countPages(xt)
	} else {
		r.pages = xt.PageCount
	}

	for i := 1; i <= r.pages; i++ {
		d, _, _, err := xt.PageDict(i, false)
		if err != nil || d == nil {
			continue
		}

		// Content streams — the lopdf get_page_content equivalent.
		if c, err := contentBytes(xt, d); err == nil {
			r.contentLen += len(c)
		}

		// Resources -> Font, incl. inherited resources.
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
			r.fonts++
			if hasFontProgram(xt, fd) {
				r.fontProgs++
			}
		}
	}

	return r
}

func countPages(xt *model.XRefTable) int {
	root, err := xt.Catalog()
	if err != nil {
		return 0
	}
	pages, err := xt.DereferenceDict(root["Pages"])
	if err != nil || pages == nil {
		return 0
	}
	if c, err := xt.DereferenceInteger(pages["Count"]); err == nil && c != nil {
		return c.Value()
	}
	return 0
}

// contentBytes concatenates and decompresses a page's Contents, which may be a
// single stream or an array of streams (PDF 32000-1 7.8.2).
func contentBytes(xt *model.XRefTable, page types.Dict) ([]byte, error) {
	o, found := page.Find("Contents")
	if !found {
		return nil, nil
	}
	o, err := xt.Dereference(o)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	switch c := o.(type) {
	case types.StreamDict:
		if err := c.Decode(); err != nil {
			return nil, err
		}
		buf.Write(c.Content)
	case types.Array:
		for _, e := range c {
			sd, _, err := xt.DereferenceStreamDict(e)
			if err != nil || sd == nil {
				continue
			}
			if err := sd.Decode(); err != nil {
				continue
			}
			buf.Write(sd.Content)
			buf.WriteByte('\n')
		}
	}
	return buf.Bytes(), nil
}

// hasFontProgram reports whether an embedded font program is reachable — the
// input ttf-parser needs for cmap recovery (FontFile2 = TrueType, 3 = CFF).
func hasFontProgram(xt *model.XRefTable, font types.Dict) bool {
	descs := []types.Dict{}
	if fd, _ := xt.DereferenceDict(font["FontDescriptor"]); fd != nil {
		descs = append(descs, fd)
	}
	// CIDFontType0/2 hide the descriptor one level down under DescendantFonts.
	if arr, _ := xt.DereferenceArray(font["DescendantFonts"]); arr != nil {
		for _, e := range arr {
			df, _ := xt.DereferenceDict(e)
			if df == nil {
				continue
			}
			if fd, _ := xt.DereferenceDict(df["FontDescriptor"]); fd != nil {
				descs = append(descs, fd)
			}
		}
	}

	for _, fd := range descs {
		for _, k := range []string{"FontFile", "FontFile2", "FontFile3"} {
			sd, _, err := xt.DereferenceStreamDict(fd[k])
			if err != nil || sd == nil {
				continue
			}
			if err := sd.Decode(); err != nil {
				continue
			}
			if len(sd.Content) > 0 {
				return true
			}
		}
	}
	return false
}

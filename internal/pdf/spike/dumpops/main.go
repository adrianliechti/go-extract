// Command dumpops prints the operator sequence for one page of a PDF, so the
// Go tokenizer can be diffed against the Rust `dump_ops` binary (which uses
// lopdf's Content::decode). Throwaway.
//
// Usage: dumpops <pdf> [page]
package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"strconv"

	"github.com/adrianliechti/go-extract/internal/pdf/content"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: dumpops <pdf> [page]")
		os.Exit(1)
	}
	page := 1
	if len(os.Args) > 2 {
		page, _ = strconv.Atoi(os.Args[2])
	}

	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	conf := model.NewDefaultConfiguration()
	conf.ValidationMode = model.ValidationRelaxed
	conf.UserPW = os.Getenv("PDFPW")
	ctx, err := pdfcpu.Read(bytes.NewReader(data), conf)
	if err != nil {
		fmt.Fprintln(os.Stderr, "read:", err)
		os.Exit(1)
	}
	xt := ctx.XRefTable
	_ = xt.EnsurePageCount()

	d, _, _, err := xt.PageDict(page, false)
	if err != nil || d == nil {
		fmt.Fprintln(os.Stderr, "page dict:", err)
		os.Exit(1)
	}

	raw, err := contentBytes(xt, d)
	if err != nil {
		fmt.Fprintln(os.Stderr, "content:", err)
		os.Exit(1)
	}

	ops, err := content.Decode(raw)
	if err != nil {
		fmt.Fprintln(os.Stderr, "decode:", err)
	}

	w := bufio.NewWriter(os.Stdout)
	defer w.Flush()
	for _, op := range ops {
		fmt.Fprintln(w, op.Operator)
	}
}

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

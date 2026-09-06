// Command ooxml2md converts a Word, Excel or PowerPoint document to Markdown.
//
//	ooxml2md [flags] <file.docx|file.xlsx|file.pptx>
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/adrianliechti/go-extract/internal/ooxml"
)

func main() {
	var (
		out        = flag.String("o", "", "write Markdown to this file instead of stdout")
		imageDir   = flag.String("images", "", "extract images into this directory")
		prefix     = flag.String("image-prefix", "", "path prefix for image links (defaults to the images directory name)")
		skipImages = flag.Bool("no-images", false, "omit images entirely")
		notes      = flag.Bool("notes", false, "include PowerPoint speaker notes")
		hidden     = flag.Bool("include-hidden", false, "include hidden worksheets and slides")
		sheetNames = flag.Bool("sheet-names", false, "always emit a heading per spreadsheet sheet")
		quiet      = flag.Bool("quiet", false, "suppress the summary written to stderr")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: %s [flags] <file>\n\nflags:\n", filepath.Base(os.Args[0]))
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}

	doc, err := ooxml.ConvertFile(flag.Arg(0), ooxml.Options{
		ImageDir:      *imageDir,
		ImagePrefix:   *prefix,
		SkipImages:    *skipImages,
		SlideNotes:    *notes,
		IncludeHidden: *hidden,
		SheetNames:    *sheetNames,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	if *out != "" {
		if err := os.WriteFile(*out, []byte(doc.Markdown), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	} else {
		fmt.Print(doc.Markdown)
	}

	if !*quiet {
		fmt.Fprintf(os.Stderr, "format=%s bytes=%d images=%d",
			doc.Format, len(doc.Markdown), len(doc.Images))
		if n := len(doc.SheetNames); n > 0 {
			fmt.Fprintf(os.Stderr, " sheets=%d", n)
		}
		if doc.SlideCount > 0 {
			fmt.Fprintf(os.Stderr, " slides=%d", doc.SlideCount)
		}
		fmt.Fprintln(os.Stderr)
	}
}

// Package media collects embedded images from an OOXML package, assigning
// each a stable, unique filename and a Markdown-ready link.
package media

import (
	"path"
	"strconv"
	"strings"

	"github.com/adrianliechti/go-extract/internal/ooxml/model"
	"github.com/adrianliechti/go-extract/internal/ooxml/opc"
)

// Collector gathers images, de-duplicating by source part so an image reused
// across a document is extracted once.
type Collector struct {
	pkg  *opc.Package
	opts model.Options

	byPart map[string]string // part name -> assigned filename
	used   map[string]bool   // assigned filenames, for uniqueness
	images []model.Image
}

// NewCollector returns a Collector for a package.
func NewCollector(pkg *opc.Package, opts model.Options) *Collector {
	return &Collector{
		pkg:    pkg,
		opts:   opts,
		byPart: map[string]string{},
		used:   map[string]bool{},
	}
}

// Add records the image a relationship points at and returns the filename
// assigned to it. It reports false when the relationship is not a usable
// embedded image.
func (c *Collector) Add(rel opc.Relationship) (string, bool) {
	if c.opts.SkipImages {
		return "", false
	}
	// A linked image lives outside the package; there are no bytes to extract.
	if rel.External {
		return "", false
	}

	part := rel.Resolve()
	if name, ok := c.byPart[part]; ok {
		return name, true
	}
	if !c.pkg.Has(part) {
		return "", false
	}

	data, err := c.pkg.ReadPart(part)
	if err != nil || len(data) == 0 {
		return "", false
	}

	name := c.assignName(part)
	c.byPart[part] = name
	c.images = append(c.images, model.Image{
		Name:        name,
		Part:        part,
		ContentType: c.pkg.ContentType(part),
		Data:        data,
	})
	return name, true
}

// AddPart records an image by part name, for formats that reference media
// without going through a relationship.
func (c *Collector) AddPart(part string) (string, bool) {
	return c.Add(opc.Relationship{Target: part, SourcePart: ""})
}

// assignName derives a unique filename from the part name, keeping the
// original base name where possible so output stays recognisable.
func (c *Collector) assignName(part string) string {
	base := path.Base(part)
	if base == "." || base == "/" || base == "" {
		base = "image"
	}
	if !c.used[base] {
		c.used[base] = true
		return base
	}

	ext := path.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	for i := 2; ; i++ {
		candidate := stem + "-" + strconv.Itoa(i) + ext
		if !c.used[candidate] {
			c.used[candidate] = true
			return candidate
		}
	}
}

// Link returns the Markdown link target for an assigned filename.
func (c *Collector) Link(name string) string {
	if c.opts.ImagePrefix == "" {
		return name
	}
	return c.opts.ImagePrefix + "/" + name
}

// Images returns everything collected, in first-reference order.
func (c *Collector) Images() []model.Image { return c.images }

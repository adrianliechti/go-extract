package docx

import (
	"encoding/xml"
	"fmt"
	"strings"

	"github.com/adrianliechti/go-extract/internal/ooxml/opc"
)

const compatibilityNS = "http://schemas.openxmlformats.org/markup-compatibility/2006"

type compatibilityFrame struct {
	namespaces map[string]string
	alternate  bool
	branch     bool
	selected   bool
}

// compatibilityReader presents only the selected MC branch to every Word
// parser, including struct decoders for tables, styles and numbering. It
// retains namespace scopes but never buffers another copy of the XML tree.
type compatibilityReader struct {
	decoder *xml.Decoder
	stack   []compatibilityFrame
}

func (r *compatibilityReader) Token() (xml.Token, error) {
	for {
		token, err := r.decoder.Token()
		if err != nil {
			return nil, err
		}
		switch t := token.(type) {
		case xml.StartElement:
			if len(r.stack) >= 10000 {
				return nil, fmt.Errorf("docx: XML nesting limit exceeded")
			}
			frame := compatibilityFrame{}
			for _, attr := range t.Attr {
				if attr.Name.Space == "xmlns" {
					if frame.namespaces == nil {
						frame.namespaces = map[string]string{}
					}
					frame.namespaces[attr.Name.Local] = attr.Value
				}
			}
			if len(r.stack) > 0 && r.stack[len(r.stack)-1].alternate {
				parent := &r.stack[len(r.stack)-1]
				selectBranch := false
				if !parent.selected && t.Name.Space == compatibilityNS {
					switch t.Name.Local {
					case "Choice":
						selectBranch = r.understands(t, frame)
					case "Fallback":
						selectBranch = true
					}
				}
				if !selectBranch {
					if err := r.decoder.Skip(); err != nil {
						return nil, err
					}
					continue
				}
				parent.selected, frame.branch = true, true
			}
			frame.alternate = t.Name == (xml.Name{Space: compatibilityNS, Local: "AlternateContent"})
			r.stack = append(r.stack, frame)
			if frame.alternate || frame.branch {
				continue
			}
		case xml.EndElement:
			if len(r.stack) > 0 {
				frame := r.stack[len(r.stack)-1]
				r.stack[len(r.stack)-1] = compatibilityFrame{}
				r.stack = r.stack[:len(r.stack)-1]
				if frame.alternate || frame.branch {
					continue
				}
			}
		}
		return token, nil
	}
}

func (r *compatibilityReader) understands(choice xml.StartElement, frame compatibilityFrame) bool {
	var required []string
	for _, attr := range choice.Attr {
		if attr.Name == (xml.Name{Local: "Requires"}) {
			required = strings.Fields(attr.Value)
		}
	}
	if len(required) == 0 {
		return false
	}
	for _, prefix := range required {
		namespace, bound := frame.namespaces[prefix]
		for i := len(r.stack) - 1; !bound && i >= 0; i-- {
			namespace, bound = r.stack[i].namespaces[prefix]
		}
		if !understoodNamespace(namespace) {
			return false
		}
	}
	return true
}

func understoodNamespace(namespace string) bool {
	switch namespace {
	case "http://schemas.openxmlformats.org/wordprocessingml/2006/main",
		"http://purl.oclc.org/ooxml/wordprocessingml/main",
		"http://schemas.openxmlformats.org/officeDocument/2006/math",
		"http://purl.oclc.org/ooxml/officeDocument/math",
		"http://schemas.openxmlformats.org/drawingml/2006/main",
		"http://purl.oclc.org/ooxml/drawingml/main",
		"http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing",
		"http://purl.oclc.org/ooxml/drawingml/wordprocessingDrawing",
		"http://schemas.openxmlformats.org/drawingml/2006/picture",
		"http://purl.oclc.org/ooxml/drawingml/picture",
		"http://schemas.microsoft.com/office/word/2010/wordprocessingShape",
		"http://schemas.microsoft.com/office/word/2010/wordprocessingGroup",
		"urn:schemas-microsoft-com:vml":
		return true
	}
	return false
}

func unmarshalPart(pkg *opc.Package, part string, value any) error {
	data, err := pkg.ReadPart(part)
	if err != nil {
		return err
	}
	return newDecoder(data).Decode(value)
}

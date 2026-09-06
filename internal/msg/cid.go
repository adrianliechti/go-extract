package msg

import (
	"strings"

	"golang.org/x/net/html"
)

func rewriteCIDReferences(source string, attachments []Attachment, prefix string) string {
	replacements := make(map[string]string)
	for i := range attachments {
		a := &attachments[i]
		id := strings.Trim(strings.TrimSpace(a.ContentID), "<>")
		if id != "" {
			replacements[strings.ToLower("cid:"+id)] = attachmentURL(prefix, a.Name)
		}
		if location := strings.TrimSpace(a.ContentLocation); location != "" {
			replacements[strings.ToLower(location)] = attachmentURL(prefix, a.Name)
		}
	}
	if len(replacements) == 0 {
		return source
	}
	doc, err := html.Parse(strings.NewReader(source))
	if err != nil {
		return source
	}
	var rewrite func(*html.Node)
	rewrite = func(node *html.Node) {
		if node.Type == html.ElementNode {
			for i := range node.Attr {
				switch strings.ToLower(node.Attr[i].Key) {
				case "src", "href", "poster", "background":
					if replacement, ok := replacements[strings.ToLower(strings.TrimSpace(node.Attr[i].Val))]; ok {
						node.Attr[i].Val = replacement
					}
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			rewrite(child)
		}
	}
	rewrite(doc)
	var rendered strings.Builder
	if err := html.Render(&rendered, doc); err != nil {
		return source
	}
	return rendered.String()
}

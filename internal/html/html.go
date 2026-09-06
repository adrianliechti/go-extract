// Package html converts HTML documents and fragments to Markdown.
package html

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"path/filepath"
	"strings"

	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/base"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/commonmark"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/table"
	"github.com/adrianliechti/go-extract/internal/model"
	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

var markdownConverter = converter.NewConverter(
	converter.WithPlugins(
		base.NewBasePlugin(),
		commonmark.NewCommonmarkPlugin(),
		table.NewTablePlugin(),
	),
)

// Options configures HTML conversion.
type Options struct {
	// BaseURL resolves relative links and image sources. When empty, the first
	// HTML <base href> value is used when present.
	BaseURL string

	// ContentType may include a charset parameter, for example
	// "text/html; charset=iso-8859-1". When empty, encoding is sniffed from the
	// document according to the HTML standard.
	ContentType string
}

// Document is the result of converting HTML to Markdown.
type Document struct {
	Markdown string
	Title    string
}

// Convert decodes and converts an in-memory HTML document or fragment.
func Convert(data []byte, opts Options) (*Document, error) {
	source, err := decode(data, opts.ContentType)
	if err != nil {
		return nil, err
	}
	title, documentBase := metadata(source)
	baseURL := strings.TrimSpace(opts.BaseURL)
	if baseURL == "" {
		baseURL = documentBase
	}

	var convertOptions []converter.ConvertOptionFunc
	if baseURL != "" {
		convertOptions = append(convertOptions, converter.WithDomain(baseURL))
	}
	markdown, err := markdownConverter.ConvertString(source, convertOptions...)
	if err != nil {
		return nil, fmt.Errorf("html: convert to Markdown: %w", err)
	}
	return &Document{
		Markdown: strings.TrimSpace(markdown),
		Title:    title,
	}, nil
}

// ConvertString converts a UTF-8 HTML string to Markdown.
func ConvertString(source string, opts Options) (*Document, error) {
	if opts.ContentType == "" {
		opts.ContentType = "text/html; charset=utf-8"
	}
	return Convert([]byte(source), opts)
}

// ToMarkdown converts HTML and returns only the generated Markdown.
func ToMarkdown(data []byte, opts Options) (string, error) {
	doc, err := Convert(data, opts)
	if err != nil {
		return "", err
	}
	return doc.Markdown, nil
}

// Detect reports whether data contains recognizable HTML markup.
func Detect(data []byte) bool {
	if len(bytes.TrimSpace(data)) == 0 {
		return false
	}
	tokenizer := html.NewTokenizer(bytes.NewReader(data))
	for tokens := 0; tokens < 128; tokens++ {
		switch tokenizer.Next() {
		case html.ErrorToken:
			return false
		case html.DoctypeToken:
			if strings.EqualFold(strings.TrimSpace(string(tokenizer.Text())), "html") {
				return true
			}
		case html.StartTagToken, html.SelfClosingTagToken:
			name, _ := tokenizer.TagName()
			if isHTMLTag(strings.ToLower(string(name))) {
				return true
			}
		}
	}
	return false
}

func isHTMLTag(name string) bool {
	switch name {
	case "html", "head", "body", "p", "div", "span", "main", "article", "section", "nav",
		"h1", "h2", "h3", "h4", "h5", "h6", "a", "img", "table", "thead", "tbody",
		"tr", "th", "td", "ul", "ol", "li", "blockquote", "pre", "code", "strong", "em", "br", "hr":
		return true
	default:
		return false
	}
}

func decode(data []byte, contentType string) (string, error) {
	if contentType == "" {
		contentType = "text/html"
	}
	reader, err := charset.NewReader(bytes.NewReader(data), contentType)
	if err != nil {
		return "", fmt.Errorf("html: decode: %w", err)
	}
	decoded, err := io.ReadAll(reader)
	if err != nil {
		return "", fmt.Errorf("html: decode: %w", err)
	}
	return strings.TrimPrefix(string(decoded), "\ufeff"), nil
}

func metadata(source string) (title, baseURL string) {
	doc, err := html.Parse(strings.NewReader(source))
	if err != nil {
		return "", ""
	}
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode {
			switch strings.ToLower(node.Data) {
			case "title":
				if title == "" {
					title = strings.Join(strings.Fields(nodeText(node)), " ")
				}
			case "base":
				if baseURL == "" {
					for _, attr := range node.Attr {
						if strings.EqualFold(attr.Key, "href") {
							baseURL = strings.TrimSpace(attr.Val)
							break
						}
					}
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	return title, baseURL
}

func nodeText(node *html.Node) string {
	var text strings.Builder
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		if current.Type == html.TextNode {
			text.WriteString(current.Data)
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return text.String()
}

// Extractor adapts HTML conversion to the unified extraction interface.
type Extractor struct {
	Options Options
}

// NewExtractor returns a unified HTML extractor.
func NewExtractor(opts Options) *Extractor {
	return &Extractor{Options: opts}
}

// Supports reports whether input is HTML based on its markup or text-format
// hints. A media type or .html-style extension permits plain HTML fragments.
func (e *Extractor) Supports(input model.Input) bool {
	if Detect(input.Data) {
		return true
	}
	mediaType, _, _ := mime.ParseMediaType(input.MediaType)
	if strings.EqualFold(mediaType, "text/html") || strings.EqualFold(mediaType, "application/xhtml+xml") {
		return len(input.Data) > 0
	}
	switch strings.ToLower(filepath.Ext(input.Name)) {
	case ".html", ".htm", ".xhtml":
		return len(input.Data) > 0
	default:
		return false
	}
}

// Extract converts HTML into a format-neutral document.
func (e *Extractor) Extract(ctx context.Context, input model.Input) (*model.Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	opts := e.Options
	if input.MediaType != "" {
		opts.ContentType = input.MediaType
	}
	doc, err := Convert(input.Data, opts)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	metadata := make(map[string]string)
	if doc.Title != "" {
		metadata["title"] = doc.Title
	}
	return &model.Document{
		Name:      input.Name,
		Format:    model.FormatHTML,
		MediaType: "text/html",
		Markdown:  doc.Markdown,
		Metadata:  metadata,
	}, nil
}

var _ model.Extractor = (*Extractor)(nil)

package web

import (
	"bytes"
	"html"
	"html/template"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
)

type assistantMessage struct {
	Role, Text, URL string
	HTML            template.HTML
}

// Only this renderer may promote assistant text to trusted template HTML.
// Goldmark's safe defaults omit raw HTML and dangerous link destinations.
func assistantMarkdown(value string) template.HTML {
	source := []byte(value)
	md := goldmark.New(goldmark.WithExtensions(extension.GFM))
	document := md.Parser().Parse(text.NewReader(source))
	images := []*ast.Image{}
	_ = ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			switch n := node.(type) {
			case *ast.Heading:
				n.Level = min(6, n.Level+1) // Keep the page's sole h1.
			case *ast.Image:
				images = append(images, n)
			}
		}
		return ast.WalkContinue, nil
	})
	// Render image descriptions as text rather than loading model-supplied URLs.
	for _, image := range images {
		image.Parent().ReplaceChild(image.Parent(), image, ast.NewString(image.Text(source)))
	}
	var out bytes.Buffer
	if err := md.Renderer().Render(&out, source, document); err != nil {
		return template.HTML("<p>" + html.EscapeString(value) + "</p>")
	}
	return template.HTML(out.String())
}

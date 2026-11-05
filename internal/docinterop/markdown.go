package docinterop

import (
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
)

// ParseMarkdown parses a document body into a Goldmark AST with GFM enabled.
func ParseMarkdown(body string) ast.Node {
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
	)
	return md.Parser().Parse(text.NewReader([]byte(body)))
}

// MarkdownBuilder helps construct canonical Markdown strings.
type MarkdownBuilder struct {
	strings.Builder
}

func (b *MarkdownBuilder) WriteParagraph(s string) {
	if b.Len() > 0 {
		b.WriteString("\n\n")
	}
	b.WriteString(s)
}

func (b *MarkdownBuilder) WriteHeading(level int, text string, anchor string) {
	if b.Len() > 0 {
		b.WriteString("\n\n")
	}
	for i := 0; i < level; i++ {
		b.WriteByte('#')
	}
	b.WriteByte(' ')
	b.WriteString(text)
	if anchor != "" {
		b.WriteString(" {#")
		b.WriteString(anchor)
		b.WriteString("}")
	}
}

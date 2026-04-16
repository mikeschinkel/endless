package markdown

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strconv"

	"github.com/a-h/templ"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// MarkdownContent parses the given markdown source and returns a templ.Component
// that renders each AST node as a styled templ component.
func MarkdownContent(source string) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		src := []byte(source)
		p := goldmark.DefaultParser()
		doc := p.Parse(text.NewReader(src))
		return renderDocument(ctx, w, doc, src)
	})
}

// section groups a heading with all sibling nodes until the next heading of same or higher level.
type section struct {
	heading  *ast.Heading
	children []ast.Node
}

// renderDocument renders the document with collapsible heading sections.
func renderDocument(ctx context.Context, w io.Writer, doc ast.Node, source []byte) error {
	children := collectChildren(doc)
	if !hasHeadings(children) {
		// No headings — render flat, no collapsible overhead
		return renderNodeSlice(ctx, w, children, source)
	}

	// Emit collapsible script once
	if err := mdCollapsibleScript().Render(ctx, w); err != nil {
		return err
	}

	return renderSections(ctx, w, children, source, 1)
}

// renderSections groups nodes into sections by heading level and renders them.
func renderSections(ctx context.Context, w io.Writer, nodes []ast.Node, source []byte, minLevel int) error {
	preamble, sections := groupSections(nodes, minLevel)

	// Render preamble (nodes before first heading at this level)
	if err := renderNodeSlice(ctx, w, preamble, source); err != nil {
		return err
	}

	// Render each section as a collapsible
	for _, s := range sections {
		headingComp := mdHeading(s.heading.Level, inlineContent(s.heading, source))
		bodyComp := templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
			return renderSections(ctx, w, s.children, source, s.heading.Level+1)
		})
		if err := mdCollapsibleSection(headingComp, bodyComp).Render(ctx, w); err != nil {
			return err
		}
	}
	return nil
}

// groupSections splits a slice of nodes into preamble + heading-based sections.
// A section starts at a heading with Level == minLevel and contains all nodes
// until the next heading with Level <= the section heading's level.
func groupSections(nodes []ast.Node, minLevel int) (preamble []ast.Node, sections []section) {
	var current *section

	for _, n := range nodes {
		h, isHeading := n.(*ast.Heading)
		if isHeading && h.Level >= minLevel {
			if current != nil {
				sections = append(sections, *current)
			}
			current = &section{heading: h}
			continue
		}

		if current == nil {
			preamble = append(preamble, n)
		} else {
			current.children = append(current.children, n)
		}
	}

	if current != nil {
		sections = append(sections, *current)
	}
	return
}

// renderNodeSlice renders a slice of AST nodes in sequence.
func renderNodeSlice(ctx context.Context, w io.Writer, nodes []ast.Node, source []byte) error {
	for _, n := range nodes {
		if err := renderNode(ctx, w, n, source); err != nil {
			return err
		}
	}
	return nil
}

// collectChildren returns all direct children of a node as a slice.
func collectChildren(n ast.Node) []ast.Node {
	var children []ast.Node
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		children = append(children, c)
	}
	return children
}

// hasHeadings returns true if any node in the slice is a heading.
func hasHeadings(nodes []ast.Node) bool {
	for _, n := range nodes {
		if n.Kind() == ast.KindHeading {
			return true
		}
	}
	return false
}

// renderChildren iterates over all children of a node and renders each one.
func renderChildren(ctx context.Context, w io.Writer, n ast.Node, source []byte) error {
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		if err := renderNode(ctx, w, c, source); err != nil {
			return err
		}
	}
	return nil
}

// inlineContent wraps the children of a node as a templ.Component.
func inlineContent(n ast.Node, source []byte) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		return renderChildren(ctx, w, n, source)
	})
}

// renderNode dispatches a single AST node to its corresponding templ component.
func renderNode(ctx context.Context, w io.Writer, n ast.Node, source []byte) error {
	switch n.Kind() {
	case ast.KindHeading:
		heading := n.(*ast.Heading)
		return mdHeading(heading.Level, inlineContent(n, source)).Render(ctx, w)

	case ast.KindParagraph:
		return mdParagraph(inlineContent(n, source)).Render(ctx, w)

	case ast.KindList:
		list := n.(*ast.List)
		items := inlineContent(n, source)
		if list.IsOrdered() {
			return mdOrderedList(items, list.Start).Render(ctx, w)
		}
		return mdBulletList(items).Render(ctx, w)

	case ast.KindListItem:
		return mdListItem(inlineContent(n, source)).Render(ctx, w)

	case ast.KindFencedCodeBlock:
		cb := n.(*ast.FencedCodeBlock)
		code := extractLines(cb, source)
		lang := ""
		if l := cb.Language(source); l != nil {
			lang = string(l)
		}
		return mdFencedCodeBlock(code, lang).Render(ctx, w)

	case ast.KindCodeBlock:
		code := extractLines(n, source)
		return mdFencedCodeBlock(code, "").Render(ctx, w)

	case ast.KindBlockquote:
		return mdBlockquote(inlineContent(n, source)).Render(ctx, w)

	case ast.KindThematicBreak:
		return mdThematicBreak().Render(ctx, w)

	case ast.KindText:
		t := n.(*ast.Text)
		value := string(t.Value(source))
		if err := mdText(value).Render(ctx, w); err != nil {
			return err
		}
		if t.SoftLineBreak() {
			_, err := io.WriteString(w, "\n")
			return err
		}
		if t.HardLineBreak() {
			_, err := io.WriteString(w, "<br>")
			return err
		}
		return nil

	case ast.KindString:
		s := n.(*ast.String)
		return mdText(string(s.Value)).Render(ctx, w)

	case ast.KindEmphasis:
		em := n.(*ast.Emphasis)
		if em.Level == 2 {
			return mdStrong(inlineContent(n, source)).Render(ctx, w)
		}
		return mdEmphasis(inlineContent(n, source)).Render(ctx, w)

	case ast.KindCodeSpan:
		text := extractInlineText(n, source)
		return mdCodeSpan(text).Render(ctx, w)

	case ast.KindLink:
		link := n.(*ast.Link)
		return mdLink(string(link.Destination), string(link.Title), inlineContent(n, source)).Render(ctx, w)

	case ast.KindImage:
		img := n.(*ast.Image)
		alt := extractInlineText(n, source)
		return mdImage(string(img.Destination), alt, string(img.Title)).Render(ctx, w)

	case ast.KindAutoLink:
		al := n.(*ast.AutoLink)
		url := string(al.URL(source))
		label := string(al.Label(source))
		labelComp := templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
			_, err := io.WriteString(w, templ.EscapeString(label))
			return err
		})
		return mdLink(url, "", labelComp).Render(ctx, w)

	case ast.KindHTMLBlock, ast.KindRawHTML:
		// Strip raw HTML for security
		return nil

	case ast.KindTextBlock:
		return renderChildren(ctx, w, n, source)

	case ast.KindDocument:
		return renderChildren(ctx, w, n, source)

	default:
		// Unknown node types — render children as fallback
		return renderChildren(ctx, w, n, source)
	}
}

// extractLines concatenates all line segments from a block node (code blocks).
func extractLines(n ast.Node, source []byte) string {
	lines := n.Lines()
	if lines.Len() == 0 {
		return ""
	}
	var buf bytes.Buffer
	for i := 0; i < lines.Len(); i++ {
		seg := lines.At(i)
		buf.Write(seg.Value(source))
	}
	return buf.String()
}

// codeBlockJSON returns the code string as a JSON-encoded string literal
// for safe embedding in Alpine.js expressions.
func codeBlockJSON(code string) string {
	b, _ := json.Marshal(code)
	return string(b)
}

// olStartAttr returns templ.Attributes with a "start" attribute if start > 1.
func olStartAttr(start int) templ.Attributes {
	if start > 1 {
		return templ.Attributes{"start": strconv.Itoa(start)}
	}
	return nil
}

// extractInlineText recursively extracts plain text from inline children.
func extractInlineText(n ast.Node, source []byte) string {
	var buf bytes.Buffer
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch t := c.(type) {
		case *ast.Text:
			buf.Write(t.Value(source))
		case *ast.String:
			buf.Write(t.Value)
		default:
			buf.WriteString(extractInlineText(c, source))
		}
	}
	return buf.String()
}

// Package mdterm renders markdown to colorized ANSI for terminal display.
//
// It shares goldmark's parser with the web HTML renderer
// (internal/web/components/markdown) but targets ANSI SGR output instead of
// HTML — HTML and ANSI are different output targets, so the parser is shared
// and the renderer is separate (E-1746).
//
// The core design choice versus glow/glamour: **prose is never reflowed.** Each
// markdown paragraph is emitted as a single logical line — no hard breaks are
// inserted to hit a target width — so inline-code spans and hyphenated words are
// never mangled mid-word. The terminal soft-wraps the long line instead. Fenced
// code blocks are emitted verbatim and overflow rather than being wrapped.
//
// Every emitted logical line ends with an SGR reset (ESC[0m) so that `less -R`'s
// "a color must not change across a single line boundary" constraint always
// holds.
//
// Rendering is a pure transform (markdown in, always-colorized ANSI out). The
// decision of whether to colorize at all lives in the caller.
package mdterm

import (
	"fmt"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// SGR sequences. Kept as raw ANSI (no third-party color dependency) so mdterm
// has the same dependency surface as the shared goldmark parser.
const (
	reset       = "\x1b[0m"
	boldStyle   = "\x1b[1m"
	dimStyle    = "\x1b[2m"
	italicStyle = "\x1b[3m"
	codeStyle   = "\x1b[33m"   // inline and fenced code — yellow
	linkStyle   = "\x1b[4;34m" // underline blue
	markerStyle = "\x1b[36m"   // list bullets / ordinals — cyan
	quoteStyle  = "\x1b[36m"   // blockquote bar — cyan
)

// headingStyle returns the SGR prefix for a heading of the given level.
func headingStyle(level int) string {
	switch level {
	case 1:
		return "\x1b[1;36m" // bold cyan
	case 2:
		return "\x1b[1;35m" // bold magenta
	case 3:
		return "\x1b[1;32m" // bold green
	default:
		return "\x1b[1;34m" // bold blue
	}
}

// RenderString parses markdown and returns colorized ANSI. Always colorized;
// the caller decides whether to use it.
func RenderString(src string) string {
	s := []byte(src)
	p := goldmark.DefaultParser()
	doc := p.Parse(text.NewReader(s))

	r := &renderer{src: s}
	for c := doc.FirstChild(); c != nil; c = c.NextSibling() {
		if r.b.Len() > 0 {
			r.b.WriteString("\n") // blank line between top-level blocks
		}
		r.renderBlock(c, "")
	}
	// Normalize to exactly one trailing newline.
	return strings.TrimRight(r.b.String(), "\n") + "\n"
}

type renderer struct {
	src []byte
	b   strings.Builder
}

// line emits one logical line: indent + styled content + SGR reset + newline.
// The trailing reset guarantees no color state leaks across the line boundary.
func (r *renderer) line(indent, content string) {
	r.b.WriteString(indent)
	r.b.WriteString(content)
	r.b.WriteString(reset)
	r.b.WriteString("\n")
}

// renderBlock renders a single block-level node.
func (r *renderer) renderBlock(n ast.Node, indent string) {
	switch n.Kind() {
	case ast.KindHeading:
		h := n.(*ast.Heading)
		r.line(indent, headingStyle(h.Level)+r.inlineLine(n))

	case ast.KindParagraph, ast.KindTextBlock:
		r.line(indent, r.inlineLine(n))

	case ast.KindFencedCodeBlock, ast.KindCodeBlock:
		r.renderCode(n, indent)

	case ast.KindBlockquote:
		r.renderBlockquote(n, indent)

	case ast.KindList:
		r.renderList(n.(*ast.List), indent)

	case ast.KindThematicBreak:
		r.line(indent, dimStyle+strings.Repeat("─", 60))

	case ast.KindHTMLBlock:
		// Strip raw HTML.

	default:
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			r.renderBlock(c, indent)
		}
	}
}

// renderCode emits a code block verbatim, one physical line per source line,
// with no wrapping (it overflows rather than being mangled).
func (r *renderer) renderCode(n ast.Node, indent string) {
	lines := n.Lines()
	for i := 0; i < lines.Len(); i++ {
		seg := lines.At(i)
		text := strings.TrimRight(string(seg.Value(r.src)), "\n")
		r.line(indent, codeStyle+text)
	}
}

// renderBlockquote renders each contained paragraph as a cyan-barred, dim line.
func (r *renderer) renderBlockquote(n ast.Node, indent string) {
	bar := quoteStyle + "│ " + reset + dimStyle
	first := true
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		if !first {
			r.line(indent, quoteStyle+"│")
		}
		first = false
		r.line(indent, bar+r.inlineLine(c))
	}
}

// renderList renders a bullet or ordered list, tracking the ordinal counter.
func (r *renderer) renderList(l *ast.List, indent string) {
	idx := l.Start
	if idx == 0 {
		idx = 1
	}
	for c := l.FirstChild(); c != nil; c = c.NextSibling() {
		item, ok := c.(*ast.ListItem)
		if !ok {
			continue
		}
		var marker string
		if l.IsOrdered() {
			marker = markerStyle + fmt.Sprintf("%d.", idx) + reset
			idx++
		} else {
			marker = markerStyle + "•" + reset
		}
		r.renderListItem(item, indent, marker)
	}
}

// renderListItem places the item's first block on the marker line and indents
// any continuation blocks (including nested lists) under it.
func (r *renderer) renderListItem(item *ast.ListItem, indent, marker string) {
	childIndent := indent + "  "
	first := true
	for c := item.FirstChild(); c != nil; c = c.NextSibling() {
		switch c.Kind() {
		case ast.KindTextBlock, ast.KindParagraph:
			txt := r.inlineLine(c)
			if first {
				r.line(indent, marker+" "+txt)
				first = false
			} else {
				r.line(childIndent, txt)
			}
		case ast.KindList:
			r.renderList(c.(*ast.List), childIndent)
		default:
			r.renderBlock(c, childIndent)
		}
	}
	if first {
		r.line(indent, marker) // empty item
	}
}

// inlineLine renders a node's inline children as one logical line (no reflow).
func (r *renderer) inlineLine(n ast.Node) string {
	return r.inlineChildren(n, "")
}

// inlineChildren concatenates the rendered inline children of n. ambient is the
// SGR sequence active around this content, re-emitted after any nested styled
// span closes so styling restores correctly without a full re-walk.
func (r *renderer) inlineChildren(n ast.Node, ambient string) string {
	var b strings.Builder
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		b.WriteString(r.inline(c, ambient))
	}
	return b.String()
}

// inline renders a single inline node.
func (r *renderer) inline(n ast.Node, ambient string) string {
	switch n.Kind() {
	case ast.KindText:
		t := n.(*ast.Text)
		s := string(t.Value(r.src))
		// A soft or hard line break inside a paragraph joins with a space:
		// prose collapses to one logical line and the terminal soft-wraps it.
		if t.SoftLineBreak() || t.HardLineBreak() {
			s += " "
		}
		return s

	case ast.KindString:
		return string(n.(*ast.String).Value)

	case ast.KindCodeSpan:
		return codeStyle + inlineText(n, r.src) + reset + ambient

	case ast.KindEmphasis:
		e := n.(*ast.Emphasis)
		st := italicStyle
		if e.Level >= 2 {
			st = boldStyle
		}
		return st + r.inlineChildren(n, ambient+st) + reset + ambient

	case ast.KindLink:
		l := n.(*ast.Link)
		raw := inlineText(n, r.src)
		out := linkStyle + r.inlineChildren(n, ambient+linkStyle) + reset + ambient
		if dest := string(l.Destination); dest != "" && dest != raw {
			out += dimStyle + " (" + dest + ")" + reset + ambient
		}
		return out

	case ast.KindAutoLink:
		a := n.(*ast.AutoLink)
		return linkStyle + string(a.URL(r.src)) + reset + ambient

	case ast.KindImage:
		return dimStyle + "[image: " + inlineText(n, r.src) + "]" + reset + ambient

	case ast.KindRawHTML:
		return "" // strip

	default:
		return r.inlineChildren(n, ambient)
	}
}

// inlineText recursively extracts plain text from inline children (used for
// code spans, image alt text, and link-vs-label comparison).
func inlineText(n ast.Node, src []byte) string {
	var b strings.Builder
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch t := c.(type) {
		case *ast.Text:
			b.Write(t.Value(src))
		case *ast.String:
			b.Write(t.Value)
		default:
			b.WriteString(inlineText(c, src))
		}
	}
	return b.String()
}

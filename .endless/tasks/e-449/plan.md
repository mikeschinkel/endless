# Plan: Markdown AST to Templ Components

## Context

The Endless dashboard renders plan item markdown via `goldmark.Convert()` producing raw HTML wrapped in `prose-sm prose-invert`. The result is visually flat — headings lack size/margin differentiation, no collapsible sections, no copy buttons on code blocks. We replace this with an AST-based renderer that maps each goldmark node to a templ component, giving full styling control and enabling collapsible heading sections.

## File Organization

### New files
| File | Purpose |
|------|---------|
| `internal/web/components/markdown/renderer.go` | Parser, AST walker, section grouping, `MarkdownContent()` entry point |
| `internal/web/components/markdown/blocks.templ` | Block components: heading, paragraph, list, code block, blockquote, hr |
| `internal/web/components/markdown/inlines.templ` | Inline components: text, emphasis, strong, code span, link, image |
| `internal/web/components/markdown/section.templ` | Collapsible heading section (templUI collapsible) |

### Modified files
| File | Change |
|------|--------|
| `internal/web/pages/plan_detail.templ` | Replace `@templ.Raw(renderMarkdown(...))` → `@markdown.MarkdownContent(...)` |
| `internal/web/pages/project_detail.templ` | Same replacement (2 locations) + remove `renderMarkdown()` func |
| `internal/web/pages/status_detail.templ` | Same replacement |
| `internal/web/assets/css/input.css` | Add `@source "../../components/markdown"` for Tailwind scanning |

## Architecture

### ARCH — Entry point: `MarkdownContent(source string) templ.Component`

A single public function that parses markdown and returns a renderable templ component.

```go
func MarkdownContent(source string) templ.Component {
    return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
        src := []byte(source)
        p := goldmark.DefaultParser()
        doc := p.Parse(text.NewReader(src))
        return renderDocument(ctx, w, doc, src)
    })
}
```

### WALK — AST traversal via node navigation methods

Uses goldmark's `ast.Node` interface methods (`FirstChild()`, `NextSibling()`, `Kind()`) for all traversal. Our functions:
- `renderNode(ctx, w, node, source)` — dispatches a single node to its templ component by switching on `node.Kind()`
- `renderChildren(ctx, w, node, source)` — iterates `node.FirstChild()` through `NextSibling()`, calling `renderNode` for each
- `inlineContent(node, source)` — wraps `renderChildren` as a `templ.Component` for passing inline children to block components

This approach maps naturally to templ's composition model where components take child components as arguments.

### SECT — Section grouping for collapsible headings

`groupSections(parent ast.Node, minLevel int) (preamble []ast.Node, sections []section)`

Groups document children into:
- **Preamble**: nodes before the first heading — rendered flat, no collapsible wrapper
- **Sections**: each is a heading + all siblings until the next heading of same/higher level

Nested collapsibles: when rendering a section's body, apply `groupSections` recursively at `heading.Level + 1`. This naturally nests H3 sections inside H2 sections.

### COLL — Collapsible approach: templUI collapsible

Using `github.com/templui/templui/components/collapsible` for heading sections:
- Each section wraps in `collapsible.Collapsible(Props{Open: true})` (default expanded)
- Heading renders inside `collapsible.Trigger()`
- Section body renders inside `collapsible.Content()`
- Nested sections work naturally — an H3 collapsible lives inside an H2 collapsible's content
- Requires `collapsible.Script()` — emit once per `MarkdownContent` render
- Styling: add caret rotation indicator via Trigger's Class prop

### TEXT — Text node handling

Goldmark splits inline content across multiple `ast.Text` nodes. The renderer must:
- Call `text.Value(source)` for each Text node
- Check `SoftLineBreak()` → emit space
- Check `HardLineBreak()` → emit `<br/>`

### CODE — Code block text extraction

Fenced code blocks store content in `node.Lines()` segments. Concatenate all `segment.Value(source)` to build the code string. Pass language from `FencedCodeBlock.Language(source)` for future syntax highlighting.

## Styling (Tailwind utility classes, dark theme)

| Element | Classes |
|---------|---------|
| H1 | `text-xl font-bold text-gray-100 mt-6 mb-2` |
| H2 | `text-lg font-bold text-gray-200 mt-5 mb-2` |
| H3 | `text-base font-semibold text-gray-300 mt-4 mb-1` |
| H4 | `text-sm font-semibold text-gray-400 mt-3 mb-1` |
| H5/H6 | `text-sm font-medium text-gray-500 mt-2 mb-1` |
| Paragraph | `text-sm text-gray-400 leading-relaxed mb-3` |
| Bullet list | `list-disc pl-5 mb-3 space-y-1 text-sm text-gray-400` |
| Ordered list | `list-decimal pl-5 mb-3 space-y-1 text-sm text-gray-400` |
| Code block | `bg-gray-800/60 border border-gray-700/50 rounded-md px-4 py-3 text-xs font-mono text-gray-300 overflow-x-auto mb-3` + copy button |
| Inline code | `bg-gray-800/80 text-emerald-400 px-1.5 py-0.5 rounded text-xs font-mono` |
| Bold | `font-semibold text-gray-200` |
| Italic | `italic text-gray-300` |
| Link | `text-emerald-400 hover:text-emerald-300 underline underline-offset-2` |
| Blockquote | `border-l-2 border-gray-600 pl-4 italic text-gray-500 mb-3` |
| Thematic break | `border-t border-gray-700 my-4` |
| Collapsible (templUI) | `collapsible.Props{Open: true}`, trigger styled per heading level, content `pl-4 border-l border-gray-800/50` |

## Implementation Phases

### Phase 1 — Foundation (new files, no consumer changes)
- [REND] Create `renderer.go` — parser, walker, `MarkdownContent()`, `renderNode()`, `renderChildren()`, `inlineContent()`
- [BLCK] Create `blocks.templ` — all block-level components
- [INLN] Create `inlines.templ` — all inline components
- [CSS] Add `@source "../../components/markdown"` to `input.css`

### Phase 2 — Swap markdown rendering only (NOT the expand/collapse toggles — that's #512)
- [PLAN] Update `plan_detail.templ` — replace `@templ.Raw(renderMarkdown(...))` with `@markdown.MarkdownContent(...)`
- [PROJ] Update `project_detail.templ` — 2 locations + remove `renderMarkdown()` helper
- [STAT] Update `status_detail.templ` — 1 location
- [OLD] Keep `data.RenderMarkdown()` for now (may have non-web uses); can deprecate later

### Phase 3 — Collapsible sections
- [SCTN] Create `section.templ` with `mdCollapsibleSection` using templUI collapsible
- [GRPS] Add `groupSections()` to `renderer.go` and wire into `renderDocument()`
- [NEST] Ensure recursive nesting works (H2 → H3 → H4 sections)
- [FLAT] Verify short markdown (no headings) renders without collapse wrappers

### Phase 4 — Polish
- [COPY] Add copy button to fenced code blocks (Alpine.js `navigator.clipboard.writeText`)
- [TUNE] Visual review and spacing adjustments
- [HTML] Handle `ast.RawHTML` / `ast.HTMLBlock` — strip in v1 for safety

## Related Plans
- **#512 — Refactor detail pages: Alpine.js collapse → templUI collapsible** — separate subplan to replace raw Alpine.js expand/collapse in `plan_detail.templ`, `status_detail.templ`, `project_detail.templ` with templUI collapsible for consistency

## Key Decisions
- **Explicit AST node navigation** (Option B) — uses `FirstChild()`/`NextSibling()`/`Kind()` instead of `ast.Walk()`; maps naturally to templ's component composition model
- **templUI collapsible** — provides consistent look/feel with other templUI components; nesting works naturally
- **No syntax highlighting in v1** — immediate follow-up after this plan; code blocks get monospace + background only
- **No caching** — parse on every render; acceptable for dashboard traffic
- **Strip raw HTML** — security improvement over current `templ.Raw()` approach
- **Sections default open** — users click to collapse, not expand

## Follow-up subplans (tracked in Endless under #445)
- **#512** — Refactor detail pages: Alpine.js collapse → templUI collapsible
- **#513** — Syntax highlighting for fenced code blocks (chroma or goldmark-highlighting)

## Verification
1. `just build` compiles without errors
2. All three detail pages render markdown content with proper styling
3. Headings are collapsible — clicking collapses section content
4. Nested headings produce nested collapsibles
5. Markdown without headings renders flat (no collapsible wrappers)
6. Code blocks have copy button that works
7. Inline code, bold, italic, links all render correctly
8. No XSS — text content is templ-escaped, raw HTML blocks are stripped

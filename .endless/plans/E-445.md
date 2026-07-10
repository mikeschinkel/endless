You are creating a Markdown display component for the Endless web dashboard. Enter plan mode, explore the codebase, create a detailed plan, attach it as a subplan, get user approval, then implement.

## Context

The Endless dashboard displays plan items containing full markdown documents (headings, bullets, prose, code blocks). Currently rendered via goldmark Convert() producing raw HTML wrapped in prose-sm prose-invert Tailwind classes. The result is visually flat — headings have no margins or size differentiation, no interactivity.

Stack: Go + Templ + HTMX + Alpine.js + Tailwind CSS v4 + templUI components.

## Chosen Approach: AST to Templ Components

Parse markdown with goldmark to get the AST. Walk the tree and render each node as a templ component. This gives full control over styling and interactivity.

Goldmark exposes a full AST via parser.Parser.Parse() returning ast.Node. The tree can be walked with ast.Walk(). Each node type (heading, paragraph, list, code block) becomes a templ component.

## Requirements

- Headings are visually distinct (size, weight, color, margins above) and act as collapsible sections — clicking collapses everything until the next heading of same or higher level
- Paragraphs have proper spacing
- Bullet lists have proper indentation and markers
- Code blocks have background color, padding, ideally a copy button
- Inline code is visually distinct
- Bold/italic render correctly
- Component is reusable anywhere the dashboard displays markdown
- Short markdown (single paragraph, no headings) renders cleanly without collapse overhead

## Dismissed Alternatives

- CSS-only fix (style prose classes): Too limited, no collapsible sections or per-node control
- AST to styled HTML with Alpine.js injected: Mixes rendering concerns, hard to maintain
- Custom goldmark renderer: Outputs bytes not templ components, loses type safety

## Key Files to Explore

- internal/web/data/markdown.go — Current RenderMarkdown() to be replaced
- internal/web/pages/status_detail.templ — Primary consumer via templ.Raw(renderMarkdown())
- internal/web/pages/project_detail.templ — Also uses renderMarkdown()
- internal/web/pages/plan_detail.templ — Uses renderMarkdown()
- vendor/github.com/yuin/goldmark/ — Library with ast/ package
- vendor/github.com/templui/templui/components/collapsible/ — TemplUI collapsible
- vendor/github.com/templui/templui/components/accordion/ — TemplUI accordion

## Design Decisions for You

- Component API: single MarkdownContent(source string) component? Or parse externally and pass AST?
- templUI collapsible vs raw Alpine.js for heading collapse
- Expand all / collapse all controls
- Whether code blocks need syntax highlighting (probably not v1)
- Performance: parse on every render, or cache?

## Your Workflow

1. Enter plan mode
2. Explore the codebase — goldmark AST API, templUI collapsible, current rendering
3. Create a detailed implementation plan
4. Attach the plan: endless plan import <your-plan-file> --project endless --parent <this-item-id>
5. Get user approval
6. Exit plan mode and implement
7. When done: endless plan complete <this-item-id>
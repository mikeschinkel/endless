# Plan #510: Double-Click Title to Edit Inline

## Original Plan

The initial plan assumed pre-#509 state where the entire row was clickable. It proposed a click-timer pattern to disambiguate single-click (toggle detail) from double-click (edit), templUI Input component for the field, and blur-to-save behavior.

## Implementation Record

### What was built

Double-click on a plan item title opens a floating overlay edit form. The overlay appears at the same X/Y position as the title text, on a higher z-index so the tree layout is undisturbed.

### Key decisions made during implementation

1. **No click-timer needed** — After #509 (caret-only expand/collapse), the title span has no single-click handler, so double-click is unambiguous.

2. **Status sphere toggles detail pane** — User chose this over title-click or caret-both when asked how to trigger the detail pane after removing the row-level click handler.

3. **Overlay modal, not inline expansion** — Inline editing pushed the tree layout down, misaligning status sphere icons. User requested the form float on top of the existing layout at the title's position.

4. **Plain HTML, not templUI Input/Button** — templUI's Input wraps in `<div class="relative w-full">` which broke overlay sizing. Button variants use CSS theme vars (`bg-primary`) that didn't render in our dark theme. Plain HTML with explicit Tailwind classes gives full control for this use case.

5. **Save/Cancel buttons, not blur-to-save** — User wanted visible affordance that editing is happening. Buttons below-right aligned under the field. Green Save (`bg-emerald-600`), gray Cancel (`bg-gray-700`).

6. **Fixed-width input** — Field size set once on edit start based on title length. Does not grow as user types.

7. **Alpine x-data methods** — Refactored inline JS chains into named methods (`startEdit`, `saveEdit`, `cancelEdit`). Status page uses a Go helper `planTreeXData()` to build the x-data string. Recorded as completed #621.

### Files modified

- `internal/web/queries.go` — Added `UpdatePlanItemTitle()`
- `internal/web/handlers.go` — Added `handleUpdatePlanItemTitle()` PUT handler
- `internal/web/server.go` — Added `PUT /plan-item/{id}/title` route
- `internal/web/pages/status_detail.templ` — Overlay edit form, `planTreeXData()` helper
- `internal/web/pages/plan_detail.templ` — Same overlay pattern, per-item x-data

# Bugfix: saveEdit updated wrong element (2026-04-17)

After #511 (hover ID) added a `<span>` between the title and the edit overlay, `saveEdit`'s DOM traversal (`el.closest('.absolute').previousElementSibling`) found the ID span instead of the title span. Editing would replace the hover ID text with the new title instead of updating the actual title.

**Fix**: Target title spans directly by ID (`document.getElementById('title-' + id)` on status page, `x-ref="titleSpan"` on plan detail page) instead of fragile sibling traversal.

**Lesson**: DOM traversal via `previousElementSibling`/`closest` is fragile when the template structure changes. Use explicit IDs or refs for elements that need programmatic updates.

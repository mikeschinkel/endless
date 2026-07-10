# Plan #509: Caret-Only Expand/Collapse

## Context

Currently, clicking anywhere on a plan item row triggers an action. On the status page, clicking the row toggles the detail pane — the caret button uses `.stop` to prevent this, but the title, status sphere, and phase badge all incorrectly trigger detail toggle. On the plan detail page, clicking anywhere on the row toggles expand/collapse — the caret is just a passive text indicator, not a button.

The goal: only the caret toggles expand/collapse. Title click selects the item for the detail pane (status page) or does nothing (plan detail page, until #510 adds editing).

## Changes

### status_detail.templ

**Row div (line 242-243):** Remove `x-on:click` and `cursor-pointer` from the row div. The row should just be a flex container, not a click target.

**Title span (line 255-257):** Add `x-on:click` to toggle the detail pane. Add `cursor-pointer` to the title span. This makes only the title clickable for detail selection.

**Caret button (line 245):** Already correct — has `x-on:click.stop` for expand/collapse. Remove the `.stop` modifier since the row no longer has a click handler. Keep `cursor-pointer`.

**Expand/Collapse all buttons (lines 227-230):** Test whether these work. If broken, likely because `Object.keys()` returns string keys while the template injects numeric IDs. Fix by ensuring consistent key types in `toggleAll()`.

### plan_detail.templ

**Row div (line 49):** Remove `x-on:click="open = !open"` and `cursor-pointer` from the row div.

**Caret indicator (line 61):** Convert from passive `<span>` to clickable `<button>` with `x-on:click="open = !open"` and `cursor-pointer`. Style to match the status page caret pattern.

**Title span (line 51-53):** No click handler for now — future #510 will add double-click-to-edit.

## Files Modified

| File | Change |
|------|--------|
| `internal/web/pages/status_detail.templ` | Move click handlers from row to specific elements |
| `internal/web/pages/plan_detail.templ` | Convert caret to button, remove row-level click |

## Verification

- `just build`
- Start dashboard, navigate to `/status/endless`
- Click caret → children expand/collapse
- Click title → detail pane toggles
- Click status sphere or phase badge → nothing happens
- Expand/Collapse all buttons work
- Navigate to `/project/endless/plan`
- Click caret → content expands/collapses
- Click title → nothing happens
- Click status icon → nothing happens

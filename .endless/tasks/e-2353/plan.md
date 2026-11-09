# Plan

Replace the line-per-field rendering in `plan_items.render_items` with a headerless three-column table: number, glyphs, text. The text cell merges statement, `verify:` and detail into one wrapped block, and after reconciliation carries labeled lines beneath: the verdict, its note indented under it, the question's state and whether an earlier answer still holds. Layout goes through `tabulate` with a borderless one-space format and per-column wrap width; width comes from the terminal, 70 when it cannot be read; the number column widens to the longest label. A blank line separates items.

The glyph cell has three fixed slots, state, origin and kind, so the column stays aligned and a blank slot is the common case. One glyph map lives in `plan_items`, and a static dim legend prints under the table in every view, between two dim rules, following `session list`'s state legend. JSON and agent output do not change.

Decided in chat on 2026-11-08: whitespace layout, no borders; `tabulate` rather than `rich` or `mdterm`; glyphs with a legend rather than words.

Revised in chat on 2026-11-09, reviewing the landed first version: one space between columns, not tabulate's two; the verdict as a labeled line, `Verdict: Done differently`, at the text column's edge instead of an indented arrow, with the note on its own indented line rather than after a colon, which read as a caveat on `As planned`; proper-cased verdict words; a fixed label left of every colon and the variable text right of it, so a post-land verdict reads `Verdict: Deferred (post-land #2)`; labels in bright cyan so a long `task show` scans; rules above and below the legend so it is noticed; fixed glyph slots kept over packing the glyphs left.

# Plan

Replace the line-per-field rendering in `plan_items.render_items` with a headerless three-column table: number, glyphs, text. The text cell merges statement, `verify:` and detail into one wrapped block, and after reconciliation carries the verdict, question and `kept:` lines indented beneath. Layout goes through `tabulate` with its borderless format and per-column wrap width, as the other CLI tables do; width comes from the terminal, 70 when it cannot be read; the number column widens to the longest label. A blank line separates items.

The glyph cell has three fixed slots, state, origin and kind, so the column stays aligned and a blank slot is the common case. One glyph map lives in `plan_items`, and a static dim legend prints under the table in every view, following `session list`'s state legend. JSON and agent output do not change.

Decided in chat on 2026-11-08: whitespace layout, no borders; `tabulate` rather than `rich` or `mdterm`; glyphs with a legend rather than words.

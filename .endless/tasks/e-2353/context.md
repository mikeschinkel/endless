`endless task plan list` prints each item as an indented line: the id, an
`excl` marker for an exclusion, the statement, an `[unapproved]`-style state,
and a `verify:` line under it. The terminal hard-wraps a long statement at
column 0, mid-word, so the id column and the indentation are lost after the
first line. With nine items of one to two sentences each, the list is hard to
scan. Mike called it "effectively unusable" on 2026-10-15, reviewing
E-2345's items.

The same item rendering is used by `endless task show --plan`, which prints
the items before the plan prose.

Reusable today: internal/mdterm already lays out GFM tables to a width,
wrapping cells on whitespace and never shrinking a column below a floor.
Python reaches it through `endless-go markdown render --width`, as task_cmd
does, and its tables are the layout wanted here: no border is needed.

E-720 (replace tabulate with rich for all CLI tables) is related but broader
and unplanned; this task is about the plan-item view alone.



Raised again by Mike on 2026-11-08, reviewing E-2578's 19 items in `task show`
(the terminal hard-wrapped every statement at column 0; `[unapproved]`,
`verify:` and `detail` lines ran together). What he asked for:

- Three columns, in this order: item number, status glyph, text. With so few
  states a glyph should be obvious enough that no column headings are needed.
- Text wraps within its column to the terminal width when it can be detected,
  else to 70 columns. The text column's indent is set by the widest item
  number present (P9, P10, P100).
- Whitespace layout is enough; borders and dividing lines are optional. One
  blank line between items.
- The statement, `verify:` and `detail` are one wrapped block under the
  number, not separate indented lines.
- Reuse the table code other commands already use rather than writing a new
  renderer.

Mockup (P1 and P3 approved, P2 not):

    P1 ✓ opened before any ledger write verify: go test
         ./internal/events/... ./internal/eventcmd/... events.Execute and
         the id-allocating create path alike; no deferred db.Begin() remains
         on the emit path.

    P2   The ledger line is appended after the apply's SQL has run and
         before the database COMMIT, in the same transaction verify: a
         test whose apply SQL fails shows no new ledger line Order:
         BEGIN IMMEDIATE, run SQL, append primary line, append derived
         lines, COMMIT, then commit the segment to git.

    P3 ✓ An apply whose SQL fails leaves no line on the ledger and no change
         in the database, and emit exits non-zero with the apply's error

What renders today: `plan_items.render_items` in `src/endless/plan_items.py`,
used by `task plan list`, `task show` (the "— Plan items —" block), the
reconcile context and the post-land state. Its flags are wider than
approved/unapproved: `excl`, `unapproved`, `changed since approval`, `grown`,
`post-land`, `withdrawn`, and after reconciliation a verdict line, an EQ
question line and a `kept:` line. A glyph column has to cover that set or
keep words for the rare states.

Table code in use: Python renders every CLI table with `tabulate` (six files);
`tabulate` wraps cells with `maxcolwidths` and has a borderless `plain`
format. Go has `internal/mdterm` (GFM tables laid out to a width), reached
from Python as `endless-go markdown render --width`. E-720 would replace
`tabulate` with `rich`.

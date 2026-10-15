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
does. Its tables have column separators and a header rule but no outer
border.

E-720 (replace tabulate with rich for all CLI tables) is related but broader
and unplanned; this task is about the plan-item view alone.

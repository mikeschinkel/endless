# Analysis

**Why a table.** The terminal hard-wraps each long statement at column 0, so the number column and indentation vanish after the first line, and `verify:` and `detail` lines run together with the next item. With nineteen items (E-2578 on 2026-11-08) Mike could not review them; the same happened with nine (E-2345 on 2026-10-15). Wrapping each cell within its column keeps the number in the margin and the text in one block.

**Why tabulate.** It already renders every table in the Python CLI, has a borderless format, and wraps cells to a per-column width since 0.9; the installed version is 0.10. `internal/mdterm` could do the same from Go but would route a Python-owned rendering through a subprocess for no gain, and `rich` is E-720's wider change.

**Why glyphs with a legend.** A word per state (`[unapproved]`, `[changed since approval]`) is what pushed the lines past the width. The state set is larger than approved and unapproved (withdrawn, changed since approval, grown, post-land, exclusion), so a glyph column needs a legend, which `session list` already does for its state column with a static dim line. Fixed slots rather than a variable-length run of glyphs keep the text column at one offset.

**Why the number column tracks the widest label.** Mike's mockup indents the text by the width of the label present; a fixed width would waste columns at P9 and misalign at P100.

**Why 70 when the width is unknown.** Output through a pipe or pager has no terminal size; 70 is the width Mike named, and it fits a split pane.

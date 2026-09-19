# Route closed tasks to ⇥ instead of ⁇

## Why

Under `endless session status --all`, an ordinary done task renders with `⁇` — the glyph
`classify()` reserves for "a status slipped through unhandled" — and the legend then
advertises `⁇ unknown` for a perfectly normal row. `declined` and `obsolete` tasks never
land, so they hit this **always**; so does anything confirmed/assumed whose work never went
through `worktree land`.

The cost is not cosmetic: `⁇` is load-bearing as a diagnostic. Firing it on the most ordinary
rows in the ledger means a genuinely unhandled status would be invisible in the noise.

See this task's `--analysis` for the reproduction and the full derivation.

## Design (settled — do not re-litigate)

**New action `actDone`, glyph `⇥` (U+21E5 RIGHTWARDS ARROW TO BAR), label `closed`.**
Measured at display width 1 with `go-runewidth`, same as every other glyph in the table, so
the fixed 13-column prefix and its alignment are unaffected. "Arrow to bar" reads as a
terminus, which is what a terminal status is, and it is visually distinct from `▶ do` and
`↩ from`.

**Column 1 stays a verb column.** `✓` was considered and rejected: the phase column (column 4)
already renders `✓` for every terminal status, so `✓` in column 1 would state the same fact
twice. Blank was also considered and rejected — a row with no glyph reads as a rendering
failure rather than a classification.

**`⏚` wins over `⇥`.** A row that is both landed and terminal renders `⏚ landed`. This needs
NO reordering: `classify()` already checks `r.Landed` before the status switch, so adding the
terminal case to the switch delivers this for free. `⇥` therefore appears only on a closed
task whose work never merged — which is the informative case. Pin it with a test so a future
refactor cannot silently swap the precedence.

**Decorations still win over both.** `IsFocal`/`IsParent`/`IsFrom`/`InFlight` are checked
first, so a done parent keeps `↑`. Unchanged; just make sure the new cases do not disturb it.

**Enum position: append `actDone` AFTER `actUnknown`.** Enum order is both legend order and
sort rank (`sortRows` ranks on the enum), so appending leaves every existing rank untouched —
the same trick E-1750 used when it added `actUnknown`. The resulting order is deliberate:
closed rows sort last under `--all`, below the `⁇` anomaly rows, which is right — an
unhandled status deserves more prominence than a finished task.

**Legend reads `⇥ closed` AND keeps `✓ done`.** They are not redundant: `✓` is the phase-column
marker and still appears on *decorated* done rows (a focal, parent, or landed task can be
terminal), where column 1 shows `●`/`↑`/`⏚` rather than `⇥`. Two entries, deliberately.

## Change

All of it is in `internal/sessionstatuscmd/session_status.go` (the action enum is not consumed
anywhere else — `tree.go` takes a separate render path):

1. Add `actDone` to the `action` const block, after `actUnknown`, with a comment explaining
   the append-don't-insert rule and the `⏚`-wins precedence.
2. Add `actDone: {"⇥", "closed"}` to `actionMeta`. `buildLegend` iterates `actionMeta` by
   index, so the legend picks it up with no further change.
3. In `classify()`'s status switch, route terminal statuses to `actDone` by calling the
   existing `isTerminal(r.Status)` helper — do NOT re-list the five statuses inline, or the
   list and `isTerminal` will drift. A `switch` case cannot call a function, so this goes in
   as an `if isTerminal(r.Status) { return actDone }` immediately before the `switch r.Status`
   (after the `r.Landed` check, which must keep winning).

Adjacent, trivial, same change: `internal/monitor/session_status.go:36` still documents
`Landed` as routing "a landed non-terminal task to the catch-all ⁇ bucket". That has been
wrong since E-1750 split `⁇` into `⏚ landed` + `⁇ unknown`; `classify()` returns `actLanded`.
Fix the comment.

## Explicitly out of scope

`TestClassify` pins `Status: "blocked"` as its "unrecognized status is unknown" case, while
the guide's status table documents `blocked` as a real status (and the lifecycle diagram says
blocking is a relation, not a state). That contradiction is a separate question about whether
the status exists at all — it is NOT a terminal status and does not belong in this change.
Leave the test case as-is. Captured in this task's `--analysis` under Decisions #2; do not
file a new task for it without asking.

## Verify

Add `tests/tasks/e-1871-verify.sh`, modeled on `tests/tasks/e-1707-verify.sh` (same harness:
build → the rule's own unit tests as a fail-fast gate → full suites → end-to-end against a
seeded temp DB). Fold this task's tests into that one script; hand the user exactly one
command.

Unit tests in `internal/sessionstatuscmd/session_status_test.go`:

- `TestClassify`: each of the five terminal statuses (`confirmed`, `assumed`, `declined`,
  `obsolete`, `completed`) → `actDone`.
- `TestClassify`: terminal + `Landed: true` → `actLanded` (⏚ wins — the precedence pin).
- `TestClassify`: terminal + `IsFocal` → `actThis`, terminal + `IsParent` → `actParent`
  (decorations still win).
- `TestClassify`: no terminal status yields `actUnknown` any more.
- `TestActionIcons`: `actDone` → `⇥`, and `displayWidth("⇥") == 1`.
- `TestBuildLegend`: an undecorated terminal, unlanded row yields BOTH `⇥ closed` and
  `✓ done`, and NOT `⁇ unknown`; a terminal *landed* row yields `⏚ landed` + `✓ done` and
  NOT `⇥ closed`.
- `TestSortRows`: a closed row sorts after an `actUnknown` row and after every open row.

End-to-end through the worktree binary, against a temp DB seeded from
`internal/schema/schema.sql` (see e-1707-verify.sh §4 for the exact fixture pattern —
an epic with children, rendered with `--task <epic> --all`, plus a `task_landings` row to
drive the landed case; note the column is `merge_commit_sha`, not `landed_sha`):

- a `confirmed` child with no landing renders `⇥`, and `⁇` appears nowhere in the output;
- a `confirmed` child WITH a `task_landings` row still renders `⏚`;
- a `declined` and an `obsolete` child both render `⇥`;
- the legend line contains `⇥ closed` and `✓ done` and not `⁇ unknown`;
- the id column does not shift (glyph is width 1).

Run the project-wide regression before handing off: `just build`, `just test-go`, `just test`,
`just guide-check`.

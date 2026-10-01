# Session status stops calling landed tasks not started

`endless session status` renders one column between the task-type letter and
the id. It has four states, and the legend names them: `◆` work product still
outstanding, `⊙` not started, blank work product all landed, `~` not yet
determined. This task is about when `⊙` is shown.

## The bug

`unsettledMark` decides the glyph from one test on the row's own status:
`!taskstatus.Has(taskstatus.Shipped, r.Status)`. A task that lands mid-flight
and keeps working stays `underway`, which is not in `Shipped`, so it wears the
not-started mark despite real landed code.

E-2107 named this when it built the three-state column and accepted it, and
labelled the glyph by what it MEANS rather than by the status test "so the
derivation can be sharpened later without the vocabulary changing." This is
that sharpening.

## What it rests on instead

**`Landed || Unsettled`.** Both fields are already on `SessionStatusRow`, both
are already populated on this path, and neither costs a new read:

- `Landed` is true when the task has at least one `task_landings` row. It
  already drives the ⏚ action glyph.
- `Unsettled` is true when the worktree exists and diverges from the base —
  uncommitted changes, or commits not yet on main. It already drives ◆.

Either one is evidence that something happened. Their union is the answer to
"has this task ever produced work product", and the mark becomes:

    ~       verdict not yet known          (unchanged, still tested first)
    ◆       Unsettled                      (unchanged)
    ⊙       !Landed && !Unsettled          was: !Shipped
    blank   Landed && !Unsettled           was: Shipped && !Unsettled

`~` and `◆` keep their current precedence. Only the ⊙-versus-blank split moves,
which is the whole of the defect.

### Why this evidence and not something new

The reason E-2107 used status was cost, and the reason E-2087 rejected
`task_landings` was reliability. Both have since changed, and the second was
measured on 2026-09-30:

- 760 landings are on record.
- No code-bearing task has been missing a landing row since **May 2026** — 88 of
  the gaps are April, 2 are May, 0 after.
- The rest are research and brainstorm tasks, which have branches from `claim`
  but no code to land, so no landing row is CORRECT for them.

E-2087 was measuring the April backlog, which pre-dates the recording
mechanism. For the era this column renders, the record is accurate. And the
cost objection never applied to `Landed`, which is already on the row.

`Unsettled` is in the union because `Landed` alone answers "did this task
land", not "did it produce anything" — a task with committed but unlanded work
would otherwise still read as never started, and that is exactly the in-flight
case a session most needs to see.

One case remains uncovered and is accepted: a task that produced work, landed
none of it, and had its worktree reaped reads as never started. Nothing of it
survives to point at, so there is nothing to render from.

## Amended during implementation: evidence depends on task type

The rule above, applied to every row, would turn two sets of rows that are blank
today into ⊙: completed research and brainstorm tasks, which have no code and
correctly have no landing row, and finished pre-May code tasks, which are
`Shipped` and so currently render blank. (The original text below said those
"keep showing ⊙". That was wrong: they render blank today.) Mike chose option 3:

    epic                    DescendantShipped           (E-2198, unchanged)
    research, brainstorm    taskstatus.Shipped(status)  (unchanged)
    todo, bugfix, other     Landed                      (E-2199)

`◆` still catches the Unsettled half of the union before this split is reached.

## The pre-May rows read ⊙

About ninety finished code-bearing tasks from April 2026 and earlier have no
landing row, so they now read ⊙, which is an accepted consequence of option 3.
Backfilling landing records from git history is a much larger job with its own
task, E-1715, "Research backfilling historical task landing records", and this
change must not absorb it. Say so in a comment where the rule is written, so the
next reader knows it was considered rather than missed.

## The label does not change

The glyph keeps "not started". You raised the label because the mark was lying;
once it rests on evidence the label is true, and a vocabulary change rippling
through the legend and the docs buys nothing. `buildLegend` must keep deriving
its entry by calling `unsettledMark`, so the legend cannot disagree with the
column.

## Where the change goes

The per-type dispatch already lives in `monitor.SessionStatusRow.HasShippedWork`,
which E-2198 added. Putting a second dispatch in `unsettledMark` would split the
rule across two places, so the method takes the per-type rule and is renamed
`HasWorkProduct`, because a landed `underway` task has not "shipped". There is
still no new query and no new cache: `Landed` is already on the row.
`unsettledMark` keeps its switch and calls the renamed method. Its comment block
is rewritten to state the new rule, the 2026-09-30 measurement, the E-2087
history and the accepted consequences. The guide's paragraph on ⊙
(`docs/guide/orchestration.md`) is updated to match.

## Acceptance

- A task that landed work and is still `underway` renders blank, not ⊙.
- A task with an unsettled worktree still renders ◆.
- A todo or bugfix with no landing row and no unsettled worktree renders ⊙,
  whatever its status.
- A research or brainstorm task at `unreviewed` or `completed` renders blank
  without a landing row.
- A task whose unsettled verdict is unknown still renders `~`.
- The legend still derives its entry by calling `unsettledMark`.
- Pre-May-2026 code rows read ⊙, and a comment says why (E-1715).
- E-2198's epic behaviour is preserved.
- `just test` and `just test-go` pass.

# The not-started mark rests on evidence, not on status

## The bug

`unsettledMark` decides the mark from one test on the row's own status:
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

## The pre-May rows stay wrong

About ninety finished tasks from April 2026 and earlier have no landing row and
will keep showing ⊙. Leave them. Backfilling landing records from git history is
a much larger job with its own task — E-1715, "Research backfilling historical
task landing records" — and this change must not absorb it. Say so in a comment
where the rule is written, so the next reader knows it was considered rather
than missed.

## The label does not change

The glyph keeps "not started". You raised the label because the mark was lying;
once it rests on evidence the label is true, and a vocabulary change rippling
through the legend and the docs buys nothing. `buildLegend` must keep deriving
its entry by calling `unsettledMark`, so the legend cannot disagree with the
column.

## Where the change goes

`unsettledMark` in `internal/sessionstatuscmd/session_status.go`, and only
there. It already receives a `SessionStatusRow` carrying both fields, so this is
a change to one switch and to the comment block above it, with no new query, no
new cache, and nothing added to `internal/monitor`.

The comment block above `unsettledMark` is load-bearing — it records why the
split was decided by status, why `task_landings` was not consulted, and the
mis-signal this task removes. Rewrite it to state the new rule and why the
evidence is now trustworthy, including the measurement above. Do not delete the
E-2087 history; it explains why the obvious source was once wrong.

Coordinate with E-2198, which is changing the same function to derive an epic's
mark from its children. That change lands first and is orthogonal — it governs
which ROWS consult children, this one governs what counts as evidence for a row
that does its own work. Rebase on it rather than reverting it.

## Acceptance

- A task that landed work and is still `underway` renders blank, not ⊙.
- A task with an unsettled worktree still renders ◆.
- A task with no landing row and no unsettled worktree still renders ⊙.
- A task whose unsettled verdict is unknown still renders `~`.
- The legend still derives its entry by calling `unsettledMark`.
- Pre-May-2026 rows are untouched and a comment says why.
- E-2198's epic behaviour is preserved.
- `just test` and `just test-go` pass.

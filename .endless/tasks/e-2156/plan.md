# Plan — rebuild project monitor and project status around tasks

Specified by Mike in conversation on 2026-09-15, with `project monitor` open in
front of him. This replaces the display E-1976 shipped; it is not a tweak to it.

Explicitly an interim shape. Mike expects to replace it with a scrolling TUI
before long, and wants to live with this one to learn what he actually needs.
Build what is specified here and stop; do not optimise past it.

## The shape

One tmux pane holds the whole of `project monitor`, with the CLI pane beneath it
(the existing two-pane layout, unchanged). Inside that pane, THREE lists, in this
order, with NO separator rows and no headers between them:

1. **urgent** — `phase = 'urgent'`, non-terminal. UNTRUNCATED.
2. **epics** — `type = epic`, `phase IN ('now','next')`, non-terminal.
   UNTRUNCATED.
3. **everything else** — non-epic, `phase IN ('now','next')`, non-terminal.
   The ONLY truncated list: it takes whatever pane height remains, and grows
   when the user grows the terminal.

Each task appears ONCE, in the highest list it qualifies for: an urgent epic is
in list 1, never also in list 2; an epic is never in list 3.

Separators are unnecessary because the rows identify themselves — list 1 carries
the urgent phase glyph, list 2 carries the epic type letter. Mike may revisit
this; do not add separators pre-emptively.

## Ordering

All three lists sort reverse-chronologically.

`--sort` on BOTH `project monitor` and `project status`, taking `updated`
(default) or `id`. Mike was undecided between them and wants to try both in use,
which is the flag's whole purpose — do not pick one and drop the other.

## `project status` does not truncate

Not the third list, not anything. It is the surface for seeing everything,
including what the monitor cannot fit, and Mike expects to pipe it through grep
routinely to find tasks the monitor is not showing.

`--later` shows ONLY `phase = 'later'` tasks — the phase the other views exclude.

## Rows

Reuse `session status`'s row rendering. It is well optimised for an experienced
reader and there is no reason to invent a second format; share the code.

Changes for this context:

- **Drop the time/age column.** It is not useful here, and — the reason that
  matters more — it changes every tick, so the frame differs on every repaint and
  the pane moves constantly in the corner of the eye. Removing it makes motion
  mean something: the pane only redraws when the row set actually changes.
- **Drop the session-relative glyphs**: `●` (this session's task), `↑` (parent),
  `↩` (from/spawner). They describe a viewing session, and there is none here.
- **Keep the action glyph.** E-1976's error was GROUPING by it, not showing it.
- **Keep the phase glyph on every row**, including in the urgent list where it is
  technically redundant — for vertical alignment, and because it is what the eye
  is used to.
- Keep the type letter, the id, and the title.

## Sessions are consulted, never displayed

No session appears as a row. But a task's row DOES reflect whether a live session
is on it: `⟳` for `underway` with a live session, `◷` for `underway` without —
the distinction `session status` already draws, and the only signal that claimed
work has stalled.

E-2091's `prompted` state extends this naturally: a task whose live session is
blocked on a permission prompt should wear the `⚠` glyph rather than `⟳`, since
that is the same fact at a higher urgency. STATED EXPLICITLY so it can be
rejected: it follows from "the row reflects a live session" plus the existing
glyph vocabulary, but Mike did not say it in as many words.

## What is removed

- **All session rows**, and with them the "sessions with no task" list that
  prompted this redesign. They were unactionable — three such rows led to
  "Session NNN has no reachable tmux pane" when followed.
- **Idle-session reporting.** tmux window tabs already carry it. Out of scope
  now; Mike may revisit when a TUI can do more.
- **The seven-rank classification as a grouping mechanism** (`waiting`, `verify`,
  `read`, `review`, `orphan`, `idle`, `doing`). The glyphs survive on rows; the
  grouping does not. There is no ordering among statuses that says one matters
  more than another — they are all points in one workflow.
- **The per-rank cap and its fair-share allocation.** One list truncates, by pane
  height alone.
- **`--limit` / `--no-limit`.** Nothing is count-capped any more. This leaves
  `rowcap.limit_options_for` with no caller; remove it too if so, rather than
  leaving a factory nothing builds from.
- **`--all`.** Its meaning was "also include `ready`", which is now included by
  default as a non-terminal status.

Keep `--json` and the fault badge as they are.

## Status filtering was considered and rejected

Measured while planning: list 3 holds 295 tasks for this project — 127
`unplanned`, 92 `submitted`, 38 `unverified`, 25 `ready`, 13 `underway`.

The obvious move is to filter to statuses needing the user specifically. Mike
rejected it (2026-09-15): `session status` carries the per-session detail, this
is the high-level view, and no status is inherently more important than another
because they are all part of one workflow. `unplanned` and `ready` are both
needed now — they may matter less once auto-spawn and auto-planning exist, which
is a reason to revisit later and not to filter now.

The pane running out of room is not a defect to design around. It is the pressure
that gets work demoted to `later`, the same way 20 untruncated epics is the
pressure that gets epics demoted.

## Terminology

Use the names the product uses: `project status` (the snapshot) and `project
monitor` (the live loop), or both commands where a sentence means the pair. E-2153
removed a coined synonym for the two from the tree; this rebuild rewrites most of
the files that cleanup touched, so it is the likeliest place for a new coinage to
appear. Do not introduce one — if something genuinely lacks a name, ask.

## Verification

`.endless/tasks/e-<id>/verify.sh`, sourcing `_harness.sh`:

1. Fail-fast unit gate: `internal/projectstatuscmd`, `internal/monitor`, and the
   Python suite.
2. The three lists, in order, against a seeded database: an urgent task, an
   urgent epic, a now epic, a next epic, and non-epic tasks across the
   non-terminal statuses. Assert the order and that each task appears exactly
   once.
3. Untruncated means untruncated: seed more urgent tasks and more epics than fit
   a small `--rows` budget and assert every one renders.
4. Only list 3 truncates, and by height: the same row set at two `--rows` values
   yields more list-3 rows at the larger one, with lists 1 and 2 unchanged.
5. `--sort id` and `--sort updated` produce different orders on a fixture built
   so they must (an old task updated recently).
6. `project status` truncates nothing at any `--rows` value, and `--later` shows
   only `later` tasks.
7. No row renders a session: no `ES-` id, and no `●`/`↑`/`↩` glyph, on any
   fixture.
8. A task held by a live session renders `⟳` and the same task with no live
   session renders `◷`.
9. No age column: two renders of an unchanged row set, taken a second apart, are
   byte-identical. This is the repaint-motion property, and asserting it on the
   frame is what keeps it from regressing when a future column carries a clock.
10. No coined term: the rebuilt files carry no name for these views other than
    `project status` and `project monitor`.


## Scope added during implementation

- **Shared row code is a new package, `internal/taskrow`**: the action glyph
  table, the status→action rule, the type letter, the phase character, the
  fixed prefix and the legend fit. `session status` now uses it in place of its
  own copies, so the two views cannot drift.
- **`session status` no longer wears ⁇ on `unreviewed` tasks.** The shared rule
  maps `unreviewed` to ☰ read — the glyph `project status` already had — where
  `session status` had been falling through to its should-never-happen net.
- **`rowcap` loses the parameters E-1976 added for the per-group cap**
  (`resolve_cap(default=)`, `limit_options_for(default, unit)`). With that cap
  gone they had no caller; `limit_options` is a plain decorator again.
- **Guide text** (`docs/guide/reference.md`, `appendix-a.md`) rewritten for the
  three lists. The line saying auto-spawned sessions carry `*` in `project
  status` is removed: that marker lived on session rows, which are gone.

## Decisions taken where the plan was silent

- **⟳/⚠ apply to `underway` rows only**, as the plan words it. `session status`
  marks ANY task a live session holds ⟳; following that here would hide ☑ on
  every `unverified` task whose session is still open.
- **The unsettled column (◆ ⊙ ~) and the block column (⊗ ⏸) are not drawn.**
  The plan lists what rows keep; those were not on it. The one-column slot
  between type letter and id is left blank.
- **A cut third list prints `… N more (project status)`**, the codebase's rule
  that a dropped row leaves a trace.
- **`--sort id` is id descending** — newest filed first, the reverse-chronological
  reading of the plan's "all three lists sort reverse-chronologically".
- **A hidden session still marks its task live.** Hiding a session asks not to
  see it; it should not make the work it holds read as stalled.

## Revisit (2026-10-04), from Mike living with the monitor

- **Lists are told apart by background color**, still with no separator rows:
  urgent on palette 1, epics on palette 2, everything else on palette 3, all
  behind foreground 232 (Mike's picks), each row padded to the full width.
- **» replaces ☰ for `unreviewed`**, and its label is now "review". ☰ rendered
  wider than one column in a real terminal. ⚑ (a `submitted` plan) is relabeled
  "approve" — the act `task approve` performs — so "review" is not on two
  glyphs. Applies to `session status` too, through taskrow.
- **The monitor fits its frame to its own pane** and no longer resizes the pane.
  Dragging the tmux divider now changes how many third-list rows show; before,
  the budget came from 65% of the window and the pane was refitted every
  repaint, so dragging did nothing and a window resize snapped it back. The
  budget is the pane height less one line, which also stops the frame from
  scrolling and leaving duplicated rows above the legend. `--tmux` now starts
  the monitor pane at 65% (`split-window -l 65%`), since nothing resizes it after.
- **`project monitor --tmux --use-existing`** adopts a session of the monitor's
  name that Endless did not create: stamps it as the project's monitor and
  switches to it, leaving its panes as they are. Without the flag the refusal
  now names it.

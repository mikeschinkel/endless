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


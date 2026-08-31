# E-1976 — plan

`project status` (snapshot) + `project monitor` (live) — the project-scoped
counterpart to the `session status` / `session monitor` pair. Same architecture:
a thin Python pass-through to a Go subcommand that owns the query, the ranking,
the render and the loop.

## Decisions taken with Mike (this session)

1. **Name.** The existing `project status` (the project metadata card) is
   renamed **`project info`**. `project status` / `project monitor` become the
   attention board.
2. **"Waiting on you" is deferred.** Nothing in Endless can see a permission
   prompt today: no installed Claude hook fires on one, and `sessions.state`'s
   `needs_input` value is written only on INSERT and on the revive-an-ended-row
   CASE. Filed as **E-2091** (`cleans_up E-1976`). The board ships with the
   `waiting` rank present and exercised; it simply has no live producer until
   E-2091 lands.
3. **The tmux launcher is in scope.** `project monitor --tmux` creates (or
   switches to) the dedicated two-pane session.
4. **One ranked board**, not two tables.

## The board

Project-scoped rows, each an attention claim. A live session and the task it
claimed are ONE row, not two.

| rank | action   | glyph | what it is                                  |
|------|----------|-------|---------------------------------------------|
| 1    | waiting  | `?`   | live session, state `needs_input` (E-2091)   |
| 2    | verify   | `☑`   | task `unverified` — awaiting your verdict    |
| 3    | read     | `◑`   | task `unreviewed` — an outcome to read       |
| 4    | review   | `⚑`   | task `submitted` — a plan awaiting approval  |
| 5    | orphan   | `◷`   | task `underway`, no live session on it       |
| 6    | idle     | `‖`   | live session whose turn has ended            |
| 7    | doing    | `⟳`   | live session working — nothing needed of you |
| 8    | ready    | `▶`   | task `ready` — spawnable; `--all` only       |

Glyphs are reused from `session status` (`☑ ⚑ ◷ ▶ ⟳`) and `session list`
(`‖ ?`) wherever the meaning matches, so the eye learns one vocabulary. All are
asserted single-width so the fixed prefix stays aligned.

`waiting` ranks above `verify` even though the description says unverified sorts
to the top: a blocked session is a hard stop and there will be at most a
handful, while unverified runs to dozens. Until E-2091 lands there is no live
`needs_input` producer, so unverified IS the top rank in practice.

**Sort within a group** is per-group, from a flag on the action table:
- session-backed groups sort OLDEST activity first — the clock measures how
  long you have kept it waiting.
- task groups sort NEWEST update first — the clock measures staleness, and the
  most recently finished work is what you can actually act on. The 30-90-day
  sediment is announced by the group's footer, not floated to the top.

**Per-group cap**, default 10, `--limit N` / `--no-limit`. A global cap would
put 57 unverified rows above every session and push the sessions off the board
— the exact failure the board exists to fix. Each truncated group prints
`… N more <group> (--no-limit)` in the rowcap idiom.

**Row shape:** `I L E-NNNN P ES-NNN age Title` — action glyph, type letter, task
id, phase char, session id, compact age, title. Session id and age columns are
width-on-demand like `session status`'s block/hidden columns.

## Code

### Extractions (no duplication, no test churn)

- **`internal/faultbadge`** — `faultbadge.go` + its test move wholesale out of
  `sessionstatuscmd`; `renderFaultBadge` becomes `faultbadge.Render`.
- **`internal/liveview`** — the live-pane machinery both monitors need: the
  redraw/signal/tick/jobs-fire loop, `FrameLines`/`PaneHeightForFrame`/
  `FitPaneToFrame`/`EraseEachLineToEOL`, and the terminal helpers
  (`DetectCols`, `ColorEnabled`, `Dim`, `Collapse`, ANSI constants).

`sessionstatuscmd` keeps one-line unexported wrappers over both, so every
existing test in that package compiles and passes untouched.

### New

- **`internal/monitor/project_status.go`** — `ProjectStatusRow` + the two reads
  (live sessions for the project; attention-claiming tasks), merged in Go so a
  session and its task are one row. Read-only project resolution by name or cwd.
- **`internal/projectstatuscmd/`** — flags, ranking, render, JSON, monitor loop,
  and the tmux launcher.
- **`internal/taskstatus`** — one new `Group`, `AwaitsUser` = {submitted,
  unverified, unreviewed}, so the board's status set cannot drift from the
  vocabulary. One row in `groups`, one in `groupSlugs`.
- **`src/endless/rowcap.py`** — `resolve_cap(..., default=)` and a
  `limit_options_for(default, noun)` factory; `limit_options` becomes
  `limit_options_for(DEFAULT_ROW_CAP)`. Zero change for the 16 existing callers.
- **`src/endless/project_status_cmd.py`** — the pass-through (no SQLite; the
  six-file rule holds).
- **`cli.py`** — `project info` (renamed), `project status`, `project monitor`.

### DB context

`project-status` pins the main DB unless an explicit `--config-dir` was given —
the same rule and the same reason as `session-status`: sessions and tasks are
read by a single-database join, and a worktree sandbox holds one project row
and no tasks.

### tmux launcher

`endless project monitor --tmux [NAME]`: idempotent. Creates a detached session
named `<project>-monitor` (sanitised for tmux), top pane running
`endless project monitor <project>`, bottom pane a bare shell, both in the
project's main checkout; focus lands on the shell. Inside tmux it
`switch-client`s; outside it `attach`es. If the session already exists it just
switches/attaches.

### The E-698 trigger

`project monitor` fires `jobs.RunDue` on every refresh, exactly as `session
monitor` does. NOTE: this does not by itself make the project monitor the
on/off switch E-1815 describes — a session monitor fires the same runner. Making
auto-spawn exclusive to this window is E-1814's job (its selector job gates
itself); recorded as a comment at the trigger rather than silently over-claimed.

## Tests

- Go: ranking, sort direction, per-group cap + footer, glyph widths, row merge,
  JSON shape, tmux argv builders, liveview pane fit.
- Python: `project info` renamed and working, the two new verbs wired, flags
  refused together, help text.
- `.endless/tasks/e-1976/verify.sh`: fail-fast unit gate, then the board end to
  end against a seeded throwaway database. Sources `_harness.sh` and runs
  through `endless task verify` (E-2023's layout, adopted after the rebase).

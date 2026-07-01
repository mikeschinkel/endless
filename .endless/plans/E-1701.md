# Add a landed/dirty `◆` indicator to `session status` flat view

Split out from E-1690 Part B (the template-tightening deliverable landed as
E-1690 itself). E-1690's `Final message` discipline tells spawned sessions to
"report only what `session status` can't already show" — the landed-vs-worktree
delta is the one gap the command can't currently show. This task closes that gap
so the agent never needs to narrate it.

## Goal
Make the landed-vs-worktree delta command-provided in `endless session status`
so end-of-session recaps don't have to describe it.

## Behavior
- Per session task, determine: is it landed, and if landed does the worktree
  have changes since the land (needs re-landing)?
- Collapse to a single "dirty" indicator: in the FLAT view, render `◆`
  (U+25C6 BLACK DIAMOND; UTF-8 E2 97 86) instead of the space between the
  task-type letter (E/T/B/…) and the id — e.g. `T◆E-1684` — when not landed /
  dirty. NO legend entry (Mike, 2026-07-01).
- Distinct from `--tree`'s focal marker: the flat `◆` (dirty) and `--tree`'s
  leading `*` ("this" task) are different views/glyphs — no clash.

## Data sources (already present, reuse)
- **Landed:** `monitor.SessionStatusRow.Landed` already exists (EXISTS over
  `task_landings`), fed into the flat view today (`internal/monitor/session_status.go`).
- **Dirty since land:** `internal/monitor/reap_worktrees.go` already uses
  `git -C <wt> status --porcelain` emptiness plus `task_landings.landed_at`;
  same pattern gives "changed since land" (HEAD vs the landed commit / merge-base).
- **Render site:** `internal/sessionstatuscmd/session_status.go` ~line 290,
  `fmt.Sprintf("%s %s %-6s %s ", ...)` — the space between type letter and id
  becomes `◆` once a per-row `Dirty` bool exists. Watch the fixed 13-col prefix
  width (both space and `◆` are width 1, so alignment holds).

## Open design refinements (decide with Mike before building)
- In `--tree`, whether to switch the "this" marker from `*` to `●` (the flat
  view's `● this` icon) for cross-view consistency.
- Whether a distinct "landed" glyph reads well (vs. dirty), or one indicator is enough.

## Verification
Extend the ad-hoc verify convention (model `tests/tasks/e-1684-verify.sh`):
a landed task whose worktree has changes since the land renders `◆` between the
type letter and id; a clean landed task does not.

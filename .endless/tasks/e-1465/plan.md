# Implement `endless session next` (the read command)

Build the real Go command behind the throwaway bash/sqlite prototype (originally at `~/.config/endless/session-next.sql` + `session-next.sh`, now moved to `/tmp/` once superseded). **That prototype is the authoritative spec** for the query, columns, sorting, rendering, AND the `--watch` live-refresh loop — read it first.

## Architecture
- `endless session next` lives in the Python `session` command group, delegating the read to **Go (endless-go)** per the reads-in-Go rule (E-1486): the Python verb shells out to an endless-go subcommand (`session-next`) inheriting stdout, so the Go side detects terminal width + color against the real tty. (Pattern confirmed against `session-query`.)
- The `session-next` subcommand **pins the main DB** (sessions live in main regardless of cwd — hook writes PinMainDB), so no `--config-dir` is threaded.
- **Focal-task resolution** — do NOT re-derive pane→session logic; compose the existing Go resolvers, matching claude.go's documented priority (*session active task > `@endless_task_id`*):
  1. `monitor.GetActiveTaskForPane($TMUX_PANE)` (ED-1523) — live session's `active_task` via process match (state != 'ended').
  2. **Fallback** when that returns nothing — which is COMMON: active windows are often state `needs_input` with NULL `active_task`, and bound sessions can be `ended` with empty `process`, so the process-based resolver finds neither. Fall back to the window's `@endless_task_id` (read with `tmux display-message -p "#{@endless_task_id}"`).
  3. Global most-recent only as a last resort.
  (Exposed as `monitor.ResolveSessionNextFocal`. Extends ED-1523 with the `@endless_task_id` fallback for null-active windows.)

## Row set (per the prototype)
For focal task T:
- `session_tasks` for ALL sessions whose `active_task_id = T` (cross-project; dup-session-proof — matches `WHERE active_task_id=T`)
- ∪ the focal task T
- ∪ the parent session's active task. The parent is resolved from this window's `@endless_spawned_by` marker, which holds the spawning session's **integer sessions.id** (or a `pid-<n>` fallback for non-Claude spawners → no parent). The bash prototype's pane-based lookup of this marker is WRONG; the Go command (`monitor.ResolveSessionNextParentSession`) reads it as a session id and resolves that session's active task directly.

Omit done-work (terminal status) EXCEPT the focal and parent rows, which always show. `--all` includes done-work.

## Columns (left→right), single-spaced
`<action-icon> <type-letter> <E-id> <phase-char> <block> <title-fills-width>`
- **action icon**: `●` this · `↑` parent · `⟳` doing (another live session on it) · `▶` do (canonical-`ready`, **regardless of plan text** — ED-1522) · `✎` plan (unplanned/needs_plan/**revisit** — revisit folds into plan) · `☑` verify · `◷` orphan (underway/in_progress, no live session) · `·` other. (No `⏸` as an action; no separate `revisit` icon — it would not fit the one-line legend.)
- **type letter**: E/T/B/R/Z from `task_types.slug`
- **phase char**: `!` urgent, `1` now, `2` next, `3` later, `?` maybe; `✓` when the task is terminal/done.
- **block column** (auto-width 0/1/2): `⊗` blocked-by (any open blocker), `⏸` blocks (this blocks others). Width 0 if nothing blocked; 1 if no single task needs both; 2 only when some task is both.
- **title**: fills remaining terminal width (detected via `golang.org/x/term`, `--cols` override, then `$COLUMNS`, then 90); truncated by **display width** (`runewidth`).

Phase-by-intensity color uses **plain ANSI** (SGR bold for urgent, dim for later/maybe and terminal rows), gated on stdout being a tty. lipgloss was considered and rejected as too heavy for five intensities — it's reserved for the future TUI work.

## Live refresh (`--watch`)
`--watch` redraws the view every 2 seconds until interrupted (Ctrl-C), replicating the prototype's `session-next.sh` loop. **This is required** — without it the command can't replace the prototype the user runs continuously in a pane. Behavior:
- Focal + parent are anchored ONCE at startup (the view stays pinned to THIS window's task across refreshes); only the row query + render re-run each tick.
- Hide the cursor for the duration; restore it on every exit path (SIGINT/SIGTERM handled).
- Repaint only when the rendered frame changes (no idle flicker): cursor-home, repaint, clear-to-end-of-display so a shorter frame leaves no stale rows. Re-detect width each tick for resizes.
- Off-tty (piped/captured) `--watch` degrades to a single frame and exits, so scripts/pipes don't hang. The Python verb swallows KeyboardInterrupt (the Go child restores the cursor itself).

## Sort
action-rank (this, parent, doing, do, plan, verify, orphan, other), then phase-rank (urgent<now<next<later<maybe), then id. Ranked on the action enum, not the icon glyph (the prototype's glyph-match sort had a latent bug that mis-sorted parent rows).

## Status model
`ready` = approved-to-spawn, plan optional (ED-1522); `do` (`▶`) surfaces ready tasks regardless of plan. Canonicalize status in-query / in-render: unplanned/needs_plan/revisit→plan; verify/unverified→verify; underway/in_progress→working; terminal = confirmed/assumed/declined/obsolete/completed.

## Out of scope (separate tasks)
- `--tree` order view (E-1684) + session ordering (E-1683).
- goto/back navigation (E-1681).
- Duplicate session rows (E-1640) — resolver is dup-proof regardless.

(A full lipgloss/bubbletea TUI is NOT planned here and is NOT E-859 or E-1622 — those cover conversation-message tailing and `/bg`-agent monitoring respectively, unrelated to this view.)

## Verification
Per-task script at `tests/tasks/e-1465-verify.sh`: seeds a synthetic fixture in a throwaway `HOME` (session-next pins main, so a temp HOME is the isolation lever — no real-ledger access) and asserts every action icon, status canonicalization, block column, `--all` filter, sort order, `--watch` off-tty single-frame, and empty state. Plus Go unit tests in `internal/monitor` (query/row-set/decorations) and `internal/sessionnextcmd` (render rules).

## Reference
Prototype (moved to `/tmp/session-next.sql` + `/tmp/session-next.sh` once superseded): the SQL is the query + layout spec; the shell script is the resolution + `--watch` loop spec.

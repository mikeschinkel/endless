# E-2204 — hidden tasks and ownership, dim non-actionable rows, marker colors

Decided with Mike (ES-1248, 2026-10-01). All three parts change session status
and session monitor, which draw the same frame.

## 1. Hiding a task takes the hiding session out of its ownership

In `liveOwnership` (internal/monitor/session_ownership.go), a session that has
a `session_hidden_tasks` row for a task is excluded from that task's
surfacers, revisiters and focusers. The exception is a session that has the task
in FOCUS: it counts as a focuser again. E-2188 already lets focus override hide
for display, and a session touching the task again is exactly when duplicate
work is possible.

Effect on the reported case: E-1991's session hid E-1814, so it stops counting
as a live updater. E-1814 is no longer ambiguous, and the duplicate mark leaves
ES-1248's board.

A filer that hides its own task gives up ownership too. Hide means "not mine".

## 2. Dim rows that are not actionable from this board

In `colorize` (internal/sessionstatuscmd/session_status.go), also dim:

- **in-flight rows** — `r.InFlight` (`⟳ doing`), any task a live session other
  than this board's is working, whoever spawned it. This uses the existing flag;
  no spawn record is added;
- **the parent row** — `r.IsParent`, always, urgent phase included.

`☑ verify` rows stay bright: verifying is the user's action. The board's own
task (`IsFocal`) is never dimmed.

Display only: `--agent` and `--json` gain nothing. They already carry
`in_flight` and the parent.

## 3. The duplicate marker in 232 on 160; warnings are never dimmed

- `ansiDuplicateID` becomes `\x1b[38;5;232;48;5;160m` (256-color foreground
  232, background 160), with no 16-color fallback. The `◫` glyph is already the
  plain-text fallback.
- A highlighted id is never faded by its row's dim. `idField` cancels dim
  (`\x1b[22m`) before the duplicate or focus escape and restores it
  (`\x1b[2m`) after, but only when the row is dimmed. That keeps the 232/160
  contrast and the inverse focus id full-strength on in-flight rows, which is
  where E-1814's marker appeared.

## 4. Docs

- docs/guide/sessions.md, "Focus, and a task on more than one board": hiding
  removes your session from a task's ownership unless the task is your focus,
  and the duplicate mark's new colors.
- The same section, or the dimming note beside the relation tiers: in-flight
  and parent rows are dimmed.

## Verify

- Unit (internal/monitor): a hidden revisiter, a hidden surfacer and a hidden
  focuser are each excluded; a hidden session that has the task in focus is
  counted; the E-1814 shape (two live updaters, one hid it) is not ambiguous.
- Unit (sessionstatuscmd): colorize dims in-flight and parent rows and not
  verify rows or the board's own task; idField emits 38;5;232;48;5;160, and on
  a dimmed row wraps the id in 22m … 2m; no escapes with color off.
- A captured `session monitor` frame matches `session status` for the same
  board, as E-2188's suite does, so the two cannot split.

# Don't dim session-status rows that carry ◆ (unlanded delta)

## Why
E-1701's `◆` marks a row whose worktree diverges from main (uncommitted changes OR commits not
yet landed). But `colorize()` renders a terminal-status row (e.g. `completed`) dim grey, which
*wins* over `◆` — so a completed-but-unlanded task looks "done" and the `◆` is easy to miss.
The two signals contradict. Hit live on E-1687: it read `completed` + `◆` in dim grey, and the
needed `land` was nearly overlooked.

## Change
`colorize(line, phase, terminal, enabled)` in `internal/sessionstatuscmd/session_status.go`:
do NOT apply the dim styling (the terminal case, and arguably the later/maybe cases) when the
row is `Dirty` (`◆`). A dirty row renders at normal (or bold) weight so `◆` stays visible and
the row does not read as done. Thread the row's `Dirty` flag into `colorize` — it is already
computed on `SessionStatusRow` via `AnnotateSessionStatusDirty` (flat view only).

## Verify
A completed task whose worktree has unlanded commits renders NON-grey with a visible `◆`; once
landed (`◆` clears), it renders dim grey as before. Add/adjust a `colorize` unit test.

Today the UserPromptSubmit handler in cmd/endless-hook/claude.go calls writeClaudeCompanion only when CompanionExists returns false (the E-1011 backfill semantic). That leaves the companion file vulnerable to drift any time the active_task_id changes through a path that doesn't trigger writeClaudeCompanion — for example when:

- A multi-command bash invocation contains 'endless task start E-NNN' but the hook's tool_input.command doesn't include the relevant fragment (observed during E-1027/E-1028 verification: the synthetic stdin test fired StartWorkSession correctly while the same command in a multi-command Bash tool invocation did not).
- A user edits sessions.active_task_id directly via sqlite3.
- Any future code path that mutates session state without going through claude.go's PostToolUse handlers.

Symptom: 'endless session use' cd's to a stale worktree path; 'session show' reports stale active task. Easy for a human to misattribute or shrug off.

Fix: change the UserPromptSubmit handler to always call writeClaudeCompanion (drop the CompanionExists guard). Cost: one atomic JSON write per prompt — sub-millisecond. Benefit: the companion file is at most one prompt out of date with the DB regardless of which intermediate hook events fire or miss.

Implementation:
- Replace the 'if !exists { write }' block with an unconditional write.
- writeClaudeCompanion already overwrites atomically (tempfile + rename) — no concurrency concern.
- Delete CompanionExists if no other caller remains; otherwise keep it.

Verification:
- Manually mutate sessions.active_task_id in DB. Submit any prompt. Companion file's worktree_path updates within that turn.
- Run 'endless task start E-NNN' through whatever bash form previously failed to fire the hook. Within one prompt, companion catches up.
- Existing companion-file tests still pass; the backfill behavior subsumes the new always-write behavior.

Origin: E-1027/E-1028 verification surfaced that the hook can miss task-state events depending on Bash tool_input format. Rather than chase that intermittent miss, kill the entire drift class. Mike's question after I 'flagged' it: 'how will we know when it recurs?' — we won't; better to make recurrence harmless.
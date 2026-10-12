## PreCompact belongs in SYNC_EVENTS too, not just CLAUDE_HOOK_EVENTS

An async entry runs in the background and the thing it precedes proceeds
immediately, so a parse meant to capture the transcript before compaction would
race the compaction it precedes. This is the reasoning E-1901 used to make
`Stop` synchronous, and it applies more sharply here because the event name
states the ordering. The reference also notes `PreCompact` is an event whose
exit code can block compaction (exit 2); Endless's handler returns nil, so a
synchronous entry orders the parse without ever blocking a compaction.

## Distribution needs no new mechanism

`_repair_missing_hook_events` adds an endless-go entry for any listed event that
has none, and `_repair_hook_async_flags` corrects a flag that disagrees with
`SYNC_EVENTS`. Both are idempotent and both run on every
`endless setup claude-hook`. `Notification` (E-2091) and `StopFailure` (E-2145)
reached existing installs exactly this way; this change is the third instance of
a pattern already written and tested.

## ExitPlanMode is deleted, not relocated

Mike's answer on 2026-10-12, asked directly: the exit-plan moment is not
needed. What the dead handler would emit is a context injection reading "Plan
accepted and synced to Endless. N active item(s) tracked." — and
`handlePostToolUse` already emits "Plan file synced to Endless. N active item(s)
tracked." when a plan file is written. Auto-import is disabled on both paths
(E-2074) and sessions save plans explicitly with
`endless task update <id> --plan-file`, so the injection is informational and
duplicated.

`handlePostToolUse` routes on `payload.ToolName`, so if the moment is ever
wanted it is a branch beside the existing `Write` one. `latestPlanFile` goes
with the handler: `handleExitPlanMode` is its only caller. `isPlanFile` stays —
`handlePostToolUse` uses it.

## Rejected: installing more of the 32 while here

A blanket install was considered and rejected by Mike on 2026-10-12. Every hook
invocation runs the common path before the dispatch switch — the throttled
activity record and `monitor.TouchSession`, which writes process and
`last_activity` — so an installed event with no handler is not free: it refreshes
the session's activity whenever it fires. On a frequent event that would make a
session read active when nothing is happening, which is the failure the
`StopFailure` comment in `setup.py` already warns about. Each event gets paired
with a handler or left out; which ones deserve one is a separate brainstorm.

## The risk a synchronous entry carries, and why it is acceptable

Per the reference, `PreCompact` is an event where exit code 2 blocks
compaction. The handler returns nil, so the normal path exits 0. The path that
does not is a database failure in the common code before the dispatch switch,
which returns a refusal rather than nil. That is worth confirming during
verification: a hook that fails must not leave the user unable to compact. If
`endless-go` exits 2 on that path, the entry needs the handler to swallow the
error for this event specifically — ordering a parse is not worth blocking a
compaction over.

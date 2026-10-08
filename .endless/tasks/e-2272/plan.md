Build the job exactly as settled in E-2269's outcome (read it first; it holds the PoC results and the rejected routes). In short:

1. Per-project opt-in config (like auto_spawn.enabled); off by default.
2. Job: on each new incident in errors list, read its attribution (E-2268) and route:
   - Attributed session live: wait until Endless's hook state says idle, then deliver "we think incident <id> is yours; investigate; run endless errors accept <id> or endless errors decline <id> --reason ...". Delivery: a throwaway `claude -p "<prompt>" --allowedTools "SendMessage,ListAgents,ToolSearch"`, run from a recognisably named working folder, finding the target in ListAgents by its tmux pane. A held message (delivery notice) counts as undelivered.
   - Attributed session ended, transcript exists: resume it in a new, unfocused tmux window with the message as its first prompt, without revisiting the task (the session sets revisit itself if it accepts). Counts as a spawn.
   - Otherwise, or on decline, or on idle-without-answer: file one bugfix task per fingerprint (a recurrence updates it), context = incident detail + occurrences + attribution + a pointer to ED-1614, then spawn it.
   - Spawns throttled to one at a time, the rest queued; messages are not throttled.
   - Incidents attributed to a fix session are recorded on that fix task, never spawned.
3. `endless errors accept <id>` / `endless errors decline <id> --reason` record which session took or refused an incident (through the event pipeline).
4. Error display: a 1-character glyph on accepted incidents; `endless session goto --error-fix <id>` jumps to the accepting session.
5. `endless errors escalate <id>`: deliver now, skipping wait-for-idle.
6. Document the minimum Claude Code version for cross-session SendMessage.

## Decisions taken during implementation (Mike, 2026-10-08)

- Accept/decline are a DIRECT write to the machine-local fault store, like `errors clear` — not ledgered events. Incidents are machine-local; their ids mean nothing in the ledger (supersedes "through the event pipeline" in item 3).
- A fingerprint recurring after its fix task has settled (confirmed/assumed/declined/...): file a NEW bugfix task that `--cleans-up` the settled one, and spawn it.
- Throttle unit: one OUTSTANDING triage-started session at a time, machine-wide — no new spawn/resume while a spawned fix task is not yet unverified-or-beyond, or a resumed session has not answered.
- Backlog: only incidents first seen after the job first saw the project opted in.

## Implementation shape

- Config `fault_triage` {enabled (project), interval (user, default 1m)}.
- Migration 00017: `error_triage` (one row per incident: state, asked session, delivered_at, fix_task_id, accepted/declined session+time, decline reason, escalated_at, note) and `fault_triage_projects` (project_id, enabled_at watermark).
- `internal/faults/triage.go`: triage-state reads/writes. `internal/triagejob`: the job — a pure route decision per incident plus effect seams (sender, resume, file, spawn).
- Live delivery: sender run from `<config dir>/endless-triage`, told the target's tmux pane; it answers DELIVERED / HELD / NOT_FOUND / FAILED; anything but DELIVERED falls back to file-and-spawn.
- Idle-without-answer: delivered, then the session is idle with last_activity later than delivery (+grace), with no accept/decline.
- Resume: `endless session goto ES-N --resume --no-revisit` gains hidden `--background --target-session $N --prompt-file F` (no focus, no $TMUX needed). Target tmux session resolved like auto_spawn.target=active.
- File: `endless --no-session task add --type bugfix` with generated context and a fixed plan (spawn requires a plan), unrated (the rater job rates it); the context tells the session to `errors accept` first. Recurrence/fix-session incidents regenerate the fix task's context listing every linked incident.
- `errors list` gains a 1-char ✓ column marker on accepted incidents; `errors show` names the accepting/declining session. Hidden `endless-go errors fixer <id>` backs `session goto --error-fix <id>`.
- Escalate: runs the route for that one incident now, skipping wait-for-idle (throttle still applies to spawns).
- Docs: docs/errors.md "Triage" section incl. Claude Code version note; config README; guide reference.

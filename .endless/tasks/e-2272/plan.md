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

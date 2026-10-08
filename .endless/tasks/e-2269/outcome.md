# E-2269 synthesis: routing each new error to a session that fixes it

## Goal (corrected)
An error reaches Mike as soon as it happens, and work on it starts as soon as it happens. Nothing is held back from him. Scope: anything that lands in `errors list`. Making more failures become incidents is out of scope.

## Settled
- **Which errors:** every new incident. The plan's Q1 "not actionable" catalog flag is dropped: a condition that needs no fix should not be an error at all (ED-1614), so when the diagnosis is "routine, not a fault", the fix session changes the producer (the plan's Q7). Over time this makes the error stream trustworthy.
- **Attribution first:** E-2268 (record which task and session raised each fault) is the prerequisite.
- **Ask, don't assign:** the job tells the suspected session "we THINK this incident is yours" and asks it to confirm. In Mike's experience sessions are good at telling whether a bug is theirs. An error counts as handled only once a session accepts it. A decline, or the session going idle again without answering, sends the incident to file-and-spawn.
- **Recording agreement:** the session runs `endless errors accept <id>` or `endless errors decline <id> --reason "..."`. The sender is short-lived, so there is no reply channel. This record drives the display and the job's fallback.
- **Live session:** wait until it is idle, using Endless's own hook-recorded working/idle state, then deliver.
- **Ended session whose transcript exists:** resume it with `session goto --resume --no-revisit` semantics: a new tmux window, NOT focused (a background job never steals Mike's screen), with the message as the first prompt (`claude --resume <id> "<prompt>"`). The session decides. If it accepts, it sets its own task to `revisit` and fixes there.
- **One session, one task:** a session implements only its own task. A resumed session can never be told to file a new bugfix task and work it; working a different task means spawning it.
- **Not attributed / declined / transcript gone / message held:** file one bugfix task per fingerprint, linked to the incident; a recurrence updates that task rather than filing another. The task context gets the incident detail, occurrences and attribution (gathered without a model; no model diagnosis in the job). Spawn it.
- **Throttle:** one auto-spawn at a time; the rest queue. Messages to live sessions are not throttled. Resuming an ended session counts as a spawn.
- **Loop guard:** never spawn for an incident attributed to a fix session; record it on that fix task instead.
- **Opt-in:** per-project config, like auto_spawn.enabled (PRODUCT: someone else's project never starts spawning sessions unasked).
- **`errors escalate <id>`:** a user command that delivers right away, skipping wait-for-idle. As immediate as possible; nothing more.
- **Visibility:** a 1-character glyph in the error display once a session has accepted the incident, and `session goto --error-fix <id>` to jump to that session. Mike doesn't need "being fixed by" text: once every error is worked on, seeing an error means it is being handled.

## Delivery to a live session: PoC results (run in this session, 2026-10-07)
- Rejected: tmux send-keys/paste into a pane (unreliable, causes usability problems). Rejected: `claude -p --resume <target>` (a second process holding the same transcript; the live pane never sees what it did).
- Works: a throwaway headless sender, acting only as the sender:
  `claude -p "<prompt telling it to SendMessage to <target>>" --allowedTools "SendMessage,ListAgents,ToolSearch"`
  The prompt must come before `--allowedTools` (the flag takes several values and swallowed the prompt otherwise).
- **Idle target:** the message woke the session as a turn of its own, with nobody typing.
- **Busy target:** the message didn't break into the running command. It arrived at the next break between tool calls, with "after completing your current task, decide whether/how to respond". So wait-for-idle is what keeps routine errors out of a session's work; escalate skips it.
- The sender can exit right after SendMessage; delivery still happens.
- Caveats:
  - The sender is named after its working folder (`scratchpad-f6`): run it from a folder with a recognisable name (e.g. endless-triage).
  - ListAgents prints each peer's tmux pane (`tmux active:@114.%373`). Endless already knows session panes, so find the target by pane rather than by name; that covers older `e-NNNN-xx` names and hand-claimed sessions.
  - ListAgents showed an idle session running a background shell as `shell`, not `idle`; use Endless's own state for idleness.
  - Not tested: SendMessage says a message can be held ("usually a different permission mode"), and the sender then gets a delivery notice. Treat a held message as undelivered and fall back to file-and-spawn.
  - PRODUCT: this depends on Claude Code's cross-session SendMessage; note the minimum version.

## Follow-ups
- E-2272, blocked by E-2268: build the error triage job described above, including errors accept/decline, the glyph, `goto --error-fix` and `errors escalate` (per ED-1550, one cause, one task).
- E-2271 (the PoC task) is answered by the results above.

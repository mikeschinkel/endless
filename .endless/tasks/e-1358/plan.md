## Approach

1. Hook error grading (internal/hookcmd).
   - Walk every log.Printf in internal/hookcmd and classify each error site as state (tracking, locks, gates, relay checkpoint, report exemption, revisit gate, prompt staging, primed resume, session wake) or side work (DB backup, A/B rate, A/B pick, report labels, known label tokens). Pure informational log lines are not error sites. Record the classification in the PR description.
   - State sites return their error up to Run, wrapped in the eventError the handler already uses, so hookExitCode grades them per event.
   - Side-work sites keep logging and also call recordHookFault so the failure leaves a fault row.
2. Stop/SubagentStop.
   - Probe first: a scratch-repo Stop hook that exits 2 with a marker on stderr; check whether the marker shows up in the model's next turn. Record the result in the task notes.
   - If it does: parse stop_hook_active from the payload; hookExitCode returns exitBlocking when it is false and exitNonBlocking when true.
   - If it does not: leave stopEvents as they are.
3. Schema refusals. In Run, monitor.RecordSchemaRefusal still records the one fingerprinted fault, but the hook then exits with hookExitCode instead of returning silently, and prints the refusal through the same reader routing as other errors.
4. Bypass (Python CLI `endless hook bypass`, Go reader in hookcmd).
   - State file: hook-bypass.json in the main config directory (config.main_config_dir), holding entries of Claude session id or "all" with an expiry.
   - `endless hook bypass <session_ref> [--for <duration>]`: the session ref is an ES ref or a Claude session id. An ES ref is resolved through the database; if that fails, refuse and say to pass the Claude session id instead. Without --for the entry lasts until the session ends.
   - `endless hook bypass --all [--for <duration>]`: default duration one hour.
   - `endless hook bypass --clear [<session_ref> | --all]`.
   - Neither a ref nor --all: refuse with usage.
   - Refuse when agentenv says an agent is running the command.
   - Go side: Run reads the file before grading an error (file read only, no database). Expired entries are ignored. The SessionEnd handler removes that session's entry.
   - Under a bypass an error still logs and records its fault, then exits 1 with a one-line "hook error bypassed" notice instead of exit 2. Gates are unaffected because they are not errors.
   - haltNotice names `endless hook bypass` as what a person runs to let a fix session through.
5. Setup (src/endless/setup.py, `endless setup claude-hook`).
   - Check `claude --version`; refuse below 2.1.295 naming both versions, installing nothing.
   - Write onFailure "block" on the PreToolUse and UserPromptSubmit entries.
   - Rewrite existing Endless entries in place, so re-running setup upgrades an older install.
   - Setup output and the guide name `claude --settings '{"disableAllHooks":true}'` as the escape when the hook binary itself is missing.
6. Tests: Go tests for grading (state site, side site, Stop with and without stop_hook_active, schema refusal, bypass session/all/expired); Python tests for the bypass command's argument rules, agent refusal and file contents, and for setup's version check and in-place rewrite.

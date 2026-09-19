You are spawned to verify the E-989 companion-file mechanism.

Do exactly this and nothing else:

1. Run: `ls -la .endless/sessions/`
2. Find the file matching `claude-*.json` — it should exist for your own session.
3. Run: `cat .endless/sessions/claude-*.json`
4. State whether all expected fields are present: `harness`, `harness_session_id`, `endless_session_id`, `pane_id`, `cwd`, `pid`, `started_at`.
5. Print the literal string: `E989_VERIFICATION_OK` if all fields are present, otherwise `E989_VERIFICATION_FAILED` with reasons.
6. Stop. Do not start any tasks. Do not edit any files. Do not run anything else.

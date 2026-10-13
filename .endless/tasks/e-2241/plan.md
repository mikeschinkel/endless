## Approach

`monitor.ParseTranscript` stops discarding user messages. It stores every user message
except tool results (`{"tool_use_id"` — unchanged), and assigns a role:

- `meta` — Claude Code's own text: the line carries `isMeta: true`, or the message opens
  with one of Claude Code's wrapper tags: `local-command-caveat`, `command-name`,
  `command-message`, `local-command-stdout`, `local-command-stderr`, `bash-input`,
  `bash-stdout`, `bash-stderr`, `task-notification`, `system-reminder`, `channel`.
  The tag list is one Go slice in `internal/monitor/transcript.go`.
- `user` — everything else, including prompts that open with `<pasted_content …>` or with any
  other `<` (`<sigh>`).

Content is stored exactly as written in the transcript: no tag stripping, no
reconstruction of `/name args` or `! command` — those are stored inside their
`<command-name>` / `<bash-input>` wrappers under role `meta`.

`session messages` and `session search` (`src/endless/session_cmd.py`) currently label
every non-`user`, non-`tool_use` row "Claude". They gain a `meta` branch labelled "Meta"
(dim), so Claude Code's text is never shown as either Mike's or Claude's.

No schema change: `session_messages.role` is unconstrained TEXT.

## Verification

- Go tests in `internal/monitor/transcript_test.go` feed a fixture transcript holding: a
  paste-led prompt with typed text after the paste, a `<sigh>`-led prompt, one message per
  wrapper tag, an `isMeta` line with no tag, and a tool result. They assert the roles and the
  verbatim content stored, and that the tool result is absent.
- A Python test asserts `session messages` renders a `meta` row with the "Meta" label.
- `.endless/tasks/e-2241/verify.sh` runs both, plus a check that a real `endless-go` hook
  run on the fixture through a sandbox database stores the paste-led prompt.

## Out of scope

- Re-importing existing sessions — E-2312 (blocked by this task) adds the command and runs it once.
- The partial-line, 1 MB-line and ignored-insert-error problems — E-2311.
- Storing repeated command bodies once — E-2315.

# Fix UserPromptSubmit / SessionStart additionalContext never reaching the agent

## Symptom

Everything Endless injects on `UserPromptSubmit` and `SessionStart` is produced
correctly, written to stdout, and never arrives in the agent's context. The
`PostToolUse` injection on the same binary arrives fine.

Confirmed live on 2026-08-20 in session 1048 (`3621a2e0-…`), holding E-1917:

1. The user ran `endless task assume 1917` from their terminal.
2. Notice 758 was written for session 1048 with `changed_by_session = NULL`.
3. On the session's next turn the hook rendered
   `FYI — E-1917 status: unverified → assumed`, appended it to
   `.endless/logs/session-notices.jsonl` at `10:42:09Z`, and set `notified = 1`.
4. The agent in session 1048 did not receive it, and said so in the same turn
   the user asked.

The active-task line is the stronger evidence, because it ships on EVERY
`UserPromptSubmit` regardless of notices: `Active task: E-1917 (…) — …` has not
appeared in that session's context once, across a full working session.

## Why this is not "the agent ignored it"

`PostToolUse` `additionalContext` from the same binary DOES arrive: the
claim-into-a-live-session handoff (E-1822) reached session 1048 verbatim and was
acted on. So the hook runs, produces text, and the harness applies it — for one
event and not the other.

The two events differ in exactly one way: the JSON shape written to stdout.

- `PreToolUse` (`internal/hookcmd/claude.go:88`) and `PostToolUse` (`:652`) nest
  the payload under `hookSpecificOutput`.
- `UserPromptSubmit` and `SessionStart` write a bare top-level
  `hookResponse{AdditionalContext: …}` — `{"additionalContext":"…"}`.

## Leading hypothesis

The bare top-level `{"additionalContext": …}` is not a shape the Claude Code
hooks contract recognises for `UserPromptSubmit` / `SessionStart`. The harness
parses stdout as JSON, finds no recognised directive, and discards it silently —
no error, exit 0, nothing in the transcript.

The belief that the top-level shape is correct is asserted in a code comment
(`claude.go:650`, "Unlike the top-level hookResponse.AdditionalContext used on
SessionStart / UserPromptSubmit…") and was repeated in E-1917's own analysis. It
appears never to have been checked against the contract — only against itself.

## Confirming it

Read the current Claude Code hooks reference for `UserPromptSubmit` and
`SessionStart` and establish which of these the harness accepts:

- `{"hookSpecificOutput": {"hookEventName": "UserPromptSubmit",
   "additionalContext": "…"}}`
- plain text on stdout with exit 0 (documented for `UserPromptSubmit`, where
  stdout is added to context directly)
- the bare `{"additionalContext": "…"}` currently emitted

Then confirm empirically: emit the accepted shape from the worktree binary,
start a session against it, and check whether the active-task line appears in
context. The active-task line is the right probe — it ships every turn and needs
no notice to exist.

## The fix

Emit the accepted shape for `UserPromptSubmit` and `SessionStart`, matching what
`PreToolUse` / `PostToolUse` already do. `hookResponse` and its two call sites
(`handleUserPromptSubmit`, `handleTaskContextInjection`) are the only things that
change; nothing about what is composed changes, only how it is framed on stdout.

If plain-text-on-stdout turns out to be the accepted form for `UserPromptSubmit`,
prefer the JSON form anyway for consistency with the other events, provided the
contract accepts it.

## Blast radius — everything on these two events

This is not scoped to E-1917's notices. Every `UserPromptSubmit` /
`SessionStart` injection has been silently discarded:

- the `endless guide` pointer for a new session (E-1854)
- the one-shot full task-list context
- the per-turn active-task line, including current status/tier/phase (E-1917)
- change notices (E-1917)
- the pending inter-session message banner
- the report-channel coverage rule shipped on every SessionStart

Each of these was built, tested at the unit level, and shipped as working.

## Data loss, and what to do about it

Notices are marked `notified = 1` at render time, so a notice that never landed
is consumed and nothing will re-tell the session. 158 rows are currently
`notified = 1` and must be assumed undelivered.

Do NOT reset them to `notified = 0` as part of this fix. They are stale — their
tasks have moved on since — and replaying them would flood every returning
session with historical transitions. Leave them consumed; the fix only has to
make the next one land.

The ordering in `deliverNotices` is already correct for this (render → log →
mark), and stays as is. Marking-before-arrival is unavoidable from inside the
hook: the hook cannot observe whether the harness applied its output. The
delivery log is the compensating control, and it is what made this diagnosable
at all.

## Acceptance criteria

1. A session whose held task is changed from another terminal receives the
   `FYI — …` line on its next turn, and says so unprompted.
2. `Active task: E-<id> (<status> · tier N · <phase>) — <title>.` is present in
   the agent's context on an ordinary turn.
3. A fresh session receives the `endless guide` pointer and the task-list
   context on SessionStart.
4. `tests/tasks/e-1917-verify.sh` still passes 21/21, run BOTH with the agent
   environment and under
   `env -u CLAUDE_CODE_ENTRYPOINT -u CLAUDECODE -u CLAUDE_CODE_SESSION_ID`.
5. A regression test pins the emitted JSON shape for `UserPromptSubmit` and
   `SessionStart` against the contract, so a future edit cannot quietly revert
   to a shape the harness drops.

## Note on why the existing tests did not catch this

Every test asserts on what the hook WRITES, never on what the harness ACCEPTS.
`tests/tasks/e-1917-verify.sh` parses the hook's stdout itself and confirms the
text is there — which it always was. No test could distinguish "emitted" from
"delivered", and the transcript is not an oracle either: injected
`additionalContext` is not recorded there (verified — the active-task line
appears 0 times in a session's transcript while shipping on every turn), so its
absence proves nothing in either direction.

That is why criterion 5 pins the SHAPE rather than the presence of text, and why
criteria 1-3 are stated as things a human observes in a live session.

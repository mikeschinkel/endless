# Plan: remove `endless task chat` and chat-only session mode

Decided by Mike, 2026-09-26: the command predates `endless session` and
`task spawn`, which now handle every session, and it is no longer relevant.
Remove it outright. Pre-beta, so no deprecation period and no config migration.

## Why it is safe to remove

- A session that only talks never writes, and every gate that could refuse it
  fires on WRITE tools only (the declaration gate, the worktree gate, and since
  E-1983's reopen the unbound-worktree gate). So "chat mode" protects nothing:
  a conversation needs no mode.
- It appears not to work anyway. The declaration gate still suggests
  `endless task chat` as a way out, but since E-2093 that gate admits only a
  session that HOLDS A TASK, and a chat session never holds one. Read from the
  code, not tested — and not worth testing, since the command is going.
- `task_cmd.start_chat` inserts a NEW sessions row with a random UUID, which is
  not the running Claude session. The only thing that ever touched the real
  session was the hook's text-matched `StartChatSession` call.

## Ownership split with E-2177 — read before editing

E-2177 removes the hook's text-inferred state writes for claim and confirm and
deletes the dead matcher defaults. THIS task owns every chat-related line in
those same places, so the two never edit the same lines:

- The `actionChat` detection block in `handlePostToolUseSession`, and the
  `actionChat` constant.
- The `chat` entry in `DEFAULT_MATCHERS` (src/endless/matchers.py).

Whichever lands second rebases over the other; the overlap is adjacent lines in
`handlePostToolUseSession` and `DEFAULT_MATCHERS`, nothing semantic.

## Work

**Python**
- `src/endless/cli.py` — delete the `task chat` command (`task_chat`).
- `src/endless/task_cmd.py` — delete `start_chat`.
- `src/endless/matchers.py` — delete the `chat` default matcher.

**Go**
- `internal/hookcmd/claude.go`
  - delete the `actionChat` detection in `handlePostToolUseSession` and the
    `actionChat` constant;
  - delete the `endless task chat` line from the declaration gate's
    "Run one of:" list;
  - fix the comment above the detection block that still says
    "claim/complete/chat".
- `internal/monitor/session.go` — delete `StartChatSession` (no callers remain).
- `internal/monitor/task.go` — the declaration-context text's step 4 tells the
  agent to run `endless task chat` for a conversation. Replace with: no action is
  needed for a conversation; nothing is refused until you write.
- `internal/sessionstate/transitions.go` — two `Trigger:` strings name
  `hook claude chat → monitor.StartChatSession` and `task chat →
  task_cmd.start_chat`. Remove those clauses and leave the rest of each trigger.
  If sessionstate renders a generated artifact from this table, regenerate it.

**Tests**
- `internal/monitor/session_lifecycle_test.go` — delete
  `TestStartChatSession_InsertWithNullTask` and
  `TestStartChatSession_UpsertKeepsTaskID`, and the `StartChatSession` call near
  line 616 (check what that test is actually about first; remove only the chat
  step if the test covers something else).
- `internal/hookcmd/declaration_gate_test.go` — drop `endless task chat` from the
  expected message.
- `tests/test_agent_help.py` — uses `endless task chat` as a sample command;
  switch it to another real command.

**Docs**
- `docs/guide/tasks.md` — the "Sessions and chat" section: remove the chat line,
  and retitle the section if chat was its reason to exist.
- `docs/guide/orchestration.md` — the sentence stating `task chat` no longer
  unbinds a session: delete it.
- Run `just guide-index` if a guide heading changed, then `just guide-check`.

**Not touched:** landed task plans and analyses under `.endless/tasks/`, and
decision mirrors (ED-1560) that mention chat. They are history.

## Verification

- `grep -rn "task chat\|start_chat\|StartChatSession\|actionChat"` over `src/`,
  `internal/`, `cmd/`, `docs/guide/` and `tests/` returns nothing.
- `endless task chat` exits as an unknown command.
- The declaration gate's refusal no longer offers chat, and still offers
  `endless task claim` and `endless task show`.
- `just build`, `just test`, `just test-go`, `just guide-check` green.

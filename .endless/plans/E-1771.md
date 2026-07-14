# `endless task report <id>` — steering-prompt reporting command

Delivered design (revised from the original `--xml` spec during co-design with
Mike; supersedes the earlier `--text`). Rationale for the reframe lives in the
E-1760 outcome and this task's analysis; ED-1531 was updated to match.

## What it is

End-of-session reports stop being freeform prose the agent composes. The agent
runs `endless task report <id>`; the command computes the facts it can, gates
the one free-text escape hatch, and prints a **steering prompt** that tells the
agent to relay only those facts to the user — plainly, no ceremony.

The original single `--xml` document conflated three concerns; splitting them
collapsed the design:

1. **stdout is a steering prompt** (plain text), not a verbatim relay and not
   data. It hands the agent the relevant facts and steers a terse report.
2. **No intermediate XML.** Facts are read from git/DB into a struct; the
   agent's non-computable input is a small JSON payload. Nothing serializes to
   XML — the `--xml` surface the title named is retired.
3. **No persistence here.** Capturing the facts as queryable rows is E-1777.

## Command

```
endless task report <id> [--json '<payload>' | --json-file <path>]
```

Positional `<id>`; status-agnostic (does not change status). Normal path is no
payload at all → zero free-text entries → zero Haiku calls.

Payload (all optional):
```json
{
  "notes":     [{"kind": "anomaly|discovery", "text": "…"}],
  "questions": [{"text": "…", "type": "text|integer|real|boolean|choice", "style": "…"}]
}
```
Malformed JSON / unknown key / out-of-set kind|type → rejected (ClickException).

## Computed facts (steering prompt only; not persisted)

- **status**, **landed**, **successors** (tasks this task `blocks` + tasks that
  `cleans_up` it, each with status), **children** — one Go read via
  `session-query task-report --id N` (`internal/monitor/task_report.go`,
  `internal/sessionquerycmd`). No Python DB read (house rule).
- **git/worktree anomalies** — reuse the existing DB-free `worktree-anomalies`
  probe. Surfaced to the AGENT, with prompt guidance to mention to the user only
  when unexpected. Not persisted, not analytic.

Empty categories are omitted from the prompt (terse for the human).

Dropped vs. the original schema: `<verify>` (stop-gap pending E-1596's
verify.toml), `<deliverable present>` (redundant with `tasks.outcome`),
`commit ahead` (derivable, low snapshot value).

## Per-entry Haiku gate

Fires only when a payload entry exists. Reuses the hook-suppressed
`internal_claude.run_internal_claude(model="haiku")`. Notes get the note-check
("real non-computable fact, or absence of a problem?"); questions get the
question-check ("real decision, or restating settled state?"). A bounced entry
raises with agent-aware anti-rationalization phrasing. Model is FIXED (Haiku),
not a knob. **Fail-open** on Haiku unavailability — a handoff must always be
completable; the gate is a best-effort quality filter (diverges from the
verb-gate's fail-closed because a false block is far costlier here).

## Tunable wording (user lever)

The steer prompt and the two check prompts live in a user-editable JSONL file
(`report_prompts.py`), precedence project `.endless/report-prompts.jsonl` →
machine `~/.config/endless/report-prompts.jsonl` → embedded default (verbs.jsonl
precedent). Records `{"name":"steer|note-check|question-check","text":"…"}`.

## Files

- `internal/monitor/task_report.go` (+ test) — computed facts core.
- `internal/sessionquerycmd/session_query.go` (+ task_report_test.go) — the
  `task-report` JSON subcommand.
- `src/endless/report_cmd.py` — parse, gate, compute, render.
- `src/endless/report_prompts.py` — tunable config surface.
- `src/endless/cli.py` — the `task report` command.
- `tests/test_task_report.py`, `tests/tasks/e-1771-verify.sh`.

## Verify

`esu && ./tests/tasks/e-1771-verify.sh`

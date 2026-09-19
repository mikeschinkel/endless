# E-1853 — Verb gate: stop-and-ask on a classifier error, enforced by a gate

## 1. Classify into three outcomes
Rework `_check_verb_via_haiku` (src/endless/task_cmd.py) to return a three-state result instead of `(bool, str|None)`:
- **VERB** (clean `YES: <definition>`) + the definition.
- **NOT_VERB** — the reply is an explicitly matched clean `NO`.
- **UNDETERMINED** + a short reason (`timeout` | `nonzero-exit` | `missing-binary` | `malformed`) for the human-facing message.

Rules:
- Match a clean `NO` explicitly. A reply that is neither a clean `YES:` nor a clean `NO` (hedged, prose, empty, markdown) is **UNDETERMINED (malformed)** — NOT `NOT_VERB`. (Today "not `YES:`" is misread as NO — the core bug.)
- Retry twice on a transient UNDETERMINED (`timeout` / `nonzero-exit` / `malformed`) before settling UNDETERMINED. `missing-binary` is not retried.

## 2. Behavior in `validate_title`
- **VERB** → register via `matchers.add_verb`, pass. (unchanged)
- **NOT_VERB** → raise the existing "title must start with an actionable verb" error; the agent rewrites the title. (unchanged)
- **UNDETERMINED** → do NOT reword, register, or guess. Enter the enforced stop-and-ask gate (§3).

## 3. Enforced gate (a message alone is ignorable — the agent will reword anyway)
On UNDETERMINED, `endless task add` / `task update`:
1. Makes NO change to the task.
2. Sets a lightweight per-session flag marking that a verb-classifier stop is pending (a column / state on the session row that hit the error, carrying the failure reason for the message — no new table). Unshipped software, so add the column directly; no migration.
3. Exits non-zero with a message that names the failure reason and says: STOP, tell the user what failed, do not reword.

Enforcement lives in the Claude hook (internal/hookcmd/claude.go), extending the existing gate (which already blocks Write/Edit until a task is claimed):
- **PreToolUse**: while the flag is set for the current session, BLOCK any `endless task add` / `task update` that carries a title, with the stop message. This closes the reword bypass — the agent cannot route around the stop by picking a different word. The block is title-scoped (not a full tool freeze), so the agent can still surface the situation to the user; it simply cannot create a mis-titled task.
- **UserPromptSubmit**: clear the flag. The gate lifts the moment the human replies, so resolution is the human answering in chat — not a special command.

## 4. Human resolution — in chat, no new command
The human's reply clears the flag (via UserPromptSubmit) and directs the agent:
- "it's a verb" → the agent registers it with the existing `endless verb add <word> --definition "<d>"`, then retries the title. It now passes with no classifier call (the word is registered).
- "rewrite it" → the agent rewrites the title with a real verb.
No `verb resolve` command and no pending-decision table: existing `verb add` covers approval; a chat reply covers the rest.

## 5. Tests — tests/tasks/e-1853-verify.sh
- **Classifier (unit):** VERB / NOT_VERB / UNDETERMINED for clean YES, clean NO, malformed-non-NO (→ UNDETERMINED, not NOT_VERB), timeout, nonzero-exit, missing-binary. Retry fires on transient UNDETERMINED, not on `missing-binary` or `NO`. (Fake the `claude` subprocess.)
- **Gate:** on UNDETERMINED, `task add` refuses, creates no task, sets the session flag; a subsequent titled `task add`/`update` is blocked by the PreToolUse hook while the flag is set; the block clears after a `UserPromptSubmit`.
- **Resolution:** once the flag is cleared, the existing `endless verb add <word>` registers the verb and the original title passes; the reword path is blocked only while the flag is set.
- **Regression:** clean YES still auto-registers + passes; clean NO still rejects with the verb error.

## Out of scope
Punctuation/dedup on the verb write path (E-1837). Optional apfel/local classifier as a Haiku alternative (E-1849) — a later swap behind the same three-state result.

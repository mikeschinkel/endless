# Plan — E-1901: enforce verbatim report relay with a Stop-hook gate

**Status: implemented.** Rebased onto main (E-1803 landed), so `taskReportRe`,
the PostToolUse relay nudge, and `reportChannelRule` are reused, not reinvented.

## Decisions taken (were open in the plan)

1. **Verify command enters via the `--json` payload** as a `verify` key —
   symmetric with `notes`/`questions`, no `tasks` schema change, agent-supplied
   at report time. NOT Haiku-gated: a command is not prose, and asking a model
   whether a shell command "reads as ceremony" would invent a failure mode
   rather than catch one. Multi-line values are rejected — a checklist wearing a
   field's clothes is the ceremony this replaces.
2. **Empty report renders a fixed sanctioned line, `Nothing to report.`** — not
   silence. Silence is indistinguishable from a stalled agent; one fixed line
   proves the turn ended deliberately and states the null result, and reads
   correctly at a mid-session checkpoint as well as at session end.
3. **The comparison input is `last_assistant_message`**, the Stop-specific
   payload field. The hooks reference explicitly says hooks needing the final
   assistant text must use it rather than reading the transcript, which lags.
   This removed the transcript-parse race entirely.

## What shipped

### Part 1 — invert the default (Python)

- `--json` accepts `verify`; `_parse_verify` validates it.
- `_render_facts` split into `_render_sanctioned` (the user-facing block the
  gate enforces byte-for-byte) and `_render_agent_notes` (the worktree-anomaly
  advisory). The split is forced, not cosmetic: anomalies are conditional
  ("surface only if unexpected") while the sanctioned block is unconditional, so
  including them would compel the relay of noise, and leaving them inside while
  excluding them from the sanctioned text would bounce any agent that correctly
  acted on one. They render outside the markers with the escape route named.
- Verify leads the block (it is the one line the user acts on) and is
  backticked, so an agent copying verbatim reproduces the formatting rather than
  adding it and tripping the gate.
- Empty block → the `nothing-to-report` prompt text. Both cases now render,
  print, and arm identically — one steer, one comparison, no special case.
- `steer-empty` renamed `nothing-to-report`: its role changed from steering the
  agent to BEING the sanctioned message. No override file existed at either the
  machine or project layer, so nothing depended on the old name.
- `STEER` gained `----- BEGIN REPORT -----` / `----- END REPORT -----` markers
  and now names the gate, so the agent knows the bounce is coming.

### Part 2 — record the checkpoint

- `gatekind.GateKindRelay = 2` (slug `relay`), seeded in `schema.sql`, plus
  `internal/schema/changes/e-1901-relay-gate.sql` for the populated real DB.
- `session_gates` gained `sanctioned_text` and `bounces`. Reused rather than
  adding a table: it is already "a pending interception for a session" with a
  kind_id discriminator and named per-kind subject columns. It differs from
  `revisit` only in which hook consumes it (Stop vs PreToolUse); the table
  comment was broadened to say so.
- `monitor`: `SetRelayCheckpoint` / `PendingRelayCheckpoint` /
  `ClearRelayCheckpoint` / `BumpRelayBounce`, mirroring the revisit helpers
  including supersede-on-insert.
- `session-query relay-checkpoint --session-id N`, sanctioned text on **stdin** —
  a report block is unbounded, and a truncated sanctioned text would gate
  against the wrong string forever.
- `report_item` records via `_current_endless_session_id()`. Best-effort: an
  unresolved session means no checkpoint and no gate, never a failed report.

### Part 3 — gate at Stop

- `internal/hookcmd/relay_gate.go`. The comparison is pure and DB-free so the
  part that must be exactly right is testable without infrastructure.
- Normalization strips only meaningless variation — whitespace, blank lines,
  code fences, block markers — and never a line containing words. Fences and
  markers are tolerated deliberately: a false positive teaches the agent the
  gate is noise, and the deterrent dies with the trust.
- Compliant = normalized-equal OR normalized-empty. Silence is not what the
  report asked for, but the offense being caught is APPENDING; bouncing silence
  would punish an agent for under-speaking while trying to obey.
- Violation emits `decision:"block"` + `reason` (to Claude) + `systemMessage`
  (to the user). Both, because a violation only the agent sees is one it can
  privately reinterpret — the exact failure that made E-1803's nudge insufficient.
- Loop guard: `bounces` capped at 2, then the gate clears and **announces** that
  it gave up. `stop_hook_active` is undocumented, so the counter is the primary
  guard.
- Subagents exempt (their final message returns to the parent, not the user).
- Cleared on `UserPromptSubmit`, which is also what keeps `FULL STATUS` working.

### Part 4 — Stop made synchronous

`Stop` moved into `SYNC_EVENTS`; an async hook cannot block and would have
disabled the gate invisibly. `setup_claude_hook` early-returns once a hook is
installed, so `_repair_hook_async_flags` was added — otherwise no existing
install would ever receive the fix.

## Incidental fix, in scope because the feature depended on it

`_ensure_claude_session_id` shelled to the bare PATH `endless-go` instead of
`_resolve_endless_go()` — an instance of the exact bug E-1510's routing exists to
prevent. Under `--db sandbox` the global binary's embedded enums are a different
baseline from the sandbox DB's, the fail-closed integrity check turns that into a
hard error, and this function swallowed it as "no session". That is why the relay
checkpoint could never arm in a self-dev worktree.

## Verification

`tests/tasks/e-1901-verify.sh` — 34 checks, fully scripted, single entry point.
Fail-fast on the comparison matrix, then schema, report shape, checkpoint
arming, and the whole mechanism driven through the real hook binary (compliant
allowed, violation blocked with both audiences named, cosmetic formatting not
bounced, bounce cap terminating, new prompt retiring the checkpoint).

Adjacent suites brought forward to the superseded contract: e-1880 (27 pass),
e-1772 (14), e-1782 (10), e-1803 (9), and e-1771's steer anchor.

## Landing notes

- Run `endless db apply-change internal/schema/changes/e-1901-relay-gate.sql`
  against the real DB, then `just install`. Order matters: the change seeds the
  `relay` gate_kinds row, and any older endless-go binary opening that DB will
  fail closed on the enum drift until the new binary is installed. This is the
  standard ED-1506 contract, not new to this task.
- Existing dev sandboxes need recreating (`sandbox init --force --mode worktree`
  then `just dev-sandbox-init`): `CREATE TABLE IF NOT EXISTS` cannot add columns
  and sandboxes never apply change files.
- `endless setup claude-hook` must be re-run once to flip Stop to sync; until
  then the gate is inert.

## Honest ceiling (carried from the analysis, not softened)

A hard gate on the report checkpoint, not a guarantee the urge never fires: a
re-prompted model can comply now and drift on a later turn, and the bounce cap
means a determined violation eventually lands. What changed is the cost —
appending stops being free and becomes "publicly named and bounced, every time."

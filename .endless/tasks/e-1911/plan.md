# Plan — E-1911: append a curated block, park the relay gate

One task, four parts, one land. It closes E-1785.

**Start from current main.** E-1901 landed (`19974ef`, `aa83a0c`), so
`internal/hookcmd/relay_gate.go` and the Stop-branch call site exist there but
NOT in the `task/1785-*` worktree, which predates them. Rebase before starting
or part 1 will look unimplementable.

## Part 1 — park the gate, in code

**Kill switch.** The gate has exactly one call site,
`internal/hookcmd/claude.go:268`:

```go
if handled, err := enforceRelayGate(payload); err != nil {
```

Add a package-level `const relayGateEnabled = false` in `relay_gate.go`, with a
comment stating it is parked pending the append contract, not abandoned, and
have `enforceRelayGate` return `(false, nil)` immediately when it is false.

Everything else stays: `relay_gate.go`, its unit tests, the checkpoint
recording, the `GateKindRelay` row, the `session_gates` columns. Reviving is a
one-line flip.

**Not in `settings.json`.** The `Stop` hook stays installed and stays
synchronous (E-1901 moved it into `SYNC_EVENTS` and added the async repair —
leave both). Disabling via config would drift per-machine and per-worktree, and
would silently un-fix itself on the next `setup` run.

**The compose-time nudge must go too.** `internal/hookcmd/claude.go:490`
(E-1803) injects "relay the report's output verbatim, add nothing" at
PostToolUse on `task report`. That is the same contract the gate enforced, and
it contradicts the append model directly — leaving it means the harness keeps
instructing the opposite of what the command now asks for. Rewrite it to state
the append contract: answer normally, then append the block after the
separator.

The literal at `claude.go:298` ("hand-write it — run `endless task report <id>`
and relay its output") needs the same treatment.

## Part 2 — the append contract

**Steer text** (`src/endless/report_cmd.py`, `report_prompts.py`). Today's
`STEER` says relay the block as the ENTIRE final message, byte for byte, and
names the Stop hook as enforcement. Replace with: your own answer comes first
and is not constrained; append the block below, unchanged, after the separator.
Drop the enforcement sentence — it will be false once part 1 lands, and a steer
that threatens a gate that no longer fires teaches the agent to discount steers.

**Separator.** One opening marker, not the current BEGIN/END pair — the block
runs to the end of the message, so a closing marker is redundant. It must be
machine-detectable so a future validator can find the block:

```
----- ENDLESS REPORT -----
```

Keep it a single fixed literal in one Go/Python-shared constant, not a tunable
prompt entry, since a validator has to match it exactly.

**Empty block still renders: separator + `Nothing to report.`** Not silence.
The null case must be *stated*, because an absent block is ambiguous in a way
that matters: the user cannot tell "there were no facts" from "the block failed
to render." A rendering bug that swallows the block would be invisible, and the
user would have to go check by hand — which is the cost this whole epic exists
to remove. One fixed line makes the null result an assertion rather than an
absence.

Keep the `nothing-to-report` prompt entry; it retains its purpose under the
append contract.

Note this does NOT reintroduce the failure E-1911 was filed for. That failure
was "nothing to report" *displacing* the agent's actual message. Under the
append contract the organic answer is always present above the separator, so
`Nothing to report.` now correctly means "the computed half found nothing to
add," not "there is nothing to say."

## Part 3 — stop duplicating `session status`

Three coordinated edits; the point is that the report and the handoff template
each demanded children because the other did.

- `src/endless/report_cmd.py:276–285` — delete the `Children:` branch. This is
  also the fix for the report listing E-1907 (parent E-1671) and E-1908 (root)
  as children of E-1785: the wrong list stops being computed at all.
- `internal/templatecmd/templates/handoff/epic.md.tmpl:29` — drop
  "Final message — lead with the state of the children."
- `internal/templatecmd/template_test.go:134` — the epic case asserts that exact
  string; update it.
- `internal/monitor/task_report.go:21` — the children fact and its comment
  become dead once nothing renders them; remove rather than leave orphaned.

## Part 4 — confirmed children are not hidden

`src/endless/task_cmd.py` lines 4400, 4462, 4596 all carry:

```sql
FROM tasks WHERE parent_id = ? AND status != 'confirmed'
```

Remove the filter. It excludes `confirmed` only, so an epic renders its
`obsolete` and `assumed` children but drops the ones that were verified and
landed — which is how E-1906 vanished from `task show E-1785 --children` while
four obsolete children stayed. Confirmed children are precisely what an epic
nearing completion needs to show.

## Explicitly NOT in scope

- **The veracity rule and mechanical citation check** (E-1876 part 2). Under the
  append contract the curated block is *computed by Endless*, not composed by
  the agent, so its facts are true by construction; the organic half is
  deliberately unconstrained. What remains is agent-authored `--json` note text,
  which is not worth a task of its own.
- **`task annotate`** (E-1876 part 3). It existed to house observations that
  suppression displaced. Nothing is suppressed now, so it has no purpose.
- **The plan-write do-not-recap reminder** (E-1876 part 4) — pure suppression,
  dead under the new direction.
- **Monitor checkpoint surfacing** (E-1876 part 5). The gate is parked; surface
  it if and when the gate returns.
- **Re-enabling the gate under the append contract.** Deliberately deferred —
  the whole point of parking it is to decide its shape once the append block
  has settled.

## Verification — `tests/tasks/e-1911-verify.sh`

- **Go.** `enforceRelayGate` returns not-handled while `relayGateEnabled` is
  false, for an input that would otherwise bounce (guards against the switch
  being wired to the wrong branch). The epic handoff template no longer contains
  the children directive. `Stop` is still in `SYNC_EVENTS`.
- **Python.** `task report` on an epic omits `Children:`; a report with no facts
  emits the separator followed by exactly `Nothing to report.`; a report with
  facts emits the separator followed by the block. The separator is present in
  BOTH cases — that is the property that makes a swallowed block detectable.
  `task show --children` includes a `confirmed` child.
- **End-to-end.** Through the real CLI → worktree `endless-go` → sandbox DB:
  `task report` on an epic with a confirmed child and an unrelated task
  elsewhere in the tree produces neither a children line nor a stray id.
- **Regression.** `task show E-1785 --children` lists E-1906.

## Sequencing

Part 1 first and alone — until the gate is parked and the nudge rewritten, any
session working this task is itself being told to replace its message, which is
the contract being removed. Parts 2–4 after, in any order.

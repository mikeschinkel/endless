# Converge "was this an agent?" onto `Actor.Harness`

## What this task is NOT

E-2006 was filed as a live bug: a user editing a task from a bare shell in a
sibling tmux pane has the change credited to the adjacent Claude session, so
`tasks_notify_sessions` reads it as self-made and drops the FYI.

**That cannot happen.** Commit `254aff3a` (the E-1917 follow-up) landed at
06:41 on 2026-08-20, about six hours BEFORE this task was filed at 12:24. It put
`agentRanThisCommand()` in front of the stamp in `stampTaskActor`: a change is
attributed only when the mutating process is the agent, so a human's shell
stamps NULL and every holder is notified. The filing session read the trigger's
SQL without the gate sitting above it — the analysis says as much
("UNREPRODUCED — inferred from reading code").

Observed live on 2026-08-20 in session 1120: the user set E-2005 to `confirmed`
from their own shell, `tasks.changed_by_session` for 2005 is NULL, and the
session received `FYI — E-2005 status: unverified → confirmed`.

## What the original analysis got backwards

It argued the premise was doubtful because "all 30 user-made task changes in the
ledger recorded a NULL `changed_by_session`, i.e. the user's shell did NOT
resolve to the adjacent Claude session."

Those NULLs are the gate zeroing the value. They say nothing about the
resolver. The ungated column says the opposite: `task_landings.session_id` for
both of the user's 2026-08-20 lands of E-2005 is **1120**, the session sitting
in the next pane. The sibling-shell attribution branch fires routinely.

**The attribution is correct, and is not what this task changes.** Crediting a
command typed in a sibling pane to the session whose tmux window it was typed in
is the resolver working: it is what records a land against the right session,
what keeps `session_tasks` touch tracking honest, and what puts a real value in
`task_landings.session_id` instead of NULL. Do not "fix" it. Anyone who reads
this section as a case against the resolver has read it backwards a second time.

What is wrong is a CONSUMER mistaking that value for an answer to a different
question. `session_id` answers *which session is this about*. Self-suppression
needs *who typed this*. Using the first as a proxy for the second concludes "the
session made this change itself" about a change the USER made, and drops the
notice. `agentRanThisCommand()` is what stops that inference from being drawn
today; §1 replaces it with the field that actually answers the question.

So the premise's mechanism is real and live, correct on its own terms, and its
misuse is already prevented. Recorded here so the next reader does not
re-derive any of the three.

## What is actually wrong

**One question, two answers.**

`stampTaskActor` re-sniffs `os.Getenv("CLAUDECODE") == "1"`. The event envelope
already carries the same fact as `Actor.Harness`, stamped by
`events.EmittingActor` from `internal/agentenv` (E-2005).

They can disagree, in both directions:

| environment | `agentRanThisCommand()` | `agentenv.Detect()` |
|---|---|---|
| `CLAUDECODE=1`, no `CLAUDE_CODE_ENTRYPOINT` | agent | `Unknown` |
| `CLAUDE_CODE_ENTRYPOINT=cli`, no `CLAUDECODE` | not an agent | `claude_cli` |

Both gates are subtractive — a miss over-notifies rather than silencing — so
neither disagreement is dangerous today. The cost is that the codebase now
states the answer twice, and the next person to touch either one has no way to
know which is authoritative.

**And the answer that decides suppression is never recorded.** It is read from
the executing process's environment and discarded. A notice that was not
written leaves nothing behind explaining why. After this change the deciding
value is `actor.harness` on the ledger line, which is durable and queryable —
the same reason E-2005 put it on the envelope instead of passing it as a flag.

## Design

### 1. `stampTaskActor` reads the envelope

```go
if evt.Actor.Harness != "" && evt.Actor.SessionID != "" {
```

and `agentRanThisCommand()` is deleted — the last `CLAUDECODE` read in Go.

Reading `evt.Actor.Harness` rather than calling `agentenv` again is the whole
point. For every real path the two are the same process, so the answer is
unchanged in practice; the difference is that the envelope's answer is the
recorded one, and a hand-rolled or replayed event carries its own rather than
inheriting whatever process happens to be executing it.

`epic.status_derived` is the one event whose `Actor` is built literally, with no
harness (E-2005 left it that way deliberately: nobody typed it). It cannot reach
this code — the executor's `dispatch` switch has no case for it and it is
appended to the ledger only — so it needs no exception here. Confirm that still
holds at implementation time rather than trusting this sentence.

### 2. One predicate, and it lives in `agentenv`

Add `agentenv.Present() bool` — "an agent harness is running this process".
`events.DetectedHarness()` keeps its `""` convention on top of it.

`internal/monitor` needs the same predicate (§3) and **cannot** import
`internal/events` — `events` imports `monitor`, so the dependency only runs one
way. Putting the predicate in `agentenv`, which imports nothing but `os`, is
what lets both sides share one spelling instead of re-introducing the divergence
this task exists to remove.

**`Detect() != Unknown`, not `Supported()`.** "An agent typed this" and "Endless
supports this harness" are different questions. A Desktop agent is still an
agent, and its own edit is still noise to itself. Matches E-2005's
`Harness != ""` exactly, which is the point of converging.

### 3. Make the two hook-path stamps state what they depend on

`monitor.StartWorkSession` (claim → `underway`) and `monitor.CompleteTask`
(→ `confirmed`) bypass the executor and stamp `changed_by_session`
unconditionally.

**This is not a bug and must not be fixed as one.** Their only caller is
`internal/hookcmd/claude.go`, and `hook claude` returns before it reads stdin on
an unsupported harness (E-1962) — so an agent is the only thing that can reach
them, and the unconditional stamp is right by construction. The problem is that
the construction is invisible at the site: nothing there says "safe because our
only caller is harness-gated", so a future caller from the CLI would silently
mis-suppress.

Gate both on `agentenv.Present()` and say why in a comment. Expected to be a
no-op forever. If it ever isn't, the caller set changed and the notice was
already going to the wrong session.

### 4. No schema change

`tasks.changed_by_session` keeps its current meaning: the session that made the
change when an agent made it, NULL otherwise.

Deliberately NOT mirroring `task_landings`' two-column shape
(`changed_by_session` + `changed_by_harness`). The extra fact that shape
recovers — which session a HUMAN's edit was credited to — has no consumer, and
this trigger is pinned by the E-1917 suites. Buying nothing for a column nothing
reads is not worth disturbing them.

## Verification — `tests/tasks/e-2006-verify.sh`

1. **Fail-fast Go layer first**, as E-2001/E-2005 do.

2. **The one real behavior change, both directions.** An event with
   `Harness: ""` executed in a process where `CLAUDECODE=1` must stamp NULL
   (today it stamps the session); an event with `Harness: "claude_cli"` in a
   process with no `CLAUDECODE` must stamp the session (today it stamps NULL).
   This pair IS the task — if it passes before the change, nothing moved.

3. **The E-1917 assertions survive verbatim; only how agent-ness is expressed
   changes.** `internal/events/notice_actor_test.go` currently declares it with
   `t.Setenv("CLAUDECODE", …)` and will declare it with `evt.Actor.Harness`
   instead. Be honest in review that these four tests DO change — the assertions
   and the failure messages must not. That diff, in one screen, is the whole
   task. `internal/monitor/session_notices_test.go` drives the trigger with SQL
   and should not change at all. `tests/tasks/e-1917-verify.sh` already exports
   `CLAUDE_CODE_ENTRYPOINT=cli` and never sets `CLAUDECODE`, so it is expected
   to pass untouched — which is itself evidence the signal was already available
   where it was needed.

4. **The §3 gate is a no-op on the real path.** Drive `hook claude` under a
   supported harness and assert the claim still stamps the acting session and
   still suppresses its own notice. A green result here means the gate changed
   nothing, which is the intended outcome.

5. `tests/tasks/e-2005-verify.sh` and `tests/tasks/e-1917-verify.sh` both still
   pass in full.

Each new check must be shown to FAIL against the pre-change binary, and for
check 4 the honest demonstration is the inverse: show it passing before AND
after, and show it failing only when the caller is made to run without a
harness.

## Residual risk, stated

Any future emit path that hand-builds an `Actor` instead of using
`EmittingActor` will carry no harness and so stamp NULL — over-notifying rather
than mis-suppressing. That is the safe direction, and it is the same direction
`agentRanThisCommand()` failed in. Today there are four `Actor` construction
sites in `internal/eventcmd`: three go through `EmittingActor`, and the fourth
(epic derivation) never reaches `dispatch`.

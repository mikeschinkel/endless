# Plan: the description-sufficiency evaluator job (E-1859)

The last implementation child of E-1844. E-1845 landed the `untriaged` status and
the queue it fills; this task builds the thing that drains it — for each
`untriaged` task, decide whether its description is already a sufficient spec
(→ `submitted`) or design work is needed first (→ `unplanned`), and record the
transition with triager attribution.

Both blockers are clear: E-698's fire-once runner landed (`internal/jobs/`,
registry ships empty — this is its first real client), and E-1845 landed at
`576f948`.

Every design decision below was ruled on by Mike in session; the rationale is
recorded so the implementing session does not have to re-derive it.

## Decisions

**Logic in Python, with zero raw SQL.** `src/endless/triage.py` holds the
orchestration and the model call. Every DB read goes through the Go read helpers
(E-1481), never through Python `sqlite3`; the write already routes through
`emit_event` → the Go executor. This lands the code on the target side of
**E-1486** (`ready`, `next` — "Python has zero SQLite knowledge", DB access in Go
while Click and logic stay Python) from day one, instead of adding to its 38-site
backlog. **E-1063** (port the whole CLI to Go) stays `later` with no forcing
function, and inherits a small bounded surface.

The all-Go alternative was rejected: `run_internal_claude` stays Python under
E-1486's boundary (the verb-check that uses it is Click-layer validation), so a
Go triager would create a second model-invocation harness in a second language —
precisely what **E-1849** would then have to unify.

**Triage at file time is detached fire-and-forget.** A synchronous model call in
`endless task add` would add seconds to every filing, interactive ones included.
The inline path is a latency optimization; the background sweep is the
correctness guarantee. File time spawns the triage of that one task detached and
returns at today's speed. If the detached call dies, the task stays `untriaged`
for the sweep — the fallback is the normal path, not an error path.

**Fail-open, always.** Timeout, missing binary, non-zero exit, unparseable reply
→ leave the task `untriaged`, transition nothing, exit zero. Triple duty: it
makes the job idempotent for free (a re-claim after a lapsed lease re-selects
only still-`untriaged` rows — the lease contract's hard requirement at
`internal/jobs/jobs.go:28-32`), it keeps a model outage from corrupting the
ledger, and it makes the worst failure mode the status quo, where a human
triages by hand with `task submit` — which E-1845 already commits to keeping as
the permanent override (`task_cmd.py:2790-2797`).

**One model resolver, per-purpose values.** Add `config.internal_model(purpose)`
in `config.py`, reading a `models` map from the layered config with a documented
default per purpose. The verb-check (`task_cmd.py:264`) and the triager both
resolve through it — the same mechanism E-1859 asks for — with distinct defaults,
because "is this description a sufficient spec?" is a materially harder judgment
than "is this word a verb?". A user who wants them identical sets one value.

**The two `report_cmd` gates are not touched.** They stay pinned to Haiku.
**E-1911** (`submitted`) is actively reworking the reporting contract and those
gates; changing model selection there would collide with it.

**The prompt is a Go template, not a JSONL string.** It lives at
`internal/templatecmd/templates/triage/sufficiency.md.tmpl` and renders through
`endless-go template render`. That system (E-1565/E-1822) already gives the
three-layer lookup — `<project>/.endless/templates/<name>.local.tmpl`
(per-developer) → `<name>.tmpl` (committed, materialized from embed on first
render) → embedded fallback — which satisfies ED-1531 Req 5's requirement that
the user tune wording in config rather than product source. It renders from
stdin-supplied JSON variables, so parent, sibling titles, and linked decisions
interpolate natively; a flat JSONL string would force that assembly in Python.
It is also Go, so it aligns with E-1486 rather than adding Python surface, and it
touches nothing E-1911 is rewriting.

**Attribution is a new `ActorKind` of `triager`.** Machine-decided transitions
become first-class and queryable as such, rather than hiding inside a payload
field on a generic `system` event. This adds one value to the validated set at
`internal/events/event.go:78-82`; the change is purely additive, so every
historical event remains valid under the existing kinds and nothing needs
upcasting. Coordinate the touch with **E-1671** (`unplanned`, `now`), which is
about to rework `internal/events`. The payload still carries the deciding model
and the model's one-line rationale — that is provenance, not attribution, and it
is what you want when triage starts making calls you disagree with.

## Implementation

### 1. `src/endless/triage.py` — the decision, once

Thin glue: Go reads → JSON → Go template render → prompt → model → parse →
`emit_event`.

- `select_untriaged(limit)` — tasks in `untriaged`, oldest first, capped by a
  batch limit so one sweep cannot run away on model spend. **Via the Go read
  helpers, not Python SQL.**
- `build_context(task_id)` — assembles the template's JSON variables from
  **persisted artifacts only**: the task's description, its parent's title and
  description, sibling titles, and linked decisions. Explicitly NOT the filing
  session's transcript. That exclusion is the point of the feature — triage must
  judge what is written down, so the call is reproducible and matches what a
  future implementer will actually have. **Via the Go read helpers.**
- `render_prompt(vars)` — pipes the JSON to `endless-go template render
  triage/sufficiency`.
- `evaluate(prompt)` — `run_internal_claude` with
  `config.internal_model("triage")` and a timeout; parses a constrained reply
  into `(decision, rationale)` or `None`.
- `apply(task_id, decision, rationale)` — emits the transition with
  `actor_kind=triager`, **guarded on the row still being `untriaged`**, so a
  human who routed the task by hand in the interval always wins.
- `triage_one(task_id)` / `triage_batch(limit)` — the two entry points.

### 2. `ActorKind` addition

Add `ActorTriager ActorKind = "triager"` and register it in `validActorKinds`
(`internal/events/event.go`). Thread it through Python's `event_bridge.emit_event`
alongside the existing kinds, including whatever `_ATTRIBUTION_REQUIRED` implies —
a triager event has no session to attribute to, so it belongs with `system`/`web`
on the not-required side. Check E-1671's working state before touching the file.

### 3. CLI surface

`endless triage run [--task E-N] [--limit N] [--dry-run]` in `cli.py`, delegating
to `triage.py`. `--dry-run` prints decision and rationale without writing — the
lever for tuning the template's wording, and what makes the behavior reviewable
without burning ledger writes.

### 4. Inline at file time

In `task_cmd.py`'s add path, after the task is filed and only when the resolved
status is `untriaged`, spawn the detached single-task triage. Gating on the
resolved status leaves tier-1's auto-`ready` untouched — a tier-1 task is exempt
from planning and therefore from triage, exactly as `task_cmd.py:2151` requires.

### 5. The Go job

New package `internal/triagejob` implementing `jobs.Job`:

- `Name()` — stable; it keys the scheduling row.
- `Schedule()` — `MaxBackoff` is set, not left zero: `internal/jobs/jobs.go:60-67`
  names model-calling jobs as precisely the case it exists for, so a persistently
  broken triager decays toward the cap instead of burning model cost every
  interval. `LeaseTTL` generously exceeds the worst-case sweep (batch limit ×
  per-call timeout).
- `Run(ctx)` — `exec.Command` the Python CLI's `triage run --limit N`,
  **capturing** stdout and stderr rather than inheriting them, because
  `jobs.Job`'s contract forbids writing to either when the trigger is a live TUI.
  Return the error; the runner records it as a fault.

Two wiring details that will otherwise bite:

- **Which DB.** The Python CLI has no `--config-dir` — that flag is Go-side only;
  Python resolves via `--db main|sandbox` and `XDG_CONFIG_HOME`. Thread the
  runner's resolved DB context into the subprocess environment, or a self-dev
  sweep triages the wrong ledger.
- **Which binary.** Register the job where the runner is wired so it is present
  in the candidate build.

### 6. Docs

`endless guide tasks` and the status-lifecycle prose say triage is manual ("until
an automatic triager exists, do it by hand"). That sentence appears in
`CLAUDE.md`, `README.md`, and `docs/guide/index.md`, and
`tests/tasks/e-1648-verify.sh` asserts the canonical block matches across copies —
re-sync all of them together.

## Verification

`tests/tasks/e-1859-verify.sh`, self-contained against a throwaway
`XDG_CONFIG_HOME` and a temp project, shaped after `tests/tasks/e-1880-verify.sh`.
The model call is the one thing that cannot be asserted deterministically, so stub
it (a fake `claude` earlier on `PATH`) and test the wiring around it:

- A stubbed `SUBMITTED` verdict moves an `untriaged` task to `submitted`; a
  stubbed `UNPLANNED` verdict moves it to `unplanned`.
- Every failure shape — timeout, non-zero exit, unparseable reply, missing binary
  — leaves the task `untriaged` and exits zero.
- Idempotency: running the batch twice transitions each task once.
- The human-wins guard: a task moved out of `untriaged` between selection and
  apply is not overwritten.
- Tier-1 tasks (filed straight to `ready`) are never selected.
- `--dry-run` writes nothing.
- The emitted event carries `actor_kind=triager` and the payload provenance.
- A `.endless/templates/triage/sufficiency.md.tmpl` override is honored over the
  embedded default.
- **No new Python SQLite access**: assert `triage.py` imports no `sqlite3` and
  opens no DB — the E-1486 boundary this task is committing to.
- Fold-in: `uv run pytest tests/`, `go test ./internal/jobs/... ./internal/events/...
  ./internal/templatecmd/... ./internal/triagejob/...`, and
  `tests/tasks/e-1648-verify.sh` for the re-synced doc block.

## Out of scope

- **E-1847** — the per-task hands-off opt-out. Sits in `maybe`, deferred pending
  evidence it is needed; `untriaged` already excludes anything a human touched.
- **E-1849** — the pluggable calling-harness layer. This task standardizes the
  *model key*; making the harness swappable across agent CLIs is that task, in
  `later`.
- **E-1911** — the reporting rework, including `report_cmd`'s Haiku gates.
- **E-1913** — the `--keep-status` fix. Sibling under E-1844, independently owned.
- Any change to `internal/events` beyond adding the one `ActorKind` value.



---

# REOPENED 2026-08-13 — automatic triage never fires where tasks are filed

Reported by Mike during the E-1958 brainstorm: triage "takes fucking forever."
A task an agent files sits `untriaged` long enough that the agent surfaces the
state to Mike instead of the routing quietly resolving it. Five defects, all in
code this task landed. Fix them here rather than in a new task.

Fix them IN ORDER — Fix 1 activates a race that Fix 2 must already cover.

## Fix 1 — `inline_suppressed()` is over-broad (the primary cause)

`triage.inline_suppressed()` suppresses file-time triage whenever
`config.gated_worktree_root()` is non-None and the resolved config dir is the
main one — i.e. `--db main` from inside a self-dev worktree. That is exactly how
every agent session in this repo is told to file, and agents file ~99% of tasks.
So in endless's own repo the inline path never runs and everything waits on the
sweep.

The rationale it copied is E-698's, in `internal/jobs/suppress.go`
(`suppressedWithReason`): candidate code must not write the real ledger. Correct
THERE — the Claude hooks invoke `<worktree>/bin/endless-go`. Not true here.
Under `--db main` from a worktree:

- the child `spawn_detached` launches is `sys.argv[0]`, the `endless` shim,
  which `just install` points at the MAIN checkout's editable source;
- that child writes through `event_bridge._resolve_endless_go()`, which prefers
  the worktree binary ONLY under `--db sandbox` and otherwise falls back to the
  PATH-resolved global.

Both halves are already landed code. Confirmed empirically: the justfile's
`verify` recipe runs `endless --db sandbox task verify` — the global shim — and
only the `test` recipe's `uv run pytest` resolves worktree Python.

The correct rule is a pair, not one condition:

| binary    | DB      | verdict                       |
|-----------|---------|-------------------------------|
| candidate | sandbox | fine — this IS the test       |
| candidate | real    | forbidden — the E-698 hazard  |
| landed    | real    | fine — this is triage         |

**Do:** gate on the resolved CLI/binary path lying outside the worktree, NOT on
`self_dev`. Do not merely delete the clause — path-gating stays correct if
anyone later runs `uv run endless` from a worktree, which today nobody does.
Leave the `ENDLESS_NO_TRIAGE` opt-out untouched.

## Fix 2 — the inline path takes no lease (PREREQUISITE of Fix 1)

Sweep-vs-sweep is already safe: `internal/jobs` claims with a CAS lease (single
conditional UPDATE, SQLite's clock for every due/expiry comparison), so the N
`session monitor` instances across N tmux windows that each call `jobs.RunDue`
contend and exactly one wins. `triagejob`'s Schedule sets `LeaseTTL` explicitly
as `batchLimit * perTaskTimeout + leaseHeadroom`, NOT derived from Interval, so
shortening the interval does not shrink the lease.

The gap is inline-vs-sweep. `triage.spawn_detached` runs `endless triage run
--task N` directly — it never enters the jobs runner and takes no lease at all:

1. task filed; inline child begins a model call (up to `CALL_TIMEOUT_SECONDS`)
2. the sweep fires meanwhile and selects N, still `untriaged` because the child
   has not written yet
3. both call the model for the same task

Python re-reads the row and guards the write, so state does not corrupt — but
the re-read happens AFTER the model call, so the spend is already doubled.

This is latent TODAY only because Fix 1's bug means the inline path never runs
in a worktree. Unsuppressing it makes the window real; shortening the interval
widens it. Mike: "IT WILL HAPPEN, so we can't assume a longer delay will solve
it."

**Do:** take a per-task claim BEFORE the model call, honored by both paths, so
a second attempt from either skips a task already in flight. Must survive a
crashed claimant (time-boxed like the job lease, not an OS lock). Land this
with or before Fix 1.

## Fix 3 — child failures must reach the user, not DEVNULL

`triage.spawn_detached` sends stdin, stdout and stderr to DEVNULL. `triage.run`
is already fail-open by design (timeout, missing `claude`, non-zero exit,
unparseable verdict all silently leave the task `untriaged`), so DEVNULL on top
makes a crash indistinguishable from a considered no-verdict, recorded nowhere.
Some share of the reported latency may be the child dying unseen.

**Do:** surface failures where a user actually sees them — `session monitor`,
per Mike — not merely a log file. Keep fire-and-forget semantics: the parent
still must not block on the child. Logging is the floor; the monitor is the
requirement.

Mike's standing rule, stated here because this violated it: code must not
silently ignore errors.

## Fix 3b — a worktree's stale hook binary fails invisibly, the same defect one layer out

Added 2026-08-13 at Mike's direction. Same root as Fix 3 (a failure nobody can
see), different subsystem — scope it with Fix 3's monitor work rather than as
separate plumbing.

Observed: `session goto 1808 --resume` opened a live Claude session in
`.endless/worktrees/e-1808` that endless could not see at all. Sibling panes
could not resolve it; the DB row still read `state=ended`, `process=''`.

Cause: that worktree's `.claude/settings.json` pins every hook to its OWN
`bin/endless-go`, built Jul 29. Run read-only it fails immediately —

    resolve project: task_types integrity check: tasktype: id=1 slug mismatch:
      enum="task", table="todo"

— the E-1659 `task`→`todo` / `bug`→`bugfix` rename. The binary carries the old
enum, every current DB carries the new value, so the integrity check aborts the
process before it does anything. SessionStart therefore never registers the pane,
and the failure goes to hook stderr, which nobody reads.

Why the pin exists: E-998, "so spawned sessions test the worktree's hook." The
alternative it replaced was repointing the global symlink, which put every other
live session on the machine onto the unverified build. That rationale holds for
an ACTIVE worktree with candidate hook code. It does not hold for a dormant
worktree resumed weeks later, where there is no candidate worth verifying and
the pin only costs a working session.

Scale: every worktree pins its own binary, and any schema or enum change in main
invalidates every un-rebuilt one. There are 137 worktrees; each dormant since
before E-1659 will present exactly this way the moment it is resumed.

E-1662 already ruled that a MISSING worktree binary must fail loudly rather than
silently fall back to the global. This is its sibling: the binary is present and
stale, and "loudly" means stderr nobody sees.

**Do:** route hook failures to `session monitor`, the same destination Fix 3
uses. A hook that cannot start is strictly more serious than a triage child that
dies, because the session then runs untracked for its whole life.

Consider additionally, as a smaller and separate judgment for the implementer to
raise rather than decide: whether a worktree binary that fails its integrity
check should fall back to the global binary with a visible warning, given that
E-1662's no-silent-fallback rule was written about a MISSING binary, not a stale
one, and the failure modes differ.

## Fix 4 — sweep interval

`triagejob`'s `interval` constant is 15 minutes. Not specified by Mike.

Correction to an earlier claim: interval does NOT trade against model spend. The
sweep selects `untriaged` rows first, so an empty queue costs one query; spend is
proportional to tasks filed, not to polling frequency.

**Do:** 5 minutes. Not 1 — with many `session monitor` instances live, tighter
polling buys latency that Fix 2's per-task claim should be providing instead.

## Fix 5 — the reset message offers an undo instead of naming a cost

In `task_cmd`'s update path, the E-1845 auto-untriage note (E-1845's code,
folded in here because it is the same user-visible symptom) prints:

    E-NNNN was 'ready'; description changed → status set to untriaged (the
    description is the spec that triage and approval were judged against; pass
    --keep-status to suppress for a typo/formatting-only edit).

The status render already shows `Status: ready -> untriaged`. The trailing
`pass --keep-status to suppress` reads as an undo offered after the fact, which
invites an agent to relay a completed, correct transition to the user as a
decision to accept. Observed four times in one session.

Do NOT simply delete the clause. `--keep-status` is a spend control, not a
status convenience: every re-triage is a model call, and a filer who already
knows the edit was cosmetic should skip it. Reframe so the decision it offers is
about cost rather than about whether the status was right:

    → untriaged; re-triage will run (one model call). Pass --keep-status on a
    typo- or formatting-only edit to skip it.

**Do:** reframe along those lines and keep the "why" short.

## Why one task, not five

They share a root: a filing (or a reset) that cannot self-resolve becomes a state
someone has to narrate. Fixes 1 and 4 are why it does not self-resolve, 2 is the
race that unblocking 1 exposes, 3 is why nobody can see when it silently failed,
and 5 is what turns the residue into a question for the user. Fixing only the
message leaves the latency; fixing only the latency leaves the message inviting
undo offers on the paths where triage legitimately cannot run.

## Explicitly out of scope

The justfile's `verify` recipe runs main's Python, not the worktree's, so a
Python-side change is verified against landed code while a Go-side change is
verified against candidate. Real asymmetry, but Mike has ruled it WONTFIX: the
fix is complex and Python is slated for a port to Go, so fixing it in Python
only delays the port.

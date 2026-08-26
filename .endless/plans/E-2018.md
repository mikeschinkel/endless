# E-2018 — Enforce the documented status lifecycle on `task update`

## The defect

`task update --status` accepts any status from any status, from any actor,
whether or not a session ever claimed the task. Observed 2026-08-20: a session
holding E-1817 set E-2015 to `unverified` — a task that was `unplanned`, had
never been claimed, and had no worktree, so `session goto E-2015` found nothing
to go to. Both missing checks were computable from data Endless already had.

The lifecycle is byte-synced across three documents by a test while nothing
checks the lifecycle itself.

## Design: the Go table is the source, the diagram is generated

Not parsing `docs/status-lifecycle.mmd`. Parsing is deterministic — the
`stateDiagram-v2` transition syntax is a tiny regular subset and one regex
extracted every edge on the first try — but it makes a hand-authored document
load-bearing, and the diagram cannot express two axes the guard needs (which
actor may take an edge, and which task types a lane applies to).

Invert it. A transition table in `internal/taskstatus` is the source;
`docs/status-lifecycle.mmd` becomes a generated artifact. Drift stops being
something a test detects and becomes something that cannot happen.

This is not a new mechanism. `just guide-index` already regenerates a block
between `<!-- BEGIN generated: ... -->` / `<!-- END generated -->` markers and
`just guide-check` exits non-zero on drift as a pre-land gate. Same shape here.

**Generate the states and edges, not the whole file.** The editorial preamble
stays hand-written in the template — the same split `guide-index` uses.

## Step 0 — `blocked` leaves the vocabulary

`blocked` is not a status. It once was, and a handful of rows still carry it,
which is stale data rather than evidence of a design. Blockedness is the
`blocked_by` relation, computed from the blocker's own status — the guide's
blocking-semantics table has always described it that way, and `endless task
block` has only ever written a relation.

ED-1572 proposed writing this down and was rejected because it needs no
decision: "we need to clean up the data." That cleanup is nearly finished — one
row remains, E-627, whose own title records that E-891 superseded it, so
`task replace E-627 --by E-891` retires it.

So this task does not give `blocked` edges, in or out. It removes it:

- the `Blocked` constant and every group membership in `internal/taskstatus`
  (`All`, `ClaimPromotes`, `ChildrenStateOrder`, `SessionPending`,
  `StickyOverride`) — E-1891 relocated those faithfully, but faithful to a
  vocabulary that should not have contained it;
- the session-status disposition bucket keyed on it;
- its row in the guide's status table and the `--status` help text, both of
  which fall out for free once the vocabulary shrinks;
- `classify()`'s handling in `sessionstatuscmd`, which routes it today.

E-1891's partition invariants make this cheap to get right: remove the constant
and the tests name every group that still references it.

**Do this first and separately.** It is a vocabulary change, not a lifecycle
change, and mixing it into the table's first draft would make the diff
unreadable.

## Step 1 — the transition table

In `internal/taskstatus`, alongside the groups:

```go
type Transition struct {
    From, To Status
    Actor    Actor      // User, Agent, Session, System
    Types    []tasktype.TaskType  // empty = every type
    Label    string     // rendered as the mermaid edge label
}
```

Two columns the current diagram cannot carry:

- **Actor.** Today the diagram encodes it as prose in edge labels — "user
  approves", "session claims", "agent submits", "triage routes". Consistent
  enough to be a grammar, but a grammar in a picture is a DSL wearing mermaid
  syntax. Make it a column; the labels are then *rendered from* it, so the
  prose can no longer disagree with the rule.
- **Type.** `unverified → confirmed` is the implementation lane; `→ completed`
  is research and brainstorm (ED-1502). E-2016 adds `unreviewed` to that second
  lane. One diagram, several lifecycles.

**Deterministic order.** Go map iteration is randomized and the generated file
must be byte-stable, so the table is an ordered structure — `dtx.OrderedMap`
from `github.com/mikeschinkel/go-dt/dtx`, already a dependency. Order it to
*read* well — entry, happy path, reopening, terminal — not alphabetically.

**Two gaps the current diagram has, closed here.** `completed` has no inbound
edge at all, though research tasks land there and E-1817 itself is `completed` —
a diagram-derived guard would have refused the transition that closed the task
that filed this one. Write its inbound edges in. (The other gap was `blocked`,
handled in step 0.)

## Step 2 — the generator and its drift check

`endless-go task-status transitions` emits the table, mirroring the one-verb-per-
function shape E-1891 established. A `lifecycle-index` recipe renders
`docs/status-lifecycle.mmd` from it and re-syncs the two byte-copies in
`README.md` and `docs/guide/index.md`; `lifecycle-check` exits non-zero on drift
and joins the pre-land gate next to `guide-check`.

`tests/test_status_lifecycle_sync.py` does not go away — its job narrows. It
stops asserting that three hand-maintained copies match each other and starts
asserting that the committed artifact is current: the `gofmt -l` shape.

## Step 3 — the guard

Two checks, both at `execTaskFieldsUpdated` in `internal/events/executor.go` —
the same executor and the same route that let E-2067's parent cycle through.

Follow `ValidateNoParentCycle` exactly (landed in 320c375): a plain exported
validator in the `events` package, called from the executor when the write
actually touches the field. E-2067 chose a shared function over a registry;
follow it rather than inventing a second arrangement. Note the asymmetry —
E-2067's guard needs only the tasks table, while this one needs the transition
table, so the hook is shared but the data behind it is not.

**Transition legality.** `ValidateStatusTransition(from, to, taskType)` refuses
an edge absent from the table. The message names the illegal edge and lists the
statuses that ARE reachable from the current one, so the caller's next move is
in the refusal rather than in the docs.

**Actor reality.** A session may not set `underway` or `unverified` on a task it
does not hold, and `unverified` requires that some session claimed the task at
some point — `sessions.task_id` is write-once (ED-1560), so that is a lookup,
not a heuristic. A caller with no session at all (a human at a terminal, cron,
a migration) is exempt: this guards against an agent's mistake, not against a
person, and `actor_kind` already distinguishes them.

### Enforcement: refuse, with no `--force`

Following E-1577/E-1579's precedent for the type×status gate — a correctness
invariant with no bypass, on the grounds that the fix is to correct the call,
not to override the gate. A `--force` here would be reached for on the first
missing edge and would become habit, and an override that becomes habit is a
guard that has stopped meaning anything.

The risk this accepts is a legal-but-unwritten edge stranding someone until a
fix lands. Two things make that tolerable: the table is authored fresh rather
than derived from the incomplete diagram, and step 2 renders the diagram FROM
it, so a missing edge is visible in the picture everyone already reads rather
than hidden in a Go literal.

## Sequencing

Step 0, then 1, then 2, then 3 — and steps 0 and 3 are separately landable, so
the vocabulary fix does not wait on the generator.

E-2016 (`unreviewed`) is blocked on this and should stay that way: if this lands
first, `unreviewed` adds two edges to an existing graph instead of arriving with
no enforcement to hook into.

## Verification

Split by durability, per E-1889's convention.

**Real suites — permanent invariants:**

- Go, `taskstatus`: every transition's `From`/`To` are in the vocabulary (the
  subset invariant E-1891 already runs, extended to the table); the table is
  connected — every status is reachable from `untriaged` and every non-terminal
  status has an outbound edge, so a status cannot be added and stranded; the
  rendered order is byte-stable across runs.
- Go, the guard: each refused edge refuses and each legal edge passes, walked
  over the table rather than hand-listed; the actor rules; and the exemption for
  a caller with no session.
- Go, the generator: rendering twice gives identical bytes, and the check verb
  fails on a hand-edited artifact.
- pytest: the two byte-copies match the canonical file (narrowed from the
  current test), and `task update --status` surfaces the refusal as a clean CLI
  error rather than a traceback.

**`tests/tasks/e-2018-verify.sh` — acceptance.** Harness shape from
`tests/tasks/e-1891-verify.sh`, fail-fast section A, project-wide regression
last. It asserts: the reported case refuses end-to-end (`task update <unplanned,
never-claimed> --status unverified` is rejected, naming the reachable
statuses); `blocked` is gone from the vocabulary and from every group; the
generated diagram is current; and a legal path — file, submit, approve, claim,
finish — still runs unimpeded.

## Explicitly not in scope

- Enforcing the lifecycle on any writer other than `task update`. The other
  status writers (`task submit`, `approve`, `claim`, `confirm`) each already
  encode their own single legal edge; routing them through the table is a
  simplification, not a fix, and belongs in its own task.
- Transitive block computation across `blocked_by` chains — that is E-1553.

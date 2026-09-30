# Plan — say what spawn actually requires

## The rule, as the code has it (measured 2026-09-30)

Spawning or claiming a task requires exactly two things, and status is not one
of them:

- **Not settled.** The only status gate refuses the `settled` group (reopen
  first). `submitted`, `unplanned`, `untriaged`, `ready` and `revisit` all pass.
- **Spawnable content.** `_require_spawnable` requires a non-empty plan and no
  open questions. It never reads status.

`task spawn` → `task_cmd.spawn_plan` → settled gate, ownership, prior claim,
`_require_spawnable`, `_perform_claim_work`, which emits `task.status_changed`
to `underway`. `execTaskStatusChanged` writes the status directly; it never calls
`ValidateStatusTransition` — that runs only on the `task update` route. So the
move from `submitted` succeeds although `internal/taskstatus/transitions.go` has
no `submitted → underway` edge. The "only `ready` work may be claimed" refusal
was removed by E-2074 along with background agents.

So the code is right and the description of it is wrong. This task changes the
description, plus the one table the lifecycle diagram is generated from. It does
not change what spawn allows.

## What `ready` and `task approve` mean after this

`task approve` stays, as an OPTIONAL record that a human reviewed the plan.
`ready` means "reviewed", not "permitted to start". Nothing gates on it. Every
place that says approval is required to spawn or claim, or that only `ready` is
spawnable, is rewritten to say this.

## Changes

1. **The transition table.** Add `{From: Submitted, To: Underway, Actor:
   ActorSession, Label: "claims"}` to the Claiming group in
   `internal/taskstatus/transitions.go`, and `Submitted` to `ClaimPromotes` in
   `internal/taskstatus/taskstatus.go`, whose comment ("the claim gate refuses
   `submitted`") is rewritten. `untriaged → underway` already exists; check
   whether ClaimPromotes should list `Untriaged` too, and make it match the edges.
   Then `just lifecycle-index` regenerates `docs/status-lifecycle.mmd` and the
   copies in `README.md` and `docs/guide/index.md`; `just lifecycle-check` and
   `tests/test_status_lifecycle_sync.py` must pass.
2. **The generated diagram's hand-written preamble and comments**: drop "the
   two-step gate that makes `ready` mean approved"; say approval is a review
   record.
3. **`docs/guide/index.md`**: the `submitted` and `ready` rows, and the sentence
   "`ready` provably means human-approved, so background sessions may pick up
   only `ready` work" (stale since E-2074). The claim-promotes lines in the
   happy path and the status table must list `submitted`.
4. **`docs/guide/tasks.md`**: "Plans and open questions gate spawning" — state
   that those two are the WHOLE gate; approval is not part of it. Also "The
   approved work" and "awaiting approval — not `ready`, which means
   human-approved".
5. **`README.md`**: "a human runs `endless task approve` to reach `ready` — so
   `ready` provably means approved to implement".
6. **Refusal and help text**: `_require_spawnable`'s message ("have it approved
   … `endless task approve`") names only the plan and the open questions;
   `cli.py` help strings that call `ready` tasks "spawnable work" say what they
   list without implying nothing else is spawnable; `task approve`'s own help
   says it records a review and does not unlock spawning.
7. **Code comments**: `claim_item`'s E-2074 comment (states the removed rule as
   if live), and `internal/sessionstatuscmd`'s `actReview` comment ("the claim
   gate refuses a submitted task, so rendering it as spawnable contradicts the
   gate") — rewritten to the real reason ⚑ exists: a plan awaiting the owner's
   review. The ⚑ glyph and `review` label stay.
8. **Sweep.** `git grep` for "approv", "claim gate", "provably", "may pick up
   only", "spawnable", "awaiting approval" across docs, src and internal; fix
   any other sentence that makes approval a precondition of spawn or claim.
   Landed verify suites under `.endless/tasks/` are records — leave them.

## Out of scope — raise, do not fix

`execTaskStatusChanged` never validates against the transition table, so the
table is unenforced on the claim/spawn route. Making it enforced is a behaviour
change with its own risk: every status the claim route can write would need an
edge first. Adding the `submitted` edge (change 1) is what makes enforcing it
later safe. Say so in the handoff; do not enforce it here.

## Verify

- `just lifecycle-check` and the lifecycle sync test pass; the regenerated
  diagram shows `submitted --> underway: session claims`.
- A unit test pins that `transitions.go`'s claim edges equal `ClaimPromotes`, so
  the table and the group cannot drift again.
- A test (Python): claiming or spawning (plan dry-run) a `submitted` task with a
  plan and no open questions succeeds and moves it to `underway`; the same task
  with no plan is refused by `_require_spawnable`, and the refusal text does not
  mention approval.
- A sweep assertion: no tracked file outside `.endless/` still contains "may pick
  up only `ready`" or "claim gate refuses".


Lifted out of E-1992 when that became an epic. It was carried there because
both pieces were storage, but this is a different table for a different
purpose, and E-1991 names giving open questions a home as its own strand.

Nothing here is settled until it ships; the shape below is where the earlier
draft had got to.

## A table, not a content row

One row per question the answering party must resolve: `task_id`, `series`
(round number, shared by questions asked together), `question`, `answer`
(nullable), `status`, `answered_by` (nullable — the user, or a peer session),
and `asked_by_session` (nullable, provenance only).

A content row would be one overwritable blob. A task accumulates many questions
across many rounds, each with its own answer and its own state, and a blob
loses all of it — including the ability to ask "every unanswered question
across the project", which is the feed an attention surface needs.

## Scope is the task, not the session

A question belongs to the task. The asking session is provenance, not lifetime:
sessions come and go, and a question must outlive its asker.

## `series` is assigned inside the insert transaction

`MAX(series)+1`, computed in the same transaction as the insert. No dispenser
table: SQLite serializes writers under a single write lock, so two sessions
cannot interleave the read and the insert when both are in one transaction. The
database enforces it rather than the invariant having to hold by convention.

## Status values

`open`, `answered`, `withdrawn`, `invalid`, `superseded`.

`withdrawn` is the asker retracting it. `invalid` is the user rejecting the
question's premise rather than answering it. `superseded` is a question rolled
into a later series — needed because answered questions get folded into an
updated plan, and without a terminal state distinct from `answered` the old
series keeps reading as live.

## Peer-resolved questions still get rows

When another session answers rather than the user, the row is still written and
marked `answered` with `answered_by` naming the peer. Agent-to-agent resolution
is the majority path by design, and without rows the whole class of decisions
made without the user is invisible — which is the failure E-1991 exists to fix.
The user must be able to review what two sessions settled between themselves.

## The plan stays authoritative

This table is the audit trail of how a plan reached its current state, never a
parallel spec. An answer is not in force until it is folded into the plan.
Without that rule, an answer in the table and a stale plan disagree with no way
to tell which governs.

## As shipped (decisions made during implementation)

- **CLI (Mike, 2026-09-26):** a top-level `endless question` group, a sibling
  of `decision` and `note`: `ask <task> "q1" "q2"...` (one call = one series),
  `answer EQ-<n> "text" [--by]`, `withdraw`, `reject` (writes `invalid`),
  `supersede`, `list [<task>] [--project] [--all]`. Question ids display as
  `EQ-<n>`, alongside E-/ED-/ES-.
- **answered_by (Mike, 2026-09-26):** stored as `user` or the peer's `ES-<n>`.
  From a plain shell `--by` defaults to `user`; when an agent harness is
  detected it is required, because only the agent knows whether it is relaying
  the user or answering as a peer.
- **Events:** `task.questions_asked` (one per series, entity = task) and
  `task_question.resolved` (one per question, status in the payload). The
  series and ids are allocated under the write lock by `event emit` and written
  into the ledger payload, so replay reproduces rows instead of recomputing
  them. An illegal move is refused before the ledger append.
- **Lifecycle:** open → answered | withdrawn | invalid | superseded, and
  answered → superseded. Nothing returns to open and nothing is answered twice;
  superseding an answered question keeps its answer.
- **Reason (revisit, Mike 2026-09-26):** a `reason` column, required on every
  close without an answer — `withdrawn`, `invalid` and `superseded`, no
  exceptions — and refused on `answered`. It is never stored in `answer`.
  Superseding an answered question keeps the answer and adds the reason. Added
  by migration 00005 (a Go step that checks for the column first); `withdraw`,
  `reject` and `supersede` take a required `--reason`.
- **Migration numbering:** 00004 landed; the reason column is 00005. Renumber
  at land time if E-1531 lands first.

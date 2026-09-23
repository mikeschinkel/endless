# Give open questions a home

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

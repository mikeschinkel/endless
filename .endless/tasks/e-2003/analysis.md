## Why this is worth a gate rather than a rule

The rule already exists and is easy to state: sessions are user-machine state, so
a session reference never belongs in committed or ledger-derived content. It is
also easy to violate without noticing, because a session id is the most
convenient handle for exactly the evidence an agent is trying to cite.

Observed 2026-08-20: one session wrote session references into four separate
artifacts — a decision description and three task analyses — while documenting a
genuine defect, and caught none of them until the user did. Every one was of the
form "claimed by ES-NNNN" or a pasted ledger trace with the actor column left in.
The correct form was available and costs nothing: name the ROLE ("its claiming
session", "the holding session"), and for a ledger trace strip the actor column,
since the task id, branch and merge commit carry the argument on their own.

## Where it must fire

The same inline-content choke point the path gate uses (see E-1761, E-1794,
E-1838 for its shape and its escape-hatch design). Fields to cover: task
`description`, `analysis`, `text`, `outcome`; decision `description`; anything
else that reaches a `.endless/` mirror file or an event payload.

It must fire BEFORE the event is emitted. The ledger is immutable by design, so a
post-hoc scrub fixes the DB row and the mirror file and leaves the emitted event
carrying the reference forever. A gate that runs after emit is not a gate.

## What it must NOT flag

Two cases are legitimate and a naive regex kills both:

1. `task show`'s `Created:` / `Surfaced:` / `Revisited:` header lines. These are
   rendered by the CLI from the sessions table at display time and are not stored
   in the artifact — nothing to gate.
2. Session references in RUNTIME CLI output, e.g. a refusal that names which
   session currently holds a task. That text is printed to one user on one
   machine and never committed. This matters concretely: E-1968's §1 carries a
   drafted refusal message containing a session ref, sitting inside plan text —
   correct content, in a gated field. An escape hatch mirroring `--allow-path` is
   required, not optional.

## Detection, and its honest limit

Reliable: `ES-\d+` and session UUIDs. Phrase-matchable: "session <int>",
"session_id = <int>", `actor.session_id`. Undetectable: a bare integer that
happens to be a session id. The gate should catch the first two classes and say
so plainly rather than implying completeness — the goal is to make the common,
convenient mistake loud, not to prove absence.

## Cleanup, separately

Existing violations are already in the ledger and cannot be removed. Pre-existing
ones remain in stored content too (E-1967's analysis and plan text, E-1968's
analysis each still name a session from earlier work). Whether to scrub the DB
rows and mirrors for those is a separate call from installing the gate, and is
not part of this task.



## Lesson content is in scope (added 2026-08-20)

E-2007 replaces the flat `.endless/LESSONS.md` with a lessons table plus events
and per-lesson mirrors. That moves lessons into exactly the category this gate
covers: ledgered rows with committed mirror files.

It matters more for lessons than for most content. A lesson records a correction
to an agent's behavior, so the evidence an author reaches for is nearly always
session-scoped — "the session that did X", the transcript, the claim. A session
id is the single most convenient handle there, which is why this is the field
most likely to be polluted.

The flat file was never gated. That is a gap, not a deliberate exemption — it
predates the gate and nothing about a lesson makes a session ref safe to commit.
Whatever choke point E-2007 routes lesson writes through should be covered from
the start, rather than gated later once the rows exist.

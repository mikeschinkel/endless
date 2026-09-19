# Named content slots to replace bespoke task columns

Implements the storage half of E-1991. Absorbs E-1531 (task_content) and E-1562
(Justification-heading notes migration) — read E-1531's analysis before starting.

## The reframe that matters

E-1531 currently frames `task_content` as *flexible storage for typed content*.
That is the weaker framing and it should not be built that way. The purpose is
**named slots that pre-empt overloading**: an agent cannot dump acceptance
criteria into the plan if acceptance criteria have their own row, because the
right placement becomes more obvious than the wrong one. Flexibility is the
side effect; placement pressure is the point.

Consequence: the type set is a **closed enum, versioned per release**. Agents
cannot invent a type. Adding one is a release, not a migration, and not a
runtime decision.

## 1. Schema

`task_content` rows: `id`, `task_id`, `type` (FK to a seeded values table
mirroring a Go int-const enum, house pattern with `String()`/`Parse()`),
`content`, plus the usual timestamps.

Seeded types for this release:

| type | holds |
|---|---|
| `plan` | the primary instruction content — how the work gets done |
| `acceptance` | what must be true for the work to be considered done |
| `analysis` | supporting evidence gathered before the plan |
| `outcome` | result / reason at terminal status |
| `notes` | catch-all, used sparingly |
| `triage_report` | the triage job's recorded verdict and rationale |

`questions` is deliberately **not** a content slot — it is a table, `task_questions`,
specified in §4. A task accumulates many questions across many rounds, each with
its own answer and its own state; a single overwritable blob loses all of that
and cannot answer "every unanswered question across the project", which is the
feed E-1996's attention surface needs.

Deliberately NOT seeded: `spec`. SDD alignment is incidental and Endless must
not imply it (see E-1991's outcome). An unused slot is a field agents will find
and fill. Add it later behind a config flag if someone asks — the versioned enum
is what makes that a one-release change.

Leave numbering gaps between seeded values so related types can be inserted
later without renumbering (same convention as E-1813's complexity/risk levels).

## 2. One stored `plan` type, per-type surface words

There is exactly one stored type for primary content: `plan`. The word varies
only at the surface, because that is what agents read and write:

| task type | CLI flag | display label |
|---|---|---|
| todo, bugfix, epic | `--plan` | `## Plan` |
| research | `--brief` | `## Brief` |
| brainstorm | `--topic` | `## Topic` |

**`--plan` is a universal alias that always works**, on every task type. An
agent reaching for `--plan` on a brainstorm lands in the right row rather than
hitting an error. The per-type word is canonical in help text and rendering;
the alias is insurance against exactly the drift this epic exists to stop.

Rationale for one stored type rather than three: the naming objection is
behavioral (an agent resists "plan" on a brainstorm), and behaviour is driven by
the flag and the label, not the schema. Keeping storage singular keeps the gate
one question — "does this task have its plan row?" — with no per-type mapping.

## 3. Migrate off the bespoke columns

`tasks.text` → `plan` rows. `tasks.analysis` → `analysis`. `tasks.notes` →
`notes`. `tasks.outcome` → `outcome`. Then drop the columns and every reader.

`tasks.text` is the wide one: CLI flags `--text`/`--text-file` on `task add` and
`task update`, `task show --text`, `task search --text`, the plan mirror written
to `.endless/plans/E-NNNN.md`, the spawn handoff templates, and the guide docs.
Keep `--text` accepted as a deprecated alias for one release so in-flight
worktrees and any muscle memory do not break.

E-1562's `## Justification` heading inside notes becomes its own concern: either
a seeded `justification` type or a retained notes convention. Decide during
implementation and record which in the outcome — it is a small call and does not
need to come back to the user.

## 4. `task_questions`

A table, not a content row. One row per question the answering party must
resolve.

| column | notes |
|---|---|
| `id` | |
| `task_id` | FK |
| `series` | round number; questions asked together share one series |
| `question` | |
| `answer` | nullable |
| `status` | `open`, `answered`, `withdrawn`, `invalid`, `superseded` |
| `answered_by` | nullable — user, or a peer session |
| `asked_by_session` | nullable FK; provenance only |

**Scope: `task_id`, not session.** A question belongs to the task. The asking
session is provenance, not lifetime — sessions come and go, and a question must
outlive its asker.

**`series` is `MAX(series)+1` computed inside the insert transaction.** No
dispenser table. SQLite serializes writers under a single write lock, so two
sessions cannot interleave the `MAX` and the `INSERT` when both are in one
transaction. The database enforces it; we do not need the invariant to hold.

**Status values.** `withdrawn` = the asker retracted it. `invalid` = the user
rejected the question's premise rather than answering it. `superseded` = rolled
into a later series; needed because answered questions get folded into an
updated plan, and without a terminal state distinct from `answered` old series
keep reading as live.

**Peer-resolved questions still get rows.** When a session's question is
answered by another session rather than by the user (E-1995), the row is written
and marked `answered` with `answered_by` naming the peer. This is not
bookkeeping: agent-to-agent resolution is the majority path by design, and
without rows the entire class of decisions made without the user is invisible —
which is the failure this epic exists to fix. The user must be able to review
what two sessions settled between themselves.

**The plan remains authoritative.** `task_questions` is the audit trail of how
the plan reached its current state, never a parallel spec. An answer is not
in force until it is folded into the plan. Without that rule an answer in the
table and a stale plan disagree, with no way to tell which governs.

## Acceptance

- `task_questions` exists with the columns and status values above; `series` is
  assigned inside the insert transaction with no dispenser table.
- A question answered by a peer session is recorded with `answered_by` naming
  that session.
- `task_content` exists with the six seeded types; the type set is a Go
  int-const enum mirrored by a seeded values table, and there is no code path
  that accepts an unrecognized type at runtime.
- `--plan` works on every task type; `--brief` and `--topic` work on their own
  types; all three write the same stored type.
- `tasks.text`, `tasks.analysis`, `tasks.notes`, `tasks.outcome` are gone, with
  no remaining readers in Go, Python, schema triggers, or docs.
- Existing content survives the migration — spot-check a task with content in
  all four old columns before and after.
- `--text` still works, emits a deprecation notice, and is documented as
  removed next release.
- `go build/vet/test ./...` and `just test` pass.

## Open questions

None blocking. The `justification` call in §3 is delegated to the implementer.

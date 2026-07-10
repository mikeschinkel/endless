# E-1673 — project / machine-user two-ledger scope split

Child of E-1671. Implementation brief: E-1666 Part II §3, §4. Design is locked; this
plan is option-free.

## Objective
Make event **scope** an explicit, enforced property so project state and
machine-user state physically separate at emit time: project → committed replayable
ledger, machine-user → gitignored local ledger.

## In scope
- **Declare `scope` (`project` | `machine-user`) on every event kind. Required** — a
  kind with no declared scope is a **hard error at emit**, not a silent default,
  because the directory default (root = project) and the safe default (undeclared →
  keep OUT of version control) point in opposite directions.
- **Emit routing gate:** `project` kinds write to `.endless/db-ledger/*.jsonl`;
  `machine-user` kinds write to `.endless/db-ledger/local/*.jsonl`. A machine-user
  kind **physically cannot** reach the committed ledger (gate, not convention).
- **`.endless/db-ledger/local/` is gitignored.**
- **Classify all current kinds.**
  - project: `task.*`, `decision.*`, `decision_relation.*`, `epic.*`.
  - machine-user: `session.*`, `conversation.*`, `message.*`, `note.*`,
    `session_status.*`, `focus.*` (the last two move OUT of the committed ledger →
    eliminates today's "muddy" bucket).
- **Forward-only:** the ~21 `session_status`/`focus` events already committed are
  NOT rewritten (no history rewrite). They remain, harmless (the projector already
  skips them), and age out at a future baseline. The gate governs new writes only.

## Out of scope
- Rebuild swap semantics (only project tables swapped) — E-1674.
- The transform engine — E-1672.
- The tripwire "no machine-user kind in committed ledger" check — E-1675 (and it
  must scope to *new* events / tolerate the pre-existing historical ones).

## Edge case (decided)
If a *field* within a machine-user kind genuinely describes the *project* (not the
user's interaction with it), that field is project state and must be split into its
own project-scope kind (a structural + transform pairing). Flag to Mike; do not
guess. None known today.

## Acceptance criteria
- Every event kind has a declared scope; emitting an undeclared kind errors.
- A machine-user emit writes only under `db-ledger/local/`, never under
  `db-ledger/`; `local/` is gitignored.
- `session_status`/`focus` emits land in `local/`; project kinds unchanged.
- No history rewrite of existing committed events.

## Touches
`writer.go` (segment path), the eventcmd emit path, the kind registry in
`events/event.go` (`ValidKinds`).

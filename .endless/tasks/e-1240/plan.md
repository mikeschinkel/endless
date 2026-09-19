# E-1240: Add `completed` status gated by verbs.json `completable` flag

## Motivation

Audit-style tasks (Audit, Research, Investigate, Review, etc.) have a structural mismatch with the current terminal-status options:

- `confirmed` requires behavior to have been verified — but an audit's deliverable is findings, not behavior. There is nothing to verify.
- `assumed` is not actually terminal: it carries an implicit lifecycle expectation that it will be promoted to `confirmed` once enough real-world use validates the assumption. Audit findings never trigger that promotion event because they are not behavior — so audit tasks parked in `assumed` sit indefinitely.

The recurring symptom: a session creates an "Audit X" task (e.g. E-1219), records findings as a comment/outcome, and sets `verify` — but there is no implementation to verify, so the only path forward is to either spawn a follow-up implementation task (which then becomes the de facto verification) or leave the audit task stuck in limbo.

## Design

Three terminal statuses, semantically distinct:

| Status      | Meaning                                                                                              |
|-------------|------------------------------------------------------------------------------------------------------|
| `confirmed` | Behavior verified. Unblocks dependents.                                                              |
| `assumed`   | Behavior believed correct, awaiting promotion to `confirmed` from real-world use. Unblocks dependents. |
| `completed` | Deliverable produced (text/findings); correctness is not a property this task can hold. Unblocks dependents. |

`completed` is the right home for audit/research/review tasks: the outcome text *is* the deliverable, and any judgment about correctness lives in the follow-up task that acts on the findings.

## Gating

Two gates on the `completed` transition:

1. **`completable: true` flag in `.endless/verbs.json`** — the task's title must begin with a verb marked `completable: true`. Hard error otherwise.
2. **`--outcome` required** — the transition must include `--outcome "<findings>"`. Hard error if empty/missing.

Rationale for verb-gating (vs `--outcome` alone): `--outcome` is too soft a friction. Anyone (especially a Claude session taking the path of least resistance) will provide *some* outcome text to clear the gate. The verb gate ensures `completed` cannot be used for implementation tasks where `verify → confirmed/assumed` is the correct path.

## Schema change

`verbs.json` entries gain an optional `completable` field:

```json
{
  "value": "audit",
  "definition": "to examine systematically",
  "completable": true
}
```

The field is optional — verbs without it default to `completable: false`. The semantic of `completable: true` is intentionally narrow for now ("this verb can be marked `completed`"); expected to evolve into a more descriptive marker (e.g. `deliverable: findings`) before v1.0, but not yet.

## Starter `completable` verb list

**Already present in verbs.json — mark `completable: true`:**

- `audit`
- `decide`
- `design`
- `document`
- `evaluate`
- `investigate`
- `redesign`
- `research`
- `review`

**Add to verbs.json AND mark `completable: true`:**

- `analyze` — to break down systematically into components
- `survey` — to take stock of a landscape
- `compare` — to identify similarities and differences
- `examine` — to inspect closely
- `assess` — to form a judgment about
- `explore` — to investigate possibilities in a space
- `diagnose` — to identify the cause of a problem

Pre-populating generously is fine — unused entries cost nothing, and removal is trivial. Better to absorb them now than churn through future sessions adding them one at a time.

## Scope

One task, end-to-end (per preference to avoid over-decomposition):

1. Add the 7 new verbs to verbs.json with definitions.
2. Add `completable` optional field to verbs.json schema + mark all 16 starter verbs `completable: true`.
3. Add `completed` to the task status enum (DB + Python/Go status types + dashboard + docs).
4. Implement transition gate: `task update <id> --status completed` checks (a) title's lead verb has `completable: true`, (b) `--outcome` is non-empty. Hard error with helpful message otherwise.
5. Add `endless task complete <id> --outcome "..."` convenience command (mirrors `task confirm` / `task assume` ergonomics).
6. Update quick-start docs, status table, dependency-blocking semantics docs.
7. Surface `outcome` text in `task show` output if not already present (the outcome IS the deliverable, so it must be visible).

## Verification

- Create an Audit-style test task, attempt `task complete` without `--outcome` → expect error.
- Attempt `task complete` on a task whose title's lead verb is not `completable` → expect error.
- Successful `task complete` with `--outcome` → status becomes `completed`, outcome is queryable in `task show` and via the DB ledger.
- Confirm `task list` and dashboard render `completed` as a distinct terminal state.
- Confirm dependency-blocking treats `completed` the same as `confirmed`/`assumed` (unblocks dependents).

## Open / deferred

- The `completable` flag semantic will likely evolve before v1.0 (e.g. into `deliverable: findings`). Don't over-design the schema now.
- Whether `completed` tasks ever get promoted to anything else (probably not — they're terminal).
- Whether `completed` should also appear in a separate "audit/findings" report view. Not now.

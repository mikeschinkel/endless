# E-1717 — Detect current-but-not-projected rows in validate-db

## Context
`validate-db` (`ValidateTasks`, `internal/events/validator.go:54-70`) replays the committed
ledger into a temp DB and compares it to the live DB, but only walks **projection→live**
(missing-from-live + field drift). It deliberately skips the **live→projection** direction
(`// Skip this for now -- tasks created before event system won't have events and would all
show as missing from projected`). So a live-DB row with no ledger create — the WAL-gap class the
E-1710 audit found in E-1223/E-1224 — is invisible. That skip is exactly why `validate-db`
reported clean while a manual cross-check surfaced the gap.

## Why the skip exists — and why a naive date cutoff is the wrong fix
The skip avoids "flooding": ~253 pre-ledger rows (created before `task.created` emission began,
~April 2026 / pre-E-808) have no create event and would all report as missing. A hardcoded date
cutoff to hide them would also hide genuine post-rollout gaps. The principled fix is to
**distinguish the known, frozen pre-ledger baseline from genuine gaps**, not to blanket-skip.

## Decision
Replace the blanket skip with a reverse check that **partitions** current-but-not-projected rows:
- **Known pre-ledger baseline** (informational, not a failure): a live row whose `created_at`
  precedes the earliest committed `task.created` timestamp for its project — it demonstrably
  predates event-sourcing. This set is finite and frozen; it only shrinks if creates are later
  backfilled. Report the count; do not fail on it.
- **Real gap** (failure): every other current-but-not-projected row — created after event-sourcing
  existed yet with no committed create (E-1223/E-1224). Report and fail.

The boundary is **derived from ledger data** (earliest committed create ts per project), not a
hardcoded date, and it degrades to an empty baseline automatically if pre-ledger creates are ever
backfilled.

## Dependencies
- **Blocked by E-1716** (neutralize committed test-fixture ledger events via the revert step):
  the ~36 fixture segments create fixture tasks reusing real ids 1..~150. Until they're
  neutralized, the projection's low-id state is distorted and the reverse check would mis-report.
- **NOT dependent on E-1715** — that task backfills historical *landings* (`task.landed`), which
  never touch the `tasks` projection this check compares. (An earlier proposal wrongly listed it.)
- **Related future (soft, not required): E-1041** — backfilling pre-ledger `task.created` events
  would empty the baseline set and make the partition a no-op. Not needed to ship E-1717.

## Approach
1. In `ValidateTasks`, after the projection→live loop, add a live→projection loop over `curTasks`.
2. Compute the per-project pre-ledger cutoff = MIN committed `task.created` ts (available from the
   projection pass).
3. For each live row absent from the projection, classify: baseline (`created_at` < cutoff) vs
   real gap.
4. Extend `ValidationResult` to carry the two sets separately; update `runValidateDB` in
   `internal/eventcmd/event.go` to print `known pre-ledger baseline: N` and fail on real gaps.

## Verification
- `internal/events/validator_test.go`: seed a projection missing (a) a pre-cutoff row (→ baseline,
  no failure) and (b) a post-cutoff row (→ real gap, reported).
- Run validate-db against a temp replay of main's committed ledger; assert any E-1223/E-1224-class
  rows surface as real gaps and the ~253 April rows land in the baseline bucket.
- Reads are temp-DB + live DB only; no mutation (consistent with the E-1710 Safety rule).

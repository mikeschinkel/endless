# Epic: make self-dev worktrees exercise their own candidate build

## Goal
When developing Endless in a self-dev worktree, every moving part must use the
WORKTREE'S OWN build and state — not the global/main install: the sandbox DB, the
endless-go/Python binary that runs, the Claude hook, and the land's event
recording. The recurring failure mode is silent fallback to the global install
(stale schema/enums, wrong DB), producing hard-to-diagnose integrity skew and
DB-targeting bugs.

## Why this epic
These tasks were filed independently and linked only by relations, so they kept
getting forgotten and the same problems re-encountered. Grouping the live work
under one epic makes the body trackable as a unit.

## Children (parented here)
- E-1662 — spawn/claim leaves the worktree unbuilt (no bin/, no hook override) -> falls back to global. ROOT CAUSE of the E-1660/E-1664 incident.
- E-1655 — collapse worktree+sandbox handling to canonical e-<id> (implements ED-1515).
- E-1331 — document the worktree DB sandbox (docs/sandbox.md).
- E-1458 — opt-in hook routing to the sandbox DB for self_dev testing.

## Related (cross-cut; parented elsewhere)
- E-1625 — `just verify self_dev` recipe wrapping `endless verify` (under E-1596, verify-suite).
- E-1608 — ephemeral per-run sandboxes for verify-run isolation (under E-1596).
- E-1516 — worktree land recording failure without explicit --db (phase=maybe; relates_to until promoted).

## Done (history; not reparented, per scope decision)
E-1281, E-998, E-1368, E-1429, E-1510, E-1513, E-1628, E-1660, E-1664, E-1643.

## Anchoring decisions
ED-1515 (canonical worktree+sandbox), ED-1066 (layered sandbox isolation),
ED-1067 (shared binary-selection helper), ED-958 (canonical vocabulary).

## Out of scope (separate body)
Integrity gate behavior + ledger/schema evolution — being scoped as its own epic
via E-1652 (migrations + rebuild-db upcasting), with E-1661 (hook exit 2 on
integrity failure) and E-1665 (prevent direct event emit appending to the ledger)
as candidates there. They intersect here (E-1660/E-1664 trip the integrity gate)
but are a distinct concern.

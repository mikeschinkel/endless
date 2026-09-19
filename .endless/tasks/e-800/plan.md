# Backend Integration: Implementation Plan

## Prerequisites from E-799

- **E-804** (event envelope) — Backend interface emits Endless events
- **E-805** (Go event writing) — import/sync writes events through Go
- **E-807** (projection) — imported data projects into SQLite for dashboard
- **E-816** (paired identifiers) — soft dependency for Endless↔backend ID mapping

E-818 (interface definition) can start as design work before event-sourcing is done.

## Critical Path

```
E-849 (backend config in .endless/config.json)
  → E-818 (Backend interface + Capabilities types)
    → E-819 (Beads import-only) + E-850 (integration tests)
      → E-820 (Beads bidirectional sync) [tier 4 — needs discussion]
        → E-821 (migration command) [later]
        → E-822 (degradation UI) [later]
```

## Subtask Details

- **E-849** (tier 1): Backend config in `.endless/config.json`. Beads needs repo_path, sync_interval, sync_direction.
- **E-818** (tier 2): Go interface with CRUD, Pull, Import, Capabilities. Null backend for native-only.
- **E-819** (tier 2): Shell out to `bd --json`. Import tasks, emit `task.imported_from_backend` events. History synthesis optional.
- **E-850** (tier 1): Integration tests verifying `bd --json` output format. Detect breaking changes early.
- **E-820** (tier 4): Sync layer — outbound push, inbound pull, loop prevention via correlation token, offline queue. **Needs discussion:** may need to split into sync framework vs Beads-specific sync.
- **E-821** (tier 3): Atomic migration command with validate, snapshot, import, reconcile, rollback.
- **E-822** (tier 2): Overlay status indicators, skip reports, synced vs native-only visual distinction.

## Key Risk

Beads `bd` CLI output format is not under our control. E-850 (integration tests) mitigates this.

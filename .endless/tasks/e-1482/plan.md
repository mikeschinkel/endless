# E-894 Phase 2 — route task-display reads through Go

Umbrella context: `endless task show E-894 --text`. Blocked by Phase 1: the read
subcommands must be on main first.

## Goal

Every Python task-display read goes through Phase 1's Go subcommands, and the
dead Python SQL is deleted. Rendering stays in Python. Output does not change.

## What to build

A read-side bridge module mirroring the existing write bridge. Mirror it rather
than designing it: the DB-context threading it does is mandatory, and getting it
wrong makes reads bypass the worktree DB gate — a failure that shows up only
from inside a worktree, which is where most work happens.

One wrapper per subcommand. Errors from the subprocess surface as CLI
exceptions carrying the child's stderr, matching how the write bridge reports
a missing binary.

## Scope, stated as a boundary

Phase 1's derived inventory is this phase's work list. Cut over exactly those
reads and delete their SQL.

**Task-display reads only.** Python also reads task tables for non-display
purposes, and reads other tables entirely; both stay. That is not slippage — it
is E-1492 and the other per-subsystem children of E-1486. Do not widen.

The consequence for verification: "Python has no task SQL left" is NOT this
phase's gate and cannot pass here. The gate is that the reads named in the
inventory are gone and nothing else changed.

## Invariants

- **Output is unchanged.** Not "equivalent" — unchanged, for the same DB, across
  every format, flag and filter combination the commands accept.
- Formatting and display gating stay in Python, untouched.
- A read whose field set was not captured in Phase 1 must not be cut over.
  Discovering one means Phase 1's inventory was incomplete: fix it there, then
  come back.

## Verification

- Golden-output parity for the same DB snapshot, before and after, across every
  task-display command and every output mode. This is the phase's real test;
  budget for it accordingly.
- Removed tasks stay absent from listings and still render, marked, in the
  single-task view.
- Exercise from inside a worktree so the DB-context threading is actually
  proven, not assumed.
- `just test` passes.

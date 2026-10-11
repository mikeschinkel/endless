# Refuse to spawn a blocked task, with the same override `precedes` has

## The asymmetry

`task spawn` already enforces the ADVISORY ordering relation and ignores the
hard one.

`precedes` is explicitly advisory — a recommended order, recorded as data.
Starting a task whose predecessor is unfinished is refused, and
`--out-of-order` is the deliberate override: "Start it even though a task that
should precede it is unfinished. Those tasks are still listed."

`blocks` means the dependent cannot start until the blocker is done. Nothing
checks it. A blocked task spawns silently, and the session that lands in it
discovers the blocker itself or does not.

Observed 2026-10-11: E-2293 was spawned while `blocked_by E-2294`, with the
edge recorded fifteen minutes earlier. The spawn said nothing. `task show`
reported the blocker correctly the whole time, and `session status` rendered
`⊗ blocked` on the row — so every read surface knew, and the one write surface
that could have acted on it did not.

## The gate

`task spawn` and `task claim` refuse a task with any live blocker, by the same
release rule the display already uses: a blocker still holds its dependent
unless its status is terminal. `BlockedByN` in the session-status row is that
count and `GetActiveBlockers` in `internal/monitor/tmux_lookup.go` already
returns up to three blocker ids for the status line — reuse the existing rule
rather than writing a third copy of it.

The refusal names the blockers, the way `--out-of-order`'s does. A refusal that
says only "this task is blocked" makes the caller go look it up, and the ids are
already in hand.

## The override

`--blocked`, mirroring `--out-of-order` in shape and spirit: it starts the task
anyway and still lists the blockers. There are legitimate reasons to begin
blocked work — reading in, drafting a plan, or starting a part that does not
depend on the blocker — and an unoverridable gate would make a hard relation
unusable as documentation of intent, which is how `blocks` edges stop getting
recorded.

Name it for the condition it overrides, as `--out-of-order` is. Do not reuse
`--out-of-order` for both: the two relations mean different things, and one flag
that waves off either would make a blocked spawn indistinguishable from an
out-of-order one in a shell history or a wrapper script.

## Claim as well as spawn

Both promote a task to `underway`, and the hazard is identical. `task claim` is
the path a session already standing in a worktree takes; leaving it ungated
would make the gate avoidable by accident rather than by decision.

## Not here

Whether `precedes` and `blocks` should remain two relations is out of scope.
This task makes the enforcement symmetric; it does not revisit the vocabulary.

The epic-specific claim and spawn gating planned elsewhere (objective and
strategy rows) is a different gate on a different condition and must not be
folded in; this one applies to every task type.

## Acceptance

- `task spawn` on a task with a live blocker is refused, and the refusal names
  the blocking task ids.
- `task claim` is refused on the same condition, with the same message.
- A blocker in a terminal status does not refuse either, matching the release
  rule the display uses.
- `--blocked` starts the task anyway and lists the blockers it overrode.
- `--out-of-order` still governs `precedes` only, and neither flag waves off the
  other's condition.
- `just test` and `just test-go` pass.

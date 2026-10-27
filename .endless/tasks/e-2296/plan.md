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

## There is no override

Blocked means blocked. The gate takes no bypass flag, and `--out-of-order` must
not be widened to cover it.

This is the asymmetry, not an inconsistency to tidy up. `precedes` is advisory —
a recommended order — so an override is what makes it usable. `blocks` states
that the work cannot start, and a flag that waves that away converts a hard
constraint into a suggestion, which removes the only thing it was for.

The remedy for a block that should not apply already exists and is the right
one: remove the block (`task unblock`). That leaves a record of someone deciding
the dependency was wrong, where a bypass flag leaves a record of someone
deciding to ignore it this once — and the next session starts from a graph that
still says the task is blocked.

An earlier draft of this plan proposed a `--blocked` flag by symmetry with
`--out-of-order`. Withdrawn: nobody asked for it, and the symmetry argument is
backwards.

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
- No flag bypasses the refusal, and `--out-of-order` does not cover it. The
  refusal points at `task unblock` as the way forward.
- `just test` and `just test-go` pass.



## Answered (EQ-44, EQ-45)

- `task prime` refuses a blocked task too, the same as claim and spawn. The
  gate lives in `_require_spawnable`, which all three call.
- An agent may run `task unblock` itself, so the refusal is NO_REPORT: the
  remedy names `task unblock`, and the agent may follow it. If that becomes a
  problem, gates come later.
- Blockers are read through `get_all_relations(...)["blocked_by"]` filtered by
  the terminal set, the way `_unfinished_predecessors` reads `preceded_by`. The
  terminal set comes from `internal/taskstatus` through `statuses.get`, so this
  is the display's rule, not a third copy. `GetActiveBlockers` is not called:
  its `LIMIT 3` would cut short the blockers the refusal must name.



## Folded in

- `internal/refusal/logging.go` opens the Go log file on first write instead
  of at handler construction. Every endless-go subcommand installs the cfgstore
  handler, so an empty `log/` landed in every config dir a run touched. Under
  the verify runner that made the harness guard refuse any `sh` check that
  followed a pytest or gotest check, which blocked this task's own
  `verify.toml`. Pinned by `TestSlogHandlerCreatesNothingUntilItLogs`.
- The suite is a `verify.toml` (pytest, gotest, then `e2e.sh` on the harness),
  per the direction that unlanded tasks move off `verify.sh`.

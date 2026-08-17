# Both halves of "don't drop a worktree someone is standing in"

Folded together per ED-1550: the gate and the guidance are two symptoms of one
cause, and shipping either alone leaves the failure reachable.

## Half 1 — the code gate (the original scope)

Drop checks only foreign state and uncommitted changes. Reap checks more, and
drop should adopt both of its guards:

| Guard | Location | Catches |
|---|---|---|
| Non-ended session row with `active_task_id` for the task | `reap_worktrees.go:206` | a bound Claude session whose cwd has moved out temporarily |
| `hasLiveProcessInDir` (`lsof -d cwd +D`) | `reap_worktrees.go:237` | any process standing in the directory right now |

Either alone leaves a hole. The `lsof` probe misses a session that stepped out;
the session-table probe misses a non-Claude process. Given the severity, take
both.

## Half 2 — the guidance (folded in from a separate filing, 2026-08-16)

Twice recently sessions ADVISED dropping a worktree whose git history had
diverged from main, when no committed work would be lost and a reset or rebase
was the correct move. Dropping deletes the directory out from under the live
Claude session and orphans it — the failure class that previously cost two-plus
weeks of recovery.

A gate stops the damage but not the advice, and an agent blocked by a guard
still needs to know what to do instead — otherwise the refusal just becomes a
thing to work around. So this task must ALSO state the reset-versus-drop
decision in the agent-facing guidance: the worktree guide section and the spawn
handoff. Name the correct move before the destructive one is reachable.

Coordinate with the handoff-doc work already in flight, and with E-1898 and
E-1941, still moving through this code.

## Deliberately NOT in scope

Drop's docstring also promises an unlanded refusal the implementation never
performs. E-1902 replaces that with warn-plus-snapshot-plus-proceed; leave it
there.

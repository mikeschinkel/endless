# Worktree teardown leaves three kinds of dependent behind

Found 2026-08-06 while investigating fseventsd/kernel_task saturation.
Measured: 267 sandboxes against 113 worktrees — **184 orphaned**, 228 MB,
oldest dating to 2026-06-17.

## Defect 1 — `classify()` can never report a worktree sandbox as orphaned

`internal/sandboxcmd/list.go:83`:

```go
func classify(meta SandboxMeta) sandboxState {
    if meta.Mode == modeKeep || meta.Mode == modePersistent {
        return stateInUse    // unconditional: no worktree check, no PID check
    }
    if isAlive(meta.CreatorPID) {
        return stateLive
    }
    return stateOrphaned
}
```

Every worktree sandbox is `persistent`, so all 268 classified `in-use`.
`prune.go:33` only removes `stateOrphaned`. **`endless-sandbox prune` was
structurally incapable of reclaiming a single worktree sandbox** — its own
message ("no orphaned *ephemeral* sandboxes") shows it was built only for
ephemeral ones. A persistent sandbox whose worktree no longer exists should
classify as orphaned.

## Defect 2 — the reaper never destroys the sandbox

`internal/monitor/reap_worktrees.go` contains **zero** occurrences of
"sandbox". `Sandbox.Destroy()` is reachable only from
`internal/sandboxcmd/run.go:50` (ephemeral teardown) and the manual
`endless-sandbox destroy` CLI. Nothing in the drop/land/reap path calls it.
`CLAUDE.md` documents the gap ("Sandbox cleanup on worktree drop/land is not
yet automatic") but the reaper shipped without closing it.

## Defect 3 — `endless-go channel` processes outlive their worktree

Four processes aged **18–24 days** still hold open the sandbox DBs of
worktrees that are gone:

| PID | age | sandbox | worktree dir |
|---|---|---|---|
| 73345 | 18d18h | e-1533 | GONE |
| 93453 | 23d21h | e-1603 | GONE |
| 1973  | 23d21h | e-1774 | GONE |
| 79957 | 18d21h | e-1789 | GONE |

They were the only 4 of 183 orphans that `sandbox destroy` refused (correctly,
without `--force`) during the 2026-08-06 manual cleanup that removed the other
179. Each is a live process plus a pinned SQLite handle on a directory nobody
will read again. Teardown should terminate the channel process, or the channel
process should exit when its worktree disappears.

## Manual cleanup already performed

179 of 183 orphans destroyed by hand (288 MB → 68 MB, 268 → 89 sandboxes).
Backup at `orphaned-sandboxes-backup.tgz` in the session scratchpad. This task
is about making that automatic, not about re-doing it.

---

# Reap safety predicate (learned the hard way, 2026-08-06)

The manual cleanup used "worktree dir exists OR git tracks a worktree of that
name" as its protection rule. **That rule is insufficient.** It destroyed 10
sandboxes it should not have; all were restored from the pre-delete backup.

A sandbox may be reaped ONLY when every one of these is false:

| # | Condition | How to check | Missed by the manual run? |
|---|---|---|---|
| 1 | Worktree dir exists | `[ -d .endless/worktrees/<n> ]` | no |
| 2 | Git still tracks a worktree of that name | `git worktree list --porcelain` | no |
| 3 | **A live tmux window references the task ID** | window names carry `[E-NNNN]` | **YES — 8 destroyed** |
| 4 | **The task branch is unmerged to main** | `git branch --no-merged main` | **YES — 2 destroyed** |
| 5 | Worktree is unsettled (dirty working tree) | existing `worktree_unsettled.go` | n/a — implies (1) |
| 6 | A live process holds files in the sandbox | already enforced by `destroy` | no — correctly refused 4 |

## Why (3) matters

A tmux window is the user's own record that a task is still in play, and it
outlives the worktree. Of 57 task IDs with a live window in the `active`
session, 12 had no worktree directory at all — the window is the only surviving
handle on that work. Destroyed in error: `e-1202 e-1368 e-1552 e-1621 e-1664
e-1687 e-1702 e-1714` (the other 4 — `e-1533 e-1603 e-1774 e-1789` — survived
only because condition 6 blocked them).

Window names follow `endless_<slug>[E-NNNN]`; parse the bracketed ID. Note the
`active` session is grouped with `active-6`, so enumerate windows rather than
panes to avoid double-counting.

## Why (4) matters

A removed worktree whose branch never merged still holds the only copy of that
work. `git branch --no-merged main` currently lists **64** task branches.
Destroyed in error: `e-1036 e-1669`.

Condition (5) is subsumed by (1) for live worktrees, but must be checked
explicitly if reaping is ever extended to prune worktree dirs themselves.

## Implementation note

Conditions 3 and 4 are the two that survive worktree deletion, which is exactly
why a dir-existence check cannot stand alone. Both belong in the same predicate
the reaper consults, not as ad-hoc guards at the call site.

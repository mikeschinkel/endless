# Findings — dev-machine resource investigation, 2026-08-06

Triggered by `kernel_task` at 156% and `fseventsd` saturating CPU on the M4 mini
(24 GB RAM, 10 cores, 99-day uptime).

## Measured state

| Metric | Value |
|---|---|
| Swap | **49.2 GB used of 50.2 GB (98%)** |
| Free pages | 3,412 x 16 KB = 55 MB |
| CPU split | 65% sys / 20% user / 15% idle |
| Load average | 44-57 on 10 cores |
| Processes | 841, of which 43 are `claude` |
| tmux panes | 408 across 6 sessions |
| fseventsd CPU | 29,098 min in 100 days (~20% of a core, continuously) |

`pmset -g therm` recorded no thermal warnings, so `kernel_task` is the VM
compressor thrashing, not throttling.

## Worktree / sandbox footprint

- 113 worktrees, 180,117 files, 5.4 GB under `.endless/worktrees`
- **84,303 files (47%) are pure derived output:**
  `.venv` 67,289 - `__pycache__` 15,596 - `bin/` 1,159 - caches 259
- By bytes `bin/` dominates: 70 MB of a 76 MB worktree
- 267 sandboxes, of which **184 are orphaned** (no matching worktree), 228 MB,
  oldest dating to 2026-06-17
- Time Machine backs up all of it (`tmutil isexcluded` reports `[Included]` for
  `~/Projects`, `.endless/worktrees`, `~/.cache`) to a **network SMB share**
  (Synology, 708 GB / 3,846,305 files). Spotlight indexes the same churn
  (`spotlightknowledged.updater` 11.8%, `mds` 5.5%).

## Why we cannot just exclude and reap

A worktree dir is the only home for uncommitted + untracked work. That is
precisely what must survive removal.

## Verified mechanism for making WIP durable

A worktree's `.git` is an 84-byte pointer file; its object database is the
**main checkout's** `.git/objects`. Snapshot without mutating the working tree:

```sh
export GIT_INDEX_FILE=$(mktemp -u)
git add -A                      # honors .gitignore -> skips bin/, .venv/, __pycache__
TREE=$(git write-tree)
COMMIT=$(git commit-tree "$TREE" -p HEAD -m "wip: <name>")
unset GIT_INDEX_FILE
git update-ref refs/wip/<name> "$COMMIT"
```

Three properties, all verified end-to-end in a scratch repo:

1. Objects land in the main checkout's shared store, not the worktree.
2. `refs/wip/*` is outside git's per-worktree ref namespace (only `HEAD`,
   `refs/bisect/*`, `refs/worktree/*` are per-worktree), so the ref is written
   to `<main>/.git/refs/wip/` and survives worktree removal.
3. A ref pins its objects against GC.

Destructive test — after `git worktree remove --force`, `git branch -D`,
`git reflog expire --expire=now --all`, `git gc --prune=now --aggressive`:

```
$ git show refs/wip/e-999:scratch-notes.md
PRECIOUS untracked
```

Both the modified tracked file and the untracked file were recovered.
`git add -A` honoring `.gitignore` was confirmed against this repo's
`.gitignore` (`/bin/` :13, `.venv/` :10, `__pycache__/` :8), so snapshots
capture only the precious source and are kilobytes.

## Time Machine exclusion mechanics (verified)

Two mechanisms with opposite lifetimes:

| Form | Storage | Survives dir deletion | Root |
|---|---|---|---|
| `tmutil addexclusion <path>` | xattr `com.apple.metadata:com_apple_backup_excludeItem` on the dir | **No — dies with the inode** | no |
| `tmutil addexclusion -p <path>` | `SkipPaths` in `com.apple.TimeMachine.plist` | **Yes — accumulates forever** | yes |

Confirmed the sticky (non-`-p`) form leaves `SkipPaths` nonexistent before and
after. `-p` requires root, so it cannot be triggered accidentally. `tmutil` has
**no glob/pattern support**; the only pattern list is `StdExclusions.plist`
inside the SIP-protected `backupd.bundle`. The per-worktree creation hook is
therefore the correct place to apply exclusions.

## Sandbox leak — two independent defects

1. `internal/monitor/reap_worktrees.go` contains **zero** occurrences of
   "sandbox". `Sandbox.Destroy()` is called only from `sandboxcmd/run.go:50`
   (ephemeral teardown) and the manual `endless-sandbox destroy` CLI.
2. `internal/sandboxcmd/list.go:83` `classify()` returns `stateInUse` for any
   sandbox whose mode is `keep` or `persistent` — **unconditionally**, with no
   worktree-existence or PID check. All 267 sandboxes report `persistent` /
   `in-use`, and `prune` only removes `stateOrphaned`, so `sandbox prune` is
   structurally incapable of reclaiming any worktree sandbox. Its own message
   ("no orphaned ephemeral sandboxes") shows it was only ever built for
   ephemeral ones.

## Platform scope

The repo currently has **no** `runtime.GOOS` usage and no `_darwin.go` files —
no precedent for platform-conditional behavior. Anything built on `tmutil` or
`.metadata_never_index` would be the first, and needs an explicit no-op path on
other platforms rather than an assumed-macOS shell-out.

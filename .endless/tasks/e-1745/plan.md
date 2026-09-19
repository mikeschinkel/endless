# Fix worktree reaper: stranded orphan dirs falsely logged "removed", never deleted — PLAN

## Context

The self-dev worktree reaper (`ReapWorktreesForProject` → `ReapStaleWorktrees` →
`maybeReapWorktree` in `internal/monitor/reap_worktrees.go`; reaper origin E-1337)
logs `reap worktrees: removed <dir>` for the SAME orphaned dirs on every sweep
(observed on each land: e-1281, e-1381, e-1416) — yet the dirs persist.

Root cause (fully traced): those dirs are orphaned leftovers — non-empty, NO
`.git`, and absent from `git worktree list` (a prior reap or `git worktree prune`
dropped the git admin record but left the files). The flow:

1. Conditions 4 & 5 run `git -C <dir> rev-list main..HEAD --count` and
   `git -C <dir> status --porcelain`. With no `.git` in the dir, git walks UP to
   the main repo: rev-list → 0 (HEAD is main), and `.endless/worktrees/` is
   gitignored so porcelain is empty. Both "pass" — but they validated MAIN, not
   the orphan's contents.
2. `git worktree remove --force <dir>` → `fatal: '…' is not a working tree`
   (exit 128).
3. The `strings.Contains(out, "is not a working tree")` branch runs
   `_ = os.Remove(dir); return true, nil`. `os.Remove` refuses the NON-EMPTY dir,
   the error is discarded, and the function returns `true` anyway → the outer
   sweep logs `removed <dir>` though nothing was removed. Next sweep: identical,
   forever.

Two defects: (a) false success + misleading repeated log; (b) non-empty orphans
are never actually reaped.

## Decision (Mike)

Auto-deleting stranded orphan dirs is the intended behavior — forcing manual
cleanup is not wanted. There is no legitimate case to RETAIN a stranded
(untracked), TTL-expired, no-live-process, already-landed worktree dir: it is
already outside git's management (cannot be committed/landed from), abandoned past
`worktree_ttl`, and its task's deliverable is already on main. So: auto-remove
them, and surface only GENUINE removal errors (e.g. permission denied).

## Changes — `internal/monitor/reap_worktrees.go`

### 1. Auto-remove the stranded orphan (replace the discarded `os.Remove`)
In `maybeReapWorktree`, the `is not a working tree` branch becomes:
```go
if strings.Contains(out, "is not a working tree") {
    // Orphaned leftover: git no longer tracks it as a worktree (a prior
    // reap/prune dropped the record but left files). Remove the whole dir,
    // strictly path-scoped so RemoveAll can never over-reach. A real failure
    // (permissions, etc.) propagates as an error and is surfaced by the outer
    // sweep's existing log line — no more false "removed".
    if rerr := removeStrandedWorktreeDir(projectRoot, dir); rerr != nil {
        return false, fmt.Errorf("remove stranded worktree dir: %w", rerr)
    }
    return true, nil
}
```

### 2. New guarded remover — `removeStrandedWorktreeDir(projectRoot, dir string) error`
`os.RemoveAll(dir)` ONLY after asserting ALL of:
- `dir` is lexically within `filepath.Join(projectRoot, ".endless", "worktrees")`
  (`filepath.Rel` yields no leading `..` and is not `.`).
- `filepath.Base(dir)` matches `worktreeDirRe` (`e-\d+`).
- `dir` is not a symlink (`os.Lstat` → `mode&os.ModeSymlink == 0`).
Any assertion failing → return an error (never RemoveAll a path we can't vouch
for). This bounds the destructive op to exactly a `<main>/.endless/worktrees/e-NNN`
directory and refuses to follow a symlink to somewhere else.

### 3. Honest reporting
`return true` now means "the dir is actually gone." The tracked-worktree path
(successful `git worktree remove`) is unchanged. Genuine `RemoveAll` failures
propagate as errors → the outer loop's existing
`log.Printf("reap worktrees: %s: %v", …)` surfaces them (once per sweep) instead
of a false "removed".

## Tests — `internal/monitor/reap_worktrees_test.go` (extend existing)
The reaper already exposes test seams (`runGit`, `hasLiveProcessInDir` are
swappable package vars). Add:
- **Stranded-orphan reaped**: a real temp dir, non-empty, no `.git`, under a fake
  `<projectRoot>/.endless/worktrees/e-NNN`; stub `runGit` so `worktree remove`
  returns `fatal: … is not a working tree` and conditions 4/5 pass → assert the
  dir is gone afterward and the call returns reaped=true.
- **Path-guard rejections** for `removeStrandedWorktreeDir`: a dir outside the
  worktrees root, a non-`e-NNN` basename, and a symlink whose target is a
  sentinel dir → assert it returns an error AND the sentinel/target still exists.
- **Empty-orphan regression**: an empty stranded dir is still removed.

## Deliverable — Generate the verify script (do NOT hand-verify)

The implementing session GENERATES `tests/tasks/e-1745-verify.sh` (self-contained,
shape/output per `tests/tasks/e-1577-verify.sh`, "ALL PASSED" or a failure list)
and commits it with the implementation. It must:
- `just build` (this is Go), then `go test ./internal/monitor/ -run Reap` → passes.
- Drive `removeStrandedWorktreeDir` behavior with real temp dirs (the reaper's
  `runGit` / `hasLiveProcessInDir` seams are swappable): a non-empty stranded dir
  is removed and reported reaped=true; an empty stranded dir is removed; the path
  guard REFUSES (returns error, deletes nothing) for a dir outside the worktrees
  root, a non-`e-NNN` basename, and a symlink whose target is a sentinel.
- Assert the false-"removed" bug is gone: a removal failure reports an error, not
  a bogus "removed".

## Verification (handoff to Mike)

On completion, set `unverified` and tell Mike to run exactly:

```
esu && ./tests/tasks/e-1745-verify.sh
```

MANUAL (the one thing the script can't safely do, since it mutates real dirs):
with the actual stranded dirs (e-1281/e-1381/e-1416) present in the main checkout,
run `endless-go event reap-worktrees` and confirm they are truly deleted and the
"removed" line prints once each, then never again.

## Notes
- The clean/unmerged git checks (conditions 4 & 5) are meaningless for stranded
  dirs (they inspect main). Safety for the auto-delete rests on: task-landed +
  past-TTL + no-live-process + the strict path scope above.

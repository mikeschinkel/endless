# Implement E-1219 — `FindWorktreeRoot` must never return projectRoot as a worktree

## Context

`FindWorktreeRoot` (`internal/monitor/worktree_lock.go:212-245`) walks up from cwd looking for `.endless/worktree.json` and returns the directory containing the companion. It stops at projectRoot (per its docstring), but the termination check (line 235 `if dir == root`) fires *after* the companion-existence check (line 229 `if err == nil && !info.IsDir()`). If a companion file ever appears at `<projectRoot>/.endless/worktree.json` for *any* reason, the function returns projectRoot as a worktree.

Observed during E-1209 setup (2026-05-09): main had a tracked stale `.endless/worktree.json` (from E-1218's predecessor bug — a buggy commit accidentally tracked the file). My session at SessionStart called `FindWorktreeRoot(cwd=main, projectRoot=main)`, the function returned main itself, `handleWorktreeAdoption` claimed `.endless/worktree.lock` at main, and downstream sessions either errored as "owned by another session" or saw a leaked lock. E-1218 untracked the companion from main, eliminating the *trigger* for that scenario, but the underlying architectural bug in `FindWorktreeRoot` remains: any future code path that writes a companion at projectRoot (buggy write, manual file, race, etc.) reproduces the same failure.

## Critical files

- `internal/monitor/worktree_lock.go:212-245` — `FindWorktreeRoot`. The fix.
- `internal/monitor/worktree_lock_test.go:146-206` — existing FindWorktreeRoot tests. Add a new test for the projectRoot-companion case.
- `cmd/endless-hook/claude.go:1076` — `handleWorktreeAdoption` (caller). Confirms the fix's downstream behavior: if FindWorktreeRoot returns "" for cwd-in-main, adoption is skipped (case B in the existing comment).

## Decision

Move the `dir == root` termination check to the **start** of each loop iteration, before the companion check. projectRoot is by definition the main checkout, never a worktree, so we should never check for a companion at projectRoot — even if one exists.

```go
for {
    if dir == root {
        return "", nil   // moved: terminate at projectRoot before checking
    }
    candidate := filepath.Join(dir, worktreeEndlessDir, worktreeCompanionFile)
    info, err := os.Stat(candidate)
    if err == nil && !info.IsDir() {
        return dir, nil
    }
    if err != nil && !errors.Is(err, os.ErrNotExist) {
        return "", fmt.Errorf("stat %s: %w", candidate, err)
    }
    parent := filepath.Dir(dir)
    if parent == dir {
        return "", nil
    }
    dir = parent
}
```

**Why move the check rather than add a post-find guard:** moving is the minimal change and most defensible — projectRoot is never an Endless-managed worktree, so there's no reason to even stat the candidate file there. A post-find guard would still stat-and-discard, which is wasted work and slightly more code to reason about. Both produce the same observable behavior; moving is cleaner.

## Implementation steps

1. Edit `FindWorktreeRoot` to move the `dir == root` termination to the loop start.
2. Update the docstring (line 212-219): make the projectRoot exclusion explicit. Replace "Stops at projectRoot (inclusive — does not walk above it)" with "Stops at projectRoot exclusively — projectRoot is the main checkout, never a worktree, so even if a companion file exists there it is ignored."
3. Add a new test `TestFindWorktreeRoot_IgnoresCompanionAtProjectRoot`:
   - Plant `<root>/.endless/worktree.json`.
   - cwd = root (or anywhere inside root that isn't a real worktree).
   - Assert returned path is `""`.
4. Re-run existing tests in `internal/monitor/worktree_lock_test.go` — they should still pass (no behavior change for the non-bug scenarios).
5. `just build` to confirm Go compiles.

## Verification

- **Unit:** `go test ./internal/monitor/... -run TestFindWorktreeRoot` runs all four FindWorktreeRoot tests; all pass including the new projectRoot-companion case.
- **Integration:** plant `<projectRoot>/.endless/worktree.json` manually, fire a synthetic SessionStart payload at `bin/endless-hook` with `cwd=<projectRoot>`, observe NO `<projectRoot>/.endless/worktree.lock` is written. Remove the stray companion afterward.
- **Regression smoke:** spawn a real worktree, fire SessionStart with `cwd=<worktree>`, observe lock IS written at worktree (not projectRoot).

## Out of scope

- **Cross-project leak:** if cwd is OUTSIDE projectRoot's tree (e.g., `/home/user/different-project`), the walk-up never reaches projectRoot's `dir == root` check and could find a foreign `.endless/worktree.json` somewhere up that tree. Callers don't validate the returned path is inside projectRoot. Worth a separate audit; file as a follow-up task if the failure mode is reachable in practice.
- **Companion auto-creation on `endless task start`:** noticed during E-1219 worktree setup — post-E-1218 worktrees created via `git worktree add` (rather than `endless task spawn`) lack a companion file, causing `endless task start` to refuse with "exists but does not belong to E-NNN". Workaround is to write the companion manually. Separate usability gap; file as follow-up.
- **Reviewing the lock-file model itself:** E-1195's territory.
- **Refactoring `handleWorktreeAdoption`'s case-B comment** at `cmd/endless-hook/claude.go:1086` once this fix lands. Trivial doc cleanup; not blocking.

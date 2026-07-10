# E-1669 — Provision self_dev worktrees so the hook runs candidate code

## Problem
In a self_dev worktree the Claude hook silently runs **main's** `endless-go` build: worktree
creation auto-wires DB routing (`sandbox bind`'s XDG env block) but not hook-code routing, so the
worktree's `.claude/settings.json` has no `hooks` block and Claude falls back to the global
`/usr/local/bin/endless-go` (→ main). The session never exercises candidate hook/gate/session
logic, and a skipped/failed provision degrades silently.

## Approach
Two parts. The provisioning lives in **endless's own** `post-worktree-create.sh` hook (run by
E-986's runner at `create_task_worktree`, covering both self-claimed and spawned worktrees); the
loud self-check lives in the binary as the never-silent backstop.

### Part 1 — extend endless's hook script (depends on E-986)
Add to `<endless>/.endless/hooks/post-worktree-create.sh` (the file E-986 creates):
1. **Copy** the installed `endless-go` into `<worktree>/bin/endless-go`. At creation the
   worktree's code == main, so the copy is valid immediately; `just build` overwrites it in place
   with candidate code. Guarantees the worktree binary always exists.
2. **Repoint the Claude hook:** run the `claude-settings-init` logic so
   `<worktree>/.claude/settings.json`'s `hooks` block points at `<worktree>/bin/endless-go`
   (mirrors the global hook entries, rewrites the binary path, preserves `enabledPlugins`/env,
   sets `skip-worktree`).

Idempotent (overwrite/regenerate), per E-986's hook contract.

### Part 2 — loud foreign-build self-check (binary, self_dev-gated)
When an `endless-go` **hook** fires from a foreign build (`os.Executable()` resolved ≠
`<worktree>/bin/endless-go` resolved) inside a self_dev worktree, emit a one-line **stderr
warning** naming the fix. A warning, **not** a refuse — refusing in the hook path blocks every
tool call. Same family as the E-1429 gate / E-1368 self-detect; fires only inside a self_dev
worktree.

## Scope
Hook only. `tmux`/`channel` stay on main's build (shared/real infra, `PinMainDB`; tested
deliberately). The bare-shell foreign-build **refuse** is E-1668.

## Files
- `.endless/hooks/post-worktree-create.sh` — add the binary-copy + `claude-settings-init` repoint
  (extends E-986's script).
- `internal/hookcmd/...` (or the `hook` path in `cmd/endless-go/main.go`) — the foreign-build
  stderr warning, reusing `monitor.selfDevProjectRoot`/`worktreeDirName` + `os.Executable()`.

## Verification
```
esu
./tests/tasks/e-1669-verify.sh
```
The script (written in E-1669's worktree) asserts: after a fresh self_dev worktree is created,
`<worktree>/bin/endless-go` exists and `.claude/settings.json`'s hook command path is
`<worktree>/bin/endless-go`; and a hook fired from a foreign build in a self_dev worktree prints
the warning to stderr without blocking. Plus a Go unit test for the foreign-build helper
(`compare(exePath, cwd)`).

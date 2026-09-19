# E-1036 — Gate `just install` global side effects to main; worktree-scoped install

## Context

`just install` has two globally-observable side effects:
- `ln -sfn "$(pwd)/bin/endless-go" /usr/local/bin/endless-go`
- `uv tool install -e . --force`

Run from a **git worktree**, these repoint the *system-default* `endless-go`
symlink and Python tool at the worktree's transient binary/source — silently
serving stale code to every `--db main` invocation and all non-worktree endless
use across all projects until someone notices.

**Live incident (2026-06-15):** a `just install` run from worktree `e-1565` left
`/usr/local/bin/endless-go` pointing at e-1565's binary for ~2 days. E-1585's
landed `config.json` seeding therefore did not run during `endless task spawn`
(spawn uses the global binary under `--db main`), producing a sandbox with no
`config.json`. Surfaced while verifying E-1585.

E-1036's original "refuse" implementation was lost: a tmux crash killed the
session that did the work, and an older worktree reaper (age-based, no
merge-status check) removed its worktree before the work was confirmed merged —
hence this ticket's stale `verify` and the dead, pre-E-1367
`task/1036-just-install-worktree-guard` branch (ignore it; it would revert the
binary consolidation).

## Decision (decided — implement as written)

Don't merely refuse from a worktree; **overload `just install` by checkout
context** and add an explicit worktree-scoped form. The invariant:
**worktree-scoping is achieved via injection (`.claude/settings.json` + PATH/XDG),
NEVER by mutating the global symlink or the global `uv` tool.**

- `just install` from the **main checkout** → today's global install
  (symlink + `uv tool install -e .`). Unchanged.
- `just install` from a **worktree** → do the worktree-**scoped** setup instead
  of the global side effects, and print that it scoped to the worktree and left
  global untouched. (Turns the exact footgun reflex into the right outcome.)
- `just install-worktree` → explicit scoped bundle, callable anywhere it makes
  sense; never runs `ln -sfn` or `uv tool install`.

Worktree-scoping is what the existing recipes already do safely (E-998 / E-1281):
`go-work-init` + `build` + `claude-settings-init` + `dev-sandbox-init`. So the
scoped path is a bundle of recipes that already exist and are already injection-
based.

## Implementation (justfile)

1. Detect checkout context with the git-dir vs git-common-dir discriminator
   already used in `claude-settings-init` (justfile ~226-231) and
   `dev-sandbox-init`: equal → main; differ → worktree.
2. Refactor `install` to branch on it:
   - **main:** existing global steps (`build`, symlink, `uv tool install -e .`).
   - **worktree:** delegate to `install-worktree`; do NOT run the global steps;
     `echo` what was wired and that `/usr/local/bin` was left untouched.
3. Add recipe `install-worktree`: runs `go-work-init` + `build` +
   `claude-settings-init` + `dev-sandbox-init` (the existing scoped recipes).
   This is the explicit form; `just install` from a worktree calls it. (A
   sibling recipe is used rather than a `--worktree` flag because `just`'s flag
   passing is awkward; `just install-worktree` is the canonical explicit
   spelling. Functionally this is the "`just install --worktree`" Mike asked
   for.)
4. Hardening (second layer, keep minimal): in `install` (both paths), warn
   loudly if `/usr/local/bin/endless-go` resolves into a `.endless/worktrees/`
   path — surfaces an already-mispointed symlink instead of letting it fester.

## Verification

- **From main:** `just install` → `/usr/local/bin/endless-go` → main's
  `bin/endless-go`; `endless-go` runs main code. (Global path unchanged.)
- **From a worktree:** `just install` → `/usr/local/bin/endless-go` still points
  at **main** (confirm `ls -la`); the worktree's `.claude/settings.json` +
  `bin-sandbox/` are wired; output states "scoped to worktree, global untouched".
- `just install-worktree` from a worktree → same scoped result.
- The stale-symlink warning fires when the symlink points into a worktree.

## Coordination

- `relates_to` E-986 (pluggable post-worktree-create hook) and E-1039
  (worktree-local binary preference) — adjacent worktree-toolchain work.
- Supersedes the original "refuse" mechanism (evolved: refuse → scoped install).

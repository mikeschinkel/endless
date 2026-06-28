#!/usr/bin/env bash
#
# Endless's own post-worktree-create hook.
#
# Endless runs this script after it creates a new task worktree, with:
#   - cwd       = the freshly-created worktree
#   - argv[1]   = the worktree's absolute path
#
# Endless ships no default hook; this one belongs to the endless repo and is
# version-controlled here. Each project writes its own at
# .endless/hooks/post-worktree-create.sh to handle its language/stack's
# worktree-bootstrap needs.
#
# Contract: this script MUST be idempotent / re-runnable. Endless keeps the
# worktree on failure and tells the user to re-run the hook to finish bootstrap;
# there is no teardown. Re-running with an already-bootstrapped worktree is a
# no-op (or a clean regenerate).
#
# What this does for endless-on-endless (each step idempotent / re-runnable):
#   1. go.mod's `replace ../go-pkgs/X` directives only resolve from the main
#      checkout — a worktree sees them at the wrong relative depth and Go builds
#      break. `just go-work-init` generates a per-worktree go.work with absolute
#      paths to the local go-pkgs modules, which overrides the relative replaces.
#      (go.work is gitignored / per-developer.)
#   2. `just build` produces this worktree's own bin/* (incl. bin/endless-go).
#      Without it bin/ is absent and the per-worktree sandbox CLI falls back to
#      the global/main binary instead of the candidate build (E-1662/E-1281).
#   3. `just claude-settings-init` layers the per-worktree hook override onto
#      .claude/settings.json so the PostToolUse hook fires THIS worktree's
#      bin/endless-go (E-998), not the global one. Runs last so it sees the
#      built binary and preserves the XDG_CONFIG_HOME env block that the
#      sandbox bind step (run before this hook) wrote.

set -euo pipefail

worktree="${1:?usage: post-worktree-create.sh <worktree-path>}"
cd "${worktree}"

if ! command -v just >/dev/null 2>&1; then
    echo "post-worktree-create: 'just' not on PATH; cannot bootstrap worktree" >&2
    exit 1
fi

echo "post-worktree-create: generating go.work for ${worktree}"
just go-work-init

echo "post-worktree-create: building worktree binaries"
just build

echo "post-worktree-create: installing per-worktree Claude hook override"
just claude-settings-init

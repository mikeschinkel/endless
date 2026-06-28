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
# What this does for endless-on-endless:
#   go.mod's `replace ../go-pkgs/X` directives only resolve from the main
#   checkout — a worktree sees them at the wrong relative depth and Go builds
#   break. `just go-work-init` generates a per-worktree go.work with absolute
#   paths to the local go-pkgs modules, which overrides the relative replaces.
#   (go.work is gitignored / per-developer.)

set -euo pipefail

worktree="${1:?usage: post-worktree-create.sh <worktree-path>}"
cd "${worktree}"

if ! command -v just >/dev/null 2>&1; then
    echo "post-worktree-create: 'just' not on PATH; cannot run go-work-init" >&2
    exit 1
fi

echo "post-worktree-create: generating go.work for ${worktree}"
just go-work-init

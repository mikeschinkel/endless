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
#      (go.work is gitignored / per-developer.) This sets the worktree up so the
#      agent can rebuild candidate Go later, when its branch diverges from main.
#   2. COPY the main checkout's prebuilt bin/endless-go into this worktree's
#      bin/. A freshly-created worktree == main (no candidate code yet), so a
#      build here would only slowly reproduce the binary main already has —
#      copying is the identical result, instantly. Without bin/endless-go the
#      per-worktree sandbox CLI falls back to the global/main binary (E-1662/
#      E-1281). The agent rebuilds with `just build` only once it edits Go.
#   3. `just claude-settings-init` layers the per-worktree hook override onto
#      .claude/settings.json so the PostToolUse hook fires THIS worktree's
#      bin/endless-go (E-998), not the global one. Runs last so it sees the
#      copied binary and preserves the XDG_CONFIG_HOME env block that the
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

# Copy main's prebuilt binary rather than building (see header). The main
# checkout is the parent of the shared git-common-dir.
main_checkout="$(dirname "$(cd "$(git rev-parse --git-common-dir)" && pwd)")"
src_bin="${main_checkout}/bin/endless-go"
if [[ ! -x "${src_bin}" ]]; then
    echo "post-worktree-create: main checkout binary not found at ${src_bin};" >&2
    echo "  build it once from the main checkout with 'just build', then re-run this hook." >&2
    exit 1
fi
echo "post-worktree-create: copying ${src_bin} -> ${worktree}/bin/endless-go"
mkdir -p "${worktree}/bin"
cp -p "${src_bin}" "${worktree}/bin/endless-go"

echo "post-worktree-create: installing per-worktree Claude hook override"
just claude-settings-init

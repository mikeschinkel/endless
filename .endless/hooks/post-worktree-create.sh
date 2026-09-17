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
#   3. SEED this worktree's sandbox (E-1964). Endless creates the sandbox — an
#      empty <worktree>/.endless/sandbox/ — before running this hook, for every
#      project, and it seeds nothing: what a worktree's isolated state consists
#      of is the project's business, declared here. Endless's answer for its own
#      repo is an endless.db that `--db sandbox` reads, so dev-time worktrees
#      never write to the real ledger. A downstream project puts its own
#      fixtures, throwaway database or dummy credentials here instead, and never
#      this.
#   4. `just claude-settings-init` writes the per-worktree hook override to
#      .claude/settings.local.json so the PostToolUse hook fires THIS worktree's
#      bin/endless-go (E-998), not the global one. Runs last so it sees the
#      copied binary. The LOCAL file, not the tracked .claude/settings.json —
#      see E-1347.

set -euo pipefail

worktree="${1:?usage: post-worktree-create.sh <worktree-path>}"
cd "${worktree}"

if ! command -v just >/dev/null 2>&1; then
    echo "post-worktree-create: 'just' not on PATH; cannot bootstrap worktree" >&2
    exit 1
fi

# The main checkout is the parent of the shared git-common-dir.
main_checkout="$(dirname "$(cd "$(git rev-parse --git-common-dir)" && pwd)")"

# Run recipes from MAIN's justfile, against this worktree. Endless runs the hook
# that lives in the main checkout, so the recipes it calls have to come from the
# same place. A bare `just <recipe>` resolves the WORKTREE's justfile instead —
# identical for a worktree created a moment ago, but a worktree on an older
# branch has an older justfile, missing recipes this hook depends on or carrying
# versions that call binaries that no longer exist. Every recipe here derives
# its paths from the working directory, never from the justfile's location, so
# pointing it at main's justfile changes nothing but which version runs.
recipe() {
    just --justfile "${main_checkout}/justfile" --working-directory "${worktree}" "$@"
}

echo "post-worktree-create: generating go.work for ${worktree}"
recipe go-work-init

# Copy main's prebuilt binary rather than building (see header).
src_bin="${main_checkout}/bin/endless-go"
if [[ ! -x "${src_bin}" ]]; then
    echo "post-worktree-create: main checkout binary not found at ${src_bin};" >&2
    echo "  build it once from the main checkout with 'just build', then re-run this hook." >&2
    exit 1
fi
echo "post-worktree-create: copying ${src_bin} -> ${worktree}/bin/endless-go"
mkdir -p "${worktree}/bin"
cp -p "${src_bin}" "${worktree}/bin/endless-go"

# Seed the sandbox endless creates (and leaves empty) before this hook runs.
# Uses the binary just copied above, not the global one: a sandbox DB must be
# built by the same schema the worktree's own commands will read it with.
echo "post-worktree-create: seeding sandbox endless.db"
recipe dev-sandbox-init

echo "post-worktree-create: installing per-worktree Claude hook override"
recipe claude-settings-init

#!/usr/bin/env bash
#
# Endless's own seed-sandbox hook.
#
# `endless sandbox reset` runs this after clearing a worktree's sandbox and
# writing its self-ignoring .gitignore — at worktree creation, and before every
# `endless task verify` (E-1608). Nothing else runs it: reset is the one front
# door. It is called with:
#   - cwd       = the worktree
#   - argv[1]   = the worktree's absolute path
#   - argv[2]   = the (now empty) sandbox directory
#
# Endless ships no default hook; this one belongs to the endless repo. A
# downstream project writes its own at .endless/hooks/seed-sandbox.sh to put
# its fixtures, throwaway database or dummy credentials into the sandbox.
#
# Contract: seed only. This runs on every verify, so it must not touch the
# worktree itself — no builds, no binary copies, no settings rewrites; those
# belong in post-worktree-create.sh.
#
# What this does for endless-on-endless: builds the endless.db that
# `--db sandbox` reads (schema, the project row copied from the main database,
# a session row), so dev-time worktrees never write to the real ledger.

set -euo pipefail

worktree="${1:?usage: seed-sandbox.sh <worktree-path> <sandbox-dir>}"
cd "${worktree}"

# Prefer the worktree's own build: a sandbox DB must be built by the same schema
# the worktree's commands will read it with. A worktree that has not built yet
# falls back to the global binary.
if [[ -x "${worktree}/bin/endless-go" ]]; then
    bin="${worktree}/bin/endless-go"
else
    bin=endless-go
fi

# stdout is only the sandbox path, which the caller already knows.
"${bin}" sandbox init --mode worktree >/dev/null

#!/usr/bin/env bash
#
# E-1347 post-land: clear the legacy skip-worktree arming from every worktree.
#
# Endless runs this once, right after E-1347's merge advances main, with:
#   - cwd     = the main checkout
#   - argv[1] = the main checkout's absolute path
#
# Why a script at all: E-1347 stops WRITING the arming, which only helps
# worktrees created afterwards. Every worktree already on disk still has the
# generated override inside the tracked .claude/settings.json with
# `git update-index --skip-worktree` hiding it, and that bit makes git refuse
# to check out any commit which CHANGES that file. So one commit to
# .claude/settings.json on main blocks the rebase in every one of them at once
# — each reporting a rebase failure that names no conflicting file. Landing the
# writer-side fix without this sweep would leave that intact for the whole
# fleet (135 worktrees when this was written).
#
# `sandbox claude-settings-repair --all` does the work: per worktree it moves
# anything the arming was hiding into .claude/settings.local.json, clears the
# bit, and restores the tracked file to its committed content. A worktree
# without the bit is left completely alone.
#
# Idempotent: the repair is a no-op on an already-clean worktree, so re-running
# after a failed land does nothing.

set -euo pipefail

main_root="${1:?usage: e-1347.sh <main-checkout-path>}"
cd "${main_root}"

# The binary must be one that HAS the subcommand, and the globally installed
# endless-go is still the pre-land build at this point — `just land` refreshes
# it only after the land returns. E-1347's own worktree binary was rebuilt
# before the land (`just land` runs `just go` there first), so it is the one
# build guaranteed to be current; main's tree also holds the landed source but
# not necessarily a matching binary.
pick_binary() {
    local cand
    for cand in "${ENDLESS_WORKTREE_PATH:-}/bin/endless-go" \
                "${main_root}/bin/endless-go" \
                "$(command -v endless-go 2>/dev/null || true)"; do
        [ -n "${cand}" ] && [ -x "${cand}" ] || continue
        # Probe the usage text rather than trusting a path: an older build
        # would exit non-zero on an unknown command and abort this hook.
        if "${cand}" sandbox --help 2>&1 | grep -q 'claude-settings-repair'; then
            printf '%s\n' "${cand}"
            return 0
        fi
    done
    return 1
}

if ! endless_go="$(pick_binary)"; then
    echo "e-1347 post-land: no endless-go build with 'sandbox claude-settings-repair' found." >&2
    echo "  The land succeeded; the fleet sweep did not. Finish it with:" >&2
    echo "    cd ${main_root} && just build && ./bin/endless-go sandbox claude-settings-repair --all" >&2
    exit 1
fi

echo "e-1347 post-land: clearing legacy skip-worktree arming via ${endless_go}"
"${endless_go}" sandbox claude-settings-repair --all

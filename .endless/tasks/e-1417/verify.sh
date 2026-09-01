#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1417 and records what was true when E-1417
# landed. Edit it only if you ARE E-1417. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1417 verification — `worktree land` reports rebase-conflict causes accurately.
#
# Folds this task's own pytest suite into one fail-fast gate. It drives
# _rebase_conflict_message headlessly against crafted git repos (no full land
# machinery): a SOURCE conflict must name the file(s) + >=2 candidate recoveries
# and never the confident auto-file-only prescription; an AUTO-FILE-ONLY conflict
# must get the mechanical recovery; the two `phase` strings must name distinct
# steps; output must carry no internal E-NNN token. Also runs the orphan-drop
# regression so Step 3.7's existing behavior stays green.
#
# RED until the helper exists (import fails -> reported as a clear failure, not a
# crash); GREEN after. Run from anywhere inside the worktree:
#   endless task verify E-1417
#
# Refuse a direct run, and pick up the shared harness vocabulary. Sourced as the
# FIRST executable statement so the refusal fires before anything in this file
# runs; every definition below overrides the harness's own, so a suite written
# before the harness existed behaves exactly as it did.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
WT_ROOT=$(cd "${SCRIPT_DIR}/../.." && pwd)
cd "${WT_ROOT}" || { printf 'ERROR: cannot cd to %s\n' "${WT_ROOT}" >&2; exit 2; }

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; BOLD=""; RESET=""
fi

printf '%sE-1417 — rebase-conflict reporting in worktree land%s\n' "${BOLD}" "${RESET}"

if out=$(uv run pytest \
        tests/test_worktree_land_conflict_msg.py \
        tests/test_worktree_land_orphan_drop.py \
        -x -q 2>&1); then
    printf '%s\n' "${out}" | tail -1
    printf '%sPASS%s — conflict-message contract + orphan-drop regression green\n' \
        "${GREEN}" "${RESET}"
    exit 0
else
    printf '%s\n' "${out}" | tail -25
    printf '%sFAIL%s — see pytest output above\n' "${RED}" "${RESET}"
    exit 1
fi

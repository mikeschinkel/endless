#!/usr/bin/env bash
#
# E-1628 verification — worktree land / db apply-change / db backup must target
# the REAL (main) DB even when invoked from a self-dev session whose
# XDG_CONFIG_HOME points at a per-worktree sandbox and no explicit --db is given.
#
# Run from inside the worktree:
#   ./tests/tasks/e-1628-verify.sh
#
# What it proves:
#   1. BASELINE BUG  — a task.landed emit routed to the sandbox (no --config-dir,
#      XDG=sandbox) fails the task_landings FK (error 787), reproducing the
#      E-1542 landing failure. This is the failure mode the fix removes.
#   2. FIX (routing)  — config.default_db_to_main() pins the real DB and threads
#      --config-dir <main> to downstream endless-go shellouts, under XDG=sandbox.
#   3. FIX (end-to-end) — the real `endless db backup` command, run with
#      XDG=sandbox and NO --db, backs up the MAIN DB and writes NOTHING to the
#      sandbox backups dir.
#   4. EXPLICIT OVERRIDE — an explicit --db sandbox is honored, not overridden.
#
# Safe: the only write is a (rotated) main-DB backup copy; the FK probe writes
# nothing because it fails.

set -u

# ─── output ──────────────────────────────────────────────────────────────────

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi
UNDERLINE="──────────────────────────────────────────────────────────────"

section()    { printf '\n%s%s%s\n%s\n' "${BOLD}" "$1" "${RESET}" "${UNDERLINE}"; }
report_pass(){ printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"; PASS_COUNT=$((PASS_COUNT+1)); }
report_fail(){
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"
    printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "$2"
    printf '      %sgot:%s      %s\n' "${DIM}" "${RESET}" "$3"
    FAIL_COUNT=$((FAIL_COUNT+1)); FAILED_TESTS+=("$1")
}
summary(){
    printf '\n%sSummary%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    if [[ "${FAIL_COUNT}" -eq 0 ]]; then
        printf '  %s%d passed%s\n\n  %sALL PASSED%s\n\n' \
            "${GREEN}" "${PASS_COUNT}" "${RESET}" "${GREEN}${BOLD}" "${RESET}"
        return 0
    fi
    printf '  %s%d passed%s, %s%d failed%s\n\n  %sFAILED:%s\n' \
        "${GREEN}" "${PASS_COUNT}" "${RESET}" "${RED}" "${FAIL_COUNT}" "${RESET}" \
        "${RED}${BOLD}" "${RESET}"
    local t; for t in "${FAILED_TESTS[@]}"; do printf '    - %s\n' "${t}"; done
    printf '\n'; return 1
}

# ─── environment ─────────────────────────────────────────────────────────────

# Worktree root (this script lives at <root>/tests/tasks/).
WORKTREE_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${WORKTREE_ROOT}" || { echo "cannot cd to worktree root"; exit 1; }

WT_NAME="$(basename "${WORKTREE_ROOT}")"                       # e.g. e-1628
# Main checkout = path with the /.endless/worktrees/<name> tail stripped. Land
# resolves project-root to the main repo (not the worktree); the FK probe must
# do the same, else endless-go refuses with the E-1309 linked-worktree guard
# before it ever reaches the task_landings insert.
MAIN_ROOT="${WORKTREE_ROOT%/.endless/worktrees/*}"
SANDBOX_CFG="${HOME}/.cache/endless/sandboxes/${WT_NAME}/endless"
SANDBOX_BACKUPS="${SANDBOX_CFG}/backups"
MAIN_CFG="${HOME}/.config/endless"
MAIN_BACKUPS="${MAIN_CFG}/backups"
ENDLESS_GO="$(command -v endless-go || echo /usr/local/bin/endless-go)"

# Task id this worktree is for — guaranteed present in MAIN, and the sandbox has
# no tasks, so it is the natural "main-only" task for the FK probe.
TASK_ID="${WT_NAME#e-}"; TASK_ID="${TASK_ID%%-*}"

export XDG_SANDBOX="${SANDBOX_CFG%/endless}"  # the dir endless appends /endless to

# ─── 1. baseline bug: emit routed to sandbox fails the task_landings FK ───────

section "1. Baseline bug — task.landed routed to sandbox fails the FK (E-1542)"

# Run from the MAIN checkout cwd: that is the real `just land` scenario (land
# runs from main with the session's XDG=sandbox inherited). From a worktree cwd
# the Go binary self-detects the self-dev worktree and refuses earlier, before
# the FK insert — a different failure than the one E-1542 actually hit.
fk_out="$(cd "${MAIN_ROOT}" && XDG_CONFIG_HOME="${XDG_SANDBOX}" "${ENDLESS_GO}" event emit \
    --kind task.landed --project endless \
    --entity-type task --entity-id "${TASK_ID}" \
    --actor-kind system --actor-id system --node-id abcd \
    --project-root "${MAIN_ROOT}" \
    --payload '{"branch":"verify/e-1628","merge_commit_sha":"deadbeef"}' 2>&1)"
if [[ "${fk_out}" == *"FOREIGN KEY constraint failed"* ]]; then
    report_pass "sandbox-routed task.landed for a main-only task fails the FK (the bug)"
else
    report_fail "sandbox-routed task.landed fails the FK" \
        "output contains FOREIGN KEY constraint failed" "${fk_out}"
fi

# ─── 2. fix (routing): default_db_to_main pins main under XDG=sandbox ─────────

section "2. Fix — land/apply-change/backup default to MAIN under XDG=sandbox"

probe="$(XDG_CONFIG_HOME="${XDG_SANDBOX}" uv run python -c '
from endless import config
config.default_db_to_main()
print(config.RESOLVED_CONFIG_DIR)
print(" ".join(config.go_db_context_args()))
' 2>&1)"
resolved="$(printf '%s\n' "${probe}" | sed -n '1p')"
threaded="$(printf '%s\n' "${probe}" | sed -n '2p')"
if [[ "${resolved}" == "${MAIN_CFG}" ]]; then
    report_pass "default_db_to_main() pins the real config dir (${MAIN_CFG})"
else
    report_fail "default_db_to_main() pins main" "${MAIN_CFG}" "${resolved} | ${probe}"
fi
if [[ "${threaded}" == "--config-dir ${MAIN_CFG}" ]]; then
    report_pass "go_db_context_args() threads --config-dir <main> to endless-go"
else
    report_fail "threads --config-dir <main>" "--config-dir ${MAIN_CFG}" "${threaded}"
fi

# ─── 3. fix (end-to-end): real db backup hits MAIN, not the sandbox ──────────

section "3. Fix end-to-end — db backup (no --db, XDG=sandbox) backs up MAIN"

newest_mtime(){ local f; f="$(ls -t "$1" 2>/dev/null | head -1)"; [[ -n "$f" ]] && stat -f %m "$1/$f" 2>/dev/null || echo 0; }

sb_before="$(ls "${SANDBOX_BACKUPS}" 2>/dev/null | wc -l | tr -d ' ')"
start_epoch="$(date +%s)"

XDG_CONFIG_HOME="${XDG_SANDBOX}" uv run endless db backup >/dev/null 2>&1
rc=$?

sb_after="$(ls "${SANDBOX_BACKUPS}" 2>/dev/null | wc -l | tr -d ' ')"
main_mtime_after="$(newest_mtime "${MAIN_BACKUPS}")"

if [[ "${rc}" -eq 0 ]]; then
    report_pass "db backup succeeds (no --db) from a sandbox-routed env"
else
    report_fail "db backup succeeds" "exit 0" "exit=${rc}"
fi
if [[ "${sb_before}" == "${sb_after}" ]]; then
    report_pass "sandbox backups dir unchanged (no backup leaked to the sandbox)"
else
    report_fail "sandbox backups unchanged" "count ${sb_before}" "count ${sb_after}"
fi
# mtime of MAIN's newest backup must be at/after this run's start (same-second
# filenames overwrite in place, so compare mtime, not filename).
if [[ "${main_mtime_after}" -ge "${start_epoch}" ]]; then
    report_pass "MAIN's newest backup was (re)written by this run (fresh in main)"
else
    report_fail "fresh backup in MAIN backups" \
        "newest mtime >= ${start_epoch}" "mtime=${main_mtime_after}"
fi

# ─── 4. explicit override: --db sandbox is honored ───────────────────────────

section "4. Explicit --db sandbox is honored (not overridden)"

honored="$(XDG_CONFIG_HOME="${XDG_SANDBOX}" uv run python -c '
from endless import config
config.apply_db_choice("sandbox")   # mimics DBAwareGroup consuming --db sandbox
config.default_db_to_main()         # must NOT override an explicit choice
print(config.RESOLVED_CONFIG_DIR)
' 2>&1 | tail -1)"
if [[ "${honored}" == "${SANDBOX_CFG}" ]]; then
    report_pass "explicit --db sandbox survives default_db_to_main() (${SANDBOX_CFG})"
else
    report_fail "explicit --db sandbox honored" "${SANDBOX_CFG}" "${honored}"
fi

summary

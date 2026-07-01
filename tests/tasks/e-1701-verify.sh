#!/usr/bin/env bash
#
# E-1701 verification suite — the SINGLE entry point for verifying E-1701.
#
#   esu
#   ./tests/tasks/e-1701-verify.sh
#
# Self-contained: it builds the worktree binaries, runs the Go unit tests for
# the dirty-marker render logic, then drives the real worktree-built endless-go
# binary end-to-end against a throwaway temp DB built from the shipped
# internal/schema/schema.sql, with per-task git worktree fixtures created under
# a temp project root. Nothing in the sandbox or real ledger is touched. Exit 0
# on all-passed, 1 on any failure (with detail to diagnose).
#
# What it proves (E-1701):
#   1. Worktree builds clean.
#   2. dirtyMark + flat-view render — internal/sessionstatuscmd unit tests —
#      and taskWorktreeDirty git logic — internal/monitor unit tests.
#   3. `session-status --task E-NNN` (flat view) renders ◆ between the type
#      letter and id when the focal task's worktree diverges from main:
#        - a worktree with uncommitted changes  → ◆ (dirty),
#        - a clean worktree at main             → no ◆ (space),
#        - a task with no worktree at all        → no ◆.
#      The ◆/space swap is width-neutral, so the id column does not shift.

set -u

# ─── locate worktree ────────────────────────────────────────────────────────

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
WT_ROOT=$(cd "${SCRIPT_DIR}/../.." && pwd)
cd "${WT_ROOT}" || { printf 'ERROR: cannot cd to %s\n' "${WT_ROOT}" >&2; exit 1; }
BIN="${WT_ROOT}/bin/endless-go"
SCHEMA="${WT_ROOT}/internal/schema/schema.sql"

# ─── output ─────────────────────────────────────────────────────────────────

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi
UNDERLINE="──────────────────────────────────────────────────────────────"

section() { printf '\n%s%s%s\n%s\n' "${BOLD}" "$1" "${RESET}" "${UNDERLINE}"; }
report_pass() { printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"; PASS_COUNT=$((PASS_COUNT + 1)); }
report_fail() {
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"
    printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "$2"
    printf '      %sgot:%s      %s\n' "${DIM}" "${RESET}" "$3"
    FAIL_COUNT=$((FAIL_COUNT + 1)); FAILED_TESTS+=("$1")
}

# assert_cmd DESC CMD [ARGS...] — pass if CMD exits 0; on failure show the last
# few lines of its output so a regression is diagnosable from this report alone.
assert_cmd() {
    local desc="$1"; shift
    local out rc
    out=$("$@" 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then
        report_pass "${desc}"
    else
        report_fail "${desc}" "exit 0" "exit=${rc} | $(printf '%s\n' "${out}" | tail -8 | tr '\n' '⏎')"
    fi
}
# assert_contains DESC PATTERN CMD [ARGS...] — pass if CMD output contains PATTERN.
assert_contains() {
    local desc="$1" pattern="$2"; shift 2
    local out
    out=$("$@" 2>&1)
    if [[ "${out}" == *"${pattern}"* ]]; then report_pass "${desc}"; else report_fail "${desc}" "output ~ ${pattern}" "${out}"; fi
}
# assert_not_contains DESC PATTERN CMD [ARGS...] — pass if CMD output lacks PATTERN.
assert_not_contains() {
    local desc="$1" pattern="$2"; shift 2
    local out
    out=$("$@" 2>&1)
    if [[ "${out}" != *"${pattern}"* ]]; then report_pass "${desc}"; else report_fail "${desc}" "output !~ ${pattern}" "${out}"; fi
}

summary() {
    printf '\n%sSummary%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    if [[ "${FAIL_COUNT}" -eq 0 ]]; then
        printf '  %s%d passed%s\n\n  %sALL PASSED%s\n\n' "${GREEN}" "${PASS_COUNT}" "${RESET}" "${GREEN}${BOLD}" "${RESET}"
        return 0
    fi
    printf '  %s%d passed%s, %s%d failed%s\n\n  %sFAILED:%s\n' \
        "${GREEN}" "${PASS_COUNT}" "${RESET}" "${RED}" "${FAIL_COUNT}" "${RESET}" "${RED}${BOLD}" "${RESET}"
    for t in "${FAILED_TESTS[@]}"; do printf '    - %s\n' "${t}"; done
    printf '\n'
    return 1
}

# ─── 1. build ───────────────────────────────────────────────────────────────

section "1. Build"
assert_cmd "worktree builds clean (just build)" just build

# ─── 2. unit tests ──────────────────────────────────────────────────────────

section "2. Unit tests (render + git dirty logic)"
assert_cmd "sessionstatuscmd render tests (dirtyMark, ◆ placement, width-neutral)" \
    go test ./internal/sessionstatuscmd/...
assert_cmd "monitor tests (taskWorktreeDirty, worktree path)" \
    go test ./internal/monitor/...

# ─── 3. end-to-end flat-view ◆ ──────────────────────────────────────────────

command -v sqlite3 >/dev/null || { printf 'ERROR: sqlite3 required to seed the test DB\n' >&2; exit 1; }
command -v git     >/dev/null || { printf 'ERROR: git required for worktree fixtures\n' >&2; exit 1; }

TMP=$(mktemp -d)
trap 'rm -rf "${TMP}"' EXIT
CFG="${TMP}/config"
PROJ="${TMP}/proj"
mkdir -p "${CFG}" "${PROJ}"
DB="${CFG}/endless.db"

# git_init_repo DIR — a fresh repo on branch `main` with one commit, clean tree.
git_init_repo() {
    local dir="$1"
    mkdir -p "${dir}"
    git -C "${dir}" init -q -b main 2>/dev/null || { git -C "${dir}" init -q; git -C "${dir}" checkout -q -b main; }
    git -C "${dir}" -c user.email=t@t -c user.name=t commit -q --allow-empty -m init
}

# Seed a project whose path is PROJ, and three focal tasks. The flat view always
# includes the focal task (--task) via UNION, so no session rows are needed.
sqlite3 "${DB}" < "${SCHEMA}" >/dev/null
sqlite3 "${DB}" "
    INSERT INTO projects (id, name, path, status, created_at, updated_at)
      VALUES (1, 'p', '${PROJ}', 'active', '2026-07-01T00:00:00', '2026-07-01T00:00:00');
    INSERT INTO tasks (id, project_id, title, status, phase, created_at, updated_at) VALUES
      (701, 1, 'dirty-worktree',  'underway', 'now', '2026-07-01T00:00:00', '2026-07-01T00:00:00'),
      (702, 1, 'clean-worktree',  'underway', 'now', '2026-07-01T00:00:00', '2026-07-01T00:00:00'),
      (703, 1, 'no-worktree',     'underway', 'now', '2026-07-01T00:00:00', '2026-07-01T00:00:00');
" >/dev/null

WT_DIR="${PROJ}/.endless/worktrees"

# 701: dirty worktree (clean commit on main + one untracked file → diverges).
git_init_repo "${WT_DIR}/e-701"
: > "${WT_DIR}/e-701/uncommitted.txt"

# 702: clean worktree at main (no changes, HEAD == main → not dirty).
git_init_repo "${WT_DIR}/e-702"

# 703: no worktree directory at all → not dirty.

# status CMD — run the flat view for a given focal task against the temp DB.
# The headless --task flag (E-1685) names the focal directly AND skips PinMainDB,
# so session-status reads the resolved --config-dir context (this seeded DB).
status() { "${BIN}" --config-dir "${CFG}" session-status --task "$1"; }

section "3. End-to-end: flat-view ◆ dirty indicator"
assert_contains     "dirty worktree → ◆ before id"     "T◆E-701" status 701
assert_contains     "clean worktree → space before id" "T E-702" status 702
assert_not_contains "clean worktree → no ◆"            "◆"        status 702
assert_not_contains "no worktree    → no ◆"            "◆"        status 703
assert_contains     "no worktree    → space before id" "T E-703" status 703

# tree CMD — the IDs-only --tree view for a given focal task. The dirty ◆ is a
# FLAT-view marker only; --tree must NOT carry it (separate render path).
tree() { "${BIN}" --config-dir "${CFG}" session-status --tree --task "$1"; }

section "4. Cross-view focal marker: --tree uses ● (matches flat's ● this)"
assert_contains     "--tree marks focal with ●"        "●E-701" tree 701
assert_not_contains "--tree no longer uses * for focal" "*E-701" tree 701
assert_not_contains "--tree carries no flat ◆ dirty marker" "◆"  tree 701

summary

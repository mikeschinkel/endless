#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1707 and records what was true when E-1707
# landed. Edit it only if you ARE E-1707. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1707 verification suite — the SINGLE entry point for verifying E-1707.
#
#   esu
#   endless task verify E-1707
#
# Self-contained: it builds the worktree binaries, runs the Go unit tests that
# pin the render/color logic, then drives the real worktree-built endless-go
# binary end-to-end against a throwaway temp DB built from the shipped
# internal/schema/schema.sql, with per-task git worktree fixtures under a temp
# project root. Nothing in the sandbox or real ledger is touched. Exit 0 on
# all-passed, 1 on any failure (with detail to diagnose).
#
# The bug (E-1707): colorize() dimmed every terminal-status row, and dim WON over
# the ◆ unsettled marker (E-1701) — so a completed-but-unlanded task rendered grey,
# read as done, and the land it needed was easy to miss (hit live on E-1687).
# The fix: ◆ vetoes dim (terminal, later and maybe alike), at NORMAL weight.
#
# What it proves:
#   1. Worktree builds clean.
#   2. colorize()'s full intensity matrix and the render path with color ON —
#      TestColorize + TestRenderUnsettledRowNotDimmed — plus the whole
#      sessionstatuscmd + monitor suites for regression. The ANSI assertions live
#      in Go because the color decision is gated on stdout being a real tty
#      (colorEnabled()), which a captured-output shell harness cannot present;
#      those tests exercise renderTo, the same production render path the binary
#      calls, so nothing but the tty probe itself is left uncovered.
#   3. End-to-end through the binary: a terminal-status row with a diverged
#      worktree still renders ◆ in the id column and still documents it in the
#      legend (the E-1701 behavior E-1707 must not regress), a landed one does not,
#      and the ◆/space swap stays width-neutral.

# Refuse a direct run, and pick up the shared harness vocabulary. Sourced as the
# FIRST executable statement so the refusal fires before anything in this file
# runs; every definition below overrides the harness's own, so a suite written
# before the harness existed behaves exactly as it did.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

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

# fail_fast MSG — abort the run when a foundational stage failed, so later stages
# don't report a cascade of derived failures that obscure the real cause.
fail_fast() {
    if [[ "${FAIL_COUNT}" -ne 0 ]]; then
        printf '\n  %s%s%s\n\n' "${RED}${BOLD}" "$1" "${RESET}"
        summary
        exit 1
    fi
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
fail_fast "BUILD FAILED — later stages would only report derived failures."

# ─── 2. unit tests: the color rule ──────────────────────────────────────────

section "2. Unit tests: ◆ vetoes dim (the E-1707 fix)"
assert_cmd "colorize intensity matrix — ◆ suppresses dim for terminal/later/maybe, urgent stays bold" \
    go test ./internal/sessionstatuscmd/ -run 'TestColorize' -count=1
assert_cmd "render path with color ON — unsettled terminal row carries no dim escape, settled one still does" \
    go test ./internal/sessionstatuscmd/ -run 'TestRenderUnsettledRowNotDimmed' -count=1
fail_fast "THE E-1707 RULE ITSELF FAILED — the rest of the suite only checks it did not break neighbors."

section "3. Unit tests: no regression in the renderer"
assert_cmd "full sessionstatuscmd suite (classify, legend, ◆ placement, widths, focal expansion)" \
    go test ./internal/sessionstatuscmd/... -count=1
assert_cmd "monitor suite (unsettled git probe, worktree paths)" \
    go test ./internal/monitor/... -count=1

# ─── 4. end-to-end through the real binary ──────────────────────────────────

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

# Two terminal-status (`completed`) tasks — exactly the E-1687 shape that exposed
# the bug. The flat view always includes the focal task (--task) via UNION, so no
# session rows are needed.
sqlite3 "${DB}" < "${SCHEMA}" >/dev/null
sqlite3 "${DB}" "
    INSERT INTO projects (id, name, path, status, created_at, updated_at)
      VALUES (1, 'p', '${PROJ}', 'active', '2026-07-01T00:00:00', '2026-07-01T00:00:00');
    INSERT INTO tasks (id, project_id, title, status, phase, created_at, updated_at) VALUES
      (707, 1, 'completed but unlanded', 'completed', 'now', '2026-07-01T00:00:00', '2026-07-01T00:00:00'),
      (708, 1, 'completed and landed',   'completed', 'now', '2026-07-01T00:00:00', '2026-07-01T00:00:00');
" >/dev/null

WT_DIR="${PROJ}/.endless/worktrees"

# 707: terminal status + diverged worktree (untracked file) → unsettled → ◆.
git_init_repo "${WT_DIR}/e-707"
: > "${WT_DIR}/e-707/uncommitted.txt"

# 708: terminal status + clean worktree at main → settled → no ◆.
git_init_repo "${WT_DIR}/e-708"

# status ID — run the flat view for a given focal task against the temp DB. The
# headless --task flag (E-1685) names the focal directly AND skips PinMainDB, so
# session-status reads the resolved --config-dir context (this seeded DB).
status() { "${BIN}" --config-dir "${CFG}" session-status --task "$1"; }

section "4. End-to-end: the ◆ E-1707 protects is still rendered"
assert_contains     "terminal + unlanded → ◆ before id"        "T◆E-707" status 707
assert_contains     "terminal + unlanded → ✓ done phase char"  "◆E-707  ✓" status 707
assert_contains     "terminal + unlanded → legend documents ◆" "◆ unsettled" status 707
assert_contains     "terminal + landed   → space before id"    "T E-708" status 708
assert_not_contains "terminal + landed   → no ◆ anywhere"      "◆"        status 708

# The ◆/space swap must stay width-neutral: the id must start at the same display
# column in both rows. Count CHARACTERS (a UTF-8 locale), not bytes — ◆ is 3 bytes
# but the single column it replaces the space with.
section "5. End-to-end: ◆ does not shift the id column"
id_col() {
    local line
    line=$(status "$1" | grep -- "E-$1")
    printf '%s' "${line%%E-$1*}" | LC_ALL=en_US.UTF-8 wc -m | tr -d ' '
}
u_col=$(id_col 707)
s_col=$(id_col 708)
if [[ "${u_col}" == "${s_col}" ]]; then
    report_pass "id column unshifted by ◆ (both at display col ${u_col})"
else
    report_fail "id column unshifted by ◆" "equal widths" "unsettled=${u_col} settled=${s_col}"
fi

summary

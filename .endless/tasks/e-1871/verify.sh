#!/usr/bin/env bash
#
# E-1871 verification suite — the SINGLE entry point for verifying E-1871.
#
#   esu
#   endless task verify E-1871
#
# Self-contained: it builds the worktree binaries, runs the Go unit tests that pin
# the new classification as a fail-fast gate, then drives the real worktree-built
# endless-go binary end-to-end against a throwaway temp DB built from the shipped
# internal/schema/schema.sql. Nothing in the sandbox or real ledger is touched.
# Exit 0 on all-passed, 1 on any failure (with detail to diagnose).
#
# The bug (E-1871): classify() (internal/sessionstatuscmd/session_status.go) had no
# case for the five terminal statuses (confirmed/assumed/declined/obsolete/completed),
# so under `endless session status --all` an ordinary done task fell through to
# default → actUnknown and rendered ⁇ — the should-never-happen glyph — while the
# legend advertised "⁇ unknown". declined and obsolete never land, so they hit it
# ALWAYS. That destroyed ⁇'s value as a diagnostic: a genuinely unhandled status
# would be invisible in the noise.
# The fix: a new actDone — glyph ⇥ (U+21E5), label `closed` — appended after
# actUnknown, routed from the existing isTerminal() helper. ⏚ landed still wins,
# so ⇥ marks only closed work that never merged.
#
# What it proves:
#   1. Worktree builds clean.
#   2. The rule itself — TestClassify (all five statuses → actDone, the ⏚-wins
#      precedence, decorations still win), TestTerminalStatusNeverUnknown (stated as
#      the bug: no terminal status may EVER read ⁇), TestActionIcons (⇥, width 1,
#      appended not inserted), TestBuildLegend (⇥ closed + ✓ done, never ⁇ unknown),
#      TestSortRows (closed sorts last, below the ⁇ anomaly rows).
#   3. No regression across the whole sessionstatuscmd + monitor suites.
#   4. End-to-end through the binary: a seeded epic with confirmed/declined/obsolete/
#      landed/ready children renders ⇥ for the closed-and-unlanded ones, ⏚ for the
#      landed one, ⁇ nowhere, the right legend, and no id-column shift.

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

# ─── 2. unit tests: the rule itself (fail-fast gate) ────────────────────────

section "2. Unit tests: terminal statuses route to ⇥ closed (the E-1871 fix)"
assert_cmd "classify — all five terminal statuses → actDone; ⏚ landed still wins; decorations still win" \
    go test ./internal/sessionstatuscmd/ -run 'TestClassify' -count=1
assert_cmd "the bug, stated as the bug — no terminal status may ever classify as ⁇ unknown" \
    go test ./internal/sessionstatuscmd/ -run 'TestTerminalStatusNeverUnknown' -count=1
assert_cmd "glyph — ⇥ is actDone's icon, measures display width 1, and was appended after actUnknown" \
    go test ./internal/sessionstatuscmd/ -run 'TestActionIcons' -count=1
assert_cmd "legend — ⇥ closed AND ✓ done for an undecorated closed row; ⏚ landed instead when it merged" \
    go test ./internal/sessionstatuscmd/ -run 'TestBuildLegend' -count=1
assert_cmd "sort rank — a closed row sorts last, below the ⁇ anomaly rows and every open row" \
    go test ./internal/sessionstatuscmd/ -run 'TestSortRows' -count=1
fail_fast "THE E-1871 RULE ITSELF FAILED — the rest of the suite only checks it did not break neighbors."

# ─── 3. no regression in the renderer ───────────────────────────────────────

section "3. Unit tests: no regression in the renderer"
assert_cmd "--tree backlog still admits only ▶ do / ✎ plan — closed work stays out (classify's other consumer)" \
    go test ./internal/sessionstatuscmd/ -run 'TestDoPlanIDsExcludesClosed' -count=1
assert_cmd "full sessionstatuscmd suite (classify, legend, ◆ placement, colors, widths, focal expansion)" \
    go test ./internal/sessionstatuscmd/... -count=1
assert_cmd "monitor suite (session-status query, landed column, unsettled git probe)" \
    go test ./internal/monitor/... -count=1

# ─── 4. end-to-end through the real binary ──────────────────────────────────

command -v sqlite3 >/dev/null || { printf 'ERROR: sqlite3 required to seed the test DB\n' >&2; exit 1; }

TMP=$(mktemp -d)
trap 'rm -rf "${TMP}"' EXIT
CFG="${TMP}/config"
PROJ="${TMP}/proj"
mkdir -p "${CFG}" "${PROJ}"
DB="${CFG}/endless.db"

# The reproduction from the task's --analysis: an epic with children covering every
# arm of the classification — closed-and-unlanded (the bug), closed-and-landed
# (⏚ must still win), the two statuses that NEVER land, and one open child so the
# open path is present in the same render.
sqlite3 "${DB}" < "${SCHEMA}" >/dev/null
sqlite3 "${DB}" "
    INSERT INTO projects (id, name, path, status, created_at, updated_at)
      VALUES (1, 'p', '${PROJ}', 'active', '2026-07-01T00:00:00', '2026-07-01T00:00:00');
    INSERT INTO tasks (id, project_id, parent_id, title, status, phase, created_at, updated_at) VALUES
      (900, 1, NULL, 'the epic I am on',            'underway',  'now', '2026-07-01T00:00:00', '2026-07-01T00:00:00'),
      (901, 1, 900,  'child: confirmed, unlanded',  'confirmed', 'now', '2026-07-01T00:00:00', '2026-07-01T00:00:00'),
      (902, 1, 900,  'child: declined',             'declined',  'now', '2026-07-01T00:00:00', '2026-07-01T00:00:00'),
      (903, 1, 900,  'child: obsolete',             'obsolete',  'now', '2026-07-01T00:00:00', '2026-07-01T00:00:00'),
      (905, 1, 900,  'child: confirmed AND landed', 'confirmed', 'now', '2026-07-01T00:00:00', '2026-07-01T00:00:00'),
      (907, 1, 900,  'child: ready',                'ready',     'now', '2026-07-01T00:00:00', '2026-07-01T00:00:00');
    -- The landed arm. Column is merge_commit_sha (NOT NULL), not landed_sha.
    INSERT INTO task_landings (task_id, branch, merge_commit_sha)
      VALUES (905, 'task/905-x', 'deadbeef');
" >/dev/null

# status_all — the flat view with done-work included, for the seeded epic. The
# headless --task flag (E-1685) names the focal directly AND skips PinMainDB, so
# session-status reads the resolved --config-dir context (this seeded DB).
status_all() { "${BIN}" --config-dir "${CFG}" session-status --task 900 --all; }

section "4. End-to-end: closed rows render ⇥, not ⁇"
assert_contains     "confirmed, never landed → ⇥"           "⇥ T E-901" status_all
assert_contains     "declined (never lands)  → ⇥"           "⇥ T E-902" status_all
assert_contains     "obsolete (never lands)  → ⇥"           "⇥ T E-903" status_all
assert_contains     "confirmed AND landed    → ⏚ (⏚ wins)"  "⏚ T E-905" status_all
assert_contains     "open child unaffected   → ▶"           "▶ T E-907" status_all
assert_not_contains "⁇ appears NOWHERE in the render"       "⁇"         status_all

section "5. End-to-end: the legend"
assert_contains     "legend documents ⇥ closed"   "⇥ closed"   status_all
assert_contains     "legend keeps ✓ done"         "✓ done"     status_all
assert_contains     "legend keeps ⏚ landed"       "⏚ landed"   status_all
assert_not_contains "legend drops ⁇ unknown"      "⁇ unknown"  status_all

# ⇥ must be display width 1 like every other column-1 glyph, or every closed row
# shifts right. Count CHARACTERS (a UTF-8 locale), not bytes — ⇥ is 3 bytes wide.
section "6. End-to-end: ⇥ does not shift the id column"
id_col() {
    local line
    line=$(status_all | grep -- "E-$1")
    printf '%s' "${line%%E-$1*}" | LC_ALL=en_US.UTF-8 wc -m | tr -d ' '
}
closed_col=$(id_col 901)
landed_col=$(id_col 905)
open_col=$(id_col 907)
if [[ "${closed_col}" == "${landed_col}" && "${closed_col}" == "${open_col}" ]]; then
    report_pass "id column unshifted by ⇥ (closed/landed/open all at display col ${closed_col})"
else
    report_fail "id column unshifted by ⇥" "equal widths" \
        "closed=${closed_col} landed=${landed_col} open=${open_col}"
fi

# Without --all the terminal rows are filtered out entirely — the reason this bug
# stayed hidden day to day. Pin that the fix did not make closed work leak into the
# default view.
section "7. End-to-end: --all is still what surfaces closed rows"
status_default() { "${BIN}" --config-dir "${CFG}" session-status --task 900; }
assert_not_contains "default view still hides closed rows" "E-901"   status_default
assert_not_contains "default view carries no ⇥"            "⇥"       status_default
assert_contains     "default view still shows open work"   "T E-907" status_default

summary

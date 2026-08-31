#!/usr/bin/env bash
#
# E-1767 verification suite — the SINGLE entry point for verifying E-1767.
#
#   esu
#   endless task verify E-1767
#
# Self-contained: builds the worktree binaries, runs the Go executor/replay unit
# tests and the Python relation tests, then drives the real worktree-built
# endless-go binary end-to-end against a throwaway temp DB built from the shipped
# internal/schema/schema.sql. Nothing in the sandbox or real ledger is touched.
# Exit 0 on all-passed, 1 on any failure (with detail to diagnose).
#
# What it proves:
#   1. Worktree builds clean.
#   2. Go executor logic — execTaskDepCreated/Deleted write the task_deps row and
#      record a 'revisited' touch for BOTH endpoints; skipped without a session;
#      delete keyed on dep_type; and the replay handlers reproduce the rows with
#      no session_tasks write (internal/events unit tests).
#   3. Python emit wiring + swap — link/unlink now emit task_dep.* through the Go
#      executor; friendly duplicate/no-match messages preserved (test_relations).
#   4. The REAL binary, driven end-to-end: emitting task_dep.created/​deleted with
#      a session records the session_tasks 'revisited' touch for both endpoints;
#      a session-less actor writes the row but records no touch.

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

assert_eq() {
    local desc="$1" want="$2" got="$3"
    if [[ "${got}" == "${want}" ]]; then report_pass "${desc}"; else report_fail "${desc}" "${want}" "${got}"; fi
}
assert_emit_ok() {
    local desc="$1"; shift
    local out rc
    out=$("$@" 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "${desc}"; else report_fail "${desc}" "exit 0" "exit=${rc} | ${out}"; fi
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

# ─── temp environment ───────────────────────────────────────────────────────

TMP=$(mktemp -d)
trap 'rm -rf "${TMP}"' EXIT
CFG="${TMP}/config"
mkdir -p "${CFG}"
DB="${CFG}/endless.db"
REPO="${TMP}/repo"
mkdir -p "${REPO}"
git -C "${REPO}" init -q
git -C "${REPO}" config user.email t@t && git -C "${REPO}" config user.name t

q() { sqlite3 "${DB}" "$1"; }

# rel SID TID → the session_tasks relation slug for session 42 (empty if no row).
rel() {
    q "SELECT COALESCE(r.slug, '') FROM session_tasks st
         LEFT JOIN session_task_relations r ON r.id = st.relation_id
        WHERE st.session_id = 42 AND st.task_id = $1;"
}
dep_count() {
    q "SELECT count(*) FROM task_deps
        WHERE source_type='task' AND source_id=$1
          AND target_type='task' AND target_id=$2 AND dep_type='$3';"
}
touch_count() { q "SELECT count(*) FROM session_tasks;"; }

# emit KIND SRC TGT DEPTYPE [SESSION] — drive the real binary's event executor.
# --config-dir pins the DB (wins over cwd sandbox self-detection); a 5th arg
# adds --session-id, its absence emits a session-less (system) actor.
emit() {
    local kind="$1" src="$2" tgt="$3" dep="$4" session="${5:-}"
    local args=(
        --config-dir "${CFG}" event emit
        --kind "${kind}"
        --project p
        --entity-type task_dep
        --entity-id "${src}"
        --node-id 00a1
        --project-root "${REPO}"
        --payload "{\"source_id\":${src},\"target_id\":${tgt},\"dep_type\":\"${dep}\"}"
    )
    if [[ -n "${session}" ]]; then
        args+=(--actor-kind cli --actor-id verify@host --session-id "${session}")
    else
        args+=(--actor-kind system --actor-id verify@host)
    fi
    "${BIN}" "${args[@]}"
}

seed_db() {
    sqlite3 "${DB}" < "${SCHEMA}" >/dev/null
    sqlite3 "${DB}" "
        INSERT INTO projects (id, name, path, status, created_at, updated_at)
          VALUES (1, 'p', '${REPO}', 'active', '2026-07-10T00:00:00', '2026-07-10T00:00:00');
        INSERT INTO sessions (id, state) VALUES (42, 'working');
    "
}

# ─── checks ─────────────────────────────────────────────────────────────────

section "Build — worktree binaries (just build)"
# The end-to-end checks exercise the freshly built bin/endless-go, so a clean
# build is a hard prerequisite. Abort early if it fails.
if ! build_out=$(just build 2>&1); then
    report_fail "just build" "exit 0" "$(printf '%s\n' "${build_out}" | tail -8 | tr '\n' '⏎')"
    summary
    exit 1
fi
report_pass "just build"

section "Go unit tests — executors record touch for both endpoints; replay reproduces rows"
assert_cmd "go test internal/events (TestExecTaskDep* + TestReplayTaskDep*)" \
    go test ./internal/events/ -count=1 -run 'TestExecTaskDep|TestReplayTaskDep'

section "Python — link/unlink emit through Go; swap + friendly errors preserved"
assert_cmd "pytest tests/test_relations.py" \
    uv run pytest tests/test_relations.py -q

section "End-to-end — task_dep.created with a session touches BOTH endpoints"
seed_db
assert_emit_ok "emit task_dep.created 100→101 (blocks, session 42)" \
    emit task_dep.created 100 101 blocks 42
assert_eq "task_deps row written" "1" "$(dep_count 100 101 blocks)"
assert_eq "source endpoint (100) enrolled as revisited" "revisited" "$(rel 100)"
assert_eq "target endpoint (101) enrolled as revisited" "revisited" "$(rel 101)"

section "End-to-end — a 'block' relation (blocker→blocked) touches both"
# `task block 102 --by 103` stores blocks with blocker=source; the Python swap is
# covered by tests/test_relations.py (test_link_blocked_by_swaps). Here we drive
# the resulting event and assert both endpoints enroll.
assert_emit_ok "emit task_dep.created 103→102 (blocks, session 42)" \
    emit task_dep.created 103 102 blocks 42
assert_eq "blocker (103) enrolled as revisited" "revisited" "$(rel 103)"
assert_eq "blocked (102) enrolled as revisited" "revisited" "$(rel 102)"

section "End-to-end — a session-less actor writes the row but records NO touch"
BEFORE=$(touch_count)
assert_emit_ok "emit task_dep.created 300→301 (blocks, no session)" \
    emit task_dep.created 300 301 blocks
assert_eq "task_deps row still written" "1" "$(dep_count 300 301 blocks)"
assert_eq "no new session_tasks touch recorded" "${BEFORE}" "$(touch_count)"

section "End-to-end — task_dep.deleted removes the row (keyed on dep_type)"
assert_emit_ok "emit task_dep.deleted 100→101 (blocks, session 42)" \
    emit task_dep.deleted 100 101 blocks 42
assert_eq "task_deps row removed" "0" "$(dep_count 100 101 blocks)"

summary

#!/usr/bin/env bash
#
# E-1771 verification — `endless task report <id>`: the steering-prompt
# reporting command. The command computes the facts it can (status, follow-ups,
# children, worktree state), gates the free-text payload (notes/questions) with
# a per-entry Haiku check, and prints a steering prompt telling the agent to
# relay only those facts. This script is the single fail-fast verification.
#
# Run from anywhere inside the worktree:
#   esu
#   ./tests/tasks/e-1771-verify.sh
#
# Strategy (shape per tests/tasks/e-1758-verify.sh): a FULLY ISOLATED throwaway
# env (temp XDG_CONFIG_HOME + fresh DB, a temp git project, a real linked
# worktree) driving the CANDIDATE Python CLI (`.venv/bin/endless`) + the
# worktree-built `bin/endless-go`. Nothing touches the real endless repo/ledger.
#
# What it checks:
#   0. Fail-fast unit front: Go TaskReportFacts + session-query task-report,
#      Python tests/test_task_report.py (payload parse, mocked Haiku gate,
#      render, config surface). Project-wide `just test` is a separate pre-land
#      concern, deliberately NOT run here.
#   1. no payload           -> steer prompt + "Status:", exit 0, zero ceremony
#   2. follow-up (--cleans-up focal) renders under "Follow-ups you filed"
#   3. child (--parent focal) renders under "Children"
#   4. an untracked USER file in the worktree renders as worktree state
#   5. payload validation (no Haiku): malformed JSON / bad kind / unknown field
#      -> exit non-zero with a clear message
#   6. a project report-prompts.jsonl override wins over the embedded steer text
#
# Output: pass/fail per check, then a summary. Exit 0 all-passed, 1 any failure,
# 2 setup error.

set -u

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'
    BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi
UNDERLINE="──────────────────────────────────────────────────────────────"

section() { printf '\n%s%s%s\n%s\n' "${BOLD}" "$1" "${RESET}" "${UNDERLINE}"; }

report_pass() {
    printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"
    PASS_COUNT=$((PASS_COUNT + 1))
}

report_fail() {
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"
    printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "$2"
    printf '      %sgot:%s      %s\n' "${DIM}" "${RESET}" "$3"
    FAIL_COUNT=$((FAIL_COUNT + 1))
    FAILED_TESTS+=("$1")
}

summary() {
    printf '\n%sSummary%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    if [[ "${FAIL_COUNT}" -eq 0 ]]; then
        printf '  %s%d passed%s\n\n  %sALL PASSED%s\n\n' \
            "${GREEN}" "${PASS_COUNT}" "${RESET}" "${GREEN}${BOLD}" "${RESET}"
        return 0
    fi
    printf '  %s%d passed%s, %s%d failed%s\n\n  %sFAILED:%s\n' \
        "${GREEN}" "${PASS_COUNT}" "${RESET}" "${RED}" "${FAIL_COUNT}" "${RESET}" \
        "${RED}${BOLD}" "${RESET}"
    local t
    for t in "${FAILED_TESTS[@]}"; do printf '    - %s\n' "${t}"; done
    printf '\n'
    return 1
}

# assert_contains DESC HAYSTACK NEEDLE
assert_contains() {
    local desc="$1" hay="$2" needle="$3"
    if [[ "${hay}" == *"${needle}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "contains: ${needle}" "${hay}"
}

# assert_not_contains DESC HAYSTACK NEEDLE
assert_not_contains() {
    local desc="$1" hay="$2" needle="$3"
    if [[ "${hay}" != *"${needle}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "does NOT contain: ${needle}" "${hay}"
}

REPO_ROOT=""
WORK=""
PROJ=""
EN=""
GO=""
WT=""
TID=""
NUM=""

cleanup() { [[ -n "${WORK}" && -d "${WORK}" ]] && rm -rf "${WORK}"; }
en() { ( cd "${PROJ}" && "${EN}" "$@" ); }

add_task() {
    local out; out=$(en task add "$@" 2>&1) || { printf '%s' "${out}"; return 1; }
    printf '%s\n' "${out}" | grep -oE 'E-[0-9]+' | head -1
}

setup() {
    REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null)
    [[ -z "${REPO_ROOT}" ]] && { printf 'ERROR: not inside a git worktree\n' >&2; exit 2; }
    EN="${REPO_ROOT}/.venv/bin/endless"
    GO="${REPO_ROOT}/bin/endless-go"
    command -v uv >/dev/null 2>&1 || { printf 'ERROR: uv not on PATH\n' >&2; exit 2; }
    [[ -x "${GO}" ]] || { printf 'ERROR: %s missing — run `just build`\n' "${GO}" >&2; exit 2; }
    if [[ ! -x "${EN}" ]]; then
        ( cd "${REPO_ROOT}" && uv run endless --version >/dev/null 2>&1 ) || {
            printf 'ERROR: could not materialize .venv (uv run endless failed)\n' >&2; exit 2; }
    fi

    WORK=$(mktemp -d)
    trap cleanup EXIT

    export XDG_CONFIG_HOME="${WORK}/config"
    export XDG_CACHE_HOME="${WORK}/cache"
    export ENDLESS_AUTO_MIGRATE=1
    export PATH="${REPO_ROOT}/bin:${PATH}"
    unset ENDLESS_SESSION_ID CLAUDECODE CLAUDE_CODE_SESSION_ID 2>/dev/null || true
    mkdir -p "${XDG_CONFIG_HOME}" "${XDG_CACHE_HOME}"

    PROJ="${WORK}/proj"
    mkdir -p "${PROJ}"
    git -C "${PROJ}" init -q -b main
    git -C "${PROJ}" config user.email "verify@example.com"
    git -C "${PROJ}" config user.name "Verify"
    : > "${PROJ}/README.md"
    printf '.endless/worktree.json\n.endless/worktree.lock\n' > "${PROJ}/.gitignore"
    git -C "${PROJ}" add README.md .gitignore
    git -C "${PROJ}" commit -q -m "init"

    en register "${PROJ}" --infer --name verify1771 --status active >/dev/null 2>&1 || {
        printf 'ERROR: registering temp project failed\n' >&2; exit 2; }

    TID=$(add_task "Implement the focal thing") || {
        printf 'ERROR: creating focal task failed: %s\n' "${TID}" >&2; exit 2; }
    NUM="${TID#E-}"
    WT="${PROJ}/.endless/worktrees/e-${NUM}"
    git -C "${PROJ}" worktree add -q -b "task/${NUM}" "${WT}" main 2>/dev/null || {
        printf 'ERROR: creating worktree for %s failed\n' "${TID}" >&2; exit 2; }
    mkdir -p "${WT}/.endless"
    printf '{"kind":"task","base_branch":"main","branch":"task/%s"}\n' "${NUM}" \
        > "${WT}/.endless/worktree.json"
}

# ─── checks ─────────────────────────────────────────────────────────────────

check_unit_front() {
    section "0 — fail-fast unit front (Go + Python)"
    local out rc
    out=$(cd "${REPO_ROOT}" && go test ./internal/monitor/ -run TaskReportFacts \
          ./internal/sessionquerycmd/ -run TaskReport 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "Go report-facts + session-query tests pass"
    else report_fail "Go report-facts + session-query tests pass" "go test exit 0" "exit ${rc}
${out}"; fi

    out=$(cd "${REPO_ROOT}" && uv run pytest tests/test_task_report.py -q 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "Python tests/test_task_report.py pass"
    else report_fail "Python tests/test_task_report.py pass" "pytest exit 0" "exit ${rc}
${out}"; fi
}

check_no_payload() {
    section "1 — no payload → steering prompt with computed status, exit 0"
    local out rc
    out=$(en task report "${TID}" 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "exit 0 on the normal (no-payload) path"
    else report_fail "exit 0 on the normal (no-payload) path" "exit 0" "exit ${rc} | ${out}"; fi
    assert_contains "emits the steer header" "${out}" "Report the following to the user"
    assert_contains "renders computed status" "${out}" "Status: unplanned"
    # Clean, unrelated task → no empty-category ceremony.
    assert_not_contains "no empty 'Follow-ups' line when none" "${out}" "Follow-ups"
    assert_not_contains "no empty 'Children' line when none" "${out}" "Children"
}

check_successor() {
    section "2 — follow-up (--cleans-up focal) renders under Follow-ups"
    local sid out
    sid=$(add_task "Clean up the follow-up" --cleans-up "${TID}") || {
        report_fail "create follow-up" "task id" "${sid}"; return; }
    out=$(en task report "${TID}" 2>&1)
    assert_contains "lists the follow-up id" "${out}" "Follow-ups you filed: ${sid}"
}

check_child() {
    section "3 — child (--parent focal) renders under Children"
    local cid out
    cid=$(add_task "Build the child piece" --parent "${TID}") || {
        report_fail "create child" "task id" "${cid}"; return; }
    out=$(en task report "${TID}" 2>&1)
    assert_contains "lists the child id" "${out}" "Children: ${cid}"
}

check_anomaly() {
    section "4 — untracked USER file in the worktree renders as worktree state"
    : > "${WT}/scratch.go"
    local out
    out=$(cd "${WT}" && "${EN}" task report "${TID}" 2>&1)
    assert_contains "names the worktree-state section" "${out}" "Uncommitted/worktree state"
    assert_contains "names the uncommitted file" "${out}" "scratch.go"
    rm -f "${WT}/scratch.go"
}

check_payload_validation() {
    section "5 — payload validation (deterministic, no Haiku)"
    local out rc

    out=$(en task report "${TID}" --json '{not json' 2>&1); rc=$?
    if [[ "${rc}" -ne 0 ]]; then report_pass "malformed JSON → non-zero exit"
    else report_fail "malformed JSON → non-zero exit" "exit != 0" "exit 0 | ${out}"; fi
    assert_contains "malformed JSON message" "${out}" "not valid JSON"

    out=$(en task report "${TID}" --json '{"notes":[{"kind":"chatter","text":"x"}]}' 2>&1); rc=$?
    if [[ "${rc}" -ne 0 ]]; then report_pass "bad note kind → non-zero exit"
    else report_fail "bad note kind → non-zero exit" "exit != 0" "exit 0 | ${out}"; fi
    assert_contains "bad kind message" "${out}" "kind must be one of"

    out=$(en task report "${TID}" --json '{"summary":"hi"}' 2>&1); rc=$?
    if [[ "${rc}" -ne 0 ]]; then report_pass "unknown field → non-zero exit"
    else report_fail "unknown field → non-zero exit" "exit != 0" "exit 0 | ${out}"; fi
    assert_contains "unknown field message" "${out}" "unknown report field"
}

check_config_override() {
    section "6 — project report-prompts.jsonl overrides the embedded steer"
    printf '{"name":"steer","text":"PROJECT-STEER-MARKER\\n{facts}"}\n' \
        > "${PROJ}/.endless/report-prompts.jsonl"
    local out
    out=$(en task report "${TID}" 2>&1)
    assert_contains "project steer override wins" "${out}" "PROJECT-STEER-MARKER"
    assert_contains "facts still rendered under override" "${out}" "Status: unplanned"
    rm -f "${PROJ}/.endless/report-prompts.jsonl"
}

main() {
    setup

    printf '%sE-1771 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  cli:     %s\n' "${EN}"
    printf '  go:      %s\n' "${GO}"
    printf '  env:     isolated (%s)\n' "${WORK}"
    printf '  task:    %s (worktree %s)\n' "${TID}" "${WT}"

    check_unit_front
    check_no_payload
    check_successor
    check_child
    check_anomaly
    check_payload_validation
    check_config_override

    summary
}

main "$@"

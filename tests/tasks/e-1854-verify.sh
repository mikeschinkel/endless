#!/usr/bin/env bash
#
# E-1854 verification — "Inject a 'run endless guide' pointer into the first
# SessionStart context".
#
# The gap: Endless tells THIS repo's agent to run `endless guide` only because
# the dogfooding repo's committed CLAUDE.md says so. A downstream project that
# uses Endless as a product gets no such pointer — the hook injected the active
# task list and nothing else, and setup.py writes nothing into that project's
# CLAUDE.md — so a product user's agent had no reliable way to learn the
# workflow. The fix ships the pointer in the product: a pinned lead line on the
# first-time context injection.
#
# Run from anywhere inside the worktree:  ./tests/tasks/e-1854-verify.sh
#
# Stage 1 (fail-fast): the Go unit tests that pin the wording, the lead
# position, and the single composition site. If they fail the script stops
# before the slower end-to-end stage.
#
# Stage 2: a real end-to-end run of the worktree-built binary against a
# HERMETIC database. `endless-go --config-dir <dir> hook claude` is the
# sanctioned test seam (E-1429: an explicit --config-dir beats the hook's
# main-DB pin), so the probe never touches the user's real ledger — Stage 3
# asserts exactly that. The probe registers a throwaway project with no tasks,
# which IS the scenario this task exists for: a freshly set-up downstream
# product user.
#
# Checks:
#   1. Go unit tests (wording / lead position / one composition site).
#   2. SessionStart on a brand-new project injects the pointer as line 1.
#   3. The pointer is one-shot — a second SessionStart for the same session
#      injects nothing (it must not become a per-prompt nag).
#   4. A project WITH tasks still gets the pointer first, task list after.
#   5. UserPromptSubmit's first-injection path carries it too (same builder),
#      and the per-prompt reminder that follows does not.
#   6. The end-to-end probe left no rows in the real ledger.
#
# Exit 0 on all-passed, 1 on any failure.

set -u

# ─── globals ──────────────────────────────────────────────────────────────────

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi
UNDERLINE="──────────────────────────────────────────────────────────────"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WT_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
cd "${WT_ROOT}" || { echo "cannot cd to worktree root ${WT_ROOT}"; exit 1; }

# The pinned wording, quoted here independently of the Go source so a silent
# rewrite in either place is caught rather than tautologically confirmed.
PIN='New to this project? Run `endless guide` to learn the Endless workflow.'

# ─── output helpers ───────────────────────────────────────────────────────────

section() { printf '\n%s%s%s\n%s\n' "${BOLD}" "$1" "${RESET}" "${UNDERLINE}"; }
report_pass() { printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"; PASS_COUNT=$((PASS_COUNT + 1)); }
report_fail() {
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"
    printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "$2"
    printf '      %sgot:%s      %s\n' "${DIM}" "${RESET}" "$3"
    FAIL_COUNT=$((FAIL_COUNT + 1)); FAILED_TESTS+=("$1")
}

summary() {
    printf '\n%sSummary%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    if [[ "${FAIL_COUNT}" -eq 0 ]]; then
        printf '  %s%d passed%s\n\n  %sALL PASSED%s\n\n' "${GREEN}" "${PASS_COUNT}" "${RESET}" "${GREEN}${BOLD}" "${RESET}"
        return 0
    fi
    printf '  %s%d passed%s, %s%d failed%s\n\n  %sFAILED:%s\n' \
        "${GREEN}" "${PASS_COUNT}" "${RESET}" "${RED}" "${FAIL_COUNT}" "${RESET}" "${RED}${BOLD}" "${RESET}"
    local t; for t in "${FAILED_TESTS[@]}"; do printf '    - %s\n' "${t}"; done
    printf '\n'; return 1
}

# ─── Stage 1: Go unit tests (fail-fast) ───────────────────────────────────────

section "Stage 1 — Go unit tests (fail-fast)"

UNIT_TESTS='TestGuidePointer_Wording|TestWithGuidePointer_LeadsTheInjection|TestWithGuidePointer_EmptyTaskContext|TestWithGuidePointer_NoTasksYet|TestWithGuidePointer_NotRepeated'

UNIT_LOG="$(mktemp -t e1854-unit)"
if go test ./internal/hookcmd/ -run "${UNIT_TESTS}" -count=1 >"${UNIT_LOG}" 2>&1; then
    report_pass "hookcmd guide-pointer tests (wording, lead position, one composition site)"
else
    report_fail "hookcmd guide-pointer tests" "go test PASS" "$(tail -5 "${UNIT_LOG}")"
fi
rm -f "${UNIT_LOG}"

if [[ "${FAIL_COUNT}" -ne 0 ]]; then
    printf '\n%sfail-fast: unit tests failed; skipping end-to-end stage%s\n' "${RED}" "${RESET}"
    summary; exit 1
fi

# ─── Stage 2: end-to-end through the worktree binary, hermetic DB ─────────────

section "Stage 2 — end-to-end through endless-go hook (hermetic DB)"

GOBIN="${WT_ROOT}/bin/endless-go"
if [[ ! -x "${GOBIN}" ]]; then
    report_fail "worktree binary present" "bin/endless-go executable" "missing — run: just build"
    summary; exit 1
fi
if ! command -v python3 >/dev/null 2>&1; then
    report_fail "python3 available" "python3 on PATH (used to decode the hook's JSON)" "not found"
    summary; exit 1
fi

PROBE_DIR="$(mktemp -d -t e1854-probe)"
trap 'rm -rf "${PROBE_DIR}"' EXIT
CFG_DIR="${PROBE_DIR}/cfg"; PROJ_DIR="${PROBE_DIR}/downstream-proj"
mkdir -p "${CFG_DIR}" "${PROJ_DIR}"

# Fire one hook event and print the decoded additionalContext (empty when the
# hook emitted nothing, which is how "no injection" is asserted below).
# XDG_CONFIG_HOME and TMUX_PANE are stripped: the first would route config/log
# writes at the caller's sandbox, and the second would make the probe collide
# with — and invalidate — the live session occupying that tmux pane.
fire() { # <session_id> <event> [source]
    printf '{"session_id":"%s","cwd":"%s","hook_event_name":"%s","source":"%s","prompt":"probe"}\n' \
        "$1" "${PROJ_DIR}" "$2" "${3:-startup}" |
        env -u XDG_CONFIG_HOME -u TMUX_PANE "${GOBIN}" --config-dir "${CFG_DIR}" hook claude 2>/dev/null |
        python3 -c 'import json,sys
raw = sys.stdin.read().strip()
sys.stdout.write(json.loads(raw).get("additionalContext", "") if raw else "")'
}

# 1. Brand-new downstream project, no tasks filed: the first-run product user.
CTX="$(fire probe-a SessionStart)"
FIRST_LINE="$(printf '%s' "${CTX}" | head -1)"
if [[ "${FIRST_LINE}" == "${PIN}" ]]; then
    report_pass "SessionStart on a fresh project leads with the pinned guide pointer"
else
    report_fail "SessionStart on a fresh project leads with the pinned guide pointer" \
        "${PIN}" "${FIRST_LINE:-<no injection at all>}"
fi
if printf '%s' "${CTX}" | grep -q "No tasks yet"; then
    report_pass "the task-list body still follows the pointer (no tasks yet)"
else
    report_fail "the task-list body still follows the pointer" "'No tasks yet' after the pointer" "${CTX:-<empty>}"
fi

# 2. One-shot: the same session's next SessionStart injects nothing at all.
AGAIN="$(fire probe-a SessionStart resume)"
if [[ -z "${AGAIN}" ]]; then
    report_pass "second SessionStart for the same session injects nothing (one-shot)"
else
    report_fail "second SessionStart for the same session injects nothing" "<empty>" "${AGAIN}"
fi

# 3. A project WITH active tasks: pointer first, task list after.
DB="${CFG_DIR}/endless.db"
if [[ ! -f "${DB}" ]]; then
    report_fail "hermetic DB created at --config-dir" "${DB}" "missing"
    summary; exit 1
fi
sqlite3 "${DB}" \
    "INSERT INTO tasks (project_id, title, description, status, phase)
     VALUES ((SELECT id FROM projects WHERE path='${PROJ_DIR}'),
             'Do the thing','Do the thing','ready','now');" 2>/dev/null \
    || { report_fail "seed a task into the hermetic DB" "insert OK" "sqlite3 insert failed"; summary; exit 1; }

CTX="$(fire probe-b SessionStart)"
FIRST_LINE="$(printf '%s' "${CTX}" | head -1)"
POINTER_AT="$(printf '%s' "${CTX}" | grep -n -F "${PIN}" | head -1 | cut -d: -f1)"
TASKS_AT="$(printf '%s' "${CTX}" | grep -n -F "Do the thing" | head -1 | cut -d: -f1)"
if [[ "${FIRST_LINE}" == "${PIN}" && -n "${TASKS_AT}" && "${POINTER_AT}" -lt "${TASKS_AT}" ]]; then
    report_pass "with active tasks, the pointer leads and the task list follows"
else
    report_fail "with active tasks, the pointer leads and the task list follows" \
        "pointer on line 1, task line after it" \
        "pointer=${POINTER_AT:-none} tasks=${TASKS_AT:-none} first=${FIRST_LINE:-<empty>}"
fi

# 4. UserPromptSubmit's first-injection path shares the builder; the
#    per-prompt reminder that follows must not repeat the pointer.
CTX="$(fire probe-c UserPromptSubmit)"
if [[ "$(printf '%s' "${CTX}" | head -1)" == "${PIN}" ]]; then
    report_pass "first UserPromptSubmit also leads with the pointer"
else
    report_fail "first UserPromptSubmit also leads with the pointer" "${PIN}" "${CTX:-<empty>}"
fi
NEXT="$(fire probe-c UserPromptSubmit)"
if ! printf '%s' "${NEXT}" | grep -q -F "${PIN}"; then
    report_pass "later prompts in that session never repeat the pointer"
else
    report_fail "later prompts in that session never repeat the pointer" "no pointer" "${NEXT}"
fi

# ─── Stage 3: the probe stayed out of the real ledger ─────────────────────────

section "Stage 3 — no residue in the real ledger"

REAL_DB="${HOME}/.config/endless/endless.db"
if [[ -f "${REAL_DB}" ]]; then
    LEAKED="$(sqlite3 "${REAL_DB}" \
        "SELECT (SELECT count(*) FROM sessions WHERE session_id LIKE 'probe-%')
              + (SELECT count(*) FROM activity WHERE session_context LIKE '%probe-a%'
                                                  OR session_context LIKE '%probe-b%'
                                                  OR session_context LIKE '%probe-c%');" 2>/dev/null)"
    if [[ "${LEAKED}" == "0" ]]; then
        report_pass "--config-dir kept every probe write out of the user's real DB"
    else
        report_fail "--config-dir kept every probe write out of the user's real DB" \
            "0 probe rows in ${REAL_DB}" "${LEAKED:-<query failed>} rows"
    fi
else
    report_pass "no real ledger on this machine to pollute"
fi

summary

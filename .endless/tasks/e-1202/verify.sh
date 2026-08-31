#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1202 and records what was true when E-1202
# landed. Edit it only if you ARE E-1202. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1202 verification — the preToolUse gate that blocks direct Write/Edit of a
# task plan-file mirror (.endless/plans/E-NNN.md).
#
# Self-contained: builds bin/endless-go, runs the Go unit test, drives the
# worktree hook binary with synthetic PreToolUse payloads, and sanity-checks that
# `endless task update --text-file` still loads content correctly.
#
# Single command to run from anywhere inside the worktree:
#   endless task verify E-1202
#
# Exit 0 on all-passed, 1 on any failure. Modeled on .endless/tasks/e-1577/verify.sh.
#
# The gate is pure path-matching (no DB), so the hook-driven checks assert on exit
# code + message text. Non-plan paths may still be refused by the *worktree* gate
# (a synthetic session doesn't own the lock) — those checks therefore assert only
# that the *plan-file* gate did NOT fire (its unique phrase is absent), which is
# exactly what E-1202 governs.

# Refuse a direct run, and pick up the shared harness vocabulary. Sourced as the
# FIRST executable statement so the refusal fires before anything in this file
# runs; every definition below overrides the harness's own, so a suite written
# before the harness existed behaves exactly as it did.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

# ─── globals ────────────────────────────────────────────────────────────────

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi
UNDERLINE="──────────────────────────────────────────────────────────────"

# The plan-file gate's unique block phrases (see blockPlanFileWriteIfApplicable).
PLAN_PHRASE="refusing a direct Write/Edit of a task plan file"
HINT_PHRASE="--text-file <path>"

WT=""       # worktree root (set in main)
BIN=""      # worktree hook binary
HOOK_OUT="" # last hook invocation's combined output
HOOK_RC=0   # last hook invocation's exit code

# ─── output ─────────────────────────────────────────────────────────────────

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

# ─── hook driver ────────────────────────────────────────────────────────────

# run_hook TOOL KEY PATH — drive the hook binary with a synthetic PreToolUse
# payload. KEY is the tool_input field name ("file_path" for Write/Edit,
# "notebook_path" for NotebookEdit). Captures HOOK_OUT (stdout+stderr) and HOOK_RC.
run_hook() {
    local tool="$1" key="$2" path="$3" payload
    payload=$(printf '{"session_id":"e1202-verify","cwd":"%s","hook_event_name":"PreToolUse","tool_name":"%s","tool_input":{"%s":"%s","content":"x"}}' \
        "${WT}" "${tool}" "${key}" "${path}")
    HOOK_OUT=$(printf '%s' "${payload}" | "${BIN}" hook claude 2>&1)
    HOOK_RC=$?
}

# ─── assertions ─────────────────────────────────────────────────────────────

# check_refused DESC — the plan-file gate must have fired: exit 2 AND both the
# plan-file phrase and the --text-file hint present.
check_refused() {
    local desc="$1"
    if [[ "${HOOK_RC}" -eq 2 && "${HOOK_OUT}" == *"${PLAN_PHRASE}"* && "${HOOK_OUT}" == *"${HINT_PHRASE}"* ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" \
        "exit 2 + plan-file block naming '${HINT_PHRASE}'" \
        "rc=${HOOK_RC} | out=${HOOK_OUT}"
}

# check_gate_silent DESC — the plan-file gate must NOT have fired (its unique
# phrase absent). The worktree gate may still block; that's fine.
check_gate_silent() {
    local desc="$1"
    if [[ "${HOOK_OUT}" != *"${PLAN_PHRASE}"* ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" \
        "plan-file gate NOT triggered (phrase absent)" \
        "rc=${HOOK_RC} | out=${HOOK_OUT}"
}

# ─── sections ───────────────────────────────────────────────────────────────

test_build() {
    section "Build — bin/endless-go (gate never tested against a stale binary)"
    local out
    out=$(just build 2>&1)
    if [[ $? -ne 0 ]]; then
        report_fail "just build" "exit 0" "${out}"
        summary
        exit 1
    fi
    report_pass "just build"
}

test_unit() {
    section "Unit — planFileRe path matcher (go test)"
    local out
    out=$(go test ./internal/hookcmd/ -run TestPlanFileRe 2>&1)
    if [[ $? -eq 0 ]]; then
        report_pass "go test TestPlanFileRe"
    else
        report_fail "go test TestPlanFileRe" "exit 0 / ok" "${out}"
    fi
}

test_gate() {
    section "Gate — hook refuses plan-file writes, ignores everything else"

    # REFUSED — canonical mirror path, all three write tools.
    run_hook "Write" "file_path" "${WT}/.endless/plans/E-999.md"
    check_refused "Write .endless/plans/E-999.md → refused (names --text-file)"

    run_hook "Edit" "file_path" "${WT}/.endless/plans/E-999.md"
    check_refused "Edit .endless/plans/E-999.md → refused"

    run_hook "NotebookEdit" "notebook_path" "${WT}/.endless/plans/E-999.md"
    check_refused "NotebookEdit .endless/plans/E-999.md → refused"

    # NOT the plan-file gate — subdir excluded, non-plan file, normal source.
    run_hook "Write" "file_path" "${WT}/.endless/plans/snapshots/E-999.md"
    check_gate_silent "Write .endless/plans/snapshots/E-999.md → plan gate silent (subdir)"

    run_hook "Write" "file_path" "${WT}/.endless/plans/notes.md"
    check_gate_silent "Write .endless/plans/notes.md → plan gate silent (non-plan file)"

    run_hook "Write" "file_path" "${WT}/internal/hookcmd/claude.go"
    check_gate_silent "Write a normal source file → plan gate silent"
}

# Wrap the Python CLI through the sandbox DB (no real-ledger pollution).
endless_sb() { uv run endless "$@" --db sandbox; }

test_sanity() {
    section "Sanity — task update --text-file loads content (not the path)"

    local tid tmpf out marker
    marker="E1202-VERIFY-CONTENT-MARKER"
    tmpf=$(mktemp)
    printf '%s\nsecond line of real content\n' "${marker}" > "${tmpf}"

    tid=$(endless_sb task add "Verify text-file content load (E-1202 sanity)" 2>&1 | grep -oE 'E-[0-9]+' | head -1)
    if [[ -z "${tid}" ]]; then
        report_fail "create sandbox task" "an E-NNN id" "no id parsed"
        rm -f "${tmpf}"
        return
    fi

    endless_sb task update "${tid}" --text-file "${tmpf}" >/dev/null 2>&1
    out=$(endless_sb task show "${tid}" --text 2>&1)

    if [[ "${out}" == *"${marker}"* ]]; then
        report_pass "--text-file stored the file's CONTENT"
    else
        report_fail "--text-file stored the file's CONTENT" "output contains ${marker}" "${out}"
    fi
    if [[ "${out}" != *"${tmpf}"* ]]; then
        report_pass "--text-file did NOT store the path string"
    else
        report_fail "--text-file did NOT store the path string" "output lacks ${tmpf}" "${out}"
    fi

    rm -f "${tmpf}"
}

# ─── main ───────────────────────────────────────────────────────────────────

main() {
    WT=$(git rev-parse --show-toplevel 2>/dev/null)
    if [[ -z "${WT}" ]]; then
        printf 'ERROR: not inside a git worktree\n' >&2
        exit 2
    fi
    cd "${WT}" || exit 2
    BIN="${WT}/bin/endless-go"

    for tool in just go uv; do
        if ! command -v "${tool}" >/dev/null 2>&1; then
            printf 'ERROR: %s not on PATH\n' "${tool}" >&2
            exit 2
        fi
    done

    printf '%sE-1202 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  worktree: %s\n' "${WT}"
    printf '  db:       sandbox (CLI) / cwd-detected sandbox (hook)\n'

    test_build
    test_unit
    test_gate
    test_sanity

    summary
}

main "$@"

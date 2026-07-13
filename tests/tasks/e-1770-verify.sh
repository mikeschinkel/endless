#!/usr/bin/env bash
#
# E-1770 verification script — the single verification command for this task.
#
# Run from anywhere inside the worktree:
#   ./tests/tasks/e-1770-verify.sh
#
# Asserts the deliverables of E-1770 — the tmux return-line and the
# spawning-task identity are fully removed from spawn handoffs:
#
#   1. No rendered handoff (any of the six templates, fg or bg) contains the
#      return line or the spawning-session identity.
#   2. bg handoffs still carry the "headless background agent" + `claude attach`
#      orientation (the only thing the collapsed block should keep).
#   3. docs/guide/orchestration.md no longer mentions the return line, the
#      `tmux select-window` line back to your window, or "the spawning session's
#      task".
#   4. The templates carry no `{{.return_anchor}}` / `{{.spawner_task}}` inputs.
#   5. Fold-in regression (fail-fast): the templatecmd Go unit tests and the four
#      handoff Python tests pass.
#
# Templates are rendered via the worktree-built endless-go so the embedded copy
# under test is exactly this branch's source. Guide content is asserted against
# the worktree's own files. Targeted pytest (not `just test`) stays off the
# unrelated guide-map coverage gate.
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure.
#
# Interim ad-hoc location tests/tasks/ (matches the prototype convention);
# migrates to .endless/tasks/<id>/ once the manifest runner lands.

set -u

# ─── globals ────────────────────────────────────────────────────────────────

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

REPO_ROOT=""
BIN=""

# The six spawn handoff templates. respawn is the reopened-task variant; it
# needs a restore_case var to render.
TEMPLATES=(task bug epic research brainstorm respawn)

# Phrases that must never appear in ANY rendered handoff after E-1770.
FORBIDDEN=(
    "switch-client"
    "move-window"
    "return to that session"
    "return line above"
    "spawning session's task"
)

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

# ─── assertions ─────────────────────────────────────────────────────────────

# assert_succeeds DESC CMD [ARGS...]  — pass if CMD exits 0.
assert_succeeds() {
    local desc="$1"; shift
    local out; out=$("$@" 2>&1); local rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "exit == 0" "exit=${rc} | ${out}"
}

# assert_stdin_has DESC PATTERN  — the piped stdin contains fixed-string PATTERN.
assert_stdin_has() {
    local desc="$1"; local pat="$2"
    local out; out=$(cat)
    if [[ "${out}" == *"${pat}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "output contains: ${pat}" "absent"
}

# assert_stdin_lacks DESC PATTERN  — the piped stdin does NOT contain PATTERN.
assert_stdin_lacks() {
    local desc="$1"; local pat="$2"
    local out; out=$(cat)
    if [[ "${out}" != *"${pat}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "output lacks: ${pat}" "present"
}

# assert_file_lacks DESC FILE PATTERN  — FILE does NOT contain PATTERN.
assert_file_lacks() {
    local desc="$1"; local file="$2"; local pat="$3"
    if ! grep -qF -- "${pat}" "${file}" 2>/dev/null; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "${file} lacks: ${pat}" "present"
}

# render TYPE BG  — render a handoff template via the worktree binary.
render() {
    local ttype="$1"; local bg="$2"
    printf '{"spawned_id":1770,"label_prefix":"E-1770","title":"Demo task","worktree_path":"/tmp/wt","branch":"task/1770-x","bg":%s,"child_count":0,"children_state":"2 ready","restore_case":"reused"}' \
        "${bg}" | "${BIN}" template render "handoff/${ttype}" 2>&1
}

# ─── checks ─────────────────────────────────────────────────────────────────

check_no_return_line() {
    section "Renders — no return line / spawning-task identity (fg + bg, all six)"
    local t bg pat
    for t in "${TEMPLATES[@]}"; do
        for bg in false true; do
            for pat in "${FORBIDDEN[@]}"; do
                render "${t}" "${bg}" | \
                    assert_stdin_lacks "${t} (bg=${bg}): no '${pat}'" "${pat}"
            done
        done
    done
}

check_bg_orientation() {
    section "Renders — bg handoffs keep the headless + claude-attach orientation"
    local t
    for t in "${TEMPLATES[@]}"; do
        # respawn's bg branch is the _close note; the per-type templates carry
        # the collapsed orientation line. Both surface "claude attach"; only the
        # per-type templates surface "headless background agent".
        render "${t}" true | assert_stdin_has "${t} (bg): mentions 'claude attach'" "claude attach"
    done
    for t in task bug epic research brainstorm; do
        render "${t}" true | \
            assert_stdin_has "${t} (bg): 'headless background agent' orientation" "headless background agent"
    done
}

check_guide() {
    section "Guide — orchestration.md no longer names the return line"
    local guide="${REPO_ROOT}/docs/guide/orchestration.md"
    assert_file_lacks "orchestration.md: no 'switch-client'" "${guide}" "switch-client"
    assert_file_lacks "orchestration.md: no 'move-window'" "${guide}" "move-window"
    assert_file_lacks "orchestration.md: no 'select-window' return line" "${guide}" "select-window"
    assert_file_lacks "orchestration.md: no 'return line'" "${guide}" "return line"
    assert_file_lacks "orchestration.md: no 'spawning session's task'" "${guide}" "spawning session's task"
}

check_dead_template_inputs() {
    section "Templates — no dead {{.return_anchor}} / {{.spawner_task}} inputs"
    local dir="${REPO_ROOT}/internal/templatecmd/templates/handoff"
    local hits
    hits=$(grep -rl -- '.return_anchor\|.spawner_task' "${dir}" 2>/dev/null)
    if [[ -z "${hits}" ]]; then
        report_pass "no handoff template references return_anchor / spawner_task"
    else
        report_fail "no handoff template references return_anchor / spawner_task" "none" "${hits}"
    fi
}

check_go_unit_tests() {
    section "Fold-in regression — templatecmd Go unit tests"
    assert_succeeds "go test ./internal/templatecmd/..." \
        go test ./internal/templatecmd/...
}

check_python_handoff_tests() {
    section "Fold-in regression — the four handoff Python tests"
    assert_succeeds "uv run pytest (handoff suite)" \
        uv run pytest -q \
            tests/test_handoff.py \
            tests/test_handoff_children_state.py \
            tests/test_template_materialize.py \
            tests/test_handoff_internal_cli.py
}

# ─── main ───────────────────────────────────────────────────────────────────

main() {
    REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null)
    if [[ -z "${REPO_ROOT}" ]]; then
        printf 'ERROR: not inside a git worktree\n' >&2; exit 2
    fi
    cd "${REPO_ROOT}" || exit 2

    printf '%sE-1770 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  repo:   %s\n' "${REPO_ROOT}"

    section "Build — worktree endless-go (candidate templates under test)"
    assert_succeeds "go build -o bin/endless-go ./cmd/endless-go" \
        go build -o bin/endless-go ./cmd/endless-go
    BIN="${REPO_ROOT}/bin/endless-go"
    if [[ ! -x "${BIN}" ]]; then
        printf 'ERROR: %s not built\n' "${BIN}" >&2; exit 2
    fi

    check_no_return_line
    check_bg_orientation
    check_guide
    check_dead_template_inputs
    check_go_unit_tests
    check_python_handoff_tests

    summary
}

main "$@"

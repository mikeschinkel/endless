#!/usr/bin/env bash
#
# E-1864 verification script — the single verification command for this task.
#
# Run from anywhere inside the worktree:
#   endless task verify E-1864
#
# E-1864 makes a settled decision reversible. Before it, `decision accept` and
# `decision reject` were one-way: `decision update` has no --status flag and no
# reverse verb existed, so an accidental accept was permanent.
#
# The shipped surface is three verbs:
#
#   decision unaccept <id>     accepted → proposed   (guarded on 'accepted')
#   decision unreject <id>     rejected → proposed   (guarded on 'rejected';
#                                                     clears rejection_reason)
#   decision reconsider <id>   whichever of the two applies (unguarded)
#
# NOT `reopen`: a decision is proposed and then accepted or rejected — it is
# never "open", so there is nothing to re-open. The naming check in stage 2 is
# a real assertion, not decoration: `reopen` was the wrong word and must not
# come back.
#
# Stages (fail-fast at each boundary — a failure stops before the slower work):
#
#   1. Unit tests: the Go executors (status guards + rejection_reason clearing,
#      against a real SQLite DB) and the Python CLI guards.
#   2. CLI surface: the three verbs exist and take ITEM_IDS...; `reopen` does not.
#   3. End-to-end through the real event path into the worktree's self-dev
#      sandbox: accept → unaccept → reject → unreject → reject → reconsider,
#      asserting the projected status at each step, that unreject clears the
#      stored reason, and that each guarded verb refuses the wrong status.
#   4. Docs: the decisions guide documents the reversals and does not describe
#      settled decisions as one-way.
#
# Stage 3 leaves one probe decision in the worktree's sandbox DB (never the
# real ledger); there is no `decision delete` CLI verb to clean it up with.
# Same bounded, inspectable pollution as .endless/tasks/e-1788/verify.sh.
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure.

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
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'
    BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi

UNDERLINE="──────────────────────────────────────────────────────────────"

REPO_ROOT=""
EN=""
DID=""

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

# fail_fast MESSAGE — stop before the next (slower) stage.
fail_fast() {
    printf '\n%sfail-fast: %s%s\n' "${RED}" "$1" "${RESET}"
    summary; exit 1
}

# ─── assertions ─────────────────────────────────────────────────────────────

# assert_succeeds DESC CMD [ARGS...]  — pass if CMD exits 0.
assert_succeeds() {
    local desc="$1"; shift
    local out; out=$("$@" 2>&1); local rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "exit == 0" "exit=${rc} | $(printf '%s' "${out}" | tail -20)"
}

# assert_fails DESC CMD [ARGS...]  — pass if CMD exits non-zero.
assert_fails() {
    local desc="$1"; shift
    local out; out=$("$@" 2>&1); local rc=$?
    if [[ "${rc}" -ne 0 ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "non-zero exit" "exit=0 | ${out}"
}

# assert_output_has DESC PATTERN CMD [ARGS...]
assert_output_has() {
    local desc="$1"; local pat="$2"; shift 2
    local out; out=$("$@" 2>&1)
    if [[ "${out}" == *"${pat}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "output contains '${pat}'" "${out}"
}

# assert_output_lacks DESC PATTERN CMD [ARGS...]
assert_output_lacks() {
    local desc="$1"; local pat="$2"; shift 2
    local out; out=$("$@" 2>&1)
    if [[ "${out}" != *"${pat}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "output lacks '${pat}'" "${out}"
}

# assert_file_has DESC FILE FIXED_STRING
assert_file_has() {
    local desc="$1"; local file="$2"; local pat="$3"
    if grep -qF -- "${pat}" "${file}" 2>/dev/null; then
        report_pass "${desc}"; return
    fi
    report_fail "${desc}" "'${pat}' present in $(basename "${file}")" "absent"
}

# ─── sandbox helpers ────────────────────────────────────────────────────────

# en ARGS... — the worktree's own CLI against the worktree's sandbox DB.
en() { ( cd "${REPO_ROOT}" && "${EN}" --db sandbox "$@" ) ; }

# decision_status ID — the projected status, read back through `decision show`.
decision_status() {
    en decision show "$1" --llm 2>/dev/null \
        | grep -E '^status=' | head -1 | cut -d= -f2-
}

# decision_reason ID — the stored rejection_reason ('' when absent/cleared).
decision_reason() {
    en decision show "$1" --llm 2>/dev/null \
        | grep -E '^rejection_reason=' | head -1 | cut -d= -f2-
}

# assert_status DESC ID WANT — the projection landed in the expected status.
assert_status() {
    local desc="$1"; local id="$2"; local want="$3"
    local got; got=$(decision_status "${id}")
    if [[ "${got}" == "${want}" ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "status=${want}" "status=${got:-<unreadable>}"
}

# ─── stage 1: unit tests ────────────────────────────────────────────────────

check_unit_tests() {
    section "1 — unit tests (fail-fast)"

    local go_tests='TestDecisionUnaccepted_|TestDecisionUnrejected_|TestDecisionReversal_'
    assert_succeeds "Go executor tests (status guards, reason clearing, round-trip)" \
        bash -c "cd '${REPO_ROOT}' && go test ./internal/events/ -run '${go_tests}' -count=1"

    assert_succeeds "Python decision command suite" \
        bash -c "cd '${REPO_ROOT}' && uv run pytest -q tests/test_decision_cmd.py"

    [[ "${FAIL_COUNT}" -eq 0 ]] || fail_fast "unit tests failed; skipping the CLI and end-to-end stages"
}

# ─── stage 2: CLI surface ───────────────────────────────────────────────────

check_cli_surface() {
    section "2 — CLI surface: three verbs, and no 'reopen'"

    local v
    for v in unaccept unreject reconsider; do
        assert_output_has "decision ${v} --help takes ITEM_IDS..." "ITEM_IDS..." \
            bash -c "cd '${REPO_ROOT}' && '${EN}' --db sandbox decision ${v} --help"
    done

    assert_output_has "decision --help lists unaccept" "unaccept" \
        bash -c "cd '${REPO_ROOT}' && '${EN}' --db sandbox decision --help"
    assert_output_has "decision --help lists unreject" "unreject" \
        bash -c "cd '${REPO_ROOT}' && '${EN}' --db sandbox decision --help"
    assert_output_has "decision --help lists reconsider" "reconsider" \
        bash -c "cd '${REPO_ROOT}' && '${EN}' --db sandbox decision --help"

    # The naming correction, asserted: 'reopen' belongs to tasks, not decisions.
    assert_output_lacks "decision --help does NOT list a 'reopen' verb" "reopen" \
        bash -c "cd '${REPO_ROOT}' && '${EN}' --db sandbox decision --help"
    assert_fails "decision reopen is not a command" \
        bash -c "cd '${REPO_ROOT}' && '${EN}' --db sandbox decision reopen --help"

    [[ "${FAIL_COUNT}" -eq 0 ]] || fail_fast "CLI surface wrong; skipping the end-to-end stage"
}

# ─── stage 3: end-to-end through the event path ─────────────────────────────

check_end_to_end() {
    section "3 — end-to-end: real events into the worktree sandbox"

    local out
    out=$(en decision add "The E-1864 probe exercises decision status reversal" \
              --project endless 2>&1)
    DID=$(printf '%s\n' "${out}" | grep -oE 'ED-[0-9]+' | head -1)
    if [[ -z "${DID}" ]]; then
        report_fail "probe decision created" "an ED-NNN id" "${out}"
        fail_fast "could not create the probe decision"
    fi
    report_pass "probe decision created (${DID})"
    assert_status "starts in 'proposed'" "${DID}" "proposed"

    # --- nothing to undo yet: both guarded verbs refuse a proposed decision.
    assert_output_has "unaccept refuses a proposed decision" "only 'accepted'" \
        bash -c "cd '${REPO_ROOT}' && '${EN}' --db sandbox decision unaccept ${DID}"
    assert_output_has "unreject refuses a proposed decision" "only 'rejected'" \
        bash -c "cd '${REPO_ROOT}' && '${EN}' --db sandbox decision unreject ${DID}"
    assert_status "still 'proposed' after both refusals" "${DID}" "proposed"

    # --- the immediate need: undo an accidental accept.
    assert_succeeds "accept" en decision accept "${DID}"
    assert_status "accept landed" "${DID}" "accepted"

    assert_output_has "unreject refuses an accepted decision, points at unaccept" \
        "unaccept" \
        bash -c "cd '${REPO_ROOT}' && '${EN}' --db sandbox decision unreject ${DID}"
    assert_status "wrong-verb refusal left the status untouched" "${DID}" "accepted"

    assert_succeeds "unaccept" en decision unaccept "${DID}"
    assert_status "unaccept returned it to 'proposed'" "${DID}" "proposed"

    # --- reject stores a reason; unreject must clear it.
    assert_succeeds "reject --reason" \
        en decision reject "${DID}" --reason "e-1864 probe reason"
    assert_status "reject landed" "${DID}" "rejected"

    local reason; reason=$(decision_reason "${DID}")
    if [[ "${reason}" == "e-1864 probe reason" ]]; then
        report_pass "reject stored the reason"
    else
        report_fail "reject stored the reason" "e-1864 probe reason" "${reason:-<empty>}"
    fi

    assert_output_has "unaccept refuses a rejected decision, points at unreject" \
        "unreject" \
        bash -c "cd '${REPO_ROOT}' && '${EN}' --db sandbox decision unaccept ${DID}"
    assert_status "wrong-verb refusal left the status untouched" "${DID}" "rejected"

    assert_succeeds "unreject" en decision unreject "${DID}"
    assert_status "unreject returned it to 'proposed'" "${DID}" "proposed"

    reason=$(decision_reason "${DID}")
    if [[ -z "${reason}" ]]; then
        report_pass "unreject cleared the stored reason"
    else
        report_fail "unreject cleared the stored reason" "no rejection_reason" "${reason}"
    fi

    # --- reconsider dispatches from either terminal status.
    assert_succeeds "re-reject (re-settleable after a reversal)" \
        en decision reject "${DID}" --reason "e-1864 probe second reason"
    assert_status "re-reject landed" "${DID}" "rejected"
    assert_succeeds "reconsider from 'rejected'" en decision reconsider "${DID}"
    assert_status "reconsider returned it to 'proposed'" "${DID}" "proposed"

    assert_succeeds "re-accept" en decision accept "${DID}"
    assert_succeeds "reconsider from 'accepted'" en decision reconsider "${DID}"
    assert_status "reconsider returned it to 'proposed'" "${DID}" "proposed"

    assert_output_has "reconsider refuses a proposed decision" "only 'accepted' or 'rejected'" \
        bash -c "cd '${REPO_ROOT}' && '${EN}' --db sandbox decision reconsider ${DID}"
}

# ─── stage 4: docs ──────────────────────────────────────────────────────────

check_docs() {
    section "4 — docs/guide/decisions.md documents the reversals"
    local f="${REPO_ROOT}/docs/guide/decisions.md"
    assert_file_has "documents 'decision unaccept'" "${f}" "endless decision unaccept <id>"
    assert_file_has "documents 'decision unreject'" "${f}" "endless decision unreject <id>"
    assert_file_has "documents 'decision reconsider'" "${f}" "endless decision reconsider <id>"
    assert_file_has "states there is no reopen, and why" "${f}" \
        'A decision is never "open", so there is no `reopen`'
    assert_file_has "documents that unreject clears the stored reason" "${f}" \
        "Unrejecting clears \`rejection_reason\`"
}

# ─── main ───────────────────────────────────────────────────────────────────

main() {
    REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null)
    if [[ -z "${REPO_ROOT}" ]]; then
        printf 'ERROR: not inside a git worktree\n' >&2; exit 2
    fi
    cd "${REPO_ROOT}" || exit 2

    command -v uv >/dev/null 2>&1 || { printf 'ERROR: uv not on PATH\n' >&2; exit 2; }
    command -v go >/dev/null 2>&1 || { printf 'ERROR: go not on PATH\n' >&2; exit 2; }

    printf '%sE-1864 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  repo:   %s\n' "${REPO_ROOT}"

    # The event path routes --db sandbox writes through <worktree>/bin/endless-go,
    # so the Go executors under test must be built into this worktree.
    if [[ ! -x "${REPO_ROOT}/bin/endless-go" ]]; then
        printf 'ERROR: %s/bin/endless-go missing — run: just build\n' "${REPO_ROOT}" >&2
        exit 2
    fi

    # Materialize the worktree's own venv so the CLI under test is this
    # branch's source (the global `endless` reads the main checkout).
    EN="${REPO_ROOT}/.venv/bin/endless"
    if [[ ! -x "${EN}" ]]; then
        ( cd "${REPO_ROOT}" && uv run endless --db sandbox --version >/dev/null 2>&1 ) || {
            printf 'ERROR: could not materialize .venv (uv run endless failed)\n' >&2
            exit 2; }
    fi

    check_unit_tests
    check_cli_surface
    check_end_to_end
    check_docs

    summary
}

main "$@"

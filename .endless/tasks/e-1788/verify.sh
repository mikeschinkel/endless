#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1788 and records what was true when E-1788
# landed. Edit it only if you ARE E-1788. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1788 verification script — the single verification command for this task.
#
# Run from anywhere inside the worktree:
#   endless task verify E-1788
#
# E-1788 reconciles the decisions guide with the CLI: the docs described a
# `--decision` flag on `task add` / `task update` that does not exist. The
# behavior they described ("paired decision linked via a `documents` relation")
# is really produced by `endless decision add "..." --about <task>`. This is a
# doc-only change; the script asserts the docs now match the CLI, and folds in
# a fail-fast behavioral + regression check that the premise still holds.
#
#   1. CLI premise (read-only): `task add --help` and `task update --help` do
#      NOT list `--decision`; `decision add --help` DOES list `--about`.
#   2. docs/guide/decisions.md: the stale `--decision` section is gone and the
#      paired form now documents `endless decision add ... --about`; the "see
#      also" cross-ref no longer cites a `--decision` flag.
#   3. docs/guide/tasks.md: the two `task add` / `task update` code-block
#      `--decision` lines are gone; the relations table's `documents` row now
#      attributes the link to `--about` on `endless decision add`.
#   4. docs/guide/index.md and docs/guide/sessions.md: the same false
#      `--decision` claim is replaced with `endless decision add`.
#   5. Behavioral fold-in (fail-fast, writes to the worktree sandbox DB):
#      `decision add "..." --about <task>` creates a `documents` link.
#   6. Regression fold-in (fail-fast): `uv run pytest tests/test_decision_cmd.py`
#      (covers the flag-removed guards and the documents-relation rendering).
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure.
#
# Interim ad-hoc location .endless/tasks/ (matches the prototype convention);
# migrates to .endless/tasks/<id>/ once the manifest runner lands.

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
GUIDE=""

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

# assert_file_has DESC FILE FIXED_STRING  — FILE contains FIXED_STRING.
assert_file_has() {
    local desc="$1"; local file="$2"; local pat="$3"
    if grep -qF -- "${pat}" "${file}" 2>/dev/null; then
        report_pass "${desc}"; return
    fi
    report_fail "${desc}" "'${pat}' present in $(basename "${file}")" "absent"
}

# assert_file_lacks DESC FILE FIXED_STRING  — FILE does NOT contain FIXED_STRING.
assert_file_lacks() {
    local desc="$1"; local file="$2"; local pat="$3"
    if grep -qF -- "${pat}" "${file}" 2>/dev/null; then
        report_fail "${desc}" "'${pat}' absent from $(basename "${file}")" \
            "present"; return
    fi
    report_pass "${desc}"
}

# assert_output_has DESC PATTERN CMD [ARGS...]  — CMD stdout+stderr contains PATTERN.
assert_output_has() {
    local desc="$1"; local pat="$2"; shift 2
    local out; out=$("$@" 2>&1)
    if [[ "${out}" == *"${pat}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "output contains '${pat}'" "${out}"
}

# assert_output_lacks DESC PATTERN CMD [ARGS...]  — CMD stdout+stderr lacks PATTERN.
assert_output_lacks() {
    local desc="$1"; local pat="$2"; shift 2
    local out; out=$("$@" 2>&1)
    if [[ "${out}" != *"${pat}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "output lacks '${pat}'" "${out}"
}

# ─── checks ─────────────────────────────────────────────────────────────────

check_cli_premise() {
    section "1 — CLI premise: no --decision flag; decision add has --about"
    assert_output_lacks "task add --help does not list --decision" "--decision" \
        "${EN}" --db sandbox task add --help
    assert_output_lacks "task update --help does not list --decision" "--decision" \
        "${EN}" --db sandbox task update --help
    assert_output_has "decision add --help lists --about" "--about" \
        "${EN}" --db sandbox decision add --help
}

check_decisions_guide() {
    section "2 — docs/guide/decisions.md reconciled"
    local f="${GUIDE}/decisions.md"
    assert_file_lacks "no '--decision \"' flag usage" "${f}" "--decision \""
    assert_file_lacks "stale 'Inline \`--decision\`' heading gone" "${f}" \
        "Inline \`--decision\`"
    assert_file_has "paired form documents 'decision add ... --about'" "${f}" \
        'endless decision add "Statement of the decision" --about <id>'
    assert_file_lacks "'see also' no longer cites a --decision flag" "${f}" \
        '`--decision` flag on task add/update'
    assert_file_has "'see also' points at the documents relation" "${f}" \
        'the `documents` relation between a decision and its task'
}

check_tasks_guide() {
    section "3 — docs/guide/tasks.md reconciled"
    local f="${GUIDE}/tasks.md"
    assert_file_lacks "task add code block drops --decision" "${f}" \
        'endless task add "Title here" --decision'
    assert_file_lacks "task update code block drops --decision" "${f}" \
        'endless task update <id> --decision'
    assert_file_has "documents row attributes link to decision add --about" "${f}" \
        'you pass `--about <task>` to `endless decision add`'
}

check_index_and_sessions() {
    section "4 — docs/guide/index.md + sessions.md reconciled"
    local idx="${GUIDE}/index.md"
    assert_file_lacks "index.md drops --decision" "${idx}" "--decision"
    assert_file_has "index.md uses 'decision add ... --about <current_id>'" "${idx}" \
        'endless decision add "Statement of the decision" --about <current_id>'
    local ses="${GUIDE}/sessions.md"
    assert_file_lacks "sessions.md drops 'endless task --decision'" "${ses}" \
        'endless task --decision'
    assert_file_has "sessions.md references 'endless decision add'" "${ses}" \
        '`endless decision add`'
}

check_behavior() {
    section "5 — behavioral fold-in: --about creates a documents link (sandbox)"
    local tout tid
    tout=$(cd "${REPO_ROOT}" && "${EN}" --db sandbox task add \
        "Verify e-1788 documents-relation probe" --project endless 2>&1)
    tid=$(printf '%s\n' "${tout}" | grep -oE 'E-[0-9]+' | head -1)
    if [[ -z "${tid}" ]]; then
        report_fail "probe task created" "an E-NNN id" "${tout}"; return
    fi
    report_pass "probe task created (${tid})"
    assert_output_has "decision add --about links via 'documents'" "documents" \
        "${EN}" --db sandbox decision add \
        "The e-1788 probe records a decision about a task" --about "${tid}"
}

check_regression() {
    section "6 — regression fold-in (targeted): decision command suite"
    assert_succeeds "uv run pytest tests/test_decision_cmd.py" \
        bash -c "cd '${REPO_ROOT}' && uv run pytest -q tests/test_decision_cmd.py"
}

# ─── main ───────────────────────────────────────────────────────────────────

main() {
    REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null)
    if [[ -z "${REPO_ROOT}" ]]; then
        printf 'ERROR: not inside a git worktree\n' >&2; exit 2
    fi
    cd "${REPO_ROOT}" || exit 2
    GUIDE="${REPO_ROOT}/docs/guide"

    command -v uv >/dev/null 2>&1 || { printf 'ERROR: uv not on PATH\n' >&2; exit 2; }

    printf '%sE-1788 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  repo:   %s\n' "${REPO_ROOT}"

    # Materialize the worktree's own venv so the CLI under test is this
    # branch's source (the global `endless` reads the main checkout).
    EN="${REPO_ROOT}/.venv/bin/endless"
    if [[ ! -x "${EN}" ]]; then
        ( cd "${REPO_ROOT}" && uv run endless --db sandbox --version >/dev/null 2>&1 ) || {
            printf 'ERROR: could not materialize .venv (uv run endless failed)\n' >&2
            exit 2; }
    fi

    check_cli_premise
    check_decisions_guide
    check_tasks_guide
    check_index_and_sessions
    check_behavior
    check_regression

    summary
}

main "$@"

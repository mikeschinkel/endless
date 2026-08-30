#!/usr/bin/env bash
#
# E-1001 verification script — exercises the --xxx (inline) / --xxx-file (path)
# content-flag convention end-to-end against the worktree's sandbox DB.
#
# Run from anywhere inside the worktree:
#   ./tests/tasks/e-1001-verify.sh
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure. Each new task gets a fresh ID; the script does NOT wipe the sandbox
# between runs (pollution is bounded and inspectable via
#   uv run endless task list --db sandbox).
#
# Shape/output learned from the ad-hoc prototype tests/tasks/e-1577-verify.sh
# (the convention E-1596 is formalizing).

set -u

# ─── globals ────────────────────────────────────────────────────────────────

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'
    RED=$'\033[31m'
    DIM=$'\033[2m'
    BOLD=$'\033[1m'
    RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi

UNDERLINE="──────────────────────────────────────────────────────────────"

# ─── output ─────────────────────────────────────────────────────────────────

section() {
    printf '\n%s%s%s\n' "${BOLD}" "$1" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
}

report_pass() {
    printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"
    PASS_COUNT=$((PASS_COUNT + 1))
}

report_fail() {
    local desc="$1"
    local expected="$2"
    local actual="$3"
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "${desc}"
    printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "${expected}"
    printf '      %sgot:%s      %s\n' "${DIM}" "${RESET}" "${actual}"
    FAIL_COUNT=$((FAIL_COUNT + 1))
    FAILED_TESTS+=("${desc}")
}

summary() {
    printf '\n%sSummary%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    if [[ "${FAIL_COUNT}" -eq 0 ]]; then
        printf '  %s%d passed%s\n' "${GREEN}" "${PASS_COUNT}" "${RESET}"
        printf '\n  %sALL PASSED%s\n\n' "${GREEN}${BOLD}" "${RESET}"
        return 0
    fi
    printf '  %s%d passed%s, %s%d failed%s\n' \
        "${GREEN}" "${PASS_COUNT}" "${RESET}" \
        "${RED}" "${FAIL_COUNT}" "${RESET}"
    printf '\n  %sFAILED:%s\n' "${RED}${BOLD}" "${RESET}"
    local t
    for t in "${FAILED_TESTS[@]}"; do
        printf '    - %s\n' "${t}"
    done
    printf '\n'
    return 1
}

# ─── helpers ────────────────────────────────────────────────────────────────

# Wrap the CLI so every invocation routes through the sandbox DB.
endless() {
    uv run endless "$@" --db sandbox
}

# Create a task and emit just its E-NNN id on stdout. All other output goes to
# stderr so callers can capture only the id.
add_task_get_id() {
    local title="$1"
    shift
    local output
    output=$(endless task add "${title}" "$@" 2>&1)
    local rc=$?
    if [[ "${rc}" -ne 0 ]]; then
        printf 'ERROR: add failed for %q: %s\n' "${title}" "${output}" >&2
        return 1
    fi
    printf '%s\n' "${output}" | grep -oE 'E-[0-9]+' | head -1
}

# Write CONTENT to a fresh temp file and echo its path. Caller owns cleanup
# (the script leaves them under $TMPDIR; bounded and inspectable).
mkfile() {
    local content="$1"
    local f
    f=$(mktemp)
    printf '%s' "${content}" > "${f}"
    printf '%s\n' "${f}"
}

# ─── assertions ─────────────────────────────────────────────────────────────

# assert_refused DESC PATTERN CMD [ARGS...]
#   Pass if CMD exits non-zero AND its combined output contains PATTERN.
assert_refused() {
    local desc="$1"
    local pattern="$2"
    shift 2
    local output
    output=$("$@" 2>&1)
    local rc=$?
    if [[ "${rc}" -ne 0 ]] && [[ "${output}" == *"${pattern}"* ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" \
        "exit != 0 AND output contains: ${pattern}" \
        "exit=${rc} | output=${output}"
}

# assert_succeeds DESC CMD [ARGS...]
assert_succeeds() {
    local desc="$1"
    shift
    local output
    output=$("$@" 2>&1)
    local rc=$?
    if [[ "${rc}" -eq 0 ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "exit == 0" "exit=${rc} | output=${output}"
}

# assert_contains DESC PATTERN CMD [ARGS...]
assert_contains() {
    local desc="$1"
    local pattern="$2"
    shift 2
    local output
    output=$("$@" 2>&1)
    if [[ "${output}" == *"${pattern}"* ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "output contains: ${pattern}" "${output}"
}

# assert_not_contains DESC PATTERN CMD [ARGS...]
assert_not_contains() {
    local desc="$1"
    local pattern="$2"
    shift 2
    local output
    output=$("$@" 2>&1)
    if [[ "${output}" != *"${pattern}"* ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" \
        "output does NOT contain: ${pattern}" \
        "${output}"
}

# ─── 1: flag surface (--help) ───────────────────────────────────────────────

test_flag_surface() {
    section "1 — Flag surface: --xxx-file present, justification/reason inline-only"

    assert_contains "'task add --help' shows --text-file" \
        "--text-file" endless task add --help
    assert_contains "'task add --help' shows --description-file" \
        "--description-file" endless task add --help
    assert_contains "'task update --help' shows --text-file" \
        "--text-file" endless task update --help
    assert_contains "'task update --help' shows --analysis-file" \
        "--analysis-file" endless task update --help
    assert_contains "'task update --help' shows --outcome-file" \
        "--outcome-file" endless task update --help
    assert_contains "'task update --help' shows --description-file" \
        "--description-file" endless task update --help
    assert_contains "'decision add --help' shows --description-file" \
        "--description-file" endless decision add --help

    # Deliberate partial-consistency exceptions: short flags stay inline-only.
    assert_not_contains "'task add --help' has NO --justification-file" \
        "--justification-file" endless task add --help
    assert_not_contains "'task decline --help' has NO --reason-file" \
        "--reason-file" endless task decline --help
}

# ─── 2: text — inline vs file, and the inversion ─────────────────────────────

test_text_inline_file_inversion() {
    section "2 — --text inline vs --text-file path (incl. the inversion)"

    local tid f bad
    f=$(mkfile $'plan body from file\nline two')

    tid=$(add_task_get_id "Audit text-inline")
    endless task update "${tid}" --text 'INLINE_PLAN_BODY' >/dev/null 2>&1
    assert_contains "--text stores inline content literally" \
        "INLINE_PLAN_BODY" endless task show "${tid}" --text

    tid=$(add_task_get_id "Audit text-file")
    endless task update "${tid}" --text-file "${f}" >/dev/null 2>&1
    assert_contains "--text-file loads file contents" \
        "plan body from file" endless task show "${tid}" --text

    # The inversion: --text used to be the path flag; it is now inline, so a
    # path passed to --text is stored as the literal string, NOT loaded.
    tid=$(add_task_get_id "Audit text-inversion")
    endless task update "${tid}" --text "${f}" >/dev/null 2>&1
    assert_contains "--text <path> stores the literal path (inversion)" \
        "${f}" endless task show "${tid}" --text
    assert_not_contains "--text <path> does NOT load the file content" \
        "plan body from file" endless task show "${tid}" --text

    # Both forms together is an error.
    tid=$(add_task_get_id "Audit text-both")
    assert_refused "--text + --text-file together is refused" \
        "not both" endless task update "${tid}" --text x --text-file "${f}"

    # Missing file is an error.
    tid=$(add_task_get_id "Audit text-missing")
    bad="/no/such/e1001/file.md"
    assert_refused "--text-file with a missing path is refused" \
        "File not found" endless task update "${tid}" --text-file "${bad}"
}

# ─── 3: analysis — @file magic removed, --analysis-file replaces it ──────────

test_analysis_magic_removed() {
    section "3 — --analysis @file magic removed; --analysis-file replaces it"

    local tid f
    f=$(mkfile 'analysis loaded from file')

    tid=$(add_task_get_id "Audit analysis-file")
    endless task update "${tid}" --analysis-file "${f}" >/dev/null 2>&1
    assert_contains "--analysis-file loads file contents" \
        "analysis loaded from file" endless task show "${tid}" --analysis

    # The removed E-1329 magic: --analysis @path now stores '@path' literally.
    tid=$(add_task_get_id "Audit analysis-at-literal")
    endless task update "${tid}" --analysis "@${f}" >/dev/null 2>&1
    assert_contains "--analysis @path stores '@path' literally" \
        "@${f}" endless task show "${tid}" --analysis
    assert_not_contains "--analysis @path no longer file-loads" \
        "analysis loaded from file" endless task show "${tid}" --analysis
}

# ─── 4: outcome & description file forms ─────────────────────────────────────

test_outcome_description_file() {
    section "4 — --outcome-file / --description-file load from path"

    local tid f rid

    f=$(mkfile 'outcome loaded from file')
    tid=$(add_task_get_id "Audit outcome-file")
    endless task update "${tid}" --outcome-file "${f}" >/dev/null 2>&1
    assert_contains "'task update --outcome-file' loads file contents" \
        "outcome loaded from file" endless task show "${tid}" --outcome

    f=$(mkfile 'description loaded from file')
    tid=$(add_task_get_id "Audit description-file" --description-file "${f}")
    assert_contains "'task add --description-file' loads file contents" \
        "description loaded from file" endless task show "${tid}"

    # complete --outcome-file (research task — completable via its own gate).
    f=$(mkfile 'findings loaded from file')
    rid=$(add_task_get_id "Research outcome-file" \
        --type research --justification "smoke")
    assert_succeeds "'task complete --outcome-file' succeeds" \
        endless task complete "${rid}" --outcome-file "${f}"
    assert_contains "completed task stores the loaded findings" \
        "findings loaded from file" endless task show "${rid}" --outcome
}

# ─── 5: research handoff points at --outcome-file (the fixed latent bug) ──────

test_research_handoff() {
    section "5 — Research handoff instructs --outcome-file (was broken --outcome <file>)"

    local rid
    rid=$(add_task_get_id "Research handoff-render" \
        --type research --justification "smoke")
    assert_contains "research handoff instructs --outcome-file" \
        "--outcome-file" endless task handoff "${rid}"
    assert_not_contains "research handoff no longer says '--outcome <file>'" \
        "--outcome <file>" endless task handoff "${rid}"
}

# ─── main ───────────────────────────────────────────────────────────────────

main() {
    local repo_root
    repo_root=$(git rev-parse --show-toplevel 2>/dev/null)
    if [[ -z "${repo_root}" ]]; then
        printf 'ERROR: not inside a git worktree\n' >&2
        exit 2
    fi
    cd "${repo_root}" || exit 2

    if ! command -v uv >/dev/null 2>&1; then
        printf 'ERROR: uv not on PATH\n' >&2
        exit 2
    fi

    printf '%sE-1001 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  db:      sandbox\n'
    printf '  python:  %s\n' "$(uv run python --version 2>&1 | tail -1)"

    test_flag_surface
    test_text_inline_file_inversion
    test_analysis_magic_removed
    test_outcome_description_file
    test_research_handoff

    summary
}

main "$@"

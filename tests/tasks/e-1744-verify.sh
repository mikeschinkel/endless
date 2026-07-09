#!/usr/bin/env bash
#
# E-1744 verification script — the inline-content path gate.
#
# Every inline multiline flag (--text/--outcome/--description/--analysis) stores
# its argument verbatim. Passing a file *path* silently stored the path and
# discarded the intended content (the corruption that lost E-1626/E-1564). The
# gate at cli._resolve_content_flag now blocks two shapes:
#   Rule 1 — the whole value IS a path token (absolute OR relative).
#   Rule 2 — the content contains an ABSOLUTE path anywhere.
# Relative tokens mid-content are always allowed; --allow-path <regex> (repeatable)
# is the escape hatch for a genuine absolute path.
#
# Run from anywhere inside the worktree:
#   ./tests/tasks/e-1744-verify.sh
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure. Uses the worktree's sandbox DB via `uv run endless ... --db sandbox`;
# no build required. Each run creates fresh tasks; the sandbox is not wiped
# between runs (pollution is bounded, inspectable via
#   uv run endless task list --db sandbox).
#
# Data repair (Deliverable 2) is NOT exercised here: the only surviving
# corruption was E-1626.text (= "/tmp/e-1626-plan.md"), an obsolete + unrecoverable
# row (no committed mirror, gone /tmp file). It was cleared by hand via
#   endless task update E-1626 --text "" --db main
# — eyeball it with `endless task show E-1626 --text --db main`.

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

# ─── Rule 1: the whole value IS a path token ────────────────────────────────

test_rule1_whole_value_is_path() {
    section "Rule 1 — whole value is a path token (mis-passed file)"

    local tid
    tid=$(add_task_get_id "Gate R1 update-target")

    # Every inline field, via `task update`.
    assert_refused "update --text /abs/plan.md refused → names --text-file" \
        "--text-file" endless task update "${tid}" --text /tmp/plan.md
    assert_refused "update --outcome ./o.md refused → names --outcome-file" \
        "--outcome-file" endless task update "${tid}" --outcome ./o.md
    assert_refused "update --description ~/d.txt refused → names --description-file" \
        "--description-file" endless task update "${tid}" --description '~/d.txt'
    assert_refused "update --analysis a/b/notes.rst refused → names --analysis-file" \
        "--analysis-file" endless task update "${tid}" --analysis a/b/notes.rst

    # A bare filename with no slash is still a path token.
    assert_refused "update --text bare 'plan.md' refused" \
        "received a file path" endless task update "${tid}" --text plan.md

    # A nonexistent path is still blocked (content was lost precisely this way).
    assert_refused "update --text nonexistent /tmp path still refused" \
        "received a file path" \
        endless task update "${tid}" --text /tmp/gone-e1744-xyz.md

    # Same gate on `task add`.
    assert_refused "add --text /abs/plan.md refused" \
        "--text-file" \
        endless task add "Gate R1 add-text" --text /tmp/plan.md
    assert_refused "add --description ./pitch.md refused" \
        "--description-file" \
        endless task add "Gate R1 add-desc" --description ./pitch.md
}

# ─── Rule 2: an absolute path appears anywhere in the content ────────────────

test_rule2_absolute_in_content() {
    section "Rule 2 — absolute path anywhere in content"

    local tid
    tid=$(add_task_get_id "Gate R2 update-target")

    assert_refused "'See /tmp/x.md for detail' refused" \
        "contains an absolute path" \
        endless task update "${tid}" --text "See /tmp/x.md for detail"

    assert_refused "absolute path buried in a long paragraph refused" \
        "contains an absolute path" \
        endless task update "${tid}" --text \
        "The design review is complete and thorough; the full implementation plan is at /Users/x/plan.md and should be read before any work starts here"

    assert_refused "multiline value with an absolute path on any line refused" \
        "contains an absolute path" \
        endless task update "${tid}" --text $'first line is fine\nsecond line points at /tmp/buried.md\nthird line fine'

    assert_refused "absolute ~/ path in content refused" \
        "contains an absolute path" \
        endless task update "${tid}" --text 'the db lives at ~/.config/endless/endless.db normally'
}

# ─── Allowed: must stay green ───────────────────────────────────────────────

test_allowed() {
    section "Allowed — relative paths & plain content stay green"

    local tid real
    tid=$(add_task_get_id "Gate allowed-target")

    # --text-file with a real file is the sanctioned path load (never gated).
    real=$(mktemp)
    printf '# real plan content\n' > "${real}"
    assert_succeeds "--text-file <real file> accepted" \
        endless task update "${tid}" --text-file "${real}"
    rm -f "${real}"

    assert_succeeds "relative source ref mid-content accepted" \
        endless task update "${tid}" --text "edit internal/hookcmd/claude.go then run just build"

    assert_succeeds "relative ./ ref mid-content accepted" \
        endless task update "${tid}" --text "see notes in ./foo.md for the details here"

    assert_succeeds "multiline content with only relative paths accepted" \
        endless task update "${tid}" --text $'edits src/endless/cli.py\nand ./docs/guide/tasks.md too'

    assert_succeeds "plain inline note with no path accepted" \
        endless task update "${tid}" --text "just a plain inline note, nothing pathy"

    # A Git file URL is the recommended cross-project reference — never a
    # mis-passed file path, so it must pass even as a whole value.
    assert_succeeds "whole-value Git file URL accepted" \
        endless task update "${tid}" --text "https://github.com/org/repo/blob/main/x.md"
}

# ─── Escape hatch: --allow-path ─────────────────────────────────────────────

test_escape_hatch() {
    section "Escape hatch — --allow-path <regex> (repeatable)"

    local tid
    tid=$(add_task_get_id "Gate allow-path-target")

    assert_succeeds "matching --allow-path exempts an absolute path" \
        endless task update "${tid}" \
        --text "see /opt/corp/spec.md for the vendor API" \
        --allow-path '^/opt/corp/'

    assert_refused "a second, non-matching absolute path still blocks" \
        "contains an absolute path" \
        endless task update "${tid}" \
        --text "see /opt/corp/spec.md and also /tmp/other.md" \
        --allow-path '^/opt/corp/'

    assert_succeeds "--allow-path is repeatable (two regexes)" \
        endless task update "${tid}" \
        --text "refs /opt/corp/spec.md and /opt/acme/api.md together" \
        --allow-path '^/opt/corp/' --allow-path '^/opt/acme/'
}

# ─── Docs sweep: no path-teaching guidance remains ──────────────────────────

test_docs_sweep() {
    section "Docs sweep — no '--<inline-flag> <path>' guidance remains"

    # An inline flag immediately followed by a path-looking token teaches the
    # corruption. `--<flag>-file <path>` is correct and excluded by the regex
    # (it requires whitespace right after the flag name).
    local pattern='--(text|outcome|description|analysis) +([~./][A-Za-z0-9_/.-]*|[A-Za-z0-9_/.-]+\.(md|txt|rst))'
    local hits
    hits=$(grep -rnE -- "${pattern}" docs/ src/ 2>/dev/null || true)
    if [[ -z "${hits}" ]]; then
        report_pass "no inline-flag-with-path guidance in docs/ or src/"
    else
        report_fail "no inline-flag-with-path guidance in docs/ or src/" \
            "zero matches" "${hits}"
    fi
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

    printf '%sE-1744 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  db:      sandbox\n'
    printf '  python:  %s\n' "$(uv run python --version 2>&1 | tail -1)"

    test_rule1_whole_value_is_path
    test_rule2_absolute_in_content
    test_allowed
    test_escape_hatch
    test_docs_sweep

    summary
}

main "$@"

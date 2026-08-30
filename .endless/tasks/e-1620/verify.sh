#!/usr/bin/env bash
#
# E-1620 verification script — hierarchical task context in bg-agent labels.
#
# Run from anywhere inside the worktree:
#   ./tests/tasks/e-1620-verify.sh
#
# What it proves end-to-end (against the worktree's sandbox DB, which routes
# `endless` to <worktree>/bin/endless-go so the UPDATED embedded handoff
# templates are exercised — NOT the stale global binary):
#
#   1. The handoff's opening identity line carries the hierarchical prefix:
#        - root task / epic (no parent) ........  `- E-<id>: <title>.`
#        - any task WITH a parent ..............  `- E-<parent>/E-<id>: <title>.`
#      The rule keys solely on parent presence (a child of a plain task gets
#      the prefix too, not just children of epics).
#   2. Bare-id references elsewhere in the handoff stay unprefixed.
#   3. The `_hierarchical_label_prefix` helper — the single source for both the
#      template var and the `claude --bg --name` label — returns the right
#      shapes, and the dispatch `--name` label form (`<prefix>: <title>`)
#      composes correctly. (A live `claude --bg` launch is out of scope here;
#      the helper is the load-bearing logic shared by both consumers.)
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on environment error. The sandbox is NOT wiped between runs;
# pollution is bounded and inspectable via
#   uv run endless task list --db sandbox
#
# Reference shape: tests/tasks/e-1577-verify.sh (per E-1596 formalization task).

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

# Wrap the CLI so every invocation routes through the sandbox DB. Under
# --db sandbox in a self-dev worktree, endless execs <worktree>/bin/endless-go,
# so the rendered handoff uses THIS branch's embedded templates.
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
    report_fail "${desc}" "output does NOT contain: ${pattern}" "${output}"
}

# assert_python DESC EXPECTED CODE
#   Pass if `uv run python -c CODE` prints exactly EXPECTED (trailing newline
#   trimmed). Exercises the pure helper that backs both label consumers.
assert_python() {
    local desc="$1"
    local expected="$2"
    local code="$3"
    local output
    output=$(uv run python -c "${code}" 2>&1)
    if [[ "${output}" == "${expected}" ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "${expected}" "${output}"
}

# ─── handoff opening line carries the hierarchical prefix ────────────────────

test_handoff_opening_line() {
    section "Handoff opening line — hierarchical identity prefix"

    local tid eid cid pid ccid

    # Root standalone task → bare id.
    tid=$(add_task_get_id "Implement verify-demo root task")
    assert_contains "standalone task opens with bare id" \
        "- ${tid}: Implement verify-demo root task." \
        endless task handoff "${tid}"

    # Epic is a root → also bare id (the 'epic' shape is mechanically a root).
    eid=$(add_task_get_id "Coordinate verify-demo epic" --type epic)
    assert_contains "epic (root) opens with bare id" \
        "- ${eid}: Coordinate verify-demo epic." \
        endless task handoff "${eid}"

    # Child of an epic → E-<parent>/E-<child>.
    cid=$(add_task_get_id "Render verify-demo epic child" --parent "${eid}")
    assert_contains "child of epic opens with E-<parent>/E-<id>" \
        "- ${eid}/${cid}: Render verify-demo epic child." \
        endless task handoff "${cid}"

    # Child of a PLAIN task → still prefixed. Proves the rule keys on parent
    # presence, not on the parent being an epic.
    pid=$(add_task_get_id "Implement verify-demo plain parent")
    ccid=$(add_task_get_id "Render verify-demo plain-parent child" \
        --parent "${pid}")
    assert_contains "child of plain task ALSO gets parent prefix" \
        "- ${pid}/${ccid}: Render verify-demo plain-parent child." \
        endless task handoff "${ccid}"

    # Other id references in the handoff body stay bare (only line 3 changed).
    assert_contains "bare-id refs elsewhere stay unprefixed" \
        "endless task show ${cid} --text" \
        endless task handoff "${cid}"
    assert_not_contains "no stray prefixed id in the show-plan line" \
        "endless task show ${eid}/${cid}" \
        endless task handoff "${cid}"
}

# ─── label helper backs both --name and the template var ────────────────────

test_label_helper() {
    section "Label helper — source of truth for --name and {{.label_prefix}}"

    assert_python "helper: parented task → E-<parent>/E-<id>" \
        "E-1564/E-1620" \
        "from endless.task_cmd import _hierarchical_label_prefix as f; print(f(1620, 1564))"

    assert_python "helper: root task (parent None) → bare E-<id>" \
        "E-1620" \
        "from endless.task_cmd import _hierarchical_label_prefix as f; print(f(1620, None))"

    # The exact `--name` label form _spawn_bg_dispatch builds:
    #   label = f"{_hierarchical_label_prefix(item_id, parent_id)}: {title}"
    assert_python "dispatch --name label form (child) is '<prefix>: <title>'" \
        "E-1564/E-1620: Render hierarchical labels" \
        "from endless.task_cmd import _hierarchical_label_prefix as f; print(f'{f(1620, 1564)}: Render hierarchical labels')"

    assert_python "dispatch --name label form (root) is '<id>: <title>'" \
        "E-1568: Add --bg dispatch" \
        "from endless.task_cmd import _hierarchical_label_prefix as f; print(f'{f(1568, None)}: Add --bg dispatch')"
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

    printf '%sE-1620 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  db:      sandbox\n'
    printf '  python:  %s\n' "$(uv run python --version 2>&1 | tail -1)"

    test_handoff_opening_line
    test_label_helper

    summary
}

main "$@"

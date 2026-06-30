#!/usr/bin/env bash
#
# E-1689 verification script — the headless `--focal <id>` flag on
# `endless-go session-status` is renamed to `--task <id>`:
#
#   • `session-status --task <id>`   — names the task the view centers on,
#                                       bypasses tmux/session resolution, reads
#                                       the self-detected sandbox DB (E-1685's
#                                       headless entry point, also used by
#                                       E-1684's --tree).
#   • `session-status --focal <id>`  — REMOVED, no alias (unshipped → no
#                                       back-compat).
#
# Run from anywhere inside the worktree:
#   ./tests/tasks/e-1689-verify.sh
#
# The view is exercised headlessly through the worktree's candidate Go binary;
# seeds route to `--db sandbox` so nothing touches the real ledger. NO_COLOR +
# a wide --cols keep output ANSI-free and untruncated so greps are reliable.
# Exit 0 on all-passed, 1 on any failure.

set -u

# ─── globals ────────────────────────────────────────────────────────────────

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

BIN=""   # worktree-built endless-go

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'
    BOLD=$'\033[1m'; RESET=$'\033[0m'
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
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"
    printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "$2"
    printf '      %sgot:%s      %s\n' "${DIM}" "${RESET}" "$3"
    FAIL_COUNT=$((FAIL_COUNT + 1))
    FAILED_TESTS+=("$1")
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

# Python CLI, pinned to the sandbox so seeds never touch the real ledger.
endless() { uv run endless "$@" --db sandbox; }

# The candidate Go renderer in its headless mode.
go_status() { NO_COLOR=1 "${BIN}" session-status --cols 200 "$@"; }

# Prefer a timeout wrapper if one exists, so a regression that hangs the
# renderer on a pipe fails loudly instead of stalling the whole script.
guard() {
    if command -v timeout >/dev/null 2>&1; then timeout 15 "$@"
    elif command -v gtimeout >/dev/null 2>&1; then gtimeout 15 "$@"
    else "$@"
    fi
}

# Create a task and emit just its E-NNN id on stdout (other output → stderr).
add_task_get_id() {
    local title="$1"; shift
    local output rc
    output=$(endless task add "${title}" "$@" 2>&1); rc=$?
    if [[ "${rc}" -ne 0 ]]; then
        printf 'ERROR: add failed for %q: %s\n' "${title}" "${output}" >&2
        return 1
    fi
    printf '%s\n' "${output}" | grep -oE 'E-[0-9]+' | head -1
}

num_id() { printf '%s' "${1#E-}"; }

# ─── generic assertions ─────────────────────────────────────────────────────

# assert_fails DESC CMD...  — pass when the command exits non-zero.
assert_fails() {
    local desc="$1"; shift
    local output rc
    output=$(guard "$@" 2>&1); rc=$?
    if [[ "${rc}" -ne 0 ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "non-zero exit" "rc=0; output: ${output}"
}

# assert_has DESC NEEDLE CMD...  — run CMD, pass when combined output contains NEEDLE.
assert_has() {
    local desc="$1" needle="$2"; shift 2
    local output
    output=$(guard "$@" 2>&1)
    if [[ "${output}" == *"${needle}"* ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "output contains: ${needle}" "${output}"
}

# assert_lacks DESC NEEDLE CMD...  — run CMD, pass when output does NOT contain NEEDLE.
assert_lacks() {
    local desc="$1" needle="$2"; shift 2
    local output
    output=$(guard "$@" 2>&1)
    if [[ "${output}" != *"${needle}"* ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "output does NOT contain: ${needle}" "${output}"
}

# ─── 1. --task renders the headless snapshot view for a seeded task ──────────

test_task_flag_renders_view() {
    section "--task — headless snapshot view for a seeded task"

    local f d out
    f=$(add_task_get_id "Build e1689 task-flag focal") || return
    d=$(add_task_get_id "Build e1689 task-flag dependent") || return
    endless task block "${d}" --by "${f}" >/dev/null 2>&1

    out=$(go_status --task "$(num_id "${f}")" 2>&1)

    if [[ "${out}" == *"● this"* ]]; then
        report_pass "legend header rendered"
    else
        report_fail "legend header rendered" "the '● this …' legend line" "${out}"
    fi
    if printf '%s\n' "${out}" | grep -Eq "E-$(num_id "${f}") "; then
        report_pass "task ${f} appears as a row under --task"
    else
        report_fail "task ${f} appears as a row under --task" "a row for ${f}" "${out}"
    fi
    if printf '%s\n' "${out}" | grep -Eq "E-$(num_id "${d}") "; then
        report_pass "dependent ${d} appears (cross-row view still works post-rename)"
    else
        report_fail "dependent ${d} appears" "a row for ${d}" "${out}"
    fi
}

# ─── 2. --task drives --tree (E-1684's consumer of the flag) ─────────────────

test_task_flag_drives_tree() {
    section "--task — still names the focal task for the --tree spine"

    local f
    f=$(add_task_get_id "Build e1689 task-flag tree") || return

    # The tree's spine root is the focal task (the --task value), marked with *.
    # assert_* run through `guard` (timeout), which can only exec a real binary —
    # so call ${BIN} directly here, not the go_status shell function.
    assert_has "tree marks the --task focal with * (*E-$(num_id "${f}"))" \
        "*E-$(num_id "${f}")" "${BIN}" session-status --tree --task "$(num_id "${f}")"
}

# ─── 3. --focal is gone — renamed away with no alias ─────────────────────────

test_focal_flag_removed() {
    section "--focal — removed, no alias"

    assert_fails "session-status --focal exits non-zero" \
        "${BIN}" session-status --focal 99
    assert_has "session-status --focal reports the flag is undefined" \
        "flag provided but not defined: -focal" "${BIN}" session-status --focal 99
}

# ─── 4. help surfaces the renamed flag, not the old name ─────────────────────

test_help_text() {
    section "--help — lists --task, no trace of --focal"

    assert_has "usage lists the -task flag" \
        "-task" "${BIN}" session-status --help
    assert_has "usage describes it as an explicit task id" \
        "explicit task id" "${BIN}" session-status --help
    assert_lacks "usage no longer mentions focal" \
        "focal" "${BIN}" session-status --help
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
    BIN="${repo_root}/bin/endless-go"
    if [[ ! -x "${BIN}" ]]; then
        printf 'ERROR: %s not built — run `just build` first\n' "${BIN}" >&2
        exit 2
    fi

    printf '%sE-1689 verification — rename session-status --focal flag to --task%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  db:      sandbox\n'
    printf '  go bin:  %s\n' "${BIN}"

    test_task_flag_renders_view
    test_task_flag_drives_tree
    test_focal_flag_removed
    test_help_text

    summary
}

main "$@"

#!/usr/bin/env bash
#
# E-1688 verification script — `session next` is split into an accurately-named
# pair and the colliding E-1312 verb is freed:
#
#   • `endless session status`   — one-shot snapshot view (rename of `session next`)
#   • `endless session monitor`  — the live, looping top-like dashboard
#   • `endless session next`     — REMOVED (Go subcommand + Python verb)
#   • `endless session snapshot` — E-1312's status-snapshot recorder, renamed off
#                                   `session status` so the live view can own it
#
# Run from anywhere inside the worktree:
#   ./tests/tasks/e-1688-verify.sh
#
# The snapshot VIEW is exercised headlessly through the worktree's candidate Go
# binary (`./bin/endless-go session-status --task <id>`), which names the focal
# task directly, bypasses tmux/session resolution, and reads the self-detected
# per-worktree sandbox DB instead of pinning main (E-1685's headless entry point).
# Surface checks (verb present/absent, help text) go through the Python CLI.
# Seeds route to `--db sandbox`; exit 0 on all-passed, 1 on any failure.

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

# The candidate Go renderer in its headless mode. NO_COLOR + a wide --cols keep
# the output ANSI-free and untruncated so row/text greps are reliable.
go_status() { NO_COLOR=1 "${BIN}" session-status --cols 200 "$@"; }

# Prefer a timeout wrapper if one exists, so a regression that makes --monitor
# loop on a pipe fails loudly instead of hanging the whole script.
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

# ─── 1. session status: headless snapshot view for a seeded focal task ──────

test_status_snapshot_view() {
    section "session status — headless snapshot view for a seeded focal task"

    local f d out
    f=$(add_task_get_id "Build e1688 status focal") || return
    d=$(add_task_get_id "Build e1688 status dependent") || return
    endless task block "${d}" --by "${f}" >/dev/null 2>&1

    out=$(go_status --task "$(num_id "${f}")" 2>&1)

    if [[ "${out}" == *"● this"* ]]; then
        report_pass "legend header rendered"
    else
        report_fail "legend header rendered" "the '● this …' legend line" "${out}"
    fi
    if printf '%s\n' "${out}" | grep -Eq "E-$(num_id "${f}") "; then
        report_pass "focal task ${f} appears as a row"
    else
        report_fail "focal task ${f} appears as a row" "a row for ${f}" "${out}"
    fi
    if printf '%s\n' "${out}" | grep -Eq "E-$(num_id "${d}") "; then
        report_pass "dependent task ${d} appears as a row (cross-row view)"
    else
        report_fail "dependent ${d} appears as a row" "a row for ${d}" "${out}"
    fi
}

# ─── 2. session next: removed (Go subcommand + Python verb) ──────────────────

test_session_next_removed() {
    section "session next — verb removed, no alias"

    assert_fails "endless-go session-next subcommand is gone" \
        "${BIN}" session-next
    assert_has "endless-go reports it as an unknown subcommand" \
        "unknown subcommand" "${BIN}" session-next
    assert_fails "python 'endless session next' verb is gone" \
        uv run endless session next --help
}

# ─── 3. session monitor: exists, renders one frame without blocking ──────────

test_session_monitor() {
    section "session monitor — exists and renders one frame without blocking"

    assert_has "python 'session monitor' verb exists" \
        "Live dashboard" uv run endless session monitor --help

    # --monitor against a pipe (non-tty) MUST degrade to a single frame and
    # exit, never loop. Seed a focal task and confirm one frame is produced.
    local f out
    f=$(add_task_get_id "Build e1688 monitor focal") || return
    out=$(go_status --monitor --task "$(num_id "${f}")" 2>&1)
    if printf '%s\n' "${out}" | grep -Eq "E-$(num_id "${f}") "; then
        report_pass "session-status --monitor renders the focal frame once and exits"
    else
        report_fail "session-status --monitor renders one frame" \
            "a single frame containing ${f}" "${out}"
    fi
}

# ─── 4. status now names the live view; recorder moved to session snapshot ───

test_status_vs_snapshot_split() {
    section "session status = live view; recorder moved to 'session snapshot' (E-1312)"

    assert_has "'session status' help describes the live snapshot view" \
        "one-shot snapshot" uv run endless session status --help
    assert_lacks "'session status' is no longer the E-1312 recorder group" \
        "Record and query" uv run endless session status --help
    assert_fails "'session status add' no longer exists" \
        uv run endless session status add
    assert_has "'session snapshot' is the E-1312 recorder" \
        "Record and query session status snapshots" \
        uv run endless session snapshot --help
    assert_has "'session snapshot add' exists" \
        "add" uv run endless session snapshot --help
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

    printf '%sE-1688 verification — rename session next → status + add monitor%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  db:      sandbox\n'
    printf '  go bin:  %s\n' "${BIN}"

    test_status_snapshot_view
    test_session_next_removed
    test_session_monitor
    test_status_vs_snapshot_split

    summary
}

main "$@"

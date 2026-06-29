#!/usr/bin/env bash
#
# E-1685 verification script — exercises the focal task's direct dependents
# (what it unblocks) in `session next`, end-to-end against the worktree's
# sandbox DB.
#
# Run from anywhere inside the worktree:
#   ./tests/tasks/e-1685-verify.sh
#
# It seeds focal/dependent tasks via the Python CLI (`endless ... --db sandbox`)
# and reads them back through the worktree's candidate Go binary in its headless
# mode (`./bin/endless-go session-next --focal <id>`), which bypasses tmux/
# session resolution and reads the same self-detected sandbox DB instead of
# pinning main. Output: pass/fail per check, then a summary. Exit 0 on
# all-passed, 1 on any failure. Each run creates fresh task IDs; the sandbox is
# not wiped between runs (pollution is bounded and inspectable via
#   uv run endless task list --db sandbox).

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

# Glyphs the renderer uses for the block column (internal/sessionnextcmd).
BLOCKED_GLYPH="⊗"

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

# Wrap the Python CLI so every seed/mutation routes through the sandbox DB.
endless() {
    uv run endless "$@" --db sandbox
}

# Render the session-next view for an explicit focal task through the worktree's
# candidate Go binary, headless. NO_COLOR + a wide --cols keep the output ANSI-
# free and untruncated so the row/glyph greps are reliable.
go_session_next() {
    NO_COLOR=1 ./bin/endless-go session-next --cols 200 "$@"
}

# Create a task and emit just its E-NNN id on stdout (other output → stderr).
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

# numeric id from an "E-NNN" token (the --focal flag wants a bare integer).
num_id() { printf '%s' "${1#E-}"; }

# The row line for task E-ID within captured session-next OUTPUT, or "" if
# absent. The id is left-justified to 6 cols then followed by a space, so a
# trailing space disambiguates E-2 from E-20.
row_for() {
    local id="$1" output="$2"
    printf '%s\n' "${output}" | grep -E "E-${id} " | head -1
}

# ─── assertions (operate on already-captured session-next output) ───────────

assert_row_present() {
    local desc="$1" id="$2" output="$3"
    if [[ -n "$(row_for "${id}" "${output}")" ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "a row for E-${id}" "no E-${id} row in:\n${output}"
}

assert_row_absent() {
    local desc="$1" id="$2" output="$3"
    if [[ -z "$(row_for "${id}" "${output}")" ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "no row for E-${id}" "$(row_for "${id}" "${output}")"
}

assert_row_has_glyph() {
    local desc="$1" id="$2" glyph="$3" output="$4"
    local row
    row=$(row_for "${id}" "${output}")
    if [[ -n "${row}" ]] && [[ "${row}" == *"${glyph}"* ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "E-${id} row contains '${glyph}'" "${row:-<row absent>}"
}

assert_row_lacks_glyph() {
    local desc="$1" id="$2" glyph="$3" output="$4"
    local row
    row=$(row_for "${id}" "${output}")
    if [[ -n "${row}" ]] && [[ "${row}" != *"${glyph}"* ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "E-${id} row present WITHOUT '${glyph}'" "${row:-<row absent>}"
}

# ─── scenario 1+2: open dependent shown with ⊗ while focal is open ───────────

test_open_dependent_shown_blocked() {
    section "Dependent shown with ⊗ while focal is open (shown even while blocked)"

    local f d out
    f=$(add_task_get_id "Build e1685 focal-open")
    d=$(add_task_get_id "Build e1685 dependent-open")
    endless task block "${d}" --by "${f}" >/dev/null 2>&1

    out=$(go_session_next --focal "$(num_id "${f}")")

    assert_row_present "dependent ${d} appears as a row in focal ${f}'s view" \
        "$(num_id "${d}")" "${out}"
    assert_row_has_glyph "dependent ${d} carries ⊗ while focal ${f} is open" \
        "$(num_id "${d}")" "${BLOCKED_GLYPH}" "${out}"
}

# ─── scenario 3: ⊗ clears once the focal lands ──────────────────────────────

test_block_clears_when_focal_lands() {
    section "Dependent's ⊗ clears after the focal reaches a terminal state"

    local f d out
    f=$(add_task_get_id "Build e1685 focal-lands")
    d=$(add_task_get_id "Build e1685 dependent-unblocks")
    endless task block "${d}" --by "${f}" >/dev/null 2>&1

    # Land the focal — confirmed unblocks dependents.
    endless task confirm "${f}" >/dev/null 2>&1

    out=$(go_session_next --focal "$(num_id "${f}")")

    assert_row_present "dependent ${d} still shows after focal ${f} lands" \
        "$(num_id "${d}")" "${out}"
    assert_row_lacks_glyph "dependent ${d} shows actionable (no ⊗) once ${f} is confirmed" \
        "$(num_id "${d}")" "${BLOCKED_GLYPH}" "${out}"
}

# ─── scenario 4: terminal dependent omitted by default, shown under --all ───

test_terminal_dependent_all_gate() {
    section "Terminal dependent omitted by default, included under --all"

    local f d out_default out_all
    f=$(add_task_get_id "Build e1685 focal-terminaldep")
    d=$(add_task_get_id "Build e1685 dependent-terminal")
    endless task block "${d}" --by "${f}" >/dev/null 2>&1
    # Make the dependent itself terminal while the focal stays open.
    endless task confirm "${d}" >/dev/null 2>&1

    out_default=$(go_session_next --focal "$(num_id "${f}")")
    out_all=$(go_session_next --focal "$(num_id "${f}")" --all)

    assert_row_absent "terminal dependent ${d} omitted by default" \
        "$(num_id "${d}")" "${out_default}"
    assert_row_present "terminal dependent ${d} surfaced under --all" \
        "$(num_id "${d}")" "${out_all}"
}

# ─── scenario 5: session next does NOT write session_tasks (read-time only) ──

test_no_session_tasks_write() {
    section "session next does NOT write session_tasks (read-time dependents)"

    local f d before after
    f=$(add_task_get_id "Build e1685 focal-noprojwrite")
    d=$(add_task_get_id "Build e1685 dependent-noprojwrite")
    endless task block "${d}" --by "${f}" >/dev/null 2>&1

    before=$(sqlite3 "${SANDBOX_DB}" "SELECT count(*) FROM session_tasks;" 2>&1)
    go_session_next --focal "$(num_id "${f}")" >/dev/null 2>&1
    after=$(sqlite3 "${SANDBOX_DB}" "SELECT count(*) FROM session_tasks;" 2>&1)

    if [[ "${before}" =~ ^[0-9]+$ ]] && [[ "${after}" == "${before}" ]]; then
        report_pass "session_tasks row count unchanged (${before} → ${after})"
    else
        report_fail "session_tasks row count unchanged after session next" \
            "after == before (numeric)" "before=${before} after=${after}"
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
    if [[ ! -x ./bin/endless-go ]]; then
        printf 'ERROR: ./bin/endless-go not built — run `just build` first\n' >&2
        exit 2
    fi

    # Deterministic sandbox DB path for this worktree (basename matches the
    # sandbox dir basename, E-1281). Verified to exist before any sqlite3 read.
    SANDBOX_DB="${HOME}/.cache/endless/sandboxes/$(basename "${repo_root}")/endless/endless.db"

    printf '%sE-1685 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  db:      sandbox\n'
    printf '  go bin:  ./bin/endless-go\n'
    printf '  python:  %s\n' "$(uv run python --version 2>&1 | tail -1)"

    test_open_dependent_shown_blocked
    test_block_clears_when_focal_lands
    test_terminal_dependent_all_gate

    if [[ -f "${SANDBOX_DB}" ]]; then
        test_no_session_tasks_write
    else
        section "session next does NOT write session_tasks (read-time dependents)"
        report_fail "locate sandbox DB for session_tasks count" \
            "a readable file at the per-worktree sandbox path" \
            "missing: ${SANDBOX_DB}"
    fi

    summary
}

main "$@"

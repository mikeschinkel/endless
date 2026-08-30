#!/usr/bin/env bash
#
# E-1750 verification — the session-status legend is DYNAMIC: it lists only the
# glyphs for actions/decorations actually present in the current frame, with no
# `|` group divider, and the old ⁇ catch-all is split into ⏚ landed + ⁇ unknown.
#
# Run from anywhere inside the worktree:
#   esu && ./tests/tasks/e-1750-verify.sh
#
# Two check groups, both folded in:
#   1. Hermetic legend logic — `go test ./internal/sessionstatuscmd/...`. This is
#      the sole coverage of ◆ dirty (a real divergent worktree can't be seeded
#      here) and it independently pins ⁇ unknown, ⏚ landed, and the no-`|` rule.
#   2. End-to-end binary path — seed the per-worktree sandbox DB and drive
#      `./bin/endless-go session-status --task <id> --cols <n>` headlessly (no
#      tmux), asserting the rendered legend for representative row sets.
#
# Output: pass/fail per check, then a summary. Exit 0 all-pass / 1 any-fail /
# 2 setup. Each run creates fresh task IDs; the sandbox is not wiped between runs
# (pollution is bounded, inspect with `uv run endless task list --db sandbox`).

set -u

# ─── globals ────────────────────────────────────────────────────────────────

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

COLS=100
BIN=./bin/endless-go

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
    local desc="$1" expected="$2" actual="$3"
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

# ─── seeding helpers ────────────────────────────────────────────────────────

# Route every CLI invocation through the per-worktree sandbox DB.
endless() {
    uv run endless "$@" --db sandbox
}

# Create a task and emit just its E-NNN id on stdout; diagnostics to stderr.
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

# Render one headless frame for a focal task id (numeric or E-NNN).
render() {
    local focal="${1#E-}"
    NO_COLOR=1 "${BIN}" session-status --task "${focal}" --cols "${COLS}"
}

# The legend is always the first rendered line.
legend_of() {
    render "$1" | head -1
}

# ─── assertions ─────────────────────────────────────────────────────────────

# assert_legend_has DESC FOCAL PATTERN
assert_legend_has() {
    local desc="$1" focal="$2" pattern="$3" legend
    legend=$(legend_of "${focal}")
    if [[ "${legend}" == *"${pattern}"* ]]; then
        report_pass "${desc}"
    else
        report_fail "${desc}" "legend contains: ${pattern}" "${legend}"
    fi
}

# assert_legend_lacks DESC FOCAL PATTERN
assert_legend_lacks() {
    local desc="$1" focal="$2" pattern="$3" legend
    legend=$(legend_of "${focal}")
    if [[ "${legend}" != *"${pattern}"* ]]; then
        report_pass "${desc}"
    else
        report_fail "${desc}" "legend does NOT contain: ${pattern}" "${legend}"
    fi
}

# assert_row_glyph DESC FOCAL ROWID GLYPH — a rendered row for E-ROWID starts
# with GLYPH (the action/decoration icon).
assert_row_glyph() {
    local desc="$1" focal="$2" rowid="${3#E-}" glyph="$4" line
    line=$(render "${focal}" | grep -E "E-${rowid}( |$)" | head -1)
    if [[ "${line}" == *"${glyph}"* ]]; then
        report_pass "${desc}"
    else
        report_fail "${desc}" "row E-${rowid} renders ${glyph}" "${line:-<no row>}"
    fi
}

# assert_no_pipe DESC FOCAL — the legend carries no `|` group divider.
assert_no_pipe() {
    local desc="$1" focal="$2" legend
    legend=$(legend_of "${focal}")
    if [[ "${legend}" != *"|"* ]]; then
        report_pass "${desc}"
    else
        report_fail "${desc}" "legend has no '|'" "${legend}"
    fi
}

# assert_parity DESC FOCAL — one `session status` frame is byte-identical to one
# `session monitor` frame for the same rows. Captured SEQUENTIALLY (concurrent
# opens would contend on the sandbox's SQLite lock).
assert_parity() {
    local desc="$1" focal="${2#E-}" a b
    a=$(NO_COLOR=1 "${BIN}" session-status --task "${focal}" --cols "${COLS}" 2>&1)
    b=$(NO_COLOR=1 "${BIN}" session-status --task "${focal}" --monitor --cols "${COLS}" 2>&1)
    if [[ "${a}" == "${b}" ]]; then
        report_pass "${desc}"
    else
        report_fail "${desc}" "status frame == monitor frame" \
            "$(diff <(printf '%s' "${a}") <(printf '%s' "${b}"))"
    fi
}

# ─── check group 1: hermetic legend logic ───────────────────────────────────

test_go_unit() {
    section "Legend logic — go test ./internal/sessionstatuscmd/..."
    local output rc
    output=$(go test ./internal/sessionstatuscmd/... 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then
        report_pass "package tests pass (TestBuildLegend covers ◆ dirty, ⁇ unknown, ⏚ landed, no-|)"
    else
        report_fail "package tests" "go test exits 0" "${output}"
    fi
}

# ─── check group 2: end-to-end binary path ──────────────────────────────────

test_do_plan_only() {
    section "do/plan-only view → just those glyphs"
    local f
    f=$(add_task_get_id "Implement e1750 do/plan focal") || return
    add_task_get_id "Implement e1750 do child"   --parent "${f}" --status ready     >/dev/null || return
    add_task_get_id "Implement e1750 plan child" --parent "${f}" --status unplanned >/dev/null || return

    assert_legend_has   "legend lists ▶ do"        "${f}" "▶ do"
    assert_legend_has   "legend lists ✎ plan"      "${f}" "✎ plan"
    assert_legend_lacks "no ◷ orphan when absent"  "${f}" "orphan"
    assert_legend_lacks "no ☑ verify when absent"  "${f}" "verify"
    assert_legend_lacks "no ⏚ landed when absent"  "${f}" "landed"
    assert_legend_lacks "no ⁇ unknown when absent" "${f}" "unknown"
    assert_legend_lacks "no ⊗ blocked when absent" "${f}" "blocked"
    assert_legend_lacks "no ⏸ blocks when absent"  "${f}" "blocks"
    assert_legend_lacks "no ◆ dirty when absent"   "${f}" "dirty"
    assert_no_pipe      "legend has no | divider"  "${f}"
}

test_done() {
    section "terminal row → ✓ done"
    # A confirmed focal still renders (is_focal), carrying the phase-column ✓.
    local f
    f=$(add_task_get_id "Implement e1750 done focal") || return
    if ! endless task confirm "${f}" >/dev/null 2>&1; then
        report_fail "confirm focal for done marker" "task confirm exits 0" "see: endless task confirm ${f} --db sandbox"
        return
    fi
    assert_legend_has "legend lists ✓ done"    "${f}" "✓ done"
    assert_row_glyph  "done row renders ✓"      "${f}" "${f}" "✓"
    assert_no_pipe    "legend has no | divider" "${f}"
}

test_landed() {
    section "landed row → ⏚ landed"
    local f c sha
    sha=$(git rev-parse HEAD)
    f=$(add_task_get_id "Implement e1750 landed focal") || return
    c=$(add_task_get_id "Implement e1750 landed child" --parent "${f}" --status ready) || return
    if ! endless worktree land "${c}" --record-only --sha "${sha}" >/dev/null 2>&1; then
        report_fail "record landing for ${c}" "worktree land --record-only exits 0" "see: endless worktree land ${c} --record-only --sha ${sha} --db sandbox"
        return
    fi
    assert_legend_has "legend lists ⏚ landed" "${f}" "⏚ landed"
    assert_row_glyph  "landed row renders ⏚"  "${f}" "${c}" "⏚"
    assert_no_pipe    "legend has no | divider" "${f}"
}

test_unknown() {
    section "unrecognized status → ⁇ unknown"
    local f c
    f=$(add_task_get_id "Implement e1750 unknown focal") || return
    c=$(add_task_get_id "Implement e1750 unknown child" --parent "${f}" --status ready) || return
    # `blocked` is the one settable, non-terminal status classify() does not map to
    # a verb, so it drives the should-never-happen ⁇ unknown safety net.
    if ! endless task update "${c}" --status blocked >/dev/null 2>&1; then
        report_fail "set synthetic unknown status" "task update --status blocked exits 0" "(covered hermetically by TestBuildLegend if the CLI guard changes)"
        return
    fi
    assert_legend_has "legend lists ⁇ unknown" "${f}" "⁇ unknown"
    assert_row_glyph  "unknown row renders ⁇"  "${f}" "${c}" "⁇"
    assert_no_pipe    "legend has no | divider" "${f}"
}

test_blocked() {
    section "blocked row → ⊗ blocked (and absent when none)"
    local f blk c
    f=$(add_task_get_id "Implement e1750 blocked focal") || return
    blk=$(add_task_get_id "Implement e1750 open blocker" --status ready) || return
    c=$(add_task_get_id "Implement e1750 blocked child" --parent "${f}" --status ready --blocked-by "${blk}") || return
    assert_legend_has   "legend lists ⊗ blocked"    "${f}" "⊗ blocked"
    assert_legend_lacks "no ⏸ blocks in this view"  "${f}" "blocks"
    assert_row_glyph    "blocked row renders ⊗"     "${f}" "${c}" "⊗"
    assert_no_pipe      "legend has no | divider"   "${f}"
}

test_blocks() {
    section "blocking row → ⏸ blocks (and absent when none)"
    local f other c
    f=$(add_task_get_id "Implement e1750 blocks focal") || return
    other=$(add_task_get_id "Implement e1750 blocks target" --status ready) || return
    c=$(add_task_get_id "Implement e1750 blocking child" --parent "${f}" --status ready --blocks "${other}") || return
    assert_legend_has   "legend lists ⏸ blocks"     "${f}" "⏸ blocks"
    assert_legend_lacks "no ⊗ blocked in this view" "${f}" "blocked"
    assert_row_glyph    "blocking row renders ⏸"    "${f}" "${c}" "⏸"
    assert_no_pipe      "legend has no | divider"   "${f}"
}

test_parity() {
    section "parity — status frame == monitor frame"
    # A rich focal exercising this/do/blocked/blocks so parity covers a full line.
    local f other c
    f=$(add_task_get_id "Implement e1750 parity focal") || return
    other=$(add_task_get_id "Implement e1750 parity target" --status ready) || return
    c=$(add_task_get_id "Implement e1750 parity child" --parent "${f}" --status ready --blocks "${other}") || return
    assert_parity "one status frame is byte-identical to one monitor frame" "${f}"
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
    if ! command -v go >/dev/null 2>&1; then
        printf 'ERROR: go not on PATH\n' >&2
        exit 2
    fi
    if [[ ! -x "${BIN}" ]]; then
        printf 'ERROR: %s missing — run `just build` first\n' "${BIN}" >&2
        exit 2
    fi

    printf '%sE-1750 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  db:      sandbox\n'
    printf '  binary:  %s\n' "${BIN}"

    test_go_unit
    test_do_plan_only
    test_done
    test_landed
    test_unknown
    test_blocked
    test_blocks
    test_parity

    summary
}

main "$@"

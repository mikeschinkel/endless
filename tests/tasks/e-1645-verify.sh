#!/usr/bin/env bash
#
# E-1645 verification script — confirms `endless task spawn --reopen` does the
# right thing: a liveness guard that navigates to a live owner instead of
# double-spawning, an inherited-session resolver that skips sub-10s ghosts, a
# predicted restore_case (reused vs rebuilt-off-main), and a read-only
# `--print-decision` seam.
#
# It drives the REAL code on three layers:
#   1. Go resolver — `go test` over internal/events.ResolveReopenContext (the
#      ghost-skip pick is asserted exactly, in a temp DB).
#   2. Go binary — `endless-go session-query reopen-context` against a seeded
#      sandbox DB (the JSON shape Python consumes).
#   3. Python CLI — the worktree's `endless task spawn --reopen --print-decision`
#      (run with PYTHONPATH shadowing so the WORKTREE source is exercised, not
#      the global editable install) against the self-dev sandbox DB.
#
# The actual foreground `tmux switch-client` side-effect is NOT exercised here
# (it would yank the running client) — only the *decision* to navigate is
# asserted. Verify the real switch manually: seed a live tmux session for a task
# and run `endless task spawn E-<id> --reopen` without --print-decision.
#
# Re-runnable: every task/session id is freshly allocated per run (never a fixed
# UNIQUE value); the sandbox is NOT wiped between runs. Seeding errors abort
# loudly with exit 2 (no /dev/null swallowing).
#
# Run from inside the worktree, in a tmux session (esu provides one):
#   esu && ./tests/tasks/e-1645-verify.sh
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on environment/setup error.

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

EVENTS_PKG="./internal/events/"

# Resolved in main().
REPO_ROOT=""
ENDLESS_GO=""
DBP=""          # sandbox endless.db path
CFG=""          # dir containing the sandbox endless.db (for --config-dir)
PROJ_ID=""      # a project id in the sandbox
BG_KIND=""      # session_kinds.id for slug 'background'
CLEANUP_DIRS=() # canonical worktree dirs to rmdir on exit

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
    local detail="$2"
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "${desc}"
    printf '      %sdetail:%s %s\n' "${DIM}" "${RESET}" "${detail}"
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

cleanup() {
    local d
    for d in "${CLEANUP_DIRS[@]}"; do
        [[ -d "${d}" ]] && rmdir "${d}" 2>/dev/null
    done
}

# ─── helpers ────────────────────────────────────────────────────────────────

# run_endless: the WORKTREE's Python endless. PYTHONPATH shadows the global
# editable install so the candidate source is exercised; --db sandbox routes to
# this worktree's throwaway DB.
run_endless() {
    PYTHONPATH="${REPO_ROOT}/src" endless "$@"
}

# die LOUDLY on a setup/seed failure (exit 2). Never swallow seed output.
setup_die() {
    printf '%sSETUP ERROR:%s %s\n' "${RED}${BOLD}" "${RESET}" "$1" >&2
    exit 2
}

# seed_task TITLE -> echoes a freshly-allocated task id (integer). Aborts on
# failure. Titles must be verb-first (the title-verb gate).
seed_task() {
    local title="$1" out id
    out=$(run_endless task add "${title}" --db sandbox 2>&1)
    id=$(printf '%s' "${out}" | grep -oE 'E-[0-9]+' | head -1 | sed 's/E-//')
    if [[ -z "${id}" ]]; then
        setup_die "task add failed for '${title}': ${out}"
    fi
    printf '%s' "${id}"
}

# seed_assumed TASKID — flip a task to a terminal status so --reopen is valid.
seed_assumed() {
    local tid="$1" out
    out=$(run_endless task update "E-${tid}" --status assumed \
        --outcome "e-1645 verify seed" --db sandbox 2>&1)
    [[ $? -eq 0 ]] || setup_die "task update assumed failed for E-${tid}: ${out}"
}

# seed_ended_session TASKID STARTED LAST_ACTIVITY -> echoes the seeded sessions.id.
# A NULL transcript means evidence comes only from the >=10s span.
seed_ended_session() {
    local tid="$1" started="$2" last="$3" sname="e1645v-ended-${1}-$3" out
    out=$(sqlite3 "${DBP}" \
        "INSERT INTO sessions (session_id, project_id, state, active_task_id, started_at, last_activity) \
         VALUES ('${sname}', ${PROJ_ID}, 'ended', ${tid}, '${started}', '${last}');" 2>&1) \
        || setup_die "seed ended session for E-${tid}: ${out}"
    local sid
    sid=$(sqlite3 "${DBP}" "SELECT id FROM sessions WHERE session_id='${sname}';" 2>&1) \
        || setup_die "read seeded ended session id: ${sid}"
    printf '%s' "${sid}"
}

# seed_working_session TASKID KIND_ID PROCESS — insert a live (working) session
# bound to the task. PROCESS empty for a background agent, '%NNN' for a tmux pane.
seed_working_session() {
    local tid="$1" kind="$2" process="$3" sname="e1645v-work-${1}-${3:-bg}" out
    local proc_sql="NULL"
    [[ -n "${process}" ]] && proc_sql="'${process}'"
    out=$(sqlite3 "${DBP}" \
        "INSERT INTO sessions (session_id, project_id, state, active_task_id, kind_id, process, started_at) \
         VALUES ('${sname}', ${PROJ_ID}, 'working', ${tid}, ${kind}, ${proc_sql}, '2026-06-25T00:00:00');" 2>&1) \
        || setup_die "seed working session for E-${tid}: ${out}"
}

# assert_contains DESC NEEDLE HAYSTACK
assert_contains() {
    local desc="$1" needle="$2" hay="$3"
    if printf '%s' "${hay}" | grep -qF -- "${needle}"; then
        report_pass "${desc}"
    else
        report_fail "${desc}" "expected to contain '${needle}'; got: $(printf '%s' "${hay}" | tr '\n' '⏎')"
    fi
}

# assert_cmd_fails DESC NEEDLE -- CMD...
#   Pass iff CMD exits non-zero AND its output contains NEEDLE.
assert_cmd_fails() {
    local desc="$1" needle="$2"; shift 2
    local out rc
    out=$("$@" 2>&1); rc=$?
    if [[ "${rc}" -ne 0 ]] && printf '%s' "${out}" | grep -qF -- "${needle}"; then
        report_pass "${desc}"
    else
        report_fail "${desc}" "rc=${rc}, expected non-zero + '${needle}'; got: $(printf '%s' "${out}" | tr '\n' '⏎')"
    fi
}

# ─── checks ─────────────────────────────────────────────────────────────────

test_go_resolver() {
    section "Resolver (Go) — skips sub-10s ghosts, honors transcript evidence"
    local out rc
    out=$(go test -count=1 -run '^TestReopenContext' "${EVENTS_PKG}" 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then
        report_pass "internal/events ResolveReopenContext tests pass"
    else
        report_fail "internal/events ResolveReopenContext tests pass" \
            "$(printf '%s' "${out}" | tail -4 | tr '\n' '⏎')"
    fi
}

test_binary_reopen_context() {
    section "Resolver (binary) — reopen-context returns the real session in JSON"
    local tid sid out got
    tid=$(seed_task "Verify reopen-context binary resolver")
    sid=$(seed_ended_session "${tid}" "2026-06-23T00:00:00" "2026-06-23T00:00:30")
    out=$("${ENDLESS_GO}" --config-dir "${CFG}" session-query reopen-context \
        --task-id "${tid}" 2>&1)
    got=$(printf '%s' "${out}" | grep -oE '"inherited_session_id":[0-9]+' \
        | grep -oE '[0-9]+$')
    if [[ "${got}" == "${sid}" ]]; then
        report_pass "reopen-context inherited_session_id=${sid} (the real ended session)"
    else
        report_fail "reopen-context inherited_session_id=${sid}" \
            "got inherited_session_id=${got:-<none>}; full: $(printf '%s' "${out}" | tr '\n' '⏎')"
    fi
}

test_flag_guards() {
    section "CLI guards — --new-session / --print-decision require --reopen"
    local tid
    tid=$(seed_task "Verify reopen flag guards")
    assert_cmd_fails "--new-session without --reopen is refused" \
        "only applies with --reopen" \
        run_endless task spawn "E-${tid}" --new-session --db sandbox
    assert_cmd_fails "--print-decision without --reopen is refused" \
        "only applies with --reopen" \
        run_endless task spawn "E-${tid}" --print-decision --db sandbox
}

test_restore_case() {
    section "Decision — restore_case predicted from worktree presence"

    local tid out
    tid=$(seed_task "Verify rebuilt-off-main restore case")
    seed_assumed "${tid}"
    out=$(run_endless task spawn "E-${tid}" --reopen --print-decision --db sandbox 2>&1)
    assert_contains "reaped worktree (absent) → restore_case=rebuilt-off-main" \
        "restore_case=rebuilt-off-main" "${out}"

    # Present worktree dir → reused. Create the canonical path, assert, clean up.
    local tid2 wt out2
    tid2=$(seed_task "Verify reused restore case")
    seed_assumed "${tid2}"
    wt="${REPO_ROOT%/.endless/worktrees/*}/.endless/worktrees/e-${tid2}"
    mkdir -p "${wt}" || setup_die "mkdir ${wt}"
    CLEANUP_DIRS+=("${wt}")
    out2=$(run_endless task spawn "E-${tid2}" --reopen --print-decision --db sandbox 2>&1)
    assert_contains "present worktree → restore_case=reused" \
        "restore_case=reused" "${out2}"
    rmdir "${wt}" 2>/dev/null
}

test_session_mode() {
    section "Decision — inherit vs --new-session"

    local tid sid out
    tid=$(seed_task "Verify inherit-session decision")
    seed_assumed "${tid}"
    sid=$(seed_ended_session "${tid}" "2026-06-23T00:00:00" "2026-06-23T00:00:30")
    out=$(run_endless task spawn "E-${tid}" --reopen --print-decision --db sandbox 2>&1)
    assert_contains "prior ended session → session: inherit-session=${sid}" \
        "inherit-session=${sid}" "${out}"

    local tid2 out2
    tid2=$(seed_task "Verify new-session decision")
    seed_assumed "${tid2}"
    seed_ended_session "${tid2}" "2026-06-23T00:00:00" "2026-06-23T00:00:30" >/dev/null
    out2=$(run_endless task spawn "E-${tid2}" --reopen --new-session --print-decision --db sandbox 2>&1)
    assert_contains "--new-session → session: new-session (ignores prior)" \
        "new-session" "${out2}"
}

test_liveness_guard() {
    section "Liveness guard — navigate to a live owner instead of spawning"

    local tid out
    tid=$(seed_task "Verify background liveness navigate")
    seed_working_session "${tid}" "${BG_KIND}" ""
    out=$(run_endless task spawn "E-${tid}" --reopen --print-decision --db sandbox 2>&1)
    assert_contains "live background owner → navigate (background) / attach" \
        "navigate (background)" "${out}"

    # Foreground: a live tmux-pane session → navigate (foreground). Only the
    # DECISION is asserted; the real switch-client is the manual check above.
    local tid2 out2
    tid2=$(seed_task "Verify foreground liveness navigate decision")
    seed_working_session "${tid2}" "1" "%999"
    out2=$(run_endless task spawn "E-${tid2}" --reopen --print-decision --db sandbox 2>&1)
    assert_contains "live foreground owner → navigate (foreground) / switch-client" \
        "navigate (foreground)" "${out2}"
}

# ─── main ───────────────────────────────────────────────────────────────────

main() {
    REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null)
    if [[ -z "${REPO_ROOT}" ]]; then
        printf 'ERROR: not inside a git worktree\n' >&2
        exit 2
    fi
    cd "${REPO_ROOT}" || exit 2

    if ! command -v go >/dev/null 2>&1; then
        printf 'ERROR: go not on PATH\n' >&2
        exit 2
    fi
    if ! command -v sqlite3 >/dev/null 2>&1; then
        printf 'ERROR: sqlite3 not on PATH (needed to seed sessions)\n' >&2
        exit 2
    fi
    if ! command -v endless >/dev/null 2>&1; then
        printf 'ERROR: endless (Python CLI) not on PATH\n' >&2
        exit 2
    fi
    if [[ -z "${TMUX:-}" ]]; then
        printf 'ERROR: not in a tmux session — the spawn decision path requires it. Run via: esu && ./tests/tasks/e-1645-verify.sh\n' >&2
        exit 2
    fi

    # go.work points at the local go-pkgs/ modules; regenerate on demand.
    if [[ ! -f "${REPO_ROOT}/go.work" ]]; then
        command -v just >/dev/null 2>&1 && just go-work-init >/dev/null 2>&1
        if [[ ! -f "${REPO_ROOT}/go.work" ]]; then
            printf 'ERROR: go.work missing and could not be generated (run: just go-work-init)\n' >&2
            exit 2
        fi
    fi

    # Exercise the worktree-built endless-go; build on demand.
    ENDLESS_GO="${REPO_ROOT}/bin/endless-go"
    if [[ ! -x "${ENDLESS_GO}" ]]; then
        command -v just >/dev/null 2>&1 && just go >/dev/null 2>&1
        if [[ ! -x "${ENDLESS_GO}" ]]; then
            printf 'ERROR: bin/endless-go missing and could not be built (run: just go)\n' >&2
            exit 2
        fi
    fi

    DBP=$(run_endless db path --db sandbox 2>/dev/null)
    [[ -n "${DBP}" ]] || setup_die "could not resolve the sandbox DB path (endless db path --db sandbox)"
    # Materialize the schema if the sandbox DB is fresh (any endless-go open applies it).
    "${ENDLESS_GO}" --config-dir "$(dirname "${DBP}")" session-query reopen-context --task-id 1 >/dev/null 2>&1
    [[ -f "${DBP}" ]] || setup_die "sandbox DB does not exist at ${DBP}"
    CFG=$(dirname "${DBP}")
    PROJ_ID=$(sqlite3 "${DBP}" "SELECT id FROM projects ORDER BY id LIMIT 1;" 2>&1) \
        || setup_die "read project id: ${PROJ_ID}"
    [[ -n "${PROJ_ID}" ]] || setup_die "no project registered in the sandbox DB"
    BG_KIND=$(sqlite3 "${DBP}" "SELECT id FROM session_kinds WHERE slug='background';" 2>&1) \
        || setup_die "read background session_kind id: ${BG_KIND}"
    [[ -n "${BG_KIND}" ]] || setup_die "session_kinds has no 'background' row"

    trap cleanup EXIT

    printf '%sE-1645 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:        %s\n' "${REPO_ROOT}"
    printf '  endless-go: %s\n' "${ENDLESS_GO}"
    printf '  sandbox db: %s\n' "${DBP}"
    printf '  go:         %s\n' "$(go version 2>&1 | awk '{print $3}')"

    test_go_resolver
    test_binary_reopen_context
    test_flag_guards
    test_restore_case
    test_session_mode
    test_liveness_guard

    summary
}

main "$@"

#!/usr/bin/env bash
#
# E-1465 verification script — exercises `endless session next` end-to-end
# against an ISOLATED, synthetic database.
#
# Run from anywhere inside the worktree:
#   ./tests/tasks/e-1465-verify.sh
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on setup error.
#
# Why a temp HOME instead of --db sandbox: the `session-next` subcommand pins
# the MAIN database (sessions live in main regardless of cwd), so --db sandbox
# would be ignored. PinMainDB resolves $HOME/.config/endless/endless.db, so we
# point HOME at a throwaway dir, seed a deterministic fixture there, and run the
# WORKTREE-built binary against it. cwd is moved out of the worktree per run so
# the self-dev sandbox auto-detect doesn't fire. Nothing touches the real ledger.
#
# Focal resolution: TMUX/TMUX_PANE are unset for each run, so the resolver falls
# through to its global "most-recently-active live session" branch — making the
# seeded focal session deterministic without a live tmux window. The parent (↑)
# decoration needs a real tmux @endless_spawned_by marker, so it is covered by
# the Go unit test TestSessionNextRows_RowSetAndDecorations, not here.
#
# Ad-hoc prototype in the shape referenced by E-1596 (the formalization epic).

set -u

# ─── globals ────────────────────────────────────────────────────────────────

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

REPO_ROOT=""
BIN=""
SCHEMA=""
FIXTURE_HOME=""   # temp HOME with a seeded, populated DB
EMPTY_HOME=""     # temp HOME with a schema-only (no sessions) DB

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

# ─── isolated runner ──────────────────────────────────────────────────────────

# gosn HOME_DIR [session-next args...]
#   Run the worktree's endless-go session-next against HOME_DIR's DB, with tmux
#   env stripped (forces the global focal fallback) and cwd outside the worktree
#   (disables sandbox auto-detect). Width is fixed at 90 for stable truncation.
gosn() {
    local home_dir="$1"
    shift
    ( cd "${home_dir}" \
        && env -u TMUX -u TMUX_PANE -u XDG_CONFIG_HOME -u ENDLESS_SESSION_ID \
            HOME="${home_dir}" \
            "${BIN}" session-next --cols 90 "$@" )
}

# ─── assertions ─────────────────────────────────────────────────────────────

# assert_line_matches DESC REGEX HOME_DIR [args...]
#   Pass if any output line matches the extended regex.
assert_line_matches() {
    local desc="$1" regex="$2"; shift 2
    local output
    output=$(gosn "$@" 2>&1)
    if printf '%s\n' "${output}" | grep -Eq -- "${regex}"; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "a line matching /${regex}/" "${output}"
}

# assert_contains DESC PATTERN HOME_DIR [args...]
assert_contains() {
    local desc="$1" pattern="$2"; shift 2
    local output
    output=$(gosn "$@" 2>&1)
    if [[ "${output}" == *"${pattern}"* ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "output contains: ${pattern}" "${output}"
}

# assert_not_contains DESC PATTERN HOME_DIR [args...]
assert_not_contains() {
    local desc="$1" pattern="$2"; shift 2
    local output
    output=$(gosn "$@" 2>&1)
    if [[ "${output}" != *"${pattern}"* ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "output does NOT contain: ${pattern}" "${output}"
}

# assert_ordering DESC FIRST SECOND HOME_DIR [args...]
#   Pass if both markers appear AND SECOND appears after FIRST.
assert_ordering() {
    local desc="$1" first="$2" second="$3"; shift 3
    local output first_pos second_pos
    output=$(gosn "$@" 2>&1)
    first_pos=$(printf '%s\n' "${output}" | grep -n -F -- "${first}" | head -1 | cut -d: -f1)
    second_pos=$(printf '%s\n' "${output}" | grep -n -F -- "${second}" | head -1 | cut -d: -f1)
    if [[ -n "${first_pos}" && -n "${second_pos}" && "${second_pos}" -gt "${first_pos}" ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "'${first}' before '${second}'" \
        "first=${first_pos:-MISSING} second=${second_pos:-MISSING} | ${output}"
}

# ─── fixtures ─────────────────────────────────────────────────────────────────

# seed_fixture builds a populated DB at FIXTURE_HOME exercising every action
# icon the script can drive deterministically:
#
#   100 focal  underway/now  ●  (blocked by 107 open + blocks 101 → ⊗⏸, bw=2)
#   101 do     ready/next    ▶  (ready with NO plan text → still do, ED-1522;
#                                 blocked by open focal → ⊗)
#   102 plan   unplanned/now ✎
#   104 plan   revisit/now   ✎  (revisit folds into plan)
#   103 verify unverified    ☑
#   106 doing  underway/now  ⟳  (a SECOND live session is active on it)
#   107 orphan underway/now  ◷  (no live session on it; blocks 100 → ⏸)
#   105 done   confirmed/now  ·  (terminal: hidden by default, shown with --all)
seed_fixture() {
    local db="$1"
    sqlite3 "${db}" < "${SCHEMA}" >/dev/null
    sqlite3 "${db}" <<'SQL'
INSERT INTO projects (id,name,path) VALUES (1,'p1','/p1');
INSERT INTO tasks (id,project_id,title,status,phase,text) VALUES
 (100,1,'focal task','underway','now',NULL),
 (101,1,'do task','ready','next',NULL),
 (102,1,'plan task','unplanned','now',NULL),
 (104,1,'revisit task','revisit','now',NULL),
 (103,1,'verify task','unverified','now',NULL),
 (106,1,'inflight task','underway','now',NULL),
 (107,1,'orphan blocker','underway','now',NULL),
 (105,1,'done task','confirmed','now',NULL);
-- s1 is the focal session (newest activity → wins the global focal fallback);
-- s2 is a second live session that makes task 106 in-flight.
INSERT INTO sessions (id,session_id,project_id,platform,state,active_task_id,kind_id,started_at,last_activity) VALUES
 (1,NULL,1,'claude','working',100,1,'2026-06-20T00:00:00','2026-06-20T10:00:00'),
 (2,NULL,1,'claude','working',106,1,'2026-06-20T00:00:00','2026-06-20T09:00:00');
-- s1 has touched every candidate task, so they all enter the row set.
INSERT INTO session_tasks (session_id,task_id,created_at,updated_at) VALUES
 (1,100,'t','t'),(1,101,'t','t'),(1,102,'t','t'),(1,103,'t','t'),
 (1,104,'t','t'),(1,105,'t','t'),(1,106,'t','t'),(1,107,'t','t');
INSERT INTO task_deps (source_type,source_id,target_type,target_id,dep_type) VALUES
 ('task',107,'task',100,'blocks'),
 ('task',100,'task',101,'blocks');
SQL
}

# seed_empty builds a schema-only DB (no sessions) at EMPTY_HOME so focal
# resolution yields nothing and the empty-state line is exercised.
seed_empty() {
    local db="$1"
    sqlite3 "${db}" < "${SCHEMA}" >/dev/null
    sqlite3 "${db}" "INSERT INTO projects (id,name,path) VALUES (1,'p1','/p1');"
}

# ─── tests ─────────────────────────────────────────────────────────────────

test_action_icons() {
    section "Action icons — one row per action, status canonicalized"
    assert_line_matches "● this    (focal, now=1, blocked+blocks ⊗⏸)" \
        '^● T E-100 +1 +⊗⏸' "${FIXTURE_HOME}"
    assert_line_matches "⟳ doing   (second live session on the task)" \
        '^⟳ T E-106 ' "${FIXTURE_HOME}"
    assert_line_matches "▶ do      (ready with NO plan → still do; next=2; blocked ⊗)" \
        '^▶ T E-101 +2 +⊗' "${FIXTURE_HOME}"
    assert_line_matches "✎ plan    (unplanned)" \
        '^✎ T E-102 ' "${FIXTURE_HOME}"
    assert_line_matches "✎ plan    (revisit folds into plan)" \
        '^✎ T E-104 ' "${FIXTURE_HOME}"
    assert_line_matches "☑ verify  (unverified canonicalizes to verify)" \
        '^☑ T E-103 ' "${FIXTURE_HOME}"
    assert_line_matches "◷ orphan  (underway, no live session; blocks ⏸)" \
        '^◷ T E-107 ' "${FIXTURE_HOME}"
}

test_legend() {
    section "Header legend"
    assert_contains "legend line present and unabbreviated" \
        "● this  ↑ parent  ⟳ doing  ▶ do  ✎ plan  ◷ orphan  ☑ verify | ⊗ blocked  ⏸ blocks" \
        "${FIXTURE_HOME}"
}

test_done_work_filter() {
    section "Done-work filter — terminal rows hidden unless --all"
    assert_not_contains "confirmed task hidden by default" \
        "E-105" "${FIXTURE_HOME}"
    assert_contains "confirmed task shown with --all" \
        "E-105" "${FIXTURE_HOME}" --all
    assert_line_matches "done row uses ✓ phase char and · other icon under --all" \
        '^· T E-105 +✓' "${FIXTURE_HOME}" --all
}

test_sort_order() {
    section "Sort order — action rank, then phase, then id"
    assert_ordering "this before doing"  "E-100" "E-106" "${FIXTURE_HOME}"
    assert_ordering "doing before do"    "E-106" "E-101" "${FIXTURE_HOME}"
    assert_ordering "do before plan"     "E-101" "E-102" "${FIXTURE_HOME}"
    assert_ordering "plan before verify" "E-102" "E-103" "${FIXTURE_HOME}"
    assert_ordering "verify before orphan" "E-103" "E-107" "${FIXTURE_HOME}"
}

test_empty_state() {
    section "Empty state — no focal task resolves"
    assert_contains "prints the no-active-task hint" \
        "no active task" "${EMPTY_HOME}"
    assert_contains "legend still printed when empty" \
        "● this" "${EMPTY_HOME}"
}

# ─── main ─────────────────────────────────────────────────────────────────────

cleanup() {
    [[ -n "${FIXTURE_HOME}" ]] && rm -rf "${FIXTURE_HOME}"
    [[ -n "${EMPTY_HOME}" ]] && rm -rf "${EMPTY_HOME}"
}

main() {
    REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null)
    if [[ -z "${REPO_ROOT}" ]]; then
        printf 'ERROR: not inside a git worktree\n' >&2
        exit 2
    fi
    BIN="${REPO_ROOT}/bin/endless-go"
    SCHEMA="${REPO_ROOT}/internal/schema/schema.sql"

    if [[ ! -x "${BIN}" ]]; then
        printf 'ERROR: %s missing. Run: just build\n' "${BIN}" >&2
        exit 2
    fi
    if [[ ! -f "${SCHEMA}" ]]; then
        printf 'ERROR: schema not found at %s\n' "${SCHEMA}" >&2
        exit 2
    fi
    if ! command -v sqlite3 >/dev/null 2>&1; then
        printf 'ERROR: sqlite3 not on PATH\n' >&2
        exit 2
    fi

    trap cleanup EXIT
    FIXTURE_HOME=$(mktemp -d)
    EMPTY_HOME=$(mktemp -d)
    mkdir -p "${FIXTURE_HOME}/.config/endless" "${EMPTY_HOME}/.config/endless"
    seed_fixture "${FIXTURE_HOME}/.config/endless/endless.db"
    seed_empty "${EMPTY_HOME}/.config/endless/endless.db"

    printf '%sE-1465 verification — endless session next%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:    %s\n' "${REPO_ROOT}"
    printf '  binary: %s\n' "${BIN}"
    printf '  db:     isolated temp HOME (no real-ledger access)\n'

    test_legend
    test_action_icons
    test_done_work_filter
    test_sort_order
    test_empty_state

    summary
}

main "$@"

#!/usr/bin/env bash
#
# E-1684 verification suite — the SINGLE entry point for verifying E-1684.
#
#   esu
#   ./tests/tasks/e-1684-verify.sh
#
# Self-contained: it builds the worktree binaries, runs the Go unit tests for
# the tree-layering logic, then drives the real worktree-built endless-go binary
# end-to-end against a throwaway temp DB built from the shipped
# internal/schema/schema.sql. Nothing in the sandbox or real ledger is touched.
# Exit 0 on all-passed, 1 on any failure (with detail to diagnose).
#
# What it proves:
#   1. Worktree builds clean.
#   2. Tree layering + rendering (DAG depth, parallel siblings, diamond,
#      do_order override, IDs-only) — internal/sessionnextcmd unit tests.
#   3. `session-next --tree --focal E-NNN` renders the do/plan backlog as an
#      IDs-only implementation-order tree derived from the blocked-by DAG:
#        - a dependency chain A→B→C renders A before B before C (by nesting),
#        - tasks at equal depth with no inter-dependency render as siblings,
#        - an independent task renders as a separate flush-left root,
#        - output is IDs-only (no legend / titles / icons),
#        - a per-session do_order (E-1683) overrides the DAG order.

set -u

# ─── locate worktree ────────────────────────────────────────────────────────

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
WT_ROOT=$(cd "${SCRIPT_DIR}/../.." && pwd)
cd "${WT_ROOT}" || { printf 'ERROR: cannot cd to %s\n' "${WT_ROOT}" >&2; exit 1; }
BIN="${WT_ROOT}/bin/endless-go"
SCHEMA="${WT_ROOT}/internal/schema/schema.sql"

# ─── output ─────────────────────────────────────────────────────────────────

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi
UNDERLINE="──────────────────────────────────────────────────────────────"

section() { printf '\n%s%s%s\n%s\n' "${BOLD}" "$1" "${RESET}" "${UNDERLINE}"; }
report_pass() { printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"; PASS_COUNT=$((PASS_COUNT + 1)); }
report_fail() {
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"
    printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "$2"
    printf '      %sgot:%s      %s\n' "${DIM}" "${RESET}" "$3"
    FAIL_COUNT=$((FAIL_COUNT + 1)); FAILED_TESTS+=("$1")
}

assert_eq() {
    local desc="$1" want="$2" got="$3"
    if [[ "${got}" == "${want}" ]]; then report_pass "${desc}"; else report_fail "${desc}" "${want}" "${got}"; fi
}
# assert_cmd DESC CMD [ARGS...] — pass if CMD exits 0; on failure show the last
# few lines of its output so a regression is diagnosable from this report alone.
assert_cmd() {
    local desc="$1"; shift
    local out rc
    out=$("$@" 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then
        report_pass "${desc}"
    else
        report_fail "${desc}" "exit 0" "exit=${rc} | $(printf '%s\n' "${out}" | tail -8 | tr '\n' '⏎')"
    fi
}
# assert_contains DESC PATTERN CMD [ARGS...] — pass if CMD output contains PATTERN.
assert_contains() {
    local desc="$1" pattern="$2"; shift 2
    local out
    out=$("$@" 2>&1)
    if [[ "${out}" == *"${pattern}"* ]]; then report_pass "${desc}"; else report_fail "${desc}" "output ~ ${pattern}" "${out}"; fi
}
# assert_not_contains DESC PATTERN CMD [ARGS...] — pass if CMD output lacks PATTERN.
assert_not_contains() {
    local desc="$1" pattern="$2"; shift 2
    local out
    out=$("$@" 2>&1)
    if [[ "${out}" != *"${pattern}"* ]]; then report_pass "${desc}"; else report_fail "${desc}" "output !~ ${pattern}" "${out}"; fi
}

summary() {
    printf '\n%sSummary%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    if [[ "${FAIL_COUNT}" -eq 0 ]]; then
        printf '  %s%d passed%s\n\n  %sALL PASSED%s\n\n' "${GREEN}" "${PASS_COUNT}" "${RESET}" "${GREEN}${BOLD}" "${RESET}"
        return 0
    fi
    printf '  %s%d passed%s, %s%d failed%s\n\n  %sFAILED:%s\n' \
        "${GREEN}" "${PASS_COUNT}" "${RESET}" "${RED}" "${FAIL_COUNT}" "${RESET}" "${RED}${BOLD}" "${RESET}"
    for t in "${FAILED_TESTS[@]}"; do printf '    - %s\n' "${t}"; done
    printf '\n'
    return 1
}

# ─── temp environment ───────────────────────────────────────────────────────

command -v sqlite3 >/dev/null || { printf 'ERROR: sqlite3 required to seed the test DB\n' >&2; exit 1; }

TMP=$(mktemp -d)
trap 'rm -rf "${TMP}"' EXIT
CFG="${TMP}/config"
mkdir -p "${CFG}"
DB="${CFG}/endless.db"

# tree CMD — run session-next --tree against the temp DB for focal task 99.
# --config-dir pins the DB path. The headless --focal flag (E-1685) names the
# focal task directly AND skips PinMainDB, so session-next reads the resolved
# --config-dir context (this seeded DB) instead of main. The focal task (99) is
# deliberately SEPARATE from the do/plan backlog (100..103): the focal task is
# the session's CURRENT work and is excluded from the tree, exactly as in real
# usage. --focal takes a bare integer id.
tree() { "${BIN}" --config-dir "${CFG}" session-next --tree --focal 99; }

# seed_base builds a fresh DB with a project, a working session (id 42) whose
# active_task_id is the focal task E-99, and four do-status (ready) backlog tasks
# E-100..103 (all in session_tasks alongside the focal). Callers add task_deps
# (blockers) and/or do_order on the backlog after.
seed_base() {
    rm -f "${DB}"
    sqlite3 "${DB}" < "${SCHEMA}" >/dev/null
    sqlite3 "${DB}" "
        INSERT INTO projects (id, name, path, status, created_at, updated_at)
          VALUES (1, 'p', '${TMP}', 'active', '2026-06-29T00:00:00', '2026-06-29T00:00:00');
        INSERT INTO tasks (id, project_id, title, status, phase, created_at, updated_at) VALUES
          (99,  1, 'focal-title',   'underway', 'now', '2026-06-29T00:00:00', '2026-06-29T00:00:00'),
          (100, 1, 'alpha-title',   'ready',    'now', '2026-06-29T00:00:00', '2026-06-29T00:00:00'),
          (101, 1, 'bravo-title',   'ready',    'now', '2026-06-29T00:00:00', '2026-06-29T00:00:00'),
          (102, 1, 'charlie-title', 'ready',    'now', '2026-06-29T00:00:00', '2026-06-29T00:00:00'),
          (103, 1, 'delta-title',   'ready',    'now', '2026-06-29T00:00:00', '2026-06-29T00:00:00');
        INSERT INTO sessions (id, session_id, state, project_id, active_task_id) VALUES
          (42, 'verify-1684', 'working', 1, 99);
        INSERT INTO session_tasks (session_id, task_id, created_at, updated_at) VALUES
          (42, 99,  '2026-06-29T00:00:00', '2026-06-29T00:00:00'),
          (42, 100, '2026-06-29T00:00:00', '2026-06-29T00:00:00'),
          (42, 101, '2026-06-29T00:00:00', '2026-06-29T00:00:00'),
          (42, 102, '2026-06-29T00:00:00', '2026-06-29T00:00:00'),
          (42, 103, '2026-06-29T00:00:00', '2026-06-29T00:00:00');
    "
}

# add_block SRC TGT — SRC blocks TGT (source blocks target, dep_type='blocks').
add_block() {
    sqlite3 "${DB}" "INSERT INTO task_deps (source_type, source_id, target_type, target_id, dep_type)
                     VALUES ('task', $1, 'task', $2, 'blocks');"
}

# ─── checks ─────────────────────────────────────────────────────────────────

section "Build — worktree binaries (just build)"
if ! build_out=$(just build 2>&1); then
    report_fail "just build" "exit 0" "$(printf '%s\n' "${build_out}" | tail -8 | tr '\n' '⏎')"
    summary
    exit 1
fi
report_pass "just build"

section "Go unit tests — tree layering + rendering"
assert_cmd "go test internal/sessionnextcmd (TestBuildForest + TestParseFocalID)" \
    go test ./internal/sessionnextcmd/ -count=1 -run 'TestBuildForest|TestParseFocalID'

# Every tree is an ancestry spine: the focal task (E-99, headless --focal, no
# parent here) is the root marked `*`, and the do/plan backlog (E-100..103)
# nests under it in implementation order.

section "Ancestry spine — focal marked with *, backlog nested under it"
# Only E-100 blocks E-101; E-102 and E-103 are independent. Backlog roots
# (E-100, E-102, E-103) all nest under the focal *E-99.
seed_base
add_block 100 101
SPINE=$'*E-99\n├── E-100\n│   └── E-101\n├── E-102\n└── E-103'
assert_eq "focal is the spine root with backlog nested under it" "${SPINE}" "$(tree)"
assert_contains "focal renders as *E-99" "*E-99" tree
assert_contains "backlog nests under focal (├── E-100)" "├── E-100" tree

section "DAG chain — A→B→C, independent D (nesting = implementation order)"
# E-100 blocks E-101 blocks E-102; E-103 independent. Under *E-99: the chain
# root E-100 and the independent root E-103 are siblings.
seed_base
add_block 100 101
add_block 101 102
CHAIN=$'*E-99\n├── E-100\n│   └── E-101\n│       └── E-102\n└── E-103'
assert_eq "chain + independent root nested under focal, IDs-only" "${CHAIN}" "$(tree)"
# Order is encoded by nesting: B under A, C under B → A before B before C.
assert_contains "B nests under A (└── E-101)" "└── E-101" tree
assert_contains "C nests deepest under B (└── E-102)" "└── E-102" tree

section "Parallel group — equal-depth tasks with no inter-dependency are siblings"
# E-100 blocks E-101 AND E-102 (both depth 1, parallel); E-101 blocks E-103.
seed_base
add_block 100 101
add_block 100 102
add_block 101 103
PAR=$'*E-99\n└── E-100\n    ├── E-101\n    │   └── E-103\n    └── E-102'
assert_eq "equal-depth E-101/E-102 render as siblings under E-100" "${PAR}" "$(tree)"
assert_contains "E-101 is a branch sibling (├── E-101)" "├── E-101" tree
assert_contains "E-102 is the last sibling (└── E-102)" "└── E-102" tree

section "IDs-only — no legend, titles, or block icons"
seed_base
add_block 100 101
assert_not_contains "no legend 'this' word" "this" tree
assert_not_contains "no legend 'plan' word" "plan" tree
assert_not_contains "no block glyph ⊗" "⊗" tree
assert_not_contains "no task titles leak (alpha-title)" "alpha-title" tree
assert_not_contains "no task titles leak (focal-title)" "focal-title" tree
# The only non-tree glyphs allowed are the box-drawing connectors and the
# focal `*` marker.
assert_not_contains "no do/ready icon ▶" "▶" tree

section "do_order override — E-1683 per-session order beats the DAG"
# DAG would put E-100 first (it blocks E-101); a do_order making E-103 the
# backlog root, then E-101|E-102 parallel, then E-100, must override that —
# all still nested under the focal *E-99.
seed_base
add_block 100 101
sqlite3 "${DB}" "
    UPDATE session_tasks SET do_order=1 WHERE session_id=42 AND task_id=103;
    UPDATE session_tasks SET do_order=2 WHERE session_id=42 AND task_id=101;
    UPDATE session_tasks SET do_order=2 WHERE session_id=42 AND task_id=102;
    UPDATE session_tasks SET do_order=3 WHERE session_id=42 AND task_id=100;
"
OVR=$'*E-99\n└── E-103\n    ├── E-101\n    │   └── E-100\n    └── E-102'
assert_eq "do_order layers override the blocked-by DAG" "${OVR}" "$(tree)"

summary

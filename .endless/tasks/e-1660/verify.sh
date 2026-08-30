#!/usr/bin/env bash
#
# E-1660 verification script — confirms `just land`'s record-landing step no
# longer trips the task_types integrity gate when a branch ADDS a mirrored-enum
# value (e.g. a new task_types row).
#
# Root cause recap: the land sequence applies the schema change (DB gains the
# new task_types row) using the WORKTREE binary, then `endless worktree land`
# emits the task.landed event. Under --db main that emit resolves endless-go via
# shutil.which — the GLOBAL binary — which `just build` has not yet refreshed, so
# its tasktype enum lacks the new constant. monitor.DB() runs
# tasktype.VerifyIntegrity on every open and fails closed, so record-landing
# fails and forces a manual `just install && just land` recovery.
#
# Fix: the `land` recipe now prepends the worktree's bin to PATH for the
# `endless worktree land` call (exactly as the apply-change loop already does),
# so the record-landing emit uses the freshly-built worktree binary whose enum
# matches the rows apply-change just inserted.
#
# This script verifies the mechanism WITHOUT a destructive real land:
#   1. Gate-still-armed (don't weaken it): an event emit against a DB carrying a
#      task_types row whose id has NO matching enum constant MUST fail with the
#      integrity error. This is the exact failure the bug produced.
#   2. Matching-enum-passes: the same emit against a DB whose task_types rows all
#      match the binary's enum MUST get PAST the integrity gate (it may fail
#      later on an unrelated FK; that is fine — the gate is what we assert on).
#      This is the positive case the fix delivers: the worktree binary's enum
#      always matches the rows apply-change inserted from the same branch.
#   3. Fix-present: the `land` recipe PATH-prepends the worktree bin to the
#      `endless worktree land` call (structural guard against regression).
#
# Run from anywhere inside the worktree:
#   esu && ./tests/tasks/e-1660-verify.sh
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

# A task_types id that no Go enum constant will ever match — stands in for "a
# new mirrored-enum row the stale binary doesn't know about yet".
PHANTOM_ID=99
PHANTOM_SLUG="phantom"

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

REPO_ROOT=""
WT_BIN=""
LEDGER_REPO=""

# Build a throwaway git repo to serve as --project-root so the event's ledger
# auto-commit (which refuses to land on a linked worktree, E-1309) succeeds and
# the emit proceeds to the DB projection where VerifyIntegrity runs.
make_ledger_repo() {
    LEDGER_REPO=$(mktemp -d)
    git -C "${LEDGER_REPO}" init -q
    git -C "${LEDGER_REPO}" config user.email verify@e1660
    git -C "${LEDGER_REPO}" config user.name e1660-verify
    git -C "${LEDGER_REPO}" commit -q --allow-empty -m init
    mkdir -p "${LEDGER_REPO}/.endless"
}

# seed_db <config-dir> [inject-phantom] — create a fresh schema-seeded DB at
# <config-dir>/endless/endless.db; if a second arg is given, inject a
# task_types row with no matching enum constant.
seed_db() {
    local cfg="$1"
    local inject="${2:-}"
    mkdir -p "${cfg}/endless"
    sqlite3 "${cfg}/endless/endless.db" < "${REPO_ROOT}/internal/schema/schema.sql" >/dev/null 2>&1
    if [[ -n "${inject}" ]]; then
        sqlite3 "${cfg}/endless/endless.db" \
            "INSERT INTO task_types (id,slug,label) VALUES (${PHANTOM_ID},'${PHANTOM_SLUG}','Phantom');"
    fi
}

# emit <config-dir> — run the same event the record-landing step writes, against
# the DB under <config-dir>, using the WORKTREE binary. Echoes combined output.
emit() {
    local cfg="$1"
    "${WT_BIN}" event emit --kind task.landed --project endless \
        --entity-type task --entity-id 1657 \
        --project-root "${LEDGER_REPO}" --config-dir "${cfg}/endless" \
        --actor-kind system --actor-id "verify@e1660" --node-id "abcd" \
        --payload '{"branch":"e-1660-verify","merge_commit_sha":"deadbeef"}' 2>&1
}

# ─── 1. gate still armed (don't weaken it) ──────────────────────────────────

test_gate_armed() {
    section "Integrity gate — a DB row with no matching enum constant is REFUSED"

    local cfg
    cfg=$(mktemp -d)
    seed_db "${cfg}" inject
    local out
    out=$(emit "${cfg}")
    local desc="record-landing emit fails closed on a drifted task_types row"
    if [[ "${out}" == *"integrity check"* && "${out}" == *"no matching enum constant"* ]]; then
        report_pass "${desc}"
    else
        report_fail "${desc}" \
            "output contains 'integrity check' AND 'no matching enum constant'" \
            "${out}"
    fi
}

# ─── 2. matching enum passes the gate ───────────────────────────────────────

test_matching_passes() {
    section "Matching enum — record-landing emit gets PAST the integrity gate"

    local cfg
    cfg=$(mktemp -d)
    seed_db "${cfg}"   # no phantom row; all task_types match the binary's enum
    local out
    out=$(emit "${cfg}")
    # The worktree binary's enum matches the schema-seeded rows, so the gate
    # passes. The emit may still fail downstream on an unrelated FK (synthetic
    # DB has no task id=1657) — that is expected and NOT an integrity failure.
    local desc="emit clears the integrity gate when the binary enum matches the DB"
    if [[ "${out}" != *"integrity check"* ]]; then
        report_pass "${desc}"
    else
        report_fail "${desc}" \
            "output does NOT contain 'integrity check'" \
            "${out}"
    fi
}

# ─── 3. the fix is present in the land recipe ───────────────────────────────

test_fix_present() {
    section "Justfile — the land recipe runs record-landing with the worktree binary"

    local recipe
    recipe=$(just --show land 2>/dev/null)

    local desc="land prepends the worktree bin to PATH for 'endless worktree land'"
    if [[ "${recipe}" == *'PATH="$wt/bin:$PATH" endless worktree land'* ]]; then
        report_pass "${desc}"
    else
        report_fail "${desc}" \
            'land recipe contains: PATH="$wt/bin:$PATH" endless worktree land' \
            "$(printf '%s' "${recipe}" | grep -n 'worktree land' || echo '<no match>')"
    fi
}

# ─── main ───────────────────────────────────────────────────────────────────

cleanup() {
    [[ -n "${LEDGER_REPO}" && -d "${LEDGER_REPO}" ]] && rm -rf "${LEDGER_REPO}"
}

main() {
    REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null)
    if [[ -z "${REPO_ROOT}" ]]; then
        printf 'ERROR: not inside a git worktree\n' >&2
        exit 2
    fi
    cd "${REPO_ROOT}" || exit 2

    local missing=0
    command -v sqlite3 >/dev/null 2>&1 || { printf 'ERROR: sqlite3 not on PATH\n' >&2; missing=1; }
    command -v just >/dev/null 2>&1 || { printf 'ERROR: just not on PATH\n' >&2; missing=1; }
    command -v git >/dev/null 2>&1 || { printf 'ERROR: git not on PATH\n' >&2; missing=1; }
    [[ "${missing}" -eq 0 ]] || exit 2

    WT_BIN="${REPO_ROOT}/bin/endless-go"
    if [[ ! -x "${WT_BIN}" ]]; then
        printf 'ERROR: worktree endless-go not built (run: just build)\n' >&2
        exit 2
    fi

    trap cleanup EXIT
    make_ledger_repo

    printf '%sE-1660 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${REPO_ROOT}"
    printf '  binary:  %s\n' "${WT_BIN}"

    test_gate_armed
    test_matching_passes
    test_fix_present

    summary
}

main "$@"

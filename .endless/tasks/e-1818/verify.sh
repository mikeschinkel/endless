#!/usr/bin/env bash
#
# E-1818 verification — an unlanded worktree binary must not apply
# schema/migrations (or fail-close the enum integrity gate) to a real DB it does
# not own.
#
# Run from anywhere inside the worktree:
#   esu
#   endless task verify E-1818
#
# FAIL-FAST: each step aborts the run on first failure (exit 1); setup problems
# exit 2. This is the single comprehensive gate for E-1818.
#
# THE BUG (E-1659 incident). A self-dev worktree's .claude/settings.json runs
# <worktree>/bin/endless-go on every Claude tool event; the hook calls
# ForceRealDB() (PinMainDB() for `endless-go tmux`), which pins the
# candidate binary onto ~/.config/endless/endless.db so its DATA writes land in
# the real ledger. monitor.DB() then applied the candidate's embedded schema.SQL
# + enum integrity gate to that real DB — so an unlanded binary migrated (and,
# via a destructive schema.SQL, corrupted) a database it does not own, machine
# wide, before any land or explicit action.
#
# THE FIX. monitor.DB() opens schema-passive when pinned onto a foreign real DB
# (pinnedToForeignRealDB(): dbPathOverride != ""): it skips the schema.SQL exec
# and every enum integrity gate. Owner opens (deployed binary, self-detected
# sandbox, explicit --config-dir at land) are unchanged, so migration via
# `endless db apply-change` and `just install` still works.
#
# WHY these steps prove it, and stay non-disruptive:
#   1. `just build`                       — the candidate binary compiles.
#   2. the E-1818 monitor unit tests      — schema-passive on the pin; owner path
#                                           still migrates + fail-closes on drift;
#                                           end-to-end via the real PinMainDB()
#                                           entry point with a redirected HOME.
#   3. built-binary E2E (throwaway HOME)  — the ACTUAL bin/endless-go, pinned via
#                                           PinMainDB against a divergent "real"
#                                           DB under a temp HOME, no longer
#                                           fail-closes and leaves it unchanged.
#                                           Your real ledger is never touched.
#   4. full Go + Python suites            — complete regression.
# Modeled on .endless/tasks/e-1682/verify.sh (structure) with fail-fast control.

# Refuse a direct run, and pick up the shared harness vocabulary. Sourced as the
# FIRST executable statement so the refusal fires before anything in this file
# runs; every definition below overrides the harness's own, so a suite written
# before the harness existed behaves exactly as it did.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

# ─── output ───────────────────────────────────────────────────────────────────

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi
UNDERLINE="──────────────────────────────────────────────────────────────"

section() { printf '\n%s%s%s\n%s\n' "${BOLD}" "$1" "${RESET}" "${UNDERLINE}"; }
note()    { printf '  %s· %s%s\n' "${DIM}" "$1" "${RESET}"; }
pass()    { printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"; }

# fail DESC [DETAIL...] — print and abort the whole run (fail-fast).
fail() {
    local desc="$1"; shift
    printf '  %s✗ %s%s\n' "${RED}" "${desc}" "${RESET}"
    local line
    for line in "$@"; do
        printf '      %s%s%s\n' "${DIM}" "${line}" "${RESET}"
    done
    printf '\n  %s%sE-1818 VERIFICATION FAILED%s\n\n' "${RED}" "${BOLD}" "${RESET}"
    exit 1
}

# ─── steps ────────────────────────────────────────────────────────────────────

REPO_ROOT=""
BIN=""

step_build() {
    section "1. Build — just build (Go binaries)"
    note "produces bin/endless-go, the candidate binary the E2E below drives"
    if ! out=$(just build 2>&1); then
        fail "just build" "${out}"
    fi
    [[ -x "${BIN}" ]] || fail "bin/endless-go present" "expected an executable at ${BIN}"
    pass "just build (bin/endless-go built)"
}

step_unit() {
    section "2. Unit tests — schema-passive gate + owner path intact (internal/monitor)"
    note "TestDBSchemaPassiveOnRealDBPin: pinned open of a divergent real DB does"
    note "  not fail-close and does not run schema.SQL (the tasks table stays absent)"
    note "TestDBOwnerPathMigratesAndVerifies: unpinned owner open still applies"
    note "  schema.SQL and still fail-closes on enum drift (apply-change unaffected)"
    note "TestDBSchemaPassiveViaPinMainDB: end-to-end via the real PinMainDB() entry"
    note "  point with a redirected HOME"
    if ! out=$(go test ./internal/monitor/ -count=1 \
        -run 'TestDBSchemaPassiveOnRealDBPin|TestDBOwnerPathMigratesAndVerifies|TestDBSchemaPassiveViaPinMainDB' 2>&1); then
        fail "E-1818 monitor unit tests" "${out}"
    fi
    pass "E-1818 monitor unit tests"
}

# step_binary_e2e drives the ACTUAL built bin/endless-go, pinned via PinMainDB
# (the `tmux` subcommand path in cmd/endless-go/main.go), against a fully-schema'd
# throwaway "real" DB whose task_types enum mirror has been diverged from the
# running binary's enum. Redirected HOME keeps DBPath() -> $HOME/.config/endless
# entirely inside a tempdir; the real ledger is never opened.
#
# active-id exits 1 either way (no active task for a bogus pane); the signal is
# stderr: the buggy binary prints a "integrity check" failure, the fixed one is
# silent. The divergent row must also survive the open unchanged.
step_binary_e2e() {
    section "3. Built-binary E2E — pinned open of a divergent real DB (throwaway HOME)"
    if ! command -v sqlite3 >/dev/null 2>&1; then
        note "sqlite3 not on PATH — skipping (Go tests in step 2 cover the same gate)"
        return
    fi

    local home db
    home=$(mktemp -d) || fail "mktemp throwaway HOME"
    db="${home}/.config/endless/endless.db"
    mkdir -p "${home}/.config/endless" || fail "mkdir throwaway config dir"

    note "seeding a full deployed schema at the throwaway real-DB path, then"
    note "diverging task_types.slug (id=1 -> 'todo') from the binary's enum"
    if ! out=$(sqlite3 "${db}" < "${REPO_ROOT}/internal/schema/schema.sql" 2>&1); then
        rm -rf "${home}"; fail "seed schema into throwaway DB" "${out}"
    fi
    if ! out=$(sqlite3 "${db}" "UPDATE task_types SET slug='todo', label='Todo' WHERE id=1;" 2>&1); then
        rm -rf "${home}"; fail "diverge task_types in throwaway DB" "${out}"
    fi

    note "running: HOME=<tmp> bin/endless-go tmux active-id (pins via PinMainDB)"
    local stderr_out
    # Run from the throwaway HOME so no self-dev worktree routing applies; the
    # tmux subcommand takes PinMainDB unconditionally (no --config-dir).
    stderr_out=$(cd "${home}" && HOME="${home}" "${BIN}" tmux active-id --pane '%e1818verify' 2>&1 >/dev/null)

    if [[ "${stderr_out}" == *"integrity check"* || "${stderr_out}" == *"applying schema"* ]]; then
        rm -rf "${home}"
        fail "pinned built binary opens the divergent real DB schema-passive" \
            "stderr contained a schema/integrity error — the binary tried to migrate/verify a DB it does not own:" \
            "${stderr_out}"
    fi
    pass "pinned built binary did not fail-close on the divergent real DB"

    local slug
    slug=$(sqlite3 "${db}" "SELECT slug FROM task_types WHERE id=1;" 2>/dev/null)
    if [[ "${slug}" != "todo" ]]; then
        rm -rf "${home}"
        fail "real DB task_types unchanged after pinned open" \
            "expected slug 'todo' (untouched), got '${slug}'"
    fi
    pass "real DB task_types byte-for-byte unchanged (no seed reconcile ran)"

    rm -rf "${home}"
}

step_regression() {
    section "4. Full regression — go test ./... and the Python suite"
    note "subsumes the E-1818 unit tests plus the whole codebase"
    if ! out=$(go test ./... 2>&1); then
        fail "go test ./..." "$(printf '%s\n' "${out}" | tail -30)"
    fi
    pass "go test ./..."
    if ! out=$(just test 2>&1); then
        fail "just test (full Python suite)" "$(printf '%s\n' "${out}" | tail -30)"
    fi
    pass "just test (full Python suite)"
}

# ─── main ─────────────────────────────────────────────────────────────────────

main() {
    REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null)
    [[ -n "${REPO_ROOT}" ]] || { printf 'ERROR: not inside a git worktree\n' >&2; exit 2; }
    cd "${REPO_ROOT}" || exit 2

    local tool
    for tool in just go uv; do
        command -v "${tool}" >/dev/null 2>&1 || { printf 'ERROR: %s not on PATH\n' "${tool}" >&2; exit 2; }
    done
    BIN="${REPO_ROOT}/bin/endless-go"

    printf '%sE-1818 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  cwd:  %s\n' "${REPO_ROOT}"
    printf '  db:   throwaway (temp HOME); your real ledger is never opened\n'

    step_build
    step_unit
    step_binary_e2e
    step_regression

    printf '\n  %s%sE-1818 VERIFIED — ALL PASSED%s\n\n' "${GREEN}" "${BOLD}" "${RESET}"
}

main "$@"

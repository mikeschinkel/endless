#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1757 and records what was true when E-1757
# landed. Edit it only if you ARE E-1757. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1757 verification script — the unified `project init` command. `project init`
# registers the DB row AND scaffolds on-disk files in one idempotent pass, and
# `project register` is an alias for it. Exercises end-to-end against the
# worktree's sandbox DB, using throwaway scratch dirs for the on-disk scaffolding.
#
# Run from anywhere inside the worktree:
#   endless task verify E-1757
#
# Asserts:
#   1. `project init --help` and `project register --help` are both reachable.
#   2. Fresh `project init` on a scratch repo: exactly 1 projects row, scaffolds
#      .endless/config.json + .endless/tmp/ + the canonical 4-entry .gitignore
#      set (worktrees/, tmp/, worktree.json, worktree.lock) and NOT sessions/.
#   3. Idempotent re-run: still exactly 1 row, no duplicated .gitignore line.
#   4. No-clobber: an unmanaged config key (self_dev) survives a re-run.
#   5. Alias parity: `project register` yields the identical row + files.
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on setup error. A trap removes the scratch dirs and unregisters the
# test rows from the sandbox DB so re-runs stay clean.

# Refuse a direct run, and pick up the shared harness vocabulary. Sourced as the
# FIRST executable statement so the refusal fires before anything in this file
# runs; every definition below overrides the harness's own, so a suite written
# before the harness existed behaves exactly as it did.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

# ─── globals ────────────────────────────────────────────────────────────────

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

# The canonical .gitignore entries `project init` must scaffold (E-1757).
GITIGNORE_ENTRIES=(
    ".endless/worktrees/"
    ".endless/tmp/"
    ".endless/worktree.json"
    ".endless/worktree.lock"
)

# Unique per-run project names/dirs so the persistent sandbox DB stays clean.
INIT_NAME="e1757init$$"
REG_NAME="e1757reg$$"
INIT_DIR=""
REG_DIR=""

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

# Wrap the CLI so every invocation routes through the sandbox DB.
endless() {
    uv run endless "$@" --db sandbox
}

# Count projects rows for a given name in the sandbox DB.
row_count() {
    endless sql --tsv "SELECT count(*) FROM projects WHERE name='$1'" \
        2>/dev/null | tr -d '[:space:]'
}

# Count exact whole-line occurrences of a .gitignore entry.
gitignore_line_count() {
    local dir="$1" entry="$2"
    grep -Fxc "${entry}" "${dir}/.gitignore" 2>/dev/null || printf '0'
}

# Read a top-level key from a project's config.json (prints the JSON value or
# "None" if absent). Uses the worktree's Python via uv.
config_key() {
    local dir="$1" key="$2"
    uv run python -c '
import json, sys
with open(sys.argv[1]) as f:
    print(json.load(f).get(sys.argv[2]))
' "${dir}/.endless/config.json" "${key}" 2>/dev/null
}

cleanup() {
    [[ -n "${INIT_DIR}" ]] && rm -rf "${INIT_DIR}"
    [[ -n "${REG_DIR}" ]] && rm -rf "${REG_DIR}"
    endless project unregister "${INIT_NAME}" >/dev/null 2>&1 || true
    endless project unregister "${REG_NAME}" >/dev/null 2>&1 || true
}

# ─── generic assertions ─────────────────────────────────────────────────────

# assert_succeeds DESC CMD [ARGS...]
assert_succeeds() {
    local desc="$1"
    shift
    local output
    output=$("$@" 2>&1)
    local rc=$?
    if [[ "${rc}" -eq 0 ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "exit == 0" "exit=${rc} | output=${output}"
}

# assert_eq DESC EXPECTED ACTUAL
assert_eq() {
    local desc="$1" expected="$2" actual="$3"
    if [[ "${actual}" == "${expected}" ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "${expected}" "${actual}"
}

# assert_file DESC PATH
assert_file() {
    local desc="$1" path="$2"
    if [[ -f "${path}" ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "file exists" "missing: ${path}"
}

# assert_dir DESC PATH
assert_dir() {
    local desc="$1" path="$2"
    if [[ -d "${path}" ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "dir exists" "missing: ${path}"
}

# ─── scaffolding assertions shared by init and the register alias ───────────

# assert_scaffolded VERB DIR NAME
#   VERB is the CLI subcommand under test ("init" or "register"), used only in
#   the check labels; DIR/NAME are the scratch dir and project name.
assert_scaffolded() {
    local verb="$1" dir="$2" name="$3"

    assert_eq "'project ${verb}' created exactly 1 DB row" \
        "1" "$(row_count "${name}")"

    assert_file "'project ${verb}' wrote .endless/config.json" \
        "${dir}/.endless/config.json"
    assert_eq "config.json carries name '${name}'" \
        "${name}" "$(config_key "${dir}" name)"

    assert_dir "'project ${verb}' created .endless/tmp/" \
        "${dir}/.endless/tmp"

    local entry
    for entry in "${GITIGNORE_ENTRIES[@]}"; do
        assert_eq "'project ${verb}' .gitignore has '${entry}' (once)" \
            "1" "$(gitignore_line_count "${dir}" "${entry}")"
    done

    if grep -q '\.endless/sessions' "${dir}/.gitignore" 2>/dev/null; then
        report_fail "'project ${verb}' .gitignore omits .endless/sessions/" \
            "no .endless/sessions entry" "found one"
    else
        report_pass "'project ${verb}' .gitignore omits .endless/sessions/"
    fi
}

# ─── checks ─────────────────────────────────────────────────────────────────

test_help_reachable() {
    section "1. \`project init\` and \`project register\` alias are reachable"
    assert_succeeds "'project init --help' works" \
        endless project init --help
    assert_succeeds "'project register --help' works (alias)" \
        endless project register --help
}

test_fresh_init() {
    section "2. Fresh \`project init\` registers + scaffolds"
    endless project init "${INIT_DIR}" --infer \
        --name "${INIT_NAME}" --status active >/dev/null 2>&1
    assert_scaffolded "init" "${INIT_DIR}" "${INIT_NAME}"
}

test_idempotent_rerun() {
    section "3. Re-running \`project init\` is idempotent"
    endless project init "${INIT_DIR}" --infer \
        --name "${INIT_NAME}" --status active >/dev/null 2>&1

    assert_eq "re-run left exactly 1 DB row (no duplicate)" \
        "1" "$(row_count "${INIT_NAME}")"

    local entry
    for entry in "${GITIGNORE_ENTRIES[@]}"; do
        assert_eq "re-run: '${entry}' still appears exactly once" \
            "1" "$(gitignore_line_count "${INIT_DIR}" "${entry}")"
    done
}

test_no_clobber() {
    section "4. Re-running \`project init\` does not clobber unmanaged keys"
    # Inject an endless-managed-elsewhere key that init's writer doesn't set.
    uv run python -c '
import json, sys
p = sys.argv[1]
with open(p) as f:
    cfg = json.load(f)
cfg["self_dev"] = True
with open(p, "w") as f:
    json.dump(cfg, f, indent=2)
    f.write("\n")
' "${INIT_DIR}/.endless/config.json"

    endless project init "${INIT_DIR}" --infer \
        --name "${INIT_NAME}" --status active >/dev/null 2>&1

    assert_eq "unmanaged key 'self_dev' survived the re-run" \
        "True" "$(config_key "${INIT_DIR}" self_dev)"
}

test_register_alias_parity() {
    section "5. \`project register\` alias scaffolds identically"
    endless project register "${REG_DIR}" --infer \
        --name "${REG_NAME}" --status active >/dev/null 2>&1
    assert_scaffolded "register" "${REG_DIR}" "${REG_NAME}"
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

    INIT_DIR=$(mktemp -d) || exit 2
    REG_DIR=$(mktemp -d) || exit 2
    trap cleanup EXIT

    printf '%sE-1757 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  db:      sandbox\n'
    printf '  python:  %s\n' "$(uv run python --version 2>&1 | tail -1)"

    test_help_reachable
    test_fresh_init
    test_idempotent_rerun
    test_no_clobber
    test_register_alias_parity

    summary
}

main "$@"

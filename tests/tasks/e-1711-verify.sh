#!/usr/bin/env bash
#
# E-1711 verification script — confirms drift_detection (E-917) and the orphaned
# suggestions framework (E-918) are fully excised.
#
# What was removed:
#   - Go hook (internal/hookcmd/claude.go): the PreToolUse drift check,
#     RegisterTaskFile recording, blockDriftViolation, the SUGGESTION scanner,
#     and the "N unreviewed suggestions" SessionStart line.
#   - Go monitor: IsFileInTaskScope + RegisterTaskFile (files.go) and the whole
#     suggestions.go. GetTaskTitle STAYS.
#   - Config: the drift_detection default (normalize.go + README.md).
#   - Schema: task_files + suggestions tables (schema.sql + db.py _migrate_v4).
#   - CLI: the `endless suggestions` command group (cli.py + suggestions_cmd.py).
#
# This script asserts the symbols/files/table/config-key/CLI command are gone,
# a survivor (GetTaskTitle) remains, a freshly-applied schema creates neither
# table, and the packages build + all suites pass.
#
# Run from anywhere inside the worktree (esu cd's here and exports the session):
#   esu && ./tests/tasks/e-1711-verify.sh
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

# Go sources scanned for excised symbols (test files excluded — retargeted tests
# legitimately still reference generic check keys, not drift/suggestion code).
GO_SRC=(
    internal/hookcmd/claude.go
    internal/monitor/files.go
    internal/config/normalize.go
)
PY_SRC=(
    src/endless/cli.py
    src/endless/db.py
)

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

# ─── assertions ─────────────────────────────────────────────────────────────

# assert_cmd DESC CMD [ARGS...]
#   Pass if CMD exits 0. On failure, report the tail of its combined output.
assert_cmd() {
    local desc="$1"
    shift
    local output
    output=$("$@" 2>&1)
    local rc=$?
    if [[ "${rc}" -eq 0 ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "exit=${rc} | $(printf '%s' "${output}" | tail -3 | tr '\n' '⏎')"
}

# assert_exits DESC WANT CMD [ARGS...]
#   Pass if CMD exits with status WANT (used for "command should fail").
assert_exits() {
    local desc="$1"
    local want="$2"
    shift 2
    local output
    output=$("$@" 2>&1)
    local rc=$?
    if [[ "${rc}" -eq "${want}" ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "exit=${rc}, want ${want} | $(printf '%s' "${output}" | tail -2 | tr '\n' '⏎')"
}

# assert_absent DESC PATTERN FILE...
#   Pass if the fixed-string PATTERN appears in NONE of the given files.
assert_absent() {
    local desc="$1"
    local pattern="$2"
    shift 2
    local hit
    hit=$(grep -Fl -- "${pattern}" "$@" 2>/dev/null)
    if [[ -z "${hit}" ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "still found in: $(printf '%s' "${hit}" | tr '\n' ' ')"
}

# assert_present DESC PATTERN FILE
#   Pass if the fixed-string PATTERN appears in FILE.
assert_present() {
    local desc="$1"
    local pattern="$2"
    local file="$3"
    if grep -qF -- "${pattern}" "${file}" 2>/dev/null; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "expected to find ${pattern} in ${file}"
}

# assert_no_file DESC PATH
assert_no_file() {
    local desc="$1"
    local path="$2"
    if [[ ! -e "${path}" ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "still present: ${path}"
}

# ─── checks ─────────────────────────────────────────────────────────────────

test_go_symbols_gone() {
    section "Go — drift/suggestion symbols excised from source"

    assert_absent "no drift_detection reference"       "drift_detection"       "${GO_SRC[@]}"
    assert_absent "no blockDriftViolation"             "blockDriftViolation"   internal/hookcmd/claude.go
    assert_absent "no IsFileInTaskScope"               "IsFileInTaskScope"     "${GO_SRC[@]}"
    assert_absent "no RegisterTaskFile"                "RegisterTaskFile"      "${GO_SRC[@]}"
    assert_absent "no ScanRecentSuggestions"           "ScanRecentSuggestions" internal/hookcmd/claude.go
    assert_absent "no CountOpenSuggestions"            "CountOpenSuggestions"  internal/hookcmd/claude.go
}

test_py_symbols_gone() {
    section "Python — suggestions/drift symbols excised from source"

    assert_absent "no suggestions command group"  "@main.group(\"suggestions\")" src/endless/cli.py
    assert_absent "no suggestions_cmd import"      "suggestions_cmd"              src/endless/cli.py
    assert_absent "no _migrate_v4 function"        "_migrate_v4"                  src/endless/db.py
    assert_absent "no task_files DDL in db.py"     "task_files"                   src/endless/db.py
    assert_absent "no suggestions DDL in db.py"    "CREATE TABLE IF NOT EXISTS suggestions" src/endless/db.py
}

test_files_gone() {
    section "Files — deleted modules absent"

    assert_no_file "internal/monitor/suggestions.go deleted"      internal/monitor/suggestions.go
    assert_no_file "internal/monitor/suggestions_test.go deleted" internal/monitor/suggestions_test.go
    assert_no_file "src/endless/suggestions_cmd.py deleted"       src/endless/suggestions_cmd.py
    assert_no_file "docs/guide/help/suggestions.md deleted"       docs/guide/help/suggestions.md
}

test_survivors() {
    section "Survivors — kept symbols still present"

    assert_present "GetTaskTitle still in files.go"      "func GetTaskTitle"      internal/monitor/files.go
    assert_present "extractFilePath still in claude.go"  "func extractFilePath"   internal/hookcmd/claude.go
}

test_schema_clean() {
    section "Schema — fresh DB has neither table; config key gone"

    # Apply the authoritative schema to a throwaway sqlite DB (stdlib sqlite3,
    # no project deps) and confirm neither table is created.
    local out
    out=$(python3 - <<'PY' 2>&1
import sqlite3, tempfile, os, sys
p = os.path.join(tempfile.mkdtemp(), "t.db")
c = sqlite3.connect(p)
c.executescript(open("internal/schema/schema.sql").read())
tables = {r[0] for r in c.execute("SELECT name FROM sqlite_master WHERE type='table'")}
bad = [t for t in ("task_files", "suggestions") if t in tables]
print("BAD:" + ",".join(bad) if bad else "OK")
PY
)
    if [[ "${out}" == "OK" ]]; then
        report_pass "fresh schema.sql creates no task_files/suggestions table"
    else
        report_fail "fresh schema.sql creates no task_files/suggestions table" "${out}"
    fi

    assert_absent "drift_detection gone from schema.sql"        "task_files"       internal/schema/schema.sql
    assert_absent "suggestions table gone from schema.sql"      "CREATE TABLE IF NOT EXISTS suggestions" internal/schema/schema.sql
    assert_absent "drift_detection gone from config README"     "drift_detection"  internal/config/README.md
}

test_cli_gone() {
    section "CLI — 'endless suggestions' is no longer a command"

    # `uv run endless` executes the worktree's Python source; Click exits 2 on an
    # unknown subcommand. A surviving group (task) must still exit 0 as a control.
    assert_exits "endless suggestions is unknown (exit 2)" 2 uv run endless suggestions --help
    assert_exits "control: endless task still resolves (exit 0)" 0 uv run endless task --help
}

test_builds_and_tests() {
    section "Build & tests — everything green"

    assert_cmd "just build succeeds" just build
    assert_cmd "go test internal/config" go test -count=1 ./internal/config/...
    assert_cmd "go test internal/monitor" go test -count=1 ./internal/monitor/...
    assert_cmd "go test internal/hookcmd" go test -count=1 ./internal/hookcmd/...
    assert_cmd "just test (Python suite) passes" just test
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

    for tool in go python3 just uv git; do
        if ! command -v "${tool}" >/dev/null 2>&1; then
            printf 'ERROR: %s not on PATH\n' "${tool}" >&2
            exit 2
        fi
    done

    # Worktrees need a go.work pointing at the local go-pkgs/ modules; without it
    # the replace directives resolve at the wrong depth and the build fails.
    if [[ ! -f "${repo_root}/go.work" ]]; then
        just go-work-init >/dev/null 2>&1
        if [[ ! -f "${repo_root}/go.work" ]]; then
            printf 'ERROR: go.work missing and could not be generated (run: just go-work-init)\n' >&2
            exit 2
        fi
    fi

    printf '%sE-1711 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  go:      %s\n' "$(go version 2>&1 | awk '{print $3}')"

    test_go_symbols_gone
    test_py_symbols_gone
    test_files_gone
    test_survivors
    test_schema_clean
    test_cli_gone
    test_builds_and_tests

    summary
}

main "$@"

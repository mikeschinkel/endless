#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2369 and records what was true when E-2369
# landed. Edit it only if you ARE E-2369. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2369: restrict which directories can be Endless projects.
#
# What is verified here:
#   A. Fail-fast: this task's Go and Python tests, each contract test by name
#      so deleting one cannot turn the section green by absence.
#   B. Through a binary built from this tree, against a throwaway database and
#      the runner's temp HOME: $HOME and above are disallowed; ~/Projects is a
#      parent directory whose children are allowed; a row at $HOME is never
#      inherited; ~/Downloads is refused whole; the overrides skeleton is
#      written; a project inside or around another is flagged.
#   C. The guide page is current and `endless guide disallowed-paths` shows it.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || setup_error "not inside a git worktree"
cd "${WT}" || setup_error "cannot cd to worktree root ${WT}"

TMP="$(mktemp -d)" || setup_error "cannot create a scratch directory"
TMP="$(cd "${TMP}" && pwd -P)"
trap 'rm -rf "${TMP}"' EXIT

command -v sqlite3 >/dev/null 2>&1 || setup_error "sqlite3 is required to seed the probe database"
command -v python3 >/dev/null 2>&1 || setup_error "python3 is required to read the JSON verdicts"

# ---------------------------------------------------------------------------
section "A. This task's own tests (fail-fast)"
# ---------------------------------------------------------------------------

if out=$(go test ./internal/monitor 2>&1); then
    report_pass "go test: monitor"
else
    report_fail "go test: monitor" "exit 0" "$(printf '%s' "${out}" | tail -30)"
    summary
fi

contract() {
    local t="$1"
    if go test ./internal/monitor -run "^${t}$" -v 2>&1 | grep -q "^--- PASS: ${t}"; then
        report_pass "contract test runs and passes: ${t}"
    else
        report_fail "contract test runs and passes: ${t}" "--- PASS: ${t}" "no PASS line"
    fi
}
for t in TestResolveDirectory_HomeAndAboveAreDisallowed \
         TestResolveDirectory_OutsideHomeIsAllowed \
         TestResolveDirectory_HomeRowIsNeverInherited \
         TestProjectIDForPath_NeverAutoRegistersHome \
         TestResolveDirectory_SelfAndSubtreeOverrides \
         TestResolveDirectory_OverrideRemovesABuiltin \
         TestResolveDirectory_HomeCannotBeRemoved \
         TestResolveDirectory_ParentDirStopsTheWalk \
         TestResolveDirectory_NestedProjects \
         TestXDGUserDir_LocalizedAndDisabled \
         TestDisallowedPathsDocIsCurrent; do
    contract "${t}"
done

if out=$(uv run pytest tests/test_disallowed_projects.py -q 2>&1); then
    report_pass "pytest: tests/test_disallowed_projects.py"
else
    report_fail "pytest: tests/test_disallowed_projects.py" "exit 0" "$(printf '%s' "${out}" | tail -30)"
fi

# ---------------------------------------------------------------------------
section "B. The resolver, end to end"
# ---------------------------------------------------------------------------

BIN="${TMP}/endless-go"
go build -o "${BIN}" ./cmd/endless-go || setup_error "cannot build cmd/endless-go from this tree"
PROBE="${TMP}/db"
mkdir -p "${PROBE}"
GO=("${BIN}" --db-dir "${PROBE}")

H="$(cd "${HOME}" && pwd -P)"
mkdir -p "${H}/Projects/app" "${H}/Downloads/some-repo-main" "${TMP}/work/outer/inner"

# field DIR KEY: one field of DIR's verdict.
field() {
    "${GO[@]}" project resolve "$1" 2>/dev/null | python3 -c '
import json, sys
d = json.load(sys.stdin)
rows = d["rows"] if isinstance(d, dict) else d
print(rows[0].get(sys.argv[1], ""))' "$2"
}

assert_eq "\$HOME is disallowed" "disallowed" "$(field "${H}" kind)"
assert_eq "the directory above \$HOME is disallowed" "disallowed" "$(field "$(dirname "${H}")" kind)"
assert_eq "~/Projects is disallowed" "disallowed" "$(field "${H}/Projects" kind)"
assert_contains "~/Projects is a parent directory" "parent directory" "$(field "${H}/Projects" rule)"
assert_eq "a directory in ~/Projects may be a project" "none" "$(field "${H}/Projects/app" kind)"
assert_eq "a directory outside \$HOME may be a project" "none" "$(field "${TMP}/work/outer" kind)"

if [[ "$(uname)" == "Darwin" ]]; then
    assert_eq "~/Downloads/<repo> is disallowed" "disallowed" "$(field "${H}/Downloads/some-repo-main" kind)"
    assert_eq "~/Downloads is refused as a subtree" "subtree" "$(field "${H}/Downloads/some-repo-main" scope)"
else
    report_skip "~/Downloads subtree" "Linux reads it from user-dirs.dirs; covered by the Go tests"
fi

if [[ -f "${PROBE}/disallowed-paths.json" ]]; then
    report_pass "the overrides skeleton is written on first need"
else
    report_fail "the overrides skeleton is written on first need" "${PROBE}/disallowed-paths.json" "missing"
fi

# The bug that filed E-2369: a row at $HOME claimed everything under it.
sqlite3 "${PROBE}/endless.db" "INSERT INTO projects (name, path) VALUES ('me', '~');" \
    || setup_error "cannot seed the \$HOME row"
assert_eq "a row at \$HOME is not inherited below it" "none" "$(field "${H}/Projects/app" kind)"

# Nesting: inside a project, and holding one.
sqlite3 "${PROBE}/endless.db" "INSERT INTO projects (name, path) VALUES ('inner', '${TMP}/work/outer/inner');" \
    || setup_error "cannot seed the nested row"
mkdir -p "${TMP}/work/outer/inner/src"
assert_eq "a directory inside a project is Within it" "True" "$(field "${TMP}/work/outer/inner/src" within)"
assert_eq "a directory around a project Encloses it" "${TMP}/work/outer/inner" "$(field "${TMP}/work/outer" encloses)"

# ---------------------------------------------------------------------------
section "C. The guide page"
# ---------------------------------------------------------------------------

if diff -q <("${BIN}" project disallowed-doc) docs/guide/disallowed-paths.md >/dev/null; then
    report_pass "docs/guide/disallowed-paths.md matches the list in the code"
else
    report_fail "docs/guide/disallowed-paths.md matches the list in the code" "no diff" "stale: run just disallowed-doc"
fi
assert_contains "endless guide disallowed-paths prints the page" \
    "Directories that cannot be projects" "$(uv run endless guide disallowed-paths 2>&1)"

summary

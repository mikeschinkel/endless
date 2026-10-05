#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2242 and records what was true when E-2242
# landed. Edit it only if you ARE E-2242. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2242 — a rewritten main no longer slips past worktree land.
#
#   1. The task's own unit tests (internal/landgate, the orphan-drop helper,
#      the land-gate classifier), fail-fast.
#   2. End to end through THIS worktree's `endless-go worktree land-gate`, on a
#      throwaway project declaring NO migration dirs whose main is rewritten
#      after the branch forks: the verdict is base_rewritten, it outranks the
#      project's pre-land hook, and `git rebase main` clears it.
#   3. Land's orphan-stripping step, on a branch whose leading auto-amend
#      orphan is followed by copies of a rewritten main's commits (one adding a
#      file main later changed — the 2026-10-05 shape): the copies are dropped,
#      not replayed into a conflict, and only the branch's own work remains.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null) || setup_error "not inside a git worktree"
cd "${REPO_ROOT}" || setup_error "cannot cd to ${REPO_ROOT}"
for tool in go git uv python3; do
    command -v "${tool}" >/dev/null 2>&1 || setup_error "${tool} not on PATH"
done

WORK_TMP=$(mktemp -d)
trap 'rm -rf "${WORK_TMP}"' EXIT

section "1. Unit tests (fail-fast)"
if go test -count=1 ./internal/landgate/ >"${WORK_TMP}/go.log" 2>&1; then
    report_pass "go test ./internal/landgate"
else
    report_fail "go test ./internal/landgate" "$(tail -40 "${WORK_TMP}/go.log")"
    summary
fi
PY_TESTS=(tests/test_worktree_land_orphan_drop.py tests/test_worktree_land_migration_gate.py)
if uv run pytest -q "${PY_TESTS[@]}" >"${WORK_TMP}/py.log" 2>&1; then
    report_pass "pytest ${PY_TESTS[*]}"
else
    report_fail "pytest ${PY_TESTS[*]}" "$(tail -40 "${WORK_TMP}/py.log")"
    summary
fi

g() { git -C "$1" "${@:2}" >/dev/null 2>&1 || setup_error "git ${*:2} in $1"; }
put() { mkdir -p "$(dirname "$1/$2")"; printf '%s\n' "$3" >"$1/$2"; }
commit() { g "$1" add -A; g "$1" commit -q -m "$2"; }

# new_repo DIR — a main checkout with one shared commit.
new_repo() {
    g "${WORK_TMP}" init -q -b main "$1"
    g "$1" config user.email t@example.com
    g "$1" config user.name t
    put "$1" .endless/config.json '{"name":"p"}'
    put "$1" README.md "init"
    commit "$1" base
}

# rewrite_main DIR SHARED — replay main's commits since SHARED onto one made
# "on the host", the way `git pull --rebase` does.
rewrite_main() {
    g "$1" checkout -q -b remote "$2"
    put "$1" HOST.md "edited on the host"
    commit "$1" "host edit"
    g "$1" checkout -q main
    g "$1" rebase -q remote
}

section "2. The gate, for a project with no migration dirs"
BIN="${WORK_TMP}/endless-go"
go build -o "${BIN}" ./cmd/endless-go >"${WORK_TMP}/build.log" 2>&1 \
    || setup_error "build endless-go: $(cat "${WORK_TMP}/build.log")"

MAIN="${WORK_TMP}/main"
WT="${WORK_TMP}/wt"
new_repo "${MAIN}"
SHARED=$(git -C "${MAIN}" rev-parse HEAD)
put "${MAIN}" decisions/ED-1.md "v1"
commit "${MAIN}" "Endless: add decision ED-1"
g "${MAIN}" worktree add -q -b task/1 "${WT}"
put "${WT}" code.go "package x"
commit "${WT}" "branch work"
rewrite_main "${MAIN}" "${SHARED}"
mkdir -p "${MAIN}/.endless/hooks"
printf '#!/bin/sh\necho hook ran\nexit 1\n' >"${MAIN}/.endless/hooks/pre-land.sh"
chmod +x "${MAIN}/.endless/hooks/pre-land.sh"

gate() {
    HOME="${WORK_TMP}/home" XDG_CONFIG_HOME="${WORK_TMP}/home/.config" \
        "${BIN}" worktree land-gate --project "${MAIN}" --worktree "${WT}" \
        --base main --task E-1 2>&1
}
field() { python3 -c 'import json,sys; v=json.load(sys.stdin); print(v.get(sys.argv[1]) or "")' "$1"; }

OUT=$(gate)
assert_eq "no migration dirs, rewritten main: verdict is base_rewritten, ahead of the hook" \
    "base_rewritten" "$(printf '%s' "${OUT}" | field source)"
assert_contains "summary names git rebase main" \
    "git rebase main" "$(printf '%s' "${OUT}" | field summary)"

g "${WT}" rebase -q main
rm "${MAIN}/.endless/hooks/pre-land.sh"
OUT=$(gate)
assert_eq "git rebase main clears it" "" "$(printf '%s' "${OUT}" | field source)"

section "3. Land's orphan drop skips a rewritten main's copies"
MAIN2="${WORK_TMP}/main2"
WT2="${WORK_TMP}/wt2"
new_repo "${MAIN2}"
SHARED=$(git -C "${MAIN2}" rev-parse HEAD)
put "${MAIN2}" .endless/db-ledger/x.jsonl '{"a":1}'
commit "${MAIN2}" "Endless: record ledger entry"
put "${MAIN2}" decisions/ED-1.md "v1"
commit "${MAIN2}" "Endless: add decision ED-1"
g "${MAIN2}" worktree add -q -b task/2 "${WT2}"
put "${WT2}" code.go "package x"
commit "${WT2}" "branch work"
rewrite_main "${MAIN2}" "${SHARED}"
put "${MAIN2}" decisions/ED-1.md "v2"
commit "${MAIN2}" "Endless: update decision ED-1"

DROP=$(uv run python3 -c '
import sys
from pathlib import Path
from endless.worktree_cmd import _drop_orphan_amendable_commits
print(_drop_orphan_amendable_commits(Path(sys.argv[1]), "main")[0])
' "${WT2}" 2>&1)
assert_eq "one leading orphan dropped, without a rebase conflict" "1" "${DROP}"
assert_eq "only the branch's own commit remains ahead of main" \
    "branch work" "$(git -C "${WT2}" log --format=%s main..HEAD)"
assert_eq "main's later edit of the copied file survives" \
    "v2" "$(cat "${WT2}/decisions/ED-1.md")"
assert_eq "HEAD stays attached to the task branch" \
    "refs/heads/task/2" "$(git -C "${WT2}" symbolic-ref HEAD 2>&1)"

summary

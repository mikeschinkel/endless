#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2232 and records what was true when E-2232
# landed. Edit it only if you ARE E-2232. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2232 — the land migration gate no longer reads a rewritten main's own
# migrations as the branch's, and names `git rebase main` as the fix.
#
#   1. The task's own unit tests (internal/landgate, the Python classifier),
#      fail-fast.
#   2. End to end through THIS worktree's `endless-go worktree land-gate`, on a
#      throwaway repo whose main is rewritten after the branch forks (the
#      2026-10-03 `git pull --rebase` shape): the verdict is base_rewritten,
#      not a migration collision, offers no rename, and `git rebase main`
#      clears it.
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
if uv run pytest -q tests/test_worktree_land_migration_gate.py >"${WORK_TMP}/py.log" 2>&1; then
    report_pass "pytest tests/test_worktree_land_migration_gate.py"
else
    report_fail "pytest tests/test_worktree_land_migration_gate.py" "$(tail -40 "${WORK_TMP}/py.log")"
    summary
fi

section "2. A rewritten main, end to end"
BIN="${WORK_TMP}/endless-go"
go build -o "${BIN}" ./cmd/endless-go >"${WORK_TMP}/build.log" 2>&1 \
    || setup_error "build endless-go: $(cat "${WORK_TMP}/build.log")"

MAIN="${WORK_TMP}/main"
WT="${WORK_TMP}/wt"
g() { git -C "$1" "${@:2}" >/dev/null 2>&1 || setup_error "git ${*:2} in $1"; }
put() { mkdir -p "$(dirname "$1/$2")"; printf '%s\n' "$3" >"$1/$2"; }
commit() { g "$1" add -A; g "$1" commit -q -m "$2"; }

g "${WORK_TMP}" init -q -b main "${MAIN}"
g "${MAIN}" config user.email t@example.com
g "${MAIN}" config user.name t
put "${MAIN}" .endless/config.json '{"name":"p","migrations":{"dirs":["db/migrations"]}}'
put "${MAIN}" db/migrations/00001_base.sql "-- base"
commit "${MAIN}" base
SHARED=$(git -C "${MAIN}" rev-parse HEAD)
put "${MAIN}" db/migrations/00002_main.sql "-- main"
commit "${MAIN}" "main migration"
g "${MAIN}" worktree add -q -b task/1 "${WT}"
put "${WT}" code.go "package x"
commit "${WT}" "branch work"
# Rewrite main: replay its commit onto one made "on the host".
g "${MAIN}" checkout -q -b remote "${SHARED}"
put "${MAIN}" README.md "edited on the host"
commit "${MAIN}" readme
g "${MAIN}" checkout -q main
g "${MAIN}" rebase -q remote

gate() {
    HOME="${WORK_TMP}/home" XDG_CONFIG_HOME="${WORK_TMP}/home/.config" \
        "${BIN}" worktree land-gate --project "${MAIN}" --worktree "${WT}" \
        --base main --task E-1 2>&1
}
field() { python3 -c 'import json,sys; v=json.load(sys.stdin); print(v.get(sys.argv[1]) or "")' "$1"; }

OUT=$(gate)
assert_eq "verdict source is base_rewritten, not migrations" \
    "base_rewritten" "$(printf '%s' "${OUT}" | field source)"
assert_contains "summary names git rebase main" \
    "git rebase main" "$(printf '%s' "${OUT}" | field summary)"
assert_not_contains "no rename of main's own migration is offered" \
    "00003_main" "${OUT}"
assert_not_contains "no sandbox-reset step for a rewrite" \
    "sandbox reset" "${OUT}"

g "${WT}" rebase -q main
OUT=$(gate)
assert_eq "git rebase main clears it" "False" \
    "$(printf '%s' "${OUT}" | python3 -c 'import json,sys; print(bool(json.load(sys.stdin).get("refused")))')"

summary

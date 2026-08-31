#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1719 and records what was true when E-1719
# landed. Edit it only if you ARE E-1719. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1719 verification script — the record-only landing slice: nullable
# task_landings.branch, historical --ts, and the explicit-value task.landed emit
# that `worktree land --record-only` produces.
#
# Run from anywhere inside the worktree:
#   endless task verify E-1719
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on a setup problem.
#
# Coverage is three sections:
#   A. Go unit tests for the DB-level behavior (NULL branch, evt.TS landed_at,
#      reaper NULL read).
#   B. Python pytest for the `worktree land --record-only` wiring.
#   C. An isolated end-to-end: seed a DB, apply the migration on a populated
#      old-shape table, emit a record-only landing, and assert the row.
#
# Section C runs fully isolated (a throwaway git repo for the ledger + a temp
# DB) rather than the worktree sandbox, because the emit path git-commits its
# ledger segment and `worktree land` itself is pinned to the real/main DB — so
# the sandbox can't host this end-to-end. This is the E-1596 ad-hoc-prototype
# shape, not a shared harness.

# Refuse a direct run, and pick up the shared harness vocabulary. Sourced as the
# FIRST executable statement so the refusal fires before anything in this file
# runs; every definition below overrides the harness's own, so a suite written
# before the harness existed behaves exactly as it did.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

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

section()     { printf '\n%s%s%s\n%s\n' "${BOLD}" "$1" "${RESET}" "${UNDERLINE}"; }
report_pass() { printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"; PASS_COUNT=$((PASS_COUNT + 1)); }
report_fail() {
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"
    printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "$2"
    printf '      %sgot:%s      %s\n' "${DIM}" "${RESET}" "$3"
    FAIL_COUNT=$((FAIL_COUNT + 1)); FAILED_TESTS+=("$1")
}
summary() {
    printf '\n%sSummary%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    if [[ "${FAIL_COUNT}" -eq 0 ]]; then
        printf '  %s%d passed%s\n\n  %sALL PASSED%s\n\n' \
            "${GREEN}" "${PASS_COUNT}" "${RESET}" "${GREEN}${BOLD}" "${RESET}"
        return 0
    fi
    printf '  %s%d passed%s, %s%d failed%s\n\n  %sFAILED:%s\n' \
        "${GREEN}" "${PASS_COUNT}" "${RESET}" "${RED}" "${FAIL_COUNT}" "${RESET}" "${RED}${BOLD}" "${RESET}"
    local t; for t in "${FAILED_TESTS[@]}"; do printf '    - %s\n' "${t}"; done
    printf '\n'; return 1
}

# assert_eq DESC EXPECTED ACTUAL
assert_eq() {
    if [[ "$2" == "$3" ]]; then report_pass "$1"; else report_fail "$1" "$2" "$3"; fi
}

# ─── section A: Go unit tests ────────────────────────────────────────────────

test_go_units() {
    section "A. Go unit tests (nullable branch, evt.TS landed_at, reaper NULL read)"
    local out rc
    out=$(go test ./internal/events/ -run 'TestExecTaskLanded' 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then report_pass "events: execTaskLanded (NULL branch + historical landed_at)"
    else report_fail "events: execTaskLanded" "go test exit 0" "exit=$rc"$'\n'"$out"; fi

    out=$(go test ./internal/monitor/ -run 'NullBranchSkipsBranchDelete' 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then report_pass "monitor: reaper reads a NULL-branch landing, skips branch -D"
    else report_fail "monitor: reaper NULL branch" "go test exit 0" "exit=$rc"$'\n'"$out"; fi

    out=$(go test ./internal/eventcmd/ 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then report_pass "eventcmd: event emit (incl. --ts) compiles + passes"
    else report_fail "eventcmd package" "go test exit 0" "exit=$rc"$'\n'"$out"; fi
}

# ─── section B: Python pytest for the wiring ─────────────────────────────────

test_py_wiring() {
    section "B. Python pytest — worktree land --record-only wiring"
    local out rc
    out=$(uv run pytest tests/test_worktree_land_record_only.py -q 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then
        report_pass "record-only wiring: actor system, session NULL, branch→'', --at from commit date, dry-run"
    else
        report_fail "record-only wiring pytest" "pytest exit 0" "exit=$rc"$'\n'"$out"
    fi
}

# ─── section C: isolated end-to-end ──────────────────────────────────────────

test_e2e() {
    section "C. Isolated end-to-end — migration + record-only emit + row shape"
    local GO="$1"

    local TMP REPO CFG GODIR SHA CDATE
    TMP="$(mktemp -d)"
    REPO="$TMP/repo"; CFG="$TMP/xdg"; GODIR="$CFG/endless"
    mkdir -p "$REPO/.endless/db-ledger" "$GODIR"
    git -C "$REPO" init -q
    GIT_COMMITTER_DATE="2026-05-09T18:30:00Z" \
        git -C "$REPO" -c user.email=t@t -c user.name=t \
        commit -q --allow-empty --date="2026-05-09T18:30:00Z" -m "probe landing commit"
    SHA="$(git -C "$REPO" rev-parse HEAD)"
    CDATE="$(git -C "$REPO" show -s --format=%cI "$SHA")"

    # TSV read / --write against the isolated Python DB.
    q()  { ( cd "$REPO" && XDG_CONFIG_HOME="$CFG" uv run endless sql "$1" --tsv 2>/dev/null ); }
    qw() { ( cd "$REPO" && XDG_CONFIG_HOME="$CFG" uv run endless sql "$1" --write >/dev/null 2>&1 ); }

    # The global `endless` is an editable install from main, so the DB it creates
    # carries main's OLD schema (branch NOT NULL) — exactly Phase 2's start state.
    qw "INSERT INTO projects (id,name,path,status,created_at,updated_at) VALUES (1,'probe','$REPO','active','2026-01-01T00:00:00','2026-01-01T00:00:00')"
    qw "INSERT INTO tasks (id,project_id,title,phase,status,type_id) VALUES (1209,1,'probe task','now','underway',1)"
    assert_eq "seed: project + task 1209 present" "1" "$(q "SELECT count(*) FROM tasks WHERE id=1209")"

    qw "INSERT INTO task_landings (task_id,branch,merge_commit_sha,landed_at) VALUES (1209,'task/1209-old','abc123','2026-05-09T00:00:00')"
    if qw "INSERT INTO task_landings (task_id,branch,merge_commit_sha,landed_at) VALUES (1209,NULL,'zzz','2026-05-09T00:00:00')"; then
        report_fail "pre-migration: NULL branch rejected" "insert refused (NOT NULL)" "insert accepted"
    else
        report_pass "pre-migration: NULL branch rejected by NOT NULL constraint"
    fi
    local before after
    before="$(q "SELECT count(*) FROM task_landings")"

    ( cd "$REPO" && "$GO" --config-dir "$GODIR" event apply-change \
        "$REPO_ROOT/internal/schema/changes/e-1719-nullable-task-landings-branch.sql" >/dev/null )
    after="$(q "SELECT count(*) FROM task_landings")"
    assert_eq "migration preserved existing rows" "$before" "$after"

    ( cd "$REPO" && "$GO" --config-dir "$GODIR" event emit \
        --kind task.landed --project probe --entity-type task --entity-id 1209 \
        --actor-kind system --actor-id backfill --node-id ba15 \
        --project-root "$REPO" --ts "$CDATE" \
        --payload "{\"branch\":\"\",\"merge_commit_sha\":\"$SHA\"}" >/dev/null )

    assert_eq "record-only landing: branch recorded NULL" \
        "1" "$(q "SELECT branch IS NULL FROM task_landings WHERE merge_commit_sha='$SHA'")"
    assert_eq "record-only landing: session_id NULL (system actor)" \
        "1" "$(q "SELECT session_id IS NULL FROM task_landings WHERE merge_commit_sha='$SHA'")"
    assert_eq "record-only landing: landed_at = commit date, not now()" \
        "2026-05-09T18:30:00" "$(q "SELECT landed_at FROM task_landings WHERE merge_commit_sha='$SHA'")"

    rm -rf "$TMP"
}

# ─── main ─────────────────────────────────────────────────────────────────────

main() {
    REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null)"
    [[ -n "${REPO_ROOT}" ]] || { printf 'ERROR: not inside a git worktree\n' >&2; exit 2; }
    cd "${REPO_ROOT}" || exit 2

    command -v go >/dev/null || { printf 'ERROR: go not on PATH\n' >&2; exit 2; }
    command -v uv >/dev/null || { printf 'ERROR: uv not on PATH\n' >&2; exit 2; }
    local GO="${REPO_ROOT}/bin/endless-go"
    [[ -x "$GO" ]] || { printf 'ERROR: %s missing — run `just build`\n' "$GO" >&2; exit 2; }

    printf '%sE-1719 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  cwd: %s\n' "${REPO_ROOT}"

    test_go_units
    test_py_wiring
    test_e2e "$GO"

    summary
}

main "$@"

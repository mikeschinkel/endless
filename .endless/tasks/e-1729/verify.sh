#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1729 and records what was true when E-1729
# landed. Edit it only if you ARE E-1729. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1729 verification script — ledger-target routing follows the active DB
# context (ED-1525): main context → project ledger, auto-committed on main
# (unchanged); sandbox context → ledger inside the sandbox dir, no git
# involvement; validate-db/rebuild-db read the same target emits write.
#
# Run from anywhere inside the worktree:
#   endless task verify E-1729
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on a setup problem.
#
# BEFORE E-1729 is implemented, sections B and C are expected to FAIL — they
# demonstrate the routing bug (sandbox emits land in the project ledger and
# amend main's ledger commit). After E-1729 lands, everything must pass.
#
# Coverage:
#   A. Main-context baseline (must pass before AND after): emit writes the
#      project ledger and auto-commits it on main, leaving the tree clean.
#   B. Sandbox-context routing: emit with a sandbox config dir writes the
#      sandbox's ledger, leaves the project ledger and its git history
#      untouched, and still lands the SQL row in the sandbox DB.
#   C. Read symmetry: validate-db in sandbox context replays the sandbox
#      ledger, not the project's. (If E-1729's implementation changes the
#      validate-db CLI shape, adjust the invocation here — the assertion is
#      the replayed-event count, not the flag spelling.)
#   D. Go regression: the events package (commit/amend machinery) still passes.
#
# Everything runs isolated: a throwaway git repo as project root, temp config
# dirs for both DBs, and a fake sandbox under a temp XDG_CACHE_HOME (sandbox
# detection is ConfigDir-under-CacheDir()/sandboxes, and CacheDir honors
# XDG_CACHE_HOME). No real DB, ledger, or cache is touched. This is the
# E-1596 ad-hoc-prototype shape, not a shared harness.

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

# ─── shared fixture ──────────────────────────────────────────────────────────

TMP=""; REPO=""; CACHE=""; MAIN_CFG=""; SB_ROOT=""; SB_CFG=""

# repo_ledger_lines: total JSONL lines across the project repo's ledger dir.
repo_ledger_lines() {
    cat "$REPO"/.endless/db-ledger/db-entries-*.jsonl 2>/dev/null | wc -l | tr -d ' '
}

# sandbox_ledger_lines: total JSONL lines in any segment file under the
# sandbox dir (the policy fixes the target to the sandbox; the exact subdir
# is E-1729's implementation detail, so search the whole sandbox root).
sandbox_ledger_lines() {
    find "$SB_ROOT" -name 'db-entries-*.jsonl' -exec cat {} + 2>/dev/null | wc -l | tr -d ' '
}

# q CFG_HOME SQL — TSV read against an isolated Python-created DB.
q()  { ( cd "$REPO" && XDG_CONFIG_HOME="$1" uv run endless sql "$2" --tsv 2>/dev/null ); }
qw() { ( cd "$REPO" && XDG_CONFIG_HOME="$1" uv run endless sql "$2" --write >/dev/null 2>&1 ); }

setup_fixture() {
    TMP="$(mktemp -d)"
    REPO="$TMP/repo"
    CACHE="$TMP/cache"
    MAIN_CFG="$TMP/xdg"                                # plain context: NOT under sandboxes/
    SB_ROOT="$CACHE/endless/sandboxes/e-9999"          # fake sandbox root
    SB_CFG="$SB_ROOT/endless"                          # its config dir (= Go --config-dir)
    mkdir -p "$REPO" "$MAIN_CFG/endless" "$SB_CFG"

    git -C "$REPO" init -q
    git -C "$REPO" config user.email verify@test
    git -C "$REPO" config user.name verify
    git -C "$REPO" commit -q --allow-empty -m "initial commit"

    # Seed both isolated DBs the way production arrives at them: the project
    # row's path points at the (real) project root — in the sandbox DB too,
    # mirroring seedFromWorktree, which is exactly what makes the routing
    # question live.
    qw "$MAIN_CFG" "INSERT INTO projects (id,name,path,status,created_at,updated_at) VALUES (1,'probe','$REPO','active','2026-01-01T00:00:00','2026-01-01T00:00:00')"
    qw "$MAIN_CFG" "INSERT INTO tasks (id,project_id,title,phase,status,type_id) VALUES (1209,1,'probe main task','now','underway',1)"
    qw "$SB_ROOT"  "INSERT INTO projects (id,name,path,status,created_at,updated_at) VALUES (1,'probe','$REPO','active','2026-01-01T00:00:00','2026-01-01T00:00:00')"
    qw "$SB_ROOT"  "INSERT INTO tasks (id,project_id,title,phase,status,type_id) VALUES (1210,1,'probe sandbox task','now','underway',1)"

    [[ "$(q "$MAIN_CFG" 'SELECT count(*) FROM tasks WHERE id=1209')" == "1" ]] || return 1
    [[ "$(q "$SB_ROOT"  'SELECT count(*) FROM tasks WHERE id=1210')" == "1" ]] || return 1
    return 0
}

# ─── section A: main-context baseline ────────────────────────────────────────

test_main_context() {
    section "A. Main context — project ledger + write-time auto-commit (unchanged behavior)"
    local GO="$1" sha

    ( cd "$REPO" && "$GO" --config-dir "$MAIN_CFG/endless" event emit \
        --kind task.landed --project probe --entity-type task --entity-id 1209 \
        --actor-kind system --actor-id e1729-verify --node-id aa01 \
        --project-root "$REPO" \
        --payload "{\"branch\":\"\",\"merge_commit_sha\":\"main-probe\"}" >/dev/null 2>&1 )
    assert_eq "main emit exits 0 and writes one project-ledger line" \
        "1" "$(repo_ledger_lines)"
    assert_eq "main emit auto-commits the segment (HEAD subject)" \
        "Endless: record ledger entry" \
        "$(git -C "$REPO" log -1 --format=%s)"
    assert_eq "main emit leaves the project tree clean" \
        "" "$(git -C "$REPO" status --porcelain)"
    assert_eq "main emit landed the SQL row in the main-context DB" \
        "1" "$(q "$MAIN_CFG" "SELECT count(*) FROM task_landings WHERE task_id=1209")"
}

# ─── section B: sandbox-context routing ──────────────────────────────────────

test_sandbox_context() {
    section "B. Sandbox context — ledger goes to the sandbox, project repo untouched"
    local GO="$1" rc head_before head_after lines_before

    head_before="$(git -C "$REPO" rev-parse HEAD)"
    lines_before="$(repo_ledger_lines)"

    ( cd "$REPO" && XDG_CACHE_HOME="$CACHE" "$GO" --config-dir "$SB_CFG" event emit \
        --kind task.landed --project probe --entity-type task --entity-id 1210 \
        --actor-kind system --actor-id e1729-verify --node-id bb02 \
        --project-root "$REPO" \
        --payload "{\"branch\":\"\",\"merge_commit_sha\":\"sandbox-probe\"}" >/dev/null 2>&1 )
    rc=$?
    head_after="$(git -C "$REPO" rev-parse HEAD)"

    assert_eq "sandbox emit exits 0" "0" "$rc"
    assert_eq "sandbox emit landed the SQL row in the sandbox DB" \
        "1" "$(q "$SB_ROOT" "SELECT count(*) FROM task_landings WHERE task_id=1210")"
    assert_eq "project ledger gained no lines from the sandbox emit" \
        "$lines_before" "$(repo_ledger_lines)"
    assert_eq "project git history untouched by the sandbox emit (HEAD unchanged)" \
        "$head_before" "$head_after"
    assert_eq "sandbox emit wrote exactly one line under the sandbox dir" \
        "1" "$(sandbox_ledger_lines)"
    assert_eq "sandbox ledger segment carries the sandbox node id" \
        "1" "$(find "$SB_ROOT" -name 'db-entries-bb02-*.jsonl' | wc -l | tr -d ' ')"
}

# ─── section C: read symmetry ────────────────────────────────────────────────

test_read_symmetry() {
    section "C. Read symmetry — validate-db in sandbox context replays the sandbox ledger"
    local GO="$1" out replayed

    out="$( cd "$REPO" && XDG_CACHE_HOME="$CACHE" "$GO" --config-dir "$SB_CFG" \
        event validate-db --project-root "$REPO" 2>&1 )"
    replayed="$(printf '%s' "$out" | grep -oE '[0-9]+ events replayed' | head -1)"
    # The sandbox ledger holds exactly the one sandbox emit; the project
    # ledger holds the main emit (plus, pre-fix, the leaked sandbox line).
    assert_eq "sandbox validate-db replays exactly the sandbox's 1 event" \
        "1 events replayed" "${replayed:-<no 'events replayed' line in output>}"
}

# ─── section D: Go regression ────────────────────────────────────────────────

test_go_regression() {
    section "D. Go regression — events package (commit/amend machinery)"
    local out rc
    out=$(go test ./internal/events/ 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then report_pass "go test ./internal/events/ passes"
    else report_fail "go test ./internal/events/" "exit 0" "exit=$rc"$'\n'"$out"; fi
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

    printf '%sE-1729 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  cwd: %s\n' "${REPO_ROOT}"

    if ! setup_fixture; then
        printf 'ERROR: fixture setup failed (isolated DB seeding)\n' >&2
        rm -rf "$TMP"; exit 2
    fi

    test_main_context "$GO"
    test_sandbox_context "$GO"
    test_read_symmetry "$GO"
    test_go_regression

    rm -rf "$TMP"
    summary
}

main "$@"

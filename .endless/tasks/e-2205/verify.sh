#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2205 and records what was true when E-2205
# landed. Edit it only if you ARE E-2205. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2205: a self_dev land no longer records ERR-0020 against itself.
#
# THE FAILURE (seen on the E-2189 land, error 1540)
#   The land migrated the main database (Step 5.5), then spent seconds
#   compiling the installed endless-go (Step 5.6). The tmux status line polls
#   that binary about once a second; in the gap it met a database ahead of it
#   and recorded ERR-0020 — a fault the land itself caused.
#
# THE CLAIMS
#   C1  `just go` is atomic: build to bin/endless-go.next, rename over
#       bin/endless-go. A failed build keeps the old binary; no .next is left.
#   C2  The land builds before migrating and swaps right after; nothing builds
#       after the migration. A build failure stops the land with the database
#       untouched, and its message names the re-run.
#   C3  The swap leaves milliseconds in which a reader that started on the old
#       binary reads the migrated version; the land clears exactly the ERR-0020
#       that caused — the fingerprint naming both versions, first seen inside
#       its window — after the record, and never fails over it.
#   C4  END TO END: a real land that migrates the database while a loop polls
#       the installed binary's status line leaves no open ERR-0020. The same
#       land in the old order (build after migrating, no clear) leaves one.
#
# Layers:
#   A. Fail-fast: build, and this task's Go and Python tests.
#   B. The claims by test name, so deleting one cannot pass by absence.
#   C. End to end, control then fix.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || setup_error "not inside a git worktree"
cd "${WT}" || setup_error "cannot cd to worktree root ${WT}"

command -v sqlite3 >/dev/null 2>&1 || setup_error "sqlite3 is required"
[[ -f "${WT}/go.work" ]] || setup_error "no go.work in ${WT}; run 'just go-work-init'"

TMP="$(mktemp -d)" || setup_error "cannot create a scratch directory"
trap 'rm -rf "${TMP}"' EXIT

# ─── A. fail-fast ───────────────────────────────────────────────────────────

section "A. The tree builds and this task's tests pass"

if out=$(just go 2>&1 && just migrate-bin 2>&1); then
    report_pass "just go && just migrate-bin"
else
    report_fail "just go && just migrate-bin" "exit 0" "$(printf '%s' "${out}" | tail -20)"
    summary
fi
assert_eq "just go leaves no bin/endless-go.next behind" "no" \
    "$([[ -e bin/endless-go.next ]] && echo yes || echo no)"

if out=$(go test -count=1 ./internal/faults/ ./internal/errorscmd/ 2>&1 \
        && go test -count=1 ./internal/monitor/ -run 'DatabaseAhead|ConnectAction|SchemaRefusal' 2>&1); then
    report_pass "go test: faults, errorscmd, monitor schema refusal"
else
    report_fail "go test: faults, errorscmd, monitor schema refusal" "exit 0" \
        "$(printf '%s' "${out}" | tail -30)"
    summary
fi

PY_TESTS=(tests/test_worktree_land_record_binary.py
          tests/test_just_go_atomic.py
          tests/test_worktree_land_migrate_up.py
          tests/test_worktree_land_migrate_exec.py
          tests/test_worktree_land_schema_apply.py)
if out=$(uv run pytest -q "${PY_TESTS[@]}" 2>&1); then
    report_pass "pytest: land record-binary, just go atomic, migrate-up, migrate-exec, schema-apply"
else
    report_fail "pytest: land record-binary, just go atomic, migrate-up, migrate-exec, schema-apply" \
        "exit 0" "$(printf '%s' "${out}" | tail -30)"
    summary
fi

# ─── B. the claims, by name ─────────────────────────────────────────────────

section "B. Each claim's test runs and passes (C1-C3)"

go_contract() {
    local pkg="$1" t="$2"
    if go test -count=1 "${pkg}" -run "^${t}$" -v 2>&1 | grep -q "^--- PASS: ${t}"; then
        report_pass "go: ${t}"
    else
        report_fail "go: ${t}" "--- PASS: ${t}" "no PASS line"
    fi
}
go_contract ./internal/faults/ TestClearFingerprintSince_ClearsOnlyThatFingerprintInsideTheWindow
go_contract ./internal/faults/ TestClearFingerprintSince_LeavesAnIncidentOpenedBeforeTheWindow
go_contract ./internal/monitor/ TestDatabaseAheadFingerprint_MatchesTheRecordedRefusal

py_contract() {
    local node="tests/$1"
    if out=$(uv run pytest -q "${node}" 2>&1) && [[ "${out}" == *passed* ]]; then
        report_pass "py: $1"
    else
        report_fail "py: $1" "passed" "$(printf '%s' "${out}" | tail -10)"
    fi
}
py_contract test_just_go_atomic.py::test_a_successful_build_replaces_the_binary_and_leaves_no_next
py_contract test_just_go_atomic.py::test_a_failed_build_keeps_the_old_binary_and_leaves_no_next
py_contract test_just_go_atomic.py::test_go_build_next_leaves_the_installed_binary_alone
R=test_worktree_land_record_binary.py
py_contract ${R}::test_build_then_migrate_then_swap_then_record_with_the_installed_binary
py_contract ${R}::test_a_failed_build_stops_before_the_migration
py_contract ${R}::test_a_failed_swap_is_post_migration_and_records_nothing
py_contract ${R}::test_build_failure_message_says_nothing_migrated_and_names_the_rerun
py_contract ${R}::test_swap_failure_message_is_rerunnable
py_contract ${R}::test_swap_renames_the_built_binary_over_the_installed_one
py_contract ${R}::test_build_runs_go_build_next_in_the_main_checkout
py_contract ${R}::test_build_and_swap_do_nothing_outside_self_dev
py_contract ${R}::test_clear_names_both_versions_the_window_and_the_land
py_contract ${R}::test_no_migration_clears_nothing
py_contract ${R}::test_a_failed_clear_never_fails_the_land

# ─── C. end to end ──────────────────────────────────────────────────────────

section "C. A real land migrating the database under a polling status line (C4)"

# The "old installed binary": this project at the commit before its newest
# goose migration, so it carries one version fewer than this tree.
newest=$(ls internal/schema/migrations/ | grep -E '^[0-9]+_' | sort | tail -1)
added=$(git log -1 --format=%H --diff-filter=A -- "internal/schema/migrations/${newest}")
[[ -n "${added}" ]] || setup_error "cannot find the commit that added ${newest}"
mkdir -p "${TMP}/old"
git archive "${added}^" | tar -x -C "${TMP}/old" \
    || setup_error "cannot export ${added}^"
cp "${WT}/go.work" "${TMP}/old/go.work"
(cd "${TMP}/old" && go build -o "${TMP}/old-endless-go" ./cmd/endless-go) >/dev/null 2>&1 \
    || setup_error "cannot build the pre-${newest} endless-go"

# land <mode> — stage a main database with the old binary (one version
# behind), then run the driver under its own HOME; sets OUT.
land() {
    local home="${TMP}/home-$1"
    mkdir -p "${home}"
    (cd "${TMP}" && HOME="${home}" XDG_CONFIG_HOME= \
        "${TMP}/old-endless-go" --db main event migrate >/dev/null) \
        || setup_error "could not build the scratch main database"
    printf '{}\n' >"${home}/.config/endless/config.json"
    OUT=$(cd "${TMP}" && HOME="${home}" XDG_CONFIG_HOME= \
        uv run --project "${WT}" python "${ENDLESS_VERIFY_DIR}/land_e2e.py" \
            "${WT}" "${TMP}/repo-$1" "$1" "${TMP}/old-endless-go" 2>&1)
}

latest=$((10#${newest%%_*}))

land control
assert_contains "control (build after migrating, the land before E-2205): the land completes" \
    "outcome=landed" "${OUT}"
assert_contains "control: the database was migrated to v${latest}" "db_version=${latest}" "${OUT}"
assert_contains "control: the status line left an open ERR-0020, as on the E-2189 land" \
    "err0020_open=1" "${OUT}"

land fixed
assert_contains "fixed: the land completes" "outcome=landed" "${OUT}"
assert_contains "fixed: the database was migrated to v${latest}" "db_version=${latest}" "${OUT}"
assert_contains "fixed: the landing is recorded" "landings=1" "${OUT}"
assert_contains "fixed: the installed binary is the new build" "installed_is_new=yes" "${OUT}"
assert_contains "fixed: no bin/endless-go.next is left behind" "next_left=no" "${OUT}"
assert_contains "fixed: no ERR-0020 is left open" "err0020_open=0" "${OUT}"
# Recorded-then-cleared is the residual race the clear exists for; it happens
# on some runs and not others. Whatever was recorded, the land cleared all of it.
inc=$(printf '%s\n' "${OUT}" | sed -n 's/^err0020_incidents=//p')
by_land=$(printf '%s\n' "${OUT}" | sed -n 's/^err0020_cleared_by_land=//p')
assert_eq "fixed: every ERR-0020 recorded (${inc:-?}) was cleared by the land" "${inc}" "${by_land}"
assert_not_contains "fixed: the poller ran (the absence is not vacuous)" "polls=0" "${OUT}"

land caught
assert_contains "caught (a poll forced between migrate and swap): the land completes" \
    "outcome=landed" "${OUT}"
assert_contains "caught: the poll recorded an ERR-0020" "err0020_incidents=1" "${OUT}"
assert_contains "caught: the land cleared it" "err0020_cleared_by_land=1" "${OUT}"
assert_contains "caught: no ERR-0020 is left open" "err0020_open=0" "${OUT}"

summary

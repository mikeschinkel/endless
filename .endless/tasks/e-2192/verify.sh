#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2192 and records what was true when E-2192
# landed. Edit it only if you ARE E-2192. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2192: a self_dev land applies the landing branch's goose migrations
# (`endless-migrate up`) before it records task.landed.
#
# THE FAILURE (E-2188)
#   `worktree land` applied only internal/schema/changes/ files. Goose
#   migrations reached main only when an installed binary next connected, so
#   the worktree's candidate endless-go that emits task.landed at Step 6 met a
#   database missing its own new column — `no such column: focus_task_id` —
#   and main was left advanced with the landing unrecorded.
#
# THE CLAIMS
#   C1  `endless-migrate up` brings a behind database to the latest embedded
#       version and seeds it; on a current database it is a no-op; `--db
#       sandbox` is refused; the executable offers exactly `apply` and `up` and
#       still links nothing but migration machinery.
#   C2  Every self_dev land runs `up` after the ff-merge and before the record,
#       behind ONE backup, in the order the task's land.toml names; land.toml
#       is refused before the merge when it holds anything unknown or invalid.
#   C3  The recovery re-run runs `up` before retrying the record.
#   C5  PRODUCT: land.toml is optional everywhere. Outside self_dev no table is
#       known, [self_dev] is refused like any unknown table, and neither the
#       refusal, the guide nor the scaffolded .endless/tasks/CLAUDE.md teaches
#       an Endless-only setting to a project it cannot apply to.
#   C4  END TO END: a real land (real git, real backup, real `up`, real
#       candidate emit) against a main database held at 00008 — the E-2188
#       shape — records the landing in one run. The same land with `up`
#       disabled (the land before this change) fails exactly as E-2188 did.
#
# Layers:
#   A. Fail-fast: build, and this task's Go and Python tests.
#   B. The claims by test name, so deleting one cannot pass by absence
#      (C1-C3, C5).
#   C. End to end, control then fix (C4).
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || setup_error "not inside a git worktree"
cd "${WT}" || setup_error "cannot cd to worktree root ${WT}"

command -v sqlite3 >/dev/null 2>&1 || setup_error "sqlite3 is required to stage the behind database"

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

if out=$(go test -count=1 ./internal/schemachange/ ./internal/schema/... 2>&1); then
    report_pass "go test: schemachange (the executable), schema"
else
    report_fail "go test: schemachange (the executable), schema" "exit 0" \
        "$(printf '%s' "${out}" | tail -30)"
    summary
fi

PY_TESTS=(tests/test_worktree_land_migrate_up.py
          tests/test_worktree_land_migrate_exec.py
          tests/test_worktree_land_schema_apply.py
          tests/test_suite_rules.py)
if out=$(uv run pytest -q "${PY_TESTS[@]}" 2>&1); then
    report_pass "pytest: land migrate-up, migrate-exec, schema-apply, suite rules"
else
    report_fail "pytest: land migrate-up, migrate-exec, schema-apply, suite rules" \
        "exit 0" "$(printf '%s' "${out}" | tail -30)"
    summary
fi

# ─── B. the claims, by name ─────────────────────────────────────────────────

section "B. Each claim's test runs and passes (C1-C3, C5)"

go_contract() {
    local t="$1"
    if go test -count=1 ./internal/schemachange/ -run "^${t}$" -v 2>&1 \
            | grep -q "^--- PASS: ${t}"; then
        report_pass "go: ${t}"
    else
        report_fail "go: ${t}" "--- PASS: ${t}" "no PASS line"
    fi
}
go_contract TestMigrateExecutable_UpBringsABehindDatabaseToLatestAndSeedsIt
go_contract TestMigrateExecutable_UpOnACurrentDatabaseIsANoOp
go_contract TestMigrateExecutable_UpRefusesDBSandbox
go_contract TestMigrateExecutable_UpRefusesADatabaseThatDoesNotExist
go_contract TestMigrateExecutable_OffersOnlyApplyAndUp
go_contract TestMigrateExecutable_LinksNothingButTheMigrationMachinery

py_contract() {
    local node="tests/test_worktree_land_migrate_up.py::$1"
    if out=$(uv run pytest -q "${node}" 2>&1) && [[ "${out}" == *passed* ]]; then
        report_pass "py: $1"
    else
        report_fail "py: $1" "passed" "$(printf '%s' "${out}" | tail -10)"
    fi
}
py_contract test_up_runs_after_the_merge_and_before_the_record
py_contract test_default_order_is_one_backup_then_up_then_changes
py_contract test_changes_first_order_from_land_toml
py_contract test_land_toml_refusals_name_the_file_and_the_offender
py_contract test_invalid_land_toml_refuses_before_the_merge
py_contract test_land_toml_is_read_from_the_landing_branch_not_main
py_contract test_the_recovery_rerun_runs_up_before_recording
py_contract test_up_failure_reports_main_advanced_and_does_not_record
py_contract test_non_self_dev_land_runs_no_up
py_contract test_no_land_toml_changes_nothing_outside_self_dev
py_contract test_outside_self_dev_no_table_is_known_and_none_is_advertised
py_contract test_a_bad_land_toml_refuses_a_non_self_dev_land_before_the_merge

if out=$(uv run pytest -q "tests/test_guide_conditionals.py::test_land_toml_self_dev_settings_are_taught_only_to_self_dev" 2>&1) \
        && [[ "${out}" == *passed* ]]; then
    report_pass "guide: land.toml's [self_dev] settings are taught only to self_dev projects"
else
    report_fail "guide: land.toml's [self_dev] settings are taught only to self_dev projects" \
        "passed" "$(printf '%s' "${out}" | tail -10)"
fi

# ─── C. end to end ──────────────────────────────────────────────────────────

section "C. A real land against a main database held at 00008 (C4)"

# stage_behind_db <home> — a main database under <home>, built by a NON-candidate
# copy of this tree's endless-go (a copy outside the worktree path), then held
# at goose 00008: sessions.focus_task_id dropped and 00009+ un-stamped.
stage_behind_db() {
    local home="$1" db="$1/.config/endless/endless.db"
    mkdir -p "${home}"
    cp "${WT}/bin/endless-go" "${TMP}/installed-endless-go"
    (cd "${TMP}" && HOME="${home}" XDG_CONFIG_HOME= \
        "${TMP}/installed-endless-go" --db main event migrate >/dev/null) \
        || setup_error "could not build the scratch main database"
    sqlite3 "${db}" "ALTER TABLE sessions DROP COLUMN focus_task_id;
                     DELETE FROM goose_db_version WHERE version_id >= 9;" \
        || setup_error "could not hold the scratch database at 00008"
    printf '{}\n' >"${home}/.config/endless/config.json"
    [[ "$(sqlite3 "${db}" 'SELECT max(version_id) FROM goose_db_version')" == 8 ]] \
        || setup_error "the scratch database is not at 00008"
}

# land <mode> — run the driver under its own HOME; sets OUT.
land() {
    local home="${TMP}/home-$1"
    stage_behind_db "${home}"
    OUT=$(cd "${TMP}" && HOME="${home}" XDG_CONFIG_HOME= \
        uv run --project "${WT}" python "${ENDLESS_VERIFY_DIR}/land_e2e.py" \
            "${WT}" "${TMP}/repo-$1" "$1" 2>&1)
}

land control
assert_contains "control (no \`up\`, the land before E-2192): main advances" \
    "main_advanced=yes" "${OUT}"
assert_contains "control: recording fails on the unmigrated column, as E-2188 did" \
    "no such column: focus_task_id" "${OUT}"
assert_contains "control: no landing is recorded" "landings=0" "${OUT}"

land fixed
assert_contains "fixed: the land completes" "outcome=landed" "${OUT}"
assert_contains "fixed: main advances" "main_advanced=yes" "${OUT}"
assert_contains "fixed: the database was migrated to the branch's latest" \
    "db_version=10" "${OUT}"
assert_contains "fixed: the landing is recorded in the same run" "landings=1" "${OUT}"
assert_not_contains "fixed: no missing-column failure" "no such column" "${OUT}"

summary

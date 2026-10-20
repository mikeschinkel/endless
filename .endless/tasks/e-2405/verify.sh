#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2405 and records what was true when E-2405
# landed. Edit it only if you ARE E-2405. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2405: spawn, prime and claim a task in its own project's directory, and the
# spawning session tracks what it spawned.
#
# What is verified here:
#   A. Fail-fast: the touched Go packages, and the Python tests for the
#      commands that create or locate one task's worktree.
#   B. Each contract test by NAME, so deleting one cannot turn the suite green
#      by absence. The Python ones run against real git repositories (two
#      registered projects, run from the first) and an endless-go built from
#      this tree, so the worktree, the hook and the events are real.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || setup_error "not inside a git worktree"
cd "${WT}" || setup_error "cannot cd to worktree root ${WT}"
just build >/dev/null 2>&1 || setup_error "just build failed"

# ---------------------------------------------------------------------------
section "A. This task's own tests (fail-fast)"
# ---------------------------------------------------------------------------

PKGS=(./internal/sessiontaskrelation ./internal/events ./internal/monitor
      ./internal/sessionstatuscmd ./internal/schema/...)
if out=$(go test "${PKGS[@]}" 2>&1); then
    report_pass "go test: sessiontaskrelation, events, monitor, sessionstatuscmd, schema"
else
    report_fail "go test: sessiontaskrelation, events, monitor, sessionstatuscmd, schema" \
        "exit 0" "$(printf '%s' "${out}" | grep -v '^20[0-9][0-9]/' | tail -40)"
    summary
fi

PYTESTS=(tests/test_task_project_dir.py tests/test_spawn_foreground.py
         tests/test_session_resume_recover.py tests/test_task_claim_worktree.py
         tests/test_prime.py)
if out=$(uv run pytest "${PYTESTS[@]}" -q 2>&1); then
    report_pass "pytest: spawn, prime, claim, unsettled and resume in the task's project"
else
    report_fail "pytest: spawn, prime, claim, unsettled and resume in the task's project" \
        "exit 0" "$(printf '%s' "${out}" | tail -40)"
    summary
fi

# ---------------------------------------------------------------------------
section "B. Contract tests, by name"
# ---------------------------------------------------------------------------

check_go() {
    local pkg="$1" t="$2"
    if go test "${pkg}" -run "^${t}$" -v 2>&1 | grep -q "^--- PASS: ${t}"; then
        report_pass "${t}"
    else
        report_fail "${t}" "--- PASS: ${t}" "no PASS line"
    fi
}

check_py() {
    local t="$1"
    if uv run pytest "tests/test_task_project_dir.py::${t}" -q 2>&1 \
            | grep -qE '^[0-9]+ passed'; then
        report_pass "${t}"
    else
        report_fail "${t}" "passed" "did not pass"
    fi
}

# P1-P5, P7-P10, P16: the commands, run from one project on another's task.
for t in test_spawn_creates_the_worktree_in_the_tasks_project \
         test_spawn_tags_its_events_with_the_tasks_project \
         test_spawn_runs_the_tasks_projects_hook \
         test_spawn_names_the_project_when_it_is_not_cwds \
         test_spawn_in_cwds_own_project_does_not_name_it \
         test_prime_creates_the_worktree_in_the_tasks_project \
         test_claim_across_projects_refuses_and_names_spawn \
         test_unsettled_inspects_the_tasks_projects_worktree \
         test_resume_recreates_a_dropped_worktree_in_the_tasks_project \
         test_every_command_refuses_a_missing_project_path \
         test_spawn_records_the_task_on_the_spawning_session \
         test_an_auto_spawn_records_no_spawning_session; do
    check_py "${t}"
done

# P15-P17: the spawned relation, its rank, its row and its stewardship.
check_go ./internal/sessiontaskrelation TestRank_SpawnedSitsBetweenClaimedAndQueued
check_go ./internal/sessiontaskrelation TestSpawned_IdSlugLabel
check_go ./internal/events TestSessionTasksSpawned_RecordsRelationSix
check_go ./internal/events TestSessionTasksSpawned_UpgradesWeakerKeepsClaimed
check_go ./internal/monitor TestSessionStatusRows_SpawnedTaskInAnotherProject
check_go ./internal/monitor TestSessionStatusRowsForSession_SpawnedTaskInAnotherProject
check_go ./internal/monitor TestStewardship_SpawnerStewardsLikeAFiler
check_go ./internal/sessionstatuscmd TestRelationColumn_MarksSpawned
check_go ./internal/schema TestMigrate_MatchesSchemaSQL

summary

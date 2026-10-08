#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2272 and records what was true when E-2272
# landed. Edit it only if you ARE E-2272. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2272: the fault-triage job routes each new error to a session that fixes
# it, and sessions answer with `errors accept|decline`.
#
# What is verified here:
#   A. Fail-fast: this task's own tests — the router against a real fault store
#      with fake effects (triagejob), the store, the listing marker, the config
#      merge, the migration, the Python argv and goto refusals — with each
#      routing contract test named so deleting one cannot pass by absence.
#   B. End to end through a binary built from this tree, against a throwaway
#      database: an agent accepts an error, the listing marks it ✓, `show`
#      names who accepted it, `fixer` names the session; a person without a
#      session is refused; a decline queues it with its reason; a cleared
#      error cannot be escalated.
#   C. The job is registered and, with nothing opted in, says so and does
#      nothing.
#   D. The migrated schema carries both new tables.
#
# NOT verified here: real delivery through `claude -p` and SendMessage, a real
# resume or spawn. Those start real Claude sessions and are proved by use.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || setup_error "not inside a git worktree"
cd "${WT}" || setup_error "cannot cd to worktree root ${WT}"

TMP="$(mktemp -d)" || setup_error "cannot create a scratch directory"
trap 'rm -rf "${TMP}"' EXIT

# ---------------------------------------------------------------------------
section "A. This task's own tests (fail-fast)"
# ---------------------------------------------------------------------------

PKGS="./internal/triagejob ./internal/faults ./internal/errorscmd ./internal/config ./internal/autospawnjob ./internal/schema/..."
if out=$(go test ${PKGS} 2>&1); then
    report_pass "go test: triagejob, faults, errorscmd, config, autospawnjob, schema"
else
    report_fail "go test: triagejob, faults, errorscmd, config, autospawnjob, schema" "exit 0" \
        "$(printf '%s' "${out}" | tail -30)"
    summary
fi

out=$(go test ./internal/triagejob -v 2>&1)
for t in TestRoute_IdleLiveSessionIsMessagedByPane \
         TestRoute_BusySessionWaitsUntilIdleUnlessEscalated \
         TestRoute_HeldMessageFallsBackToFileAndSpawn \
         TestRoute_SenderFailureFallsBackAndFailsTheRun \
         TestRoute_EndedSessionWithTranscriptIsResumedAndThrottled \
         TestRoute_EndedSessionWithoutTranscriptIsFiled \
         TestRoute_ThrottleHoldsASpawnUntilOpen \
         TestRoute_DeclineFilesAndSpawns \
         TestRoute_AcceptSettlesIt \
         TestRoute_IdleWithoutAnswerFallsBackAfterTheGrace \
         TestRoute_FixSessionFaultIsRecordedNeverSpawned \
         TestRoute_RecurrenceJoinsTheOpenFixTask \
         TestRoute_RecurrenceAfterASettledFixFilesANewOneCleaningItUp \
         TestRoutables_OnlyAfterTheOptInWatermark \
         TestParseDelivery; do
    assert_contains "routing contract runs and passes: ${t}" "--- PASS: ${t}" "${out}"
done

for t in TestListingLines_MarkAcceptedIncidentsWithOneGlyph; do
    assert_contains "contract runs and passes: ${t}" "--- PASS: ${t}" \
        "$(go test ./internal/errorscmd -run "^${t}$" -v 2>&1)"
done
assert_contains "contract runs and passes: TestMerge_FaultTriageEnabledIsNeverInherited" \
    "--- PASS: TestMerge_FaultTriageEnabledIsNeverInherited" \
    "$(go test ./internal/config -run '^TestMerge_FaultTriageEnabledIsNeverInherited$' -v 2>&1)"

if out=$(uv run pytest tests/test_error_triage_cli.py tests/test_go_cli_parity.py -q 2>&1); then
    report_pass "pytest: triage argv, goto refusals, Go/Python verb parity"
else
    report_fail "pytest: triage argv, goto refusals, Go/Python verb parity" "exit 0" \
        "$(printf '%s' "${out}" | tail -20)"
fi

# ---------------------------------------------------------------------------
section "B. Answers, end to end"
# ---------------------------------------------------------------------------
# Built from this tree, against an explicit --db-dir, so nothing here touches
# the real record or this worktree's sandbox. No routing runs: escalate is
# exercised only on its refusal, because a real route starts real sessions.

BIN="${TMP}/endless-go"
go build -o "${BIN}" ./cmd/endless-go || setup_error "cannot build cmd/endless-go from this tree"
PROBE="${TMP}/config"
mkdir -p "${PROBE}"
GO=("${BIN}" --db-dir "${PROBE}")

person() {
    env -u CLAUDE_CODE_ENTRYPOINT -u __CFBundleIdentifier -u CLAUDECODE \
        -u CLAUDE_CODE_SESSION_ID -u ENDLESS_SESSION_ID "$@"
}
agent() {
    person CLAUDE_CODE_ENTRYPOINT=cli "$@"
}

(cd "${TMP}" && person "${GO[@]}" errors raise --summary "e-2272 primer") >"${TMP}/primer.txt" 2>&1 \
    || setup_error "errors raise (primer) failed: $(head -3 "${TMP}/primer.txt")"
command -v sqlite3 >/dev/null 2>&1 || setup_error "sqlite3 is required to seed the probe"
sqlite3 "${PROBE}/endless.db" <<'SQL' || setup_error "cannot seed the probe database"
INSERT INTO projects (id, name, path) VALUES (900, 'e-2272-probe', '/tmp/e-2272-probe-nonexistent');
INSERT INTO tasks (id, project_id, title, status, type_id) VALUES (40, 900, 't', 'underway', 1);
INSERT INTO sessions (id, session_id, project_id, task_id, platform, state, started_at, last_activity)
VALUES (7, 'e2272-claude-uuid', 900, 40, 'claude', 'working', '2026-10-08T00:00:00', '2026-10-08T00:00:00');
SQL

(cd "${TMP}" && person "${GO[@]}" errors raise --summary "e-2272 accepted") >"${TMP}/r1.txt" 2>&1
ID=$(grep -oE 'as error [0-9]+' "${TMP}/r1.txt" | grep -oE '[0-9]+$')
(cd "${TMP}" && person "${GO[@]}" errors raise --summary "e-2272 declined") >"${TMP}/r2.txt" 2>&1
DID=$(grep -oE 'as error [0-9]+' "${TMP}/r2.txt" | grep -oE '[0-9]+$')
[[ -n "${ID}" && -n "${DID}" ]] || setup_error "errors raise did not report incident ids"

person "${GO[@]}" errors accept "${ID}" >"${TMP}/p.txt" 2>&1
assert_eq "a person with no session is refused (exit 2)" "2" "$?"
assert_contains "and told to name one" "--session" "$(cat "${TMP}/p.txt")"

out=$(agent CLAUDE_CODE_SESSION_ID=e2272-claude-uuid "${GO[@]}" errors accept "${ID}" 2>&1)
assert_contains "an agent session accepts it as ES-7" "accepted by ES-7" "${out}"

LIST="$(person "${GO[@]}" errors list --all-projects 2>&1)"
assert_contains "the listing marks the accepted error ✓" "✓" "$(printf '%s\n' "${LIST}" | grep -F 'e-2272 accepted')"
assert_not_contains "and not the others" "✓" "$(printf '%s\n' "${LIST}" | grep -F 'e-2272 declined')"

SHOW="$(person "${GO[@]}" errors show "${ID}" 2>&1)"
assert_contains "show names who accepted it" "by ES-7" "${SHOW}"
assert_contains "show points at goto --error-fix" "session goto --error-fix ${ID}" "${SHOW}"
assert_eq "fixer names the accepting session" "ES-7" "$(person "${GO[@]}" errors fixer "${ID}" 2>/dev/null)"

person "${GO[@]}" errors fixer "${DID}" >/dev/null 2>&1
assert_eq "fixer on an unaccepted error exits 1" "1" "$?"

person "${GO[@]}" errors decline "${DID}" --session 7 >/dev/null 2>&1
assert_eq "decline without --reason is refused (exit 2)" "2" "$?"
out=$(person "${GO[@]}" errors decline "${DID}" --session 7 --reason "not mine: probe" 2>&1)
assert_contains "decline on a named session" "declined by ES-7" "${out}"
assert_eq "a decline queues it for file-and-spawn, with its reason" "queued|not mine: probe" \
    "$(sqlite3 "${PROBE}/endless.db" "SELECT state || '|' || decline_reason FROM error_triage WHERE error_id = ${DID}")"

person "${GO[@]}" errors clear "${DID}" >/dev/null 2>&1
out=$(person "${GO[@]}" errors escalate "${DID}" 2>&1)
assert_contains "a cleared error cannot be escalated" "is cleared" "${out}"

# ---------------------------------------------------------------------------
section "C. The job"
# ---------------------------------------------------------------------------

JOBS="$(person "${GO[@]}" jobs list 2>&1)"
assert_contains "fault-triage is a registered job" "fault-triage" "${JOBS}"
(cd "${TMP}" && person "${GO[@]}" jobs run --job fault-triage) >"${TMP}/run.txt" 2>&1
assert_contains "with nothing opted in it says so" "no project has opted in" \
    "$(person "${GO[@]}" jobs list 2>&1 | grep -F fault-triage)"

# ---------------------------------------------------------------------------
section "D. The migrated schema"
# ---------------------------------------------------------------------------

assert_eq "error_triage and fault_triage_projects exist" "2" \
    "$(sqlite3 "${PROBE}/endless.db" "SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('error_triage','fault_triage_projects')")"

summary

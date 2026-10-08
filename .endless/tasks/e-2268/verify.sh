#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2268 and records what was true when E-2268
# landed. Edit it only if you ARE E-2268. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2268: every fault records which task and session raised it, and
# `errors list` / `errors show` say so.
#
# What is verified here:
#   A. Fail-fast: this task's own tests — the store (faults), the resolver
#      policy (monitor), the rendering (errorscmd), the migration (schema) and
#      the rater's explicit task — with each contract test named so deleting
#      one cannot turn the section green by absence.
#   B. End to end through a binary built from this tree, against a throwaway
#      database: an agent-run fault names its session and that session's task;
#      a person-run fault in a task worktree names the task and NO session,
#      even with ENDLESS_SESSION_ID exported; the two land on ONE incident whose
#      latest raiser is the person's and which carries a +1 breadth marker.
#   C. `errors show` lists every raiser, and --detail names each occurrence's.
#   D. A migrated database carries the new columns and table.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || setup_error "not inside a git worktree"
cd "${WT}" || setup_error "cannot cd to worktree root ${WT}"

TMP="$(mktemp -d)" || setup_error "cannot create a scratch directory"
trap 'rm -rf "${TMP}"' EXIT

# ---------------------------------------------------------------------------
section "A. This task's own tests (fail-fast)"
# ---------------------------------------------------------------------------

if out=$(go test ./internal/faults ./internal/errorscmd ./internal/schema/... 2>&1); then
    report_pass "go test: faults, errorscmd, schema"
else
    report_fail "go test: faults, errorscmd, schema" "exit 0" "$(printf '%s' "${out}" | tail -30)"
    summary
fi

if out=$(go test ./internal/monitor -run '^TestResolveFaultRaiser$' -v 2>&1); then
    report_pass "go test: monitor resolver policy"
else
    report_fail "go test: monitor resolver policy" "exit 0" "$(printf '%s' "${out}" | tail -30)"
    summary
fi
for t in "an_agent-run_fault_records_the_session_and_its_task" \
         "a_person-run_fault_records_no_session,_even_with_esu's_variable_set" \
         "an_explicit_task_overrides_the_session's" \
         "the_worktree_fills_the_task_when_there_is_no_session"; do
    assert_contains "resolver case runs and passes: ${t}" \
        "--- PASS: TestResolveFaultRaiser/${t}" "${out}"
done

for t in TestRecord_AttributesTheResolvedRaiser \
         TestRecord_ExplicitRaiserOverridesTheResolvedOne \
         TestRecord_ASecondRaiserUpdatesTheLatestAndAddsASource \
         TestRecord_ARepeatFromTheSameRaiserOnlyBumpsItsRow \
         TestRecord_UnattributedOccurrencesShareOneSource; do
    if go test ./internal/faults -run "^${t}$" -v 2>&1 | grep -q "^--- PASS: ${t}"; then
        report_pass "contract test runs and passes: ${t}"
    else
        report_fail "contract test runs and passes: ${t}" "--- PASS: ${t}" "no PASS line"
    fi
done

for t in TestListingLines_CarryTheBYColumn TestShow_ListsEveryRaiserAndEachOccurrencesOwn; do
    if go test ./internal/errorscmd -run "^${t}$" -v 2>&1 | grep -q "^--- PASS: ${t}"; then
        report_pass "contract test runs and passes: ${t}"
    else
        report_fail "contract test runs and passes: ${t}" "--- PASS: ${t}" "no PASS line"
    fi
done

if out=$(uv run pytest tests/test_rater.py -q -k report_failure_names_the_task 2>&1); then
    report_pass "pytest: the rater names the task it failed to rate"
else
    report_fail "pytest: the rater names the task it failed to rate" "exit 0" "$(printf '%s' "${out}" | tail -20)"
fi

# ---------------------------------------------------------------------------
section "B. Agent and person raisers, end to end"
# ---------------------------------------------------------------------------
# Built from this tree, against an explicit --db-dir, so the probe touches
# neither the real record nor this worktree's sandbox.

BIN="${TMP}/endless-go"
go build -o "${BIN}" ./cmd/endless-go \
    || setup_error "cannot build cmd/endless-go from this tree"

PROBE="${TMP}/config"
mkdir -p "${PROBE}"
GO=("${BIN}" --db-dir "${PROBE}")

# person / agent run endless-go with the agent-detection variables removed or
# set. Every variable agentenv reads is cleared first, so this suite answers the
# same whether or not an agent is running it.
person() {
    env -u CLAUDE_CODE_ENTRYPOINT -u __CFBundleIdentifier -u CLAUDECODE \
        -u CLAUDE_CODE_SESSION_ID -u ENDLESS_SESSION_ID "$@"
}
agent() {
    person CLAUDE_CODE_ENTRYPOINT=cli "$@"
}

# A first raise creates and migrates the probe database; the seed needs it. It
# runs from outside any worktree with no agent, so it is the unattributed case —
# from this worktree it would correctly name E-2268.
(cd "${TMP}" && person "${GO[@]}" errors raise --summary "e-2268 primer") >"${TMP}/primer.txt" 2>&1 \
    || setup_error "errors raise (primer) failed: $(head -3 "${TMP}/primer.txt")"

command -v sqlite3 >/dev/null 2>&1 || setup_error "sqlite3 is required to seed the probe"
sqlite3 "${PROBE}/endless.db" <<'SQL' || setup_error "cannot seed the probe database"
INSERT INTO projects (id, name, path) VALUES (900, 'e-2268-probe', '/tmp/e-2268-probe');
INSERT INTO tasks (id, project_id, title, status, type_id) VALUES (40, 900, 't', 'underway', 1);
INSERT INTO sessions (id, session_id, project_id, task_id, platform, state, started_at, last_activity)
VALUES (7, 'e2268-claude-uuid', 900, 40, 'claude', 'working', '2026-10-07T00:00:00', '2026-10-07T00:00:00');
SQL

agent CLAUDE_CODE_SESSION_ID=e2268-claude-uuid "${GO[@]}" errors raise --summary "e-2268 probe" \
    >"${TMP}/raise-agent.txt" 2>&1 \
    || setup_error "errors raise (agent) failed: $(head -3 "${TMP}/raise-agent.txt")"
ID=$(grep -oE 'as error [0-9]+' "${TMP}/raise-agent.txt" | grep -oE '[0-9]+$')
[[ -n "${ID}" ]] || setup_error "errors raise did not report an incident id"

row() { sqlite3 "${PROBE}/endless.db" "SELECT coalesce(task_id,'-') || '/' || coalesce(session_id,'-') FROM errors WHERE id = ${ID}"; }
assert_eq "an agent-run fault records ES-7 and its task E-40" "40/7" "$(row)"

# The person runs from inside a task worktree, with esu's variable exported and
# naming the same session — which must NOT be recorded.
WTDIR="${TMP}/proj/.endless/worktrees/e-55"
mkdir -p "${WTDIR}"
(cd "${WTDIR}" && person ENDLESS_SESSION_ID=7 "${GO[@]}" errors raise --summary "e-2268 probe") \
    >"${TMP}/raise-person.txt" 2>&1 \
    || setup_error "errors raise (person) failed: $(head -3 "${TMP}/raise-person.txt")"
PID=$(grep -oE 'as error [0-9]+' "${TMP}/raise-person.txt" | grep -oE '[0-9]+$')

assert_eq "the person's repeat lands on the same incident" "${ID}" "${PID}"
assert_eq "a person-run fault names the worktree's task and no session" "55/-" "$(row)"
assert_eq "the incident has two distinct raisers" "2" \
    "$(sqlite3 "${PROBE}/endless.db" "SELECT count(*) FROM errors_sources WHERE error_id = ${ID}")"

LIST="$(person "${GO[@]}" errors list --all-projects 2>&1)"
assert_contains "errors list has a BY column" "BY" "$(printf '%s\n' "${LIST}" | grep -F 'CODE')"
assert_contains "errors list names the latest raiser and one other" "E-55 +1" \
    "$(printf '%s\n' "${LIST}" | grep -F 'e-2268 probe')"
assert_contains "an unattributed incident reads -" " - " \
    "$(printf '%s\n' "${LIST}" | grep -F 'e-2268 primer')"

# ---------------------------------------------------------------------------
section "C. errors show names every raiser"
# ---------------------------------------------------------------------------

SHOW="$(person "${GO[@]}" errors show "${ID}" --detail 2>&1)"
assert_contains "show has a Raised by block" "Raised by:" "${SHOW}"
assert_contains "it names the agent's session and task" "ES-7 (E-40)  1 occurrence(s)" "${SHOW}"
assert_contains "it names the person's task" "E-55" "${SHOW}"
assert_contains "--detail names occurrence 1's raiser" "occurrence 1, raised by ES-7 (E-40)" "${SHOW}"
assert_contains "--detail names occurrence 2's raiser" "occurrence 2, raised by E-55" "${SHOW}"

# ---------------------------------------------------------------------------
section "D. The migrated schema"
# ---------------------------------------------------------------------------

COLS="$(sqlite3 "${PROBE}/endless.db" "SELECT group_concat(name, ',') FROM pragma_table_info('errors')")"
assert_contains "errors.task_id exists" "task_id" "${COLS}"
assert_contains "errors.session_id exists" "session_id" "${COLS}"
assert_eq "errors_sources and its unique index exist" "2" \
    "$(sqlite3 "${PROBE}/endless.db" "SELECT count(*) FROM sqlite_master WHERE name IN ('errors_sources','idx_errors_sources_uniq')")"

summary

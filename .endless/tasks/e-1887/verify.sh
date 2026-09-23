#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1887 and records what was true when E-1887
# landed. Edit it only if you ARE E-1887. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1887: a Claude hook that fails reports it to somebody.
#
# It reported to nobody. hook.Run logged the error and exited with a code
# chosen so a broken hook never blocks a tool call, and Claude Code discards
# hook stderr — so the failure was invisible BY CONSTRUCTION. Session ES-1055
# ran that way for four weeks: every PreToolUse died inside TouchSession with
# "table sessions has no column named process", `sessions.process_id` stayed
# NULL, and the only symptom was a blank tmux status bar, which is also what a
# pane with no task looks like.
#
# What is verified here:
#   A. Fail-fast: this task's own Go tests, and each contract test by NAME so
#      deleting one cannot turn the section green by absence.
#   B. A hook whose database write fails records ONE fault, with the code that
#      names what failed, carrying the context a reader needs — end to end,
#      through a binary built from this tree against a throwaway database.
#   C. Recording did not buy that visibility by trading away the non-blocking
#      contract: the exit code a failure produces is exactly E-1661's.
#   D. Repeats collapse. One stale binary failing on every event in every pane
#      is ONE incident with a rising count, not one per pane.
#   E. Different causes get different codes — the whole reason there are four.
#   F. A hook that SUCCEEDS records nothing. A diagnostic that fires on success
#      is noise, and noise is what gets an alert ignored.
#   G. The fault reaches the surface a user actually watches: the fault row on
#      `session status`.
#   H. The fallback sink. A fault raised BECAUSE the database failed cannot be
#      indexed in it, and used to be written nowhere at all. It now lands in
#      errors.jsonl marked unindexed, `errors list` prints it with the table
#      unreadable, the fault row says so in one line, and `errors clear`
#      silences it for good.
#   I. A healthy database behaves exactly as it did before — no notice, no
#      extra line, nothing added to the log beyond the indexed one.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || setup_error "not inside a git worktree"
cd "${WT}" || setup_error "cannot cd to worktree root ${WT}"

TMP="$(mktemp -d)" || setup_error "cannot create a scratch directory"
trap 'rm -rf "${TMP}"' EXIT

# ---------------------------------------------------------------------------
section "A. This task's own tests (fail-fast)"
# ---------------------------------------------------------------------------
# hookcmd holds the classification and the recording site. faults holds the
# fallback sink and the clear watermark. faultrow holds the one-line notice.
# errorscmd holds the `list`/`clear` surfaces over the unindexed entries.

if out=$(go test ./internal/hookcmd ./internal/faults ./internal/faultrow ./internal/errorscmd 2>&1); then
    report_pass "go test: hookcmd, faults, faultrow, errorscmd"
else
    report_fail "go test: hookcmd, faults, faultrow, errorscmd" "exit 0" "$(printf '%s' "${out}" | tail -40)"
    summary
fi

# Named individually so that deleting a contract test cannot make this section
# pass by having nothing left to run.
check_named() {
    local pkg="$1" t="$2"
    if go test "${pkg}" -run "^${t}$" -v 2>&1 | grep -q "^--- PASS: ${t}"; then
        report_pass "contract test runs and passes: ${t}"
    else
        report_fail "contract test runs and passes: ${t}" "--- PASS: ${t}" "no PASS line"
    fi
}

check_named ./internal/hookcmd TestFaultCodeFor_ClassifiesAtTheRaiseSite
check_named ./internal/hookcmd TestRecordHookFault_RecordsOneIncidentWithTheExpectedCode
check_named ./internal/hookcmd TestRecordHookFault_RepeatsCollapseIntoOneIncident
check_named ./internal/hookcmd TestRecordHookFault_LeavesTheExitCodeAlone
check_named ./internal/hookcmd TestRecordHookFault_RecordsWhenTheDatabaseIsTheThingThatFailed
check_named ./internal/faults TestRecord_WritesTheLogLineWhenTheIndexWriteFails
check_named ./internal/faults TestRecord_HealthyDatabaseIsUnchanged
check_named ./internal/faults TestClearUnindexed_MovesTheWatermarkAndItPersists
check_named ./internal/faults TestUnindexed_ATruncatedLogResetsTheWatermark
check_named ./internal/faults TestUnindexed_WatermarkHoldsAcrossALogThatOutgrowsTheDigestPrefix
check_named ./internal/faultrow TestRender_NoticeWhenTheStoreCannotBeRead
check_named ./internal/faultrow TestRender_SilentWhenTheStoreIsUnbound

# The Python half of `clear --log`. The Go/Python parity test compares
# SUBCOMMANDS, so a flag added to an existing verb passes it while being
# unshipped — which is exactly how `errors raise` went unreachable for months.
if uv run pytest tests/test_go_cli_parity.py -q >"${TMP}/parity.txt" 2>&1; then
    report_pass "pytest: the Go/Python CLI parity suite, --log included"
else
    report_fail "pytest: the Go/Python CLI parity suite, --log included" \
        "exit 0" "$(tail -20 "${TMP}/parity.txt")"
fi

# ---------------------------------------------------------------------------
# The fixture.
# ---------------------------------------------------------------------------
# Built from this tree, not taken from bin/, so the answers come from what is
# committed here rather than from whatever was last installed.
#
# --db-dir at a throwaway path keeps every write out of the real record AND out
# of this worktree's sandbox. It is also what stops the `hook` subcommand
# pinning the main database, which it does whenever no explicit DB context is
# given (E-1450/E-1429) — without it this suite would write hook traffic and
# fault rows into the developer's live database.

command -v sqlite3 >/dev/null 2>&1 || setup_error "sqlite3 is required to build the failing-write fixture"

BIN="${TMP}/endless-go"
go build -o "${BIN}" ./cmd/endless-go \
    || setup_error "cannot build cmd/endless-go from this tree"

CFG="${TMP}/config"
PROJ="${TMP}/proj"
mkdir -p "${CFG}" "${PROJ}"
GO=("${BIN}" --db-dir "${CFG}")

# A real git repository, because the hook resolves a project from its cwd and
# derives a worktree root from it.
git -C "${PROJ}" init -q . || setup_error "cannot init the probe repository"
: >"${PROJ}/README"
git -C "${PROJ}" add -A >/dev/null 2>&1
git -C "${PROJ}" -c user.email=verify@endless -c user.name=verify commit -qm init >/dev/null 2>&1 \
    || setup_error "cannot commit in the probe repository"

# fire <event> <session> [pane] — one Claude hook event, as the harness sends
# it. Prints the exit code; the hook's own output goes to a file.
#
# CLAUDE_CODE_ENTRYPOINT=cli is load-bearing: on an unsupported harness the
# whole hook is a deliberate no-op before stdin is read (E-1962), so without it
# this suite would verify nothing while passing.
fire() {
    local event="$1" session="$2" pane="${3:-%1}"
    CLAUDE_CODE_ENTRYPOINT=cli TMUX_PANE="${pane}" \
        "${GO[@]}" hook claude \
        >"${TMP}/hook-out.txt" 2>"${TMP}/hook-err.txt" \
        <<<"{\"session_id\":\"${session}\",\"cwd\":\"${PROJ}\",\"hook_event_name\":\"${event}\",\"tool_name\":\"Read\"}"
    printf '%s' "$?"
}

# One healthy event first: it registers the project and proves the fixture can
# succeed, which is what makes a later failure attributable to the break rather
# than to the setup.
SETUP_EXIT="$(cd "${PROJ}" && fire PreToolUse e1887-setup)"
[[ "${SETUP_EXIT}" == "0" ]] \
    || setup_error "a healthy hook event exited ${SETUP_EXIT}: $(head -3 "${TMP}/hook-err.txt")"

# ---------------------------------------------------------------------------
section "F. A hook that succeeds records nothing"
# ---------------------------------------------------------------------------
# Asserted BEFORE the break, against the event that just ran. A recorder that
# fires on success would make every one of the assertions below meaningless and
# would train a user to ignore the row.

assert_eq "a successful hook event opens no incident" \
    "0" "$(sqlite3 "${CFG}/endless.db" 'SELECT count(*) FROM errors')"

# ---------------------------------------------------------------------------
# Break the WRITE, and only the write.
# ---------------------------------------------------------------------------
# Triggers that abort any write to `sessions`, which is surgical in the way
# this needs: the hook's own write fails exactly as ES-1055's did, while the
# `errors` table stays perfectly writable — so the fault has somewhere to land
# and we are testing the recorder rather than the database.
#
# A trigger rather than a dropped column because migration would put a column
# back on the next open, and a fixture the code under test repairs is not a
# fixture.
sqlite3 "${CFG}/endless.db" <<'SQL' || setup_error "cannot break the sessions table"
CREATE TRIGGER e1887_no_insert BEFORE INSERT ON sessions
BEGIN SELECT RAISE(ABORT, 'e-1887 fixture: sessions is not writable'); END;
CREATE TRIGGER e1887_no_update BEFORE UPDATE ON sessions
BEGIN SELECT RAISE(ABORT, 'e-1887 fixture: sessions is not writable'); END;
SQL

# ---------------------------------------------------------------------------
section "B. The failing write is recorded"
# ---------------------------------------------------------------------------

FAIL_EXIT="$(cd "${PROJ}" && fire PreToolUse e1887-write)"

assert_eq "the failing write opens exactly one incident" \
    "1" "$(sqlite3 "${CFG}/endless.db" 'SELECT count(*) FROM errors')"

assert_eq "it carries the code for a failed hook WRITE" \
    "ERR-0015" "$(sqlite3 "${CFG}/endless.db" 'SELECT code FROM errors')"

assert_eq "at error severity, from the code and not the call site" \
    "error" "$(sqlite3 "${CFG}/endless.db" 'SELECT severity FROM errors')"

assert_eq "attributed to the hook that raised it" \
    "hook:claude" "$(sqlite3 "${CFG}/endless.db" 'SELECT source FROM errors')"

SUMMARY="$(sqlite3 "${CFG}/endless.db" 'SELECT summary FROM errors')"
assert_contains "the summary names the operation that failed" \
    "touching session" "${SUMMARY}"
assert_contains "and carries the underlying database error" \
    "sessions is not writable" "${SUMMARY}"

# The detail log is where a diagnosis happens, and the four fields below are
# the ones the ES-1055 incident was actually solved by — above all `binary`,
# since "which endless-go wrote this" was the entire answer there.
DETAIL="$("${GO[@]}" errors show 1 --detail 2>&1)"
assert_contains "the detail names the event that fired" "hook_event: PreToolUse" "${DETAIL}"
assert_contains "the detail names the session" "session_id: e1887-write" "${DETAIL}"
assert_contains "the detail names the pane" "pane: %1" "${DETAIL}"
assert_contains "the detail names the binary that ran" "binary: ${BIN}" "${DETAIL}"

# A code is not shipped until a reader can be told what to do about it.
assert_contains 'and errors show prints the remedy for it' "What to do:" "${DETAIL}"

# ---------------------------------------------------------------------------
section "C. The exit-code contract is untouched"
# ---------------------------------------------------------------------------
# This is the check that matters most, because the failure mode it guards
# against is worse than the bug being fixed: a hook that starts blocking tool
# calls on a database hiccup would take a session down rather than annotate it.
#
# The codes are E-1661's, not "always 0" — PreToolUse and PostToolUse are the
# two events whose stderr reaches the model, so a failure there is graded
# blocking on purpose, while Stop keeps the non-blocking exit because exit 2 on
# Stop starts a turn-per-iteration loop only Esc can leave. Recording changed
# neither.

assert_eq "a PreToolUse failure still exits 2 (the agent is told)" \
    "2" "${FAIL_EXIT}"

assert_eq "a Stop failure still exits non-blocking (no turn loop)" \
    "1" "$(cd "${PROJ}" && fire Stop e1887-stop)"

assert_eq "a PostToolUse failure still exits 2" \
    "2" "$(cd "${PROJ}" && fire PostToolUse e1887-post)"

# ---------------------------------------------------------------------------
section "D. Repeats collapse into one incident"
# ---------------------------------------------------------------------------
# The whole shape of the ES-1055 outage: one stale binary, every event, every
# pane, four weeks. Fingerprinted by session or pane this would be an incident
# per session and the shared cause would be buried under them.

BEFORE="$(sqlite3 "${CFG}/endless.db" 'SELECT occurrences FROM errors WHERE code = "ERR-0015"')"
for pane in %11 %12 %13; do
    for i in 1 2 3 4 5 6 7; do
        (cd "${PROJ}" && fire PreToolUse "e1887-rep-${pane}-${i}" "${pane}") >/dev/null
    done
done

assert_eq "twenty-one further failures across three panes are still ONE incident" \
    "1" "$(sqlite3 "${CFG}/endless.db" 'SELECT count(*) FROM errors WHERE code = "ERR-0015"')"

assert_eq "and the occurrence count rose by twenty-one" \
    "$((BEFORE + 21))" \
    "$(sqlite3 "${CFG}/endless.db" 'SELECT occurrences FROM errors WHERE code = "ERR-0015"')"

# ---------------------------------------------------------------------------
section "E. Different causes get different codes"
# ---------------------------------------------------------------------------
# The reason there are four codes and not one. A report that says only "a hook
# failed" has told its reader nothing they can act on, and a malformed harness
# payload has no remedy in common with a schema-drifted database.

CLAUDE_CODE_ENTRYPOINT=cli "${GO[@]}" hook claude >/dev/null 2>&1 <<<'this is not json'

assert_eq "an unreadable event envelope raises the payload code, not the write code" \
    "ERR-0017" \
    "$(sqlite3 "${CFG}/endless.db" 'SELECT code FROM errors WHERE code = "ERR-0017"')"

assert_eq "the two are separate incidents" \
    "2" "$(sqlite3 "${CFG}/endless.db" 'SELECT count(*) FROM errors')"

# ---------------------------------------------------------------------------
section "G. It reaches the surface a user watches"
# ---------------------------------------------------------------------------
# `errors list` is where someone who already suspects a problem looks. The
# fault row is what makes them suspect one — and its absence is exactly what
# made the four-week outage survive.

render() {
    "${GO[@]}" session-status --task 1887 --cols 140 2>/dev/null \
        | sed 's/\x1b\[[0-9;]*m//g' \
        | grep -F 'Run eeh' || true
}

ROW="$(render)"
assert_contains "the fault row renders for a hook failure" "Run eeh" "${ROW}"
assert_contains "and names the codes it is reporting" "ERR-0015" "${ROW}"

# ---------------------------------------------------------------------------
section "H. When the database is what failed"
# ---------------------------------------------------------------------------
# The case the fallback sink exists for, and the one that cannot be served by
# the table: a fault raised BECAUSE the database is unreachable has no row to
# be recorded in. Until this task it was written nowhere at all.

LOG="${CFG}/log/errors.jsonl"
[[ -f "${LOG}" ]] || setup_error "no detail log at ${LOG}"

"${GO[@]}" errors clear >/dev/null 2>&1 \
    || setup_error "cannot clear the incidents raised above"

assert_eq "nothing is outstanding before the database is broken" \
    "" "$(render)"

LINES_BEFORE="$(wc -l <"${LOG}" | tr -d ' ')"

# Replace the database with bytes SQLite cannot open. Not a chmod: a
# permission error and a corrupt file take different paths inside the driver,
# and this is the one a half-written or truncated database produces.
#
# Checkpointed and kept, because the last assertions in this section restore it
# — proving the notice went away because the watermark moved and not because
# the log was emptied.
sqlite3 "${CFG}/endless.db" 'PRAGMA wal_checkpoint(TRUNCATE)' >/dev/null 2>&1
cp "${CFG}/endless.db" "${TMP}/endless.db.good" || setup_error "cannot snapshot the probe database"
rm -f "${CFG}/endless.db-wal" "${CFG}/endless.db-shm"
printf 'this is not a database' >"${CFG}/endless.db"

(cd "${PROJ}" && fire Stop e1887-nodb) >/dev/null

assert_eq "a failing hook still appends to the log with no database at all" \
    "$((LINES_BEFORE + 1))" "$(wc -l <"${LOG}" | tr -d ' ')"

LAST="$(tail -1 "${LOG}")"
assert_contains "the line is marked unindexed" '"unindexed":true' "${LAST}"
assert_contains "and carries a null id rather than an invented one" '"fault_id":null' "${LAST}"
assert_contains "and says why it could not be indexed" '"index_error"' "${LAST}"
assert_contains "and names the session it happened in" "e1887-nodb" "${LAST}"

# The read surfaces, in the state where the table is unreachable.
LIST="$("${GO[@]}" errors list 2>&1)"
assert_contains 'errors list prints it with the table unreadable' \
    "never indexed" "${LIST}"
assert_contains "and says plainly that it has no id to address" \
    "no id" "${LIST}"

NOTICE="$(render)"
assert_contains "the fault row says the record could not be read" \
    "could not be read" "${NOTICE}"
assert_contains "and counts what is waiting in the log" "1 in the log" "${NOTICE}"
assert_eq "in ONE line — a broken database must not take the pane over" \
    "1" "$(printf '%s\n' "${NOTICE}" | grep -c 'Run eeh')"

# Dismissal has to work in this state too, or it is a notice a user learns to
# ignore — and `--log` is the spelling that says so without also requiring a
# table clear that CANNOT succeed here.
LOGCLEAR="$("${GO[@]}" errors clear --log 2>&1)"
LOGCLEAR_EXIT=$?

assert_eq "clear --log succeeds with no readable database at all" "0" "${LOGCLEAR_EXIT}"
assert_contains "and says what it dismissed" "waiting in the log" "${LOGCLEAR}"

# The two halves of the notice are dismissed differently, and that is the
# design rather than a gap. A BACKLOG of reports is something a person can say
# they have seen. An unreadable database is a live condition, still true after
# the acknowledgement, and a diagnostic that let someone dismiss an ongoing
# failure would be worse than one that never fired.
CLEARED="$(render)"
assert_not_contains "clearing dismisses the backlog" "in the log" "${CLEARED}"
assert_contains "and does NOT dismiss the live failure underneath it" \
    "could not be read" "${CLEARED}"

# Restoring the database removes the live condition. The unindexed line is
# still in the log — nothing deleted it — so a row that comes back now would
# mean the watermark had not held.
cp "${TMP}/endless.db.good" "${CFG}/endless.db" || setup_error "cannot restore the probe database"

assert_contains "the unindexed line is still in the log, undeleted" \
    '"unindexed":true' "$(cat "${LOG}")"

assert_eq "yet with the database readable again the notice is gone" "" "$(render)"

assert_eq "and it stays gone on a later run — the watermark is a file, not a session" \
    "" "$(render)"

# --log dismisses ONE half. A user working through open rows must not lose them
# by acknowledging the log, and an id names a row the log does not have.
"${GO[@]}" errors raise --severity error --summary "e-1887 row that must survive --log" >/dev/null 2>&1 \
    || setup_error "cannot raise a row for the --log isolation check"
"${GO[@]}" errors clear --log >/dev/null 2>&1 || true

assert_eq "clear --log leaves every open row exactly as it was" \
    "1" "$(sqlite3 "${CFG}/endless.db" 'SELECT count(*) FROM errors WHERE cleared_at IS NULL')"

"${GO[@]}" errors clear --log 7 >"${TMP}/logid.txt" 2>&1
assert_eq "clear --log with an id is refused rather than guessed at" "2" "$?"
assert_contains "and says why the two cannot be combined" \
    "holds no ids" "$(cat "${TMP}/logid.txt")"

"${GO[@]}" errors clear >/dev/null 2>&1 || true

# ---------------------------------------------------------------------------
section "I. A healthy database is unchanged"
# ---------------------------------------------------------------------------
# The fallback must be invisible when nothing is wrong. A diagnostics surface
# that adds a line to a healthy pane has made itself the problem.

CLEAN="${TMP}/clean"
CLEAN_PROJ="${TMP}/cleanproj"
mkdir -p "${CLEAN}" "${CLEAN_PROJ}"
git -C "${CLEAN_PROJ}" init -q .
: >"${CLEAN_PROJ}/README"
git -C "${CLEAN_PROJ}" add -A >/dev/null 2>&1
git -C "${CLEAN_PROJ}" -c user.email=verify@endless -c user.name=verify commit -qm init >/dev/null 2>&1

CLEAN_EXIT=$(CLAUDE_CODE_ENTRYPOINT=cli TMUX_PANE=%1 \
    "${BIN}" --db-dir "${CLEAN}" hook claude >/dev/null 2>&1 \
    <<<"{\"session_id\":\"e1887-clean\",\"cwd\":\"${CLEAN_PROJ}\",\"hook_event_name\":\"PreToolUse\",\"tool_name\":\"Read\"}"; printf '%s' "$?")

assert_eq "a healthy hook event succeeds" "0" "${CLEAN_EXIT}"

assert_eq "it records no fault" \
    "0" "$(sqlite3 "${CLEAN}/endless.db" 'SELECT count(*) FROM errors')"

CLEAN_LOG="${CLEAN}/log/errors.jsonl"
CLEAN_LINES=0
[[ -f "${CLEAN_LOG}" ]] && CLEAN_LINES="$(wc -l <"${CLEAN_LOG}" | tr -d ' ')"
assert_eq "it writes no line to the detail log" "0" "${CLEAN_LINES}"

assert_eq "and the status view renders no fault row at all" \
    "" "$("${BIN}" --db-dir "${CLEAN}" session-status --task 1887 --cols 140 2>/dev/null \
            | sed 's/\x1b\[[0-9;]*m//g' | grep -F 'Run eeh' || true)"

summary

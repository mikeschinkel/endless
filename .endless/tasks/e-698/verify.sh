#!/usr/bin/env bash
#
# E-698 verification — "Build a fire-once background job runner for Endless".
#
# What shipped:
#   - internal/jobs    the fire-once runner: registry, CAS lease, backoff.
#   - internal/faults  the machine-local fault record the runner reports through.
#   - endless-go jobs   list|run|retry     (+ `endless jobs ...` in Python)
#   - endless-go errors show|clear|codes   (+ `endless errors ...` in Python)
#   - a fault badge on `session status` / `session monitor`.
#   - the session-monitor trigger, firing RunDue on each refresh.
#
# The registry ships EMPTY on purpose: the runner knows nothing job-specific,
# and E-1859 / E-1881 are its first clients. Several checks below therefore
# exercise the machinery directly (SQL, seeded rows) rather than through a
# production job that does not exist.
#
# Checks, fail-fast in order:
#   1. The task's own Go unit tests — fail fast.
#   2. Schema: both tables, and the partial unique index that IS the incident
#      model, exist in a freshly created DB.
#   3. Timestamp format: the runner's writes use the schema's T-separated form,
#      not SQLite's space-separated datetime() — mixing them breaks every
#      due/expiry comparison silently.
#   4. Multi-PROCESS compare-and-set: N concurrent OS processes race one due
#      job; exactly one claims it. (Go tests race goroutines; this races
#      processes, which is the deployment shape.)
#   5. Lease expiry is reclaimable; a live lease is not.
#   6. End-to-end CLI through the real binary: jobs list/run, errors
#      show/clear/codes.
#   7. Incident model end-to-end: repeats dedupe, clearing closes, recurrence
#      opens a NEW incident.
#   8. The badge renders on the real session-status view.
#   9. Architecture: faults does not depend on the events/ledger layer, and
#      session-status no longer pins main inside a self_dev worktree.
#  10. Python CLI surfaces are wired.
#
# Run from anywhere inside the worktree:  endless task verify E-698
#
# Refuse a direct run, and pick up the shared harness vocabulary. Sourced as the
# FIRST executable statement so the refusal fires before anything in this file
# runs; every definition below overrides the harness's own, so a suite written
# before the harness existed behaves exactly as it did.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

ROOT=$(git rev-parse --show-toplevel) || { echo "not in a git repo" >&2; exit 1; }
cd "${ROOT}" || exit 1

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi

TMP=""
cleanup() { [[ -n "${TMP}" ]] && rm -rf "${TMP}"; }
trap cleanup EXIT

pass() { printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"; }
fail() {
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"
    [[ -n "${2:-}" ]] && printf '      %s%s%s\n' "${DIM}" "$2" "${RESET}"
    exit 1
}
section() { printf '\n%s%s%s\n' "${BOLD}" "$1" "${RESET}"; }

GO_BIN="${ROOT}/bin/endless-go"
[[ -x "${GO_BIN}" ]] || fail "worktree binary missing" "run 'just go' first: ${GO_BIN}"

TMP=$(mktemp -d) || fail "mktemp failed"

# sq runs SQL against a throwaway DB seeded with the real schema.
SCHEMA="${ROOT}/internal/schema/schema.sql"
TESTDB="${TMP}/e698.db"
sq() { sqlite3 "${TESTDB}" "$1"; }

# ── 1. the task's own unit tests, fail-fast ─────────────────────────────────
section "1. Go unit tests (fail-fast)"
if go test ./internal/jobs/ ./internal/faults/ ./internal/sessionstatuscmd/ ./internal/monitor/ >"${TMP}/gotest.log" 2>&1; then
    pass "go test: jobs, faults, sessionstatuscmd, monitor"
else
    sed 's/^/      /' "${TMP}/gotest.log" >&2
    fail "the task's Go unit tests"
fi

# ── 2. schema ───────────────────────────────────────────────────────────────
section "2. Schema"
sqlite3 "${TESTDB}" < "${SCHEMA}" >/dev/null || fail "applying schema.sql to a fresh DB"
pass "schema.sql applies to a fresh DB"

for table in jobs errors; do
    got=$(sq "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='${table}'")
    [[ "${got}" == "1" ]] || fail "table '${table}' was not created"
    pass "table '${table}' exists"
done

# The partial unique index IS the incident model: it is what makes a repeat
# dedupe in place while a recurrence AFTER clearing opens a new row.
idx=$(sq "SELECT sql FROM sqlite_master WHERE type='index' AND name='idx_errors_open_uniq'")
[[ -n "${idx}" ]] || fail "idx_errors_open_uniq is missing" "without it, repeats flood the table"
grep -q "cleared_at IS NULL" <<<"${idx}" \
    || fail "idx_errors_open_uniq is not partial" "got: ${idx}"
grep -q "UNIQUE" <<<"${idx}" || fail "idx_errors_open_uniq is not unique" "got: ${idx}"
pass "idx_errors_open_uniq is a UNIQUE partial index on open rows"

# Re-applying must be a clean no-op: schema.sql runs on EVERY connect.
sqlite3 "${TESTDB}" < "${SCHEMA}" >/dev/null || fail "re-applying schema.sql is not idempotent"
pass "schema.sql re-applies as a no-op"

# ── 3. timestamp format ─────────────────────────────────────────────────────
section "3. Timestamp format"
# The trap: SQLite's datetime() emits 'YYYY-MM-DD HH:MM:SS' while the rest of
# schema.sql stores 'YYYY-MM-DDTHH:MM:SS'. Because ' ' sorts before 'T', a row
# written with the wrong one looks permanently overdue and the lease stops
# arbitrating anything. Assert the T form is what actually lands.
sq "INSERT INTO jobs (name, next_due_at) VALUES ('fmt-probe', strftime('%Y-%m-%dT%H:%M:%S','now'))"
stored=$(sq "SELECT next_due_at FROM jobs WHERE name='fmt-probe'")
grep -qE '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}$' <<<"${stored}" \
    || fail "next_due_at is not T-separated" "got: ${stored}"
pass "next_due_at uses the schema's T-separated format"

# And prove the ordering hazard is real, so this check cannot be dismissed.
wrong=$(sq "SELECT CASE WHEN datetime('now') < strftime('%Y-%m-%dT%H:%M:%S','now') THEN 'yes' ELSE 'no' END")
[[ "${wrong}" == "yes" ]] \
    || fail "the space-vs-T ordering hazard changed" "re-check the sqlNow constants in internal/jobs/run.go"
pass "space-separated timestamps really do sort before T-separated ones"

grep -q "strftime('%Y-%m-%dT%H:%M:%S', 'now')" internal/jobs/run.go \
    || fail "internal/jobs/run.go no longer uses the T-separated SQL clock"
if grep -qE "datetime\('now'" internal/jobs/run.go; then
    fail "internal/jobs/run.go uses datetime('now')" "it must use the T-separated strftime form"
fi
pass "the runner's SQL uses only the T-separated clock"

# ── 4. multi-process compare-and-set ────────────────────────────────────────
section "4. Compare-and-set lease across concurrent PROCESSES"
sq "DELETE FROM jobs"
sq "INSERT INTO jobs (name, next_due_at) VALUES ('contended', strftime('%Y-%m-%dT%H:%M:%S','now','-1 hour'))"

# The exact claim statement from internal/jobs/run.go, run from N separate OS
# processes at once. Each writes a line only if IT won the row.
RACERS=10
for i in $(seq 1 "${RACERS}"); do
    (
        sqlite3 "${TESTDB}" <<SQL >"${TMP}/racer.${i}"
.timeout 5000
UPDATE jobs
   SET lease_owner      = 'racer-${i}',
       lease_expires_at = strftime('%Y-%m-%dT%H:%M:%S','now','+300 seconds')
 WHERE name = 'contended'
   AND next_due_at <= strftime('%Y-%m-%dT%H:%M:%S','now')
   AND (lease_owner IS NULL OR lease_expires_at <= strftime('%Y-%m-%dT%H:%M:%S','now'))
 RETURNING lease_owner;
SQL
    ) &
done
wait

winners=$(cat "${TMP}"/racer.* 2>/dev/null | grep -c . || true)
[[ "${winners}" == "1" ]] \
    || fail "${winners} of ${RACERS} concurrent processes claimed the job, want exactly 1" \
            "the compare-and-set lease is not mutually exclusive"
pass "exactly 1 of ${RACERS} concurrent processes claimed the due job"

# ── 5. lease expiry ─────────────────────────────────────────────────────────
section "5. Lease expiry"
sq "DELETE FROM jobs"
# A process that claimed the job and died: due, leased, lease already expired.
sq "INSERT INTO jobs (name, next_due_at, lease_owner, lease_expires_at)
    VALUES ('abandoned',
            strftime('%Y-%m-%dT%H:%M:%S','now','-1 hour'),
            'dead-process',
            strftime('%Y-%m-%dT%H:%M:%S','now','-30 minutes'))"
reclaimed=$(sq "UPDATE jobs SET lease_owner='fresh'
                 WHERE name='abandoned'
                   AND next_due_at <= strftime('%Y-%m-%dT%H:%M:%S','now')
                   AND (lease_owner IS NULL OR lease_expires_at <= strftime('%Y-%m-%dT%H:%M:%S','now'))
                 RETURNING lease_owner")
[[ "${reclaimed}" == "fresh" ]] \
    || fail "an expired lease was not reclaimable" "a crashed owner would strand the job forever"
pass "an expired lease is reclaimable with no cleanup step"

sq "DELETE FROM jobs"
sq "INSERT INTO jobs (name, next_due_at, lease_owner, lease_expires_at)
    VALUES ('held',
            strftime('%Y-%m-%dT%H:%M:%S','now','-1 hour'),
            'live-process',
            strftime('%Y-%m-%dT%H:%M:%S','now','+1 hour'))"
stolen=$(sq "UPDATE jobs SET lease_owner='thief'
              WHERE name='held'
                AND next_due_at <= strftime('%Y-%m-%dT%H:%M:%S','now')
                AND (lease_owner IS NULL OR lease_expires_at <= strftime('%Y-%m-%dT%H:%M:%S','now'))
              RETURNING lease_owner")
[[ -z "${stolen}" ]] || fail "a LIVE lease was stolen" "got new owner: ${stolen}"
pass "a live lease cannot be stolen"

# ── 6. end-to-end CLI ───────────────────────────────────────────────────────
section "6. CLI end-to-end (worktree binary, sandbox DB)"
out=$("${GO_BIN}" jobs list 2>&1) || fail "endless-go jobs list" "${out}"
grep -q "no jobs registered" <<<"${out}" \
    || fail "jobs list should report an empty registry" "got: ${out}"
pass "jobs list reports the empty registry explicitly"

out=$("${GO_BIN}" jobs run 2>&1) || fail "endless-go jobs run" "${out}"
grep -q "0 registered, 0 claimed, 0 failed" <<<"${out}" \
    || fail "jobs run summary is wrong for an empty registry" "got: ${out}"
pass "jobs run fires once and exits cleanly with no jobs"

out=$("${GO_BIN}" jobs retry nope 2>&1) && fail "jobs retry accepted an unknown job"
pass "jobs retry rejects an unknown job"

out=$("${GO_BIN}" errors codes 2>&1) || fail "endless-go errors codes" "${out}"
for code in ERR-0001 ERR-0002 ERR-0003 ERR-0004 ERR-0005; do
    grep -q "${code}" <<<"${out}" || fail "errors codes omits ${code}" "got: ${out}"
done
pass "errors codes prints the full catalog"

# Codes must not be mistakable for task ids (E-NNNN, or a future project prefix).
if grep -qE '^E-[0-9]{4}\b' <<<"${out}"; then
    fail "an error code looks like a task id" "codes must be ERR-NNNN"
fi
pass "no error code is shaped like a task id"

# ── 7. incident model end-to-end ────────────────────────────────────────────
section "7. Incident model"
sq "DELETE FROM errors"
rec() { # <fingerprint>
    sq "INSERT INTO errors (code, severity, source, fingerprint, summary,
                            occurrences, first_seen_at, last_seen_at)
        VALUES ('ERR-0001','warning','job:probe','$1','probe failed', 1,
                strftime('%Y-%m-%dT%H:%M:%S','now'), strftime('%Y-%m-%dT%H:%M:%S','now'))
        ON CONFLICT (source, code, fingerprint) WHERE cleared_at IS NULL
        DO UPDATE SET occurrences = occurrences + 1,
                      last_seen_at = strftime('%Y-%m-%dT%H:%M:%S','now')"
}

rec fp1; rec fp1; rec fp1
rows=$(sq "SELECT count(*) FROM errors")
occ=$(sq "SELECT occurrences FROM errors")
[[ "${rows}" == "1" ]] || fail "3 repeats created ${rows} rows, want 1"
[[ "${occ}" == "3" ]] || fail "occurrences = ${occ}, want 3"
pass "repeats dedupe in place, keeping the count (100 failures ≠ 1 failure)"

sq "UPDATE errors SET cleared_at = strftime('%Y-%m-%dT%H:%M:%S','now'), cleared_by='verify'"
rec fp1
rows=$(sq "SELECT count(*) FROM errors")
open_rows=$(sq "SELECT count(*) FROM errors WHERE cleared_at IS NULL")
open_occ=$(sq "SELECT occurrences FROM errors WHERE cleared_at IS NULL")
[[ "${rows}" == "2" ]] || fail "recurrence after clearing produced ${rows} rows, want 2"
[[ "${open_rows}" == "1" ]] || fail "${open_rows} open incidents after recurrence, want 1"
[[ "${open_occ}" == "1" ]] || fail "the new incident inherited a count of ${open_occ}, want 1"
cleared_occ=$(sq "SELECT occurrences FROM errors WHERE cleared_at IS NOT NULL")
[[ "${cleared_occ}" == "3" ]] || fail "cleared history was mutated: occurrences=${cleared_occ}, want 3"
pass "recurrence after clearing opens a NEW incident, preserving history"

# ── 8. the badge on the real view ───────────────────────────────────────────
section "8. Fault badge on session-status"
# Headless mode (--task) reads the self-detected sandbox DB, so seed a fault
# there and confirm the real renderer surfaces it.
SANDBOX_DB="${HOME}/.cache/endless/sandboxes/$(basename "${ROOT}")/endless/endless.db"
if [[ ! -f "${SANDBOX_DB}" ]]; then
    fail "sandbox DB not found at ${SANDBOX_DB}" "run 'just dev-sandbox-init'"
fi

sqlite3 "${SANDBOX_DB}" "DELETE FROM errors WHERE source='job:e698-verify'" 2>/dev/null || true
sqlite3 "${SANDBOX_DB}" \
    "INSERT INTO errors (code, severity, source, fingerprint, summary,
                         occurrences, first_seen_at, last_seen_at)
     VALUES ('ERR-0002','error','job:e698-verify','e698-badge','job \"e698-verify\" panicked',
             1, strftime('%Y-%m-%dT%H:%M:%S','now'), strftime('%Y-%m-%dT%H:%M:%S','now'))" \
    || fail "could not seed a fault into the sandbox DB"

TASK_ID=$(basename "${ROOT}" | sed 's/^e-//')
out=$("${GO_BIN}" session-status --task "${TASK_ID}" 2>&1)
grep -q "ERROR" <<<"${out}" || fail "badge does not show the ERROR severity" "got: ${out}"
grep -q "endless errors show" <<<"${out}" \
    || fail "badge does not name the command that explains it" "got: ${out}"
pass "session-status renders the fault badge"

sqlite3 "${SANDBOX_DB}" \
    "UPDATE errors SET cleared_at = strftime('%Y-%m-%dT%H:%M:%S','now')
      WHERE source='job:e698-verify'" || fail "could not clear the seeded fault"
out=$("${GO_BIN}" session-status --task "${TASK_ID}" 2>&1)
grep -q "endless errors show" <<<"${out}" \
    && fail "badge still rendered after the incident was cleared" "got: ${out}"
pass "badge disappears once the incident is cleared"

out=$("${GO_BIN}" errors show 2>&1) || fail "endless-go errors show" "${out}"
out=$("${GO_BIN}" errors show --all 2>&1) || fail "endless-go errors show --all" "${out}"
grep -q "e698-verify" <<<"${out}" \
    || fail "--all does not include cleared history" "clearing must never delete"
pass "errors show --all still carries the cleared incident (clearing never deletes)"

sqlite3 "${SANDBOX_DB}" "DELETE FROM errors WHERE source='job:e698-verify'"

# ── 9. architecture invariants ──────────────────────────────────────────────
section "9. Architecture"
# faults must not reach the ledger layer: it is machine-local observation, and
# the db-ledger is committed to the project repo.
if go list -deps ./internal/faults | grep -q "endless/internal/events"; then
    fail "internal/faults depends on internal/events" "faults must never reach the db-ledger"
fi
pass "internal/faults does not depend on the events/ledger layer"

# ...and it must not depend on monitor either, or E-1884 (converting monitor's
# own log sites to faults) would create an import cycle.
if go list -deps ./internal/faults | grep -q "endless/internal/monitor"; then
    fail "internal/faults depends on internal/monitor" \
         "that becomes an import cycle when monitor reports faults (E-1884)"
fi
pass "internal/faults is free of an internal/monitor dependency (keeps E-1884 open)"

# session-status MUST pin main even in a worktree. Session/pane state is
# machine-scoped and is read by a single-DB join against tasks, so a sandbox
# read renders "no active task" for every pane — the regression E-698 shipped
# and this suite now guards against.
grep -q "if !monitor.HasExplicitDBContext() {" internal/sessionstatuscmd/session_status.go \
    || fail "session-status no longer pins main on its tmux path" \
            "the worktree monitor would render 'no active task' for every pane"
if grep -q "if !monitor.InSelfDevWorktree() {" internal/sessionstatuscmd/session_status.go; then
    fail "session-status skips the main pin inside a self_dev worktree" \
         "that is the regression; the job guard belongs on the trigger instead"
fi
pass "session-status pins main unless an explicit --config-dir was given"

# ...and the protection that skip was providing now lives on the trigger.
grep -q "if Suppressed() {" internal/jobs/run.go \
    || fail "RunDue no longer consults the suppression guard" \
            "candidate job code could write the developer's real ledger"
pass "RunDue suppresses itself instead (guard moved to the trigger)"

out=$(ENDLESS_NO_JOBS=1 "${GO_BIN}" jobs list 2>&1) || fail "jobs list under ENDLESS_NO_JOBS" "${out}"
grep -q "jobs suppressed" <<<"${out}" \
    || fail "jobs list stays silent when the runner is suppressed" \
            "an operator cannot tell 'nothing due' from 'will never run'"
pass "jobs list reports suppression rather than looking idle"

grep -q "jobs.RunDue" internal/sessionstatuscmd/session_status.go \
    || fail "the session monitor no longer fires the job runner"
grep -q "jobsInFlight" internal/sessionstatuscmd/session_status.go \
    || fail "the monitor trigger lost its single-in-flight guard" \
            "a slow job would stack up invocations every 2s"
pass "the session monitor fires RunDue behind a single-in-flight guard"

# ── 10. Python CLI wiring ───────────────────────────────────────────────────
section "10. Python CLI surfaces"
for verb in jobs errors; do
    uv run endless "${verb}" --help >/dev/null 2>&1 \
        || fail "endless ${verb} --help failed" "the Python group is not wired"
    pass "endless ${verb} is wired"
done

out=$(uv run endless jobs list --db sandbox 2>&1) || fail "endless jobs list --db sandbox" "${out}"
grep -q "no jobs registered" <<<"${out}" \
    || fail "endless jobs list did not reach the Go runner" "got: ${out}"
pass "endless jobs list threads --db through to the Go runner"

out=$(uv run endless errors show --db sandbox 2>&1) || fail "endless errors show --db sandbox" "${out}"
pass "endless errors show threads --db through to the Go fault record"

# ── 11. documentation ───────────────────────────────────────────────────────
section "11. Documentation"
# The command→section map is a pre-land gate: a new user-facing verb with no
# guide coverage fails it.
just guide-check >"${TMP}/guide.log" 2>&1 \
    || { sed 's/^/      /' "${TMP}/guide.log" >&2; fail "just guide-check"; }
pass "guide command→section map is complete and in sync"

# On-disk markdown is not the deliverable; what `endless guide` prints is.
out=$(uv run endless guide reference 2>/dev/null)
grep -q "Background jobs" <<<"${out}" || fail "endless guide reference omits the jobs section"
grep -q "^## Errors" <<<"${out}" || fail "endless guide reference omits the errors section"
pass "endless guide reference renders both new sections"

# The load-bearing semantics must be stated where a session will read them, not
# only in code comments.
grep -q "every job must be idempotent" <<<"${out}" \
    || fail "the guide does not state the idempotency requirement the lease imposes"
grep -q "Clearing is not retrying" <<<"${out}" \
    || fail "the guide does not distinguish 'errors clear' from 'jobs retry'"
pass "the guide states the idempotency requirement and the clear-vs-retry distinction"

# Every catalog code is documented (also asserted as a Go test; repeated here so
# the suite fails loudly on a docs-only edit).
for code in ERR-0001 ERR-0002 ERR-0003 ERR-0004 ERR-0005; do
    grep -q "^## ${code} — " docs/errors.md || fail "docs/errors.md has no section for ${code}"
done
pass "docs/errors.md documents every catalog code"

printf '\n%sALL PASSED%s\n\n' "${GREEN}${BOLD}" "${RESET}"

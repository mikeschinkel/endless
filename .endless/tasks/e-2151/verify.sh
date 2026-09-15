#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2151 and records what was true when E-2151
# landed. Edit it only if you ARE E-2151. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2151: nothing expires off the fault notification row. Both severities
# stay until a human clears them.
#
# A warning used to stop being counted an hour of ACTIVE time after it last
# occurred — `staleWarningAfter`, applied by `badgeworthy`. The incident was
# never cleared or deleted; `endless errors show` listed it the whole time. But
# that row is what makes a person think to run that command, so a warning that
# left it had in practice left: discoverable only by someone who already
# suspected it existed. Errors never expired, so the two severities disagreed
# about what "uncleared" meant.
#
# What is verified here:
#   A. Fail-fast: this task's own Go tests, and each contract test by name so
#      deleting one cannot turn the section green by absence.
#   B. The expiry is gone from the source — the threshold, the filter that
#      applied it, and the dependency on the active-time clock it needed.
#   C. It is gone from BEHAVIOUR, end to end, through a binary built from this
#      tree — against a throwaway database whose activity log is seeded so an
#      hour-of-active-time rule would provably have fired. This is the check
#      that discriminates: run against HEAD~ the same fixture renders
#      "ERR-0007 probe error", and against this tree "1 error, 1 warning".
#   D. Clearing is what removes an incident, and the only thing that does —
#      including a lone aged warning, the exact case the expiry was aimed at.
#   E. The active-time clock SURVIVES. Losing its only caller is not a reason
#      to delete it: measuring time the user was actually present for is what
#      an error-detail view needs to say how long ago a fault last fired
#      (E-2148 item 2). No caller today is not dead.
#   F. The documentation no longer teaches a rule the code no longer has.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || setup_error "not inside a git worktree"
cd "${WT}" || setup_error "cannot cd to worktree root ${WT}"

TMP="$(mktemp -d)" || setup_error "cannot create a scratch directory"
trap 'rm -rf "${TMP}"' EXIT

BADGE=internal/faultbadge/faultbadge.go
[[ -f "${BADGE}" ]] || setup_error "${BADGE} is missing"

# sweep <phrase> — tracked PRODUCT files still containing it.
#
# `git grep` rather than `grep -r`: it searches tracked files only, so a scratch
# file cannot fail the suite, and it prints repo-relative paths with no leading
# "./" that some greps add and others do not.
#
# .endless/ is excluded because all of it is Endless's own records — the
# append-only ledger, the plan and analysis mirrors, and landed verify suites
# including this one, which has to quote the removed names in order to assert
# they are gone. A record of what was true then is not the tool saying it now.
sweep() {
    git grep -lIF -e "$1" -- ':!.endless/' 2>/dev/null | sort
}

# ---------------------------------------------------------------------------
section "A. This task's own tests (fail-fast)"
# ---------------------------------------------------------------------------
# faultbadge holds the change. faults owns the aggregate the row now renders
# unfiltered. errorscmd owns `errors clear`, which this change makes the only
# exit an incident has.

if out=$(go test ./internal/faultbadge ./internal/faults ./internal/errorscmd 2>&1); then
    report_pass "go test: faultbadge, faults, errorscmd"
else
    report_fail "go test: faultbadge, faults, errorscmd" "exit 0" "$(printf '%s' "${out}" | tail -30)"
    summary
fi

for t in TestRender_BadgesAWarningHoweverLongAgoItFired \
         TestRender_CountsEveryUnclearedIncidentHoweverOld \
         TestRender_StopsBadgingOnlyWhatSomebodyCleared; do
    if go test ./internal/faultbadge -run "^${t}$" -v 2>&1 | grep -q "^--- PASS: ${t}"; then
        report_pass "contract test runs and passes: ${t}"
    else
        report_fail "contract test runs and passes: ${t}" "--- PASS: ${t}" "no PASS line"
    fi
done

# ---------------------------------------------------------------------------
section "B. The expiry is gone from the source"
# ---------------------------------------------------------------------------

assert_eq "no file still defines or reads staleWarningAfter" \
    "" "$(sweep 'staleWarningAfter')"

assert_eq "no file still defines or calls badgeworthy" \
    "" "$(sweep 'badgeworthy')"

# The clock was the only thing faultbadge needed monitor for. A leftover import
# would mean a filter is still in there under another name.
assert_not_contains "faultbadge no longer depends on the monitor package" \
    "internal/monitor" "$(cat "${BADGE}")"

# Inherited invariant, cheap to keep: what the row shows is a READ. A renderer
# that clears incidents would make looking at a pane dismiss them.
assert_not_contains "the renderer still never mutates the fault store" \
    "faults.Clear" "$(cat "${BADGE}")"

# ---------------------------------------------------------------------------
section "C. Both severities survive a clock that would have expired one"
# ---------------------------------------------------------------------------
# Built from this tree, not taken from bin/, so the answers come from what is
# committed here rather than from whatever was last installed.
#
# Both sides take an explicit --config-dir at a throwaway path. That keeps the
# probe out of the real record AND out of this worktree's sandbox, and it is
# the only way to make raise and render provably name one database: `errors`
# pins main, `session-status` pins main on its normal path, and `--task`
# deliberately reads the resolved context.

BIN="${TMP}/endless-go"
go build -o "${BIN}" ./cmd/endless-go \
    || setup_error "cannot build cmd/endless-go from this tree"

PROBE="${TMP}/config"
mkdir -p "${PROBE}"
GO=("${BIN}" --config-dir "${PROBE}")

"${GO[@]}" errors raise --summary "e-2151 probe warning" >"${TMP}/raise-w.txt" 2>&1 \
    || setup_error "errors raise (warning) failed: $(head -3 "${TMP}/raise-w.txt")"
WARN_ID=$(grep -oE 'as error [0-9]+' "${TMP}/raise-w.txt" | grep -oE '[0-9]+$')
[[ -n "${WARN_ID}" ]] || setup_error "errors raise did not report an incident id"

"${GO[@]}" errors raise --severity error --summary "e-2151 probe error" >"${TMP}/raise-e.txt" 2>&1 \
    || setup_error "errors raise (error) failed: $(head -3 "${TMP}/raise-e.txt")"
ERR_ID=$(grep -oE 'as error [0-9]+' "${TMP}/raise-e.txt" | grep -oE '[0-9]+$')
[[ -n "${ERR_ID}" ]] || setup_error "errors raise did not report an incident id"

# Age both incidents three hours, then seed an activity log that accrues ~80
# minutes of ACTIVE time inside that window: 21 rows four minutes apart, every
# gap under monitor.IdleGap so none of them reads as the user walking away.
# That is what makes this fixture discriminating rather than decorative — an
# hour-of-active-time rule has provably elapsed by the time the row renders.
command -v sqlite3 >/dev/null 2>&1 || setup_error "sqlite3 is required to seed the probe clock"
sqlite3 "${PROBE}/endless.db" <<'SQL' || setup_error "cannot seed the probe database"
INSERT OR IGNORE INTO projects (id, name, path) VALUES (1, 'e-2151-probe', '/tmp/e-2151-probe');
UPDATE errors SET last_seen_at = strftime('%Y-%m-%dT%H:%M:%S','now','-3 hours');
WITH RECURSIVE n(i) AS (SELECT 0 UNION ALL SELECT i+1 FROM n WHERE i < 20)
INSERT INTO activity (project_id, source, working_dir, created_at)
SELECT 1, 'claude', '/tmp/e-2151-probe',
       strftime('%Y-%m-%dT%H:%M:%S','now','-3 hours','+' || (i*4+1) || ' minutes')
FROM n;
SQL

rows=$(sqlite3 "${PROBE}/endless.db" 'SELECT count(*) FROM activity')
[[ "${rows}" == "21" ]] || setup_error "seeded ${rows} activity rows, expected 21"

# render — the notification row as a person would see it, ANSI stripped.
render() {
    "${GO[@]}" session-status --task 2151 --cols 140 2>"${TMP}/stderr.txt" \
        | sed 's/\x1b\[[0-9;]*m//g' \
        | grep -F 'Run eeh' || true
}

# `Run eeh` is the row's public identifier — the one string by which a caller
# can pick it out of a frame it did not render itself.
BOTH="$(render)"
assert_contains "the row is present with both incidents open" "Run eeh" "${BOTH}"
assert_contains "it counts the aged warning alongside the error" "1 error, 1 warning" "${BOTH}"

# ---------------------------------------------------------------------------
section "D. Clearing is the only exit"
# ---------------------------------------------------------------------------

"${GO[@]}" errors clear "${ERR_ID}" >"${TMP}/clear-e.txt" 2>&1 \
    || setup_error "errors clear ${ERR_ID} failed: $(head -3 "${TMP}/clear-e.txt")"

# The crux. A warning that fired once, hours of active time ago, with nothing
# more severe to ride along with, still raises the row on its own — which is
# precisely the case the expiry was written to suppress.
LONE="$(render)"
assert_contains "a lone aged warning still raises the row" "WARNING" "${LONE}"
assert_contains "and still names the incident" "ERR-0006" "${LONE}"

"${GO[@]}" errors clear "${WARN_ID}" >"${TMP}/clear-w.txt" 2>&1 \
    || setup_error "errors clear ${WARN_ID} failed: $(head -3 "${TMP}/clear-w.txt")"

assert_eq "clearing the last incident is what removes the row" "" "$(render)"

# ---------------------------------------------------------------------------
section "E. The active-time clock survives"
# ---------------------------------------------------------------------------
# It lost its only caller here, which is not a reason to delete it: time the
# user was actually present for is what an error-detail view needs in order to
# say how long ago a fault last fired.

assert_contains "monitor.ActiveSecondsSince is still exported" \
    "func ActiveSecondsSince(" "$(cat internal/monitor/activity.go)"

if go test ./internal/monitor -run '^TestActiveSecondsSince' -v 2>&1 | grep -q '^--- PASS: TestActiveSecondsSince'; then
    report_pass "its tests still exist and pass"
else
    report_fail "its tests still exist and pass" "at least one passing TestActiveSecondsSince" "none ran"
fi

# Its comments must not still justify it by a rule the code no longer has.
assert_not_contains "its doc no longer justifies itself by an age-off" \
    "age-off" "$(cat internal/monitor/activity.go)"

# ---------------------------------------------------------------------------
section "F. The documentation teaches the rule the code has"
# ---------------------------------------------------------------------------

REF="$(cat docs/guide/reference.md)"
assert_contains "the guide states that nothing leaves on its own" \
    "Nothing leaves the badge on its own." "${REF}"
assert_not_contains "the guide no longer teaches the hour of active time" \
    "stops being badged" "${REF}"
assert_not_contains "and no longer says a stale warning leaves" \
    "a stale warning does" "${REF}"

# Every surface a USER reads, swept together. Scoped to what ships as prose and
# help text: source comments legitimately say "nothing ages off" now, which is
# the new rule stated, not the old one lingering.
for phrase in 'ages off' 'age-off' 'stale warning' 'stops being badged'; do
    assert_eq "no user-facing text still describes a warning expiring: \"${phrase}\"" \
        "" "$(git grep -lIF -e "${phrase}" -- docs/ README.md src/ 2>/dev/null | sort)"
done

summary

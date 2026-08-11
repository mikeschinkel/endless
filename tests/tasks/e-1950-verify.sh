#!/usr/bin/env bash
#
# E-1950 verification script — the session-status fault badge.
#
# Three defects, one root cause each:
#
#   1. A single SQLITE_BUSY on a job's scheduling row raised a user-visible
#      ERR-0004 warning, which then pinned itself to the badge forever with no
#      visible way to resolve it. Contention is now retried before it faults,
#      the badge points at a command that explains dismissal, and a warning
#      stops being badged after an hour of ACTIVE time — measured from the
#      activity log, so it cannot expire overnight while nobody is watching.
#
#   2. The badge spent a whole second row on a dim `endless errors show`. It is
#      one row now: chip, incident text, `Run eeh` right-aligned.
#
#   3. The WARNING chip was black on ANSI yellow (`\033[30;43m`) — a range the
#      terminal theme remaps, which is why it rendered as an unreadable orange
#      block. The whole row is now reversed using fixed 256-color indices.
#
# Run from anywhere inside the worktree:
#   esu && ./tests/tasks/e-1950-verify.sh
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on a setup problem.
#
# Isolation: no real DB, ledger or cache is touched. The Go unit tests run
# against in-memory SQLite; the render checks drive the freshly-built worktree
# binary's own renderer through `go test`, and the static checks read source.

set -u

ROOT=$(git rev-parse --show-toplevel) || { echo "not in a git repo" >&2; exit 2; }
cd "${ROOT}" || exit 2

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

TMP=$(mktemp -d) || fail "mktemp failed"

BADGE=internal/sessionstatuscmd/faultbadge.go

# ── 1. the task's own unit tests, fail-fast ─────────────────────────────────
section "1. Go unit tests (fail-fast)"
if go test ./internal/sessionstatuscmd/ ./internal/faults/ ./internal/jobs/ ./internal/monitor/ ./internal/errorscmd/ \
        >"${TMP}/gotest.log" 2>&1; then
    pass "go test: sessionstatuscmd, faults, jobs, monitor, errorscmd"
else
    sed 's/^/      /' "${TMP}/gotest.log" >&2
    fail "the task's Go unit tests"
fi

# The named tests are the ones that encode this task's contract. Assert they
# ran, so deleting them cannot turn this section green by absence.
for t in TestBadgeworthy_DropsAWarningOnlyAfterAnActiveHour \
         TestBadgeworthy_NeverAgesOutAnError \
         TestBadgeworthy_KeepsWhatItCannotMeasure \
         TestBadgeLine_IsOneRowCarryingBothTextAndHint \
         TestBadgeLine_OmitsTheCountASingleChipAlreadyConveys \
         TestBadgeLine_UsesThemeIndependentColors; do
    go test ./internal/sessionstatuscmd/ -run "^${t}$" -v 2>&1 | grep -q "^--- PASS: ${t}" \
        || fail "${t} did not run and pass"
done
pass "each badge contract test exists and passes by name"

for t in TestActiveSecondsSince_SumsOnlyNonIdleGaps \
         TestActiveSecondsSince_IsZeroWhenTheMachineWasUnattended \
         TestActiveSecondsSince_CountsTheOpenSegmentUpToNow; do
    go test ./internal/monitor/ -run "^${t}$" -v 2>&1 | grep -q "^--- PASS: ${t}" \
        || fail "${t} did not run and pass"
done
pass "each active-clock contract test exists and passes by name"

for t in TestIsBusy_RecognizesSQLiteContention \
         TestEnsureRowWithRetry_GivesUpPromptlyOnANonBusyError; do
    go test ./internal/jobs/ -run "^${t}$" -v 2>&1 | grep -q "^--- PASS: ${t}" \
        || fail "${t} did not run and pass"
done
pass "each scheduling-retry contract test exists and passes by name"

# ── 2. defect 1: the fault that should never have been raised ───────────────
section "2. Contention is retried before it faults"

grep -q 'ensureRowWithRetry(db, job.Name())' internal/jobs/run.go \
    || fail "runOne no longer routes the scheduling write through the retry" \
            "a single lost lock race would raise ERR-0004 again"
pass "runOne creates the scheduling row through ensureRowWithRetry"

grep -q 'func isBusy(' internal/jobs/run.go || fail "isBusy is gone"
pass "contention is distinguished from real database failure"

# The retry must be bounded: an unbounded one turns a wedged DB into a hang.
retries=$(grep -oE 'ensureRowBusyRetries = [0-9]+' internal/jobs/run.go | grep -oE '[0-9]+$')
[[ -n "${retries}" ]] || fail "ensureRowBusyRetries is not a literal constant"
(( retries >= 1 && retries <= 5 )) \
    || fail "ensureRowBusyRetries = ${retries}" "want a small bound (1-5)"
pass "the retry is bounded at ${retries} extra attempts"

# ── 3. defect 1: the stale warning ages off the badge ───────────────────────
section "3. Stale warnings stop being badged"

grep -q 'staleWarningAfter' "${BADGE}" || fail "the age-off threshold is gone"
grep -q 'ActiveSecondsSince' "${BADGE}" \
    || fail "the badge no longer consults the active clock" \
            "a wall-clock age-off can expire overnight, unseen — that is the bug"
pass "the age-off is keyed to active time, not wall-clock"

# Warnings only. This is the line between "healed itself" and "still broken".
grep -q 'faults.SeverityWarning' "${BADGE}" || fail "the age-off is not severity-scoped"
pass "the age-off is scoped to warnings"

# Ageing off must not be clearing: the incident stays open and listed.
grep -q 'faults.Clear' "${BADGE}" \
    && fail "the badge clears incidents" "ageing off the badge must not mutate the store"
pass "ageing off does not clear or delete the incident"

# ── 4. defect 2: one row, not two ───────────────────────────────────────────
section "4. The badge is a single row"

# Two Fprintln calls in the renderer was literally the wasted row.
n=$(grep -c 'Fprintln' "${BADGE}")
(( n == 1 )) || fail "renderFaultBadge emits ${n} lines" "want exactly 1"
pass "the renderer emits exactly one line"

grep -q 'badgeHint = "Run eeh"' "${BADGE}" \
    || fail "the badge no longer points at the eeh helper"
pass "the badge names the eeh helper"

# ── 5. defect 3: readable, theme-independent colors ─────────────────────────
section "5. The chip is readable on any theme"

grep -qE '\\033\[3[0-7];4[0-7]m' "${BADGE}" \
    && fail "the badge still uses the 30-47 ANSI range" \
            "those are theme-remapped — it is why black-on-yellow read as an orange block"
pass "no theme-remapped ANSI color codes remain"

for c in rowError chipError rowWarning chipWarning; do
    grep -qE "${c} += \"\\\\033\[48;5;[0-9]+;38;5;[0-9]+m\"" "${BADGE}" \
        || fail "${c} is not a 256-color fg/bg pair"
done
pass "every severity style is a fixed 256-color fg/bg pair"

# The whole row reversed, not a colored word floating in plain text.
grep -q 'func rowStyle(' "${BADGE}" || fail "rowStyle is gone — the row is not reversed"
pass "the whole row carries the reversed background"

# ── 6. the dismissal is discoverable ────────────────────────────────────────
section "6. 'errors clear' is discoverable"

grep -q 'printClearHint' internal/errorscmd/errors.go \
    || fail "'errors show' no longer names 'errors clear'" \
            "the badge cannot spell out the dismissal; the command it points at must"
pass "'errors show' closes by naming 'errors clear'"

grep -q '^eeh()' <(uv run endless shell-init 2>/dev/null) \
    || fail "shell-init does not define eeh" "the badge points at a helper that does not exist"
pass "shell-init defines the eeh helper"

# ── 7. it actually renders ──────────────────────────────────────────────────
section "7. End-to-end render"

GO_BIN="${ROOT}/bin/endless-go"
[[ -x "${GO_BIN}" ]] || fail "worktree binary missing" "run 'just build' first: ${GO_BIN}"
"${GO_BIN}" session-status >"${TMP}/status.txt" 2>&1 \
    || fail "session-status exited non-zero" "$(head -3 "${TMP}/status.txt")"
grep -qE '^\s*endless errors show\s*$' "${TMP}/status.txt" \
    && fail "the standalone hint row is still being rendered"
pass "session-status renders without the orphaned hint row"

# Whatever the badge renders must fit on one line. Count the rows that carry a
# severity chip; two would mean the second line came back.
chips=$(grep -cE '(WARNING|ERROR)' "${TMP}/status.txt" || true)
(( chips <= 1 )) || fail "the badge occupies ${chips} rows" "want at most 1"
pass "the badge occupies at most one row in a live render"

# ── 8. documentation ────────────────────────────────────────────────────────
section "8. Documentation"

just guide-check >"${TMP}/guide.log" 2>&1 \
    || { sed 's/^/      /' "${TMP}/guide.log" >&2; fail "just guide-check"; }
pass "guide command→section map is complete and in sync"

out=$(uv run endless guide reference 2>/dev/null)
grep -q 'active' <<<"${out}" || fail "the guide does not mention the active-time age-off"
grep -q 'eeh' <<<"${out}" || fail "the guide does not mention the eeh helper"
pass "the guide documents the age-off and the eeh helper"

# The old absolute claim is now false for warnings and must not survive.
grep -q 'nothing clears itself' docs/guide/reference.md \
    && fail "the guide still claims nothing ever leaves the badge on its own"
pass "the guide no longer overstates the manual-clear rule"

grep -q 'E-1950' docs/errors.md || fail "docs/errors.md does not describe the ERR-0004 retry"
pass "docs/errors.md describes the scheduling-write retry"

for code in ERR-0001 ERR-0002 ERR-0003 ERR-0004 ERR-0005; do
    grep -q "^## ${code} — " docs/errors.md || fail "docs/errors.md has no section for ${code}"
done
pass "docs/errors.md still documents every catalog code"

printf '\n%sALL PASSED%s\n\n' "${GREEN}${BOLD}" "${RESET}"

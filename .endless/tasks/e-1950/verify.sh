#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1950 and records what was true when E-1950
# landed. Edit it only if you ARE E-1950. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
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
#   endless task verify E-1950
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on a setup problem.
#
# Isolation: no real DB, ledger or cache is touched. The Go unit tests run
# against in-memory SQLite; the render checks drive the freshly-built worktree
# binary's own renderer through `go test`, and the static checks read source.

# Refuse a direct run, and pick up the shared harness vocabulary. Sourced as the
# FIRST executable statement so the refusal fires before anything in this file
# runs; every definition below overrides the harness's own, so a suite written
# before the harness existed behaves exactly as it did.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

ROOT=$(git rev-parse --show-toplevel) || { echo "not in a git repo" >&2; exit 2; }
cd "${ROOT}" || exit 2

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi

TMP=""
RAISED=""
# The synthetic incident lives in a throwaway config dir under TMP (see section
# 7), so removing TMP disposes of it — nothing to clear, and nothing that can
# outlive a failed run.
cleanup() { [[ -n "${TMP}" ]] && rm -rf "${TMP}"; }
trap 'cleanup' EXIT

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

# Every check in this suite once drove `endless-go` directly, so `errors raise`
# shipping in the binary with no Python command went green anyway — the verb a
# user types did not exist. Assert the surface a human actually uses.
uv run endless errors --help 2>&1 | grep -qE '^\s+raise\s' \
    || fail "'endless errors' does not expose raise" \
            "a verb only endless-go can reach is unshipped, however well documented"
pass "'endless errors raise' exists on the Python CLI, not just in the binary"

uv run pytest tests/test_go_cli_parity.py -q >"${TMP}/parity.log" 2>&1 \
    || { sed 's/^/      /' "${TMP}/parity.log" >&2; fail "Go/Python CLI parity"; }
pass "every endless-go errors/jobs verb is reachable from the Python CLI"

# ── 7. it actually renders ──────────────────────────────────────────────────
section "7. End-to-end render"

GO_BIN="${ROOT}/bin/endless-go"
[[ -x "${GO_BIN}" ]] || fail "worktree binary missing" "run 'just build' first: ${GO_BIN}"
"${GO_BIN}" session-status >"${TMP}/status.txt" 2>&1 \
    || fail "session-status exited non-zero" "$(head -3 "${TMP}/status.txt")"
grep -qE '^\s*endless errors show\s*$' "${TMP}/status.txt" \
    && fail "the standalone hint row is still being rendered"
pass "session-status renders without the orphaned hint row"

# `errors raise` is what makes this section possible: before it, the badge could
# only be exercised end-to-end by waiting for something to actually break.
#
# Both sides take an explicit --config-dir at a throwaway path, for two reasons.
# It keeps the probe out of the real error record AND out of the worktree's
# sandbox — but more importantly it is the only way to make the two agree:
# `errors` pins main and `session-status` pins main on its normal path, while
# `--task` deliberately reads the resolved context. An explicit --config-dir
# overrides all three, so raise and render provably name one database.
PROBE_DIR="${TMP}/config"
mkdir -p "${PROBE_DIR}"
GO_PROBE=("${GO_BIN}" --config-dir "${PROBE_DIR}")

"${GO_PROBE[@]}" errors raise --summary "e-1950 verify probe" >"${TMP}/raise.txt" 2>&1 \
    || fail "errors raise failed" "$(head -3 "${TMP}/raise.txt")"
RAISED=$(grep -oE 'as error [0-9]+' "${TMP}/raise.txt" | grep -oE '[0-9]+$')
[[ -n "${RAISED}" ]] || fail "errors raise did not report an incident id" "$(cat "${TMP}/raise.txt")"
pass "errors raise records a synthetic incident (id ${RAISED})"

# The badge and the command it points at must name the SAME record. Left on cwd
# routing, `errors show` read the per-worktree sandbox while the badge read
# main, so the badge could count an incident `eeh` would not list (E-1950).
"${GO_PROBE[@]}" errors show >"${TMP}/show.txt" 2>&1 \
    || fail "errors show failed against the probe DB"
grep -q "e-1950 verify probe" "${TMP}/show.txt" \
    || fail "errors show does not list the incident errors raise just recorded" \
            "the badge would be pointing at a command that cannot explain it"
pass "errors show lists what errors raise recorded (one DB, not two)"

# The badge must be ONE row at every width, and must never exceed it. Sweeping
# here rather than checking one width on purpose: terminal width is not a fixed
# property of anyone's setup, and a single sampled width tests nobody's terminal
# but the sampler's.
for cols in 20 34 40 60 80 94 100 120 200; do
    "${GO_PROBE[@]}" session-status --task 1950 --cols "${cols}" >"${TMP}/w${cols}.txt" 2>&1 \
        || fail "session-status --cols ${cols} exited non-zero"
    badge=$(grep -nE '(WARNING|ERROR)' "${TMP}/w${cols}.txt" || true)
    n=$(printf '%s' "${badge}" | grep -c . || true)
    (( n <= 1 )) || fail "at cols=${cols} the badge occupies ${n} rows" "want at most 1"

    # The BADGE line must never exceed the width, or the terminal wraps it and
    # the monitor's newline-counting pane fit never sees the extra row. Scoped to
    # the badge on purpose: other rows (e.g. the fixed no-task hint) have their
    # own width behavior that predates this task.
    # Measured in DISPLAY COLUMNS, not bytes: the truncation ellipsis is three
    # bytes and one column, and a byte count would report a false overflow.
    width=$(sed 's/\x1b\[[0-9;]*m//g' "${TMP}/w${cols}.txt" \
            | grep -E '(WARNING|ERROR)' \
            | python3 -c '
import sys, unicodedata
def w(s):
    return sum(2 if unicodedata.east_asian_width(c) in "WF" else 1 for c in s)
print(max([w(l.rstrip("\n")) for l in sys.stdin] or [0]))')
    (( width < cols )) || fail "at cols=${cols} the badge is ${width} columns wide" "must stay under cols"
done
pass "the badge is one row and never overflows, swept across 20-200 columns"

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

for code in ERR-0001 ERR-0002 ERR-0003 ERR-0004 ERR-0005 ERR-0006 ERR-0007; do
    grep -q "^## ${code} — " docs/errors.md || fail "docs/errors.md has no section for ${code}"
done
pass "docs/errors.md still documents every catalog code"

printf '\n%sALL PASSED%s\n\n' "${GREEN}${BOLD}" "${RESET}"

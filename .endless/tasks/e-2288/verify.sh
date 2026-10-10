#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2288 and records what was true when E-2288
# landed. Edit it only if you ARE E-2288. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2288: a fault whose database write fails is called "log only", not
# "unindexed".
#
# What is verified here:
#   A. Fail-fast: the touched packages' Go tests, and this task's contract
#      tests by NAME so deleting one cannot turn the section green by absence.
#   B. End to end through a binary built from this tree, against a throwaway
#      database made unreadable: the line is written as "log_only"/"db_error",
#      `errors list` uses the new header and reason prefix, and a line in the
#      pre-rename spelling is still listed and cleared.
#   C. Grep guard: no fault-facing source or doc under internal/ or docs/ still
#      says indexed/unindexed, beyond the reader's back-compat keys.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || setup_error "not inside a git worktree"
cd "${WT}" || setup_error "cannot cd to worktree root ${WT}"

TMP="$(mktemp -d)" || setup_error "cannot create a scratch directory"
trap 'rm -rf "${TMP}"' EXIT

# ---------------------------------------------------------------------------
section "A. This task's own tests (fail-fast)"
# ---------------------------------------------------------------------------

PKGS=(./internal/faults ./internal/faultrow ./internal/errorscmd ./internal/hookcmd ./internal/sessionstatuscmd)
if out=$(go test "${PKGS[@]}" 2>&1); then
    report_pass "go test: faults, faultrow, errorscmd, hookcmd, sessionstatuscmd"
else
    report_fail "go test: faults, faultrow, errorscmd, hookcmd, sessionstatuscmd" "exit 0" "$(printf '%s' "${out}" | tail -40)"
    summary
fi

check_named() {
    local pkg="$1" t="$2"
    if go test "${pkg}" -run "^${t}$" -v 2>&1 | grep -q "^--- PASS: ${t}"; then
        report_pass "contract test runs and passes: ${t}"
    else
        report_fail "contract test runs and passes: ${t}" "--- PASS: ${t}" "no PASS line"
    fi
}

check_named ./internal/faults TestRecord_WritesTheLogOnlySpelling
check_named ./internal/faults TestLogOnly_ListsAndClearsPreRenameLines
check_named ./internal/faults TestRecord_WritesTheLogLineWhenTheDBWriteFails
check_named ./internal/faultrow TestRender_NoticeCountsLogOnlyBesideAHealthyStore

# ---------------------------------------------------------------------------
section "B. End to end, with the database unreadable"
# ---------------------------------------------------------------------------
# Built from this tree, and pointed at a throwaway --db-dir, so nothing here
# touches the real record or this worktree's sandbox.

BIN="${TMP}/endless-go"
go build -o "${BIN}" ./cmd/endless-go || setup_error "cannot build cmd/endless-go from this tree"

CFG="${TMP}/config"
mkdir -p "${CFG}"
GO=("${BIN}" --db-dir "${CFG}")

# One healthy raise first, so the database and the log directory both exist.
"${GO[@]}" errors raise --severity error --summary "e-2288 healthy row" >/dev/null 2>&1 \
    || setup_error "cannot raise a fault against a healthy probe database"
LOG="${CFG}/log/errors.jsonl"
[[ -f "${LOG}" ]] || setup_error "no detail log at ${LOG}"

# A pre-rename line, exactly as E-1887's writer spelled it.
printf '%s\n' '{"kind":"fault","ts":"2026-01-01T00:00:00","fault_id":null,"unindexed":true,"index_error":"e-2288 legacy reason","code":"ERR-0015","severity":"error","source":"hook:claude","fingerprint":"x","summary":"e-2288 legacy line"}' >>"${LOG}"

# Bytes SQLite cannot open: the database write now fails.
rm -f "${CFG}/endless.db-wal" "${CFG}/endless.db-shm"
printf 'this is not a database' >"${CFG}/endless.db"

"${GO[@]}" errors raise --severity error --summary "e-2288 log-only line" >/dev/null 2>&1

LAST="$(tail -1 "${LOG}")"
assert_contains "the new line is the one just raised" "e-2288 log-only line" "${LAST}"
assert_contains "it is marked log_only" '"log_only":true' "${LAST}"
assert_contains "and says why the database write failed, as db_error" '"db_error":"' "${LAST}"
assert_not_contains "and carries no unindexed key" '"unindexed"' "${LAST}"
assert_not_contains "and no index_error key" '"index_error"' "${LAST}"

LIST="$("${GO[@]}" errors list 2>&1)"
assert_contains "errors list uses the new header" \
    "2 occurrence(s) written to log only; DB write failed:" "${LIST}"
assert_contains "and the new reason prefix" "not in the database:" "${LIST}"
assert_contains "the pre-rename line is listed" "e-2288 legacy line" "${LIST}"
assert_contains "with its old index_error shown as the reason" \
    "not in the database: e-2288 legacy reason" "${LIST}"
assert_not_contains "nothing says indexed" "indexed" "${LIST}"

CLEAR="$("${GO[@]}" errors clear --log 2>&1)"
assert_contains "clear --log dismisses both spellings" \
    "dismissed 2 occurrence(s) waiting in the log" "${CLEAR}"

AFTER="$("${GO[@]}" errors list 2>&1)"
assert_not_contains "and the listing no longer shows them" "written to log only" "${AFTER}"

# ---------------------------------------------------------------------------
section "C. No fault-facing indexed/unindexed wording remains"
# ---------------------------------------------------------------------------
# Allowed hits, all about the pre-rename spelling the reader still accepts: the
# reader's back-compat keys in detaillog.go, test fixtures written in the old
# spelling, and the one docs sentence that names it.

HITS="$(grep -rnE 'unindexed|index_error|not indexed|never indexed|IndexError' internal docs \
    | grep -v -e '^internal/faults/detaillog.go:' -e '_test.go:' -e '^docs/errors.md:.*E-2288 carry' || true)"
assert_eq "no unindexed/index_error outside the back-compat reader" "" "${HITS}"

summary

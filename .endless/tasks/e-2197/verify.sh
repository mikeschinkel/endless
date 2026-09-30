#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2197 and records what was true when E-2197
# landed. Edit it only if you ARE E-2197. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2197 verification — a stale bin/endless-go no longer decides the Python
# suite's result, and the failure it hid is real and fixed.
#
#   1. The guard: a session id with no sessions row skipped E-2018's
#      never-claimed rule entirely (ValidateStatusActor returned early). It now
#      skips only the held-task rule.
#   2. The fixture: tests/test_report_reminder.py moved never-claimed tasks to
#      `unverified`. Against the fixed guard that is refused; the fixture now
#      records the claiming session, and the file passes on a fresh build.
#   3. The drift: `just test` builds bin/endless-go before pytest runs.
#
#   endless task verify E-2197
#
# Exit 0 on all-passed, 1 on any failure, 2 on setup error.

source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || setup_error "not in a git repo"
cd "${WT}" || setup_error "cannot cd to ${WT}"

TMP="$(mktemp -d)" || setup_error "could not create a temp dir"
trap 'rm -rf "${TMP}"' EXIT

# ── 1. fail-fast unit gate ──────────────────────────────────────────────────
section "1. Guard unit tests (fail fast)"

if go test ./internal/events/ >"${TMP}/go.log" 2>&1; then
    report_pass "go test ./internal/events/ (incl. the unreadable-session cases)"
else
    report_fail "go test ./internal/events/" "exit 0" "$(tail -25 "${TMP}/go.log")"
    summary
fi

# ── 2. the fixture, against a binary built from this tree ───────────────────
section "2. Report-reminder fixture on a fresh build"

if go build -o bin/endless-go ./cmd/endless-go >"${TMP}/build.log" 2>&1; then
    report_pass "bin/endless-go built from this worktree"
else
    report_fail "go build" "exit 0" "$(tail -25 "${TMP}/build.log")"
    summary
fi

if uv run pytest -q -p no:cacheprovider tests/test_report_reminder.py >"${TMP}/py.log" 2>&1; then
    report_pass "pytest tests/test_report_reminder.py"
else
    report_fail "pytest tests/test_report_reminder.py" "exit 0" "$(tail -25 "${TMP}/py.log")"
fi

# ── 3. just test cannot run against a stale binary ──────────────────────────
section "3. just test builds first"

plan="$(just --dry-run test 2>&1)"
build_line="$(grep -n 'go build -o bin/endless-go' <<<"${plan}" | head -1 | cut -d: -f1)"
pytest_line="$(grep -n 'pytest tests/' <<<"${plan}" | head -1 | cut -d: -f1)"
if [[ -n "${build_line}" && -n "${pytest_line}" && "${build_line}" -lt "${pytest_line}" ]]; then
    report_pass "just test runs the Go build before pytest"
else
    report_fail "just test runs the Go build before pytest" \
        "go build line before pytest line" "${plan}"
fi

summary

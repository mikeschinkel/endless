#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2277 and records what was true when E-2277
# landed. Edit it only if you ARE E-2277. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2277 verification — a just-landed worktree reads settled the moment the
# land finishes: the on-demand probe (`task unsettled`, and the warm `worktree
# land` runs after recording) no longer returns a stale pre-land "unsettled"
# cache entry for a branch tip that is now an ancestor of the base, and the
# answer it writes is what the ◆ column then reads.
#
#   endless task verify E-2277
#
# Exit 0 on all-passed, 1 on any failure, 2 on setup error.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || setup_error "not in a git repo"
cd "${WT}" || setup_error "cannot cd to ${WT}"
TMP="$(mktemp -d)" || setup_error "could not create a temp dir"
TMP="$(cd "${TMP}" && pwd -P)"
trap 'rm -rf "${TMP}"' EXIT

# ── 1. fail-fast unit gate ──────────────────────────────────────────────────
section "1. Unit gate (fail fast)"

if go test ./internal/monitor/ >"${TMP}/go.log" 2>&1; then
    report_pass "go test ./internal/monitor/: stale-after-land, cache, reaper and probe tests"
else
    report_fail "go test ./internal/monitor/" "exit 0" "$(tail -25 "${TMP}/go.log")"
    summary
fi

if uv run pytest -q tests/test_worktree_land_record_binary.py >"${TMP}/py.log" 2>&1; then
    report_pass "pytest: land warms the landed worktree after recording; a failed warm never fails the land"
else
    report_fail "pytest tests/test_worktree_land_record_binary.py" "exit 0" "$(tail -25 "${TMP}/py.log")"
    summary
fi

# ── 2. end to end: a real repo, a real land, the built probe ────────────────
section "2. a just-landed worktree reads settled straight away"

BIN="${TMP}/endless-go"
go build -o "${BIN}" ./cmd/endless-go >"${TMP}/build.log" 2>&1 \
    || setup_error "could not build endless-go: $(tail -10 "${TMP}/build.log")"

REPO="${TMP}/proj"
g() { git -C "$1" "${@:2}" >/dev/null 2>&1; }
mkdir -p "${REPO}"
g "${REPO}" init -q -b main
g "${REPO}" config user.email t@e.x
g "${REPO}" config user.name t
g "${REPO}" config commit.gpgsign false
echo x >"${REPO}/README"; g "${REPO}" add -A; g "${REPO}" commit -q -m init

WTD="${REPO}/.endless/worktrees/e-1"
g "${REPO}" worktree add -q -b task/1 "${WTD}" main || setup_error "worktree add failed"
g "${WTD}" config commit.gpgsign false
echo work >"${WTD}/work.txt"; g "${WTD}" add work.txt; g "${WTD}" commit -q -m "E-1: the work"
echo base >"${REPO}/base.txt"; g "${REPO}" add base.txt; g "${REPO}" commit -q -m "someone else"

# The job's watermark at the current base tip, as one pass leaves it.
CACHE="${REPO}/.git/info/endless/unlanded"
mkdir -p "${CACHE}"
WM="$(git -C "${REPO}" rev-parse main)"
echo "${WM}" >"${CACHE}/base-main"

# What `worktree land` does: rebase onto the base...
g "${WTD}" rebase main || setup_error "rebase failed"
HEAD_OID="$(git -C "${WTD}" rev-parse HEAD)"
# ...a compute-mode reader measures it in the window, writing an unsettled entry...
mkdir -p "${CACHE}/unsettled/${WM}"
echo "abc1234 E-1: the work" >"${CACHE}/unsettled/${WM}/${HEAD_OID}"
# ...then the fast-forward. Watermark and branch tip unchanged: the entry is stale.
g "${REPO}" merge -q --ff-only task/1 || setup_error "ff-merge failed"

probe() { "${BIN}" session-query worktree-unsettled "${WTD}" 2>"${TMP}/probe.err"; }
field() { python3 -c 'import json,sys; d=json.load(sys.stdin)["rows"][0]; print(d[sys.argv[1]])' "$1"; }

OUT="$(probe)" || setup_error "probe failed: $(cat "${TMP}/probe.err")"
assert_eq "on-demand probe after the land: unsettled" "False" "$(printf '%s' "${OUT}" | field unsettled)"
assert_eq "on-demand probe after the land: unlanded_count" "0" "$(printf '%s' "${OUT}" | field unlanded_count)"
assert_eq "on-demand probe after the land: reason" "settled" "$(printf '%s' "${OUT}" | field reason)"

if [[ -f "${CACHE}/settled/${HEAD_OID}" ]]; then
    report_pass "the probe wrote settled/<branch-tip>, which the ◆ column reads with no job pass"
else
    report_fail "settled/<branch-tip> written" "present" "absent: $(ls -R "${CACHE}")"
fi

# ── 3. a branch that is NOT in the base keeps its cached verdict ────────────
section "3. a branch with unlanded work still reads unsettled"

echo more >"${WTD}/more.txt"; g "${WTD}" add more.txt; g "${WTD}" commit -q -m "E-1: more work"
OUT="$(probe)" || setup_error "probe failed: $(cat "${TMP}/probe.err")"
assert_eq "new unlanded commit: unsettled" "True" "$(printf '%s' "${OUT}" | field unsettled)"
assert_eq "new unlanded commit: unlanded_count" "1" "$(printf '%s' "${OUT}" | field unlanded_count)"

summary

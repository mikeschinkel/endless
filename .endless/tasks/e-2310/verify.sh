#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2310 and records what was true when E-2310
# landed. Edit it only if you ARE E-2310. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2310 — Go tests' scratch tmux servers stay out of the shared socket dir.
#
#   1. The task's own tests (the helper's guard, and the two live-tmux tests
#      that now use it) — fail fast.
#   2. The whole Go suite, run with TMUX_TMPDIR pointed at a fresh directory
#      standing in for the shared one, leaves no socket in it, and leaves no
#      private tmuxtest directory behind under /tmp.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null) || setup_error "not inside a git worktree"
cd "${REPO_ROOT}" || setup_error "cannot cd to ${REPO_ROOT}"
for tool in go tmux; do
    command -v "${tool}" >/dev/null 2>&1 || setup_error "${tool} not on PATH"
done

# Short and under /tmp, so a socket path inside it stays under the ~104-byte limit.
SHARED=$(mktemp -d /tmp/e2310-shared-XXXXXX) || setup_error "cannot make the stand-in socket dir"
cleanup() {
    local sock
    for sock in "${SHARED}"/tmux-*/*; do
        [[ -S "${sock}" ]] && tmux -S "${sock}" kill-server 2>/dev/null
    done
    rm -rf "${SHARED}"
}
trap cleanup EXIT

private_dirs() { ls -d /tmp/endless-tmux-* 2>/dev/null | sort; }

section "The task's own tests"
OUT=$(go test -count=1 ./internal/tmuxtest ./internal/spawnlaunchcmd ./internal/sessionmonitorcmd 2>&1)
if [[ $? -eq 0 ]]; then
    report_pass "tmuxtest guard, placement and tag tests pass"
else
    report_fail "tmuxtest guard, placement and tag tests pass" "${OUT}"
    summary
fi

section "The Go suite leaves no socket in the shared directory"
BEFORE_PRIVATE=$(private_dirs)
OUT=$(env -u TMUX -u TMUX_PANE TMUX_TMPDIR="${SHARED}" go test -count=1 ./internal/... 2>&1)
if [[ $? -eq 0 ]]; then
    report_pass "go test ./internal/... is green"
else
    report_fail "go test ./internal/... is green" "$(printf '%s\n' "${OUT}" | grep -E -A12 '^(--- FAIL|FAIL)')"
fi
assert_eq "no socket left in the stand-in shared dir" "" \
    "$(find "${SHARED}" -mindepth 1 -not -type d 2>/dev/null)"
assert_eq "no private tmuxtest dir left under /tmp" "" \
    "$(comm -13 <(printf '%s\n' "${BEFORE_PRIVATE}") <(private_dirs))"

summary

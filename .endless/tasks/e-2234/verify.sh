#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2234 and records what was true when E-2234
# landed. Edit it only if you ARE E-2234. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2234 — focus and placement flags on `task spawn`: --no-refocus,
# --to-first (the default), --to-last, --to-left, --to-right and
# --tmux-session, plus auto_spawn.placement (default last) for the auto-spawn
# job.
#
#   1. The task's own unit tests, fail fast: the Go argv builders, target
#      resolution and auto-spawn settings, and the Python flag plumbing.
#   2. End to end: THIS worktree's `endless-go spawn-window`, built fresh, run
#      against a scratch tmux server (`tmux -L`) whose window numbering has a
#      hole. Asserts where each window's tab lands and which window is active
#      afterwards. `claude` and `endless` are stubs on PATH, so nothing real is
#      launched and no database is read.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

ROOT=$(git rev-parse --show-toplevel) || setup_error "not inside a git worktree"
cd "${ROOT}" || setup_error "cannot cd to ${ROOT}"
for tool in go uv tmux; do
    command -v "${tool}" >/dev/null 2>&1 || setup_error "${tool} not on PATH"
done

WORK_TMP=$(mktemp -d)
SOCK="e2234-verify-$$"
tm() { tmux -L "${SOCK}" -f /dev/null "$@"; }
trap 'tm kill-server >/dev/null 2>&1; rm -rf "${WORK_TMP}"' EXIT

# ── 1. unit tests, fail fast ─────────────────────────────────────────────────
section "1. Unit tests (fail fast)"
if out=$(go test -count=1 ./internal/spawnlaunchcmd/ ./internal/autospawnjob/ ./internal/config/ 2>&1); then
    report_pass "go test spawnlaunchcmd, autospawnjob, config (incl. live tmux placement)"
else
    report_fail "go test spawnlaunchcmd, autospawnjob, config" "ok" "$(printf '%s' "${out}" | tail -30)"
    summary
fi
if out=$(uv run pytest -q tests/test_spawn_foreground.py 2>&1); then
    report_pass "pytest tests/test_spawn_foreground.py (CLI flags, mutual exclusion, refusal before pre-claim)"
else
    report_fail "pytest tests/test_spawn_foreground.py" "passed" "$(printf '%s' "${out}" | tail -30)"
    summary
fi

# ── 2. end to end against a scratch tmux server ──────────────────────────────
section "2. spawn-window against a scratch tmux server"

BIN="${WORK_TMP}/endless-go"
go build -o "${BIN}" ./cmd/endless-go >"${WORK_TMP}/build.log" 2>&1 \
    || setup_error "go build ./cmd/endless-go: $(tail -20 "${WORK_TMP}/build.log")"

STUBS="${WORK_TMP}/stubs"
mkdir -p "${STUBS}"
printf '#!/bin/sh\nexec sleep 300\n' >"${STUBS}/claude"
printf '#!/bin/sh\nexec sleep 300\n' >"${STUBS}/endless"
chmod +x "${STUBS}/claude" "${STUBS}/endless"
export PATH="${STUBS}:${PATH}"

# Windows A(0) C(2): the hole at 1 is where `<session>:` alone used to land.
# Every pane runs sleep, never a shell: a shell killed with the server writes
# its history into the runner's temp HOME after the runner has begun removing it.
tm new-session -d -s probe -n A -x 200 -y 50 sleep 300 || setup_error "tmux new-session"
tm set-option -g default-command "sleep 300"
tm new-window -d -t probe:1 -n B
tm new-window -d -t probe:2 -n C
tm kill-window -t probe:B
tm new-session -d -s other -n O -x 200 -y 50 || setup_error "tmux new-session other"
SOCK_PATH=$(tm display-message -p -t probe '#{socket_path}')
PANE_A=$(tm display-message -p -t probe:A '#{pane_id}')

windows() { tm list-windows -t "=$1" -F '#{window_name}#{?window_active,*,}' | tr '\n' ' ' | sed 's/ $//'; }

# spawn runs spawn-window as if from pane A of session probe: $TMUX points the
# plain `tmux` it runs at the scratch server, and $TMUX_PANE is the spawner.
spawn() {
    local name=$1; shift
    local handoff="${WORK_TMP}/handoff-${name}.md"
    echo "handoff" >"${handoff}"
    TMUX="${SOCK_PATH},0,0" TMUX_PANE="${PANE_A}" \
        "${BIN}" spawn-window --claude-bin "${STUBS}/claude" --handoff-file "${handoff}" \
        --task-id 1 --project-id 1 --spawned-by verify --window-name "${name}" \
        --cwd "${WORK_TMP}" "$@" >"${WORK_TMP}/out-${name}.log" 2>&1
}

spawn F --no-refocus
assert_eq "--to-first (default) lands before every window, focus held" "F A* C" "$(windows probe)"
spawn L --placement last --no-refocus
assert_eq "--to-last lands after every window, not in the index hole" "F A* C L" "$(windows probe)"
spawn R --placement right --no-refocus
assert_eq "--to-right lands just after the active window" "F A* R C L" "$(windows probe)"
spawn Lf --placement left --no-refocus
assert_eq "--to-left lands just before the active window" "F Lf A* R C L" "$(windows probe)"
spawn X
assert_eq "without --no-refocus the new window (first) takes focus" "X* F Lf A R C L" "$(windows probe)"

# --tmux-session: lands in the named session, not the spawner's.
before=$(windows probe)
spawn T --tmux-session other --placement last
assert_eq "--tmux-session other: the window opens in session other" "O T*" "$(windows other)"
assert_eq "--tmux-session other: the spawner's session is untouched" "${before}" "$(windows probe)"

if spawn N --tmux-session oth; then
    report_fail "--tmux-session with a prefix of a real name is refused" "non-zero exit" "exit 0"
else
    assert_contains "--tmux-session oth (a prefix) is refused, naming it" 'no tmux session is named "oth"' "$(cat "${WORK_TMP}/out-N.log")"
fi
assert_eq "the refused spawn created no window" "O T*" "$(windows other)"

if spawn P --placement middle; then
    report_fail "an unknown --placement is refused" "non-zero exit" "exit 0"
else
    assert_contains "an unknown --placement is refused" "middle" "$(cat "${WORK_TMP}/out-P.log")"
fi

summary

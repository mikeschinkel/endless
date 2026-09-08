#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2125 and records what was true when E-2125
# landed. Edit it only if you ARE E-2125. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2125: a `tmux new-window` with no `-t` lands in the server's "current"
# session, which off a command line means the most recently active one. So a
# window Endless was asked to open in one session appeared in whichever session
# the person happened to be looking at, carrying nothing that said where it came
# from or what it belonged to.
#
# What is verified here:
#   A. Fail-fast: the task's own tests, Go and Python, all pass.
#   B. Live proof, on a private tmux server: a spawn asked for from a pane in
#      session `spawner`, while `operator` is the session tmux considers
#      current, creates its window in `spawner` and leaves `operator` alone.
#   C. An unresolvable target refuses and creates nothing — the fix is worth
#      nothing if the unknown case quietly falls back to landing anywhere.
#   D. Every `new-window` in the product carries a target, and the layout that
#      follows one anchors on a pane id rather than looking the window back up
#      by name (the same unqualified-target bug, one call later).
#   E. The suite itself can no longer reach a live tmux server, so this cannot
#      be re-introduced by a test the way it was found.
#
# B and C need a real tmux and are integration-only; the argv property behind
# them is mirrored into internal/spawnlaunchcmd/tmux_driver_test.go and
# tests/test_session_goto_back.py, and E into tests/test_tmux_isolation.py, so
# the coverage outlives this suite.

source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)"
cd "${WT}" || setup_error "cannot cd to worktree root ${WT}"

# ---------------------------------------------------------------------------
section "A. This task's own tests (fail-fast)"
# ---------------------------------------------------------------------------

if out=$(go build -o "${WT}/bin/endless-go" ./cmd/endless-go 2>&1); then
    report_pass "go build: endless-go"
else
    report_fail "go build: endless-go" "exit 0" "$(printf '%s' "${out}" | tail -30)"
    summary
fi

if out=$(go test ./internal/spawnlaunchcmd/ 2>&1); then
    report_pass "go test: spawnlaunchcmd"
else
    report_fail "go test: spawnlaunchcmd" "exit 0" "$(printf '%s' "${out}" | tail -30)"
    summary
fi

if out=$(uv run pytest -q \
        tests/test_tmux_isolation.py \
        tests/test_session_goto_back.py \
        tests/test_session_resume_window_options.py \
        tests/test_transcript_gone.py \
        tests/test_spawn_foreground.py 2>&1); then
    report_pass "pytest: tmux isolation, goto/resume windows, spawn argv"
else
    report_fail "pytest: tmux isolation, goto/resume windows, spawn argv" \
        "exit 0" "$(printf '%s' "${out}" | tail -30)"
    summary
fi

# ---------------------------------------------------------------------------
section "B. A spawn lands in the session that asked for it"
# ---------------------------------------------------------------------------
# Everything below runs against a private tmux server (`tmux -L`), never the
# one the person running this is sitting in — which is the whole subject of
# this task, so doing otherwise would be the bug wearing a lab coat.

SOCK="endless-e2125-$$"
WORK=""
cleanup() {
    tmux -L "${SOCK}" kill-server 2>/dev/null
    [[ -n "${WORK}" ]] && rm -rf "${WORK}"
}
trap cleanup EXIT

T() { tmux -L "${SOCK}" "$@"; }

if ! command -v tmux >/dev/null 2>&1; then
    report_skip "a spawn lands in the spawning session" "tmux not installed"
    report_skip "the frontmost session is left alone" "tmux not installed"
    report_skip "an unresolvable target refuses" "tmux not installed"
    report_skip "a refused spawn creates no window" "tmux not installed"
else
    WORK=$(mktemp -d)
    : > "${WORK}/handoff.md"

    # The spawn layout puts an interactive shell in one pane, and a shell writes
    # a history file under $HOME on exit. The tmux SERVER's environment is fixed
    # when it starts and a later set-environment does not reach a session that
    # already exists, so the home this suite wants its panes to use has to be in
    # place on the very first command — otherwise the file lands in the runner's
    # temp HOME, which the runner then cannot remove and says so on the way out.
    mkdir -p "${WORK}/home"
    T kill-server 2>/dev/null
    HOME="${WORK}/home" T new-session -d -s spawner -n home \
        || setup_error "cannot start private tmux server"
    T new-session -d -s operator -n home
    # A pane whose command exits must leave its window standing, so what this
    # asserts is where the window went and not how fast /bin/echo returns.
    T set-option -g remain-on-exit on >/dev/null 2>&1
    # Touching `operator` last makes it the session an untargeted new-window
    # would have chosen. That is the operator's frontmost session, reproduced.
    T select-window -t operator:home
    sleep 0.3

    PANE=$(T display-message -p -t spawner:home '#{pane_id}')
    SESS=$(T display-message -p -t spawner:home '#{session_id}')
    SOCKPATH=$(T display-message -p -t spawner:home '#{socket_path}')
    CURRENT=$(T display-message -p '#{session_name}')

    assert_eq "the session tmux would pick on its own is NOT the spawner's" \
        "operator" "${CURRENT}"

    # --claude-bin /bin/echo replaces the one thing in a spawn that must not run
    # here: the real Claude launch. Every tmux decision under test is upstream
    # of it and unchanged.
    env TMUX="${SOCKPATH},1,${SESS#$}" TMUX_PANE="${PANE}" \
        "${WT}/bin/endless-go" spawn-window \
        --claude-bin /bin/echo \
        --handoff-file "${WORK}/handoff.md" \
        --window-name E2125PROBE \
        --cwd "${WORK}" >/dev/null 2>&1
    sleep 0.5

    LANDED=$(T list-windows -a -F '#{session_name} #{window_name}' \
        | awk '$2 == "E2125PROBE" { print $1 }')
    assert_eq "a spawn lands in the spawning session" "spawner" "${LANDED}"

    OPERATOR_WINDOWS=$(T list-windows -t operator -F '#{window_name}' | tr '\n' ' ')
    assert_eq "the frontmost session is left alone" "home " "${OPERATOR_WINDOWS}"

    # -------------------------------------------------------------------
    section "C. No answer to \"which session?\" means no window"
    # -------------------------------------------------------------------

    REFUSAL=$(env -u TMUX_PANE TMUX="${SOCKPATH},1,${SESS#$}" \
        "${WT}/bin/endless-go" spawn-window \
        --claude-bin /bin/echo \
        --handoff-file "${WORK}/handoff.md" \
        --window-name E2125REFUSE \
        --cwd "${WORK}" 2>&1)
    RC=$?

    if (( RC != 0 )); then
        report_pass "an unresolvable target refuses"
    else
        report_fail "an unresolvable target refuses" "non-zero exit" "exit ${RC}"
    fi
    assert_contains "the refusal names what is missing" "TMUX_PANE" "${REFUSAL}"

    STRAY=$(T list-windows -a -F '#{window_name}' | grep -c E2125REFUSE)
    assert_eq "a refused spawn creates no window" "0" "${STRAY}"
fi

# ---------------------------------------------------------------------------
section "D. Every new-window in the product carries a target"
# ---------------------------------------------------------------------------
# Stated as a property over the tree rather than over the two call sites known
# today, so a third one added later is caught by this rather than by somebody
# finding a window in their session.

UNTARGETED=0
SITES=0
while IFS= read -r line; do
    SITES=$((SITES + 1))
    [[ "${line}" == *'"-t"'* ]] && continue
    UNTARGETED=$((UNTARGETED + 1))
    printf '      untargeted: %s\n' "${line}"
done < <(grep -rn '"new-window"' "${WT}/src" "${WT}/internal" | grep -v '_test\.')

if (( SITES == 0 )); then
    report_fail "the new-window call sites were found" "at least one" "none"
else
    assert_eq "every new-window call site names a target (${SITES} found)" \
        "0" "${UNTARGETED}"
fi

DRIVER="${WT}/internal/spawnlaunchcmd/tmux_driver.go"
new_window_body=$(awk '/^func newWindowArgs\(/,/^}/' "${DRIVER}")
assert_contains "the builder cannot omit the target — it is a parameter" \
    '"new-window", "-t", target' "${new_window_body}"
assert_contains "new-window reports the pane it created" \
    '"-P", "-F", "#{pane_id}"' "${new_window_body}"

# The layout used to find its pane by asking tmux for the window BY NAME, which
# resolves against the current session exactly the way the bug did.
assert_not_contains "the layout no longer looks the window up by name" \
    "panePaneIDArgs" "$(cat "${WT}"/internal/spawnlaunchcmd/*.go "${WT}"/src/endless/*.py)"

session_body=$(awk '/^func spawnerSession\(/,/^}/' "${DRIVER}")
assert_contains "the target comes from the spawner's pane" \
    'os.Getenv("TMUX_PANE")' "${session_body}"

py_target=$(awk '/^def _spawner_session_target\(/,/^def _tmux_switch_client/' \
    "${WT}/src/endless/session_cmd.py")
assert_contains "the Python side resolves its target the same way" \
    '#{session_id}' "${py_target}"

# ---------------------------------------------------------------------------
section "E. The suite cannot reach a live tmux server"
# ---------------------------------------------------------------------------
# Deleting \$TMUX only stops code that BRANCHES on it; tmux itself finds the
# running server through its default socket regardless. Pointing TMUX_TMPDIR at
# an empty per-test directory is what makes a shelled-out tmux find nothing.

CONFTEST=$(cat "${WT}/tests/conftest.py")
assert_contains "conftest gives each test its own tmux socket dir" \
    'monkeypatch.setenv("TMUX_TMPDIR", str(tmux_tmpdir))' "${CONFTEST}"
assert_contains "and it is scoped to that test's tmp_path" \
    'tmux_tmpdir = tmp_path / "tmux"' "${CONFTEST}"

summary

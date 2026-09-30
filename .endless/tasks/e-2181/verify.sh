#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2181 and records what was true when E-2181
# landed. Edit it only if you ARE E-2181. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2181 verification — a Claude session Endless launches on a task is named
# for the task.
#
# Before: neither `task spawn` nor a shell-side `task claim` passed
# `claude --name`, so Claude Code named the session after the cwd basename plus
# a two-character disambiguator (`e-1983-f2`). A session name is an address —
# ListAgents lists by it, SendMessage sends to it — and that one had to be read
# off a listing first.
#
# After: both launch paths pass `--name e-NNNN`; an explicit `task spawn --name`
# still wins.
#
# Section 2 drives the real Python launch path through the REAL compiled
# `endless-go spawn-window`, on a private tmux server, into a fake `claude`
# that records the argv it was exec'd with. Nothing between Python and claude's
# argv is stubbed.
#
#   endless task verify E-2181
#
# Exit 0 on all-passed, 1 on any failure, 2 on setup error.

source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || setup_error "not in a git repo"
cd "${WT}" || setup_error "cannot cd to ${WT}"

command -v tmux >/dev/null 2>&1 || setup_error "tmux is not installed"

TMP="$(mktemp -d)" || setup_error "could not create a temp dir"
SOCK="${TMP}/e2181.sock"
cleanup() {
    tmux -S "${SOCK}" kill-server 2>/dev/null
    rm -rf "${TMP}"
}
trap cleanup EXIT

# ── 1. fail-fast unit gate ──────────────────────────────────────────────────
section "1. Unit gate (fail fast)"

if uv run pytest -q tests/test_spawn_foreground.py \
        tests/test_claim_launches_a_session.py >"${TMP}/py.log" 2>&1; then
    report_pass "pytest test_spawn_foreground, test_claim_launches_a_session"
else
    report_fail "pytest spawn/claim launch" "exit 0" "$(tail -25 "${TMP}/py.log")"
    summary
fi

if go test ./internal/spawnlaunchcmd/ -run TestBuildClaudeArgv >"${TMP}/go.log" 2>&1; then
    report_pass "go test ./internal/spawnlaunchcmd/ -run TestBuildClaudeArgv"
else
    report_fail "go test TestBuildClaudeArgv" "exit 0" "$(tail -25 "${TMP}/go.log")"
    summary
fi

# ── 2. end to end: Python → real endless-go → claude's argv ─────────────────
section "2. The name reaches claude's argv through the real launcher"

BIN="${TMP}/endless-go"
go build -o "${BIN}" ./cmd/endless-go >"${TMP}/build.log" 2>&1 \
    || setup_error "could not build endless-go: $(tail -5 "${TMP}/build.log")"

# Stands in for claude: records its argv, one per line, then stays up the way
# a real session would until the server is killed.
FAKE="${TMP}/claude"
cat >"${FAKE}" <<FAKE_EOF
#!/usr/bin/env bash
printf '%s\n' "\$@" >"${TMP}/argv.tmp" && mv "${TMP}/argv.tmp" "${TMP}/argv"
exec sleep 120
FAKE_EOF
chmod +x "${FAKE}"

# The shell-side claim launch, run from INSIDE a pane of the private server so
# spawn-window's $TMUX / $TMUX_PANE resolve to it and never to the caller's.
cat >"${TMP}/launch.py" <<PY_EOF
from endless import event_bridge, task_cmd
event_bridge._resolve_endless_go = lambda *a, **k: "${BIN}"
task_cmd._claude_binary = lambda: "${FAKE}"
# The spawner marker reads a database; it is not what this suite is about.
task_cmd._current_endless_session_id = lambda *a, **k: "verify-e2181"
task_cmd._launch_claude_for_claim(2181, 1, "${TMP}")
PY_EOF

tmux -S "${SOCK}" new-session -d -s e2181 -x 200 -y 50 \
    "cd '${WT}' && uv run python '${TMP}/launch.py' >'${TMP}/launch.log' 2>&1; sleep 120" \
    || setup_error "could not start a tmux server on ${SOCK}"

for _ in $(seq 1 100); do
    [[ -s "${TMP}/argv" ]] && break
    sleep 0.1
done

if [[ ! -s "${TMP}/argv" ]]; then
    report_fail "the fake claude was launched" "an argv file" \
        "none; launch log: $(tail -10 "${TMP}/launch.log" 2>/dev/null)"
    summary
fi

argv="$(tr '\n' ' ' <"${TMP}/argv")"
name="$(awk 'prev=="--name"{print; exit} {prev=$0}' "${TMP}/argv")"
assert_eq "claude was exec'd with --name e-2181 (argv: ${argv})" "e-2181" "${name}"
assert_eq "--name appears exactly once" "1" "$(grep -cx -- '--name' "${TMP}/argv")"

# The window name is unchanged by this task: still the E-2102 form.
window="$(tmux -S "${SOCK}" list-windows -t e2181 -F '#{window_name}' | grep -x 'E-2181')"
assert_eq "the window alongside it is still named E-2181" "E-2181" "${window}"

summary

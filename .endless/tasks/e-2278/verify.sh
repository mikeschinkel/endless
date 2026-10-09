#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2278 and records what was true when E-2278
# landed. Edit it only if you ARE E-2278. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2278: every verify suite runs as a person, whoever starts it, on a private
# tmux server; as_agent and with_tmux opt a check into the agent's side and
# into tmux; and the runner executes a snapshot of verify.sh.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

root="$(git -C "$ENDLESS_VERIFY_DIR" rev-parse --show-toplevel)" || setup_error "not in a git checkout"
cd "$root" || setup_error "cannot cd to $root"
[[ -x "$root/bin/endless-go" ]] || setup_error "no $root/bin/endless-go — run \`just build\`"
EGO="$root/bin/endless-go"
# This suite tests the runner it is run by. main's runner predates E-2278 and
# exports no agent env, so under it every check below fails for the wrong
# reason; say which runner to use instead.
[[ -n "${ENDLESS_VERIFY_AGENT_ENV:-}" ]] \
    || setup_error "this suite needs E-2278's own runner, not main's — run: endless task verify E-2278 --db sandbox"

section "This task's own tests (fail fast)"
TESTS='TestPersonEnv_StripsTheCallersIdentity|TestAgentEnv_IsTheSupportedHarness|TestWriteAgentEnv|TestMakeTmuxDir_FitsASocketPath|TestSnapshotScript_KeepsTheLayout|TestRun_ScriptSuite_RunsASnapshot|TestRun_ScriptSuite_SameVerdictWhoeverRunsIt|TestRun_Manifest_AsAgentIsPerCheck|TestRun_PrivateTmuxIsTornDown|TestRun_Manifest_TmuxIsPerCheck'
if ! out="$(go test ./internal/verifycmd/ -count=1 -run "^(${TESTS})\$" -v 2>&1)"; then
    report_fail "E-2278 runner tests pass" "ok" "$out"
    summary
fi
for t in ${TESTS//|/ }; do
    assert_contains "$t" "--- PASS: $t" "$out"
done
out="$(go test ./internal/agentenv/ ./internal/verify/ -count=1 -run '^(TestIsHarnessVar|TestObservedCLIEnv|TestCheck_AsAndTmuxParse)$' -v 2>&1)"
for t in TestIsHarnessVar TestObservedCLIEnv TestCheck_AsAndTmuxParse; do
    assert_contains "$t" "--- PASS: $t" "$out"
done
out="$(uv run pytest tests/test_verify_cmd.py -q -k runner_env_drops_the_exported_audience 2>&1)"
assert_contains "the front door no longer hands the runner ENDLESS_AUDIENCE" "1 passed" "$out"

section "The default is a person, whoever ran this suite"
leaked="$(env | grep -E '^(CLAUDE[A-Z_]*|AI_AGENT|__CFBundleIdentifier|ENDLESS_AUDIENCE|ENDLESS_SESSION_ID|TMUX|TMUX_PANE)=' | cut -d= -f1 | tr '\n' ' ')"
assert_eq "no caller-identity variable reached the suite" "" "$leaked"
out="$(endless task show E-99999999 --db sandbox 2>&1)"
assert_contains "a refusal is rendered" "No task found with id 99999999" "$out"
assert_not_contains "...in the person's form (no agent directive)" "Handle this yourself" "$out"
out="$(endless task list --db sandbox 2>&1)"
assert_contains "the footer is the person's" $'\ndb: sandbox' $'\n'"$out"
assert_not_contains "...not the agent's" "# db:" "$out"
out="$(printf 'not json' | "$EGO" hook claude --db sandbox 2>&1)"
assert_eq "the Claude hook gate is shut for a person (payload ignored)" "" "$out"
out="$(endless session id --db sandbox 2>&1)"
assert_contains "a person outside tmux has no session" "No current Endless session" "$out"

section "as_agent gives every caller the same agent"
out="$(as_agent endless task show E-99999999 --db sandbox 2>&1)"
assert_contains "the same refusal renders in the agent's form" "Handle this yourself" "$out"
out="$(as_agent endless task list --db sandbox 2>&1)"
assert_contains "the footer is the agent's" "# db: sandbox" "$out"
out="$(printf 'not json' | as_agent "$EGO" hook claude --db sandbox 2>&1)"
assert_contains "the Claude hook gate is open (payload acted on)" "parsing payload" "$out"
assert_eq "as_agent carries the fixed fixture session" \
    "00000000-0000-4000-8000-000000000001" "$(as_agent printenv CLAUDE_CODE_SESSION_ID)"
sid="$(as_agent endless session id --db sandbox 2>&1)"
want="$(endless sql "SELECT id FROM sessions WHERE session_id = '00000000-0000-4000-8000-000000000001'" --db sandbox 2>&1 | grep -E '^ *[0-9]+ *$' | tr -d ' ')"
assert_eq "endless session id resolves the fixture session" "${want:-<no fixture row>}" "$sid"
assert_eq "as_agent leaks nothing into the next check" "" "$(printenv CLAUDECODE || true)"

section "A private tmux server, never the caller's"
assert_contains "TMUX_TMPDIR is the run's own, under /tmp" "/tmp/endless-verify-" "${TMUX_TMPDIR:-}"
if command -v tmux >/dev/null; then
    out="$(tmux list-sessions 2>&1)"
    assert_not_contains "tmux by default reaches no live session" ":" "$(tmux list-sessions -F '#{session_name}:' 2>/dev/null)"
    assert_contains "...because it asks the private socket, where nothing runs" "${TMUX_TMPDIR}/tmux-" "$out"
    assert_eq "the fixture pane's session is the private one" "endless-verify" \
        "$(with_tmux tmux display-message -p '#{session_name}')"
    pane="$(with_tmux printenv TMUX_PANE)"
    assert_eq "with_tmux names the fixture pane in TMUX_PANE" "$(with_tmux tmux display-message -p '#{pane_id}')" "$pane"
    assert_contains "with_tmux's TMUX is on the private socket" "${TMUX_TMPDIR}" "$(with_tmux printenv TMUX)"
    assert_eq "outside with_tmux, TMUX_PANE is unset again" "" "$(printenv TMUX_PANE || true)"
    a="$(with_tmux as_agent endless session id --db sandbox 2>&1)"
    b="$(as_agent with_tmux endless session id --db sandbox 2>&1)"
    assert_eq "with_tmux and as_agent combine in either order" "$a" "$b"
    assert_eq "an agent in the fixture pane resolves the fixture session" "$sid" "$a"
else
    report_skip "private tmux server checks" "tmux not installed"
fi

section "The run executes a snapshot of verify.sh"
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
assert_contains "this file is the run's copy" "/snapshot/.endless/tasks/e-2278" "$here"
assert_eq "ENDLESS_VERIFY_DIR still names the real suite" "$root/.endless/tasks/e-2278" \
    "$(cd "$ENDLESS_VERIFY_DIR" && pwd -P)"

summary

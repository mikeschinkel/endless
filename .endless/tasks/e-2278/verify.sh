#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2278 and records what was true when E-2278
# landed. Edit it only if you ARE E-2278. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2278: every verify suite runs as a person, whoever starts it, on a private
# tmux server; as_agent and with_tmux opt a check into the agent's side and
# into tmux; and the runner executes a snapshot of verify.sh.
#
# This task changes the runner itself, so the suite does not rely on the
# runner that started it (main's, which records the pass in main). It drives
# the CANDIDATE runner — this branch's bin/endless-go — against a throwaway
# fixture project, the way E-2023 and E-2243 tested theirs, and starts it
# twice: once from an agent's environment and once from a person's. The
# fixture suites record what they saw; this suite compares the two.
#
# Not covered here: how Endless itself reacts to the two environments where
# that needs a seeded sandbox database (the Python CLI's refusal form and
# footer, session-id resolution). A fixture project has no main database to
# seed one from. Only the database-free reactions are checked.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

root="$(git -C "$ENDLESS_VERIFY_DIR" rev-parse --show-toplevel)" || setup_error "not in a git checkout"
cd "$root" || setup_error "cannot cd to $root"
EGO="$root/bin/endless-go"
[[ -x "$EGO" ]] || setup_error "no $EGO — run \`just build\`"
command -v tmux >/dev/null || setup_error "tmux is required: this suite tests the runner's private tmux server"

TMP="$(mktemp -d)" || setup_error "could not create a temp dir"
trap 'rm -rf "$TMP"' EXIT

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

# ── The fixture project ──────────────────────────────────────────────────────
# A plain project (no worktree, no sandbox) holding this branch's harness and
# four fixture suites. Each script suite writes what it saw into $E2278_OUT,
# which the runner passes through untouched: it says nothing about identity.
PROJ="$TMP/proj"
mkdir -p "$PROJ/.endless/tasks/e-1" "$PROJ/.endless/tasks/e-2" \
         "$PROJ/.endless/tasks/e-3" "$PROJ/.endless/tasks/e-4"
cp "$root/.endless/tasks/_harness.sh" "$root/.endless/tasks/_guard.sh" "$PROJ/.endless/tasks/"

# E-1: what a suite sees, by default and under each wrapper.
cat > "$PROJ/.endless/tasks/e-1/verify.sh" <<'FIXTURE'
#!/usr/bin/env bash
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"
o="$E2278_OUT"
ids='^(CLAUDE[A-Z_]*|AI_AGENT|__CFBundleIdentifier|ENDLESS_AUDIENCE|ENDLESS_SESSION_ID|TMUX|TMUX_PANE)='
env | grep -E "$ids" | sort > "$o/person.env"
as_agent env | grep -E "$ids" | sort > "$o/agent.env"
env | grep -E "$ids" | sort > "$o/after-agent.env"
printf '%s' "$TMUX_TMPDIR" > "$o/tmux_tmpdir"
tmux list-sessions > "$o/list-sessions" 2>&1
with_tmux sh -c 'printf "%s|%s|%s" "$TMUX_PANE" "$TMUX" "$(tmux display-message -p "#{session_name} #{pane_id}")"' > "$o/with_tmux"
with_tmux as_agent sh -c 'printf "%s|%s" "$TMUX_PANE" "$CLAUDECODE"' > "$o/with_tmux_as_agent"
as_agent with_tmux sh -c 'printf "%s|%s" "$TMUX_PANE" "$CLAUDECODE"' > "$o/as_agent_with_tmux"
"$E2278_EGO" verify > "$o/refusal.person" 2>&1
as_agent "$E2278_EGO" verify > "$o/refusal.agent" 2>&1
printf 'x' | "$E2278_EGO" hook claude > "$o/hook.person" 2>&1
printf 'x' | as_agent "$E2278_EGO" hook claude > "$o/hook.agent" 2>&1
printf '%s' "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)" > "$o/running_from"
printf '%s' "$ENDLESS_VERIFY_DIR" > "$o/verify_dir"
report_pass "recorded"
summary
FIXTURE

# E-2: a manifest whose checks opt in per check.
cat > "$PROJ/.endless/tasks/e-2/verify.toml" <<'FIXTURE'
schema = 1
task   = "E-2"
[[check]]
runner  = "bats"
command = "if [ \"${CLAUDECODE:-}\" = 1 ] && [ \"${ENDLESS_AUDIENCE:-}\" = agent ]; then echo 'ok 1 as agent'; else echo 'not ok 1 as agent'; fi; echo 1..1"
as      = "agent"
[[check]]
runner  = "bats"
command = "if [ -z \"${CLAUDECODE:-}\" ] && [ -z \"${ENDLESS_AUDIENCE:-}\" ]; then echo 'ok 1 sibling is a person'; else echo 'not ok 1 sibling is a person'; fi; echo 1..1"
[[check]]
runner  = "bats"
command = "if [ \"$(tmux display-message -p '#{session_name}')\" = endless-verify ]; then echo 'ok 1 in the fixture pane'; else echo 'not ok 1 in the fixture pane'; fi; echo 1..1"
tmux    = true
[[check]]
runner  = "bats"
command = "if [ -z \"${TMUX:-}\" ] && [ -z \"${TMUX_PANE:-}\" ]; then echo 'ok 1 sibling is outside tmux'; else echo 'not ok 1 sibling is outside tmux'; fi; echo 1..1"
FIXTURE

# E-3: rewrites its own verify.sh in place mid-run, as an editor's save does.
cat > "$PROJ/.endless/tasks/e-3/verify.sh" <<'FIXTURE'
#!/usr/bin/env bash
{ for i in $(seq 1 200); do printf '%079d\n' 0 | tr 0 '#'; done; echo 'exit 7'; } \
    > "$ENDLESS_VERIFY_DIR/verify.sh"
echo "the original carried on"
exit 0
FIXTURE

# E-4: starts a server on the private socket, then fails.
cat > "$PROJ/.endless/tasks/e-4/verify.sh" <<'FIXTURE'
#!/usr/bin/env bash
printf '%s' "$TMUX_TMPDIR" > "$E2278_OUT/e4_tmux_tmpdir"
tmux new-session -d -s probe sh || exit 3
exit 1
FIXTURE
chmod +x "$PROJ"/.endless/tasks/e-*/verify.sh

# The two callers. Built with bash arrays, never `env $VARS` (in zsh that does
# not word-split, and the strip silently does nothing).
IDVARS=(CLAUDECODE AI_AGENT __CFBundleIdentifier ENDLESS_AUDIENCE ENDLESS_SESSION_ID TMUX TMUX_PANE TMUX_TMPDIR)
while IFS= read -r v; do IDVARS+=("$v"); done < <(env | cut -d= -f1 | grep '^CLAUDE')
UNSET=(); for v in "${IDVARS[@]}"; do UNSET+=(-u "$v"); done
PERSON=(env "${UNSET[@]}")
AGENT=(env "${UNSET[@]}" CLAUDECODE=1 CLAUDE_CODE_ENTRYPOINT=cli
       CLAUDE_CODE_SESSION_ID=6f53f9d3-c73e-4f9e-b04f-fcd3742290f1 CLAUDE_PID=4242
       AI_AGENT=claude-code_2-1-222_agent ENDLESS_AUDIENCE=agent ENDLESS_SESSION_ID=1306
       TMUX="/tmp/tmux-$(id -u)/default,1,0" TMUX_PANE=%413)

# run_as <AGENT|PERSON> <task> → the runner's exit code; outputs in $TMP/<caller>/
run_as() {
    local caller="$1" id="$2" rc=0
    local -a cmd
    if [[ "$caller" == AGENT ]]; then cmd=("${AGENT[@]}"); else cmd=("${PERSON[@]}"); fi
    mkdir -p "$TMP/$caller"
    (cd "$PROJ" && "${cmd[@]}" E2278_OUT="$TMP/$caller" E2278_EGO="$EGO" \
        "$EGO" verify "$id" >"$TMP/$caller/$id.log" 2>&1) || rc=$?
    return "$rc"
}

for caller in AGENT PERSON; do
    run_as "$caller" E-1 || setup_error "the candidate runner failed the E-1 fixture as $caller: $(tail -20 "$TMP/$caller/E-1.log")"
done
A="$TMP/AGENT" P="$TMP/PERSON"

section "Started by an agent, every check still runs as a person"
assert_eq "no caller-identity variable reached the suite" "" "$(cat "$A/person.env")"
assert_not_contains "a refusal renders in the person's form" "Handle this yourself" "$(cat "$A/refusal.person")"
assert_eq "the Claude hook gate is shut for a person (payload ignored)" "" "$(cat "$A/hook.person")"

section "as_agent gives every caller the same agent"
assert_eq "an agent started it, a person started it: the same agent env" "$(cat "$P/agent.env")" "$(cat "$A/agent.env")"
assert_contains "it is the supported harness" "CLAUDE_CODE_ENTRYPOINT=cli" "$(cat "$A/agent.env")"
assert_contains "...with the fixed fixture session, not the caller's" \
    "CLAUDE_CODE_SESSION_ID=00000000-0000-4000-8000-000000000001" "$(cat "$A/agent.env")"
assert_contains "...and the agent audience" "ENDLESS_AUDIENCE=agent" "$(cat "$A/agent.env")"
assert_not_contains "...and none of the caller's tmux" "TMUX_PANE=%413" "$(cat "$A/agent.env")"
assert_contains "a refusal renders in the agent's form" "Handle this yourself" "$(cat "$A/refusal.agent")"
assert_contains "the Claude hook gate is open (payload acted on)" "parsing payload" "$(cat "$A/hook.agent")"
assert_eq "as_agent leaks nothing into the next check" "" "$(cat "$A/after-agent.env")"

section "The same suite gives an agent and a person the same results"
same=1
for f in person.env agent.env after-agent.env refusal.person refusal.agent hook.person hook.agent; do
    if [[ "$(cat "$A/$f")" != "$(cat "$P/$f")" ]]; then
        same=0
        report_fail "the $f an agent's run saw equals a person's" "$(cat "$P/$f")" "$(cat "$A/$f")"
    fi
done
(( same )) && report_pass "every recorded result is identical from both callers"

section "A private tmux server, never the caller's"
tdir="$(cat "$A/tmux_tmpdir")"
assert_contains "TMUX_TMPDIR is the run's own, under /tmp" "/tmp/endless-verify-" "$tdir"
assert_contains "tmux by default asks the private socket, where nothing runs" "$tdir/tmux-" "$(cat "$A/list-sessions")"
IFS='|' read -r pane tmuxv shown < "$A/with_tmux"
assert_contains "with_tmux runs in the fixture pane" "endless-verify " "$shown"
assert_eq "...and TMUX_PANE names that pane" "${shown#* }" "$pane"
assert_contains "...on the private socket" "$tdir/" "$tmuxv"
assert_eq "with_tmux and as_agent combine in either order" "$(cat "$A/with_tmux_as_agent")" "$(cat "$A/as_agent_with_tmux")"
assert_eq "...giving the fixture pane and the agent" "$pane|1" "$(cat "$A/as_agent_with_tmux")"
assert_eq "the private directory is gone after the run" "gone" "$([[ -e "$tdir" ]] || echo gone)"
rc=0; run_as PERSON E-4 || rc=$?
assert_eq "a failing suite that started a server fails as itself" "1" "$rc"
t4="$(cat "$P/e4_tmux_tmpdir")"
assert_eq "...and its private server and directory are gone too" "gone" "$([[ -e "$t4" ]] || echo gone)"
assert_eq "...no server answers on that socket" "1" \
    "$(TMUX_TMPDIR="$t4" env -u TMUX tmux has-session -t probe 2>/dev/null; echo $?)"

section "verify.toml checks opt in one at a time"
rc=0; run_as AGENT E-2 || rc=$?
assert_eq "as = agent and tmux = true reach only their own check" "0" "$rc"
assert_contains "...all four checks passed" "4 passed" "$(cat "$A/E-2.log")"

section "The run executes a snapshot of verify.sh"
assert_contains "the suite ran from the run's copy" "/snapshot/.endless/tasks/e-1" "$(cat "$A/running_from")"
assert_eq "ENDLESS_VERIFY_DIR still names the real suite" \
    "$(cd "$PROJ/.endless/tasks/e-1" && pwd -P)" "$(cd "$(cat "$A/verify_dir")" && pwd -P)"
rc=0; run_as PERSON E-3 || rc=$?
assert_eq "a suite rewritten mid-run completes as the original" "0" "$rc"

summary

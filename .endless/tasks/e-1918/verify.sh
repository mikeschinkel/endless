#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1918 and records what was true when E-1918
# landed. Edit it only if you ARE E-1918. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1918 verification script — `ES-` refs at every session-ref entry point, and
# resuming a session that never claimed a task.
#
# Two threads:
#   A. `ES-<id>` — the form `task show` prints under Created:/Touched by: and the
#      form the guide tells sessions to prefer — is accepted by EVERY session-ref
#      entry point, not just the half that already honored it. Section B is the
#      drift test: one row per command surface, so adding a new session-ref
#      command means adding a row, and forgetting `ES-` fails the suite.
#      `ES-<n>` is session-EXPLICIT: it never falls back to task `<n>`'s session,
#      which is the entire point of stating the id space.
#   B. A session with no active task is resumable. It gets a container task
#      minted and claimed straight to `underway`, with the worktree `task claim`
#      would have built, so `session resume` has somewhere to land instead of
#      refusing a session whose transcript is intact.
#
# Run from anywhere inside the worktree:
#   endless task verify E-1918
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on a setup problem.
#
# Isolation: a throwaway git repo as project root under a temp dir, a temp
# XDG_CONFIG_HOME (its own DB) and XDG_CACHE_HOME, and the freshly-built worktree
# binary prepended to PATH. No real DB/ledger/cache/worktree is touched.
#
# NOTHING HERE MAY LAUNCH CLAUDE. `session resume` ends in os.execvp, so every
# check drives it through `--dry-run`, the seam E-1918 widened from the
# --review/--reopen paths to every path. A stub `claude` earlier on PATH than the
# real one records any launch, and section H fails if that record ever appears.

# Refuse a direct run, and pick up the shared harness vocabulary. Sourced as the
# FIRST executable statement so the refusal fires before anything in this file
# runs; every definition below overrides the harness's own, so a suite written
# before the harness existed behaves exactly as it did.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

# ─── output ─────────────────────────────────────────────────────────────────

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi
UNDERLINE="──────────────────────────────────────────────────────────────"

section()     { printf '\n%s%s%s\n%s\n' "${BOLD}" "$1" "${RESET}" "${UNDERLINE}"; }
report_pass() { printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"; PASS_COUNT=$((PASS_COUNT + 1)); }
report_fail() {
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"
    printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "$2"
    printf '      %sgot:%s      %s\n' "${DIM}" "${RESET}" "$3"
    FAIL_COUNT=$((FAIL_COUNT + 1)); FAILED_TESTS+=("$1")
}
summary() {
    printf '\n%sSummary%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    if [[ "${FAIL_COUNT}" -eq 0 ]]; then
        printf '  %s%d passed%s\n\n  %sALL PASSED%s\n\n' \
            "${GREEN}" "${PASS_COUNT}" "${RESET}" "${GREEN}${BOLD}" "${RESET}"
        return 0
    fi
    printf '  %s%d passed%s, %s%d failed%s\n\n  %sFAILED:%s\n' \
        "${GREEN}" "${PASS_COUNT}" "${RESET}" "${RED}" "${FAIL_COUNT}" "${RESET}" "${RED}${BOLD}" "${RESET}"
    local t; for t in "${FAILED_TESTS[@]}"; do printf '    - %s\n' "${t}"; done
    printf '\n'; return 1
}

# assert_eq DESC EXPECTED ACTUAL
assert_eq() {
    if [[ "$2" == "$3" ]]; then report_pass "$1"; else report_fail "$1" "$2" "$3"; fi
}
# assert_contains DESC NEEDLE HAYSTACK
assert_contains() {
    if [[ "$3" == *"$2"* ]]; then report_pass "$1"
    else report_fail "$1" "output contains: $2" "$3"; fi
}
# assert_not_contains DESC NEEDLE HAYSTACK
assert_not_contains() {
    if [[ "$3" != *"$2"* ]]; then report_pass "$1"
    else report_fail "$1" "output must NOT contain: $2" "$3"; fi
}

# ─── shared fixture ──────────────────────────────────────────────────────────

WT=""; TMP=""; REPO=""; DBDIR=""; LAUNCH_MARKER=""

# E: the worktree's Python CLI against the isolated DB, from the repo dir.
E() { ( cd "$REPO" && uv run --project "$WT" endless "$@" 2>&1 ); }
# E_TMUX: same, but with a $TMUX that points at no reachable server — enough for
# `session goto`'s in-tmux gate, never enough to touch the developer's own server.
E_TMUX() {
    ( cd "$REPO" \
      && TMUX="$TMP/no-such-tmux,0,0" TMUX_PANE="%999999" \
         uv run --project "$WT" endless "$@" 2>&1 )
}
# Q SQL: one scalar/row out of the isolated DB.
Q() { E sql "$1" --tsv 2>/dev/null; }
# JSON_TAIL OUTPUT: the trailing JSON object of a --dry-run render. The creation
# path narrates to stdout before the JSON, so the decision is the tail, not the
# whole capture.
JSON_TAIL() { printf '%s\n' "$1" | awk '/^\{/{f=1} f'; }
# JQ_GET JSON KEY: one top-level value, via python3 (no jq dependency).
JQ_GET() {
    printf '%s' "$1" | python3 -c '
import json, sys
try:
    print(json.load(sys.stdin).get(sys.argv[1]))
except Exception as e:
    print(f"<not JSON: {e}>")
' "$2" 2>&1
}

# Ids are fixed so every assertion can name them literally.
T_DRIFT=8001      # the drift session's claimed goal (worktree pre-made on disk)
T_NOISY=8002      # a task the task-less session touched, and the hide row's subject
T_COLLIDE=8003    # a task id that is ALSO a session id — the ES- disambiguation
S_DRIFT=8801      # task-bearing; the whole of section B runs against it
S_TASKLESS=8802   # never claimed a task — the subject of section E
S_NOPROJECT=8804  # task-less AND project-less — the residual error case
S_ONCOLLIDE=9000  # the session working task E-8003

setup_fixture() {
    # -P: macOS's mktemp hands back /var/..., a symlink to /private/var/....
    # `project register` stores what it is given, but every worktree path the
    # code produces goes through Path.resolve() — so an unresolved fixture root
    # would make correct paths compare unequal.
    TMP="$(cd "$(mktemp -d)" && pwd -P)"
    REPO="$TMP/repo"
    DBDIR="$TMP/xdg/endless"
    LAUNCH_MARKER="$TMP/claude-was-launched"
    export XDG_CONFIG_HOME="$TMP/xdg"
    export XDG_CACHE_HOME="$TMP/cache"

    # A stub `claude` ahead of everything else: if any check ever reaches the
    # exec, section H sees the marker and fails rather than a real Claude
    # session appearing in the developer's terminal mid-suite.
    mkdir -p "$TMP/stub" "$REPO" "$DBDIR"
    cat > "$TMP/stub/claude" <<EOF
#!/bin/sh
echo "STUB CLAUDE LAUNCHED: \$*" >> "$LAUNCH_MARKER"
exit 0
EOF
    chmod +x "$TMP/stub/claude"
    # Prepend the freshly-built worktree binary so the Python event bridge's
    # PATH fallback (cwd is the /tmp repo, not a self-dev worktree) execs
    # candidate code, not the stale global endless-go.
    export PATH="$TMP/stub:$WT/bin:$PATH"

    git -C "$REPO" init -q
    git -C "$REPO" symbolic-ref HEAD refs/heads/main
    git -C "$REPO" config user.email verify@test
    git -C "$REPO" config user.name verify
    git -C "$REPO" commit -q --allow-empty -m "initial commit"

    E project register "$REPO" --name probe --label Probe --desc d --lang Go --status active >/dev/null 2>&1

    local probe_id
    probe_id="$(Q "SELECT id FROM projects WHERE name='probe'")"
    [[ -n "$probe_id" ]] || return 1

    E sql "INSERT INTO tasks (id, project_id, title, status, phase) VALUES
             ($T_DRIFT,   $probe_id, 'Drift goal task',   'underway', 'now'),
             ($T_NOISY,   $probe_id, 'Noisy touched task','ready',    'now'),
             ($T_COLLIDE, $probe_id, 'Colliding id task', 'underway', 'now')" --write >/dev/null 2>&1

    # Pre-made worktrees, as bare directories: every resolver exercised outside
    # section E only asks whether the path is a directory, and a real `git
    # worktree add` here would add failure modes those sections are not about.
    # Section E is where the real creation path runs.
    mkdir -p "$REPO/.endless/worktrees/e-$T_DRIFT" \
             "$REPO/.endless/worktrees/e-$T_NOISY" \
             "$REPO/.endless/worktrees/e-$T_COLLIDE"

    # Every session here except $S_TASKLESS and $S_NOPROJECT already has a task:
    # the sections before E must not mint one as a side effect, or E's "exactly
    # one task was created" would be measuring the wrong thing.
    E sql "INSERT INTO sessions (id, session_id, project_id, state, kind_id, active_task_id) VALUES
             ($S_DRIFT,      'uuid-$S_DRIFT',      $probe_id, 'idle', 1, $T_DRIFT),
             ($S_TASKLESS,   'uuid-$S_TASKLESS',   $probe_id, 'idle', 1, NULL),
             ($S_NOPROJECT,  'uuid-$S_NOPROJECT',  NULL,      'idle', 1, NULL),
             ($S_ONCOLLIDE,  'uuid-$S_ONCOLLIDE',  $probe_id, 'idle', 1, $T_COLLIDE),
             ($T_COLLIDE,    'uuid-sess-$T_COLLIDE', $probe_id,'idle',1, $T_NOISY)" --write >/dev/null 2>&1

    # What the task-less session had already touched — the only evidence of what
    # it was doing, and what the auto-created task's description must name.
    E sql "INSERT INTO session_tasks (session_id, task_id, relation_id, created_at, updated_at) VALUES
             ($S_TASKLESS, $T_DRIFT, 3, '2026-08-07T00:00:00', '2026-08-07T00:00:00'),
             ($S_TASKLESS, $T_NOISY, 3, '2026-08-07T00:00:00', '2026-08-07T00:00:00')" --write >/dev/null 2>&1

    E sql "INSERT INTO session_messages (session_id, role, content, created_at) VALUES
             ('uuid-$S_DRIFT','user','drift-marker-message','2026-08-07T00:00:00'),
             ('uuid-$S_TASKLESS','user','taskless-marker-message','2026-08-07T00:00:00')" --write >/dev/null 2>&1

    [[ "$(Q "SELECT count(*) FROM sessions")" == "5" ]] || return 1
    [[ "$(Q "SELECT count(*) FROM session_tasks")" == "2" ]] || return 1
    return 0
}

# ─── section A: the Go resolver's ES- branch ────────────────────────────────

GO_RESUME() { NO_COLOR=1 "$WT/bin/endless-go" --config-dir "$DBDIR" \
                  session-query resume-target --ref "$1" 2>&1; }

test_go_resolver() {
    section "A. resume-target: ES- is session-explicit, and the project rides along"

    local out
    out="$(GO_RESUME "ES-$S_TASKLESS")"
    assert_contains "ES-<id> resolves to that session id" \
        "\"endless_id\":$S_TASKLESS" "$out"
    assert_contains "...carrying its project id (the task-less branch needs it)" \
        "\"project_id\":" "$out"
    assert_contains "...and its resolved project path" "$REPO" "$out"

    # The whole reason the prefix exists: $T_COLLIDE is both a task with a
    # session and a session id, and the two readings must not be confusable.
    assert_contains "ES-<n> picks session <n>, not task E-<n>'s session" \
        "\"endless_id\":$T_COLLIDE" "$(GO_RESUME "ES-$T_COLLIDE")"
    assert_contains "E-<n> still resolves task-first" \
        "\"endless_id\":$S_ONCOLLIDE" "$(GO_RESUME "E-$T_COLLIDE")"
    assert_contains "bare <n> still prefers the task" \
        "\"endless_id\":$S_ONCOLLIDE" "$(GO_RESUME "$T_COLLIDE")"
    assert_contains "bare <n> still falls back to the session id" \
        "\"endless_id\":$S_TASKLESS" "$(GO_RESUME "$S_TASKLESS")"

    # A miss on the ES- branch stays a miss — silently re-reading it as a task
    # is exactly what the prefix rules out.
    assert_contains "ES-<unknown> errors instead of falling back to a task" \
        "no session with id 999999" "$(GO_RESUME "ES-999999")"
    assert_contains "ES- with a non-integer says what ES- takes" \
        "integer session id" "$(GO_RESUME "ES-nope")"
}

# ─── section B: the drift test ───────────────────────────────────────────────
#
# One row per session-ref entry point. Each is run twice against the SAME
# session — once as `<id>`, once as `ES-<id>` — and must produce byte-identical
# output. A new session-ref command is one new row here; a command that forgets
# `ES-` fails the suite.
#
# Fields: desc | needle | env | args (REF is substituted) | reset (or -)
#
# The needle proves the bare form resolved at all, so an entry point that breaks
# for BOTH forms cannot pass by being equally broken twice.

drift_rows() {
    cat <<EOF
session show|Session E-${S_DRIFT}|-|session show REF|-
session cd|.endless/worktrees/e-${T_DRIFT}|-|session cd REF|-
session use|ENDLESS_SESSION_ID=${S_DRIFT}|-|session use REF|-
session history|drift-marker-message|-|session history REF|-
session hide --task|ES-${S_DRIFT}|-|session hide REF --task E-${T_NOISY}|session unhide REF --task E-${T_NOISY}
session goto|Session ${S_DRIFT}|tmux|session goto REF|-
session resume|"endless_id": ${S_DRIFT}|-|session resume REF --dry-run|-
EOF
}

run_ref() {  # run_ref ENVKIND ARGS...
    local envkind="$1"; shift
    if [[ "$envkind" == "tmux" ]]; then E_TMUX "$@"; else E "$@"; fi
}

test_drift() {
    section "B. Drift test: every session-ref entry point accepts ES-<id>"

    local desc needle envkind args reset bare_out es_out
    while IFS='|' read -r desc needle envkind args reset; do
        [[ -n "$desc" ]] || continue
        # shellcheck disable=SC2086  # args are fixed, space-separated tokens
        bare_out="$(run_ref "$envkind" ${args//REF/$S_DRIFT})"
        # shellcheck disable=SC2086
        [[ "$reset" == "-" ]] || run_ref "$envkind" ${reset//REF/$S_DRIFT} >/dev/null 2>&1
        # shellcheck disable=SC2086
        es_out="$(run_ref "$envkind" ${args//REF/ES-$S_DRIFT})"
        # shellcheck disable=SC2086
        [[ "$reset" == "-" ]] || run_ref "$envkind" ${reset//REF/ES-$S_DRIFT} >/dev/null 2>&1

        assert_contains "$desc — bare <id> resolves" "$needle" "$bare_out"
        assert_contains "$desc — ES-<id> resolves" "$needle" "$es_out"
        assert_eq "$desc — ES-<id> is byte-identical to <id>" "$bare_out" "$es_out"
    done < <(drift_rows)
}

# ─── section C: ES- never means the task ─────────────────────────────────────

test_es_is_session_explicit() {
    section "C. ES-<n> is the session, even when task E-<n> has one too"

    local out
    out="$(E session resume "ES-$T_COLLIDE" --dry-run)"
    assert_contains "resume ES-<n> takes session <n>" \
        "\"endless_id\": $T_COLLIDE" "$(JSON_TAIL "$out")"
    assert_not_contains "...and not task E-<n>'s session" \
        "\"endless_id\": $S_ONCOLLIDE" "$(JSON_TAIL "$out")"

    out="$(E session resume "E-$T_COLLIDE" --dry-run)"
    assert_contains "resume E-<n> is unchanged: task-first" \
        "\"endless_id\": $S_ONCOLLIDE" "$(JSON_TAIL "$out")"

    out="$(E session resume "$T_COLLIDE" --dry-run)"
    assert_contains "resume <n> is unchanged: prefers the task" \
        "\"endless_id\": $S_ONCOLLIDE" "$(JSON_TAIL "$out")"

    # goto's bare-integer branch refuses the collision outright; ES- is how you
    # say which one you meant, which is why it must reach the session resolver.
    out="$(E_TMUX session goto "$T_COLLIDE")"
    assert_contains "goto <n> refuses the ambiguity" "is ambiguous" "$out"
    out="$(E_TMUX session goto "ES-$T_COLLIDE")"
    assert_not_contains "goto ES-<n> is never ambiguous" "is ambiguous" "$out"
    assert_contains "...it names session <n>" "Session $T_COLLIDE" "$out"

    local rc
    E session resume "ES-999999" --dry-run >/dev/null 2>&1; rc=$?
    assert_eq "resume ES-<unknown> exits non-zero" "1" "$rc"
}

# ─── section D: --dry-run on the plain path, and its deprecated spelling ─────

test_dry_run_seam() {
    section "D. --dry-run works on the plain resume path; --print-decision aliases it"

    local out json
    out="$(E session resume "ES-$S_DRIFT" --dry-run)"
    json="$(JSON_TAIL "$out")"
    assert_eq "--dry-run emits the resolved uuid" \
        "uuid-$S_DRIFT" "$(JQ_GET "$json" uuid)"
    assert_eq "...the endless session id" "$S_DRIFT" "$(JQ_GET "$json" endless_id)"
    assert_eq "...the active task" "$T_DRIFT" "$(JQ_GET "$json" active_task_id)"
    assert_eq "...and no recovery happened" "False" "$(JQ_GET "$json" recovered)"

    local alias_out
    alias_out="$(E session resume "ES-$S_DRIFT" --print-decision)"
    assert_eq "--print-decision still works, identically" \
        "$json" "$(JSON_TAIL "$alias_out")"

    # E-1918 removed the "--print-decision applies only with --review/--reopen"
    # guard; the plain path is now the seam every check in this suite runs
    # through, so its absence is the precondition for all of them.
    assert_not_contains "the intent guard is gone" "applies only with" "$out"
}

# ─── section E: resuming a task-less session ─────────────────────────────────

auto_title() { printf 'Auto-resumed task for session ES-%s' "$1"; }

test_taskless_resume() {
    section "E. A task-less session gets a container task, claimed, with a worktree"

    assert_eq "PRE: the session has no active task" \
        "" "$(Q "SELECT COALESCE(active_task_id,'') FROM sessions WHERE id=$S_TASKLESS")"
    assert_eq "PRE: nothing earlier in this suite minted a container task" \
        "0" "$(Q "SELECT count(*) FROM live_tasks WHERE title LIKE 'Auto-resumed task for session%'")"

    local out json new_id title
    title="$(auto_title "$S_TASKLESS")"
    out="$(E session resume "ES-$S_TASKLESS" --dry-run)"
    json="$(JSON_TAIL "$out")"

    assert_eq "exactly one task was created for it" \
        "1" "$(Q "SELECT count(*) FROM live_tasks WHERE title='$title'")"
    new_id="$(Q "SELECT id FROM live_tasks WHERE title='$title'")"
    if [[ -z "$new_id" ]]; then
        report_fail "the auto-created task is findable by title" "$title" "$out"
        return
    fi
    report_pass "the auto-created task is titled '$title'"

    assert_eq "...created straight at 'underway'" \
        "underway" "$(Q "SELECT status FROM live_tasks WHERE id=$new_id")"
    assert_eq "...and claimed by the resumed session" \
        "$new_id" "$(Q "SELECT active_task_id FROM sessions WHERE id=$S_TASKLESS")"

    # The description is what keeps the placeholder from being contentless.
    local desc
    desc="$(Q "SELECT description FROM live_tasks WHERE id=$new_id")"
    assert_contains "the description names the session" "ES-$S_TASKLESS" "$desc"
    assert_contains "...and the tasks it had already touched" \
        "E-$T_DRIFT, E-$T_NOISY" "$desc"

    # A real worktree, on disk, from task claim's own creation path.
    if [[ -d "$REPO/.endless/worktrees/e-$new_id" ]]; then
        report_pass "the worktree exists on disk"
    else
        report_fail "the worktree exists on disk" \
            "$REPO/.endless/worktrees/e-$new_id" "missing"
    fi
    assert_eq "--dry-run resumes into that worktree" \
        "$REPO/.endless/worktrees/e-$new_id" "$(JQ_GET "$json" worktree)"
    assert_eq "...and says a task was created" "True" "$(JQ_GET "$json" created_task)"
    assert_eq "...naming which" "$new_id" "$(JQ_GET "$json" active_task_id)"

    # Idempotency: the claim set active_task_id, so a second resume takes the
    # ordinary task path. Asserted rather than trusted — a future refactor that
    # starts minting a task per resume must fail here.
    local again
    again="$(JSON_TAIL "$(E session resume "ES-$S_TASKLESS" --dry-run)")"
    assert_eq "resuming twice creates exactly one task" \
        "1" "$(Q "SELECT count(*) FROM live_tasks WHERE title='$title'")"
    assert_eq "...the second resume creates nothing" \
        "False" "$(JQ_GET "$again" created_task)"
    assert_eq "...and lands in the same worktree" \
        "$REPO/.endless/worktrees/e-$new_id" "$(JQ_GET "$again" worktree)"
}

test_task_bearing_unchanged() {
    section "F. A task-bearing session still resumes into its own worktree"

    local before after json
    before="$(Q "SELECT count(*) FROM live_tasks")"
    json="$(JSON_TAIL "$(E session resume "ES-$S_DRIFT" --dry-run)")"
    after="$(Q "SELECT count(*) FROM live_tasks")"

    assert_eq "no task is created for a session that has one" "$before" "$after"
    assert_eq "...it resumes into the task's own worktree" \
        "$REPO/.endless/worktrees/e-$T_DRIFT" "$(JQ_GET "$json" worktree)"
    assert_eq "...and says so" "False" "$(JQ_GET "$json" created_task)"
}

# ─── section G: the residual error case ──────────────────────────────────────

test_projectless_session() {
    section "G. A project-less session still errors — and names the UUID"

    local out rc
    out="$(E session resume "ES-$S_NOPROJECT" --dry-run)"; rc=$?
    assert_eq "it exits non-zero" "1" "$rc"
    assert_contains "...saying there is nowhere to put a task" \
        "no registered project" "$out"
    assert_contains "...and naming the UUID so a manual resume is one paste away" \
        "claude --resume uuid-$S_NOPROJECT" "$out"
    assert_eq "...having created nothing" \
        "0" "$(Q "SELECT count(*) FROM live_tasks WHERE title='$(auto_title "$S_NOPROJECT")'")"

    # The no-UUID diagnostic keeps its own parenthetical, and keeps its priority:
    # a dispatch row with no transcript has nothing to resume, so nothing is
    # minted for it either.
    E sql "INSERT INTO sessions (id, session_id, project_id, state, kind_id, active_task_id)
             SELECT 8899, NULL, id, 'idle', 1, NULL FROM projects WHERE name='probe'" --write >/dev/null 2>&1
    out="$(E session resume "ES-8899" --dry-run)"
    assert_contains "a session with no UUID is still refused" "no Claude UUID" "$out"
    assert_contains "...with the background-agent parenthetical it was written for" \
        "never started" "$out"
    assert_eq "...and no task minted for it" \
        "0" "$(Q "SELECT count(*) FROM live_tasks WHERE title='$(auto_title 8899)'")"
}

# ─── section H: nothing launched Claude; unit + regression suites ────────────

test_no_launch_and_suites() {
    section "H. No check launched Claude; Go + Python suites"

    if [[ -f "$LAUNCH_MARKER" ]]; then
        report_fail "no check exec'd claude" "no launch" "$(cat "$LAUNCH_MARKER")"
    else
        report_pass "no check exec'd claude (--dry-run stops short of the exec)"
    fi

    local out rc
    out=$(cd "$WT" && go test ./internal/monitor/ ./internal/sessionquerycmd/ 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then report_pass "go test monitor + sessionquerycmd passes"
    else report_fail "go test monitor + sessionquerycmd" "exit 0" "exit=$rc"$'\n'"$(printf '%s' "$out" | tail -20)"; fi

    out=$(cd "$WT" && uv run pytest tests/test_session_resume_taskless.py \
            tests/test_session_resume_recover.py tests/test_session_resolver.py -q 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then report_pass "pytest resume-taskless + resume-recover + resolver suites pass"
    else report_fail "pytest resume suites" "exit 0" "exit=$rc"$'\n'"$(printf '%s' "$out" | tail -20)"; fi
}

# ─── main ─────────────────────────────────────────────────────────────────────

main() {
    WT="$(git rev-parse --show-toplevel 2>/dev/null)"
    [[ -n "${WT}" ]] || { printf 'ERROR: not inside a git worktree\n' >&2; exit 2; }

    command -v go >/dev/null      || { printf 'ERROR: go not on PATH\n' >&2; exit 2; }
    command -v uv >/dev/null      || { printf 'ERROR: uv not on PATH\n' >&2; exit 2; }
    command -v git >/dev/null     || { printf 'ERROR: git not on PATH\n' >&2; exit 2; }
    command -v python3 >/dev/null || { printf 'ERROR: python3 not on PATH\n' >&2; exit 2; }
    [[ -x "${WT}/bin/endless-go" ]] || {
        printf 'ERROR: %s/bin/endless-go missing — run `just build`\n' "${WT}" >&2; exit 2; }

    printf '%sE-1918 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  worktree: %s\n' "${WT}"

    if ! setup_fixture; then
        printf 'ERROR: fixture setup failed (isolated DB seeding)\n' >&2
        [[ -n "$TMP" ]] && rm -rf "$TMP"
        exit 2
    fi

    test_go_resolver
    test_drift
    test_es_is_session_explicit
    test_dry_run_seam
    test_taskless_resume
    test_task_bearing_unchanged
    test_projectless_session
    test_no_launch_and_suites

    [[ -n "$TMP" ]] && rm -rf "$TMP"
    summary
}

main "$@"

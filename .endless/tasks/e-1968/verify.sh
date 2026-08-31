#!/usr/bin/env bash
#
# E-1968 verification script — the session/task movement verbs account for task
# state, and stop editing `sessions.active_task_id` behind the user's back.
#
# One root, six symptoms. Each section below is one of them:
#
#   A. `task reopen` no longer releases the session→task binding. It used to,
#      silently, with a --help line ("no session binding") that read as "does not
#      create one". Under ED-1560 that column is write-once, so there is nothing
#      to gate and nothing to announce — the release is simply gone.
#   B. `task release` is disabled: its defining act is the clear the invariant
#      forbids. Kept as a tombstone that answers with the invariant and a route.
#   C. `task bind` is first-set-only: it may fill an empty session, never move a
#      bound one.
#   D. `task pause` is retired. Pausing on the epic-revisit gate is declining to
#      clear it; the verb existed only to carry the unbind.
#   E. `task spawn --reopen` (with --new-session / --print-decision) is retired,
#      and spawn's done-ish refusal routes to `session goto --resume --revisit`
#      instead of the `task reopen` first dance that walked the user around the
#      very guard that produced the message.
#   F. `session goto --resume` requires --revisit / --no-revisit on settled work,
#      and `session resume` requires --force to clobber a pane holding a task.
#
# Section 0 runs this task's unit suites FAIL-FAST: if the Go or Python tests are
# red, the CLI-level sections below are measuring nothing, so the run stops there.
#
# Run from anywhere inside the worktree:
#   endless task verify E-1968
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on a setup problem.
#
# Isolation: a throwaway git repo as project root under a temp dir, a temp
# XDG_CONFIG_HOME (its own DB) and XDG_CACHE_HOME, and the freshly-built worktree
# binary prepended to PATH. No real DB/ledger/cache/worktree is touched.
#
# NOTHING HERE MAY LAUNCH CLAUDE. `session resume` ends in os.execvp and
# `session goto --resume` opens a tmux window; a stub `claude` earlier on PATH
# records any launch, $TMUX points at a server that does not exist, and the final
# section fails if the marker ever appears.

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

WT=""; TMP=""; REPO=""; DBDIR=""; LAUNCH_MARKER=""; PROJ_ID=""

# E: the worktree's Python CLI against the isolated DB, from the repo dir.
E() { ( cd "$REPO" && uv run --project "$WT" endless "$@" 2>&1 ); }
# E_AS SID ...: the same, but claiming to be running inside session SID. That is
# layer 1 of `_current_endless_session_id`, which is what the `session resume`
# clobber gate and `task bind` read to find "this pane's" session.
E_AS() { local sid="$1"; shift; ( cd "$REPO" && ENDLESS_SESSION_ID="$sid" \
           uv run --project "$WT" endless "$@" 2>&1 ); }
# E_TMUX: a $TMUX pointing at no reachable server — enough to satisfy the
# in-tmux gates on `session goto` and `task spawn`, never enough to reach the
# developer's own tmux server.
E_TMUX() {
    ( cd "$REPO" \
      && TMUX="$TMP/no-such-tmux,0,0" TMUX_PANE="%999999" \
         uv run --project "$WT" endless "$@" 2>&1 )
}
# Q SQL: one scalar/row out of the isolated DB.
Q() { E sql "$1" --tsv 2>/dev/null; }

# Ids are fixed so every assertion can name them literally.
T_BOUND=9101      # assumed, with a live-ish session bound — section A's subject
T_SPAWN=9102      # assumed, unbound — section E's spawn refusal
T_UNVER=9103      # unverified — the force-set branch spawn must NOT reword
T_OTHER=9104      # a second task, for bind's repoint refusal
T_DECL=9105       # declined — --revisit must refuse to revive a decision
S_BOUND=9801      # the session holding T_BOUND
S_EMPTY=9802      # a session holding no task (bind may fill it)

setup_fixture() {
    # -P: macOS's mktemp hands back /var/..., a symlink to /private/var/....
    TMP="$(cd "$(mktemp -d)" && pwd -P)"
    REPO="$TMP/repo"
    DBDIR="$TMP/xdg/endless"
    LAUNCH_MARKER="$TMP/claude-was-launched"
    export XDG_CONFIG_HOME="$TMP/xdg"
    export XDG_CACHE_HOME="$TMP/cache"

    mkdir -p "$TMP/stub" "$REPO" "$DBDIR"
    cat > "$TMP/stub/claude" <<EOF
#!/bin/sh
echo "STUB CLAUDE LAUNCHED: \$*" >> "$LAUNCH_MARKER"
exit 0
EOF
    chmod +x "$TMP/stub/claude"
    export PATH="$TMP/stub:$WT/bin:$PATH"

    git -C "$REPO" init -q
    git -C "$REPO" symbolic-ref HEAD refs/heads/main
    git -C "$REPO" config user.email verify@test
    git -C "$REPO" config user.name verify
    git -C "$REPO" commit -q --allow-empty -m "initial commit"

    E project register "$REPO" --name probe --label Probe --desc d --lang Go --status active >/dev/null 2>&1

    PROJ_ID="$(Q "SELECT id FROM projects WHERE name='probe'")"
    [[ -n "$PROJ_ID" ]] || return 1

    E sql "INSERT INTO tasks (id, project_id, title, status, phase) VALUES
             ($T_BOUND, $PROJ_ID, 'Bound settled task', 'assumed',    'now'),
             ($T_SPAWN, $PROJ_ID, 'Unbound settled task','assumed',   'now'),
             ($T_UNVER, $PROJ_ID, 'Awaiting verify',    'unverified', 'now'),
             ($T_OTHER, $PROJ_ID, 'Some other task',    'ready',      'now'),
             ($T_DECL,  $PROJ_ID, 'Decided against',    'declined',   'now')" --write >/dev/null 2>&1

    # Bare worktree dirs: the resolvers exercised here only ask whether the path
    # is a directory, and a real `git worktree add` would add failure modes none
    # of these sections is about.
    mkdir -p "$REPO/.endless/worktrees/e-$T_BOUND" \
             "$REPO/.endless/worktrees/e-$T_SPAWN"

    E sql "INSERT INTO sessions (id, session_id, project_id, state, kind_id, active_task_id) VALUES
             ($S_BOUND, 'uuid-$S_BOUND', $PROJ_ID, 'ended', 1, $T_BOUND),
             ($S_EMPTY, 'uuid-$S_EMPTY', $PROJ_ID, 'idle',  1, NULL)" --write >/dev/null 2>&1

    [[ "$(Q "SELECT count(*) FROM tasks")" == "5" ]] || return 1
    [[ "$(Q "SELECT active_task_id FROM sessions WHERE id=$S_BOUND")" == "$T_BOUND" ]] || return 1
    return 0
}

# ─── section 0: the unit suites, fail-fast ───────────────────────────────────

test_suites_fail_fast() {
    section "0. Unit suites (fail-fast — nothing below means anything if these are red)"

    local out rc
    out=$(cd "$WT" && go test ./internal/... 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then report_pass "go test ./internal/... passes"
    else
        report_fail "go test ./internal/..." "exit 0" "exit=$rc"$'\n'"$(printf '%s' "$out" | grep -E '^(---|FAIL|\s+---)' | head -20)"
        return 1
    fi

    out=$(cd "$WT" && uv run pytest \
            tests/test_task_reopen.py \
            tests/test_task_release.py \
            tests/test_task_bind_write_once.py \
            tests/test_revisit_verbs.py \
            tests/test_session_goto_back.py \
            tests/test_session_resume_clobber_gate.py \
            tests/test_session_resume_recover.py \
            -q 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then report_pass "pytest E-1968 suites pass"
    else
        report_fail "pytest E-1968 suites" "exit 0" "exit=$rc"$'\n'"$(printf '%s' "$out" | tail -20)"
        return 1
    fi
    return 0
}

# ─── section A: task reopen leaves the binding alone ─────────────────────────

test_reopen_keeps_binding() {
    section "A. \`task reopen\` changes task state and nothing else"

    local before after out
    before="$(Q "SELECT active_task_id FROM sessions WHERE id=$S_BOUND")"
    assert_eq "precondition: session $S_BOUND holds E-$T_BOUND" "$T_BOUND" "$before"

    out="$(E task reopen "E-$T_BOUND")"
    assert_contains "reopen reports the status move" "assumed -> revisit" "$out"
    assert_eq "task is now revisit" "revisit" \
        "$(Q "SELECT status FROM tasks WHERE id=$T_BOUND")"

    after="$(Q "SELECT active_task_id FROM sessions WHERE id=$S_BOUND")"
    assert_eq "the session→task binding SURVIVED the reopen" "$T_BOUND" "$after"

    # The whole loss mechanism: a `task.released` ledger entry is what NULLed
    # the column. Reopen must not emit one at all — checked against the ledger
    # itself, which is the durable record the DB is projected from.
    local ledger released changed
    ledger="$(cat "$REPO"/.endless/db-ledger/*.jsonl 2>/dev/null)"
    # Sanity first: prove we are reading the ledger this reopen actually wrote,
    # so the negative below is evidence rather than an empty-file tautology.
    changed="$(printf '%s' "$ledger" | grep -c 'task.status_changed' || true)"
    if [[ "$changed" -ge 1 ]]; then
        report_pass "the reopen's ledger entries are where we are looking"
    else
        report_fail "the reopen's ledger entries are where we are looking" \
            "at least one task.status_changed entry" "found $changed"
    fi
    released="$(printf '%s' "$ledger" | grep -c 'task.released' || true)"
    assert_eq "reopen appended no task.released ledger entry" "0" "$released"

    # ...and the help no longer says the opposite of what it does.
    local help_out
    help_out="$(E task reopen --help)"
    assert_not_contains "--help dropped the misleading 'no session binding'" \
        "no session binding" "$help_out"
    assert_contains "--help states the binding is left intact" \
        "INTACT" "$help_out"
    assert_contains "--help names the route back into the session" \
        "session goto" "$help_out"

    # Put it back for the sections that follow.
    E sql "UPDATE tasks SET status='assumed' WHERE id=$T_BOUND" --write >/dev/null 2>&1
}

# ─── section B: task release is a tombstone ──────────────────────────────────

test_release_disabled() {
    section "B. \`task release\` is disabled, not deleted"

    local out
    out="$(E_AS "$S_BOUND" task release)"
    assert_contains "bare release refuses" "deliberately disabled" "$out"
    assert_contains "...naming the invariant" "one session, one task" "$out"
    assert_contains "...and the hand-back route" "--status revisit" "$out"

    out="$(E task release "E-$T_BOUND")"
    assert_contains "release E-NNN refuses too" "deliberately disabled" "$out"

    out="$(E task release "E-$T_BOUND" --ignore-missing)"
    assert_contains "--ignore-missing is no escape hatch" "deliberately disabled" "$out"

    assert_eq "no refusal touched the binding" "$T_BOUND" \
        "$(Q "SELECT active_task_id FROM sessions WHERE id=$S_BOUND")"

    # A tombstone, not a deletion: the verb must still resolve.
    assert_contains "the command is still wired (answers, not 'no such command')" \
        "release" "$(E task --help)"
}

# ─── section C: bind is first-set-only ───────────────────────────────────────

test_bind_first_set_only() {
    section "C. \`task bind\` fills an empty session, never moves a bound one"

    local out
    out="$(E_AS "$S_EMPTY" task bind "E-$T_OTHER")"
    assert_contains "bind fills a session holding no task" "bound to session" "$out"
    assert_eq "...and the binding landed" "$T_OTHER" \
        "$(Q "SELECT active_task_id FROM sessions WHERE id=$S_EMPTY")"

    out="$(E_AS "$S_EMPTY" task bind "E-$T_SPAWN")"
    assert_contains "re-pointing a bound session refuses" "already holds" "$out"
    assert_contains "...naming what it holds" "E-$T_OTHER" "$out"
    assert_contains "...and the route for the other work" "task spawn E-$T_SPAWN" "$out"
    assert_eq "...and the binding did NOT move" "$T_OTHER" \
        "$(Q "SELECT active_task_id FROM sessions WHERE id=$S_EMPTY")"

    out="$(E_AS "$S_EMPTY" task bind "E-$T_OTHER")"
    assert_contains "re-binding the SAME task is a no-op, not an error" \
        "already bound" "$out"
}

# ─── section D: task pause is retired ────────────────────────────────────────

test_pause_retired() {
    section "D. \`task pause\` is gone; pausing means running nothing"

    local out
    out="$(E task --help)"
    assert_not_contains "task pause is no longer a command" " pause " "$out"
    assert_contains "task continue survives (it is what clears the gate)" \
        "continue" "$out"

    # The gate's prompt is the only place the verbs were advertised.
    out="$(cd "$WT" && grep -c 'endless task pause' internal/hookcmd/claude.go || true)"
    assert_eq "the revisit gate no longer offers a pause verb" "0" "$out"
}

# ─── section E: spawn --reopen is retired, and its refusal reworded ──────────

test_spawn_reopen_retired() {
    section "E. \`task spawn --reopen\` is retired; the done-ish refusal routes to the session"

    local out
    for flag in --reopen --new-session --print-decision; do
        out="$(E_TMUX task spawn "E-$T_SPAWN" "$flag")"
        assert_contains "spawn $flag answers instead of parse-erroring" "retired" "$out"
        assert_contains "spawn $flag names the route" \
            "endless session goto E-$T_SPAWN --resume --revisit" "$out"
        assert_not_contains "spawn $flag is not a click parse error" \
            "no such option" "$out"
    done

    # §6: the reopenable-terminal branch of the done-ish gate.
    out="$(E_TMUX task spawn "E-$T_SPAWN")"
    assert_contains "plain spawn on 'assumed' names the status" "assumed" "$out"
    assert_contains "...and routes to the session that did the work" \
        "endless session goto E-$T_SPAWN --resume --revisit" "$out"
    assert_not_contains "...and no longer offers \`task reopen\` first (the trap)" \
        "endless task reopen" "$out"
    assert_not_contains "...nor the retired flag" "--reopen" "$out"
    assert_eq "the refusal changed no status" "assumed" \
        "$(Q "SELECT status FROM tasks WHERE id=$T_SPAWN")"

    # The OTHER branch — force-set statuses that are not reopenable — keeps its
    # existing wording. Rewording both would have lost the distinction.
    out="$(E_TMUX task spawn "E-$T_UNVER")"
    assert_contains "plain spawn on 'unverified' still points at --force" "--force" "$out"
    assert_not_contains "...and not at --revisit" "--revisit" "$out"
}

# ─── section F: the navigation verbs say what they intend ────────────────────

test_navigation_intent() {
    section "F. \`session goto --resume\` states intent; \`session resume\` guards the pane"

    local out
    # No flag on settled work → refuse, naming both options.
    out="$(E_TMUX session goto "E-$T_BOUND" --resume)"
    assert_contains "settled target refuses without an intent" "is 'assumed'" "$out"
    assert_contains "...offering --revisit" "--revisit" "$out"
    assert_contains "...and --no-revisit" "--no-revisit" "$out"
    assert_eq "the refusal changed no status" "assumed" \
        "$(Q "SELECT status FROM tasks WHERE id=$T_BOUND")"

    # --no-revisit: opens (it dies at the unreachable tmux server, which is
    # AFTER the status decision) and leaves the status alone.
    E_TMUX session goto "E-$T_BOUND" --resume --no-revisit >/dev/null 2>&1
    assert_eq "--no-revisit left the status alone" "assumed" \
        "$(Q "SELECT status FROM tasks WHERE id=$T_BOUND")"

    # --revisit: flips the task before it reaches the window.
    E_TMUX session goto "E-$T_BOUND" --resume --revisit >/dev/null 2>&1
    assert_eq "--revisit flipped the task to revisit" "revisit" \
        "$(Q "SELECT status FROM tasks WHERE id=$T_BOUND")"
    assert_eq "...without disturbing the binding" "$T_BOUND" \
        "$(Q "SELECT active_task_id FROM sessions WHERE id=$S_BOUND")"

    # An unsettled task needs no flag — the gate must not become friction.
    out="$(E_TMUX session goto "E-$T_BOUND" --resume)"
    assert_not_contains "a 'revisit' task needs no intent flag" "Say what you intend" "$out"

    # The flags are resume-path modifiers.
    out="$(E_TMUX session goto "E-$T_BOUND" --revisit)"
    assert_contains "--revisit without --resume is refused" "only with --resume" "$out"

    # `session resume` from a pane holding live work.
    out="$(E_AS "$S_BOUND" session resume "E-$T_SPAWN")"
    assert_contains "resume refuses to clobber a pane holding a task" \
        "This pane is working E-$T_BOUND" "$out"
    assert_contains "...pointing at the new-window route" \
        "endless session goto E-$T_SPAWN --resume" "$out"
    assert_contains "...and offering --force" "--force" "$out"

    # A pane with no task bound is not gated (the ordinary recovery case).
    out="$(E_AS "$S_EMPTY" session resume "ES-$S_EMPTY" --dry-run)"
    assert_not_contains "a pane with no task is not gated" "would replace" "$out"
}

# ─── section G: nothing launched Claude ──────────────────────────────────────

test_no_launch() {
    section "G. No check launched Claude"

    if [[ -f "$LAUNCH_MARKER" ]]; then
        report_fail "no check exec'd claude" "no launch" "$(cat "$LAUNCH_MARKER")"
    else
        report_pass "no check exec'd claude"
    fi
}

# ─── main ─────────────────────────────────────────────────────────────────────

main() {
    WT="$(git rev-parse --show-toplevel 2>/dev/null)"
    [[ -n "${WT}" ]] || { printf 'ERROR: not inside a git worktree\n' >&2; exit 2; }

    command -v go >/dev/null      || { printf 'ERROR: go not on PATH\n' >&2; exit 2; }
    command -v uv >/dev/null      || { printf 'ERROR: uv not on PATH\n' >&2; exit 2; }
    command -v git >/dev/null     || { printf 'ERROR: git not on PATH\n' >&2; exit 2; }
    [[ -x "${WT}/bin/endless-go" ]] || {
        printf 'ERROR: %s/bin/endless-go missing — run `just build`\n' "${WT}" >&2; exit 2; }

    printf '%sE-1968 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  worktree: %s\n' "${WT}"

    if ! test_suites_fail_fast; then
        printf '\n%sUnit suites failed — stopping before the CLI sections.%s\n\n' \
            "${RED}${BOLD}" "${RESET}"
        summary
        exit 1
    fi

    if ! setup_fixture; then
        printf 'ERROR: fixture setup failed (isolated DB seeding)\n' >&2
        [[ -n "$TMP" ]] && rm -rf "$TMP"
        exit 2
    fi

    test_reopen_keeps_binding
    test_release_disabled
    test_bind_first_set_only
    test_pause_retired
    test_spawn_reopen_retired
    test_navigation_intent
    test_no_launch

    [[ -n "$TMP" ]] && rm -rf "$TMP"
    summary
}

main "$@"

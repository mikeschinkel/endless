#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2018 and records what was true when E-2018
# landed. Edit it only if you ARE E-2018. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2018 verification — the documented status lifecycle is enforced on
# `task update`, and the diagram that documents it is generated from the rule.
#
# The defect: `task update --status` accepted any status, from any status, from
# any actor, whether or not a session had ever claimed the task. Observed
# 2026-08-20 — a session holding one task set an `unplanned`, never-claimed task
# to `unverified`, so `session goto` on it found nothing to go to. Both missing
# checks were computable from data Endless already had. Meanwhile the lifecycle
# diagram was byte-synced across three documents by a test while nothing checked
# the lifecycle itself, and the picture had drifted from the CLI in two places
# nobody could see.
#
# The fix inverts the direction: a transition table in internal/taskstatus is the
# source, docs/status-lifecycle.mmd is rendered FROM it, and the executor
# validates against it. `blocked` left the status vocabulary on the way past —
# blockedness is the `blocked_by` relation and always was.
#
# What this script covers is ACCEPTANCE — the point-in-time claims of this task.
# The PERMANENT invariants live where they belong and run forever:
# internal/taskstatus/transitions_test.go (the table's structural invariants and
# the byte-stable render), internal/events/status_transition_test.go (both
# guards, walked over the table), internal/taskstatuscmd (the two new verbs),
# tests/test_status_lifecycle_sync.py (the committed artifact is current) and
# tests/test_status_lifecycle_gate.py (the refusal reaches the CLI cleanly).
# See E-1889's convention.
#
# Run from anywhere inside the worktree:
#   endless task verify E-2018
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on a setup problem.
#
# Fail-fast: section A runs this task's own unit tests (Go + Python) FIRST and
# aborts the whole run if they fail — no point exercising end-to-end behavior
# built on a broken unit.
#
# Isolation: a throwaway git repo as project root under a temp dir, a temp
# XDG_CONFIG_HOME (its own DB) and XDG_CACHE_HOME. No real DB/ledger/cache is
# touched. Harness shape borrowed from .endless/tasks/e-1891/verify.sh.
#
# Harness determinism: the actor half of the guard keys on whether an AGENT
# harness emitted the event, so every check that depends on it sets or clears
# CLAUDE_CODE_ENTRYPOINT explicitly rather than inheriting whatever shell this
# runs in. The script therefore gives the same answer run by a person and run by
# a Claude session.

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

WT=""; TMP=""; REPO=""; CFG=""

# E: the worktree's Python CLI against the isolated DB, as a PERSON — no agent
# harness. Transition legality still applies to a person; actor reality does not.
E() {
    ( cd "$REPO" && env -u CLAUDE_CODE_ENTRYPOINT -u __CFBundleIdentifier \
        uv run --project "$WT" endless "$@" )
}
# TS: the worktree's `endless-go task-status`.
TS() { "$WT/bin/endless-go" task-status "$@"; }
# ST TASK_ID: current status of a task in the isolated DB.
ST() { E sql "SELECT status FROM tasks WHERE id=$1" --tsv 2>/dev/null; }
# LAST_ID: id of the most recently inserted task.
LAST_ID() { E sql "SELECT id FROM tasks ORDER BY id DESC LIMIT 1" --tsv 2>/dev/null; }
# add_task TITLE [extra args...]: add a task; echoes its id.
add_task() {
    local title="$1"; shift
    E task add "$title" --description "spec" "$@" >/dev/null 2>&1
    LAST_ID
}
# force_status TASK_ID STATUS: a raw write, bypassing the executor. Used only to
# PLACE a task in a status, never to assert one.
force_status() {
    E sql "UPDATE tasks SET status='$2' WHERE id=$1" --write >/dev/null 2>&1
}

# EMIT_OUT / EMIT_RC hold the last direct emit's output and exit code.
EMIT_OUT=""; EMIT_RC=0

# emit_status HARNESS SESSION_ID TASK_ID STATUS — drive execTaskFieldsUpdated
# directly, with full control of the actor. HARNESS is "agent" or "human".
#
# Direct rather than through the Python CLI because the actor half of the guard
# reads fields the CLI resolves from the ambient environment (which session am I
# in, is there a harness) — and a check whose setup is "whatever shell you
# happen to be in" is not a check.
emit_status() {
    local harness="$1" session="$2" task="$3" status="$4"
    local -a env_args=()
    if [[ "$harness" == "agent" ]]; then
        env_args=(CLAUDE_CODE_ENTRYPOINT=cli)
    else
        env_args=(-u CLAUDE_CODE_ENTRYPOINT -u __CFBundleIdentifier)
    fi
    EMIT_OUT=$(env "${env_args[@]}" "$WT/bin/endless-go" --config-dir "$CFG" event emit \
        --kind task.fields_updated --project probe \
        --entity-type task --entity-id "$task" \
        --actor-kind cli --actor-id e2018-verify --node-id a7f3 \
        --session-id "$session" --project-root "$REPO" \
        --payload "{\"fields\":{\"status\":\"$status\"}}" 2>&1)
    EMIT_RC=$?
}

# seed_session SESSION_ID TASK_ID — a sessions row holding a task. 0 means the
# session has claimed nothing.
seed_session() {
    local bound="$2"
    [[ "$bound" == "0" ]] && bound="NULL"
    E sql "INSERT INTO sessions (id, session_id, project_id, task_id, started_at)
           VALUES ($1, 'sess-$1', 1, $bound, '2026-08-20T00:00:00')" --write >/dev/null 2>&1
}

setup_fixture() {
    TMP="$(mktemp -d)"
    REPO="$TMP/repo"
    export XDG_CONFIG_HOME="$TMP/xdg"
    export XDG_CACHE_HOME="$TMP/cache"
    CFG="$XDG_CONFIG_HOME/endless"
    # Prepend the freshly-built worktree binary so the Python event bridge's
    # PATH fallback (cwd is the /tmp repo, not a self-dev worktree) execs
    # candidate code, not the stale global endless-go — which knows neither the
    # guard nor the two new task-status verbs.
    export PATH="$WT/bin:$PATH"
    mkdir -p "$REPO" "$CFG"

    git -C "$REPO" init -q
    git -C "$REPO" config user.email verify@test
    git -C "$REPO" config user.name verify
    git -C "$REPO" commit -q --allow-empty -m "initial commit"

    E project register "$REPO" --name probe --label Probe --desc d --lang Go --status active >/dev/null 2>&1

    [[ "$(E sql 'SELECT count(*) FROM projects' --tsv 2>/dev/null)" == "1" ]] || return 1
    return 0
}

# ─── section A: unit tests (FAIL-FAST GATE) ──────────────────────────────────

test_units() {
    section "A. Unit tests for the new behavior (fail-fast gate)"
    local out rc

    out=$(cd "$WT" && go test ./internal/taskstatus/ ./internal/taskstatuscmd/ ./internal/events/ 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then
        report_pass "go test taskstatus + taskstatuscmd + events passes"
    else
        report_fail "go test taskstatus + taskstatuscmd + events" \
            "exit 0" "exit=$rc"$'\n'"$(printf '%s' "$out" | grep -v '^ok' | head -25)"
    fi

    out=$(cd "$WT" && uv run pytest tests/test_status_lifecycle_gate.py \
        tests/test_status_lifecycle_sync.py -q 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then
        report_pass "pytest lifecycle gate + artifact-currency suites pass"
    else
        report_fail "pytest lifecycle suites" "exit 0" \
            "exit=$rc"$'\n'"$(printf '%s' "$out" | tail -25)"
    fi

    if [[ "${FAIL_COUNT}" -gt 0 ]]; then
        printf '\n  %sABORTING%s — unit tests failed; skipping end-to-end checks.\n' \
            "${RED}${BOLD}" "${RESET}"
        return 1
    fi
    return 0
}

# ─── section B: the reported case ────────────────────────────────────────────

test_reported_case() {
    section "B. The reported case is refused end-to-end"
    # `endless task update E-2015 --status unverified` on a task that was
    # `unplanned` and had never been claimed. Asserted through the Python CLI,
    # which is where it was typed.
    local t out
    t="$(add_task "Add the reported case")"
    force_status "$t" "unplanned"

    out="$(E task update "E-$t" --status unverified 2>&1)"
    assert_contains "the refusal names the illegal edge" "unplanned" "$out"
    assert_contains "and names the status that was asked for" "unverified" "$out"
    assert_eq "the write did not land" "unplanned" "$(ST "$t")"
    assert_not_contains "and it is a clean CLI error, not a traceback" \
        "Traceback" "$out"

    # The refusal carries the caller's next move — the reason it lists the
    # reachable set rather than only saying no.
    local reachable
    for reachable in submitted ready underway revisit; do
        assert_contains "it offers '$reachable' as a legal next step" "$reachable" "$out"
    done

    # There is no --force. E-1577/E-1579's precedent: the fix is to correct the
    # call, not to override a correctness invariant.
    out="$(E task update "E-$t" --status unverified --force 2>&1)"
    assert_eq "--force does not buy a way past it" "unplanned" "$(ST "$t")"
}

# ─── section C: `blocked` left the vocabulary ────────────────────────────────

test_blocked_is_gone() {
    section "C. 'blocked' is not a status"
    # It was a status once and a handful of rows carried it, but blockedness is
    # the `blocked_by` relation — `endless task block` has only ever written a
    # relation, and the guide's blocking-semantics table computes a dependent's
    # fate from its blocker's own status.
    local out group
    out="$(TS get all)"
    assert_not_contains "the vocabulary does not contain it" "blocked" "$out"

    # Every group, walked — a stale membership is the omission-shaped failure
    # the registry exists to prevent, so this cannot be a hand-written list.
    local stale=""
    for group in $(TS groups); do
        if TS get "$group" | grep -qx "blocked"; then stale="$stale $group"; fi
    done
    assert_eq "no group still lists it" "" "$stale"

    assert_not_contains "the generated diagram does not draw it" \
        "blocked" "$(TS lifecycle)"

    out="$(E task update "E-$(add_task "Add a blockable thing")" --status blocked 2>&1)"
    assert_contains "the CLI refuses it as an unknown status" "Invalid status" "$out"

    # And the relation it was confused with still works, untouched.
    local a b
    a="$(add_task "Add a blocked thing")"; b="$(add_task "Add a blocker")"
    E task block "E-$a" --by "E-$b" >/dev/null 2>&1
    assert_contains "'task block' still records the relation" \
        "E-$b" "$(E task show "E-$a" 2>&1)"
    assert_eq "and leaves the blocked task's status alone" "untriaged" "$(ST "$a")"
}

# ─── section D: the diagram is generated, not maintained ─────────────────────

test_diagram_is_generated() {
    section "D. The diagram is rendered from the table"
    local out rc

    out=$(cd "$WT" && just lifecycle-check 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then report_pass "the committed artifacts are current"
    else report_fail "just lifecycle-check" "exit 0" "exit=$rc"$'\n'"$out"; fi

    # Regenerating is a no-op — the property that makes the check meaningful.
    out=$(cd "$WT" && just lifecycle-index 2>&1)
    assert_contains "regenerating changes nothing" "already in sync" "$out"

    # A hand-edit inside the generated block is CAUGHT, which is what makes the
    # table the source rather than one more copy. Restored either way.
    local canon="$WT/docs/status-lifecycle.mmd" backup="$TMP/lifecycle.bak"
    cp "$canon" "$backup"
    printf '    ready --> confirmed: user skips the work\n' >> "$canon"
    out=$(cd "$WT" && just lifecycle-check 2>&1); rc=$?
    cp "$backup" "$canon"
    if [[ $rc -ne 0 ]]; then report_pass "a hand-edited diagram is caught"
    else report_fail "a hand-edited diagram is caught" "a non-zero exit" "exit 0"; fi
    assert_contains "and the failure names the fix" "lifecycle-index" "$out"

    out=$(cd "$WT" && just lifecycle-check 2>&1); rc=$?
    assert_eq "the artifact is restored" "0" "$rc"

    # The two gaps the hand-maintained diagram had, closed by construction.
    out="$(TS lifecycle)"
    assert_contains "'completed' now has an inbound edge" \
        "--> completed" "$out"
    assert_contains "and the actor is rendered from the table's own column" \
        "submitted --> ready: user approves" "$out"

    # The editorial preamble is hand-written and survives regeneration — the
    # same split `just guide-index` uses.
    assert_contains "the hand-written preamble survives" \
        "blocked_by" "$(sed -n '1,/BEGIN generated/p' "$canon")"
}

# ─── section E: actor reality ────────────────────────────────────────────────

test_actor_reality() {
    section "E. A session may not report work it did not do"
    # Driven through `endless-go event emit` so the actor is set explicitly
    # rather than resolved from whatever shell this runs in.
    local held other
    held="$(add_task "Add the task a session holds")";  force_status "$held" underway
    other="$(add_task "Add somebody else's task")";     force_status "$other" underway
    seed_session 9001 "$held"

    emit_status agent 9001 "$other" unverified
    if (( EMIT_RC != 0 )); then
        report_pass "a session cannot advance a task it does not hold"
    else
        report_fail "a session cannot advance a task it does not hold" \
            "a refusal" "exit 0"
    fi
    assert_contains "the refusal names the task it actually holds" \
        "holds task $held" "$EMIT_OUT"
    assert_eq "and nothing was written" "underway" "$(ST "$other")"

    emit_status agent 9001 "$held" unverified
    assert_eq "the holding session may advance its own task" \
        "unverified" "$(ST "$held")"

    # `unverified` on work no session ever picked up — the reported case's
    # second half, reachable even with no session id at all.
    local orphan
    orphan="$(add_task "Add a task nobody claimed")"; force_status "$orphan" underway
    emit_status agent "" "$orphan" unverified
    assert_contains "'unverified' needs the task to have been claimed" \
        "never been claimed" "$EMIT_OUT"
    assert_eq "and nothing was written" "underway" "$(ST "$orphan")"

    # A person is exempt. This guards against an agent's mistake, not against a
    # person — and a person's command routinely carries a neighbouring agent's
    # session id, which is why the exemption cannot key on session-presence.
    emit_status human 9001 "$orphan" unverified
    assert_eq "a person at a terminal is exempt" "unverified" "$(ST "$orphan")"

    # But only from the ACTOR rules. An illegal edge is a mistake whoever typed
    # it, so a person gets no pass on transition legality.
    local misrouted
    misrouted="$(add_task "Add a misroutable thing")"; force_status "$misrouted" unplanned
    emit_status human "" "$misrouted" unverified
    assert_eq "transition legality still applies to a person" \
        "unplanned" "$(ST "$misrouted")"

    # And the rule is scoped: only the two work-progress statuses assert
    # something about who did the work.
    local judged
    judged="$(add_task "Add a judgeable thing")"; force_status "$judged" ready
    emit_status agent 9001 "$judged" declined
    assert_eq "a judgment about someone else's task is not actor-checked" \
        "declined" "$(ST "$judged")"
}

# ─── section F: the legal path is unimpeded ──────────────────────────────────

test_legal_path_unimpeded() {
    section "F. The documented path still runs, start to finish"
    # A guard that refuses the ordinary workflow is worse than no guard. File,
    # route, submit, approve, claim, finish — through the real verbs where they
    # exist, and `task update --status` where they do not.
    local t out
    t="$(add_task "Add a thing to carry all the way")"
    assert_eq "filed as untriaged" "untriaged" "$(ST "$t")"

    E task update "E-$t" --status unplanned >/dev/null 2>&1
    assert_eq "triage routes it to unplanned" "unplanned" "$(ST "$t")"

    E task submit "E-$t" >/dev/null 2>&1
    assert_eq "'task submit' reaches submitted" "submitted" "$(ST "$t")"

    E task approve "E-$t" >/dev/null 2>&1
    assert_eq "'task approve' reaches ready" "ready" "$(ST "$t")"

    E task update "E-$t" --status underway >/dev/null 2>&1
    assert_eq "the session takes it underway" "underway" "$(ST "$t")"

    seed_session 9002 "$t"
    emit_status agent 9002 "$t" unverified
    assert_eq "the holding session reports it done" "unverified" "$(ST "$t")"

    E task confirm "E-$t" >/dev/null 2>&1
    assert_eq "'task confirm' finishes it" "confirmed" "$(ST "$t")"

    # Attaching a plan still promotes a pre-judgment task — the executor's own
    # inferred status write, now routed through the same guard.
    local p
    p="$(add_task "Add a thing to plan")"
    printf '# a plan\n' > "$TMP/plan.md"
    E task update "E-$p" --text-file "$TMP/plan.md" >/dev/null 2>&1
    assert_eq "attaching a plan still promotes to submitted" "submitted" "$(ST "$p")"

    # The findings lane, which is the other lifecycle in the same diagram.
    local r
    r="$(add_task "Audit a thing" --type research \
            --justification "the findings lane needs a research task to walk")"
    force_status "$r" underway
    E task update "E-$r" --status completed --outcome "findings" >/dev/null 2>&1
    assert_eq "research work finishes as completed" "completed" "$(ST "$r")"

    # Reopening landed work, and reversing an abandonment — the two paths the
    # hand-maintained diagram either omitted or made impossible.
    E task update "E-$t" --status revisit >/dev/null 2>&1
    assert_eq "confirmed work can be reopened" "revisit" "$(ST "$t")"

    local d
    d="$(add_task "Add a thing to decline")"
    E task update "E-$d" --status declined --outcome "not doing it" >/dev/null 2>&1
    assert_eq "declining works" "declined" "$(ST "$d")"
    E task update "E-$d" --status untriaged >/dev/null 2>&1
    assert_eq "and the decision can be reconsidered" "untriaged" "$(ST "$d")"

    # A row left holding a retired status is not stranded. With no --force, a
    # guard that refused every move OUT of an unknown status would make the rows
    # least able to fix themselves permanent.
    local legacy
    legacy="$(add_task "Add a legacy-status thing")"; force_status "$legacy" blocked
    E task update "E-$legacy" --status obsolete >/dev/null 2>&1
    assert_eq "a retired status can still be escaped" "obsolete" "$(ST "$legacy")"
}

# ─── section G: project-wide regression ──────────────────────────────────────

test_regression() {
    section "G. Regression — full Go suite + full Python suite"
    local out rc
    out=$(cd "$WT" && go test ./... 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then report_pass "go test ./... passes"
    else report_fail "go test ./..." "exit 0" \
        "exit=$rc"$'\n'"$(printf '%s' "$out" | grep -v '^ok\|no test files' | head -25)"; fi

    out=$(cd "$WT" && uv run pytest tests/ -q 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then report_pass "pytest tests/ passes"
    else report_fail "pytest tests/" "exit 0" \
        "exit=$rc"$'\n'"$(printf '%s' "$out" | tail -25)"; fi
}

# ─── main ─────────────────────────────────────────────────────────────────────

main() {
    WT="$(git rev-parse --show-toplevel 2>/dev/null)"
    [[ -n "${WT}" ]] || { printf 'ERROR: not inside a git worktree\n' >&2; exit 2; }

    command -v go >/dev/null || { printf 'ERROR: go not on PATH\n' >&2; exit 2; }
    command -v uv >/dev/null || { printf 'ERROR: uv not on PATH\n' >&2; exit 2; }
    command -v just >/dev/null || { printf 'ERROR: just not on PATH\n' >&2; exit 2; }
    [[ -x "${WT}/bin/endless-go" ]] || { printf 'ERROR: %s missing — run `just build`\n' "${WT}/bin/endless-go" >&2; exit 2; }
    "${WT}/bin/endless-go" task-status lifecycle >/dev/null 2>&1 \
        || { printf 'ERROR: %s predates the lifecycle verbs — run `just build`\n' "${WT}/bin/endless-go" >&2; exit 2; }

    printf '%sE-2018 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  worktree: %s\n' "${WT}"

    if ! setup_fixture; then
        printf 'ERROR: fixture setup failed (isolated DB seeding)\n' >&2
        [[ -n "$TMP" ]] && rm -rf "$TMP"
        exit 2
    fi

    if ! test_units; then
        [[ -n "$TMP" ]] && rm -rf "$TMP"
        summary
        exit 1
    fi

    test_reported_case
    test_blocked_is_gone
    test_diagram_is_generated
    test_actor_reality
    test_legal_path_unimpeded
    test_regression

    [[ -n "$TMP" ]] && rm -rf "$TMP"
    summary
}

main "$@"

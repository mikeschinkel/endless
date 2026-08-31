#!/usr/bin/env bash
#
# E-1845 verification script — the `untriaged` status upstream of `unplanned`.
#
# `untriaged` means "filed, but nobody has looked at it yet". Every new task
# starts there; triage routes it to `submitted` (the description is already a
# sufficient spec) or `unplanned` (design work needed first):
#
#   untriaged → unplanned → submitted → (approve) → ready → underway → ...
#            └──────────────↗
#
# The automatic triager is E-1859 and does not exist yet, so this task also
# ships the MANUAL route (`task submit` accepts `untriaged`) — without it every
# newly filed task would be stranded until E-1859 lands. Check C is the one that
# would have caught that shipping trap.
#
# Run from anywhere inside the worktree:
#   endless task verify E-1845
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
# touched. Shape borrowed from .endless/tasks/e-1648/verify.sh.

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

WT=""; TMP=""; REPO=""

# E: run the worktree's Python CLI against the isolated DB, from the repo dir.
E() { ( cd "$REPO" && uv run --project "$WT" endless "$@" ); }
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
# force_status TASK_ID STATUS: set a status directly, bypassing the CLI's own
# transition rules, so a check can start from any state it needs.
force_status() {
    E sql "UPDATE tasks SET status='$2' WHERE id=$1" --write >/dev/null 2>&1
}

setup_fixture() {
    TMP="$(mktemp -d)"
    REPO="$TMP/repo"
    export XDG_CONFIG_HOME="$TMP/xdg"
    export XDG_CACHE_HOME="$TMP/cache"
    # Prepend the freshly-built worktree binary so the Python event bridge's
    # PATH fallback (cwd is the /tmp repo, not a self-dev worktree) execs
    # candidate code, not the stale global endless-go.
    export PATH="$WT/bin:$PATH"
    mkdir -p "$REPO" "$XDG_CONFIG_HOME/endless"

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

    out=$(cd "$WT" && go test ./internal/sessionstatuscmd/ ./internal/events/ 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then
        report_pass "go test ./internal/sessionstatuscmd/ ./internal/events/ passes"
    else
        report_fail "go test ./internal/sessionstatuscmd/ ./internal/events/" \
            "exit 0" "exit=$rc"$'\n'"$out"
    fi

    out=$(cd "$WT" && uv run pytest tests/test_untriaged_status.py \
        tests/test_submit_approve.py tests/test_text_auto_promote.py -q 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then
        report_pass "pytest untriaged + submit/approve + auto-promote suites pass"
    else
        report_fail "pytest untriaged/submit-approve/auto-promote" "exit 0" \
            "exit=$rc"$'\n'"$(printf '%s' "$out" | tail -25)"
    fi

    if [[ "${FAIL_COUNT}" -gt 0 ]]; then
        printf '\n  %sABORTING%s — unit tests failed; skipping end-to-end checks.\n' \
            "${RED}${BOLD}" "${RESET}"
        return 1
    fi
    return 0
}

# ─── section B: task add defaults ────────────────────────────────────────────

test_add_default() {
    section "B. 'task add' defaults to untriaged"
    local t
    t="$(add_task "Add a plain thing")"
    assert_eq "no --status lands at 'untriaged'" "untriaged" "$(ST "$t")"

    t="$(add_task "Add a quick thing" --tier 1)"
    assert_eq "--tier 1 still lands at 'ready' (exempt from planning AND triage)" \
        "ready" "$(ST "$t")"

    t="$(add_task "Add a forced thing" --status revisit)"
    assert_eq "explicit --status is honored" "revisit" "$(ST "$t")"

    t="$(add_task "Add a planned thing" --text "# plan body")"
    assert_eq "add --text promotes untriaged → submitted" "submitted" "$(ST "$t")"
}

# ─── section C: NO DEADLOCK — the manual route out ───────────────────────────

test_no_deadlock() {
    section "C. No deadlock: a fresh task can leave untriaged with no triager"
    # This is the check that would have caught the shipping trap: if `untriaged`
    # were not in _SUBMITTABLE_FROM, every newly filed task would be stranded
    # until E-1859 (the automatic triager) shipped.
    local t out
    t="$(add_task "Add a strandable thing")"
    assert_eq "starts 'untriaged'" "untriaged" "$(ST "$t")"

    out="$(E task submit "E-$t" 2>&1)"
    assert_eq "'task submit' alone moves it out" "submitted" "$(ST "$t")"
    assert_not_contains "submit is not refused" "Cannot submit" "$out"

    # The other manual leg: route to unplanned when it needs design work.
    t="$(add_task "Add a needs-design thing")"
    E task update "E-$t" --status unplanned >/dev/null 2>&1
    assert_eq "'update --status unplanned' is the other manual leg" \
        "unplanned" "$(ST "$t")"

    # And the full ladder still runs to completion from the new head.
    t="$(add_task "Add a full-ladder thing")"
    E task submit "E-$t" >/dev/null 2>&1
    E task approve "E-$t" >/dev/null 2>&1
    assert_eq "untriaged → submit → approve → ready" "ready" "$(ST "$t")"
}

# ─── section D: description-edit reset ───────────────────────────────────────

test_description_reset() {
    section "D. A material description edit resets a pre-work task to untriaged"
    local t out st

    # Fires from every pre-work status.
    for st in untriaged unplanned submitted ready revisit; do
        t="$(add_task "Add a respec-able thing")"
        force_status "$t" "$st"
        E task update "E-$t" --description "materially different text" >/dev/null 2>&1
        assert_eq "resets from '$st'" "untriaged" "$(ST "$t")"
    done

    # Never fires from a work-started or terminal status. `underway` is the
    # important one: an edit must not yank work from under a live session.
    for st in underway unverified confirmed assumed completed declined obsolete; do
        t="$(add_task "Add an in-flight thing")"
        force_status "$t" "$st"
        E task update "E-$t" --description "materially different text" >/dev/null 2>&1
        assert_eq "does NOT reset from '$st'" "$st" "$(ST "$t")"
    done

    # Guard 1: an identical rewrite is a no-op.
    t="$(add_task "Add an idempotent thing")"
    force_status "$t" "ready"
    E task update "E-$t" --description "spec" >/dev/null 2>&1
    assert_eq "identical rewrite is a no-op" "ready" "$(ST "$t")"

    # Guard 2: --keep-status suppresses.
    t="$(add_task "Add a typo-fix thing")"
    force_status "$t" "ready"
    E task update "E-$t" --description "Spec." --keep-status >/dev/null 2>&1
    assert_eq "--keep-status suppresses the reset" "ready" "$(ST "$t")"

    # Guard 3: an explicit --status in the same call wins.
    t="$(add_task "Add an intentional thing")"
    force_status "$t" "ready"
    E task update "E-$t" --description "materially different" --status blocked >/dev/null 2>&1
    assert_eq "explicit --status wins over the reset" "blocked" "$(ST "$t")"

    # The reset explains itself and names the escape hatch.
    t="$(add_task "Add an explained thing")"
    force_status "$t" "ready"
    out="$(E task update "E-$t" --description "materially different" 2>&1)"
    assert_contains "the reset explains why" "description changed" "$out"
    assert_contains "the reset names --keep-status" "--keep-status" "$out"

    # Composition: re-spec + attach a plan in one call lands `submitted`, not
    # `untriaged` — a task carrying a full plan must not read as unlooked-at.
    t="$(add_task "Add a respec-and-plan thing")"
    force_status "$t" "ready"
    E task update "E-$t" --description "materially different" --text "# plan" >/dev/null 2>&1
    assert_eq "re-spec + plan attach composes to 'submitted'" "submitted" "$(ST "$t")"

    # A title-only edit is not a re-spec.
    t="$(add_task "Add a renamed thing")"
    force_status "$t" "ready"
    E task update "E-$t" --title "Add a renamed thing v2" >/dev/null 2>&1
    assert_eq "title-only edit does not reset" "ready" "$(ST "$t")"
}

# ─── section E: session status renders it, and NOT as 'plan' or '⁇' ──────────

test_session_status_render() {
    section "E. 'session status' shows ◌ triage — not ✎ plan, not the ⁇ unknown glyph"
    local untriaged focal out
    # The untriaged task must be a NON-focal row: focal/parent/from decorations
    # outrank status in classify(), so pointing --task at it would render ● this
    # and prove nothing. Making it the blocker of the focal task puts it in the
    # row set on its own status.
    untriaged="$(add_task "Add a renderable blocker")"
    focal="$(add_task "Add a focal thing" --status ready)"
    E task block "E-$focal" --by "E-$untriaged" >/dev/null 2>&1
    assert_eq "fixture blocker is untriaged" "untriaged" "$(ST "$untriaged")"

    # Headless: --task names the focal task directly, bypassing tmux/session
    # resolution, and reads the same isolated DB via XDG_CONFIG_HOME.
    out="$( cd "$REPO" && XDG_CONFIG_HOME="$XDG_CONFIG_HOME" \
        "$WT/bin/endless-go" session-status --task "$focal" --cols 200 2>&1 )"

    assert_contains "the untriaged row renders the ◌ glyph" "◌ T E-$untriaged" "$out"
    assert_contains "the legend labels it 'triage'" "◌ triage" "$out"
    assert_not_contains "it is NOT labeled 'plan'" "✎ plan" "$out"
    assert_not_contains "it is NOT the ⁇ unknown glyph" "⁇" "$out"

    # The classifier-level guarantee, independent of which rows the view chose
    # to render: untriaged must map to actTriage, never actPlan or actUnknown.
    local plan_case
    plan_case="$(grep -B1 'return actPlan' "$WT/internal/sessionstatuscmd/session_status.go" | grep 'case ')"
    assert_not_contains "classify: untriaged is not in the actPlan case" \
        "untriaged" "$plan_case"
    assert_contains "classify: untriaged has its own case" \
        'case "untriaged":' "$(cat "$WT/internal/sessionstatuscmd/session_status.go")"
    assert_contains "actionMeta labels it 'triage', not 'plan'" \
        'actTriage:  {"◌", "triage"}' \
        "$(cat "$WT/internal/sessionstatuscmd/session_status.go")"
}

# ─── section F: not actionable, but still blocking ───────────────────────────

test_not_actionable_still_blocking() {
    section "F. 'task next' omits untriaged; an untriaged blocker still blocks"
    local untriaged ready dependent out

    untriaged="$(add_task "Add an unlooked-at thing")"
    ready="$(add_task "Add a pickupable thing" --status ready)"
    out="$(E task next --json 2>&1)"
    assert_contains "'task next' offers the ready task" "E-$ready" "$out"
    assert_not_contains "'task next' omits the untriaged task" "E-$untriaged" "$out"

    # Unfinished work is unfinished whether or not it has been looked at, so an
    # untriaged blocker keeps its dependent off the actionable list.
    dependent="$(add_task "Add a dependent thing" --status ready)"
    E task block "E-$dependent" --by "E-$untriaged" >/dev/null 2>&1
    out="$(E task next --json 2>&1)"
    assert_not_contains "an untriaged blocker still blocks its dependent" \
        "E-$dependent" "$out"
}

# ─── section G: epic status derivation ───────────────────────────────────────

test_epic_derivation() {
    section "G. An epic derives 'untriaged' from freshly filed children"
    local epic child
    epic="$(add_task "Build an epic thing" --type epic)"
    child="$(add_task "Add a child thing" --parent "E-$epic")"
    assert_eq "the child is untriaged" "untriaged" "$(ST "$child")"
    assert_eq "the epic derives 'untriaged' (not left unchanged)" \
        "untriaged" "$(ST "$epic")"

    # Routing the child up the ladder pulls the epic with it.
    E task submit "E-$child" >/dev/null 2>&1
    assert_eq "routing the child to submitted pulls the epic to submitted" \
        "submitted" "$(ST "$epic")"
}

# ─── section H: canonical lifecycle + prose docs ─────────────────────────────

test_docs() {
    section "H. Canonical lifecycle is in sync and the prose table lists it"
    local canon="$WT/docs/status-lifecycle.mmd"
    if [[ ! -f "$canon" ]]; then
        report_fail "canonical mermaid file exists" "docs/status-lifecycle.mmd" "missing"
        return
    fi
    assert_contains "canonical mermaid names 'untriaged' as the entry state" \
        "[*] --> untriaged" "$(cat "$canon")"
    assert_not_contains "no stale 'unevaluated' left in the canonical file" \
        "unevaluated" "$(cat "$canon")"

    extract() {
        awk '
            /^<!-- BEGIN canonical:docs\/status-lifecycle.mmd/ {inblk=1; next}
            /^<!-- END canonical:docs\/status-lifecycle.mmd/   {inblk=0}
            inblk && /^```mermaid$/ {infence=1; next}
            inblk && infence && /^```$/ {infence=0; next}
            inblk && infence {print}
        ' "$1"
    }
    local d
    for d in README.md CLAUDE.md docs/guide/index.md; do
        if diff -q <(extract "$WT/$d") "$canon" >/dev/null 2>&1; then
            report_pass "$d embeds the canonical block byte-for-byte"
        else
            report_fail "$d embeds the canonical block byte-for-byte" \
                "identical to docs/status-lifecycle.mmd" \
                "$(diff <(extract "$WT/$d") "$canon" | head -5)"
        fi
    done

    # The prose status table had the diagram's status but not its own row.
    assert_contains "guide's status table has an 'untriaged' row" \
        '| `untriaged`' "$(cat "$WT/docs/guide/index.md")"
    assert_contains "guide documents the description-edit reset" \
        "description edit sends a task back to" "$(cat "$WT/docs/guide/index.md")"
    assert_contains "guide documents the --keep-status escape hatch" \
        '`--keep-status` suppresses the reset' "$(cat "$WT/docs/guide/index.md")"

    # Nothing anywhere should still say `unevaluated` — the name the docs used
    # before this task renamed it. This script is excluded: it necessarily names
    # the old spelling to assert its absence.
    local stale
    stale="$(cd "$WT" && grep -rIl 'unevaluated' \
        --exclude-dir=.git --exclude-dir=vendor --exclude-dir=node_modules \
        --exclude-dir=.endless --exclude='e-1845-verify.sh' . 2>/dev/null)"
    assert_eq "no file still uses the pre-rename 'unevaluated'" "" "$stale"

    # `endless guide` renders the updated lifecycle + table.
    local guide
    guide="$(E guide 2>&1)"
    assert_contains "'endless guide' renders the untriaged entry state" \
        "[*] --> untriaged" "$guide"
    assert_contains "'endless guide' renders the untriaged table row" \
        '`untriaged`' "$guide"
}

# ─── section I: status is registered everywhere it must be ───────────────────

test_registrations() {
    section "I. 'untriaged' is a first-class status across the surfaces"
    # Rewritten by E-1891, which gave status an owning Go package. These
    # registration sites were hand-maintained literals when this suite was
    # written; several had already moved under E-1956. They are now rows in one
    # registry, so the checks ask that registry instead of grepping for the
    # literal that used to sit at each site — which is the outcome E-1845 wanted
    # and could not have.
    local ts="$WT/bin/endless-go task-status"

    assert_eq "the vocabulary lists it first (upstream of unplanned)" \
        "untriaged unplanned" \
        "$($ts get all | head -2 | tr '\n' ' ' | sed 's/ $//')"
    assert_eq "'submittable-from' accepts it (the manual route out)" "0" \
        "$($ts has submittable-from untriaged >/dev/null 2>&1; echo $?)"
    assert_eq "'pre-judgment' accepts it (plan-attach promotes)" "0" \
        "$($ts has pre-judgment untriaged >/dev/null 2>&1; echo $?)"
    assert_eq "'not-actionable' excludes it from 'task next'" "0" \
        "$($ts has not-actionable untriaged >/dev/null 2>&1; echo $?)"
    assert_eq "'children-state-order' gives it a bucket" "0" \
        "$($ts has children-state-order untriaged >/dev/null 2>&1; echo $?)"
    assert_eq "'derivation-precedence' gives it a rung" "4" \
        "$($ts rank derivation-precedence untriaged)"

    # The web dashboard's status-update handler was a registration site too;
    # E-1939 excised the dashboard, so that assertion went with it.
    assert_contains "session snapshot validation reads the shared vocabulary" \
        '_VALID_STATUSES = frozenset(TASK_STATUSES)' \
        "$(cat "$WT/src/endless/session_status_cmd.py")"

    # A claim must promote it, or the task reads untouched while worked on.
    assert_eq "claim promotes untriaged → underway" "0" \
        "$($ts has claim-promotes untriaged >/dev/null 2>&1; echo $?)"
    assert_contains "the claim SQL reads that group, not a literal" \
        "taskstatus.SQLList(taskstatus.ClaimPromotes)" \
        "$(cat "$WT/internal/monitor/session.go")"
}

# ─── section J: project-wide regression ──────────────────────────────────────

test_regression() {
    section "J. Regression — full Go suite + the Python suites that touch status"
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
    [[ -x "${WT}/bin/endless-go" ]] || { printf 'ERROR: %s missing — run `just build`\n' "${WT}/bin/endless-go" >&2; exit 2; }

    printf '%sE-1845 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
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

    test_add_default
    test_no_deadlock
    test_description_reset
    test_session_status_render
    test_not_actionable_still_blocking
    test_epic_derivation
    test_docs
    test_registrations
    test_regression

    [[ -n "$TMP" ]] && rm -rf "$TMP"
    summary
}

main "$@"

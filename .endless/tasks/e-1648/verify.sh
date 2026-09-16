#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1648 and records what was true when E-1648
# landed. Edit it only if you ARE E-1648. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1648 verification script — the `submitted` status + approve gate.
#
# The lifecycle gains a human approval gate:
#   unplanned → submitted → (Mike: approve) → ready → underway → ...
# `submitted` = spec-complete, awaiting approval; reachable two ways — the
# agent attached a plan (auto-move) OR ran `task submit` (description-
# sufficient). `ready` now provably means human-approved, so background
# sessions may run neither `approve` nor pick up (`claim`) non-`ready` work.
#
# Run from anywhere inside the worktree:
#   endless task verify E-1648
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on a setup problem.
#
# Isolation: a throwaway git repo as project root under a temp dir, a temp
# XDG_CONFIG_HOME (its own DB) and XDG_CACHE_HOME. No real DB/ledger/cache is
# touched. The freshly-built worktree binary is exercised two ways: `go test`
# runs against the worktree source, and the Python CLI's event bridge is
# pointed at `<worktree>/bin/endless-go` by prepending it to PATH (the bridge
# falls back to a PATH-resolved `endless-go` when cwd is not a self-dev
# worktree, which the isolated /tmp repo is not). This is the E-1596 ad-hoc
# prototype shape, not a shared harness.

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
# add_task TITLE [--text BODY]: add a task; echoes its id.
add_task() {
    local title="$1"; shift
    E task add "$title" --description "spec" "$@" >/dev/null 2>&1
    LAST_ID
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

    # Seed a background-kind session (session_kinds seeds slug 'background' = id 2).
    E sql "INSERT INTO sessions (id, session_id, project_id, kind_id, state) VALUES (9001,'bg-probe',1,2,'working')" --write >/dev/null 2>&1

    [[ "$(E sql 'SELECT count(*) FROM projects' --tsv 2>/dev/null)" == "1" ]] || return 1
    [[ "$(E sql 'SELECT kind_id FROM sessions WHERE id=9001' --tsv 2>/dev/null)" == "2" ]] || return 1
    return 0
}

# ─── section A: plan-attach retargets to submitted ───────────────────────────

test_retarget_auto_promote() {
    section "A. Plan-attach moves unplanned → submitted (not ready)"
    local t
    t="$(add_task "Add a planned thing" --text "# plan body")"
    assert_eq "add --text lands at 'submitted'" "submitted" "$(ST "$t")"

    # An explicit --status in the same call still wins over the auto-move.
    E task add "Add a forced thing" --description d --text "# body" --status revisit >/dev/null 2>&1
    assert_eq "explicit --status wins over the auto-move" "revisit" "$(ST "$(LAST_ID)")"
}

# ─── section B: submit verb (description-sufficient path) ─────────────────────

test_submit_verb() {
    section "B. 'task submit' moves a no-plan task → submitted"
    local t
    t="$(add_task "Add a no-plan thing")"
    # E-1845 moved the default one rung upstream; `submit` accepts both.
    assert_eq "no-text task starts 'untriaged'" "untriaged" "$(ST "$t")"
    E task submit "E-$t" >/dev/null 2>&1
    assert_eq "'task submit' → 'submitted'" "submitted" "$(ST "$t")"
}

# ─── section C: approve verb ──────────────────────────────────────────────────

test_approve_verb() {
    section "C. 'task approve' moves submitted → ready"
    local t out
    t="$(add_task "Add an approvable thing" --text "# body")"
    assert_eq "starts 'submitted'" "submitted" "$(ST "$t")"
    E task approve "E-$t" >/dev/null 2>&1
    assert_eq "'task approve' → 'ready'" "ready" "$(ST "$t")"

    # approve refuses a non-submitted source.
    local u
    u="$(add_task "Add an unplanned thing" --status unplanned)"
    out="$(E task approve "E-$u" 2>&1)"
    assert_eq "approve on 'unplanned' leaves it unchanged" "unplanned" "$(ST "$u")"
    assert_contains "approve on 'unplanned' refuses" "Cannot approve" "$out"
}

# ─── section D: background gate — approve ─────────────────────────────────────

test_bg_gate_approve() {
    section "D. Background session is refused from 'approve'"
    local t out
    t="$(add_task "Add a bg-approve thing" --text "# body")"    # submitted
    out="$(ENDLESS_SESSION_ID=9001 E task approve "E-$t" 2>&1)"
    assert_contains "background approve is refused" "background session cannot approve" "$out"
    assert_eq "background approve leaves status unchanged" "submitted" "$(ST "$t")"
    # A non-background session (no ENDLESS_SESSION_ID → no session resolved) succeeds.
    E task approve "E-$t" >/dev/null 2>&1
    assert_eq "non-background approve succeeds" "ready" "$(ST "$t")"
}

# ─── section E: background gate — non-ready pickup ───────────────────────────

test_bg_gate_pickup() {
    section "E. Background session may claim only 'ready' work"
    local t out
    t="$(add_task "Add a bg-claim thing" --text "# body")"      # submitted
    out="$(ENDLESS_SESSION_ID=9001 E task claim "E-$t" 2>&1)"
    assert_contains "background claim of non-ready work is refused" \
        "background session may only claim" "$out"
}

# ─── section F: classifier maps submitted off actPlan ────────────────────────

test_classifier() {
    section "F. session-status classifier: submitted is NOT 'plan'"
    local out rc
    out=$(cd "$WT" && go test ./internal/sessionstatuscmd/ 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then report_pass "go test ./internal/sessionstatuscmd/ passes"
    else report_fail "go test ./internal/sessionstatuscmd/" "exit 0" "exit=$rc"$'\n'"$out"; fi

    # The actPlan bucket must not list submitted. E-1765 later gave submitted its
    # OWN action (actReview ⚑) rather than sharing actDo with ready — a submitted
    # task is not spawnable, so rendering it as ▶ do contradicted the claim gate.
    # This assertion tracked that move; the E-1648 invariant it guards ("submitted
    # never reads as 'needs a plan'") is unchanged.
    assert_not_contains "classify: submitted is not in the actPlan case" "submitted" \
        "$(grep -B3 'return actPlan' "$WT/internal/sessionstatuscmd/session_status.go" | grep 'case ')"
    assert_contains "classify: submitted has its own actReview case" 'case "submitted":' \
        "$(cat "$WT/internal/sessionstatuscmd/session_status.go")"
}

# ─── section G: docs mermaid is single-sourced + in sync ─────────────────────

test_docs_sync() {
    section "G. Mermaid lifecycle: one canonical file, byte-identical copies"
    local canon="$WT/docs/status-lifecycle.mmd"
    if [[ ! -f "$canon" ]]; then
        report_fail "canonical mermaid file exists" "docs/status-lifecycle.mmd" "missing"
        return
    fi
    report_pass "canonical mermaid file exists (docs/status-lifecycle.mmd)"

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

    # submitted is documented; the stale auto-promote-to-ready prose is gone.
    assert_contains "guide documents 'submitted'" "submitted" \
        "$(cat "$WT/docs/guide/index.md")"
    assert_not_contains "guide no longer says plan-attach auto-promotes to ready" \
        "auto-promotes to \`ready\`" "$(cat "$WT/docs/guide/index.md")"
}

# ─── section H: verbs.jsonl + status registration ────────────────────────────

test_registrations() {
    section "H. submit/approve verbs registered; submitted in status set"
    local verbs
    verbs="$(cat "$WT/.endless/verbs.jsonl")"
    assert_contains "verbs.jsonl registers 'submit'" '"value": "submit"' "$verbs"
    assert_contains "verbs.jsonl registers 'approve'" '"value": "approve"' "$verbs"
    assert_contains "TASK_STATUSES includes 'submitted'" '"submitted"' \
        "$(grep -A2 'TASK_STATUSES = ' "$WT/src/endless/cli.py")"
    # blocked is NOT dropped by E-1648 (that is E-1532's scope).
    assert_contains "blocked is left intact (owned by E-1532)" '"blocked"' \
        "$(grep -A2 'TASK_STATUSES = ' "$WT/src/endless/cli.py")"
}

# ─── section I: Go + Python regression ───────────────────────────────────────

test_regression() {
    section "I. Regression — Go (events/monitor) + Python auto-promote/verbs"
    local out rc
    out=$(cd "$WT" && go test ./internal/events/ ./internal/monitor/ 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then report_pass "go test ./internal/events/ ./internal/monitor/ passes"
    else report_fail "go test ./internal/events/ ./internal/monitor/" "exit 0" "exit=$rc"$'\n'"$out"; fi

    out=$(cd "$WT" && uv run pytest tests/test_text_auto_promote.py tests/test_submit_approve.py -q 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then report_pass "pytest auto-promote + submit/approve suites pass"
    else report_fail "pytest auto-promote + submit/approve" "exit 0" "exit=$rc"$'\n'"$(printf '%s' "$out" | tail -20)"; fi
}

# ─── main ─────────────────────────────────────────────────────────────────────

main() {
    WT="$(git rev-parse --show-toplevel 2>/dev/null)"
    [[ -n "${WT}" ]] || { printf 'ERROR: not inside a git worktree\n' >&2; exit 2; }

    command -v go >/dev/null || { printf 'ERROR: go not on PATH\n' >&2; exit 2; }
    command -v uv >/dev/null || { printf 'ERROR: uv not on PATH\n' >&2; exit 2; }
    [[ -x "${WT}/bin/endless-go" ]] || { printf 'ERROR: %s missing — run `just build`\n' "${WT}/bin/endless-go" >&2; exit 2; }

    printf '%sE-1648 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  worktree: %s\n' "${WT}"

    if ! setup_fixture; then
        printf 'ERROR: fixture setup failed (isolated DB seeding)\n' >&2
        [[ -n "$TMP" ]] && rm -rf "$TMP"
        exit 2
    fi

    test_retarget_auto_promote
    test_submit_verb
    test_approve_verb
    test_bg_gate_approve
    test_bg_gate_pickup
    test_classifier
    test_docs_sync
    test_registrations
    test_regression

    [[ -n "$TMP" ]] && rm -rf "$TMP"
    summary
}

main "$@"

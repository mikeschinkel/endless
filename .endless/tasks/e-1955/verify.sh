#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1955 and records what was true when E-1955
# landed. Edit it only if you ARE E-1955. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1955 verification script — the content-identity precondition on canAmend.
#
# canAmend's reachability precondition (`for-each-ref --contains HEAD`) is a
# SHA-level test, so ANY rewrite of main's history blinds it: `git pull
# --rebase` reassigns every local SHA, after which no task branch "contains"
# main's ledger tip even though every one of them still carries the very ledger
# content that tip holds. Amending it then diverges main from every task
# branch's base and conflicts on the ledger segment at land.
#
# The fix adds a fourth precondition, checked only for ledger commits: refuse to
# amend when any `refs/heads/task/*` tip holds a `.endless/db-ledger` tree
# byte-identical to HEAD's. A tree OID is content-addressed, so it answers the
# same question in terms a rewrite cannot invalidate. It is ADDITIVE — the
# reachability test stays, and section F is the regression guard against a later
# "simplification" that collapses the two into one.
#
# Run from anywhere inside the worktree:
#   endless task verify E-1955
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on a setup problem.
#
# Isolation: each section gets its own throwaway git repo (plus a bare origin)
# as project root under a temp dir, with its own temp XDG_CONFIG_HOME (own DB)
# and XDG_CACHE_HOME. No real DB/ledger/cache is touched. The candidate binary
# is exercised by prepending `<worktree>/bin` to PATH, so the Python CLI's event
# bridge resolves `endless-go` there — these sections drive REAL endless ledger
# writes through the shipped commit path, not a re-implementation of the guard.
# ENDLESS_NO_TRIAGE=1 keeps background triage from injecting ledger events of
# its own mid-check.

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

# ─── fixture ─────────────────────────────────────────────────────────────────

WT=""; ROOT=""; SCEN=0
REPO=""; ORIGIN=""; TASKWT=""

LEDGER_SUBJECT="Endless: record ledger entry"

# E: run the worktree's Python CLI against this section's isolated DB, from the
# section's repo (which is the registered project root).
E() { ( cd "$REPO" && uv run --project "$WT" endless "$@" ); }

# ev LABEL: drive one real ledger event through the shipped commit path.
# `task add` with a constant first word keeps the verb-registration commit to
# the fixture warm-up, so every later event contributes a ledger write only.
ev() { E task add "Probe thing $1" --description spec >/dev/null 2>&1; }

# ledger_commits: how many `Endless: record ledger entry` commits main carries.
# Amending folds an event into the existing tip (count flat); appending adds one.
ledger_commits() {
    git -C "$REPO" log --format=%s | grep -c "^${LEDGER_SUBJECT}\$" || true
}

head_subject() { git -C "$REPO" log -1 --format=%s; }

# new_scenario: a fresh repo + bare origin + isolated DB, warmed up so main's
# HEAD is an amend-eligible ledger commit. Returns non-zero on a setup problem.
new_scenario() {
    SCEN=$((SCEN + 1))
    local tmp="$ROOT/s$SCEN"
    REPO="$tmp/repo"; ORIGIN="$tmp/origin.git"; TASKWT="$tmp/taskwt"
    export XDG_CONFIG_HOME="$tmp/xdg"
    export XDG_CACHE_HOME="$tmp/cache"
    mkdir -p "$REPO" "$XDG_CONFIG_HOME/endless" || return 1

    git init -q --bare "$ORIGIN" || return 1
    git -C "$REPO" init -q -b main || return 1
    git -C "$REPO" config user.email verify@test
    git -C "$REPO" config user.name verify
    git -C "$REPO" config commit.gpgsign false
    git -C "$REPO" commit -q --allow-empty -m "initial commit" || return 1
    git -C "$REPO" remote add origin "$ORIGIN" || return 1
    git -C "$REPO" push -q -u origin main || return 1

    E project register "$REPO" --name probe --label Probe --desc d \
        --lang Go --status active >/dev/null 2>&1 || return 1

    # Two warm-up events: the first registers the 'Probe' verb (its own commit)
    # and creates the ledger commit; the second proves the tip amends.
    ev warm1; ev warm2
    [[ "$(head_subject)" == "${LEDGER_SUBJECT}" ]] || return 1
    [[ "$(ledger_commits)" == "1" ]] || return 1
    return 0
}

# make_task_branch: fork refs/heads/task/900 at main's current tip in a linked
# worktree and commit real (non-ledger) work on it — the production topology.
make_task_branch() {
    git -C "$REPO" worktree add -q -b task/900 "$TASKWT" || return 1
    echo "task work" > "$TASKWT/task_work.txt"
    git -C "$TASKWT" add -A || return 1
    git -C "$TASKWT" commit -q -m "E-900: real user work" || return 1
    return 0
}

# upstream_advance: push an unrelated commit to origin/main from a throwaway
# clone, so the next `pull --rebase` rewrites the repo's local history.
upstream_advance() {
    local clone="$ROOT/s$SCEN/clone"
    git clone -q "$ORIGIN" "$clone" || return 1
    git -C "$clone" config user.email other@test
    git -C "$clone" config user.name other
    git -C "$clone" config commit.gpgsign false
    echo upstream > "$clone/upstream.txt"
    git -C "$clone" add -A || return 1
    git -C "$clone" commit -q -m "upstream: someone else's work" || return 1
    git -C "$clone" push -q origin main || return 1
    rm -rf "$clone"
    return 0
}

# land_result: what `endless worktree land` does at its core — rebase the task
# branch onto main. Echoes OK, or "CONFLICT [<files>]".
#
# Non-mutating: a successful rebase is rolled back to the branch's pre-probe
# SHA. A land leaves the branch sitting on main's ledger tip, which the
# reachability precondition would then legitimately refuse to amend — so an
# un-rolled-back probe would silently change what the checks after it measure.
land_result() {
    local before files
    before="$(git -C "$TASKWT" rev-parse HEAD)"
    if git -C "$TASKWT" rebase main >/dev/null 2>&1; then
        git -C "$TASKWT" reset -q --hard "$before"
        echo "OK"
        return
    fi
    files="$(git -C "$TASKWT" diff --name-only --diff-filter=U 2>/dev/null | tr '\n' ' ')"
    git -C "$TASKWT" rebase --abort >/dev/null 2>&1
    git -C "$TASKWT" reset -q --hard "$before"
    echo "CONFLICT [${files% }]"
}

setup_failed() {
    report_fail "$1" "scenario fixture builds" "setup failed"
}

# ─── section A: Go unit tests (fail-fast) ────────────────────────────────────

test_go_units() {
    section "A. Go unit tests — internal/events (fail-fast)"
    local out rc
    out=$(cd "$WT" && go test ./internal/events/ 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then
        report_pass "go test ./internal/events/ passes"
        return 0
    fi
    report_fail "go test ./internal/events/" "exit 0" "exit=$rc"$'\n'"$out"
    return 1
}

# ─── section B: baseline amend (flood suppression intact) ────────────────────

test_baseline_amend() {
    section "B. Baseline — no task branch, several events fold into one commit"
    new_scenario || { setup_failed "baseline scenario"; return; }

    local i
    for i in 1 2 3 4 5; do ev "b$i"; done
    assert_eq "5 further ledger events leave main at 1 ledger commit" "1" "$(ledger_commits)"
    assert_eq "HEAD is still the ledger commit" "${LEDGER_SUBJECT}" "$(head_subject)"
}

# ─── section C: reachability refusal still fires ─────────────────────────────

test_reachability_refusal() {
    section "C. Reachability precondition — a task branch on main's tip forces append"
    new_scenario || { setup_failed "reachability scenario"; return; }
    make_task_branch || { setup_failed "reachability scenario (task branch)"; return; }

    ev c1
    assert_eq "event after the task branch forks appends (2 ledger commits)" \
        "2" "$(ledger_commits)"
}

# ─── section D: the regression — rewritten history must still append ─────────

test_rewritten_history() {
    section "D. The regression — after \`pull --rebase\`, the next event must append"
    new_scenario || { setup_failed "rewrite scenario"; return; }
    make_task_branch || { setup_failed "rewrite scenario (task branch)"; return; }
    upstream_advance || { setup_failed "rewrite scenario (upstream)"; return; }

    git -C "$REPO" -c pull.rebase=true pull -q --rebase origin main >/dev/null 2>&1 \
        || { setup_failed "rewrite scenario (pull --rebase)"; return; }

    # The rewrite blinded the reachability test: only main contains HEAD now, so
    # anything that refuses here is doing it on content.
    assert_eq "the rewrite leaves only main containing HEAD (reachability is blind)" \
        "refs/heads/main" \
        "$(git -C "$REPO" for-each-ref --contains HEAD --format='%(refname)')"

    local before after
    before="$(ledger_commits)"
    ev d1
    after="$(ledger_commits)"
    assert_eq "post-rewrite ledger event appends rather than amends" \
        "$((before + 1))" "$after"

    # The conflict this whole task exists to prevent.
    assert_eq "task branch rebases onto main cleanly" "OK" "$(land_result)"

    # ── section E rides on this scenario: the fix must self-release ──
    section "E. Self-release — amending resumes after the one suppressed amend"
    local anchored i
    anchored="$(ledger_commits)"
    for i in 1 2 3 4 5; do ev "e$i"; done
    assert_eq "5 more events add no further ledger commits (amend resumed)" \
        "$anchored" "$(ledger_commits)"
    assert_eq "task branch still rebases onto main cleanly" "OK" "$(land_result)"
}

# ─── section F: merge-style pull — no false suppression ──────────────────────

test_merge_pull() {
    section "F. No false suppression — merge-style pull behaves as it always has"
    new_scenario || { setup_failed "merge scenario"; return; }
    make_task_branch || { setup_failed "merge scenario (task branch)"; return; }
    upstream_advance || { setup_failed "merge scenario (upstream)"; return; }

    git -C "$REPO" -c pull.rebase=false pull -q --no-rebase --no-edit origin main >/dev/null 2>&1 \
        || { setup_failed "merge scenario (pull --no-rebase)"; return; }

    # A merge preserves history, so main's ledger tip is still reachable from
    # the task branch — the reachability test alone already handles this.
    local before
    before="$(ledger_commits)"
    ev f1
    assert_eq "post-merge-pull ledger event appends (unchanged behaviour)" \
        "$((before + 1))" "$(ledger_commits)"

    local anchored i
    anchored="$(ledger_commits)"
    for i in 1 2 3 4; do ev "f$i"; done
    assert_eq "subsequent events still fold in (flood suppression intact)" \
        "$anchored" "$(ledger_commits)"
    assert_eq "task branch rebases onto main cleanly" "OK" "$(land_result)"
}

# ─── section G: the reachability precondition is retained ────────────────────

# Neither a tag nor a remote-tracking ref is a `refs/heads/task/*` branch, so
# the content test is structurally blind to both. Only the reachability test can
# catch them — these two checks fail the moment someone "simplifies" the pair
# into one.
test_reachability_retained() {
    section "G. Reachability retained — tag and remote-tracking tips still refuse"

    new_scenario || { setup_failed "tag scenario"; return; }
    assert_eq "no task branches exist (only reachability can answer)" "" \
        "$(git -C "$REPO" for-each-ref --format='%(refname)' refs/heads/task)"
    git -C "$REPO" tag v1 HEAD || { setup_failed "tag scenario (tag)"; return; }
    local before
    before="$(ledger_commits)"
    ev g1
    assert_eq "a tagged ledger tip still forces append" \
        "$((before + 1))" "$(ledger_commits)"

    new_scenario || { setup_failed "remote-tracking scenario"; return; }
    git -C "$REPO" push -q origin main || { setup_failed "remote-tracking scenario (push)"; return; }
    before="$(ledger_commits)"
    ev g2
    assert_eq "a pushed ledger tip still forces append" \
        "$((before + 1))" "$(ledger_commits)"
}

# ─── main ─────────────────────────────────────────────────────────────────────

main() {
    WT="$(git rev-parse --show-toplevel 2>/dev/null)"
    [[ -n "${WT}" ]] || { printf 'ERROR: not inside a git worktree\n' >&2; exit 2; }

    command -v go >/dev/null || { printf 'ERROR: go not on PATH\n' >&2; exit 2; }
    command -v uv >/dev/null || { printf 'ERROR: uv not on PATH\n' >&2; exit 2; }
    command -v git >/dev/null || { printf 'ERROR: git not on PATH\n' >&2; exit 2; }
    [[ -x "${WT}/bin/endless-go" ]] || {
        printf 'ERROR: %s missing — run `just build`\n' "${WT}/bin/endless-go" >&2; exit 2; }

    ROOT="$(mktemp -d)"
    trap 'rm -rf "${ROOT}"' EXIT
    export PATH="${WT}/bin:${PATH}"
    export ENDLESS_NO_TRIAGE=1

    printf '%sE-1955 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  worktree: %s\n' "${WT}"

    # The Go units cover canAmend's decision directly, including the empty-ledger
    # -tree and missing-branch edges. If they fail, the end-to-end sections below
    # would only re-report the same defect more slowly.
    test_go_units || { summary; exit 1; }

    test_baseline_amend
    test_reachability_refusal
    test_rewritten_history
    test_merge_pull
    test_reachability_retained

    summary
}

main "$@"

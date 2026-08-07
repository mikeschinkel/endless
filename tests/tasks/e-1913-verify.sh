#!/usr/bin/env bash
#
# E-1913 verification script — `--keep-status` holds the status across EVERY
# auto-transition.
#
# `task update` infers a status change from what you edited, in four places:
#
#   1. non-empty --text on an untriaged/unplanned task -> submitted  (E-1266/E-1648)
#   2. a material --description edit on a pre-work task -> untriaged (E-1845)
#   3. a real --text edit on a done task                -> revisit   (E-1762)
#   4. --tier 1 on an untriaged/unplanned task          -> ready
#
# Legs 2 and 3 were already guarded by the flag. Leg 1 leaked: the promotion
# lives in the Go executor, gated only on "the caller set status explicitly",
# while --keep-status was a Python-side flag with no field in the event payload
# to travel in. So appending one line to an `unplanned` task's plan silently
# promoted it — which is how E-1671 lost a deliberately-unapproved status. Leg 4
# was never guarded either.
#
# The fix (section B) sends the current status when — and only when — the
# promotion would otherwise fire, so the executor's existing "caller wins"
# branch stands down. Section C is the reason for that scoping: a status field
# is not inert in the executor, so pinning unconditionally would restamp the
# completion time of a `confirmed` task whose plan text was merely typo-fixed.
#
# Run from anywhere inside the worktree:
#   esu && ./tests/tasks/e-1913-verify.sh
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
# touched. Shape borrowed from tests/tasks/e-1845-verify.sh.

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
# assert_ne DESC NOT_EXPECTED ACTUAL
assert_ne() {
    if [[ "$2" != "$3" ]]; then report_pass "$1"
    else report_fail "$1" "anything but: $2" "$3"; fi
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
# COL TASK_ID COLUMN: any single column of a task in the isolated DB.
COL() { E sql "SELECT COALESCE($2,'') FROM tasks WHERE id=$1" --tsv 2>/dev/null; }
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
    # candidate code, not the stale global endless-go. The plan-attach promotion
    # this task suppresses lives in that binary, so the wrong one proves nothing.
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

    # The executor is deliberately UNTOUCHED by this task — the fix crosses the
    # Python-to-Go boundary in the vocabulary the executor already speaks. Its
    # own white-box tests staying green is the assertion that nothing moved.
    out=$(cd "$WT" && go test ./internal/events/ 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then
        report_pass "go test ./internal/events/ passes (executor unchanged)"
    else
        report_fail "go test ./internal/events/" "exit 0" "exit=$rc"$'\n'"$out"
    fi

    out=$(cd "$WT" && uv run pytest tests/test_keep_status.py \
        tests/test_untriaged_status.py tests/test_text_auto_promote.py -q 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then
        report_pass "pytest keep-status + untriaged + auto-promote suites pass"
    else
        report_fail "pytest keep-status/untriaged/auto-promote" "exit 0" \
            "exit=$rc"$'\n'"$(printf '%s' "$out" | tail -25)"
    fi

    if [[ "${FAIL_COUNT}" -gt 0 ]]; then
        printf '\n  %sABORTING%s — unit tests failed; skipping end-to-end checks.\n' \
            "${RED}${BOLD}" "${RESET}"
        return 1
    fi
    return 0
}

# ─── section B: THE REGRESSION — plan attach + --keep-status ─────────────────

test_plan_attach() {
    section "B. The regression: --keep-status suppresses the plan-attach promotion"
    local t plan st
    plan="$TMP/plan.md"
    printf '# plan\nbody\n' > "$plan"

    # The gap this task closes, from both pre-judgment statuses.
    for st in untriaged unplanned; do
        t="$(add_task "Add a plannable thing")"
        force_status "$t" "$st"
        E task update "E-$t" --text-file "$plan" --keep-status >/dev/null 2>&1
        assert_eq "--keep-status holds '$st' through a plan attach" "$st" "$(ST "$t")"
        assert_contains "the plan text was still written from '$st'" \
            "body" "$(COL "$t" text)"
    done

    # The promotion is correct DEFAULT behavior — suppressing it is opt-in, and
    # this task must not have cost the default.
    for st in untriaged unplanned; do
        t="$(add_task "Add a promotable thing")"
        force_status "$t" "$st"
        E task update "E-$t" --text-file "$plan" >/dev/null 2>&1
        assert_eq "without the flag, '$st' still promotes to submitted" \
            "submitted" "$(ST "$t")"
    done

    # The E-1671 case verbatim: a task parked at an unapproved status gains a
    # finding appended to an existing plan, and must not move.
    t="$(add_task "Add a parked thing")"
    force_status "$t" "unplanned"
    E task update "E-$t" --text-file "$plan" --keep-status >/dev/null 2>&1
    printf '# plan\nbody\n\n## Finding\nnew\n' > "$plan"
    E task update "E-$t" --text-file "$plan" --keep-status >/dev/null 2>&1
    assert_eq "appending a finding leaves the parked status alone" \
        "unplanned" "$(ST "$t")"
    assert_contains "the appended finding landed" "## Finding" "$(COL "$t" text)"
    printf '# plan\nbody\n' > "$plan"

    # The pinned status write is a no-op write: reporting it as a change would
    # misdescribe what the update did.
    t="$(add_task "Add a quiet thing")"
    force_status "$t" "unplanned"
    assert_not_contains "the update does not report a phantom status change" \
        "Status:" "$(E task update "E-$t" --text-file "$plan" --keep-status 2>&1)"
}

# ─── section C: the other three legs stay guarded ────────────────────────────

test_other_legs() {
    section "C. The other three auto-transitions stay guarded"
    local t plan before after
    plan="$TMP/plan.md"

    # Leg 3 — E-1762 auto-revisit on a done task.
    t="$(add_task "Add a finished thing")"
    force_status "$t" "assumed"
    printf '# plan (typo fixed)\n' > "$plan"
    E task update "E-$t" --text-file "$plan" --keep-status >/dev/null 2>&1
    assert_eq "--keep-status holds 'assumed' through a plan-text edit" \
        "assumed" "$(ST "$t")"

    t="$(add_task "Add another finished thing")"
    force_status "$t" "assumed"
    printf '# plan (materially new scope)\n' > "$plan"
    E task update "E-$t" --text-file "$plan" >/dev/null 2>&1
    assert_eq "without the flag, a done task still flips to revisit" \
        "revisit" "$(ST "$t")"

    # Leg 2 — E-1845 description reset on a pre-work task.
    t="$(add_task "Add an approved thing")"
    force_status "$t" "ready"
    E task update "E-$t" --description "Spec." --keep-status >/dev/null 2>&1
    assert_eq "--keep-status holds 'ready' through a description edit" \
        "ready" "$(ST "$t")"

    t="$(add_task "Add a respec-able thing")"
    force_status "$t" "ready"
    E task update "E-$t" --description "materially different" >/dev/null 2>&1
    assert_eq "without the flag, a description edit still resets" \
        "untriaged" "$(ST "$t")"

    # Leg 4 — the tier-1 planning exemption.
    t="$(add_task "Add a quick thing")"
    E task update "E-$t" --tier 1 --keep-status >/dev/null 2>&1
    assert_eq "--keep-status holds 'untriaged' through --tier 1" \
        "untriaged" "$(ST "$t")"
    assert_eq "the tier was still set" "1" "$(COL "$t" tier)"

    t="$(add_task "Add another quick thing")"
    E task update "E-$t" --tier 1 >/dev/null 2>&1
    assert_eq "without the flag, --tier 1 still advances to ready" \
        "ready" "$(ST "$t")"

    # The pin is SCOPED, and this is why: a status field is not inert in the
    # executor. Present at all, it rewrites completed_at and clears the tier of
    # a terminal-status task — so pinning unconditionally would restamp the
    # completion time of a confirmed task whose plan text was merely typo-fixed.
    t="$(add_task "Add a shipped thing")"
    printf '# plan\n' > "$plan"
    E task update "E-$t" --text-file "$plan" >/dev/null 2>&1
    E task update "E-$t" --status confirmed --outcome "shipped" >/dev/null 2>&1
    before="$(COL "$t" completed_at)"
    assert_ne "fixture: the confirmed task carries a completion timestamp" "" "$before"
    printf '# plan (typo fixed)\n' > "$plan"
    E task update "E-$t" --text-file "$plan" --keep-status >/dev/null 2>&1
    after="$(COL "$t" completed_at)"
    assert_eq "a typo fix does not restamp completed_at" "$before" "$after"
    assert_eq "and does not move the confirmed status" "confirmed" "$(ST "$t")"
}

# ─── section D: --status and --keep-status are contradictory ─────────────────

test_contradiction() {
    section "D. --status together with --keep-status is refused"
    local t out rc

    t="$(add_task "Add a contradicted thing")"
    force_status "$t" "unplanned"
    out="$( E task update "E-$t" --status ready --keep-status 2>&1 )"; rc=$?
    assert_ne "the call exits non-zero" "0" "$rc"
    assert_contains "the refusal says why" "contradict" "$out"
    assert_eq "the refusal changes nothing" "unplanned" "$(ST "$t")"

    # Each flag alone is still fine.
    E task update "E-$t" --status ready >/dev/null 2>&1
    assert_eq "--status alone still works" "ready" "$(ST "$t")"
}

# ─── section E: the contract is documented where agents read it ──────────────

test_docs() {
    section "E. The widened contract is documented, and the two sides agree"
    local help guide claude tasks_doc

    # Click rewraps option help to the terminal width, so a phrase can land
    # across a line break. Collapse whitespace before matching on wording.
    help="$(E task update --help 2>&1 | tr -s '[:space:]' ' ')"
    assert_contains "--help states no auto-transition fires" \
        "no auto-transition fires" "$help"
    assert_not_contains "--help no longer scopes the flag to a done task" \
        "on a done task" "$help"
    assert_contains "--help names the --status conflict" \
        "Cannot be combined with --status" "$help"

    tasks_doc="$(cat "$WT/docs/guide/tasks.md")"
    assert_contains "guide tasks has a --keep-status section" \
        '### `--keep-status`' "$tasks_doc"
    assert_contains "guide tasks enumerates all four inferences" \
        "suppresses all four" "$tasks_doc"

    guide="$(cat "$WT/docs/guide/index.md")"
    assert_contains "guide index states the widened contract" \
        "holds the status across every auto-transition" "$guide"
    # E-1845's own suite asserts this sentence; the widening must not drop it.
    assert_contains "guide index keeps E-1845's escape-hatch sentence" \
        '`--keep-status` suppresses the reset' "$guide"

    claude="$(cat "$WT/CLAUDE.md")"
    assert_contains "CLAUDE.md states the flag is absolute" \
        "is absolute" "$claude"

    # The rendered guide (not just the file on disk) carries it.
    assert_contains "'endless guide tasks' renders the section" \
        "suppresses all four" "$(E guide tasks 2>&1)"

    # The fix reads the executor's promotion-source set from Python. The two
    # definitions are separate declarations in separate languages; if one gains
    # a status and the other does not, --keep-status silently leaks again.
    assert_contains "Python names exactly the pre-judgment pair" \
        '"untriaged", "unplanned",' \
        "$(grep -A2 '_PRE_JUDGMENT_STATUSES: frozenset' "$WT/src/endless/task_cmd.py")"
    assert_contains "Go names exactly the same pair" \
        'return status == "untriaged" || status == "unplanned"' \
        "$(cat "$WT/internal/events/executor.go")"
}

# ─── section F: project-wide regression ──────────────────────────────────────

test_regression() {
    section "F. Regression — full Go suite, full Python suite, guide gate, build"
    local out rc

    out=$(cd "$WT" && go build ./... 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then report_pass "go build ./... succeeds"
    else report_fail "go build ./..." "exit 0" "exit=$rc"$'\n'"$(printf '%s' "$out" | head -20)"; fi

    out=$(cd "$WT" && go vet ./... 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then report_pass "go vet ./... is clean"
    else report_fail "go vet ./..." "exit 0" "exit=$rc"$'\n'"$(printf '%s' "$out" | head -20)"; fi

    out=$(cd "$WT" && go test ./... 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then report_pass "go test ./... passes"
    else report_fail "go test ./..." "exit 0" \
        "exit=$rc"$'\n'"$(printf '%s' "$out" | grep -v '^ok\|no test files' | head -25)"; fi

    out=$(cd "$WT" && uv run pytest tests/ -q 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then report_pass "pytest tests/ passes"
    else report_fail "pytest tests/" "exit 0" \
        "exit=$rc"$'\n'"$(printf '%s' "$out" | tail -25)"; fi

    # The guide gate: this task added a topic to the cross-reference, so the
    # generated block in docs/guide/index.md must be in sync with it.
    out=$(cd "$WT" && uv run python -m endless.guide_map check 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then report_pass "guide cross-reference is in sync (just guide-check)"
    else report_fail "just guide-check" "exit 0" \
        "exit=$rc"$'\n'"$(printf '%s' "$out" | tail -15)"; fi
}

# ─── main ─────────────────────────────────────────────────────────────────────

main() {
    WT="$(git rev-parse --show-toplevel 2>/dev/null)"
    [[ -n "${WT}" ]] || { printf 'ERROR: not inside a git worktree\n' >&2; exit 2; }

    command -v go >/dev/null || { printf 'ERROR: go not on PATH\n' >&2; exit 2; }
    command -v uv >/dev/null || { printf 'ERROR: uv not on PATH\n' >&2; exit 2; }
    [[ -x "${WT}/bin/endless-go" ]] || { printf 'ERROR: %s missing — run `just build`\n' "${WT}/bin/endless-go" >&2; exit 2; }

    printf '%sE-1913 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
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

    test_plan_attach
    test_other_legs
    test_contradiction
    test_docs
    test_regression

    [[ -n "$TMP" ]] && rm -rf "$TMP"
    summary
}

main "$@"

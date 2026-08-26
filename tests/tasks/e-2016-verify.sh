#!/usr/bin/env bash
#
# E-2016 verification — `unreviewed` sits between `underway` and `completed`
# for research and brainstorm, so a session cannot declare its own findings
# finished.
#
# The defect: research and brainstorm reached `completed` on the session's own
# say-so. Nothing gated the OUTCOME the way `unverified` → `confirmed` gates
# implementation work. E-1817 is the case in point — an audit whose outcome went
# through five rounds of Mike's correction AFTER the session had marked it
# completed, each round materially changing the deliverable. Anything that had
# started on the self-declared version would have built on a deliverable that
# moved five times.
#
# The fix is a distinct status rather than a reuse of the `unverified` lane.
# `unverified` asks "does it work"; `unreviewed` asks "has the owner read it".
# E-1817 failed the second and never the first. One status covers both types —
# they differ in deliverable (findings against a synthesis) but not in the
# failure mode.
#
# Two consequences worth naming, because neither is obvious from the title:
#   - `unreviewed` is NOT terminal, so it HOLDS a dependent. The deliverable of
#     findings work is information other tasks consume, which makes an
#     unreviewed outcome more dangerous downstream than unverified code, not
#     less.
#   - The outcome requirement moved to the gate. `unreviewed` means "outcome
#     written, awaiting the read", so entering it empty hands the owner nothing
#     to sign off on — a worse failure than the one this task set out to fix.
#
# What this script covers is ACCEPTANCE — the point-in-time claims of this task.
# The PERMANENT invariants live where they belong and run forever:
# internal/taskstatus/taskstatus_test.go (the registry's partition invariants,
# which is what forced a decision on every grouping), transitions_test.go (the
# lane split and its type coverage), internal/events/status_transition_test.go
# (the executor guard), tests/test_research_gate.py and
# tests/test_completed_status.py (the CLI refusals). See E-1889's convention.
#
# Run from anywhere inside the worktree:
#   esu && ./tests/tasks/e-2016-verify.sh
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
# touched. Harness shape borrowed from tests/tasks/e-2018-verify.sh.

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
# harness. Transition legality applies to a person too; E-2018's actor-reality
# half does not, which keeps these checks about THIS task's gate.
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
# add_findings_task TITLE TYPE: a research/brainstorm task under an underway
# epic, which is what satisfies E-1544's research-creation gate without a
# --justification. Echoes its id.
add_findings_task() {
    local epic
    epic="$(add_task "Anchor epic for $2" --type epic)"
    force_status "$epic" "underway"
    add_task "$1" --type "$2" --parent "E-$epic"
}

setup_fixture() {
    TMP="$(mktemp -d)"
    REPO="$TMP/repo"
    export XDG_CONFIG_HOME="$TMP/xdg"
    export XDG_CACHE_HOME="$TMP/cache"
    CFG="$XDG_CONFIG_HOME/endless"
    # Prepend the freshly-built worktree binary so the Python event bridge's
    # PATH fallback (cwd is the /tmp repo, not a self-dev worktree) execs
    # candidate code, not the stale global endless-go.
    export PATH="$WT/bin:$PATH"
    mkdir -p "$REPO" "$CFG"

    git -C "$REPO" init -q
    git -C "$REPO" config user.email verify@test
    git -C "$REPO" config user.name verify
    git -C "$REPO" commit -q --allow-empty -m "initial commit"

    E project register "$REPO" --name probe --label Probe --desc d --lang Go --status active >/dev/null 2>&1

    # Seed the project verb list. E-1240's gate reserves `completed` for titles
    # whose lead verb is marked `completable`, and matchers._resolved_verbs
    # takes the FIRST source that exists — project, else machine, else the
    # built-in defaults. It does not layer them. So the moment `task add`
    # auto-registers one verb into the project file, that one-entry file
    # SHADOWS every default, and `audit` stops being completable. Without this,
    # section B would be testing E-1240's gate rather than E-2016's, and would
    # pass for the wrong reason.
    mkdir -p "$REPO/.endless"
    cat > "$REPO/.endless/verbs.jsonl" <<'VERBS'
{"value": "audit", "definition": "to examine systematically", "completable": true}
{"value": "anchor", "definition": "to fix or secure firmly in place"}
{"value": "add", "definition": "to introduce or include something new"}
{"value": "build", "definition": "to construct"}
{"value": "fix", "definition": "to repair"}
{"value": "brainstorm", "definition": "to generate ideas"}
VERBS

    [[ "$(E sql 'SELECT count(*) FROM projects' --tsv 2>/dev/null)" == "1" ]] || return 1
    return 0
}

# ─── section A: unit tests (FAIL-FAST GATE) ──────────────────────────────────

test_units() {
    section "A. Unit tests for the new behavior (fail-fast gate)"
    local out rc

    out=$(cd "$WT" && go test ./internal/taskstatus/ ./internal/events/ 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then
        report_pass "go test taskstatus + events passes"
    else
        report_fail "go test taskstatus + events" \
            "exit 0" "exit=$rc"$'\n'"$(printf '%s' "$out" | grep -v '^ok' | head -25)"
    fi

    out=$(cd "$WT" && uv run pytest tests/test_research_gate.py \
        tests/test_completed_status.py tests/test_status_lifecycle_sync.py -q 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then
        report_pass "pytest research-gate + completed-status + artifact-currency suites pass"
    else
        report_fail "pytest E-2016 suites" "exit 0" \
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
    section "B. A findings task cannot declare its own outcome finished"
    # E-1817's shape: a research task, underway, whose session marks it
    # completed. That is the call this task exists to refuse.
    local t out
    t="$(add_findings_task "Audit the thing" research)"
    force_status "$t" "underway"

    out="$(E task update "E-$t" --status completed --outcome "findings text" 2>&1)"
    assert_eq "the write did not land" "underway" "$(ST "$t")"
    assert_contains "the refusal names the gate as the way forward" "unreviewed" "$out"
    assert_not_contains "and it is a clean CLI error, not a traceback" "Traceback" "$out"

    # No --force. E-1577/E-1579's precedent, which E-2018 kept: the fix is to
    # correct the call, not to override a correctness invariant.
    out="$(E task update "E-$t" --status completed --outcome "findings text" --force 2>&1)"
    assert_eq "--force does not buy a way past it" "underway" "$(ST "$t")"

    # Brainstorm is the same gate — one status, both types, because they share
    # the failure mode even though the deliverable differs.
    local b
    b="$(add_findings_task "Brainstorm the thing" brainstorm)"
    force_status "$b" "underway"
    E task update "E-$b" --status completed --outcome "synthesis" >/dev/null 2>&1
    assert_eq "brainstorm is gated the same way" "underway" "$(ST "$b")"
}

# ─── section C: the gate is passable ─────────────────────────────────────────

test_gate_is_passable() {
    section "C. The gate opens — the two-step path runs end to end"
    local t
    t="$(add_findings_task "Audit the other thing" research)"
    force_status "$t" "underway"

    E task update "E-$t" --status unreviewed --outcome "findings text" >/dev/null 2>&1
    assert_eq "the outcome lands the task at the gate" "unreviewed" "$(ST "$t")"

    E task update "E-$t" --status completed >/dev/null 2>&1
    assert_eq "and the owner's read carries it to completed" "completed" "$(ST "$t")"

    # Reachable from `ready` too: findings work needs no worktree to produce.
    local r
    r="$(add_findings_task "Audit from ready" research)"
    force_status "$r" "ready"
    E task update "E-$r" --status unreviewed --outcome "findings" >/dev/null 2>&1
    assert_eq "ready reaches the gate directly" "unreviewed" "$(ST "$r")"

    # The gate must be escapable DOWNWARD, or a wrong outcome is stranded —
    # E-1817 took five rounds of correction, and every one of them needed this.
    local s
    s="$(add_findings_task "Audit sent back" research)"
    force_status "$s" "unreviewed"
    E task update "E-$s" --status revisit >/dev/null 2>&1
    assert_eq "a wrong outcome can be sent back to revisit" "revisit" "$(ST "$s")"
}

# ─── section D: the inverse half ─────────────────────────────────────────────

test_implementation_types_refused() {
    section "D. Implementation work is refused the review gate"
    # Without this the two tracks are only half-separated: findings types were
    # already refused the verification lane, but the review lane was open to
    # everyone.
    local t out
    t="$(add_task "Add a widget")"
    force_status "$t" "underway"

    out="$(E task update "E-$t" --status unreviewed 2>&1)"
    assert_eq "a todo cannot enter the review gate" "underway" "$(ST "$t")"
    assert_contains "the refusal names the status refused" "unreviewed" "$out"
    # The remedy differs by direction — telling a todo to "use --status
    # completed" would be wrong.
    assert_contains "and names the gate that DOES apply to it" "unverified" "$out"

    local b
    b="$(add_task "Fix a widget" --type bugfix)"
    force_status "$b" "underway"
    E task update "E-$b" --status unreviewed >/dev/null 2>&1
    assert_eq "a bugfix is refused the same way" "underway" "$(ST "$b")"

    # And the direct route to `completed` is intact for them: `completed` is
    # gated on the title's lead verb, not on type, so a todo-typed audit still
    # finishes in one step.
    local a
    a="$(add_task "Audit the config")"
    force_status "$a" "underway"
    E task update "E-$a" --status completed >/dev/null 2>&1
    assert_eq "a todo keeps its one-step route to completed" "completed" "$(ST "$a")"
}

# ─── section E: the outcome requirement moved to the gate ────────────────────

test_outcome_required_at_the_gate() {
    section "E. Entering the gate empty is refused"
    # `unreviewed` means "outcome written, awaiting the owner's read". Reaching
    # it with nothing written hands the owner an empty gate to sign off on.
    local t out
    t="$(add_findings_task "Audit with no outcome" research)"
    force_status "$t" "underway"

    out="$(E task update "E-$t" --status unreviewed 2>&1)"
    assert_eq "the write did not land" "underway" "$(ST "$t")"
    assert_contains "the refusal says an outcome is required" "outcome is required" "$out"
}

# ─── section F: registry membership ──────────────────────────────────────────

test_registry() {
    section "F. The registry places it deliberately, not by default"
    assert_contains "it is in the vocabulary" "unreviewed" "$(TS get all)"
    assert_eq "it has a label" "Unreviewed" "$(TS label unreviewed)"
    assert_eq "it has its own glyph" "☐" "$(TS glyph unreviewed)"
    assert_eq "review-track is exactly this status" "unreviewed" "$(TS get review-track)"

    # NOT terminal, which is what makes it HOLD a dependent — the whole point.
    # Findings work produces information other tasks consume, so an unreviewed
    # outcome is more dangerous downstream than unverified code, not less.
    if TS has terminal unreviewed >/dev/null 2>&1; then
        report_fail "it is not terminal (so it blocks dependents)" "not a member" "member of terminal"
    else
        report_pass "it is not terminal (so it blocks dependents)"
    fi
    # Shipped: the work HAPPENED, so `obsolete` ("it never needed doing") is a
    # lie about it — E-1956's rule, applied to the new status.
    if TS has shipped unreviewed >/dev/null 2>&1; then
        report_pass "it is shipped (so 'obsolete' is refused on it)"
    else
        report_fail "it is shipped" "member of shipped" "not a member"
    fi
    # The two lanes must not overlap.
    if TS has verification-track unreviewed >/dev/null 2>&1; then
        report_fail "it is not in the verification track" "not a member" "member"
    else
        report_pass "it is not in the verification track"
    fi
}

# ─── section G: an unreviewed blocker holds its dependent ────────────────────

test_blocks_dependents() {
    section "G. An unreviewed blocker still holds its dependent"
    local blocker dependent out
    blocker="$(add_findings_task "Audit that blocks" research)"
    force_status "$blocker" "unreviewed"
    dependent="$(add_task "Build on the findings")"
    force_status "$dependent" "ready"
    E task block "E-$dependent" --by "E-$blocker" >/dev/null 2>&1

    out="$(E task next 2>&1)"
    assert_not_contains "task next does not offer the dependent" "E-$dependent" "$out"
}

# ─── section H: the diagram regenerated from the table ───────────────────────

test_diagram_is_current() {
    section "H. The generated lifecycle diagram carries the new edges"
    local out rc
    out=$(cd "$WT" && just lifecycle-check 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then
        report_pass "committed lifecycle artifacts are current"
    else
        report_fail "just lifecycle-check" "exit 0" "exit=$rc"$'\n'"$out"
    fi

    local mmd="$WT/docs/status-lifecycle.mmd"
    assert_contains "the diagram draws the gate's inbound edge" \
        "underway --> unreviewed" "$(cat "$mmd")"
    assert_contains "and its outbound edge, taken by the USER" \
        "unreviewed --> completed: user" "$(cat "$mmd")"
    assert_contains "and the type restriction is rendered, not implied" \
        "(research/brainstorm)" "$(cat "$mmd")"
}

# ─── section I: regression ───────────────────────────────────────────────────

test_regression() {
    section "I. Project-wide regression"
    local out rc
    out=$(cd "$WT" && just test-go 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then report_pass "just test-go passes"
    else report_fail "just test-go" "exit 0" \
        "exit=$rc"$'\n'"$(printf '%s' "$out" | grep -E '^(FAIL|--- FAIL)' | head -20)"; fi

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
    [[ "$("${WT}/bin/endless-go" task-status label unreviewed 2>/dev/null)" == "Unreviewed" ]] \
        || { printf 'ERROR: %s predates `unreviewed` — run `just build`\n' "${WT}/bin/endless-go" >&2; exit 2; }

    printf '%sE-2016 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
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
    test_gate_is_passable
    test_implementation_types_refused
    test_outcome_required_at_the_gate
    test_registry
    test_blocks_dependents
    test_diagram_is_current
    test_regression

    [[ -n "$TMP" ]] && rm -rf "$TMP"
    summary
}

main "$@"

#!/usr/bin/env bash
#
# E-1889 verification — make reopening your own landed work the documented,
# tooled norm.
#
# The problem: agents reflexively file a NEW task for a bug they just
# introduced instead of reopening the task that introduced it. The cause was
# not carelessness — the handoff template instructed exactly that, the status
# diagram drew no edge from a done status back to `revisit`, and `revisit`'s
# own definition only ever spoke about replanning. So the reflex was correct
# behavior against wrong documentation.
#
# What this suite checks, and what it deliberately does NOT:
#
#   Behavior is pinned by REAL suites, not here — reopen landing `revisit`,
#   `session resume --reopen` refusing a decision-bearing status, each
#   file-time hint firing exactly when its condition holds, claim promotion
#   accepting `revisit`, and the canonical-mermaid byte-identity invariant.
#   Those are permanent invariants; a per-task script is retired when its task
#   lands, so they belong in tests/ and internal/. Section A runs them as a
#   FAIL-FAST gate — there is no point checking wording against a broken unit.
#
#   Sections B–E are this task's point-in-time ACCEPTANCE: the exact prose
#   shipped in the six handoff templates and three guide surfaces. Wording is
#   the deliverable here, so wording is what is asserted.
#
#   Section F is the project-wide regression.
#
# Run from anywhere inside the worktree:
#   esu && ./tests/tasks/e-1889-verify.sh
#
# Isolation: templates are rendered through the worktree's own `endless-go`
# into a throwaway project dir per render (the renderer materializes a copy of
# the template into <root>/.endless/templates on first render, so a reused dir
# would shadow the embedded candidate copy on the next run). Guide checks read
# tracked files and the rendered `endless guide` output. No real DB, ledger, or
# cache is touched; nothing needs teardown beyond the temp dir.
#
# Output: pass/fail per check, then a summary. Exit 0 all-passed, 1 any
# failure, 2 setup error.

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

# assert_contains DESC NEEDLE HAYSTACK
assert_contains() {
    if [[ "$3" == *"$2"* ]]; then report_pass "$1"
    else report_fail "$1" "contains: $2" "$(printf '%s' "$3" | head -20)"; fi
}
# assert_not_contains DESC NEEDLE HAYSTACK
assert_not_contains() {
    if [[ "$3" != *"$2"* ]]; then report_pass "$1"
    else report_fail "$1" "must NOT contain: $2" "$(printf '%s' "$3" | head -20)"; fi
}

# ─── fixture ────────────────────────────────────────────────────────────────

WT=""; TMP=""; RENDER_SEQ=0

# render TEMPLATE [TASK_TYPE]: render handoff/<TEMPLATE> through the worktree
# binary and echo it. TASK_TYPE defaults to TEMPLATE — the shared partials
# (E-1822's `_mechanics.tmpl`) branch on `.task_type`, not on which wrapper
# pulled them in, so a wrapper rendered without it takes the default branch.
#
# Each render gets its own project root: the renderer writes the embedded
# template to <root>/.endless/templates on first use, and that on-disk copy
# then wins over the embedded one — so reusing a root would silently verify a
# stale template rather than the candidate build.
render() {
    RENDER_SEQ=$((RENDER_SEQ + 1))
    local root="${TMP}/render-${RENDER_SEQ}" ttype="${2:-$1}"
    mkdir -p "${root}/.endless"
    printf '{"spawned_id":2026,"title":"Probe","task_type":"%s","worktree_path":"/tmp/wt","branch":"task/2026-probe","restore_case":"reused","children_state":"none"}' \
        "${ttype}" \
        | ( cd "${root}" && "${WT}/bin/endless-go" template render "handoff/$1" 2>&1 )
}

# ─── section A: the real suites (FAIL-FAST GATE) ────────────────────────────

test_units() {
    section "A. Behavior lives in the real suites (fail-fast gate)"
    local out rc

    out=$(cd "$WT" && go test ./internal/monitor/ 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then
        report_pass "go test ./internal/monitor/ — claim promotion accepts revisit"
    else
        report_fail "go test ./internal/monitor/" "exit 0" \
            "exit=$rc"$'\n'"$(printf '%s' "$out" | tail -25)"
    fi

    out=$(cd "$WT" && uv run pytest -q \
        tests/test_task_reopen.py \
        tests/test_task_add_hints.py \
        tests/test_session_resume_recover.py \
        tests/test_status_lifecycle_sync.py 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then
        report_pass "pytest reopen + add-hints + resume-recover + lifecycle-sync"
    else
        report_fail "pytest for this task's units" "exit 0" \
            "exit=$rc"$'\n'"$(printf '%s' "$out" | tail -25)"
    fi

    if [[ "${FAIL_COUNT}" -gt 0 ]]; then
        printf '\n  %sABORTING%s — units failed; skipping the acceptance checks.\n' \
            "${RED}${BOLD}" "${RESET}"
        return 1
    fi
    return 0
}

# ─── section B: the four-case wording in the five work templates ────────────

test_handoff_four_cases() {
    section "B. Every work handoff carries the four-case discovery test"
    # todo/bugfix/epic/research take it from the shared `handoff_focus` partial;
    # `claim` (the claimed-into-a-live-session handoff) pulls the same partial,
    # so it is checked here rather than trusted; `respawn` keeps its own copy —
    # E-1822 deliberately left it out of the shared mechanics.
    local t out spec ttype
    for spec in todo bugfix epic research respawn:todo claim:todo; do
        t="${spec%%:*}"; ttype="${spec##*:}"
        out="$(render "$t" "$ttype")"
        if [[ -z "$out" ]]; then
            report_fail "handoff/$t renders" "non-empty output" "(empty)"
            continue
        fi
        assert_contains "handoff/$t — opens the four cases" \
            "For anything you discover along the way:" "$out"
        assert_contains "handoff/$t — case 1: do it inside the work underway" \
            "Could it reasonably be done now, inside the work already underway? Do it" "$out"
        assert_contains "handoff/$t — case 1: says HOW to report it" \
            "add a \`discovery\` note to" "$out"
        assert_contains "handoff/$t — case 2: reopen your own landed work" \
            "Is it a bug in work THIS session landed? Reopen that task" "$out"
        assert_contains "handoff/$t — case 2: names the reopen command" \
            "--status revisit --db main\`) and fix it there." "$out"
        assert_contains "handoff/$t — case 3: file it, and confirm first" \
            "Otherwise file it (\`--cleans-up E-2026\`) and confirm before" "$out"
        assert_contains "handoff/$t — case 4: file the cause, not each symptom" \
            "Check whether they share a root cause" "$out"
        # The line this task exists to remove: filing as the default AND a
        # blanket ban on fixing anything inline.
        assert_not_contains "handoff/$t — the old file-everything line is gone" \
            "don't fix them inline" "$out"
    done
}

# ─── section C: brainstorm keeps its own deliverable ────────────────────────

test_handoff_brainstorm() {
    section "C. brainstorm keeps 'file it' (that IS its deliverable) + root cause"
    local out; out="$(render brainstorm)"
    # For a brainstorm, emitting tasks and decisions is the point, so the
    # four-case rewrite would contradict the template's whole purpose.
    assert_contains "brainstorm — still files ideas as tasks/decisions" \
        "file them as new tasks" "$out"
    assert_contains "brainstorm — still prefers filing over building inline" \
        "rather than building inline" "$out"
    assert_contains "brainstorm — gained the root-cause line" \
        "Check whether they share a root cause" "$out"
    assert_not_contains "brainstorm — did NOT take the four-case rewrite" \
        "Is it a bug in work THIS session landed?" "$out"
}

# ─── section D: the guide pattern ───────────────────────────────────────────

test_guide_pattern() {
    section "D. 'Fix a bug in your own landed work' is in the rendered guide"
    local orch tasks rc
    orch="$(cd "$WT" && uv run endless guide orchestration 2>/dev/null)"; rc=$?
    if [[ $rc -ne 0 || -z "$orch" ]]; then
        report_fail "endless guide orchestration renders" "exit 0, non-empty" "exit=$rc"
        return
    fi
    tasks="$(cd "$WT" && uv run endless guide tasks 2>/dev/null)"
    if [[ -z "$tasks" ]]; then
        report_fail "endless guide tasks renders" "non-empty" "(empty)"
        return
    fi

    assert_contains "section exists" \
        "Fix a bug in your own landed work" "$orch"
    # 1. the judgment
    assert_contains "states the judgment (not new work)" \
        "is not new work" "$orch"
    # 2. the mechanics
    assert_contains "gives the reopen command" \
        "endless task update E-<id> --status revisit" "$orch"
    assert_contains "says to reuse the existing worktree" \
        "Reuse the task's existing worktree" "$orch"
    assert_contains "forbids a second worktree for one task" \
        "Do **not** create a second worktree for the same task." "$orch"
    # 3. re-verification
    assert_contains "says to re-run the task's own verify suite" \
        "e-<id>-verify.sh" "$orch"
    assert_contains "says to extend it rather than author a second" \
        "rather than authoring a second script" "$orch"
    # 4. the boundary
    assert_contains "names the boundary (when a separate task IS right)" \
        "When a separate task IS right" "$orch"
    assert_contains "gives the defect-vs-new-work test" \
        "new work the conversation surfaced" "$orch"

    # The cross-reference: a session reading tasks.md must be able to get here.
    assert_contains "tasks.md cross-references the pattern" \
        "orchestration.md#fix-a-bug-in-your-own-landed-work" \
        "$(cat "$WT/docs/guide/tasks.md")"
    assert_contains "the cross-reference renders" \
        "Reopen that task" "$tasks"
}

# ─── section E: the revisit definition ──────────────────────────────────────

test_revisit_row() {
    section "E. The 'revisit' status row reads as specified"
    local index; index="$(cat "$WT/docs/guide/index.md")"
    assert_contains "row covers the stale-plan case" \
        "a partial plan that no longer holds" "$index"
    assert_contains "row covers the shipped-and-wrong case" \
        "work that shipped and turned out wrong" "$index"
    assert_contains "row names reopening your own landed work" \
        "Reopening your own landed work lands here." "$index"
    assert_not_contains "the replanning-only definition is gone" \
        "Was partially planned but needs re-evaluation." "$index"

    # The mermaid edges the CLI has always accepted but the diagram never drew.
    # (Byte-identity across the three copies is the real test in section A;
    # this asserts the content the copies now carry.)
    local mmd; mmd="$(cat "$WT/docs/status-lifecycle.mmd")"
    local s
    for s in confirmed assumed completed; do
        assert_contains "diagram draws $s --> revisit" \
            "$s --> revisit: shipped work found wrong" "$mmd"
    done
    for s in declined obsolete; do
        assert_not_contains "diagram still leaves $s terminal" \
            "$s --> revisit" "$mmd"
    done
}

# ─── section F: project-wide regression ─────────────────────────────────────

test_regression() {
    section "F. Regression — full Go and Python suites"
    local out rc
    out=$(cd "$WT" && go test ./... 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then report_pass "go test ./... passes"
    else report_fail "go test ./..." "exit 0" \
        "exit=$rc"$'\n'"$(printf '%s' "$out" | grep -v '^ok\|no test files' | head -25)"; fi

    out=$(cd "$WT" && uv run pytest tests/ -q 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then report_pass "pytest tests/ passes"
    else report_fail "pytest tests/" "exit 0" \
        "exit=$rc"$'\n'"$(printf '%s' "$out" | tail -25)"; fi

    # NOTHING ELSE BELONGS HERE. In particular, do not invoke another task's
    # tests/tasks/e-NNNN-verify.sh. A per-task verify script is an acceptance
    # harness valid ONLY in the window just before its own task lands; after
    # that it is expired by design, and its going stale is its expected end
    # state, not a defect. Running one as a regression gate asserts that a
    # landed task's point-in-time wording never changes again — which is the
    # opposite of what these scripts mean.
}

# ─── main ────────────────────────────────────────────────────────────────────

main() {
    WT="$(git rev-parse --show-toplevel 2>/dev/null)"
    [[ -n "${WT}" ]] || { printf 'ERROR: not inside a git worktree\n' >&2; exit 2; }

    command -v go >/dev/null || { printf 'ERROR: go not on PATH\n' >&2; exit 2; }
    command -v uv >/dev/null || { printf 'ERROR: uv not on PATH\n' >&2; exit 2; }
    [[ -x "${WT}/bin/endless-go" ]] || {
        printf 'ERROR: %s missing — run `just build`\n' "${WT}/bin/endless-go" >&2
        exit 2
    }

    printf '%sE-1889 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  worktree: %s\n' "${WT}"

    TMP="$(mktemp -d)" || { printf 'ERROR: mktemp failed\n' >&2; exit 2; }
    trap '[[ -n "${TMP}" ]] && rm -rf "${TMP}"' EXIT

    if ! test_units; then
        summary
        exit 1
    fi

    test_handoff_four_cases
    test_handoff_brainstorm
    test_guide_pattern
    test_revisit_row
    test_regression

    summary
}

main "$@"

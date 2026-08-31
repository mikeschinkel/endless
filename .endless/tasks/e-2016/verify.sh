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
#   endless task verify E-2016
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
# touched. Harness shape borrowed from .endless/tasks/e-2018/verify.sh.

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
#
# ABORTS on a failed add rather than echoing LAST_ID anyway. That silent
# fallback cost a debugging cycle: E-1658's verb/type creation gate landed
# mid-task and started refusing two of the fixture's titles, so `add_task`
# returned the PREVIOUS task's id, later checks forced a status onto the wrong
# row, and two assertions failed with symptoms that looked like product bugs in
# opposite directions. A fixture that cannot create what a check needs must say
# so, not hand back something plausible.
add_task() {
    local title="$1"; shift
    local out
    if ! out="$(E task add "$title" --description "spec" "$@" 2>&1)"; then
        printf '\n  %sFIXTURE ERROR%s: task add %q failed:\n%s\n' \
            "${RED}${BOLD}" "${RESET}" "$title" "$out" >&2
        exit 2
    fi
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
    epic="$(add_task "Anchor epic for $2" --type epic)" || exit 2
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

    # Seed the project verb list. E-1658 gates CREATION on verb category: a
    # research/brainstorm title must lead with an `investigation` verb and a
    # todo/bugfix title with an `action` one. matchers._resolved_verbs takes the
    # FIRST source that exists — project, else machine, else the built-in
    # defaults — and does not layer them, so the moment `task add`
    # auto-registers one verb into the project file, that file SHADOWS every
    # default and the categories vanish. Seeding it explicitly is what keeps
    # these checks about E-2016's gate rather than E-1658's.
    mkdir -p "$REPO/.endless"
    cat > "$REPO/.endless/verbs.jsonl" <<'VERBS'
{"value": "audit", "definition": "to examine systematically", "category": ["investigation"]}
{"value": "explore", "definition": "to investigate possibilities in a space", "category": ["investigation"]}
{"value": "anchor", "definition": "to fix or secure firmly in place", "category": ["action"]}
{"value": "add", "definition": "to introduce or include something new", "category": ["action"]}
{"value": "build", "definition": "to construct", "category": ["action"]}
{"value": "fix", "definition": "to repair", "category": ["action"]}
VERBS

    [[ "$(E sql 'SELECT count(*) FROM projects' --tsv 2>/dev/null)" == "1" ]] || return 1
    return 0
}

# ─── section A: unit tests (FAIL-FAST GATE) ──────────────────────────────────

test_units() {
    section "A. Unit tests for the new behavior (fail-fast gate)"
    local out rc

    out=$(cd "$WT" && go test ./internal/taskstatus/ ./internal/events/ \
        ./internal/templatecmd/ ./internal/hookcmd/ 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then
        report_pass "go test taskstatus + events + templatecmd + hookcmd passes"
    else
        report_fail "go test taskstatus + events + templatecmd + hookcmd" \
            "exit 0" "exit=$rc"$'\n'"$(printf '%s' "$out" | grep -v '^ok' | head -25)"
    fi

    out=$(cd "$WT" && uv run pytest tests/test_research_gate.py \
        tests/test_completed_status.py tests/test_status_lifecycle_sync.py \
        tests/test_handoff.py -q 2>&1); rc=$?
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
    b="$(add_findings_task "Explore the thing" brainstorm)"
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

    # And `completed` is not theirs either. When E-2016 landed, the findings
    # lane still admitted todo/bugfix directly, on the theory that a
    # todo-typed audit should finish there. E-1658 retired that: it gates
    # CREATION on verb category (a todo cannot even be TITLED "Audit ...")
    # and makes completed-eligibility a rule about type rather than about the
    # title's verb. So the implementation types now live wholly in the
    # verification lane, and this asserts the whole lane is closed to them.
    E task update "E-$t" --status completed >/dev/null 2>&1
    assert_eq "a todo cannot reach completed either" "underway" "$(ST "$t")"
    E task update "E-$b" --status completed >/dev/null 2>&1
    assert_eq "nor can a bugfix" "underway" "$(ST "$b")"

    # The lane they DO have is intact — this change must not have stranded them.
    E task update "E-$t" --status unverified >/dev/null 2>&1
    assert_eq "a todo still reports done at unverified" "unverified" "$(ST "$t")"
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

    out=$(cd "$WT" && just guide-check 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then
        report_pass "the generated guide cross-reference is current"
    else
        report_fail "just guide-check" "exit 0" "exit=$rc"$'\n'"$out"
    fi
}

# ─── section H2: the guide documents it ──────────────────────────────────────

test_guide_documents_it() {
    section "H2. The guide documents the status a reader will hit"
    # A status nobody can read about is a status nobody uses correctly. The
    # table is hand-written (only the diagram above it is generated), so
    # nothing else would catch its absence.
    local idx tasks
    idx="$(cat "$WT/docs/guide/index.md")"
    tasks="$(cat "$WT/docs/guide/tasks.md")"

    assert_contains "the status table has a row for it" '| `unreviewed`' "$idx"
    # `completed` is the status `unreviewed` leads to, and the table had no row
    # for it at all — a gap this task's own edit made newly visible.
    assert_contains "and a row for \`completed\`, which it leads to" '| `completed`' "$idx"
    assert_contains "blocking semantics say it still blocks" \
        'B in `unreviewed` → A is **still blocked**' "$idx"
    assert_contains "the happy path names it for findings work" "status unreviewed" "$idx"
    assert_contains "and tells an agent not to self-complete findings work" \
        "Don't mark research or brainstorm items \`completed\`" "$idx"
    assert_contains "task active's comment lists all three" \
        "underway + unverified + unreviewed" "$idx"
    assert_contains "the tasks guide shows the command" "--status unreviewed" "$tasks"
    assert_contains "and counts it as shipped for the obsolete refusal" \
        '`unverified`, `unreviewed`, `confirmed`' "$tasks"
}

# ─── section H3: the handoff obeys the same table ────────────────────────────

# render_handoff TYPE — the spawn handoff a session of that type is handed.
render_handoff() {
    printf '{"spawned_id":9999,"label_prefix":"E-9999","title":"Audit the thing","task_type":"%s","worktree_path":"/tmp/wt","branch":"b","child_count":0,"children_state":"","report_gate":true}' "$1" \
        | "$WT/bin/endless-go" template render "handoff/$1"
}

test_handoff_agrees_with_the_table() {
    section "H3. The spawn handoff instructs a legal transition"
    # The gap this task shipped with, found by another session: the handoff is
    # the ONLY lifecycle documentation a spawned session actually obeys — it is
    # pasted into its first turn and names the exact command to finish with.
    # E-2016 moved research and brainstorm behind `unreviewed` and left the
    # templates saying `completed`, so every spawned research session was told
    # to run a command the executor refuses. Both the spawn and claim variants
    # carried it, because both include handoff/_mechanics.
    local out typ
    for typ in research brainstorm; do
        out="$(render_handoff "$typ")"
        assert_contains "the $typ handoff names the review gate" \
            "--status unreviewed" "$out"
        assert_not_contains "and no longer instructs the refused command ($typ)" \
            "--status completed" "$out"
        assert_contains "and says the terminal is the user's to set ($typ)" \
            "mark \`completed\` yourself" "$out"
    done

    # A todo must NOT have been dragged into the review lane by the same edit.
    out="$(render_handoff todo)"
    assert_contains "the todo handoff still says unverified" "--status unverified" "$out"
    assert_not_contains "and was not dragged into the review lane" "--status unreviewed" "$out"
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
    test_guide_documents_it
    test_handoff_agrees_with_the_table
    test_regression

    [[ -n "$TMP" ]] && rm -rf "$TMP"
    summary
}

main "$@"

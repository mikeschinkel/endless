#!/usr/bin/env bash
#
# E-1891 verification script — one owning package for the task status vocabulary.
#
# Status was the only closed vocabulary in the system with no owning package.
# Task type, session kind and session↔task relation each have one; status lived
# as bare literals across ~20 sites in two languages, in four shapes: copies of
# the whole vocabulary, policy subsets, lists buried inside SQL string literals,
# and per-status ordering data.
#
# The failure mode is OMISSION, not typos. E-1648 added `submitted` and missed
# two sites. E-1845 added `untriaged` and had to hand-edit every site with
# nothing to catch a miss. Both are now structurally impossible: one map in
# internal/taskstatus, read everywhere, with partition invariants that turn a
# forgotten grouping into a failing test.
#
# What this script covers is ACCEPTANCE — the point-in-time claims of this task:
# the live symptoms are fixed end-to-end, every converted site reads from the
# registry, and the duplicates are gone. The PERMANENT invariants live where
# they belong and run forever: internal/taskstatus/taskstatus_test.go (partition
# and subset invariants, membership pinning), internal/taskstatuscmd (the
# subcommand contract), and tests/test_status_registry_client.py (the Python
# client holds nothing of its own). See E-1889's convention.
#
# Run from anywhere inside the worktree:
#   esu && ./tests/tasks/e-1891-verify.sh
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
# touched. Harness shape borrowed from tests/tasks/e-1845-verify.sh.

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
# assert_no_match DESC FILE EXTENDED_REGEX  — the file must not match at all.
assert_no_match() {
    local hits
    hits="$(grep -nE "$3" "$WT/$2" 2>/dev/null | head -3)"
    if [[ -z "$hits" ]]; then report_pass "$1"
    else report_fail "$1" "no match for /$3/ in $2" "$hits"; fi
}

# ─── shared fixture ──────────────────────────────────────────────────────────

WT=""; TMP=""; REPO=""

# E: run the worktree's Python CLI against the isolated DB, from the repo dir.
E() { ( cd "$REPO" && uv run --project "$WT" endless "$@" ); }
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
# add_child PARENT_ID STATUS: add a child of PARENT_ID forced to STATUS with a
# raw write. Fast, and enough for checks that only read the status back — but it
# bypasses the event executor, so no epic re-derivation fires. Use add_child_via_cli
# when the derivation is what is under test.
add_child() {
    local t; t="$(add_task "Add a child in $2" --parent "E-$1")"
    E sql "UPDATE tasks SET status='$2' WHERE id=$t" --write >/dev/null 2>&1
    echo "$t"
}
# add_child_via_cli PARENT_ID STATUS: same, through `task update`, so the status
# change goes through the executor and the parent epic re-derives.
add_child_via_cli() {
    local t; t="$(add_task "Add a child in $2" --parent "E-$1")"
    E task update "E-$t" --status "$2" >/dev/null 2>&1
    echo "$t"
}

setup_fixture() {
    TMP="$(mktemp -d)"
    REPO="$TMP/repo"
    export XDG_CONFIG_HOME="$TMP/xdg"
    export XDG_CACHE_HOME="$TMP/cache"
    # Prepend the freshly-built worktree binary so the Python event bridge's
    # PATH fallback (cwd is the /tmp repo, not a self-dev worktree) execs
    # candidate code, not the stale global endless-go. This matters twice as
    # much for E-1891: `endless.statuses` shells out to `task-status`, which the
    # global binary does not know until this lands.
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

    out=$(cd "$WT" && go test ./internal/taskstatus/ ./internal/taskstatuscmd/ 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then
        report_pass "go test ./internal/taskstatus/ ./internal/taskstatuscmd/ passes"
    else
        report_fail "go test ./internal/taskstatus/ ./internal/taskstatuscmd/" \
            "exit 0" "exit=$rc"$'\n'"$out"
    fi

    out=$(cd "$WT" && uv run pytest tests/test_status_registry_client.py \
        tests/test_handoff_children_state.py tests/test_replaced_by_inline.py -q 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then
        report_pass "pytest registry-client + children-state + vocabulary suites pass"
    else
        report_fail "pytest status-registry suites" "exit 0" \
            "exit=$rc"$'\n'"$(printf '%s' "$out" | tail -25)"
    fi

    # Named explicitly, not just swept up by the file above: this is the
    # regression that made E-1891 fatal inside every worktree branched before
    # it. If someone deletes the test, the suite must notice.
    out=$(cd "$WT" && uv run pytest tests/test_status_registry_client.py -q \
        -k "too_old or when_it_can_answer or memoized" 2>&1); rc=$?
    if [[ $rc -eq 0 && "$out" == *"3 passed"* ]]; then
        report_pass "the stale-worktree-binary fallback is covered by name"
    else
        report_fail "stale-worktree-binary regression tests" "3 passed, exit 0" \
            "exit=$rc"$'\n'"$(printf '%s' "$out" | tail -15)"
    fi

    if [[ "${FAIL_COUNT}" -gt 0 ]]; then
        printf '\n  %sABORTING%s — unit tests failed; skipping end-to-end checks.\n' \
            "${RED}${BOLD}" "${RESET}"
        return 1
    fi
    return 0
}

# ─── section B: live symptom 1 — `update --status submitted` ─────────────────

test_symptom_submitted_accepted() {
    section "B. Live symptom 1: 'task update --status submitted' is accepted"
    # `update_plan`'s local `valid` tuple omitted `submitted`, so a status that
    # every other surface accepted (and `task submit` sets) was refused with a
    # bare "Invalid status". E-1956 fixed the immediate case; E-1891 removed the
    # local copy that could reintroduce it. Asserted end-to-end through the CLI.
    local t out
    t="$(add_task "Add a submittable thing")"
    out="$(E task update "E-$t" --status submitted 2>&1)"
    assert_eq "the status actually lands" "submitted" "$(ST "$t")"
    assert_not_contains "no 'Invalid status' refusal" "Invalid status" "$out"

    # The general form of the defect: the validator and the advertised list are
    # now the same list, so no advertised status can be refused as unknown.
    # Walks the registry, so a status added tomorrow is covered with no edit.
    #
    # Deliberately asserts only the absence of the VOCABULARY refusal. Several
    # statuses carry further gates that are not this task's business —
    # `completed` needs an --outcome and a completable lead verb, research and
    # epic types refuse the verification track — and those refusals name their
    # own reason rather than "Invalid status".
    local s bad=""
    for s in $(TS get all); do
        t="$(add_task "Add a $s thing")"
        out="$(E task update "E-$t" --status "$s" 2>&1)"
        [[ "$out" == *"Invalid status"* ]] && bad="$bad $s"
    done
    assert_eq "no advertised status is refused as unknown" "" "$bad"
}

# ─── section C: live symptom 2 — the children-state breakdown reconciles ─────

test_symptom_children_state() {
    section "C. Live symptom 2: the children-state breakdown reconciles"
    # `_CHILDREN_STATE_ORDER` omitted `submitted`, so a submitted child was
    # counted in the "(N total)" suffix but rendered no bucket — the breakdown
    # contradicted its own docstring. Now derived from the registry, which
    # asserts that children-state-order and terminal partition the vocabulary.
    local epic out
    epic="$(add_task "Add an epic" --type epic)"
    add_child "$epic" "submitted" >/dev/null
    out="$(E task handoff "E-$epic" 2>&1)"
    assert_contains "a submitted child renders its own bucket" \
        "1 submitted (1 total)" "$out"

    # One child per status: the buckets must sum to the total, whatever the
    # vocabulary is.
    local epic2 s n=0
    epic2="$(add_task "Add a full-spectrum epic" --type epic)"
    for s in $(TS get all); do add_child "$epic2" "$s" >/dev/null; n=$((n + 1)); done
    out="$(E task handoff "E-$epic2" 2>&1)"
    assert_contains "the total counts every child" "($n total)" "$out"

    local line bucketed
    line="$(printf '%s' "$out" | grep -o 'Children: [^.]*' | head -1)"
    bucketed="$(printf '%s' "$line" \
        | sed 's/^Children: //; s/ ([0-9]* total)//' \
        | tr ',' '\n' | awk '{s += $1} END {print s+0}')"
    assert_eq "the buckets sum to the total — nothing silently dropped" \
        "$n" "$bucketed"
}

# ─── section D: the subcommand answers ───────────────────────────────────────

test_subcommand() {
    section "D. 'endless-go task-status' is the seam"
    assert_eq "get emits one status per line, in group order" \
        "underway ready submitted unplanned untriaged" \
        "$(TS get derivation-precedence | tr '\n' ' ' | sed 's/ $//')"
    assert_eq "sql-list quotes and joins for an IN clause" \
        "'untriaged','unplanned'" "$(TS sql-list pre-judgment)"
    assert_eq "rank returns the index within an ordered group" "4" \
        "$(TS rank derivation-precedence untriaged)"
    assert_eq "rank returns the sentinel outside it" "-1" \
        "$(TS rank derivation-precedence confirmed)"
    assert_eq "has exits 0 for a member" "0" \
        "$(TS has terminal confirmed >/dev/null 2>&1; echo $?)"
    assert_eq "has exits 1 for a non-member" "1" \
        "$(TS has terminal ready >/dev/null 2>&1; echo $?)"
    assert_eq "an unknown group exits 2, not 1" "2" \
        "$(TS get no-such-group >/dev/null 2>&1; echo $?)"
    # The two disagreeing blocker sets are one group. `task next` and the status
    # line's GetActiveBlockers now read Terminal, a superset of both, so a
    # dependent blocked by declined or obsolete work is released — which is what
    # `endless guide`'s blocking-semantics table has always said.
    assert_eq "there is one blocker-resolution group, not two" "" \
        "$(TS groups | grep -E '^unblocking' | tr '\n' ' ')"
    assert_eq "an unknown status exits 2, not 1" "2" \
        "$(TS has terminal no-such-status >/dev/null 2>&1; echo $?)"
    assert_eq "label mirrors tasktype.Label()" "Untriaged" "$(TS label untriaged)"
    assert_eq "glyph is semantic and colorless" "◌" "$(TS glyph untriaged)"
}

# ─── section E: the duplicates are gone ──────────────────────────────────────

test_duplicates_removed() {
    section "E. Every converted site reads the registry, not a literal"
    # The whole-vocabulary copies.
    assert_no_match "statuses.py holds no vocabulary of its own" \
        "src/endless/statuses.py" "^TASK_STATUSES = \("
    assert_contains "statuses.py fetches the vocabulary from Go" \
        'TASK_STATUSES = get("all")' "$(cat "$WT/src/endless/statuses.py")"
    assert_no_match "update_plan keeps no local valid-status tuple" \
        "src/endless/task_cmd.py" "^ *valid = \(\"untriaged\""

    # The policy subsets.
    assert_no_match "_SUBMITTABLE_FROM carries no inline statuses" \
        "src/endless/task_cmd.py" "_SUBMITTABLE_FROM = \("
    assert_no_match "_CHILDREN_STATE_ORDER carries no inline statuses" \
        "src/endless/task_cmd.py" "_CHILDREN_STATE_ORDER = \($"
    assert_no_match "_DESCRIPTION_RESET_FROM carries no inline statuses" \
        "src/endless/task_cmd.py" "_DESCRIPTION_RESET_FROM.*frozenset\(\{"
    assert_no_match "_PRE_JUDGMENT_STATUSES carries no inline statuses" \
        "src/endless/task_cmd.py" "_PRE_JUDGMENT_STATUSES.*frozenset\(\{"
    assert_no_match "_CLAIM_REQUIRES_FORCE carries no inline statuses" \
        "src/endless/task_cmd.py" "_CLAIM_REQUIRES_FORCE.*frozenset\(\{"
    assert_no_match "_REOPEN_TO_REVISIT carries no inline statuses" \
        "src/endless/session_cmd.py" "_REOPEN_TO_REVISIT.*frozenset\(\{\"?[a-z]"

    # The SQL string literals — the least visible and most rot-prone shape.
    assert_no_match "next_tasks' NOT IN list is rendered, not typed" \
        "src/endless/task_cmd.py" "NOT IN \('confirmed', 'assumed', 'completed', 'blocked'"
    assert_contains "its blocker subquery reads the one terminal group" \
        "statuses.sql_list('terminal')" \
        "$(sed -n '/^def next_tasks/,/^def /p' "$WT/src/endless/task_cmd.py")"
    assert_no_match "task active's IN list is rendered, not typed" \
        "src/endless/task_cmd.py" "IN \('underway', 'unverified'\)"
    assert_no_match "the Go claim promotion's IN list is rendered, not typed" \
        "internal/monitor/session.go" "IN \('untriaged','unplanned'"
    assert_no_match "GetActiveTasks' IN list is rendered, not typed" \
        "internal/monitor/task.go" "IN \('underway', 'untriaged'"
    assert_no_match "the session-status terminal set is rendered, not typed" \
        "internal/monitor/session_status.go" "terminalStatusSet = \"'confirmed'"

    # Per-status ordering data.
    assert_no_match "the epic derivation ladder is data, not a switch of bools" \
        "internal/events/epic_derivation.go" "anyNeedsPlan|anyUntriaged|anySubmitted"
    assert_no_match "the Go children-state order is derived, not typed" \
        "internal/hookcmd/claim_handoff.go" "childrenStateOrder = \[\]string"
}

# ─── section F: the two terminal sets collapsed into one ─────────────────────

test_terminal_sets_collapsed() {
    section "F. The byte-identical terminal sets are one group"
    # `_TERMINAL_STATUSES` and `_RELATION_TERMINAL_STATUSES` were the same five
    # statuses under two names — both asking "is this finished or abandoned".
    # The name may survive in a comment recording why it went; what must be
    # gone is every USE of it.
    assert_no_match "_RELATION_TERMINAL_STATUSES has no uses left" \
        "src/endless/task_cmd.py" "^[^#]*_RELATION_TERMINAL_STATUSES"
    assert_contains "the survivor reads the registry" \
        '_TERMINAL_STATUSES = frozenset(statuses.get("terminal"))' \
        "$(cat "$WT/src/endless/task_cmd.py")"
    # And the three separate Go spellings of the same question.
    assert_contains "monitor.IsTerminalTaskStatus delegates" \
        "taskstatus.Has(taskstatus.Terminal, status)" \
        "$(cat "$WT/internal/monitor/files.go")"
    assert_contains "sessionstatuscmd.isTerminal delegates" \
        "taskstatus.Has(taskstatus.Terminal, status)" \
        "$(cat "$WT/internal/sessionstatuscmd/session_status.go")"
    assert_contains "the epic derivation's terminal check delegates" \
        "taskstatus.Has(taskstatus.Terminal, s)" \
        "$(cat "$WT/internal/events/epic_derivation.go")"
}

# ─── section G: behavior preserved through the relocation ────────────────────

test_behavior_preserved() {
    section "G. Relocating the sets did not change what they contain"
    # The rule E-1891 held itself to. Each check drives a surface whose set
    # moved and asserts the same outcome it produced before.
    local t out

    # `task next` still omits untriaged/submitted and offers ready.
    t="$(add_task "Add a next-visible thing")"
    E task update "E-$t" --status ready >/dev/null 2>&1
    local hidden; hidden="$(add_task "Add a next-hidden thing")"
    out="$(E task next 2>&1)"
    assert_contains "'task next' offers a ready task" "E-$t" "$out"
    assert_not_contains "'task next' still omits an untriaged task" "E-$hidden" "$out"

    # `task active` still lists underway + unverified, underway first.
    local a b
    a="$(add_task "Add an unverified thing")"; E sql "UPDATE tasks SET status='unverified' WHERE id=$a" --write >/dev/null 2>&1
    b="$(add_task "Add an underway thing")";   E sql "UPDATE tasks SET status='underway'   WHERE id=$b" --write >/dev/null 2>&1
    out="$(E task active 2>&1)"
    assert_contains "'task active' lists the underway task" "E-$b" "$out"
    assert_contains "'task active' lists the unverified task" "E-$a" "$out"
    assert_eq "underway still sorts before unverified" "E-$b" \
        "$(printf '%s' "$out" | grep -oE "E-($a|$b)" | head -1)"

    # Epic derivation still walks the ladder in the same order.
    local epic
    epic="$(add_task "Add a ladder epic" --type epic)"
    add_child_via_cli "$epic" "submitted" >/dev/null
    assert_eq "a submitted child derives the epic to submitted" \
        "submitted" "$(ST "$epic")"
    add_child_via_cli "$epic" "ready" >/dev/null
    assert_eq "a ready child outranks it" "ready" "$(ST "$epic")"
    add_child_via_cli "$epic" "underway" >/dev/null
    assert_eq "an underway child outranks that" "underway" "$(ST "$epic")"

    # Claiming settled work still needs --force.
    local settled
    settled="$(add_task "Add a settled thing")"
    E sql "UPDATE tasks SET status='confirmed' WHERE id=$settled" --write >/dev/null 2>&1
    out="$(E task claim "E-$settled" 2>&1)"
    assert_contains "claiming a settled task is refused without --force" \
        "--force" "$out"
}

# ─── section H: the client fails closed ──────────────────────────────────────

test_fails_closed() {
    section "H. Python fails closed when endless-go cannot answer"
    # The decision E-1891's plan asked be made explicitly: there is no fallback
    # to fall back TO — a hardcoded Python copy is the duplicate this deletes —
    # so it says so and stops, naming the fix.
    local stale out
    stale="$TMP/stale-bin"
    mkdir -p "$stale"
    printf '#!/bin/sh\necho "endless-go: unknown subcommand \\"task-status\\"" >&2\nexit 2\n' \
        > "$stale/endless-go"
    chmod +x "$stale/endless-go"

    out=$( cd "$TMP" && PATH="$stale:$PATH" uv run --project "$WT" endless --help 2>&1 )
    assert_contains "it names the command that failed" "task-status get all" "$out"
    assert_contains "the resolver falls back before giving up" \
        "_knows_task_status(str(worktree_bin))" \
        "$(cat "$WT/src/endless/statuses.py")"
    assert_contains "it names the remedy" "just install" "$out"
    assert_not_contains "and does not traceback" "Traceback" "$out"
}

# ─── section I: project-wide regression ──────────────────────────────────────

test_regression() {
    section "I. Regression — full Go suite + full Python suite"
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
    "${WT}/bin/endless-go" task-status groups >/dev/null 2>&1 \
        || { printf 'ERROR: %s predates `task-status` — run `just build`\n' "${WT}/bin/endless-go" >&2; exit 2; }

    printf '%sE-1891 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
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

    test_symptom_submitted_accepted
    test_symptom_children_state
    test_subcommand
    test_duplicates_removed
    test_terminal_sets_collapsed
    test_behavior_preserved
    test_fails_closed
    test_regression

    [[ -n "$TMP" ]] && rm -rf "$TMP"
    summary
}

main "$@"

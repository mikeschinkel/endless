#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1915 and records what was true when E-1915
# landed. Edit it only if you ARE E-1915. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1915 verification script — `task remove` no longer orphans relation rows.
#
# The bug: `task remove` deleted the `tasks` row and left every `task_deps` row
# referencing it, in BOTH directions. Nothing surfaced the orphan while the id
# stayed free (every consumer reaches relations through a join to `tasks`), but
# task ids are reused — so a later task taking the freed id silently inherited
# the dead relations and `task report` printed them as computed fact. That is
# how E-1911's report claimed an unrelated E-1914 as a follow-up it had filed.
#
# The fix is a guard, not a cascade: `task remove` REFUSES while any relation
# references the task and names the exact `unlink` command that clears each one.
# Deny rather than cascade because a severed relation is unrecoverable and a
# refusal costs one command. All relation types, no per-type exemption —
# `relates_to` included. `--cascade` (about CHILDREN, and pre-existing) widens
# the CHECK to the whole descendant set; it is deliberately not an escape hatch.
#
# Scope beyond the plan (confirmed with the user): `decision_relations` rows
# with target_kind='task' have the identical missing-FK exposure and are covered
# by the same guard and the same repair.
#
# `reconcile()` additionally clears rows earlier removals already orphaned.
#
# Second landing folds in two follow-ups found while verifying the first:
#   E-1927 — `task import --replace` / `import-json --clear` delete tasks by
#            emitting task.bulk_cleared, which never reaches remove_item, so
#            they orphaned rows exactly as `task remove` did. Same guard now.
#   E-1928 — `--cascade` always printed "0 descendant(s)": the count ran its own
#            recursive query AFTER emit_event, which deletes the subtree
#            synchronously. It now reads the id set captured before the delete.
#
# Run from anywhere inside the worktree:
#   endless task verify E-1915
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on a setup problem.
#
# Isolation: a throwaway git repo as project root under a temp dir, with its own
# XDG_CONFIG_HOME (own DB) and XDG_CACHE_HOME, and `roots` pinned to that temp
# dir so reconcile never walks the developer's real ~/Projects. No real
# DB/ledger/cache is touched. The Python CLI runs from the worktree source with
# <worktree>/bin prepended to PATH, so the event bridge execs the candidate
# endless-go rather than the global install.

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

WT=""; TMP=""; REPO=""; DBDIR=""; PID=""

# E: the worktree's Python CLI against the isolated DB, from the repo dir.
E() { ( cd "$REPO" && uv run --project "$WT" endless "$@" ); }
# Q SQL: one scalar/row out of the isolated DB.
Q() { E sql "$1" --tsv 2>/dev/null; }
# W SQL: a write against the isolated DB (fixture seeding).
W() { E sql "$1" --write >/dev/null 2>&1; }

# Task ids are fixed so every assertion can name them literally.
VICTIM=8001     # the task each removal targets
PEER=8002       # the far endpoint of most relations
UPSTREAM=8003   # holds a relation where VICTIM is the TARGET
LONE=8004       # no relations at all — the regression control
PARENT=8100     # --cascade root, holds no relation itself
CHILD=8101
GRANDCHILD=8102 # the only descendant holding a relation
DECISION=7

setup_fixture() {
    TMP="$(mktemp -d)"
    REPO="$TMP/repo"
    DBDIR="$TMP/xdg/endless"
    export XDG_CONFIG_HOME="$TMP/xdg"
    export XDG_CACHE_HOME="$TMP/cache"
    # Prepend the worktree binary so the Python event bridge's PATH fallback
    # (cwd is the /tmp repo, not a self-dev worktree) execs candidate code.
    export PATH="$WT/bin:$PATH"
    mkdir -p "$REPO" "$DBDIR"

    git -C "$REPO" init -q
    git -C "$REPO" config user.email verify@test
    git -C "$REPO" config user.name verify
    git -C "$REPO" commit -q --allow-empty -m "initial commit"

    # Pin reconcile's scan to the temp dir. Without this, `project list` walks
    # the developer's real project roots — slow, and it makes the DB contents
    # depend on the machine.
    printf '{"roots": ["%s"]}\n' "$TMP" > "$DBDIR/config.json"

    E project register "$REPO" --name probe --label Probe --desc d --lang Go --status active >/dev/null 2>&1
    PID="$(Q "SELECT id FROM projects WHERE name='probe'")"
    [[ -n "$PID" ]] || return 1

    W "INSERT INTO tasks (id, project_id, title, status, phase) VALUES
         ($VICTIM,   $PID, 'Victim task',   'ready', 'now'),
         ($PEER,     $PID, 'Peer task',     'ready', 'now'),
         ($UPSTREAM, $PID, 'Upstream task', 'ready', 'now'),
         ($LONE,     $PID, 'Lone task',     'ready', 'now'),
         ($PARENT,   $PID, 'Parent task',   'ready', 'now')"
    W "INSERT INTO tasks (id, project_id, title, status, phase, parent_id) VALUES
         ($CHILD,      $PID, 'Child task',      'ready', 'now', $PARENT),
         ($GRANDCHILD, $PID, 'Grandchild task', 'ready', 'now', $CHILD)"
    W "INSERT INTO decisions (id, project_id, title, status, created_at)
         VALUES ($DECISION, $PID, 'A decision', 'accepted', '2026-01-01T00:00:00')"

    [[ "$(Q "SELECT count(*) FROM tasks")" == "7" ]] || return 1
    [[ "$(Q "SELECT count(*) FROM task_deps")" == "0" ]] || return 1
    return 0
}

# deps_touching ID: how many task_deps rows still reference ID as a task endpoint.
deps_touching() {
    Q "SELECT count(*) FROM task_deps
        WHERE (source_type='task' AND source_id=$1)
           OR (target_type='task' AND target_id=$1)"
}

# ─── A: the refusal, and the command it hands back ───────────────────────────

test_refusal_names_the_fix() {
    section "A. Removing a related task is refused, with the exact unlink command"

    E task link "$VICTIM" --to "E-$PEER" --type blocks >/dev/null 2>&1
    local out rc
    out="$(E task remove "$VICTIM" 2>&1)"; rc=$?

    assert_eq "the removal fails" "1" "$rc"
    assert_contains "...naming the exact command that clears the relation" \
        "endless task unlink E-$VICTIM --to E-$PEER --type blocks" "$out"
    assert_contains "...and saying WHY, not just that it refused" "ids are reused" "$out"
    assert_eq "the task is still there" \
        "1" "$(Q "SELECT count(*) FROM tasks WHERE id=$VICTIM")"
    assert_eq "...and so is its relation" "1" "$(deps_touching "$VICTIM")"

    # The refusal must land BEFORE task.deleted is emitted, or the ledger would
    # publish a deletion that never happened.
    assert_eq "no task.deleted event was emitted for the refused removal" \
        "0" "$(grep -c '"task.deleted"' "$REPO/.endless/verbs.jsonl" 2>/dev/null || echo 0)"

    E task unlink "$VICTIM" --to "$PEER" --type blocks >/dev/null 2>&1
}

# ─── B: every type, no exemption ─────────────────────────────────────────────

test_every_type_refused() {
    section "B. Refused for each of the six types independently — relates_to included"

    # The six a task removal can touch. reverses/modifies are decision↔decision
    # and there is no `decision remove`, so they are unreachable from here.
    local t out rc
    for t in blocks implements replaces documents cleans_up relates_to; do
        E task link "$VICTIM" --to "E-$PEER" --type "$t" >/dev/null 2>&1
        out="$(E task remove "$VICTIM" 2>&1)"; rc=$?
        if [[ $rc -ne 0 && "$out" == *"--type $t"* ]]; then
            report_pass "'$t' alone blocks the removal, and is named in the fix"
        else
            report_fail "'$t' alone blocks the removal" "exit!=0 and '--type $t' in message" "exit=$rc: $out"
        fi
        E task unlink "$VICTIM" --to "$PEER" --type "$t" >/dev/null 2>&1
    done

    assert_eq "the fixture is back to zero relations" "0" "$(deps_touching "$VICTIM")"
}

# ─── C: the direction a naive check misses ───────────────────────────────────

test_target_side_refused() {
    section "C. Refused when the task is the TARGET, not only the source"

    E task link "$UPSTREAM" --to "E-$VICTIM" --type blocks >/dev/null 2>&1
    local out rc
    out="$(E task remove "$VICTIM" 2>&1)"; rc=$?

    assert_eq "removing the blocked task is refused" "1" "$rc"
    assert_contains "...with the command written from the SOURCE's side" \
        "endless task unlink E-$UPSTREAM --to E-$VICTIM --type blocks" "$out"

    E task unlink "$UPSTREAM" --to "$VICTIM" --type blocks >/dev/null 2>&1
}

# ─── D: relations to and from decisions ──────────────────────────────────────

test_decision_relations_refused() {
    section "D. Decision relations count too, in both tables"

    # task → decision lives in task_deps with target_type='decision'.
    E task link "$VICTIM" --to "ED-$DECISION" --type implements >/dev/null 2>&1
    local out rc
    out="$(E task remove "$VICTIM" 2>&1)"; rc=$?
    assert_eq "a task→decision relation refuses the removal" "1" "$rc"
    assert_contains "...cleared from the task's side" \
        "endless task unlink E-$VICTIM --to ED-$DECISION --type implements" "$out"
    E task unlink "$VICTIM" --to "ED-$DECISION" --type implements >/dev/null 2>&1

    # decision → task lives in decision_relations — the sibling table with the
    # same missing FK, and the same id-reuse exposure.
    E decision link "ED-$DECISION" --to "E-$VICTIM" --type documents >/dev/null 2>&1
    out="$(E task remove "$VICTIM" 2>&1)"; rc=$?
    assert_eq "a decision→task relation refuses the removal" "1" "$rc"
    assert_contains "...cleared from the decision's side" \
        "endless decision unlink ED-$DECISION --to E-$VICTIM --type documents" "$out"
    E decision unlink "ED-$DECISION" --to "E-$VICTIM" --type documents >/dev/null 2>&1

    assert_eq "the decision relation is gone again" \
        "0" "$(Q "SELECT count(*) FROM decision_relations WHERE target_id=$VICTIM")"
}

# ─── E: --cascade widens the check, it does not bypass it ────────────────────

test_cascade_checks_descendants() {
    section "E. --cascade is refused when a DESCENDANT holds the relation"

    # The root holds nothing; only the grandchild does. Checking only the root
    # would let a parent removal delete the child AND orphan its relations.
    E task link "$GRANDCHILD" --to "E-$PEER" --type relates_to >/dev/null 2>&1
    assert_eq "PRE: the root itself holds no relation" "0" "$(deps_touching "$PARENT")"

    local out rc
    out="$(E task remove "$PARENT" --cascade 2>&1)"; rc=$?
    assert_eq "the cascade removal is refused" "1" "$rc"
    assert_contains "...naming the descendant that is holding it up" "E-$GRANDCHILD:" "$out"
    assert_contains "...and the command that clears it" \
        "endless task unlink E-$GRANDCHILD --to E-$PEER --type relates_to" "$out"
    assert_eq "the whole subtree survives" \
        "3" "$(Q "SELECT count(*) FROM tasks WHERE id IN ($PARENT,$CHILD,$GRANDCHILD)")"

    # Cleared, the same cascade goes through and leaves nothing behind.
    E task unlink "$GRANDCHILD" --to "$PEER" --type relates_to >/dev/null 2>&1
    out="$(E task remove "$PARENT" --cascade 2>&1)"; rc=$?
    assert_eq "after unlink the cascade succeeds" "0" "$rc"
    assert_eq "...and the subtree is gone" \
        "0" "$(Q "SELECT count(*) FROM tasks WHERE id IN ($PARENT,$CHILD,$GRANDCHILD)")"
    assert_eq "...leaving zero task_deps rows referencing any of it" \
        "0|0|0" "$(deps_touching "$PARENT")|$(deps_touching "$CHILD")|$(deps_touching "$GRANDCHILD")"

    # E-1928 (second landing): the count in that success line. It used to run
    # its own recursive query AFTER emit_event — which deletes the subtree
    # synchronously — so it seeded from an empty table and always said 0.
    assert_contains "...and reports the descendants it actually deleted" \
        "and 2 descendant(s)" "$out"
}

# ─── E2: the bulk-clear door (E-1927, second landing) ────────────────────────

test_bulk_clear_guarded() {
    section "E2. task import --replace is guarded too — the other way tasks get deleted"

    printf '# Plan\n\n- [ ] Alpha item\n- [ ] Beta item\n' > "$REPO/PLAN.md"
    E task import "$REPO/PLAN.md" >/dev/null 2>&1
    local imported
    imported="$(Q "SELECT MIN(id) FROM tasks WHERE source_file LIKE '%PLAN.md'")"
    [[ -n "$imported" ]] || { report_fail "import seeded a task" "an id" "nothing"; return; }

    # An unguarded re-import is what orphaned the row: the task goes, the
    # relation stays, and a later task taking the freed id inherits it.
    E task link "$imported" --to "E-$PEER" --type blocks >/dev/null 2>&1
    local out rc
    out="$(E task import "$REPO/PLAN.md" --replace 2>&1)"; rc=$?

    assert_eq "the re-import is refused" "1" "$rc"
    assert_contains "...naming the file whose import is blocked" "PLAN.md" "$out"
    assert_contains "...and the command that clears the relation" \
        "endless task unlink E-$imported --to E-$PEER --type blocks" "$out"
    assert_eq "the imported task still exists" \
        "1" "$(Q "SELECT count(*) FROM tasks WHERE id=$imported")"
    assert_eq "...and nothing was orphaned" "1" "$(deps_touching "$imported")"

    # Cleared, the re-import goes through and leaves no orphan behind.
    E task unlink "$imported" --to "$PEER" --type blocks >/dev/null 2>&1
    out="$(E task import "$REPO/PLAN.md" --replace 2>&1)"; rc=$?
    assert_eq "after unlink the re-import succeeds" "0" "$rc"
    assert_eq "...and leaves zero task_deps rows referencing the freed id" \
        "0" "$(deps_touching "$imported")"

    # An unrelated relation elsewhere must not block an import refresh.
    E task link "$PEER" --to "E-$UPSTREAM" --type relates_to >/dev/null 2>&1
    out="$(E task import "$REPO/PLAN.md" --replace 2>&1)"; rc=$?
    assert_eq "a relation on a non-imported task does not block the import" "0" "$rc"
    E task unlink "$PEER" --to "$UPSTREAM" --type relates_to >/dev/null 2>&1

    W "DELETE FROM tasks WHERE source_file LIKE '%PLAN.md'"
    rm -f "$REPO/PLAN.md"
}

# ─── F: unlink, then remove, then reuse the id ───────────────────────────────

test_unlink_then_remove_is_clean() {
    section "F. The E-1911 sequence: unlink → remove → id reused inherits nothing"

    E task link "$VICTIM" --to "E-$PEER" --type cleans_up >/dev/null 2>&1
    E task unlink "$VICTIM" --to "$PEER" --type cleans_up >/dev/null 2>&1

    local out rc
    out="$(E task remove "$VICTIM" 2>&1)"; rc=$?
    assert_eq "the removal now succeeds" "0" "$rc"
    assert_eq "...and no task_deps row survives referencing the freed id" \
        "0" "$(deps_touching "$VICTIM")"

    # Reuse the id for unrelated work — the moment the old bug became visible.
    W "INSERT INTO tasks (id, project_id, title, status, phase)
         VALUES ($VICTIM, $PID, 'Unrelated later task', 'ready', 'now')"
    local shown
    shown="$(E task show "E-$PEER" 2>&1)"
    assert_not_contains "the reused id is not reported as a relation of the peer" \
        "Cleaned up by" "$shown"
    W "DELETE FROM tasks WHERE id=$VICTIM"
}

# ─── G: the repair for rows already orphaned ─────────────────────────────────

test_reconcile_repair() {
    section "G. reconcile deletes rows the ledger already orphaned, and says so"

    # Hand-seeded: ids no tasks row holds. This is the shape `task remove` left
    # behind before the guard existed.
    W "INSERT INTO task_deps (source_type, source_id, target_type, target_id, dep_type)
         VALUES ('task', 9999, 'task', $PEER, 'blocks')"
    W "INSERT INTO task_deps (source_type, source_id, target_type, target_id, dep_type)
         VALUES ('task', $PEER, 'task', 9998, 'cleans_up')"
    W "INSERT INTO decision_relations (source_decision_id, target_kind, target_id, relation_type)
         VALUES ($DECISION, 'task', 9997, 'documents')"
    # A live relation that must NOT be swept up with them.
    E task link "$PEER" --to "E-$LONE" --type relates_to >/dev/null 2>&1

    assert_eq "PRE: three orphans and one live relation are seeded" \
        "3|1" "$(Q "SELECT count(*) FROM task_deps")|$(Q "SELECT count(*) FROM decision_relations")"

    # reconcile() runs on `project list`; roots are pinned to $TMP above.
    local out
    out="$(E project list 2>&1)"

    assert_contains "the repair reports the count" "3 orphaned relation row(s)" "$out"
    assert_contains "...and names each row it deleted (task_deps, source side)" \
        "E-9999 blocks E-$PEER" "$out"
    assert_contains "...(task_deps, target side)" "E-$PEER cleans_up E-9998" "$out"
    assert_contains "...(decision_relations)" "ED-$DECISION documents E-9997" "$out"
    assert_eq "the orphans are gone" \
        "0|0" "$(Q "SELECT count(*) FROM task_deps WHERE source_id=9999 OR target_id IN (9998)")|$(Q "SELECT count(*) FROM decision_relations WHERE target_id=9997")"
    assert_eq "the live relation is untouched" \
        "1" "$(Q "SELECT count(*) FROM task_deps WHERE source_id=$PEER AND target_id=$LONE")"

    # Second pass has nothing to say — a clean DB reports nothing.
    out="$(E project list 2>&1)"
    assert_not_contains "a clean reconcile is silent about relations" \
        "orphaned relation row(s)" "$out"

    E task unlink "$PEER" --to "$LONE" --type relates_to >/dev/null 2>&1
}

# ─── H: regressions ──────────────────────────────────────────────────────────

test_regressions() {
    section "H. Pre-existing behavior is unchanged"

    local out rc
    out="$(E task remove "$LONE" 2>&1)"; rc=$?
    assert_eq "removing a task with no relations still works" "0" "$rc"
    assert_contains "...with the usual confirmation" "Removed" "$out"

    # The children guard still fires, and still names --cascade.
    W "INSERT INTO tasks (id, project_id, title, status, phase) VALUES
         (8200, $PID, 'Childful parent', 'ready', 'now')"
    W "INSERT INTO tasks (id, project_id, title, status, phase, parent_id) VALUES
         (8201, $PID, 'Its child', 'ready', 'now', 8200)"
    out="$(E task remove 8200 2>&1)"; rc=$?
    assert_eq "removing a task with children is still refused" "1" "$rc"
    assert_contains "...still pointing at --cascade" "--cascade" "$out"
    assert_not_contains "...and not confusing children with relations" "unlink" "$out"
}

# ─── I: unit + regression suites ─────────────────────────────────────────────

test_suites() {
    section "I. Python suites"
    local out rc

    out=$(cd "$WT" && uv run pytest tests/test_task_remove_relations.py -q 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then report_pass "pytest test_task_remove_relations passes"
    else report_fail "pytest test_task_remove_relations" "exit 0" "exit=$rc"$'\n'"$(printf '%s' "$out" | tail -25)"; fi

    out=$(cd "$WT" && uv run pytest tests/test_relations.py tests/test_reconcile.py tests/test_decision_cmd.py -q 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then report_pass "pytest relations + reconcile + decision suites pass (no regression)"
    else report_fail "pytest relations + reconcile + decision" "exit 0" "exit=$rc"$'\n'"$(printf '%s' "$out" | tail -25)"; fi
}

# ─── main ─────────────────────────────────────────────────────────────────────

main() {
    WT="$(git rev-parse --show-toplevel 2>/dev/null)"
    [[ -n "${WT}" ]] || { printf 'ERROR: not inside a git worktree\n' >&2; exit 2; }

    command -v uv >/dev/null      || { printf 'ERROR: uv not on PATH\n' >&2; exit 2; }
    command -v git >/dev/null     || { printf 'ERROR: git not on PATH\n' >&2; exit 2; }
    [[ -x "${WT}/bin/endless-go" ]] || {
        printf 'ERROR: %s/bin/endless-go missing — run `just build`\n' "${WT}" >&2; exit 2; }

    printf '%sE-1915 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  worktree: %s\n' "${WT}"

    if ! setup_fixture; then
        printf 'ERROR: fixture setup failed (isolated DB seeding)\n' >&2
        [[ -n "$TMP" ]] && rm -rf "$TMP"
        exit 2
    fi

    test_refusal_names_the_fix
    test_every_type_refused
    test_target_side_refused
    test_decision_relations_refused
    test_cascade_checks_descendants
    test_bulk_clear_guarded
    test_unlink_then_remove_is_clean
    test_reconcile_repair
    test_regressions
    test_suites

    [[ -n "$TMP" ]] && rm -rf "$TMP"
    summary
}

main "$@"

#!/usr/bin/env bash
#
# E-1185 verification script — the `duplicates` / `duplicated_by` relation type.
#
# The gap: the relation vocabulary had no way to say "these two tasks were filed
# for the same concern." That fact was being forced into `replaces` (which means
# something else — B was real work and A took over from it) or `relates_to`
# (true of almost everything, so it records nothing). E-1185 adds `duplicates`
# as a ninth stored type, with `duplicated_by` as its inverse view.
#
# What is verified here:
#   A. Both spellings resolve to ONE stored row, active-voice (source is the
#      redundant filing) — the inverse view is a way to write it, not a
#      second row.
#   B. It is genuinely its own type: it coexists with `replaces` and
#      `relates_to` between the same pair, and reads back as `duplicates`.
#   C. It renders with its own directional headings in `task show` /
#      `task relations`, and in the --llm line.
#   D. It is reachable: `task link --help` names both directions, and the
#      invalid-type error lists them.
#   E. The boundaries hold — task→decision keeps its narrower vocabulary,
#      self-links are still refused, unlink round-trips (explicit and
#      auto-detected), and the `task remove` orphan guard covers the new type
#      with no exemption.
#   F. The guide documents when to reach for it over its two near-neighbours.
#
# Second landing — being a relation type means being usable everywhere relation
# types are used, so the first landing stopping where `replaces` stopped was the
# wrong yardstick:
#   H. `--duplicates` / `--replaces` on `task add`, `task update` and `epic add`
#      (`task update` had no relation flags at all), recording the relation and
#      NOT the status.
#   I. The inline `(duplicates E-NNN)` note beside a terminal status, on every
#      surface E-1956 gave `(replaced by E-NNN)` — including `session status`,
#      which is Go.
#
# Run from anywhere inside the worktree:
#   esu && ./tests/tasks/e-1185-verify.sh
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

# Task ids are fixed so every assertion can name them literally. The names
# follow the relation's own vocabulary: DUPE is the redundant filing, KEEPER is
# the task that survives.
DUPE=8001
KEEPER=8002
THIRD=8003      # unrelated far endpoint, for the coexistence checks
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

    # Pin any reconcile scan to the temp dir so it never walks the developer's
    # real project roots.
    printf '{"roots": ["%s"]}\n' "$TMP" > "$DBDIR/config.json"

    E project register "$REPO" --name probe --label Probe --desc d --lang Go --status active >/dev/null 2>&1
    PID="$(Q "SELECT id FROM projects WHERE name='probe'")"
    [[ -n "$PID" ]] || return 1

    W "INSERT INTO tasks (id, project_id, title, status, phase) VALUES
         ($DUPE,   $PID, 'Add extensions worktree-init hook', 'ready', 'now'),
         ($KEEPER, $PID, 'Add post-worktree-create hook',     'ready', 'now'),
         ($THIRD,  $PID, 'Unrelated third task',              'ready', 'now')"

    [[ "$(Q "SELECT count(*) FROM tasks")" == "3" ]] || return 1
    [[ "$(Q "SELECT count(*) FROM task_deps")" == "0" ]] || return 1
    return 0
}

# dup_rows: the stored duplicates rows, as "source|target" lines.
dup_rows() {
    Q "SELECT source_id || '|' || target_id FROM task_deps
        WHERE dep_type='duplicates' ORDER BY source_id"
}

clear_deps() { W "DELETE FROM task_deps"; }

# ─── A: one stored row, active voice, either spelling ────────────────────────

test_stores_active_voice() {
    section "A. Both spellings resolve to ONE active-voice row"

    local out rc
    out="$(E task link "$DUPE" --to "E-$KEEPER" --type duplicates 2>&1)"; rc=$?
    assert_eq "'--type duplicates' is accepted" "0" "$rc"
    assert_eq "...stored source=the redundant filing, target=the keeper" \
        "$DUPE|$KEEPER" "$(dup_rows)"
    assert_eq "...as dep_type 'duplicates', not 'replaces' or 'relates_to'" \
        "1" "$(Q "SELECT count(*) FROM task_deps WHERE dep_type='duplicates'")"

    # The inverse view is a way to WRITE the same row, not a second row: writing
    # it from the keeper's side now collides with what already exists.
    out="$(E task link "$KEEPER" --to "E-$DUPE" --type duplicated_by 2>&1)"; rc=$?
    assert_eq "the inverse spelling of the same fact is refused as a duplicate link" "1" "$rc"
    assert_contains "...naming it in the direction the user typed" \
        "already linked" "$out"
    assert_eq "...and no second row was written" "$DUPE|$KEEPER" "$(dup_rows)"

    clear_deps

    # ...and on its own, `duplicated_by` swaps to the same storage.
    E task link "$KEEPER" --to "E-$DUPE" --type duplicated_by >/dev/null 2>&1
    assert_eq "'--type duplicated_by' alone swaps to the identical stored row" \
        "$DUPE|$KEEPER" "$(dup_rows)"

    clear_deps
}

# ─── B: genuinely a distinct type ────────────────────────────────────────────

test_distinct_from_neighbours() {
    section "B. A ninth type — coexists with replaces and relates_to, not folded into either"

    E task link "$DUPE" --to "E-$KEEPER" --type duplicates >/dev/null 2>&1
    E task link "$DUPE" --to "E-$KEEPER" --type replaces   >/dev/null 2>&1
    E task link "$DUPE" --to "E-$KEEPER" --type relates_to >/dev/null 2>&1

    assert_eq "all three coexist between the same ordered pair" \
        "3" "$(Q "SELECT count(*) FROM task_deps WHERE source_id=$DUPE AND target_id=$KEEPER")"
    assert_eq "...and the duplicates row is stored under its own dep_type" \
        "duplicates" "$(Q "SELECT dep_type FROM task_deps
                            WHERE source_id=$DUPE AND target_id=$KEEPER
                              AND dep_type='duplicates'")"

    # Removing the duplicates row leaves its neighbours alone — the reverse of
    # the conflation this task exists to end.
    E task unlink "$DUPE" --to "$KEEPER" --type duplicates >/dev/null 2>&1
    assert_eq "unlinking 'duplicates' does not disturb replaces/relates_to" \
        "relates_to,replaces" \
        "$(Q "SELECT group_concat(dep_type) FROM (
                SELECT dep_type FROM task_deps WHERE source_id=$DUPE ORDER BY dep_type)")"

    clear_deps
}

# ─── C: how it reads back ────────────────────────────────────────────────────

test_rendering() {
    section "C. Directional headings on both sides"

    E task link "$DUPE" --to "E-$KEEPER" --type duplicates >/dev/null 2>&1

    local dupe_show keeper_show
    dupe_show="$(E task show "E-$DUPE" 2>&1)"
    keeper_show="$(E task show "E-$KEEPER" 2>&1)"

    assert_contains "the redundant filing reads 'Duplicates:'" "Duplicates:" "$dupe_show"
    assert_contains "...pointing at the keeper" "E-$KEEPER" "$dupe_show"
    assert_not_contains "...and not at the passive phrasing" "Duplicated by:" "$dupe_show"

    assert_contains "the keeper reads 'Duplicated by:'" "Duplicated by:" "$keeper_show"
    assert_contains "...pointing back at the redundant filing" "E-$DUPE" "$keeper_show"

    # The machine-readable line carries the same directional phrase.
    assert_contains "--llm names the relation from each side" \
        "E-$KEEPER (duplicates)" "$(E task relations "E-$DUPE" --llm 2>&1)"
    assert_contains "...and its inverse from the other" \
        "E-$DUPE (duplicated by)" "$(E task relations "E-$KEEPER" --llm 2>&1)"

    clear_deps
}

# ─── D: discoverability ──────────────────────────────────────────────────────

test_discoverable() {
    section "D. An agent can find the type without reading the source"

    local help_out err_out
    help_out="$(E task link --help 2>&1)"
    assert_contains "'task link --help' lists 'duplicates'" "duplicates" "$help_out"
    assert_contains "...and the inverse view 'duplicated_by'" "duplicated_by" "$help_out"

    err_out="$(E task link "$DUPE" --to "E-$KEEPER" --type dupe 2>&1)"
    assert_contains "a bad --type still lists the valid set" "Invalid relation type" "$err_out"
    assert_contains "...now including duplicates" "duplicates" "$err_out"
    assert_contains "...and duplicated_by" "duplicated_by" "$err_out"
    assert_eq "...and wrote nothing" "0" "$(Q "SELECT count(*) FROM task_deps")"
}

# ─── E: boundaries unchanged ─────────────────────────────────────────────────

test_boundaries() {
    section "E. The existing guards cover the new type — no exemption"

    local out rc

    # task→decision keeps its own narrower vocabulary; duplicates is task↔task.
    W "INSERT INTO decisions (id, project_id, title, status, created_at)
         VALUES ($DECISION, $PID, 'A decision', 'accepted', '2026-01-01T00:00:00')"
    out="$(E task link "$DUPE" --to "ED-$DECISION" --type duplicates 2>&1)"; rc=$?
    assert_eq "'duplicates' toward a DECISION is refused" "1" "$rc"
    assert_contains "...saying which pair it is illegal for" \
        "not legal for task→decision" "$out"

    # Self-link.
    out="$(E task link "$DUPE" --to "E-$DUPE" --type duplicates 2>&1)"; rc=$?
    assert_eq "a task cannot duplicate itself" "1" "$rc"
    assert_contains "...with the existing message" "cannot link to itself" "$out"

    # Unlink round-trips, both explicitly and by auto-detection.
    E task link "$DUPE" --to "E-$KEEPER" --type duplicates >/dev/null 2>&1
    E task unlink "$DUPE" --to "$KEEPER" --type duplicates >/dev/null 2>&1
    assert_eq "explicit unlink removes the row" "0" "$(Q "SELECT count(*) FROM task_deps")"

    E task link "$DUPE" --to "E-$KEEPER" --type duplicates >/dev/null 2>&1
    out="$(E task unlink "$DUPE" --to "$KEEPER" 2>&1)"; rc=$?
    assert_eq "unlink auto-detects it when it is the only relation" "0" "$rc"
    assert_eq "...and the row is gone" "0" "$(Q "SELECT count(*) FROM task_deps")"

    # E-1915's orphan guard applies to every stored type, this one included.
    E task link "$DUPE" --to "E-$KEEPER" --type duplicates >/dev/null 2>&1
    out="$(E task remove "$KEEPER" 2>&1)"; rc=$?
    assert_eq "removing a task held by a 'duplicates' row is refused" "1" "$rc"
    assert_contains "...naming the exact unlink command" \
        "endless task unlink E-$DUPE --to E-$KEEPER --type duplicates" "$out"
    assert_eq "...and the task survives" \
        "1" "$(Q "SELECT count(*) FROM tasks WHERE id=$KEEPER")"

    clear_deps

    # A relation on an unrelated pair is untouched by any of the above.
    E task link "$THIRD" --to "E-$KEEPER" --type relates_to >/dev/null 2>&1
    assert_eq "an unrelated relation still round-trips normally" \
        "1" "$(Q "SELECT count(*) FROM task_deps WHERE dep_type='relates_to'")"
    clear_deps
}

# ─── F: the guide says when to reach for it ──────────────────────────────────

test_guide_documents_it() {
    section "F. The guide distinguishes it from its two near-neighbours"

    local guide
    guide="$(E guide tasks 2>&1)"

    assert_contains "the relation-type table has a duplicates row" \
        '`duplicates` / `duplicated_by`' "$guide"
    assert_contains "...the decision tree offers it" \
        "same task filed twice" "$guide"
    assert_contains "...and it is contrasted with replaces" \
        "vs \`replaces\` vs \`relates_to\`" "$guide"
    assert_contains "...including what it does NOT do to status" \
        "changes no status" "$guide"
}

# ─── H: the relation flags ───────────────────────────────────────────────────

test_relation_flags() {
    section "H. --duplicates / --replaces on add, update and epic add"

    local out rc new_id

    # `task add --duplicates`: the new task is the redundant filing.
    out="$(E task add "File the same concern twice" --duplicates "E-$KEEPER" 2>&1)"; rc=$?
    new_id="$(Q "SELECT id FROM tasks WHERE title='File the same concern twice'")"
    assert_eq "'task add --duplicates' is accepted" "0" "$rc"
    assert_eq "...linking the NEW task as the duplicate" \
        "$new_id|$KEEPER" "$(dup_rows)"
    clear_deps
    W "DELETE FROM tasks WHERE id=$new_id"

    # `task add --replaces`.
    out="$(E task add "Supersede the earlier attempt" --replaces "E-$KEEPER" 2>&1)"; rc=$?
    new_id="$(Q "SELECT id FROM tasks WHERE title='Supersede the earlier attempt'")"
    assert_eq "'task add --replaces' is accepted" "0" "$rc"
    assert_eq "...stored as a replaces row from the new task" \
        "$new_id|$KEEPER" \
        "$(Q "SELECT source_id || '|' || target_id FROM task_deps WHERE dep_type='replaces'")"
    clear_deps
    W "DELETE FROM tasks WHERE id=$new_id"

    # `task update` had NO relation flags before this, and its "Nothing to
    # update" refusal fires when no field is named — a relation flag has to
    # count as the edit.
    out="$(E task update "$DUPE" --duplicates "E-$KEEPER" 2>&1)"; rc=$?
    assert_eq "'task update --duplicates' alone is accepted" "0" "$rc"
    assert_not_contains "...not tripping the empty-edit refusal" "Nothing to update" "$out"
    assert_eq "...and records the relation" "$DUPE|$KEEPER" "$(dup_rows)"
    clear_deps

    # ...but a bare `task update` must still be refused.
    out="$(E task update "$DUPE" 2>&1)"; rc=$?
    assert_eq "a bare 'task update' is still refused" "1" "$rc"
    assert_contains "...with the unchanged message" "Nothing to update" "$out"

    # Applied to every id named.
    E task update "$DUPE" "$THIRD" --duplicates "E-$KEEPER" >/dev/null 2>&1
    assert_eq "the relation is recorded from EACH named task" \
        "$DUPE|$KEEPER
$THIRD|$KEEPER" "$(dup_rows)"
    clear_deps

    # The trap: --replaces records the relation, it does not close anything.
    # `task replace` is the status-bearing surface and the help text says so.
    E task update "$THIRD" --replaces "E-$KEEPER" >/dev/null 2>&1
    assert_eq "'--replaces' leaves the replaced task's status alone" \
        "ready" "$(Q "SELECT status FROM tasks WHERE id=$KEEPER")"
    assert_contains "...and the help says which command does close it" \
        "task replace" "$(E task update --help 2>&1)"
    clear_deps

    # epic add carries the same vocabulary — a different flag set there is drift.
    out="$(E epic add "Track the duplicated concern" --duplicates "E-$KEEPER" 2>&1)"; rc=$?
    assert_eq "'epic add --duplicates' is accepted too" "0" "$rc"
    new_id="$(Q "SELECT id FROM tasks WHERE title='Track the duplicated concern'")"
    assert_eq "...with the same stored shape" "$new_id|$KEEPER" "$(dup_rows)"
    clear_deps
    W "DELETE FROM tasks WHERE id=$new_id"
}

# ─── I: the inline note ──────────────────────────────────────────────────────

test_inline_note() {
    section "I. The note rides with a terminal status, as E-1956 does for replaces"

    W "UPDATE tasks SET status='obsolete' WHERE id=$DUPE"
    E task link "$DUPE" --to "E-$KEEPER" --type duplicates >/dev/null 2>&1

    local shown listed
    shown="$(E task show "E-$DUPE" 2>&1)"
    assert_contains "'task show' puts it on the Status line" \
        "obsolete (duplicates E-$KEEPER)" "$shown"

    # ...and never on the task that was KEPT — the note names where the work is.
    assert_not_contains "the keeper's status line carries nothing" \
        "duplicates E-" "$(E task show "E-$KEEPER" 2>&1 | grep '^Status:')"

    listed="$(E task list --all 2>&1)"
    assert_contains "'task list' carries it in the status column" \
        "obsolete (duplicates E-$KEEPER)" "$listed"

    assert_contains "--llm carries it as a parseable key" \
        "duplicates=E-$KEEPER" "$(E task show "E-$DUPE" --llm 2>&1)"
    assert_contains "...on the list line too" \
        "duplicates=E-$KEEPER" "$(E task list --all --llm 2>&1)"

    # --json is DATA: emitted even where the display gate would suppress it.
    W "UPDATE tasks SET status='underway' WHERE id=$DUPE"
    assert_not_contains "an OPEN status draws no note" \
        "duplicates E-" "$(E task show "E-$DUPE" 2>&1)"
    assert_contains "...but --json still carries the relation" \
        "\"E-$KEEPER\"" "$(E task show "E-$DUPE" --json 2>&1 | tr -d ' \n' | grep -o '"duplicates":\[[^]]*\]')"
    assert_contains "...and the key is present even when empty" \
        '"duplicates": []' "$(E task show "E-$KEEPER" --json 2>&1)"

    # The Go surface, driven headless. --task bypasses tmux/session resolution
    # and reads the same DB the Python CLI above just wrote to.
    W "UPDATE tasks SET status='obsolete' WHERE id=$DUPE"
    local table json
    table="$( cd "$REPO" && "$WT/bin/endless-go" session-status --task "$DUPE" --all --cols 200 2>&1 )"
    assert_contains "'session status' draws the note on the row" \
        "(duplicates E-$KEEPER)" "$table"

    json="$( cd "$REPO" && "$WT/bin/endless-go" session-status --task "$DUPE" --all --json 2>&1 )"
    assert_contains "...and --json carries it as data" "\"E-$KEEPER\"" \
        "$(printf '%s' "$json" | tr -d ' \n' | grep -o '"duplicates":\["E-[0-9]*"\]')"
    # Always present, never absent: a consumer must not have to read a missing
    # key as "not a duplicate". The keeper holds no duplicates row at all.
    json="$( cd "$REPO" && "$WT/bin/endless-go" session-status --task "$KEEPER" --all --json 2>&1 )"
    assert_contains "...and the key is present even when empty" '"duplicates": []' "$json"

    clear_deps
    W "UPDATE tasks SET status='ready' WHERE id=$DUPE"
}

# ─── G: unit + regression suites ─────────────────────────────────────────────

test_suites() {
    section "G. Python suites"
    local out rc

    out=$(cd "$WT" && uv run pytest tests/test_relations.py tests/test_duplicates_inline.py -q 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then report_pass "pytest relations + duplicates-inline suites pass"
    else report_fail "pytest relations + duplicates-inline" "exit 0" "exit=$rc"$'\n'"$(printf '%s' "$out" | tail -25)"; fi

    # STORED_DEP_TYPES is parametrized over there, so a ninth type widens it.
    # test_replaced_by_inline is the no-regression check on E-1956, whose helpers
    # were generalized to carry both relations.
    out=$(cd "$WT" && uv run pytest tests/test_task_remove_relations.py tests/test_decision_cmd.py tests/test_replaced_by_inline.py -q 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then report_pass "pytest remove-relations + decision + replaced-by suites pass (no regression)"
    else report_fail "pytest remove-relations + decision + replaced-by" "exit 0" "exit=$rc"$'\n'"$(printf '%s' "$out" | tail -25)"; fi

    out=$(cd "$WT" && go test ./internal/monitor/ ./internal/sessionstatuscmd/ 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then report_pass "go test monitor + sessionstatuscmd pass (the session status column)"
    else report_fail "go test monitor + sessionstatuscmd" "exit 0" "exit=$rc"$'\n'"$(printf '%s' "$out" | tail -25)"; fi
}

# ─── main ─────────────────────────────────────────────────────────────────────

main() {
    WT="$(git rev-parse --show-toplevel 2>/dev/null)"
    [[ -n "${WT}" ]] || { printf 'ERROR: not inside a git worktree\n' >&2; exit 2; }

    command -v uv >/dev/null      || { printf 'ERROR: uv not on PATH\n' >&2; exit 2; }
    command -v git >/dev/null     || { printf 'ERROR: git not on PATH\n' >&2; exit 2; }
    [[ -x "${WT}/bin/endless-go" ]] || {
        printf 'ERROR: %s/bin/endless-go missing — run `just build`\n' "${WT}" >&2; exit 2; }

    printf '%sE-1185 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  worktree: %s\n' "${WT}"

    if ! setup_fixture; then
        printf 'ERROR: fixture setup failed (isolated DB seeding)\n' >&2
        [[ -n "$TMP" ]] && rm -rf "$TMP"
        exit 2
    fi

    test_stores_active_voice
    test_distinct_from_neighbours
    test_rendering
    test_discoverable
    test_boundaries
    test_guide_documents_it
    test_relation_flags
    test_inline_note
    test_suites

    [[ -n "$TMP" ]] && rm -rf "$TMP"
    summary
}

main "$@"

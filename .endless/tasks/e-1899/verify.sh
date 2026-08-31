#!/usr/bin/env bash
#
# E-1899 verification script — removal of the unused `task list --tree` /
# `epic list --tree` indented-tree renderer.
#
# `epic_cmd.list_epics` is a thin wrapper that forwarded `tree=tree` into
# `task_cmd.show_plan`, so the two flags shared ONE branch and ONE glyph map.
# Both flags, the `tree` parameter on both functions, the `else: # Tree output`
# branch, and the glyph map that opened it are gone.
#
# The only real risk in a removal like this is deleting the WRONG `--tree`:
# there were four flags by that name and two identically-named
# `status_indicators` maps. Section D is the check that catches it —
# `session status --tree` and `session monitor --tree` are Go-backed, are the
# trees actually in use, and share nothing with the list tree but the flag
# name.
#
# The second `status_indicators` map (in `next_tasks`) went too: it turned out
# to be dead — assigned, never read, because `next_tasks` renders through
# `_render_flat_table`, which has no glyph column. Section E asserts both maps
# are gone AND that `task next` renders exactly what it rendered before.
#
# Run from anywhere inside the worktree:
#   endless task verify E-1899
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on a setup problem.
#
# Fail-fast: section A runs the whole Python suite FIRST and aborts the run if
# it fails — a removal must not break an unrelated suite, and there is no point
# exercising end-to-end behavior on top of one that is already red.
#
# Isolation: a throwaway git repo as project root under a temp dir, a temp
# XDG_CONFIG_HOME (its own DB) and XDG_CACHE_HOME. No real DB/ledger/cache is
# touched. Shape borrowed from .endless/tasks/e-1845/verify.sh.

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
# E_RC ...: same, but echo the exit code on the last line of stdout.
E_RC() { local out rc; out="$(E "$@" 2>&1)"; rc=$?; printf '%s\nrc=%d' "$out" "$rc"; }
# LAST_ID: id of the most recently inserted task.
LAST_ID() { E sql "SELECT id FROM tasks ORDER BY id DESC LIMIT 1" --tsv 2>/dev/null; }
# add_task TITLE [extra args...]: add a task; echoes its id. Titles must lead
# with a registered verb (`Add ...`) or the verb gate refuses the insert.
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

    [[ "$(E sql 'SELECT count(*) FROM projects' --tsv 2>/dev/null)" == "1" ]] || return 1

    # A small tree with roots, a child and a grandchild, plus an epic. A
    # regression in the flat renderer (which never indented, but does read
    # parent_id off every row) would show against this shape.
    ROOT_A="$(add_task "Add a parent thing" --status ready)"
    ROOT_B="$(add_task "Add a second root" --status ready --phase later)"
    CHILD="$(add_task "Add a child thing" --parent "E-$ROOT_A" --status ready)"
    GRAND="$(add_task "Add a grandchild thing" --parent "E-$CHILD" --status unplanned)"
    EPIC="$(add_task "Build an epic thing" --type epic --status ready)"
    [[ -n "$ROOT_A" && -n "$ROOT_B" && -n "$CHILD" && -n "$GRAND" && -n "$EPIC" ]] || return 1
    [[ "$(E sql 'SELECT count(*) FROM tasks' --tsv 2>/dev/null)" == "5" ]] || return 1
    return 0
}

# ─── section A: full Python suite (FAIL-FAST GATE) ───────────────────────────

test_units() {
    section "A. Python suite (fail-fast gate)"
    local out rc
    out=$(cd "$WT" && uv run pytest tests/ -q 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then
        report_pass "uv run pytest tests/ -q passes"
    else
        report_fail "uv run pytest tests/ -q" "exit 0" \
            "exit=$rc"$'\n'"$(printf '%s' "$out" | tail -25)"
    fi

    if [[ "${FAIL_COUNT}" -gt 0 ]]; then
        printf '\n  %sABORTING%s — the suite is red; skipping end-to-end checks.\n' \
            "${RED}${BOLD}" "${RESET}"
        return 1
    fi
    return 0
}

# ─── section B: the flags are gone ───────────────────────────────────────────

test_flags_gone() {
    section "B. 'task list --tree' and 'epic list --tree' no longer exist"
    local out

    out="$(E_RC task list --tree)"
    assert_contains "'task list --tree' is refused"      "No such option: --tree" "$out"
    assert_contains "'task list --tree' exits nonzero"   "rc=2"                   "$out"

    out="$(E_RC epic list --tree)"
    assert_contains "'epic list --tree' is refused"      "No such option: --tree" "$out"
    assert_contains "'epic list --tree' exits nonzero"   "rc=2"                   "$out"

    out="$(E task list --help 2>&1)"
    assert_not_contains "'task list --help' no longer advertises --tree" "--tree" "$out"
    assert_contains     "'task list --help' still advertises --tier"    "--tier"  "$out"

    out="$(E epic list --help 2>&1)"
    assert_not_contains "'epic list --help' no longer advertises --tree" "--tree" "$out"
    assert_contains     "'epic list --help' still advertises --sort"    "--sort"  "$out"
}

# ─── section C: the flat table is untouched ──────────────────────────────────

test_flat_table_intact() {
    section "C. The flat table and every surviving list flag still behave"
    local out

    out="$(E_RC task list)"
    assert_contains "'task list' still renders the header"  "Tasks for probe"        "$out"
    assert_contains "'task list' renders the column head"   "ID   Phase  Status"     "$out"
    assert_contains "'task list' lists the root"            "Add a parent thing"     "$out"
    assert_contains "'task list' lists the child"           "Add a child thing"      "$out"
    assert_contains "'task list' lists the grandchild"      "Add a grandchild thing" "$out"
    assert_contains "'task list' prints the item count"     "5 item(s)"              "$out"
    assert_contains "'task list' exits clean"               "rc=0"                   "$out"
    # The flat renderer never indented; a leaked tree branch would.
    assert_not_contains "'task list' does not indent children" \
        "  E-$CHILD" "$out"

    # --status
    out="$(E task list --status unplanned 2>&1)"
    assert_contains     "--status keeps the matching row"  "Add a grandchild thing" "$out"
    assert_not_contains "--status drops the others"        "Add a parent thing"     "$out"

    # --phase
    out="$(E task list --phase later 2>&1)"
    assert_contains     "--phase keeps the matching row"   "Add a second root"      "$out"
    assert_not_contains "--phase drops the others"         "Add a parent thing"     "$out"

    # --parent
    out="$(E task list --parent "E-$ROOT_A" 2>&1)"
    assert_contains     "--parent keeps the child"         "Add a child thing"      "$out"
    assert_not_contains "--parent drops the parent itself" "Add a parent thing"     "$out"

    # --sort: default is id; --sort title reorders. Compare the ID column order.
    local ids_by_id ids_by_title
    ids_by_id="$(E task list 2>&1 | grep -o '^E-[0-9]*' | tr '\n' ' ')"
    ids_by_title="$(E task list --sort title 2>&1 | grep -o '^E-[0-9]*' | tr '\n' ' ')"
    assert_eq "default sort is by id" \
        "E-$ROOT_A E-$ROOT_B E-$CHILD E-$GRAND E-$EPIC " "$ids_by_id"
    assert_eq "--sort title reorders the rows" \
        "E-$CHILD E-$GRAND E-$ROOT_A E-$ROOT_B E-$EPIC " "$ids_by_title"

    # --llm
    out="$(E task list --llm 2>&1)"
    assert_contains     "--llm emits the project header"   "# probe"                "$out"
    assert_contains     "--llm emits a bare row"           "E-$ROOT_A now ready Add a parent thing" "$out"
    assert_not_contains "--llm skips the table chrome"     "ID   Phase"             "$out"

    # --json
    out="$(E task list --json 2>&1)"
    assert_contains "--json emits JSON"                    '"id": "E-'"$ROOT_A"'"'  "$out"
    assert_contains "--json carries the status field"      '"status": "ready"'      "$out"
    if printf '%s' "$out" | uv run --project "$WT" python -c 'import json,sys; json.load(sys.stdin)' 2>/dev/null; then
        report_pass "--json output parses as JSON"
    else
        report_fail "--json output parses as JSON" "valid JSON" "$out"
    fi

    # epic list — the same renderer reached through the wrapper.
    out="$(E_RC epic list)"
    assert_contains     "'epic list' still renders"        "Build an epic thing"    "$out"
    assert_not_contains "'epic list' filters to epics"     "Add a parent thing"     "$out"
    assert_contains     "'epic list' exits clean"          "rc=0"                   "$out"

    out="$(E epic list --json 2>&1)"
    assert_contains "'epic list --json' still emits JSON"  '"id": "E-'"$EPIC"'"'    "$out"
}

# ─── section D: the trees that matter still work ─────────────────────────────

test_real_trees_survive() {
    section "D. The Go-backed session trees — the ones actually in use — survive"
    local out

    # The deterministic one: the Go renderer against the isolated DB, focal task
    # named explicitly so no tmux/session resolution is involved. If the wrong
    # --tree had been deleted, this renders nothing.
    out="$( cd "$REPO" && XDG_CONFIG_HOME="$XDG_CONFIG_HOME" \
        "$WT/bin/endless-go" session-status --tree --task "$ROOT_A" --cols 200 2>&1 )"
    assert_contains "session-status --tree marks the focal task" "●E-$ROOT_A" "$out"
    assert_contains "session-status --tree nests the child"      "── E-$CHILD" "$out"
    assert_not_contains "session-status --tree stays IDs-only"   "Add a parent thing" "$out"

    # The Python surfaces that reach that renderer. These resolve their own
    # focal task from the live session, so assert on tree SHAPE, not on ids.
    out="$(E session status --help 2>&1)"
    assert_contains "'session status --help' still advertises --tree" "--tree" "$out"
    out="$(E session monitor --help 2>&1)"
    assert_contains "'session monitor --help' still advertises --tree" "--tree" "$out"

    out="$(E_RC session status --tree)"
    assert_contains "'session status --tree' exits clean" "rc=0" "$out"
    assert_contains "'session status --tree' renders a focal tree row" "●E-" "$out"

    # --tree renders a single frame; the redraw loop is the table view only.
    out="$(E_RC session monitor --tree)"
    assert_contains "'session monitor --tree' exits clean" "rc=0" "$out"
    assert_contains "'session monitor --tree' renders a focal tree row" "●E-" "$out"
}

# ─── section E: the surviving glyph map is intact ────────────────────────────

test_glyph_maps_gone() {
    section "E. Both status_indicators maps are gone, and 'task next' is unchanged"
    # The plan protected the second map as an unrelated bystander. It turned out
    # to be dead: assigned in next_tasks and never read — that function renders
    # through _render_flat_table, which has no glyph column. So both maps go,
    # and the guarantee that matters shifts from "the survivor is intact" to
    # "task next renders exactly what it rendered before".
    local src maps out
    src="$(cat "$WT/src/endless/task_cmd.py")"

    maps="$(grep -c 'status_indicators' "$WT/src/endless/task_cmd.py")"
    assert_eq "no status_indicators map remains at all" "0" "$maps"
    assert_not_contains "show_plan's styled map is gone" \
        'click.style("◌", fg="yellow")' "$src"
    assert_not_contains "next_tasks' plain map is gone" \
        '"unplanned": "○",' "$src"
    # Deleting an assignment is only safe because nothing read it.
    assert_eq "nothing in task_cmd reads an indicator" "" \
        "$(grep -n 'indicator' "$WT/src/endless/task_cmd.py")"

    # 'task next' renders through the flat table, exactly as before.
    out="$(E_RC task next)"
    assert_contains "'task next' still renders"        "Next up (probe):"       "$out"
    assert_contains "'task next' renders the column head" "ID   Phase  Status"  "$out"
    assert_contains "'task next' lists actionable work" "Add a grandchild thing" "$out"
    assert_contains "'task next' exits clean"          "rc=0"                   "$out"
    # It never printed the glyphs the dead map held — that was the tell.
    assert_not_contains "'task next' prints no status glyph" "○" "$out"

    out="$(E task next --json 2>&1)"
    assert_contains "'task next --json' still emits JSON" '"id": "E-'          "$out"
    out="$(E_RC task next --llm)"
    assert_contains "'task next --llm' still emits bare rows" "# probe"        "$out"
    assert_contains "'task next --llm' exits clean"        "rc=0"              "$out"
}

# ─── section F: no dangling references ───────────────────────────────────────

test_no_dangling_refs() {
    section "F. Nothing still passes a tree through the list path"
    local hits

    # `as_tree` was the Click destination for both deleted flags — it should be
    # gone from the source tree entirely.
    hits="$(grep -rn 'as_tree' "$WT/src/endless" 2>/dev/null)"
    assert_eq "no 'as_tree' left in src/endless" "" "$hits"

    # `tree=` survives at exactly the two session_status_resolve call sites.
    # \btree= does not match `worktree=`.
    hits="$(grep -rnE '\btree=' "$WT/src/endless" 2>/dev/null)"
    local count
    count="$(printf '%s' "$hits" | grep -c 'session_status_resolve' 2>/dev/null)"
    assert_eq "exactly two 'tree=' survivors" "2" "$(printf '%s\n' "$hits" | grep -c . )"
    assert_eq "both survivors are session_status_resolve calls" "2" "$count"

    # Neither list function still declares the parameter.
    assert_not_contains "show_plan no longer takes 'tree'" \
        "tree: bool" "$(sed -n '/^def show_plan(/,/^):/p' "$WT/src/endless/task_cmd.py")"
    assert_not_contains "list_epics no longer takes 'tree'" \
        "tree: bool" "$(sed -n '/^def list_epics(/,/^):/p' "$WT/src/endless/epic_cmd.py")"

    # The branch itself and its recursive renderer are gone.
    assert_not_contains "the '# Tree output' branch is gone" \
        "# Tree output" "$(cat "$WT/src/endless/task_cmd.py")"
    assert_not_contains "the _render recursion is gone" \
        "_render(None, 1)" "$(cat "$WT/src/endless/task_cmd.py")"

    # The docs no longer advertise the removed flag.
    assert_not_contains "the task guide no longer documents 'task list --tree'" \
        "task list --tree" "$(cat "$WT/docs/guide/tasks.md")"
}

# ─── section G: project-wide regression ──────────────────────────────────────

test_regression() {
    section "G. Regression — the Go suite still passes"
    local out rc
    out=$(cd "$WT" && go test ./... 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then report_pass "go test ./... passes"
    else report_fail "go test ./..." "exit 0" \
        "exit=$rc"$'\n'"$(printf '%s' "$out" | grep -v '^ok\|no test files' | head -25)"; fi
}

# ─── main ─────────────────────────────────────────────────────────────────────

main() {
    WT="$(git rev-parse --show-toplevel 2>/dev/null)"
    [[ -n "${WT}" ]] || { printf 'ERROR: not inside a git worktree\n' >&2; exit 2; }

    command -v go >/dev/null || { printf 'ERROR: go not on PATH\n' >&2; exit 2; }
    command -v uv >/dev/null || { printf 'ERROR: uv not on PATH\n' >&2; exit 2; }
    [[ -x "${WT}/bin/endless-go" ]] || { printf 'ERROR: %s missing — run `just build`\n' "${WT}/bin/endless-go" >&2; exit 2; }

    printf '%sE-1899 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  worktree: %s\n' "${WT}"

    if ! test_units; then
        summary
        exit 1
    fi

    if ! setup_fixture; then
        printf 'ERROR: fixture setup failed (isolated DB seeding)\n' >&2
        [[ -n "$TMP" ]] && rm -rf "$TMP"
        exit 2
    fi

    test_flags_gone
    test_flat_table_intact
    test_real_trees_survive
    test_glyph_maps_gone
    test_no_dangling_refs
    test_regression

    [[ -n "$TMP" ]] && rm -rf "$TMP"
    summary
}

main "$@"

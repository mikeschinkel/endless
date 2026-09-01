#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1659 and records what was true when E-1659
# landed. Edit it only if you ARE E-1659. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1659 verification script — confirms the task-TYPE slug rename:
#   task -> todo    (label Task -> Todo)
#   bug  -> bugfix  (label Bug  -> Bugfix)
#
# The type is stored as a stable integer type_id (TaskTypeTask=1, TaskTypeBug=2);
# only the slug/label moved. Historical task.created / task.fields_updated events
# carry the OLD slug string and must still replay to the same ids — so
# tasktype.Parse() KEEPS 'task'/'bug' as legacy aliases while String()/Label()
# emit only the new names. That one-function alias is the whole "upcasting" the
# rename needs; no DB row changes type_id and no pipeline is required.
#
# Verification checks:
#   1. the new slugs/labels are present at every canonical definition site;
#   2. the OLD slug/label FORMS are gone from those sites (surgically, because
#      the words "task"/"bug" are ubiquitous elsewhere and must NOT be touched);
#   3. the legacy aliases are DELIBERATELY retained in Parse() so history replays;
#   4. behavior is correct — the renamed handoff templates render, the old
#      template names are gone, and the tasktype package + both full suites are
#      green.
#
# Run from anywhere inside the worktree (esu cd's here and exports the session):
#   endless task verify E-1659
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on environment/setup error.

# Refuse a direct run, and pick up the shared harness vocabulary. Sourced as the
# FIRST executable statement so the refusal fires before anything in this file
# runs; every definition below overrides the harness's own, so a suite written
# before the harness existed behaves exactly as it did.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

# ─── globals ────────────────────────────────────────────────────────────────

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'
    RED=$'\033[31m'
    DIM=$'\033[2m'
    BOLD=$'\033[1m'
    RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi

UNDERLINE="──────────────────────────────────────────────────────────────"

# ─── output ─────────────────────────────────────────────────────────────────

section() {
    printf '\n%s%s%s\n' "${BOLD}" "$1" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
}

report_pass() {
    printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"
    PASS_COUNT=$((PASS_COUNT + 1))
}

report_fail() {
    local desc="$1"
    local detail="$2"
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "${desc}"
    printf '      %sdetail:%s %s\n' "${DIM}" "${RESET}" "${detail}"
    FAIL_COUNT=$((FAIL_COUNT + 1))
    FAILED_TESTS+=("${desc}")
}

summary() {
    printf '\n%sSummary%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    if [[ "${FAIL_COUNT}" -eq 0 ]]; then
        printf '  %s%d passed%s\n' "${GREEN}" "${PASS_COUNT}" "${RESET}"
        printf '\n  %sALL PASSED%s\n\n' "${GREEN}${BOLD}" "${RESET}"
        return 0
    fi
    printf '  %s%d passed%s, %s%d failed%s\n' \
        "${GREEN}" "${PASS_COUNT}" "${RESET}" \
        "${RED}" "${FAIL_COUNT}" "${RESET}"
    printf '\n  %sFAILED:%s\n' "${RED}${BOLD}" "${RESET}"
    local t
    for t in "${FAILED_TESTS[@]}"; do
        printf '    - %s\n' "${t}"
    done
    printf '\n'
    return 1
}

# ─── assertions ─────────────────────────────────────────────────────────────

# assert_cmd DESC CMD [ARGS...] — pass if CMD exits 0.
assert_cmd() {
    local desc="$1"
    shift
    local output
    output=$("$@" 2>&1)
    local rc=$?
    if [[ "${rc}" -eq 0 ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "exit=${rc} | $(printf '%s' "${output}" | tail -3 | tr '\n' '⏎')"
}

# assert_present DESC FILE PATTERN — pass iff PATTERN (fixed string) appears in FILE.
assert_present() {
    local desc="$1" file="$2" pattern="$3"
    if grep -qF -- "${pattern}" "${file}" 2>/dev/null; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "expected '${pattern}' in ${file}"
}

# assert_absent_in DESC FILE PATTERN — pass iff PATTERN (fixed string) does NOT
# appear in FILE. File-scoped on purpose: 'task'/'bug' are ubiquitous words, so a
# repo-wide grep would drown in legitimate non-type uses (entity_type='task',
# worktree kinds, command names, prose). We assert only that the OLD type-slug
# FORMS are gone from the specific definition sites they lived at.
assert_absent_in() {
    local desc="$1" file="$2" pattern="$3"
    if grep -qF -- "${pattern}" "${file}" 2>/dev/null; then
        local hit
        hit=$(grep -nF -- "${pattern}" "${file}" | head -2 | tr '\n' '⏎')
        report_fail "${desc}" "stray '${pattern}' in ${file}: ${hit}"
        return
    fi
    report_pass "${desc}"
}

# assert_file DESC PATH — pass iff PATH exists.
assert_file() {
    local desc="$1" path="$2"
    if [[ -e "${path}" ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "expected file ${path}"
}

# assert_no_file DESC PATH — pass iff PATH does NOT exist.
assert_no_file() {
    local desc="$1" path="$2"
    if [[ -e "${path}" ]]; then
        report_fail "${desc}" "unexpected file ${path}"
        return
    fi
    report_pass "${desc}"
}

HANDOFF_VARS='{"spawned_id":1659,"label_prefix":"E-1659","title":"Rename task types","worktree_path":"/w","branch":"br","child_count":0,"children_state":"none","bg":false}'

# assert_render DESC TEMPLATE MODE NEEDLE — render via the worktree binary and
# assert NEEDLE present (MODE=has) or absent (MODE=lacks).
assert_render() {
    local desc="$1" tmpl="$2" mode="$3" needle="$4"
    local out rc
    out=$(printf '%s' "${HANDOFF_VARS}" | ./bin/endless-go template render "${tmpl}" 2>&1)
    rc=$?
    if [[ "${rc}" -ne 0 ]]; then
        report_fail "${desc}" "render exit=${rc} | $(printf '%s' "${out}" | tail -2 | tr '\n' '⏎')"
        return
    fi
    if [[ "${mode}" == "has" ]]; then
        if printf '%s' "${out}" | grep -qF -- "${needle}"; then
            report_pass "${desc}"
        else
            report_fail "${desc}" "expected '${needle}' in rendered ${tmpl}"
        fi
    else
        if printf '%s' "${out}" | grep -qF -- "${needle}"; then
            report_fail "${desc}" "unexpected '${needle}' in rendered ${tmpl}"
        else
            report_pass "${desc}"
        fi
    fi
}

# assert_render_fails DESC TEMPLATE — pass iff rendering TEMPLATE errors (the old
# per-type template name no longer exists).
assert_render_fails() {
    local desc="$1" tmpl="$2"
    local out rc
    out=$(printf '%s' "${HANDOFF_VARS}" | ./bin/endless-go template render "${tmpl}" 2>&1)
    rc=$?
    if [[ "${rc}" -ne 0 ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "render of ${tmpl} unexpectedly succeeded"
}

# ─── checks ─────────────────────────────────────────────────────────────────

test_build() {
    section "Build — packages compile and the binary re-embeds renamed templates"
    assert_cmd "internal/... compiles" go build ./internal/...
    assert_cmd "endless-go builds (re-embeds handoff templates)" \
        go build -o bin/endless-go ./cmd/endless-go
}

test_new_present() {
    section "Rename — new slugs/labels at the canonical definition sites"
    assert_present "tasktype String() emits 'todo'" \
        internal/tasktype/tasktype.go 'return "todo"'
    assert_present "tasktype String() emits 'bugfix'" \
        internal/tasktype/tasktype.go 'return "bugfix"'
    assert_present "tasktype Label() emits 'Todo'" \
        internal/tasktype/tasktype.go 'return "Todo"'
    assert_present "tasktype Label() emits 'Bugfix'" \
        internal/tasktype/tasktype.go 'return "Bugfix"'
    assert_present "schema seed row is 'todo','Todo'" \
        internal/schema/schema.sql "(1, 'todo',       'Todo'),"
    assert_present "schema seed row is 'bugfix','Bugfix'" \
        internal/schema/schema.sql "(2, 'bugfix',     'Bugfix'),"
    assert_present "CLI --type choice lists 'todo'/'bugfix'" \
        src/endless/cli.py '["todo", "bugfix", "research", "epic", "brainstorm"]'
    assert_present "default task type is 'todo'" \
        src/endless/task_cmd.py 'task_type = task_type or "todo"'
    assert_present "task update valid_types is new names" \
        src/endless/task_cmd.py '("todo", "bugfix", "research", "epic", "brainstorm")'
}

test_self_heal() {
    section "Migration — the seed self-heals a rename (Option 1), no change-file"
    # The mirror seeds must UPSERT so re-applying schema.SQL reconciles a
    # populated DB's stale rows to the enum (INSERT OR IGNORE could not).
    assert_present "task_types seed upserts (ON CONFLICT DO UPDATE)" \
        internal/schema/schema.sql \
        "ON CONFLICT(id) DO UPDATE SET slug = excluded.slug, label = excluded.label"
    assert_absent_in "task_types seed is no longer INSERT OR IGNORE" \
        internal/schema/schema.sql "INSERT OR IGNORE INTO task_types"
    assert_absent_in "process_kinds seed is no longer INSERT OR IGNORE" \
        internal/schema/schema.sql "INSERT OR IGNORE INTO process_kinds"
    # No per-rename change-file: Option 1 removed the need for one.
    assert_no_file "no E-1659 migration change-file" \
        internal/schema/changes/e-1659-rename-task-todo-bug-bugfix.sql
    # Hermetic proof: re-applying schema.SQL reconciles stale task_types /
    # process_kinds rows to the current enum values, in place, no duplicates.
    # (This read session_kinds until E-2074 dropped that table with background
    # agents; process_kinds is the same ED-1506 mirror pattern.)
    assert_cmd "schema self-heal test passes (reconcile renamed rows)" \
        go test -count=1 ./internal/schema/...
}

test_old_gone() {
    section "Rename — old slug/label FORMS removed from the definition sites"
    # String()/Label() must not emit the old presentation forms.
    assert_absent_in "String() no longer emits 'task'" \
        internal/tasktype/tasktype.go 'return "task"'
    assert_absent_in "String() no longer emits 'bug'" \
        internal/tasktype/tasktype.go 'return "bug"'
    assert_absent_in "Label() no longer emits 'Task'" \
        internal/tasktype/tasktype.go 'return "Task"'
    assert_absent_in "Label() no longer emits 'Bug'" \
        internal/tasktype/tasktype.go 'return "Bug"'
    # The schema seed must not carry the old slug/label pairs.
    assert_absent_in "schema seed drops 'task','Task'" \
        internal/schema/schema.sql "(1, 'task',       'Task'),"
    assert_absent_in "schema seed drops 'bug','Bug'" \
        internal/schema/schema.sql "(2, 'bug',        'Bug'),"
    # The CLI must not offer the old type names as choices.
    assert_absent_in "CLI --type choice drops old names" \
        src/endless/cli.py '["task", "bug", "research", "epic", "brainstorm"]'
}

test_legacy_alias() {
    section "Upcasting — legacy slugs are DELIBERATELY retained so history replays"
    assert_present "Parse() accepts legacy 'task' as alias for todo" \
        internal/tasktype/tasktype.go 'case "todo", "task":'
    assert_present "Parse() accepts legacy 'bug' as alias for bugfix" \
        internal/tasktype/tasktype.go 'case "bugfix", "bug":'
    # The dedicated unit test proves aliases parse to the right ids while String()
    # emits only the new slugs, and that the seed matches the enum (VerifyIntegrity).
    assert_cmd "tasktype package tests pass (aliases + roundtrip + integrity)" \
        go test -count=1 ./internal/tasktype/...
}

test_handoff_render() {
    section "Behavior — renamed handoff templates render; old names are gone"
    assert_render "todo handoff renders the verify contract" \
        handoff/todo has 'Hand me exactly ONE command to verify'
    assert_render "bugfix handoff leads with reproduce-first" \
        handoff/bugfix has 'Reproduce the bug first'
    assert_file "todo template file exists" \
        internal/templatecmd/templates/handoff/todo.md.tmpl
    assert_file "bugfix template file exists" \
        internal/templatecmd/templates/handoff/bugfix.md.tmpl
    assert_no_file "old task template file is gone" \
        internal/templatecmd/templates/handoff/task.md.tmpl
    assert_no_file "old bug template file is gone" \
        internal/templatecmd/templates/handoff/bug.md.tmpl
    assert_render_fails "old handoff/task template name no longer resolves" \
        handoff/task
    assert_render_fails "old handoff/bug template name no longer resolves" \
        handoff/bug
}

test_suites() {
    section "Regression — full Go and Python suites stay green"
    assert_cmd "go test ./... (all packages)" \
        go test -count=1 ./...
    # The handoff render tests shell out to `endless-go` via PATH; put the
    # freshly-built worktree binary first so pytest exercises the new templates.
    assert_cmd "pytest tests/ (worktree binary on PATH)" \
        env PATH="${PWD}/bin:${PATH}" uv run pytest tests/ -q
}

# ─── main ───────────────────────────────────────────────────────────────────

main() {
    local repo_root
    repo_root=$(git rev-parse --show-toplevel 2>/dev/null)
    if [[ -z "${repo_root}" ]]; then
        printf 'ERROR: not inside a git worktree\n' >&2
        exit 2
    fi
    cd "${repo_root}" || exit 2

    if ! command -v go >/dev/null 2>&1; then
        printf 'ERROR: go not on PATH\n' >&2
        exit 2
    fi
    if ! command -v uv >/dev/null 2>&1; then
        printf 'ERROR: uv not on PATH\n' >&2
        exit 2
    fi

    # Worktrees need a go.work pointing at the local go-pkgs/ modules; without it
    # the replace directives resolve at the wrong depth and the build fails.
    if [[ ! -f "${repo_root}/go.work" ]]; then
        if command -v just >/dev/null 2>&1; then
            just go-work-init >/dev/null 2>&1
        fi
        if [[ ! -f "${repo_root}/go.work" ]]; then
            printf 'ERROR: go.work missing and could not be generated (run: just go-work-init)\n' >&2
            exit 2
        fi
    fi

    printf '%sE-1659 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  go:      %s\n' "$(go version 2>&1 | awk '{print $3}')"

    test_build
    test_new_present
    test_old_gone
    test_self_heal
    test_legacy_alias
    test_handoff_render
    test_suites

    summary
}

main "$@"

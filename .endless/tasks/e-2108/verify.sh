#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2108 and records what was true when E-2108
# landed. Edit it only if you ARE E-2108. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2108 verification — task branches are `task/<id>`, and
# task_landings.branch is retired.
#
# WHAT CHANGED, and why the two halves are one task:
#
#   ED-1587 renames task worktree branches from `task/<id>-<title-slug>` to
#   `task/<id>`. The slug was frozen at worktree creation, so a renamed task's
#   branch named what the task used to be called — but the load-bearing problem
#   was composability: a branch name could only be LOOKED UP, never constructed
#   from the id. `task_landings.branch` existed for exactly that reason.
#
#   Make the name a pure function of the id and the column becomes a second
#   source of truth, so it goes — and takes two carve-outs with it. E-1719's
#   record-only landing recorded no branch, which is why the reaper scanned the
#   column as sql.NullString and skipped the delete on NULL. E-2087 then added a
#   git fallback for a worktree with no landing row, plus a rule that the
#   fallback deliberately did NOT apply to a NULL row. One column, one null case
#   and one fallback collapse into one line: ask git what the directory has
#   checked out.
#
#   git rather than the derived `task/<taskID>` is a deliberate choice, and it is
#   the PRODUCT-shaped one. A project that adopts this endless keeps branches cut
#   before the rename; git is right about those, right about a detached
#   `--review` tree, and right about a branch a user renamed by hand. Deriving
#   would leak every pre-rename branch as an undeletable orphan on someone else's
#   machine.
#
# THE MIGRATION, and its blast radius:
#
#   .endless/hooks/post-land/e-2108.sh renames this repo's ~146 surviving slug
#   branches. They are local-only — no remote, no PRs, no CI — which is the
#   fallout ED-1167 cited when it accepted the drift instead. `git branch -m`
#   updates HEAD in whichever worktree holds the branch, so a live session keeps
#   committing to the same branch under its new name; the hook rewrites each
#   worktree's companion in the same pass so `worktree check` does not start
#   reporting a branch mismatch it never had. Check 6 drives the real script.
#
#   A downstream project that never runs it is fine: nothing derives a name it
#   has to match. The one place a legacy name is still looked up is E-1500's
#   orphan-branch recovery, which sweeps `task/<id>-*` as well as `task/<id>` so
#   a claim can never silently strand an orphan branch cut before the rename.
#
# Run from anywhere inside the worktree:
#   esu
#   endless task verify E-2108
#
# Requires `just build` first — checks 3 and 5 drive the CANDIDATE bin/endless-go,
# not the global install. (Setup builds it if it is missing.)
#
# What it checks:
#   0. FAIL-FAST fold-in regression: go build, go vet, the whole Go suite, the
#      Python suite, `just guide-check`, `just lifecycle-check`. A failure here
#      short-circuits the rest — the remaining checks would report on rubble.
#   1. The name is a pure function of the id, and has exactly one home.
#   2. schema.sql declares no `branch`, and a fresh DB agrees — while every
#      neighbouring column, the index and the landed-notice trigger survive.
#   3. The change file migrates a POPULATED DB: the column goes, the rows and
#      the trigger stay, re-applying is a no-op, and a DB that never had the
#      column applies it cleanly.
#   4. No live reader of the retired column survives, on either side of the seam.
#   5. The reaper takes its branch name from git — proven by running its tests,
#      not by reading its source.
#   6. The post-land migration really renames branches, updates the companion,
#      moves a live worktree's HEAD, refuses a collision, and is idempotent.
#   7. `worktree land` no longer offers `--branch`.
#
# Output: pass/fail per check, then a summary. Exit 0 all-passed, 1 any failure,
# 2 setup error.

# Refuse a direct run, and pick up the shared harness vocabulary. Sourced as the
# FIRST executable statement so the refusal fires before anything else runs.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

REPO_ROOT=""
GO=""
PY=""
EN=""
WORK=""
CHANGE="internal/schema/changes/e-2108-drop-task-landings-branch.go"
HOOK=".endless/hooks/post-land/e-2108.sh"

# ─── local assertions the harness does not carry ────────────────────────────

# assert_succeeds DESC CMD [ARGS...]
assert_succeeds() {
    local desc="$1"; shift
    local out rc
    out=$("$@" 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "exit 0" "exit ${rc} | $(tail -25 <<<"${out}")"
}

# assert_file_lacks DESC FILE LITERAL
assert_file_lacks() {
    local desc="$1" file="$2" pat="$3"
    if ! grep -qF -- "${pat}" "${file}" 2>/dev/null; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "${file} lacks: ${pat}" "still present"
}

# assert_no_hits DESC HITS
assert_no_hits() {
    local desc="$1" hits="$2"
    if [[ -z "${hits}" ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "zero hits" "${hits}"
}

# g runs git in a directory and prints its combined output.
g() {
    local dir="$1"; shift
    git -C "${dir}" "$@" 2>&1
}

# ─── setup ──────────────────────────────────────────────────────────────────

cleanup() {
    if [[ -n "${WORK}" && -d "${WORK}" ]]; then
        rm -rf "${WORK}"
    fi
}

setup() {
    REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null)
    [[ -z "${REPO_ROOT}" ]] && setup_error "not inside a git worktree"
    cd "${REPO_ROOT}" || setup_error "cannot cd to ${REPO_ROOT}"

    GO="${REPO_ROOT}/bin/endless-go"
    PY="${REPO_ROOT}/.venv/bin/python"
    EN="${REPO_ROOT}/.venv/bin/endless"
    command -v sqlite3 >/dev/null 2>&1 || setup_error "sqlite3 not on PATH"

    if [[ ! -x "${GO}" ]]; then
        printf 'building bin/endless-go …\n'
        ( cd "${REPO_ROOT}" && just build >/dev/null 2>&1 ) \
            || setup_error "just build failed"
    fi
    if [[ ! -x "${PY}" || ! -x "${EN}" ]]; then
        ( cd "${REPO_ROOT}" && uv run endless --version >/dev/null 2>&1 ) \
            || setup_error "could not materialize .venv (uv run endless failed)"
    fi

    WORK=$(mktemp -d)
    trap cleanup EXIT
}

# ─── 0 — the fail-fast fold-in regression front ─────────────────────────────
#
# The project-wide regression, folded in rather than handed to the user as a
# separate checklist. Everything below assumes a tree that builds.
check_regression_front() {
    section "0 — fail-fast fold-in regression"

    assert_succeeds "go build ./... is clean" go build ./...
    assert_succeeds "go vet ./... is clean (catches orphaned test references)" \
        go vet ./...
    assert_succeeds "the whole Go suite passes" go test -timeout 600s ./...
    assert_succeeds "the Python suite passes" uv run pytest -q
    assert_succeeds "just guide-check is green (command→section map intact)" \
        just guide-check
    assert_succeeds "just lifecycle-check is green" just lifecycle-check
}

# ─── 1 — the name is a pure function of the id ──────────────────────────────
#
# The property everything else rests on. Asserted through the real function, not
# a re-implementation of it here.
check_branch_name() {
    section "1 — task/<id>, constructed from the id alone"

    local out
    out=$("${PY}" -c \
        'from endless.worktree_cmd import task_branch; print(task_branch(2108))' \
        2>&1)
    assert_eq "task_branch(2108) is task/2108" "task/2108" "${out}"

    out=$("${PY}" -c \
        'from endless.worktree_cmd import task_branch; print(task_branch(1))' 2>&1)
    assert_eq "task_branch(1) is task/1 — no padding, no slug" "task/1" "${out}"

    # One home for the pattern. worktree_cmd.py is that home — task_branch()
    # itself, and E-1500's legacy-orphan glob — so it is the one file excluded.
    # A second f-string spelling the pattern out anywhere else is how the two
    # construction sites drifted apart from each other in the first place.
    local hits
    hits=$(grep -rn --exclude=worktree_cmd.py \
        -e 'task/{task_id}' -e 'task/{tid}' -e 'task/{id}' \
        src cmd internal 2>/dev/null)
    assert_no_hits "no file but worktree_cmd.py constructs a branch name" "${hits}"

    # Gone from the source, not merely unused. A test that still called it could
    # not import, so the Go/Python suites in check 0 cover the other direction.
    hits=$(grep -rn '_slugify_title' src cmd internal 2>/dev/null)
    assert_no_hits "the title slugifier is deleted" "${hits}"

    # create_task_worktree no longer takes a title, which is what makes the
    # branch name unable to depend on one.
    hits=$(grep -rn 'def create_task_worktree' -A 2 src/endless/worktree_cmd.py \
        | grep 'title')
    assert_no_hits "create_task_worktree takes no title" "${hits}"
}

# ─── 2 — the schema shape, and the overreach guard ──────────────────────────

check_schema_shape() {
    section "2 — schema.sql drops branch, and keeps everything beside it"

    assert_file_lacks "schema.sql declares no task_landings.branch column" \
        "internal/schema/schema.sql" "branch            TEXT,"

    local db cols objects
    db="${WORK}/fresh.db"
    if ! sqlite3 "${db}" < internal/schema/schema.sql >/dev/null 2>&1; then
        report_fail "schema.sql applies cleanly to an empty DB" "exit 0" \
            "sqlite3 failed"
        return
    fi
    report_pass "schema.sql applies cleanly to an empty DB"

    cols=$(sqlite3 "${db}" \
        "SELECT ','||group_concat(name)||',' FROM pragma_table_info('task_landings');")
    assert_not_contains "a fresh DB's task_landings has no branch column" \
        ",branch," "${cols}"

    # The overreach guard. base_branch answers the opposite question and no id
    # determines it, so it and every other column must survive untouched.
    local keep
    for keep in id task_id session_id base_branch merge_commit_sha landed_at \
                landed_by_harness; do
        assert_contains "a fresh DB KEEPS task_landings.${keep}" \
            ",${keep}," "${cols}"
    done

    objects=$(sqlite3 "${db}" \
        "SELECT ','||group_concat(name)||',' FROM sqlite_master;")
    assert_contains "the landing index survives" ",idx_task_landings_task," \
        "${objects}"
    assert_contains "the landed-notice trigger survives" \
        ",task_landings_notify_sessions," "${objects}"
}

# ─── 3 — the change file migrates a populated DB ────────────────────────────
#
# The change file is what runs against the real populated DB at land time, so
# prove it on a populated DB carrying the old column rather than trusting the
# DDL.
#
# The pre-change shape is the CURRENT schema.sql with `branch` added back, not a
# hand-rolled miniature of task_landings, and that is not laziness — SQLite's
# DROP COLUMN re-parses every view and trigger in the schema before it will drop
# anything. A cut-down fixture fails on `live_tasks` (no `tasks.removed`), then on
# `tasks_updated_at` (no `tasks.updated_at`), and so on down the file: the only
# fixture that can reach the code under test is a whole one. Rebuilding from
# schema.sql also means no git archaeology and no drift as the schema moves.
#
# Column ORDER differs from the historical DB — the re-added `branch` lands last
# rather than fourth — and nothing here depends on it: DROP COLUMN takes a name.
check_change_file_migrates() {
    section "3 — the change file drops the column from a populated DB"

    if [[ ! -f "${CHANGE}" ]]; then
        report_fail "the change file exists" "${CHANGE}" "absent"
        return
    fi
    report_pass "the change file exists"

    local mdir="${WORK}/migrate"
    mkdir -p "${mdir}"
    if ! sqlite3 "${mdir}/endless.db" < internal/schema/schema.sql >/dev/null 2>&1; then
        report_fail "the pre-change fixture builds" "schema.sql applies" \
            "sqlite3 failed"
        return
    fi
    if ! sqlite3 "${mdir}/endless.db" \
        "ALTER TABLE task_landings ADD COLUMN branch TEXT;" >/dev/null 2>&1; then
        report_fail "the pre-change fixture builds" "branch column re-added" \
            "ALTER TABLE failed"
        return
    fi
    # Two landing rows: a normal one, and the E-1719 record-only case whose
    # branch was NULL — the row shape the reaper's retired carve-out existed for.
    sqlite3 "${mdir}/endless.db" <<'SQL'
INSERT INTO projects (id, name, path) VALUES (1, 'p', '/tmp/p');
INSERT INTO tasks (id, project_id, title, phase, status, type_id)
VALUES (7, 1, 't', 'now', 'ready', 1);
INSERT INTO task_landings
    (task_id, branch, base_branch, merge_commit_sha, landed_at)
VALUES (7, 'task/7-an-old-slug', 'main', 'deadbeef', '2026-01-01T00:00:00'),
       (7, NULL, NULL, 'cafebabe', '2026-01-02T00:00:00');
SQL

    local pre applied cols rows
    pre=$(sqlite3 "${mdir}/endless.db" \
        "SELECT count(*) FROM pragma_table_info('task_landings') WHERE name='branch';")
    assert_eq "PRE: the old DB really carries the column" "1" "${pre}"
    assert_eq "PRE: and two landing rows, one of them the E-1719 NULL case" "2" \
        "$(sqlite3 "${mdir}/endless.db" "SELECT count(*) FROM task_landings;")"

    applied=$("${GO}" --config-dir "${mdir}" event apply-change "${CHANGE}" 2>&1)
    assert_contains "apply-change succeeds on the populated old DB" \
        '"status":"applied"' "${applied}"

    cols=$(sqlite3 "${mdir}/endless.db" \
        "SELECT ','||group_concat(name)||',' FROM pragma_table_info('task_landings');")
    assert_not_contains "POST: branch is dropped" ",branch," "${cols}"
    assert_contains "POST: base_branch is NOT — it answers a different question" \
        ",base_branch," "${cols}"

    assert_eq "POST: both landing rows survive the drop" "2" \
        "$(sqlite3 "${mdir}/endless.db" "SELECT count(*) FROM task_landings;")"
    assert_eq "POST: and their surviving fields are intact" \
        "7|main|deadbeef|2026-01-01T00:00:00" \
        "$(sqlite3 "${mdir}/endless.db" \
            "SELECT task_id||'|'||base_branch||'|'||merge_commit_sha||'|'||landed_at \
             FROM task_landings WHERE merge_commit_sha='deadbeef';")"

    # apply-change opens the DB through monitor.DB(), which applies schema.sql
    # first — so the run is also an end-to-end check that the CURRENT schema and
    # this change agree, and that the drop takes nothing else with it.
    rows=$(sqlite3 "${mdir}/endless.db" \
        "SELECT ','||group_concat(name)||',' FROM sqlite_master;")
    assert_contains "POST: the landing index survives the migration" \
        ",idx_task_landings_task," "${rows}"
    assert_contains "POST: the landed-notice trigger survives the migration" \
        ",task_landings_notify_sessions," "${rows}"

    # Idempotence: `just land` may re-run a change after a partial failure.
    applied=$("${GO}" --config-dir "${mdir}" event apply-change "${CHANGE}" 2>&1)
    assert_not_contains "re-applying is a no-op, not an error" \
        '"status":"applied"' "${applied}"

    # A DB that NEVER had the column must survive it too — that is every fresh
    # install and every sandbox, built from a schema.sql that no longer declares
    # it. SQLite has no DROP COLUMN IF EXISTS, which is why this change is a .go
    # with a pragma probe rather than one line of .sql.
    local ndir="${WORK}/never"
    mkdir -p "${ndir}"
    applied=$("${GO}" --config-dir "${ndir}" event apply-change "${CHANGE}" 2>&1)
    assert_contains "a DB that never had the column applies it cleanly" \
        '"status":"applied"' "${applied}"
    cols=$(sqlite3 "${ndir}/endless.db" \
        "SELECT ','||group_concat(name)||',' FROM pragma_table_info('task_landings');")
    assert_not_contains "…and still has no branch column afterwards" \
        ",branch," "${cols}"
}

# ─── 4 — no live reader of the retired column survives ──────────────────────
#
# Comments are filtered out: a line explaining why the column is absent is the
# removal documenting itself. .endless/ is excluded wholesale — the ledger,
# plans, landed change files and other tasks' frozen verify suites all name what
# they recorded, and none of it is imported, built or read by the product.
check_no_live_readers() {
    section "4 — no live reader of task_landings.branch survives"

    # The three unambiguous spellings the column had in a query, listed rather
    # than pattern-matched: `branch` is a substring of `base_branch` and a
    # perfectly ordinary Python parameter name, so a looser sweep reports the
    # code that is CORRECT. e-1719-* and e-2108-* are excluded — the first is the
    # kept change file that made the column nullable, the second is this change's
    # own DDL, and both must name what they touched. A spelling this misses still
    # cannot survive: the column is gone, so any query naming it fails, and check
    # 0 runs both suites.
    local hits
    hits=$(grep -rnI --exclude-dir=vendor --exclude-dir=.git \
        --exclude-dir=.endless \
        --exclude='e-1719-*' --exclude='e-2108-*' \
        -e 'SELECT branch' -e 'SELECT landed_at, branch' \
        -e 'session_id, branch,' \
        cmd internal src tests docs 2>/dev/null \
        | grep -vE ':[0-9]+:[[:space:]]*(#|--|//)')
    assert_no_hits "no live query names the task_landings.branch column" "${hits}"

    # Scoped to the struct, not the package: `Branch` is an ordinary field name
    # and internal/events has an unrelated one (LedgerOrphanReport.Branch, E-1957).
    # A package-wide sweep reports that as a failure of this task, which is how a
    # verify suite starts lying about code it has no opinion on.
    local block
    block=$(awk '/^type TaskLandedPayload struct \{/{f=1} f{print} f&&/^\}/{exit}' \
        internal/events/payload.go)
    if [[ -z "${block}" ]]; then
        report_fail "TaskLandedPayload carries no Branch field" \
            "the struct is declared in payload.go" "struct not found"
    else
        # -w keeps BaseBranch out: `_` and letters are word constituents, so
        # \bBranch\b cannot match inside it.
        hits=$(grep -nw 'Branch' <<<"${block}")
        assert_no_hits "TaskLandedPayload carries no Branch field" "${hits}"
    fi

    assert_file_lacks "the live executor inserts no branch column" \
        "internal/events/executor.go" "session_id, branch, base_branch"
    assert_file_lacks "the replay projector inserts no branch column either" \
        "internal/events/projector.go" "session_id, branch, base_branch"

    hits=$(grep -n "land\[.branch.\]\|landings\[0\]\[.branch.\]" \
        src/endless/task_cmd.py 2>/dev/null)
    assert_no_hits "\`task landed\` reads no branch out of a landing row" "${hits}"

    # A pre-E-2108 event still carries a "branch" key in the ledger, and a
    # rebuild replays every one of them. Dropping the key silently is the
    # compatibility claim; the Go suite proves it, this names it.
    assert_contains "the ledger-compatibility test is present" \
        "TestExecTaskLanded_LegacyBranchKeyIgnored" \
        "$(cat internal/events/task_landed_test.go)"
}

# ─── 5 — the reaper takes its branch name from git ──────────────────────────
#
# Proven by running the reaper's own tests rather than reading its source: the
# question is behavioural (does a detached tree skip `git branch -D`, does a
# worktree with no landing row still get its branch deleted), and a grep cannot
# answer either.
check_reaper_reads_git() {
    section "5 — the reaper asks git which branch a worktree holds"

    local out rc
    out=$(go test -count=1 -v -run \
        'TestMaybeReapWorktree_(ReapsCleanAbandoned|DetachedHeadSkipsBranchDelete|SettledWithNoLandingIsReapable)' \
        ./internal/monitor/ 2>&1)
    rc=$?
    if [[ "${rc}" -ne 0 ]]; then
        report_fail "the reaper's branch-name tests pass" "exit 0" \
            "exit ${rc} | $(tail -25 <<<"${out}")"
        return
    fi
    report_pass "the reaper's branch-name tests pass"

    assert_contains "a clean landed worktree still gets its branch deleted" \
        "--- PASS: TestMaybeReapWorktree_ReapsCleanAbandoned" "${out}"
    assert_contains "a detached worktree is reaped WITHOUT a branch delete" \
        "--- PASS: TestMaybeReapWorktree_DetachedHeadSkipsBranchDelete" "${out}"
    assert_contains "a worktree with no landing row still names its branch" \
        "--- PASS: TestMaybeReapWorktree_SettledWithNoLandingIsReapable" "${out}"

    assert_file_lacks "the reaper's landing query no longer selects branch" \
        "internal/monitor/reap_worktrees.go" "SELECT landed_at, branch"
}

# ─── 6 — the post-land migration ────────────────────────────────────────────
#
# The only piece of this task with no other coverage: the script that renames
# this repo's ~146 surviving slug branches. Driven for real against a scratch
# repo carrying every case it will meet — a branch with a live worktree and a
# companion, a bare branch, and an id whose target name is already taken.
check_migration_hook() {
    section "6 — the post-land migration renames the existing branches"

    if [[ ! -x "${HOOK}" ]]; then
        report_fail "the post-land hook is committed and executable" \
            "${HOOK} executable" "missing or not +x"
        return
    fi
    report_pass "the post-land hook is committed and executable"

    local repo="${WORK}/repo"
    mkdir -p "${repo}"
    g "${repo}" init -q -b main . >/dev/null
    g "${repo}" config user.email t@t.t >/dev/null
    g "${repo}" config user.name t >/dev/null
    printf 'hi\n' > "${repo}/README.md"
    g "${repo}" add -A >/dev/null
    g "${repo}" commit -qm init >/dev/null

    g "${repo}" branch task/10-alpha-beta main >/dev/null
    g "${repo}" branch task/11-gamma main >/dev/null
    g "${repo}" branch task/12-delta main >/dev/null
    g "${repo}" branch task/12 main >/dev/null   # the collision
    g "${repo}" worktree add -q .endless/worktrees/e-10 task/10-alpha-beta >/dev/null
    mkdir -p "${repo}/.endless/worktrees/e-10/.endless"
    cat > "${repo}/.endless/worktrees/e-10/.endless/worktree.json" <<'JSON'
{
  "kind": "task",
  "base_branch": "main",
  "branch": "task/10-alpha-beta",
  "created_at": "2026-09-06T21:12:06.763712+00:00"
}
JSON

    local out rc branches
    out=$(bash "${REPO_ROOT}/${HOOK}" "${repo}" 2>&1)
    rc=$?
    assert_eq "the hook exits 0 when nothing failed" "0" "${rc}"
    assert_contains "it reports what it did" "2 renamed, 1 skipped, 0 failed" \
        "${out}"

    branches=",$(g "${repo}" for-each-ref --format='%(refname:short)' refs/heads/ \
        | tr '\n' ',')"
    assert_contains "task/10-alpha-beta became task/10" ",task/10," "${branches}"
    assert_not_contains "…and the slug name is gone" ",task/10-alpha-beta," \
        "${branches}"
    assert_contains "task/11-gamma became task/11" ",task/11," "${branches}"

    # A live worktree keeps working: git moved its HEAD with the ref, and the
    # companion was rewritten so `worktree check` still agrees with git.
    assert_eq "the live worktree's HEAD moved with the branch" "task/10" \
        "$(g "${repo}/.endless/worktrees/e-10" symbolic-ref --short HEAD)"
    assert_contains "and its companion records the new name" '"branch": "task/10"' \
        "$(cat "${repo}/.endless/worktrees/e-10/.endless/worktree.json")"

    # A target name already in use is reported, never forced: two branches
    # claiming one task id is a state a human should look at, and `git branch -M`
    # would silently destroy one of them.
    assert_contains "a name collision is skipped, not forced" \
        "task/12 already exists" "${out}"
    assert_contains "…and the colliding branch is left alone" ",task/12-delta," \
        "${branches}"

    # Idempotent: a second run finds nothing left with the old shape.
    out=$(bash "${REPO_ROOT}/${HOOK}" "${repo}" 2>&1)
    assert_contains "re-running renames nothing" "0 renamed" "${out}"

    # And on a repo with no slug branches at all — every downstream project that
    # never had one, and this repo after the migration.
    local clean="${WORK}/clean"
    mkdir -p "${clean}"
    g "${clean}" init -q -b main . >/dev/null
    g "${clean}" config user.email t@t.t >/dev/null
    g "${clean}" config user.name t >/dev/null
    printf 'hi\n' > "${clean}/README.md"
    g "${clean}" add -A >/dev/null
    g "${clean}" commit -qm init >/dev/null
    out=$(bash "${REPO_ROOT}/${HOOK}" "${clean}" 2>&1)
    rc=$?
    assert_eq "a repo with no task branches exits 0" "0" "${rc}"
    assert_contains "…and says so rather than failing" "nothing to do" "${out}"
}

# ─── 7 — the CLI surface ────────────────────────────────────────────────────

check_cli_surface() {
    section "7 — \`worktree land\` no longer offers --branch"

    local out
    out=$(cd "${REPO_ROOT}" && "${EN}" worktree land --help 2>&1)
    assert_not_contains "\`worktree land --help\` lists no --branch" \
        "--branch" "${out}"
    assert_contains "…but --record-only and --sha are still there" \
        "--record-only" "${out}"
}

main() {
    setup

    check_regression_front
    if (( FAIL_COUNT > 0 )); then
        printf '\n  %sregression front failed — stopping before the rest%s\n' \
            "${RED}" "${RESET}"
        summary
    fi

    check_branch_name
    check_schema_shape
    check_change_file_migrates
    check_no_live_readers
    check_reaper_reads_git
    check_migration_hook
    check_cli_surface

    summary
}

main "$@"

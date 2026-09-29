#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2189 and records what was true when E-2189
# landed. Edit it only if you ARE E-2189. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2189: `task replace` becomes `task supersede`, and the relation it records
# becomes `supersedes` / `superseded_by` — stored dep_type, display names,
# labels, the inline status note, the --json / --agent keys and the flag —
# matching the `superseded` status and `decision supersede`.
#
# What is verified here:
#   A. Fail-fast: this task's own Go and Python tests.
#   B. The real CLI against a real, isolated database: supersede, the hidden
#      working `replace` alias, the retired `--replaces` flag, `task link`.
#
# History replaying is proven in A, not here: every task_dep event written
# before this task says 'replaces', and the events tests drive both the live
# executor and the replay handler with one. A shell rebuild-db is a dry run
# that discards its projection, so it could only show "exit 0".
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

REPO_ROOT="$(git rev-parse --show-toplevel)"
cd "${REPO_ROOT}" || setup_error "cannot cd to worktree root ${REPO_ROOT}"

# ---------------------------------------------------------------------------
section "A. This task's own tests (fail-fast)"
# ---------------------------------------------------------------------------

go_pkg() { # go_pkg <label> <go test args...>
    local label="$1"; shift
    if out=$(go test "$@" 2>&1); then
        report_pass "go test: ${label}"
    else
        report_fail "go test: ${label}" "exit 0" "$(printf '%s' "${out}" | tail -30)"
        summary
    fi
}

py_file() { # py_file <label> <pytest target...>
    local label="$1"; shift
    if out=$(uv run pytest -q -p no:cacheprovider "$@" 2>&1); then
        report_pass "pytest: ${label}"
    else
        report_fail "pytest: ${label}" "exit 0" "$(printf '%s' "${out}" | tail -30)"
        summary
    fi
}

go_pkg "legacy 'replaces' ledger events resolve to 'supersedes' (live + replay)" \
    ./internal/events/ -run 'TaskDep'
go_pkg "migration 00012, and migrated == schema.sql" ./internal/schema/
go_pkg "session status reads the supersedes rows" ./internal/monitor/ -run 'SessionStatusRow|SupersededBy|Duplicates'
go_pkg "session status note and --json key" ./internal/sessionstatuscmd/
go_pkg "the lifecycle table" ./internal/taskstatus/

py_file "superseded_by inline, supersede and its alias" tests/test_superseded_by_inline.py
py_file "the --supersedes flag, --replaces retired"     tests/test_duplicates_inline.py
py_file "relation vocabulary registries"                tests/test_relations.py
py_file "reason required on supersede"                  tests/test_outcome.py
py_file "the not-authoritative banner"                  tests/test_authority_banner.py
py_file "status notes stay out of the tables"           tests/test_status_column_width.py

# ---------------------------------------------------------------------------
section "B. The real CLI, against a real isolated database"
# ---------------------------------------------------------------------------

GO="${REPO_ROOT}/bin/endless-go"
EN="${REPO_ROOT}/.venv/bin/endless"
[[ -x "${GO}" ]] || setup_error "${GO} missing — run \`just build\`"
[[ -x "${EN}" ]] || ( uv run endless --version >/dev/null 2>&1 ) \
    || setup_error "could not materialize .venv (uv run endless failed)"

WORK="$(cd "$(mktemp -d)" && pwd -P)"
trap 'rm -rf "${WORK}"' EXIT
export XDG_CONFIG_HOME="${WORK}/config"
export XDG_CACHE_HOME="${WORK}/cache"
export ENDLESS_AUTO_MIGRATE=1
export ENDLESS_NO_TRIAGE=1            # no model call from a verify run
export PATH="${REPO_ROOT}/bin:${PATH}"
unset ENDLESS_SESSION_ID CLAUDECODE CLAUDE_CODE_SESSION_ID CLAUDE_CODE_ENTRYPOINT 2>/dev/null || true
mkdir -p "${XDG_CONFIG_HOME}" "${XDG_CACHE_HOME}"

PROJ="${WORK}/proj"
mkdir -p "${PROJ}"
git -C "${PROJ}" init -q -b main
git -C "${PROJ}" config user.email "verify@example.com"
git -C "${PROJ}" config user.name "Verify"
git -C "${PROJ}" config commit.gpgsign false
: > "${PROJ}/README.md"
git -C "${PROJ}" add README.md && git -C "${PROJ}" commit -q -m init

en() { ( cd "${PROJ}" && "${EN}" "$@" 2>&1 ); }
en project register "${PROJ}" --infer --name verify2189 --status active >/dev/null \
    || setup_error "registering the temp project failed"

add() { # add <title> → E-N
    local out
    out=$(en task add "$1" --description "$1 for the verify run.") \
        || { printf '%s' "${out}"; return 1; }
    printf '%s\n' "${out}" | grep -oE 'E-[0-9]+' | head -1
}
field() { # field <E-N> <json key>
    en task show "$1" --json | python3 -c "import json,sys; v=json.load(sys.stdin)['$2']; print(','.join(v) if isinstance(v,list) else v)"
}
DBFILE="${XDG_CONFIG_HOME}/endless/endless.db"
dep_rows() { sqlite3 "${DBFILE}" "SELECT dep_type FROM task_deps WHERE source_id = $1 AND target_id = $2"; }

OLD=$(add "Add the old thing") || setup_error "task add failed: ${OLD}"
NEW=$(add "Add the successor") || setup_error "task add failed: ${NEW}"

out=$(en task supersede "${OLD}" --by "${NEW}" --outcome "the successor subsumes it")
assert_contains "task supersede reports the supersession" "(superseded by ${NEW})" "${out}"
assert_eq "the superseded task is 'superseded'" "superseded" "$(field "${OLD}" status)"
assert_eq "--json carries superseded_by" "${NEW}" "$(field "${OLD}" superseded_by)"
assert_eq "the stored dep_type is 'supersedes'" "supersedes" "$(dep_rows "${NEW#E-}" "${OLD#E-}")"
assert_contains "task show labels the link 'superseded by'" "superseded by" "$(en task show "${OLD}" | tr 'A-Z' 'a-z')"
assert_not_contains "task show no longer says 'replaced by'" "replaced by" "$(en task show "${OLD}" | tr 'A-Z' 'a-z')"
assert_contains "--agent carries superseded_by=" "superseded_by=${NEW}" "$(en task show "${OLD}" --agent)"

OLD2=$(add "Add another old thing") || setup_error "task add failed"
NEW2=$(add "Add another successor") || setup_error "task add failed"
out=$(en task replace "${OLD2}" --by "${NEW2}" --outcome "handed on")
assert_eq "the hidden 'task replace' alias still works" "superseded" "$(field "${OLD2}" status)"
assert_eq "…and records the same 'supersedes' row" "supersedes" "$(dep_rows "${NEW2#E-}" "${OLD2#E-}")"

help=$(en task --help)
assert_contains "task --help lists supersede" "  supersede " "${help}"
assert_not_contains "task --help does not list replace" "  replace " "${help}"

A=$(add "Add a third thing") || setup_error "task add failed"
B=$(add "Add a fourth thing") || setup_error "task add failed"
out=$(en task update "${A}" --replaces "${B}")
assert_contains "--replaces is refused by name, pointing at --supersedes" \
    "--replaces was renamed to --supersedes" "${out}"
assert_eq "…and writes no relation" "" "$(dep_rows "${A#E-}" "${B#E-}")"
B_BEFORE="$(field "${B}" status)"
en task update "${A}" --supersedes "${B}" >/dev/null
assert_eq "--supersedes records the relation" "supersedes" "$(dep_rows "${A#E-}" "${B#E-}")"
assert_eq "--supersedes leaves the other task's status alone" "${B_BEFORE}" "$(field "${B}" status)"

C=$(add "Add a fifth thing") || setup_error "task add failed"
out=$(en task link "${C}" --to "${A}" --type replaces)
assert_contains "task link rejects the old relation name" "Invalid relation type 'replaces'" "${out}"

summary

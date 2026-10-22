#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2422 and records what was true when E-2422
# landed. Edit it only if you ARE E-2422. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2422: the session-notice fan-out moves from five SQLite triggers into the
# Go executor, so notices survive tasks moving to a per-project database.
#
# What is verified here:
#   A. Fail-fast: this task's Go tests — the executor-path notice suite, the
#      actor and landing suites it ported, the migration that drops the
#      triggers, the delivery tests in internal/monitor, and the fault catalog.
#   B. End to end: an endless-go built from this tree, against a scratch
#      database, emitting real events as a person and as an agent.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || setup_error "not inside a git worktree"
cd "${WT}" || setup_error "cannot cd to worktree root ${WT}"
command -v sqlite3 >/dev/null 2>&1 || setup_error "sqlite3 is required to seed the scratch database"

E2E="$(mktemp -d)" || setup_error "cannot create a scratch directory"
trap 'rm -rf "${E2E}"' EXIT

# ---------------------------------------------------------------------------
section "A. This task's own tests (fail-fast)"
# ---------------------------------------------------------------------------

go_tests() {
    local label="$1"; shift
    local out
    if out=$(go test "$@" 2>&1); then
        report_pass "${label}"
    else
        report_fail "${label}" "pass" "$(printf '%s' "${out}" | tail -40)"
        summary
    fi
}
go_tests "events: notice fan-out, actor, landing and one-edit-one-notice tests" \
    ./internal/events/ -run 'Notice|TaskContent_OneEditOneNotice|ExecTaskLanded' -count=1
go_tests "schema: migrations, including 00025 dropping the triggers and the column" \
    ./internal/schema/... -count=1
go_tests "monitor: notice delivery, reaping and rendering" \
    ./internal/monitor/ -run 'Notice' -count=1
go_tests "faults: WARN-0036 is catalogued, documented and its number spent" \
    ./internal/faults/ -count=1

# ---------------------------------------------------------------------------
section "B. End to end: endless-go against a scratch database"
# ---------------------------------------------------------------------------

mkdir -p "${E2E}/home/.config/endless" "${E2E}/proj" "${E2E}/bin"
go build -o "${E2E}/bin/endless-go" ./cmd/endless-go || setup_error "cannot build cmd/endless-go from this tree"
(cd "${E2E}/proj" && git init -q && git commit -q --allow-empty -m init) \
    || setup_error "cannot create the scratch project"
DBDIR="${E2E}/home/.config/endless"
DB="${DBDIR}/endless.db"

e2e() {
    (cd "${E2E}" && env -u TMUX -u TMUX_PANE HOME="${E2E}/home" \
        XDG_CONFIG_HOME="${E2E}/home/.config" PATH="${E2E}/bin:${PATH}" "$@")
}
emit() {
    e2e endless-go --db-dir "${DBDIR}" event emit --node-id 00aa --project proj \
        --project-root "${E2E}/proj" --actor-kind cli --actor-id verify \
        --entity-type task "$@" >/dev/null
}
notices() { sqlite3 "${DB}" "SELECT changes FROM session_notices WHERE session_id = $1 ORDER BY id"; }
count()   { sqlite3 "${DB}" "SELECT count(*) FROM session_notices WHERE session_id = $1"; }

e2e endless-go --db-dir "${DBDIR}" session-query list-live --project-root "${E2E}/proj" \
    >/dev/null 2>&1 || setup_error "cannot initialize the scratch database"
emit --kind task.created --entity-id 1 \
    --payload '{"title":"held","phase":"now","status":"ready","type":"todo"}' \
    || setup_error "cannot create the scratch task"
sqlite3 "${DB}" <<'SQL' || setup_error "cannot seed sessions holding the task"
INSERT INTO sessions (id, session_id, project_id, platform, state, task_id) VALUES
  (11, 's-agent', 1, 'claude', 'working', 1),
  (12, 's-other', 1, 'claude', 'idle',    1),
  (13, 's-ended', 1, 'claude', 'ended',   1);
INSERT INTO session_tasks (session_id, task_id, created_at, updated_at) VALUES
  (11, 1, 'x', 'x'), (12, 1, 'x', 'x'), (13, 1, 'x', 'x');
SQL

assert_eq "the migrated database carries none of the five notice triggers" "0" \
    "$(sqlite3 "${DB}" "SELECT count(*) FROM sqlite_master WHERE type = 'trigger' AND name IN
        ('tasks_notify_sessions', 'task_content_notify_insert', 'task_content_notify_update',
         'task_content_notify_delete', 'task_landings_notify_sessions')")"
assert_eq "and tasks.changed_by_session is gone" "0" \
    "$(sqlite3 "${DB}" "SELECT count(*) FROM pragma_table_info('tasks') WHERE name = 'changed_by_session'")"

# A person's edit, credited to the agent's session the way `esu` would: nobody
# is suppressed, and one edit is one notice naming every field it moved.
emit --kind task.fields_updated --entity-id 1 --session-id 11 \
    --payload '{"fields":{"status":"revisit","complexity":"low","plan":"secret-plan-body"}}' \
    || setup_error "cannot emit the person's edit"
want='{"status":{"before":"ready","after":"revisit"},"complexity":{"before":null,"after":"low"},"plan":{"before":null,"after":"…"}}'
assert_eq "a person's edit reaches the session it was credited to, as one notice" "${want}" "$(notices 11)"
assert_eq "and the idle holder" "${want}" "$(notices 12)"
assert_eq "but never the ended one" "0" "$(count 13)"
assert_not_contains "the plan's text is never reproduced" "secret-plan-body" \
    "$(sqlite3 "${DB}" "SELECT group_concat(changes) FROM session_notices")"

# The same session, now an agent: it is not told about its own edit.
as_agent emit --kind task.fields_updated --entity-id 1 --session-id 11 \
    --payload '{"fields":{"phase":"next"}}' || setup_error "cannot emit the agent's edit"
assert_eq "an agent is not told about its own edit" "1" "$(count 11)"
assert_contains "the other holder is, attributed to the agent" \
    '{"phase":{"before":"now","after":"next"}}|11' \
    "$(sqlite3 "${DB}" "SELECT changes || '|' || changed_by_session FROM session_notices WHERE session_id = 12 ORDER BY id DESC LIMIT 1")"

emit --kind task.fields_updated --entity-id 1 --payload '{"fields":{"description":"rewritten"}}' \
    || setup_error "cannot emit the description edit"
assert_eq "a description edit writes no notice" "2" "$(count 12)"

# A person's land is announced to every holder, the attributed session included.
emit --kind task.landed --entity-id 1 --session-id 11 \
    --payload '{"base_branch":"main","merge_commit_sha":"1dd0006deadbeef"}' \
    || setup_error "cannot emit the landing"
assert_contains "a person's land reaches the session it was credited to" \
    '{"landed":{"before":null,"after":"main@1dd0006"}}' "$(notices 11)"

summary

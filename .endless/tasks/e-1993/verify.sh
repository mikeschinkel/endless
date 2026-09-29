#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1993 and records what was true when E-1993
# landed. Edit it only if you ARE E-1993. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1993 verification — cap task titles and descriptions, add the `context`
# content name, require a plan (and no open questions) to claim or spawn, and
# remove the `untriaged` status with the description triage behind it.
#
#   endless task verify E-1993
#
# Exit 0 on all-passed, 1 on any failure, 2 on setup error.

source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || setup_error "not in a git repo"
cd "${WT}" || setup_error "cannot cd to ${WT}"

TMP="$(mktemp -d)" || setup_error "could not create a temp dir"
trap 'rm -rf "${TMP}"' EXIT

# ── 1. fail-fast unit gate ──────────────────────────────────────────────────
section "1. Unit gate (fail fast)"

if go test ./internal/taskstatus/ ./internal/taskcontent/ ./internal/events/ \
        ./internal/schema/... ./internal/sessionstatuscmd/ ./internal/sessionquerycmd/ \
        ./internal/monitor/ ./internal/faults/ ./internal/taskstatuscmd/ ./internal/jobs/ \
        >"${TMP}/gotest.log" 2>&1; then
    report_pass "go test: status table, content names, legacy replay, migration 00008, session status"
else
    report_fail "go test on the touched packages" "exit 0" "$(tail -25 "${TMP}/gotest.log")"
    summary
fi

if uv run pytest -q \
        tests/test_field_limits.py \
        tests/test_context_content.py \
        tests/test_spawn_gate.py \
        tests/test_plan_required_status_model.py \
        tests/test_submit_approve.py \
        tests/test_plan_auto_promote.py \
        tests/test_keep_status.py \
        tests/test_status_change_audience.py \
        tests/test_status_lifecycle_sync.py \
        tests/test_task_claim_worktree.py \
        >"${TMP}/py.log" 2>&1; then
    report_pass "pytest: limits, context, spawn gate, status model, submit/approve, keep-status, lifecycle sync"
else
    report_fail "pytest on the touched suites" "exit 0" "$(tail -25 "${TMP}/py.log")"
    summary
fi

# ── 2. build ────────────────────────────────────────────────────────────────
section "2. Build"

mkdir -p "${TMP}/bin"
if go build -o "${TMP}/bin/endless-go" ./cmd/endless-go >"${TMP}/build.log" 2>&1; then
    report_pass "go build ./cmd/endless-go"
else
    report_fail "go build ./cmd/endless-go" "exit 0" "$(tail -25 "${TMP}/build.log")"
    summary
fi

# ── 3. vocabulary and the lifecycle picture ─────────────────────────────────
section "3. Vocabulary"

assert_eq "context is the first content name" "context	Context" \
    "$("${TMP}/bin/endless-go" task-content names | head -1)"
assert_not_contains "untriaged is not a status" "untriaged" \
    "$("${TMP}/bin/endless-go" task-status get all)"
lifecycle="$(just lifecycle-check 2>&1)"; rc=$?
assert_eq "just lifecycle-check passes (diagram matches the table)" "0" "${rc}"
assert_not_contains "no transition targets untriaged" "--> untriaged" \
    "$(cat docs/status-lifecycle.mmd)"
assert_contains "a material plan edit drops approval (ready → submitted)" \
    "ready --> submitted: system resets on a material plan edit" \
    "$(cat docs/status-lifecycle.mmd)"
assert_not_contains "README no longer describes triage" "untriaged" "$(cat README.md)"

# ── fixture: a real config dir, a real git project, a real projects row ─────
CFG_HOME="${TMP}/cfg"
CFG="${CFG_HOME}/endless"
PROJ="${TMP}/proj"
mkdir -p "${CFG}" "${PROJ}/.endless" "${TMP}/home"

printf '{"name": "e1993"}\n' >"${PROJ}/.endless/config.json"
printf '{"node_id": "e199"}\n' >"${CFG}/config.json"
git -C "${PROJ}" init -q -b main             >/dev/null 2>&1 || setup_error "git init failed"
git -C "${PROJ}" config user.email t@e.com   >/dev/null 2>&1
git -C "${PROJ}" config user.name  T         >/dev/null 2>&1
git -C "${PROJ}" config commit.gpgsign false >/dev/null 2>&1
git -C "${PROJ}" add .endless/config.json    >/dev/null 2>&1
git -C "${PROJ}" commit -qm init             >/dev/null 2>&1 || setup_error "git commit failed"

cat >"${TMP}/seed.py" <<'PY'
import os
from pathlib import Path
from endless import config
config.set_db_context(Path(os.environ["E1993_CFG"]))
from endless import db
db.execute(
    "INSERT INTO projects (id, name, path, status) VALUES (1, 'e1993', ?, 'active')",
    (os.environ["E1993_PROJ"],),
)
PY

E1993_CFG="${CFG}" E1993_PROJ="${PROJ}" PATH="${TMP}/bin:${PATH}" \
    uv run python "${TMP}/seed.py" >"${TMP}/seed.log" 2>&1 \
    || setup_error "could not seed the fixture database: $(tail -5 "${TMP}/seed.log")"

# human — one `endless` invocation as a person at a terminal, against the
# fixture database, with this worktree's endless-go first on PATH.
human() {
    ( cd "${PROJ}" && env -u ENDLESS_SESSION_ID -u CLAUDECODE \
        -u CLAUDE_CODE_ENTRYPOINT -u __CFBundleIdentifier -u TMUX -u TMUX_PANE \
        HOME="${TMP}/home" XDG_CONFIG_HOME="${CFG_HOME}" \
        PATH="${TMP}/bin:${PATH}" \
        uv run --project "${WT}" endless --no-session "$@" 2>&1 )
}

task_id() { sed -n 's/.*\(E-[0-9][0-9]*\).*/\1/p' <<<"$1" | head -1; }

new_task() {
    local out id
    out="$(human task add "$@")"
    id="$(task_id "${out}")"
    [[ -n "${id}" ]] || printf '%s\n' "${out}" >"${TMP}/task_add.log"
    printf '%s\n' "${id}"
}

need_id() {
    [[ -n "$1" ]] || setup_error "fixture task add failed: $(cat "${TMP}/task_add.log" 2>/dev/null)"
}

json_field() {
    human task show "$1" --json | uv run python -c \
        'import json,sys; v=json.load(sys.stdin)[sys.argv[1]]; print("null" if v is None else v)' "$2"
}

sql() {
    uv run python - "$1" "${CFG}/endless.db" <<'PY'
import sqlite3, sys
for row in sqlite3.connect(sys.argv[2]).execute(sys.argv[1]):
    print("|".join("" if v is None else str(v) for v in row))
PY
}

# sql_write runs a write against the fixture database and commits it.
sql_write() {
    uv run python - "$1" "${CFG}/endless.db" <<'PY'
import sqlite3, sys
con = sqlite3.connect(sys.argv[2])
con.execute(sys.argv[1])
con.commit()
PY
}

tasks_count() { sql "SELECT count(*) FROM tasks"; }

# ── 4. length and id limits ─────────────────────────────────────────────────
section "4. Titles ≤ 60, descriptions ≤ 256, no ids — refused with a destination"

before="$(tasks_count)"
out="$(human task add "Add $(printf 'x%.0s' {1..57})" --description "d")"; rc=$?
assert_eq "a 61-character title is refused" "1" "${rc}"
assert_contains "the title refusal names where the how goes" "--analysis" "${out}"
assert_contains "the title refusal names where the why goes" "--context" "${out}"

out="$(human task add "Add a widget" --description "$(printf 'd%.0s' {1..257})")"; rc=$?
assert_eq "a 257-character description is refused" "1" "${rc}"
assert_contains "the description refusal routes background to --context" "--context" "${out}"
assert_contains "the description refusal routes relationships to a task link" "task link" "${out}"

out="$(human task add "Fix the widget from E-12" --description "d")"; rc=$?
assert_eq "a task id in the title is refused" "1" "${rc}"
out="$(human task add "Fix the widget" --description "Follow-up to ED-4.")"; rc=$?
assert_eq "a decision id in the description is refused" "1" "${rc}"
assert_contains "the id refusal says to use a task link" "task link" "${out}"
assert_eq "nothing was created by any refusal" "${before}" "$(tasks_count)"

A="$(new_task "Add a widget to the cache" --description "Add a widget." --context "Today the cache has no widget; see E-99.")"
need_id "${A}"
assert_eq "ids are fine in context" "Today the cache has no widget; see E-99." "$(json_field "${A}" context)"

n="${A#E-}"
sql_write "UPDATE tasks SET title = 'Add $(printf 'y%.0s' {1..80})' WHERE id = ${n}"
out="$(human task update "${A}" --phase later)"; rc=$?
assert_eq "an update that does not write an old long title is not refused" "0" "${rc}"
assert_eq "nothing is truncated" "84" "$(sql "SELECT length(title) FROM tasks WHERE id = ${n}")"

# ── 5. context ──────────────────────────────────────────────────────────────
section "5. context: flags, default render after the description, --clear"

shown="$(human task show "${A}" --no-color)"
assert_contains "context renders by default" "— Context —" "${shown}"
desc_line="$(grep -n '— Description —' <<<"${shown}" | cut -d: -f1)"
ctx_line="$(grep -n '— Context —' <<<"${shown}" | cut -d: -f1)"
assert_eq "context renders directly after the description" "1" \
    "$([[ -n "${desc_line}" && -n "${ctx_line}" && "${ctx_line}" -gt "${desc_line}" ]] && echo 1 || echo 0)"
printf 'Evidence: three reports.\n' >"${TMP}/ctx.md"
human task update "${A}" --context-file "${TMP}/ctx.md" --keep-status >/dev/null
assert_eq "--context-file writes the row" "Evidence: three reports." "$(json_field "${A}" context)"
human task update "${A}" --clear context >/dev/null
assert_eq "--clear context deletes the row" "" \
    "$(sql "SELECT content FROM task_content WHERE task_id = ${n} AND name = 'context'")"

# ── 6. entry statuses; no untriaged; no triage ──────────────────────────────
section "6. New tasks file unplanned (or submitted with a plan); untriaged is gone"

B="$(new_task "Add a gadget" --description "Add a gadget.")"
need_id "${B}"
assert_eq "a task filed without a plan is unplanned" "unplanned" "$(json_field "${B}" status)"
C="$(new_task "Add a gizmo" --description "Add a gizmo." --plan "# Plan\nbuild it\n")"
need_id "${C}"
assert_eq "a task filed with a plan is submitted" "submitted" "$(json_field "${C}" status)"
out="$(human task update "${B}" --status untriaged)"; rc=$?
assert_eq "--status untriaged is refused" "1" "${rc}"
out="$(human triage run --help)"; rc=$?
assert_eq "endless triage no longer exists" "2" "${rc}"
out="$(human task submit "${B}")"; rc=$?
assert_eq "task submit refuses a task with no plan" "1" "${rc}"

# ── 7. the plan gate and parking on questions ───────────────────────────────
section "7. No plan, or an open question: not claimable"

out="$(human task claim "${B}" --unattended)"; rc=$?
assert_eq "claim refuses a task with no plan" "1" "${rc}"
assert_contains "the refusal gives the route forward" "--plan-file" "${out}"
assert_eq "a refused claim changes nothing" "unplanned" "$(json_field "${B}" status)"

human task approve "${C}" >/dev/null
human question ask "${C}" "Which cache tier?" >/dev/null
shown="$(human task show "${C}" --no-color)"
assert_contains "task show marks the task parked" "Parked:" "${shown}"
assert_contains "task show lists the open question" "Which cache tier?" "${shown}"
out="$(human task claim "${C}" --unattended)"; rc=$?
assert_eq "claim refuses a planned task with an open question" "1" "${rc}"
assert_contains "the refusal lists the question" "Which cache tier?" "${out}"

qid="$(sql "SELECT id FROM task_questions WHERE task_id = ${C#E-}")"
human question answer "EQ-${qid}" "The warm tier." --by user >/dev/null
out="$(human task claim "${C}" --unattended)"; rc=$?
assert_eq "answered and planned: the claim goes through" "0" "${rc}"
assert_eq "and the task is underway" "underway" "$(json_field "${C}" status)"

# ── 8. what moves status now ────────────────────────────────────────────────
section "8. A description edit never moves status; a plan edit on ready does"

D="$(new_task "Add a doohickey" --description "Add a doohickey." --plan "# Plan\nv1\n")"
need_id "${D}"
human task approve "${D}" >/dev/null
human task update "${D}" --description "Add a doohickey, again." >/dev/null
assert_eq "a description edit on a ready task keeps it ready" "ready" "$(json_field "${D}" status)"
human task update "${D}" --plan "# Plan\nv1 (typo fixed)\n" --keep-status >/dev/null
assert_eq "--keep-status suppresses the plan-edit reset" "ready" "$(json_field "${D}" status)"
human task update "${D}" --plan "# Plan\nv2, different\n" >/dev/null
assert_eq "a material plan edit returns a ready task to submitted" "submitted" "$(json_field "${D}" status)"

summary

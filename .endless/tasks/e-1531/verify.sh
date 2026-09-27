#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1531 and records what was true when E-1531
# landed. Edit it only if you ARE E-1531. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1531 verification — a task's typed prose lives in task_content, one row per
# content name, instead of the tasks.plan / outcome / analysis / notes columns.
#
# Before: four fixed columns; a decline's reason overwrote a research task's
# findings in tasks.outcome; notes were dropped on rebuild; a new kind of content
# cost a column.
#
# After: task_content rows under names from the taskcontent Go enum; `reason`
# is split from `outcome`; the guard honours a stored reason; executor and
# projector share one content map; every name is a flag, a heading and a mirror.
#
#   endless task verify E-1531
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

if go test ./internal/taskcontent/ ./internal/taskstatus/ ./internal/schema/... \
        ./internal/events/ ./internal/docmirror/ ./internal/docsweep/ \
        ./internal/monitor/ ./internal/hookcmd/ ./internal/sessionquerycmd/ \
        >"${TMP}/gotest.log" 2>&1; then
    report_pass "go test: taskcontent, schema (lift + parity), events (live/rebuild parity), mirrors, monitor"
else
    report_fail "go test on the touched packages" "exit 0" "$(tail -25 "${TMP}/gotest.log")"
    summary
fi

if uv run pytest -q \
        tests/test_task_content.py \
        tests/test_outcome.py \
        tests/test_task_show_payload.py \
        tests/test_research_gate.py \
        tests/test_doc_mirror_to_main.py \
        tests/test_suite_rules.py \
        >"${TMP}/py.log" 2>&1; then
    report_pass "pytest: content, outcome/reason, show payload, notes, mirrors, suite rules"
else
    report_fail "pytest on the touched suites" "exit 0" "$(tail -25 "${TMP}/py.log")"
    summary
fi

# ── 2. build ────────────────────────────────────────────────────────────────
# From THIS worktree: the Python CLI asks endless-go for the content vocabulary
# and writes through its event pipeline, so an installed binary would prove
# something about a different tree.
section "2. Build"

mkdir -p "${TMP}/bin"
if go build -o "${TMP}/bin/endless-go" ./cmd/endless-go >"${TMP}/build.log" 2>&1; then
    report_pass "go build ./cmd/endless-go"
else
    report_fail "go build ./cmd/endless-go" "exit 0" "$(tail -25 "${TMP}/build.log")"
    summary
fi

# ── 3. the vocabulary ───────────────────────────────────────────────────────
section "3. Content names come from the Go enum"

assert_eq "endless-go task-content names: slug and label, display order" \
    "$(printf 'analysis\tAnalysis\nplan\tPlan\noutcome\tOutcome\nreason\tReason\nnotes\tNotes')" \
    "$("${TMP}/bin/endless-go" task-content names)"

# ── fixture: a real config dir, a real git project, a real projects row ─────
CFG_HOME="${TMP}/cfg"
CFG="${CFG_HOME}/endless"
PROJ="${TMP}/proj"
mkdir -p "${CFG}" "${PROJ}/.endless" "${TMP}/home"

printf '{"name": "e1531"}\n' >"${PROJ}/.endless/config.json"
# The machine config the event bridge reads its node id from.
printf '{"node_id": "e153"}\n' >"${CFG}/config.json"
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
config.set_db_context(Path(os.environ["E1531_CFG"]))
from endless import db
db.execute(
    "INSERT INTO projects (id, name, path, status) VALUES (1, 'e1531', ?, 'active')",
    (os.environ["E1531_PROJ"],),
)
PY

E1531_CFG="${CFG}" E1531_PROJ="${PROJ}" PATH="${TMP}/bin:${PATH}" \
    uv run python "${TMP}/seed.py" >"${TMP}/seed.log" 2>&1 \
    || setup_error "could not seed the fixture database: $(tail -5 "${TMP}/seed.log")"

# human — one `endless` invocation as a person at a terminal, against the
# fixture database, with this worktree's endless-go first on PATH.
human() {
    ( cd "${PROJ}" && env -u ENDLESS_SESSION_ID -u CLAUDECODE \
        -u CLAUDE_CODE_ENTRYPOINT -u __CFBundleIdentifier \
        HOME="${TMP}/home" XDG_CONFIG_HOME="${CFG_HOME}" \
        PATH="${TMP}/bin:${PATH}" \
        uv run --project "${WT}" endless "$@" 2>&1 )
}

task_id() { sed -n 's/.*\(E-[0-9][0-9]*\).*/\1/p' <<<"$1" | head -1; }

# new_task prints the new task's id; the caller checks it with need_id, since a
# setup_error inside $(...) would only end the subshell.
new_task() {
    local out id
    out="$(human task add "$1" --description "$2")"
    id="$(task_id "${out}")"
    [[ -n "${id}" ]] || printf '%s\n' "${out}" >"${TMP}/task_add.log"
    printf '%s\n' "${id}"
}

need_id() {
    [[ -n "$1" ]] || setup_error "fixture task add failed: $(cat "${TMP}/task_add.log" 2>/dev/null)"
}

# json_field reads one key of `task show --json`; None prints as "null".
json_field() {
    human task show "$1" --json | uv run python -c \
        'import json,sys; v=json.load(sys.stdin)[sys.argv[1]]; print("null" if v is None else v)' "$2"
}

# sql runs a read against the fixture database.
sql() {
    uv run python - "$1" "${CFG}/endless.db" <<'PY'
import sqlite3, sys
for row in sqlite3.connect(sys.argv[2]).execute(sys.argv[1]):
    print("|".join("" if v is None else str(v) for v in row))
PY
}

# ── 4. the table replaced the columns ───────────────────────────────────────
section "4. task_content replaces the four columns"

assert_eq "tasks has no plan/outcome/analysis/notes column" "" \
    "$(sql "SELECT name FROM pragma_table_info('tasks') WHERE name IN ('plan','outcome','analysis','notes')")"
assert_eq "description stays a column" "description" \
    "$(sql "SELECT name FROM pragma_table_info('tasks') WHERE name = 'description'")"
assert_contains "task_content is one row per (task, name)" "UNIQUE(task_id, name)" \
    "$(sql "SELECT sql FROM sqlite_master WHERE name = 'task_content'")"

# ── 5. write, read, render ──────────────────────────────────────────────────
section "5. Every content name is a flag, a row, a heading and a mirror"

A="$(new_task "Add a widget to the cache layer" "a widget")"
need_id "${A}"
out="$(human task update "${A}" --plan "the plan" --analysis "the analysis" \
    --notes "the notes" --keep-status)"; rc=$?
assert_eq "task update --plan --analysis --notes exits 0" "0" "${rc}"
n="${A#E-}"
assert_eq "one task_content row per name written" \
    "$(printf 'analysis|the analysis\nnotes|the notes\nplan|the plan')" \
    "$(sql "SELECT name, content FROM task_content WHERE task_id = ${n} ORDER BY name")"
assert_eq "--json carries notes" "the notes" "$(json_field "${A}" notes)"
assert_eq "--json carries reason as null when absent" "null" "$(json_field "${A}" reason)"
assert_eq "--json reason_chars is 0 when absent" "0" "$(json_field "${A}" reason_chars)"

shown="$(human task show "${A}" --all-fields --no-color)"
assert_contains "--all-fields renders the Notes heading" "— Notes —" "${shown}"
assert_contains "--all-fields renders the Plan heading" "— Plan —" "${shown}"
hidden="$(human task show "${A}" --no-color)"
assert_contains "a hidden name collapses to a placeholder naming its own flag" \
    "(--notes to display)" "${hidden}"
assert_eq "notes.md mirrored on the project's main checkout" "the notes" \
    "$(cat "${PROJ}/.endless/tasks/e-${n}/notes.md" 2>/dev/null)"

human task update "${A}" --clear notes >/dev/null
assert_eq "--clear deletes the row rather than storing empty content" "" \
    "$(sql "SELECT content FROM task_content WHERE task_id = ${n} AND name = 'notes'")"

# ── 6. outcome and reason are two things ────────────────────────────────────
section "6. outcome is the deliverable, reason is why it ended"

B="$(new_task "Add a thing that gets overtaken" "b")"
need_id "${B}"
human task update "${B}" --outcome "the findings" >/dev/null
out="$(human task update "${B}" --status obsolete --outcome "nobody needs it now")"; rc=$?
assert_eq "--status obsolete --outcome exits 0" "0" "${rc}"
assert_eq "the findings survive the abandonment" "the findings" "$(json_field "${B}" outcome)"
assert_eq "--outcome with an abandonment status is stored as the reason" \
    "nobody needs it now" "$(json_field "${B}" reason)"
assert_contains "the reason renders under its own heading" "— Reason —" \
    "$(human task show "${B}" --reason --no-color)"
assert_eq "reason.md mirrored on the project's main checkout" "nobody needs it now" \
    "$(cat "${PROJ}/.endless/tasks/e-${B#E-}/reason.md" 2>/dev/null)"

C="$(new_task "Add a thing declined later" "c")"
need_id "${C}"
human task update "${C}" --reason "written before the decision" >/dev/null
out="$(human task update "${C}" --status declined)"; rc=$?
assert_eq "a stored reason satisfies the abandonment guard" "0" "${rc}"
assert_eq "…and is the reason the task ends with" "written before the decision" \
    "$(json_field "${C}" reason)"

D="$(new_task "Add a thing with only findings" "d")"
need_id "${D}"
human task update "${D}" --outcome "some findings" >/dev/null
out="$(human task update "${D}" --status obsolete)"; rc=$?
assert_eq "a stored outcome does NOT satisfy the guard" "nonzero" \
    "$([[ ${rc} -ne 0 ]] && echo nonzero || echo zero)"
assert_contains "the refusal asks for a reason and names the flag" "--reason" "${out}"

# ── 7. the repository ───────────────────────────────────────────────────────
section "7. Repository: stale outcome mirrors removed, recognizers derived"

assert_eq "a migrated reason's old outcome.md is gone (E-1013)" "" \
    "$(git ls-files .endless/tasks/e-1013/outcome.md)"
assert_eq "no regex names a content kind literally" "" \
    "$(grep -rn 'plan|outcome|analysis' internal/docmirror/*.go src/endless/doc_mirror.py 2>/dev/null)"

gc="$(just guide-check 2>&1)"; rc=$?
assert_eq "just guide-check exits 0" "0" "${rc}"

summary

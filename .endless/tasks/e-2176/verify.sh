#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2176 and records what was true when E-2176
# landed. Edit it only if you ARE E-2176. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"
#
# E-2176: give open questions a home in their own table.
#
# WHAT LANDED
#   A `task_questions` table — one row per question, scoped to the task, with a
#   series (round), an answer, a status and who answered — written only through
#   two new event kinds, replayed by the projector through the same apply
#   functions the live executor runs, and reached from a top-level
#   `endless question` group (ask / answer / withdraw / reject / supersede /
#   list). Python holds no SQL for it: reads go through `endless-go
#   session-query`, writes through `event emit`.
#
# THE CLAIMS
#   C1  THE TABLE HAS THE PLAN'S SHAPE, in schema.sql and in a numbered
#       migration, and the two agree.
#   C2  SERIES AND IDS ARE ALLOCATED BY THE DATABASE, under the write lock, and
#       written into the ledger line — a caller cannot supply either.
#   C3  THE LIFECYCLE IS open → answered|withdrawn|invalid|superseded, plus
#       answered → superseded, and nothing else.
#   C4  WHO ANSWERED IS RECORDED — `user` or a peer's ES-<n> — and an agent
#       must say which; it cannot default to the user.
#   C5  A REFUSED MOVE NEVER REACHES THE LEDGER.
#   C6  REPLAY REPRODUCES THE LIVE ROWS.
#   C7  THE CLI WORKS END TO END against an isolated database.
#   C8  NO NEW PYTHON SQLITE, and the command is documented in the guide.
#
# ISOLATION: package tests, pytest under the runner's temp HOME, and one CLI
# run against a scratch --db-dir. Nothing touches the main database or ledger.

set -u

WT="$(git rev-parse --show-toplevel)"
cd "${WT}" || setup_error "cannot cd to worktree root ${WT}"

TMP="$(mktemp -d)" || setup_error "cannot create a scratch directory"
trap 'rm -rf "${TMP}"' EXIT

go build -o "${TMP}/endless-go" ./cmd/endless-go 2>"${TMP}/build.txt" \
    || setup_error "cannot build endless-go: $(tail -5 "${TMP}/build.txt")"

run_check() { # name, log, command...
    local name="$1" log="$2"; shift 2
    if "$@" >"${log}" 2>&1; then
        report_pass "${name}"
    else
        report_fail "${name}" "passes" "$(tail -25 "${log}")"
    fi
}

# ---------------------------------------------------------------------------
section "A. This task's own tests (fail-fast)"
# ---------------------------------------------------------------------------
run_check "Go: executor, replay parity, allocation (internal/events)" "${TMP}/a1.txt" \
    go test ./internal/events/ -run 'TaskQuestion|PreAllocateQuestions|ValidKinds_Count' -count=1
run_check "Go: event emit front door — allocation and pre-ledger refusal (internal/eventcmd)" "${TMP}/a2.txt" \
    go test ./internal/eventcmd/ -run 'Question|RebuildDB_ConfirmRefuses' -count=1
run_check "Go: the status vocabulary (internal/questionstatus)" "${TMP}/a3.txt" \
    go test ./internal/questionstatus/ -count=1
run_check "Go: the read path (internal/monitor)" "${TMP}/a4.txt" \
    go test ./internal/monitor/ -run 'TaskQuestions' -count=1
run_check "Go: schema.sql and the migrations agree (internal/schema)" "${TMP}/a5.txt" \
    go test ./internal/schema/ -count=1
run_check "Python: question_cmd, stubbed and end to end" "${TMP}/a6.txt" \
    uv run pytest tests/test_question_cmd.py -q

# ---------------------------------------------------------------------------
section "B. The table (C1)"
# ---------------------------------------------------------------------------
mig="$(ls internal/schema/migrations/*_task_questions.sql 2>/dev/null | head -1)"
if [[ -n "${mig}" ]]; then
    report_pass "a numbered migration adds task_questions (${mig##*/})"
else
    report_fail "a numbered migration adds task_questions" "a file" "none"
fi
for col in "task_id INTEGER NOT NULL" "series INTEGER NOT NULL" "question TEXT NOT NULL" \
           "answer TEXT," "status TEXT NOT NULL DEFAULT 'open'" "answered_by TEXT," \
           "asked_by_session INTEGER,"; do
    assert_contains "schema.sql declares: ${col}" "${col}" \
        "$(sed -n '/CREATE TABLE IF NOT EXISTS task_questions/,/^);/p' internal/schema/schema.sql)"
done

# ---------------------------------------------------------------------------
section "C. The CLI end to end, against a scratch database (C2–C5, C7)"
# ---------------------------------------------------------------------------
DB="${TMP}/db"; ROOT="${TMP}/root"; mkdir -p "${DB}" "${ROOT}"
{ git -C "${ROOT}" init -q && git -C "${ROOT}" config user.email t@e \
    && git -C "${ROOT}" config user.name t \
    && git -C "${ROOT}" commit -q --allow-empty -m init; } || setup_error "cannot init scratch repo"
"${TMP}/endless-go" --db-dir "${DB}" event migrate >/dev/null 2>&1 \
    || setup_error "cannot migrate scratch db"
sqlite3 "${DB}/endless.db" "INSERT INTO projects (id,name,path) VALUES (1,'scratch','${ROOT}');" \
    || setup_error "cannot seed scratch db"

emit() { # kind entity-type entity-id payload
    "${TMP}/endless-go" --db-dir "${DB}" event emit --kind "$1" --project scratch \
        --entity-type "$2" --entity-id "$3" --actor-kind cli --actor-id verify \
        --node-id a7f3 --project-root "${ROOT}" --payload "$4" 2>&1
}
q() { sqlite3 "${DB}/endless.db" "$1"; }

# The task is created through the ledger too, so C6's replay has it.
emit task.created task 0 '{"title":"probe","phase":"now","status":"ready","type":"todo","sort_order":0}' \
    | grep -q '"id":"E-1"' || setup_error "cannot create the scratch task"

out="$(emit task.questions_asked task 1 '{"questions":[{"question":"A?"},{"question":"B?"}]}')"
assert_contains "C2: first round is series 1, ids EQ-1 and EQ-2" '"ids":["EQ-1","EQ-2"]' "${out}"
out="$(emit task.questions_asked task 1 '{"questions":[{"question":"C?"}]}')"
assert_contains "C2: the next round on the task is series 2" '"series":2' "${out}"
out="$(emit task.questions_asked task 1 '{"series":9,"questions":[{"question":"D?"}]}')"
assert_contains "C2: a caller-supplied series is refused" "allocated here" "${out}"
assert_contains "C2: the ledger line carries the allocated numbers" '"series":2,"questions":[{"id":3' \
    "$(cat "${ROOT}"/.endless/db-ledger/*.jsonl)"

emit task_question.resolved task_question 1 '{"status":"answered","answer":"yes","answered_by":"ES-77"}' >/dev/null
assert_eq "C4: a peer answer names the peer" "answered|yes|ES-77" \
    "$(q "SELECT status||'|'||answer||'|'||answered_by FROM task_questions WHERE id=1")"
emit task_question.resolved task_question 1 '{"status":"superseded"}' >/dev/null
assert_eq "C3: answered → superseded keeps the answer" "superseded|yes" \
    "$(q "SELECT status||'|'||answer FROM task_questions WHERE id=1")"
emit task_question.resolved task_question 2 '{"status":"invalid"}' >/dev/null

before="$(cat "${ROOT}"/.endless/db-ledger/*.jsonl | wc -l | tr -d ' ')"
out="$(emit task_question.resolved task_question 2 '{"status":"withdrawn"}')"
assert_contains "C3: invalid is terminal" "cannot become withdrawn" "${out}"
out="$(emit task_question.resolved task_question 3 '{"status":"answered","answer":"x","answered_by":"bob"}')"
assert_contains "C4: answered_by must be user or ES-<n>" "answered_by" "${out}"
out="$(emit task_question.resolved task_question 3 '{"status":"open"}')"
assert_contains "C3: nothing returns to open" "new series" "${out}"
assert_eq "C5: the three refusals added nothing to the ledger" "${before}" \
    "$(cat "${ROOT}"/.endless/db-ledger/*.jsonl | wc -l | tr -d ' ')"

out="$("${TMP}/endless-go" --db-dir "${DB}" event rebuild-db --project-root "${ROOT}" 2>&1)"
assert_not_contains "C6: the ledger replays with no warnings" "warning:" "${out}"

out="$("${TMP}/endless-go" --db-dir "${DB}" session-query task-questions --id 1 2>&1)"
assert_contains "C7: session-query lists the open question" '"question":"C?"' "${out}"
assert_not_contains "C7: resolved questions are not open" '"question":"A?"' "${out}"

# ---------------------------------------------------------------------------
section "D. The Python surface (C4, C7, C8)"
# ---------------------------------------------------------------------------
help="$(uv run endless question --help 2>&1)"
for verb in ask answer withdraw reject supersede list; do
    assert_contains "C7: endless question ${verb}" "${verb}" "${help}"
done
assert_contains "C4: --by is documented as required for agents" "required when an agent" \
    "$(uv run endless question answer --help 2>&1)"
assert_not_contains "C8: question_cmd.py holds no SQL" "sqlite3" "$(cat src/endless/question_cmd.py)"
assert_not_contains "C8: question_cmd.py does not use the legacy db helper" "db.query" \
    "$(cat src/endless/question_cmd.py)"
run_check "C8: CLAUDE.md's Python-SQLite file count still holds" "${TMP}/d1.txt" \
    uv run pytest tests/test_claude_md_rules.py -q
assert_contains "C8: the guide maps \`question\` to the tasks section" "| \`question\` | tasks |" \
    "$(cat docs/guide/index.md)"
assert_contains "C8: the tasks guide has an Open questions section" "## Open questions" \
    "$(cat docs/guide/tasks.md)"

summary

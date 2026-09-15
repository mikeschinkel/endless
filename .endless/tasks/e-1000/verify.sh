#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1000 and records what was true when E-1000
# landed. Edit it only if you ARE E-1000. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1000: tasks.text is tasks.plan.
#
# The field has been called "the plan" in every conversation and every guide
# for as long as it has existed; only the column, the flag and the event key
# still said `text`. This closes that gap end to end — schema, the column the
# Go reads name, the event payload key, the Python CLI flags, and the
# agent-facing strings.
#
# Three answers shaped the work, and each is verified below:
#   1. `--text` is REMOVED as a working flag but RETAINED as a recognised one,
#      erroring with a pointer at `--plan`. Not a hidden alias (both names
#      living at once is what lets an obsolete name survive for years) and not
#      a bare removal (Click's "no such option" tells a session nothing).
#   2. `decisions.text` is NOT renamed — E-1868 rewrites decision storage
#      wholesale, so renaming it here is work that gets thrown away.
#   3. Legacy ledger replay is handled HERE. The db-ledger holds 1,257 events
#      carrying a `text` key and is immutable by design; a projector that did
#      not map them would rebuild those tasks with EMPTY PLANS, silently,
#      because an absent field is indistinguishable from an empty one.
#
# What is verified here:
#   A. Fail-fast: this task's own tests — the Go packages that name the column
#      and the payload key, and the four Python suites that drive the renamed
#      flags through the CLI.
#   B. The schema this tree ships: the column is `plan`, `tasks.text` is gone,
#      and the notice trigger writes a `plan` key.
#   C. The MIGRATION, against a real pre-rename database reconstructed from
#      main's schema — the one thing `just test`/`just test-go` cannot cover,
#      because a fresh test DB is born with the new name and never exercises
#      the rename at all.
#   D. The CLI, through the real binary: `--plan`/`--plan-file` work, `--text`
#      and `--text-file` are refused BY NAME, and the plan-attach promotion
#      still fires.
#   E. The machine surfaces agents read: `plan`/`plan_chars` in `task show
#      --json`, `has_plan` in `session status --json`.
#   F. Answer 2 held: decisions kept their `text`.
#   G. The old spelling is gone from everything the tool speaks.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)"
cd "${WT}" || setup_error "cannot cd to worktree root ${WT}"

TMP="$(mktemp -d)" || setup_error "cannot create a scratch directory"
trap 'rm -rf "${TMP}"' EXIT

# ---------------------------------------------------------------------------
section "A. This task's own tests (fail-fast)"
# ---------------------------------------------------------------------------
# internal/events owns the payload key, the two field maps that accept both
# spellings, and the plan-attach promotion. internal/monitor owns the column
# reads and the notice vocabulary. The two cmd packages own the JSON field
# names. The Python suites are the ones that drive the renamed flags through
# Click — the half no Go test can see.

if out=$(go test ./internal/events ./internal/monitor \
                 ./internal/sessionstatuscmd ./internal/sessionquerycmd \
                 ./internal/schema 2>&1); then
    report_pass "go test: events, monitor, sessionstatuscmd, sessionquerycmd, schema"
else
    report_fail "go test: the packages that name the column or the payload key" \
        "exit 0" "$(printf '%s' "${out}" | tail -30)"
    summary
fi

# These seven are where the renamed surface is actually EXERCISED end to end —
# `--plan-file` attaching through Click, the plan-attach promotion firing, the
# `— Plan —` / `## Plan` sections rendering, the `plan` / `plan_chars` JSON
# keys, and the worktree mirror reading the renamed column. They run against a
# seeded project fixture, which is why they live here rather than as shell
# assertions: this suite's runner gives it a temp HOME, and a home-relative
# project path (E-2011) cannot resolve under one.
if out=$(uv run pytest \
            tests/test_plan_auto_promote.py \
            tests/test_task_show_payload.py \
            tests/test_keep_status.py \
            tests/test_plan_file_to_worktree.py \
            tests/test_task_update_type_analysis.py \
            tests/test_analysis_show.py \
            tests/test_outcome.py \
            tests/test_task_reopen.py -q 2>&1); then
    report_pass "pytest: the suites that drive --plan / --plan-file through Click"
else
    report_fail "pytest: the renamed-flag suites" \
        "exit 0" "$(printf '%s' "${out}" | tail -30)"
    summary
fi

# ---------------------------------------------------------------------------
section "B. The schema this tree ships"
# ---------------------------------------------------------------------------
# Asserted against a database built from THIS tree's schema.sql rather than
# against the file's text, so a CREATE TABLE that says one thing and a trigger
# that says another cannot both pass.

FRESH="${TMP}/fresh.db"
uv run python - "${FRESH}" <<'PY' || setup_error "cannot build a fresh DB from schema.sql"
import sqlite3, sys, pathlib
db = sqlite3.connect(sys.argv[1])
db.executescript(pathlib.Path("internal/schema/schema.sql").read_text())
db.commit()
PY

cols() { uv run python -c "
import sqlite3, sys
db = sqlite3.connect(sys.argv[1])
print(' '.join(r[1] for r in db.execute('PRAGMA table_info(tasks)')))
" "$1"; }

FRESH_COLS="$(cols "${FRESH}")"
assert_contains "a fresh tasks table has a \`plan\` column" " plan " " ${FRESH_COLS} "
assert_not_contains "and no \`text\` column" " text " " ${FRESH_COLS} "

# live_tasks is `SELECT *`, so it serves whatever tasks has — but only if the
# rename actually reached the table rather than being papered over by a view.
LIVE_COLS="$(uv run python -c "
import sqlite3, sys
db = sqlite3.connect(sys.argv[1])
print(' '.join(d[0] for d in db.execute('SELECT * FROM live_tasks LIMIT 0').description))
" "${FRESH}")"
assert_contains "live_tasks serves \`plan\` through to readers" " plan " " ${LIVE_COLS} "

# The trigger's key is a STRING LITERAL, which is exactly what SQLite would NOT
# have rewritten for us on a rename — so it is worth proving rather than reading.
NOTICE_KEYS="$(uv run python - "${FRESH}" <<'PY'
import sqlite3, json, sys
db = sqlite3.connect(sys.argv[1])
db.executescript("""
INSERT INTO projects (id, name, path) VALUES (1, 'p', '/tmp/p');
INSERT INTO tasks (id, project_id, title, status, phase) VALUES (1, 1, 't', 'ready', 'now');
INSERT INTO sessions (id, project_id, state) VALUES (1, 1, 'idle');
INSERT INTO session_tasks (session_id, task_id, created_at, updated_at)
     VALUES (1, 1, '2026-01-01T00:00:00', '2026-01-01T00:00:00');
UPDATE tasks SET plan = '# Plan' WHERE id = 1;
""")
rows = list(db.execute("SELECT changes FROM session_notices"))
print(' '.join(sorted(k for r in rows for k in json.loads(r[0]))))
PY
)" || setup_error "cannot exercise the notice trigger"
assert_eq "the notice trigger names the field \`plan\`" "plan" "${NOTICE_KEYS}"

# ---------------------------------------------------------------------------
section "C. The migration, against a real pre-rename database"
# ---------------------------------------------------------------------------
# This is the section no ordinary test can stand in for. Every test DB is built
# from the CURRENT schema.sql and is therefore born with `plan` — it never has
# a `text` column to rename, so it never executes the ALTER at all. The DB the
# migration will actually meet is main's, so main's schema.sql is what gets
# reconstructed here.
#
# Two properties, and the second is the subtle one: SQLite rewrites COLUMN
# REFERENCES inside a trigger on RENAME COLUMN, but not string literals. A
# migration that renamed the column and left the trigger alone would keep
# emitting `{"text": …}` notices against a column now called plan.

OLD_SCHEMA="${TMP}/old-schema.sql"
if ! git show main:internal/schema/schema.sql > "${OLD_SCHEMA}" 2>/dev/null; then
    report_skip "migration against a pre-rename DB" "main's schema.sql is unreadable"
else
    if grep -qE '^    text TEXT,' "${OLD_SCHEMA}"; then
        LEGACY="${TMP}/legacy.db"
        uv run python - "${LEGACY}" "${OLD_SCHEMA}" <<'PY' \
            || setup_error "cannot build a pre-rename DB from main's schema"
import sqlite3, sys, pathlib
db = sqlite3.connect(sys.argv[1])
db.executescript(pathlib.Path(sys.argv[2]).read_text())
db.executescript("""
INSERT INTO projects (id, name, path) VALUES (1, 'p', '/tmp/p');
INSERT INTO tasks (id, project_id, title, status, phase, text)
     VALUES (1, 1, 't', 'ready', 'now', '# Plan written before the rename');
INSERT INTO sessions (id, project_id, state) VALUES (1, 1, 'idle');
INSERT INTO session_tasks (session_id, task_id, created_at, updated_at)
     VALUES (1, 1, '2026-01-01T00:00:00', '2026-01-01T00:00:00');
""")
db.commit()
PY

        # ENDLESS_CHANGE_DB is the seam `endless db apply-change` itself uses to
        # point a change script at a resolved path, so this is the real entry
        # point and not a test-only door.
        CHANGE="internal/schema/changes/e-1000-rename-tasks-text-to-plan.go"
        [[ -f "${CHANGE}" ]] || setup_error "missing change script ${CHANGE}"

        if out=$(ENDLESS_CHANGE_DB="${LEGACY}" go run "${CHANGE}" 2>&1); then
            report_pass "the change applies to a pre-rename database"
        else
            report_fail "the change applies to a pre-rename database" \
                "exit 0" "$(printf '%s' "${out}" | tail -20)"
        fi

        LEGACY_COLS="$(cols "${LEGACY}")"
        assert_contains "the column is now \`plan\`" " plan " " ${LEGACY_COLS} "
        assert_not_contains "and \`text\` is gone from tasks" " text " " ${LEGACY_COLS} "

        # RENAME COLUMN carries the values with the column. A migration that
        # dropped and re-added would pass the two checks above and lose 1,250
        # plans; this is the one that notices.
        assert_eq "the existing plan content moved with the column" \
            "# Plan written before the rename" \
            "$(uv run python -c "
import sqlite3, sys
print(sqlite3.connect(sys.argv[1]).execute('SELECT plan FROM tasks WHERE id=1').fetchone()[0])
" "${LEGACY}")"

        MIGRATED_KEYS="$(uv run python - "${LEGACY}" <<'PY'
import sqlite3, json, sys
db = sqlite3.connect(sys.argv[1])
db.execute("DELETE FROM session_notices")
db.execute("UPDATE tasks SET plan = '# Plan, edited after the rename' WHERE id = 1")
rows = list(db.execute("SELECT changes FROM session_notices"))
print(' '.join(sorted(k for r in rows for k in json.loads(r[0]))))
PY
)" || setup_error "cannot exercise the migrated trigger"
        assert_eq "the re-created trigger names the field \`plan\`, literal and all" \
            "plan" "${MIGRATED_KEYS}"

        # Idempotent twice over: the _schema_version marker stops the second
        # run, and the column probe would stop it even without one.
        if out=$(ENDLESS_CHANGE_DB="${LEGACY}" go run "${CHANGE}" 2>&1); then
            report_pass "re-running the change on the same DB is a no-op"
        else
            report_fail "re-running the change on the same DB is a no-op" \
                "exit 0" "$(printf '%s' "${out}" | tail -20)"
        fi

        # And a DB that never had the old name at all — every fresh install,
        # every sandbox, every test DB — must be a no-op, not a hard error.
        BORN_NEW="${TMP}/born-new.db"
        cp "${FRESH}" "${BORN_NEW}"
        if out=$(ENDLESS_CHANGE_DB="${BORN_NEW}" go run "${CHANGE}" 2>&1); then
            report_pass "the change is a no-op on a DB born with \`plan\`"
        else
            report_fail "the change is a no-op on a DB born with \`plan\`" \
                "exit 0" "$(printf '%s' "${out}" | tail -20)"
        fi
    else
        # main already carries the rename: the migration has landed, and
        # reconstructing a pre-rename DB from it is no longer possible.
        report_skip "migration against a pre-rename DB" \
            "main already has tasks.plan — this task has landed"
    fi
fi

# ---------------------------------------------------------------------------
section "D. Answer 1: the retired flags refuse, by name"
# ---------------------------------------------------------------------------
# This is the half of the rename that only a real command can show. A retired
# flag has to fail in a way that TELLS a session where the field went, because
# muscle memory will keep reaching for `--text` long after the column stopped
# being called that — and Click's own "No such option: --text" says nothing.
#
# Every check here resolves at PARSE time, before any project or database is
# touched, which is what makes them runnable under this suite's temp HOME.

# retired_refuses VERB... -- FLAG... -- EXPECTED
retired_refuses() {
    local -a verb=() flag=()
    local want="" seen=0
    for a in "$@"; do
        if [[ "${a}" == "--" ]]; then seen=$((seen + 1)); continue; fi
        case "${seen}" in
            0) verb+=("${a}") ;;
            1) flag+=("${a}") ;;
            *) want="${a}" ;;
        esac
    done
    local out rc
    out=$(uv run endless "${verb[@]}" "${flag[@]}" 2>&1); rc=$?
    if (( rc != 0 )) && [[ "${out}" == *"renamed to ${want}"* ]]; then
        report_pass "\`endless ${verb[*]} ${flag[0]}\` is refused, naming ${want}"
    else
        report_fail "\`endless ${verb[*]} ${flag[0]}\` is refused, naming ${want}" \
            "exit != 0 and output naming ${want}" "exit=${rc} | ${out}"
    fi
}

retired_refuses task show E-1 -- --text                    -- --plan
retired_refuses task search q -- --text                    -- --plan
retired_refuses epic show E-1 -- --text                    -- --plan
retired_refuses task add T    -- --text inline             -- --plan
retired_refuses task add T    -- --text-file /dev/null       -- --plan-file
retired_refuses task update E-1 -- --text inline           -- --plan
retired_refuses task update E-1 -- --text-file /dev/null   -- --plan-file
retired_refuses epic add T    -- --text inline             -- --plan
retired_refuses epic update E-1 -- --text-file /dev/null   -- --plan-file

# A retired flag is NOT advertised — nothing should learn a dead name — while
# the live one is. Both halves matter: a hidden alias that still worked is the
# outcome this design rejected.
for verb in "task add" "task update" "epic add" "epic update"; do
    # shellcheck disable=SC2086
    help_out="$(uv run endless ${verb} --help 2>&1)"
    assert_contains "\`${verb} --help\` offers --plan" "--plan " "${help_out}"
    assert_contains "\`${verb} --help\` offers --plan-file" "--plan-file " "${help_out}"
    assert_not_contains "\`${verb} --help\` does not advertise --text" "--text" "${help_out}"
done

for verb in "task show" "task search" "epic show"; do
    # shellcheck disable=SC2086
    help_out="$(uv run endless ${verb} --help 2>&1)"
    assert_contains "\`${verb} --help\` offers --plan" "--plan" "${help_out}"
    assert_not_contains "\`${verb} --help\` does not advertise --text" "--text" "${help_out}"
done

# `--clear` names the field it erases, so its vocabulary moved with the column.
CLEAR_HELP="$(uv run endless task update --help 2>&1)"
assert_contains "--clear's help names \`plan\`, not \`text\`" \
    "description/plan/analysis/outcome" "${CLEAR_HELP}"
CLEAR_OUT="$(uv run endless task update E-1 --clear text 2>&1)"; CLEAR_RC=$?
if (( CLEAR_RC != 0 )) && [[ "${CLEAR_OUT}" == *"plan"* ]]; then
    report_pass "\`--clear text\` is refused and the choices name \`plan\`"
else
    report_fail "\`--clear text\` is refused and the choices name \`plan\`" \
        "exit != 0 and output naming plan" "exit=${CLEAR_RC} | ${CLEAR_OUT}"
fi

# `lesson write --text` is a DIFFERENT field and was deliberately left alone
# (it stores a lesson, not a plan). If the sweep had been done by search and
# replace, this is what it would have broken.
LESSON_HELP="$(uv run endless lesson write --help 2>&1)"
assert_contains "\`lesson write\` keeps its own --text" "--text" "${LESSON_HELP}"

# ---------------------------------------------------------------------------
section "E. The Go-side machine surface"
# ---------------------------------------------------------------------------
# Built from this tree rather than taken from bin/, so the answers come from
# what is committed here and not from whatever was last installed.

BIN="${TMP}/endless-go"
go build -o "${BIN}" ./cmd/endless-go \
    || setup_error "cannot build cmd/endless-go from this tree"

SQ_HELP="$("${BIN}" session-query 2>&1 || true)"
[[ -n "${SQ_HELP}" ]] || setup_error "session-query printed no usage"

assert_contains "triage-context advertises \`has_plan\`" "has_plan" "${SQ_HELP}"
assert_not_contains "and no longer \`has_text\`" "has_text" "${SQ_HELP}"
assert_contains "the plan-reading verb is \`task-plan\`" "task-plan" "${SQ_HELP}"
assert_not_contains "\`task-text\` is gone" "task-text" "${SQ_HELP}"
assert_contains "task-field's whitelist names \`plan\`" \
    "--name <plan|outcome|analysis>" "${SQ_HELP}"

MAIN_HELP="$("${BIN}" 2>&1 || true)"
assert_contains "the top-level usage names task-plan" "task-plan" "${MAIN_HELP}"
assert_not_contains "and not task-text" "task-text" "${MAIN_HELP}"

# ---------------------------------------------------------------------------
section "F. Answer 2: decisions kept their \`text\`"
# ---------------------------------------------------------------------------
# A second column of the same name lives on `decisions`. E-1868 rewrites
# decision storage wholesale, so renaming it here is work that gets thrown
# away — recorded as a decision, not skipped by accident. Asserted so a later
# sweep does not "finish the job" and collide with E-1868.

DEC_COLS="$(uv run python -c "
import sqlite3, sys
db = sqlite3.connect(sys.argv[1])
print(' '.join(r[1] for r in db.execute('PRAGMA table_info(decisions)')))
" "${FRESH}")"
assert_contains "decisions.text is deliberately untouched" " text " " ${DEC_COLS} "
assert_contains "decision show still heads its body '## Text'" \
    '## Text' "$(cat src/endless/decision_cmd.py)"

# ---------------------------------------------------------------------------
section "G. The old spelling is gone from what the tool speaks"
# ---------------------------------------------------------------------------
# A sweep rather than a file list, because the point of the change is that
# nothing SAYS it any more.
#
# The sweep is scoped to the tool's own source and docs — the things endless
# SPEAKS. Everything under .endless/ is data, not speech: the db-ledger and
# LESSONS.md are append-only history, the plans/analyses/outcomes/decisions
# mirrors are committed prose from before the rename, and the landed suites in
# .endless/tasks/ are pinned to the tree of their own land. Retrofitting any of
# them would be rewriting a record, not finishing a rename.
#
# Two exclusions inside the source tree, each for a reason:
#   - internal/schema/changes/* name the columns of THEIR moment, this task's
#     change file included — it has to say `text` in order to rename it.
#   - internal/events/plan_rename_test.go documents the legacy key it exists to
#     pin. A change is allowed to name the thing it removed.

ROOTS=(cmd internal src docs README.md AGENTS.md justfile)

sweep() {
    grep -rIl -- "$1" "${ROOTS[@]}" 2>/dev/null \
      | sed 's|^\./||' \
      | grep -v '^internal/schema/changes/' \
      | grep -v '^internal/events/plan_rename_test\.go$' \
      | sort
}

assert_eq "nothing in the source tree still writes \`tasks.text\`" "" "$(sweep 'tasks\.text')"
assert_eq "nothing still reports \`has_text\`" "" "$(sweep 'has_text')"
assert_eq "nothing still reports \`text_chars\`" "" "$(sweep 'text_chars')"
assert_eq "no \`session-query task-text\` verb remains" "" "$(sweep 'task-text')"
assert_eq "no Go code names monitor.TaskText" "" "$(sweep 'TaskText')"

# `--text` survives in exactly two files, both legitimately: lesson_cmd.py and
# the `lesson write` option pair in cli.py (a lesson is not a plan), and the
# retired-flag machinery in cli.py that refuses it everywhere else. Section D
# proves behaviourally that no task or epic surface still advertises it.
assert_eq "\`--text\` survives only where it still means something" \
    "src/endless/cli.py
src/endless/lesson_cmd.py" \
    "$(sweep '\-\-text')"

# In cli.py it appears as exactly one live pair — lesson write's — plus the
# retirements. The counts are the assertion: a live `--text` re-added to a task
# verb moves the first number.
assert_eq "cli.py declares exactly one live --text pair (lesson write's)" \
    "2" "$(grep -c '@click.option("--text' src/endless/cli.py || true)"
assert_eq "cli.py retires --text on the three display/search surfaces" \
    "3" "$(grep -c 'retired_option("--text"' src/endless/cli.py || true)"
assert_eq "and retires the --text/--text-file pair on the four content verbs" \
    "4" "$(grep -c 'retired_content_options("text", "plan")' src/endless/cli.py || true)"

summary

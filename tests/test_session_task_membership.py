"""Tests for `session touch` (and its aliases `touch` / `session task add`) and
`session task remove` (E-1696, E-2173, E-2291).

Two verbs and an alias that say explicitly what a session's task list holds.
Capture is otherwise automatic — the Go executors record a row for every task a
session claims, files or edits — and these cover what automation cannot reach:
scope entry with no edit at all (`touch`, and since E-2291 its alias `session
task add`), and a capture that should not have happened (`remove`).

These run through the real CLI and the real Go executors, so they pin the
whole path: id normalization, the upgrade-only relation ladder, the goal
refusal, and the hide cleared alongside a removal.
"""

import pytest
from click.testing import CliRunner

from endless import db
from endless.cli import main


SESSION_ID = 9101


@pytest.fixture(autouse=True)
def _project(seeded_project_at_cwd):
    """Every test here emits events, so cwd must resolve to a registered
    project — _resolve_project(None) is on the emit path."""
    return seeded_project_at_cwd


def _seed(task_ids=(500, 501)):
    """Some tasks and one live session against the fixture's project."""
    project_id = db.query("SELECT id FROM projects")[0]["id"]
    for task_id in task_ids:
        db.execute(
            "INSERT INTO tasks (id, project_id, title, status, phase) "
            "VALUES (?, ?, ?, 'ready', 'now')",
            (task_id, project_id, f"task {task_id}"),
        )
    db.execute(
        "INSERT INTO sessions (id, session_id, project_id, state) "
        "VALUES (?, ?, ?, 'working')",
        (SESSION_ID, f"uuid-{SESSION_ID}", project_id),
    )


def _relation(task_id, session_id=SESSION_ID):
    """The relation slug for one (session, task) pair, or None if no row."""
    rows = db.query(
        "SELECT r.slug FROM session_tasks st "
        "LEFT JOIN session_task_relations r ON r.id = st.relation_id "
        "WHERE st.session_id = ? AND st.task_id = ?",
        (session_id, task_id),
    )
    return rows[0]["slug"] if rows else None


def _run(*args):
    return CliRunner().invoke(
        main, list(args) + ["--session-id", str(SESSION_ID)]
    )


# --- add --------------------------------------------------------------------


def test_add_enrolls_an_untouched_task_as_touched():
    """The case no automatic capture can reach: nothing has happened to this
    task, so only an explicit verb can put it on the list."""
    _seed()

    result = _run("session", "task", "add", "E-500")
    assert result.exit_code == 0, result.output
    assert _relation(500) == "touched"


def test_add_is_an_alias_of_touch():
    """E-2291: `session task add` emits exactly what `touch` emits — the same
    event kind, the same payload — and nothing emits session_tasks.queued."""
    from unittest.mock import patch

    from endless import session_task_cmd

    _seed()
    calls = []
    with patch.object(session_task_cmd.event_bridge, "emit_event",
                      side_effect=lambda **kw: calls.append(kw) or {}):
        for verb in (("session", "task", "add"), ("touch",)):
            result = _run(*verb, "E-500")
            assert result.exit_code == 0, result.output
    assert [c["kind"] for c in calls] == ["session_tasks.touched"] * 2
    assert calls[0]["payload"] == calls[1]["payload"]


def test_add_accepts_several_ids_and_collapses_repeats():
    """Naming the same task twice is the same request, not a contradiction."""
    _seed()

    result = _run("session", "task", "add", "E-500", "E-501", "E-500")
    assert result.exit_code == 0, result.output
    assert _relation(500) == "touched"
    assert _relation(501) == "touched"


def test_add_promotes_a_weaker_existing_relation():
    """A task this session merely touched is strengthened, not duplicated."""
    _seed()
    db.execute(
        "INSERT INTO session_tasks "
        "(session_id, task_id, relation_id, created_at, updated_at) "
        "VALUES (?, 500, 3, '2026-08-21T00:00:00', '2026-08-21T00:00:00')",
        (SESSION_ID,),
    )

    result = _run("session", "task", "add", "E-500")
    assert result.exit_code == 0, result.output
    assert _relation(500) == "touched"
    rows = db.query(
        "SELECT count(*) AS n FROM session_tasks "
        "WHERE session_id = ? AND task_id = 500",
        (SESSION_ID,),
    )
    assert rows[0]["n"] == 1


def test_add_leaves_the_session_claim_alone():
    """Adding your own claimed task is redundant, not wrong — the ladder
    refuses the downgrade and the CLI reports it rather than erroring."""
    _seed()
    db.execute(
        "INSERT INTO session_tasks "
        "(session_id, task_id, relation_id, created_at, updated_at) "
        "VALUES (?, 500, 1, '2026-08-21T00:00:00', '2026-08-21T00:00:00')",
        (SESSION_ID,),
    )

    result = _run("session", "task", "add", "E-500")
    assert result.exit_code == 0, result.output
    assert _relation(500) == "claimed"
    assert "already in this session as Claimed" in result.output


def test_add_rejects_an_unknown_task():
    """A typo fails the call instead of silently queuing nothing."""
    _seed()

    result = _run("session", "task", "add", "E-999")
    assert result.exit_code != 0
    assert _relation(999) is None


def test_add_rejects_a_malformed_id():
    """Validated Python-side, before any event is emitted."""
    _seed()

    result = _run("session", "task", "add", "banana")
    assert result.exit_code != 0
    assert "E-NNN" in result.output


def test_add_requires_at_least_one_id():
    _seed()

    result = _run("session", "task", "add")
    assert result.exit_code != 0


# --- touch (E-2173) ---------------------------------------------------------


def _task_row(task_id):
    """Every task field a mutation would move, so a test can assert the whole
    row went unchanged rather than naming one column and missing the next."""
    return db.query(
        "SELECT title, status, phase, updated_at FROM tasks WHERE id = ?",
        (task_id,),
    )[0]


def test_touch_enrolls_a_task_as_touched():
    """The verb's whole job: scope entry, at relation `touched` (E-2291;
    `revisited` before it, which now means only that the session edited it)."""
    _seed()

    result = _run("touch", "E-500")
    assert result.exit_code == 0, result.output
    assert _relation(500) == "touched"


def test_touch_changes_nothing_about_the_task():
    """The point of the verb. The workaround it replaces rewrote `phase` to buy
    the same display effect, so a test that only checked session_tasks would
    pass for the bug as readily as for the fix."""
    _seed()
    before = _task_row(500)

    result = _run("touch", "E-500")
    assert result.exit_code == 0, result.output
    assert _task_row(500) == before


def test_touch_accepts_several_ids_and_collapses_repeats():
    _seed()

    result = _run("touch", "E-500", "E-501", "E-500")
    assert result.exit_code == 0, result.output
    assert _relation(500) == "touched"
    assert _relation(501) == "touched"


def test_touch_leaves_a_stronger_relation_alone_and_says_which():
    """Claimed and bound outrank `touched`. The report has to name the
    survivor — "already in this session" alone would leave the reader unable to
    tell a claim from a bind."""
    _seed()
    db.execute(
        "INSERT INTO session_tasks "
        "(session_id, task_id, relation_id, created_at, updated_at) "
        "VALUES (?, 500, 1, '2026-08-21T00:00:00', '2026-08-21T00:00:00')",
        (SESSION_ID,),
    )

    result = _run("touch", "E-500")
    assert result.exit_code == 0, result.output
    assert _relation(500) == "claimed"
    assert "already in this session as Claimed" in result.output


def test_touch_leaves_the_session_bound_task_bound():
    """E-2291 P14: touching the task this session bound leaves it `bound` and
    says so, without an error."""
    _seed()
    db.execute(
        "INSERT INTO session_tasks "
        "(session_id, task_id, relation_id, created_at, updated_at) "
        "VALUES (?, 500, 6, '2026-08-21T00:00:00', '2026-08-21T00:00:00')",
        (SESSION_ID,),
    )

    result = _run("touch", "E-500")
    assert result.exit_code == 0, result.output
    assert _relation(500) == "bound"
    assert "already in this session as Bound" in result.output


def test_touch_rejects_an_unknown_task():
    """A typo fails the call rather than silently enrolling nothing."""
    _seed()

    result = _run("touch", "E-999")
    assert result.exit_code != 0
    assert _relation(999) is None


def test_touch_rejects_a_malformed_id_naming_the_verb():
    """Validated Python-side, before any event is emitted — and the refusal
    names `touch`, not the `session task` group it shares a module with."""
    _seed()

    result = _run("touch", "banana")
    assert result.exit_code != 0
    assert "touch: malformed task id" in result.output


def test_touch_requires_at_least_one_id():
    _seed()

    result = _run("touch")
    assert result.exit_code != 0


@pytest.mark.parametrize("verb", [
    ("session", "touch"), ("touch",), ("session", "task", "add"),
])
def test_all_three_names_record_touched(verb):
    """E-2291 P24: `session touch` is the name; `touch` and `session task add`
    are aliases of it, and all three record the same relation."""
    _seed()

    result = _run(*verb, "E-500")
    assert result.exit_code == 0, result.output
    assert _relation(500) == "touched"


def test_session_touch_help_is_the_users_command():
    """E-2291 P23: the help describes it as the user's command, and the two
    aliases carry the same help."""
    helps = [
        CliRunner().invoke(main, [*verb, "--help"]).output
        for verb in (("session", "touch"), ("touch",), ("session", "task", "add"))
    ]
    assert "A command for you, the user" in helps[0]
    assert helps[1].split("Usage:")[1].split("\n", 1)[1] == \
        helps[0].split("Usage:")[1].split("\n", 1)[1]
    assert helps[2].split("Usage:")[1].split("\n", 1)[1] == \
        helps[0].split("Usage:")[1].split("\n", 1)[1]


def test_touch_is_reachable_at_the_top_level():
    """Top-level, not under `session` or `task` (E-2173): parking it under a
    parent would only schedule a rename as subcommands move up."""
    assert "touch" in main.commands
    assert "touch" not in getattr(main.commands["task"], "commands", {})


# --- remove -----------------------------------------------------------------


def test_remove_drops_the_association():
    _seed()
    _run("session", "task", "add", "E-500")
    assert _relation(500) == "touched"

    result = _run("session", "task", "remove", "E-500")
    assert result.exit_code == 0, result.output
    assert _relation(500) is None


def test_remove_clears_a_hide_on_the_same_pair():
    """The two tables are independent, so the hide would otherwise outlive the
    membership — and a re-captured task would return silently suppressed."""
    _seed()
    _run("session", "task", "add", "E-500")
    db.execute(
        "INSERT INTO session_hidden_tasks (session_id, task_id, hidden_at) "
        "VALUES (?, 500, '2026-08-21T00:00:00')",
        (SESSION_ID,),
    )

    result = _run("session", "task", "remove", "E-500")
    assert result.exit_code == 0, result.output
    assert db.query(
        "SELECT 1 FROM session_hidden_tasks "
        "WHERE session_id = ? AND task_id = 500",
        (SESSION_ID,),
    ) == []


def test_remove_refuses_the_session_claim():
    """Not a false positive by construction: the session claimed that task."""
    _seed()
    db.execute(
        "INSERT INTO session_tasks "
        "(session_id, task_id, relation_id, created_at, updated_at) "
        "VALUES (?, 500, 1, '2026-08-21T00:00:00', '2026-08-21T00:00:00')",
        (SESSION_ID,),
    )

    result = _run("session", "task", "remove", "E-500")
    assert result.exit_code != 0
    assert _relation(500) == "claimed"


def test_remove_refuses_the_session_bound_task():
    """E-2291 P13: a bound task is the session's own as a claimed one is, so
    remove refuses it the same way and names `session hide --task`."""
    _seed()
    db.execute(
        "INSERT INTO session_tasks "
        "(session_id, task_id, relation_id, created_at, updated_at) "
        "VALUES (?, 500, 6, '2026-08-21T00:00:00', '2026-08-21T00:00:00')",
        (SESSION_ID,),
    )

    result = _run("session", "task", "remove", "E-500")
    assert result.exit_code != 0
    assert "session hide --task" in result.output
    assert _relation(500) == "bound"


def test_remove_of_an_untouched_task_is_a_reported_no_op():
    """Matches `session unhide --task`: not an error, but never silent."""
    _seed()

    result = _run("session", "task", "remove", "E-500")
    assert result.exit_code == 0, result.output
    assert "not in this session" in result.output


def test_remove_is_scoped_to_one_session():
    """The invariant a session-resolution regression would break: removing in
    one session leaves another session's row for the same task untouched."""
    _seed()
    other = 9102
    project_id = db.query("SELECT id FROM projects")[0]["id"]
    db.execute(
        "INSERT INTO sessions (id, session_id, project_id, state) "
        "VALUES (?, ?, ?, 'working')",
        (other, f"uuid-{other}", project_id),
    )
    for session_id in (SESSION_ID, other):
        db.execute(
            "INSERT INTO session_tasks "
            "(session_id, task_id, relation_id, created_at, updated_at) "
            "VALUES (?, 500, 3, '2026-08-21T00:00:00', '2026-08-21T00:00:00')",
            (session_id,),
        )

    result = _run("session", "task", "remove", "E-500")
    assert result.exit_code == 0, result.output
    assert _relation(500) is None
    assert _relation(500, session_id=other) == "revisited"

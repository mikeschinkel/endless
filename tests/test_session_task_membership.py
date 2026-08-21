"""Tests for `session task add` / `session task remove` (E-1696).

Two verbs that correct what a session's task list holds. Capture is otherwise
automatic — the Go executors record a row for every task a session claims,
files or edits — and these cover the two cases automation cannot reach: work
decided on but not yet touched (`add`), and a capture that should not have
happened (`remove`).

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
        "INSERT INTO sessions (id, session_id, project_id, state, kind_id) "
        "VALUES (?, ?, ?, 'working', 1)",
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


def test_add_enrolls_an_untouched_task_as_queued():
    """The case no automatic capture can reach: nothing has happened to this
    task, so only an explicit verb can put it on the list."""
    _seed()

    result = _run("session", "task", "add", "E-500")
    assert result.exit_code == 0, result.output
    assert _relation(500) == "queued"


def test_add_accepts_several_ids_and_collapses_repeats():
    """Naming the same task twice is the same request, not a contradiction."""
    _seed()

    result = _run("session", "task", "add", "E-500", "E-501", "E-500")
    assert result.exit_code == 0, result.output
    assert _relation(500) == "queued"
    assert _relation(501) == "queued"


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
    assert _relation(500) == "queued"
    rows = db.query(
        "SELECT count(*) AS n FROM session_tasks "
        "WHERE session_id = ? AND task_id = 500",
        (SESSION_ID,),
    )
    assert rows[0]["n"] == 1


def test_add_leaves_the_session_goal_alone():
    """Queuing your own claimed task is redundant, not wrong — the ladder
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
    assert _relation(500) == "goal"
    assert "goal" in result.output


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


# --- remove -----------------------------------------------------------------


def test_remove_drops_the_association():
    _seed()
    _run("session", "task", "add", "E-500")
    assert _relation(500) == "queued"

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


def test_remove_refuses_the_session_goal():
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
    assert _relation(500) == "goal"


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
        "INSERT INTO sessions (id, session_id, project_id, state, kind_id) "
        "VALUES (?, ?, ?, 'working', 1)",
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

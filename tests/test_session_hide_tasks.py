"""Tests for per-session task hiding — `session hide/unhide --task` (E-1914).

Hiding is display-scoped and belongs to the (session, task) PAIR: it suppresses
a task row from ONE session's `session status` view and touches nothing else —
not the task, not another session's view, not `task next` or blocking. These
tests pin the Python command surface: session/task resolution, the no-op
contract, the untouched no-flag path, and — the one that matters most — that a
hide in one session leaves every other session's hidden set empty.
"""

import pytest
from click.testing import CliRunner

from endless import db
from endless.cli import main


def _seed(session_ids=(9001,), task_ids=(500, 501)):
    """A project, some tasks, and some sessions, all in the isolated test DB."""
    db.execute(
        "INSERT INTO projects (id, name, path) VALUES (1, 'probe', '/probe')"
    )
    for task_id in task_ids:
        db.execute(
            "INSERT INTO tasks (id, project_id, title, status, phase) "
            "VALUES (?, 1, ?, 'ready', 'now')",
            (task_id, f"task {task_id}"),
        )
    for session_id in session_ids:
        db.execute(
            "INSERT INTO sessions (id, session_id, project_id, state) "
            "VALUES (?, ?, 1, 'working')",
            (session_id, f"uuid-{session_id}"),
        )


def _hidden(session_id):
    return {
        r["task_id"]
        for r in db.query(
            "SELECT task_id FROM session_hidden_tasks WHERE session_id = ?",
            (session_id,),
        )
    }


def _run(*args):
    return CliRunner().invoke(main, list(args))


# --- the core per-session guarantee ----------------------------------------


def test_hide_is_scoped_to_one_session():
    """The invariant a global-flag regression would break and nothing else would
    catch: two sessions, one task, one hide — the other session is untouched."""
    _seed(session_ids=(9001, 9002))

    result = _run("session", "hide", "ES-9001", "--task", "E-500")
    assert result.exit_code == 0, result.output

    assert _hidden(9001) == {500}
    assert _hidden(9002) == set()


def test_hide_leaves_the_task_itself_alone():
    """Display-scoped means display-scoped: nothing about the task changes."""
    _seed()
    before = dict(db.query("SELECT * FROM tasks WHERE id = 500")[0])

    _run("session", "hide", "ES-9001", "--task", "E-500")

    assert dict(db.query("SELECT * FROM tasks WHERE id = 500")[0]) == before


def test_hide_does_not_fabricate_a_session_tasks_touch():
    """ED-1545's reason for a separate table: hiding a task must never make
    `task show` report a touch that never happened."""
    _seed()

    _run("session", "hide", "ES-9001", "--task", "E-500")

    assert db.query("SELECT * FROM session_tasks") == []


# --- resolution -------------------------------------------------------------


@pytest.mark.no_session_stub
def test_session_defaults_to_the_current_session(monkeypatch):
    _seed()
    monkeypatch.setenv("ENDLESS_SESSION_ID", "9001")

    result = _run("session", "hide", "--task", "E-500")

    assert result.exit_code == 0, result.output
    assert _hidden(9001) == {500}


@pytest.mark.no_session_stub
def test_no_current_session_is_a_clear_error(monkeypatch):
    """There is no sane fallback: a hide with nobody to own it would be global."""
    _seed()
    monkeypatch.delenv("ENDLESS_SESSION_ID", raising=False)

    result = _run("session", "hide", "--task", "E-500")

    assert result.exit_code != 0
    assert "per-session" in result.output
    assert _hidden(9001) == set()


@pytest.mark.parametrize("ref", ["ES-9001", "es-9001", "9001"])
def test_session_ref_forms(ref):
    _seed()
    assert _run("session", "hide", ref, "--task", "E-500").exit_code == 0
    assert _hidden(9001) == {500}


@pytest.mark.parametrize("ref", ["E-500", "e-500", "500"])
def test_task_ref_forms(ref):
    _seed()
    assert _run("session", "hide", "ES-9001", "--task", ref).exit_code == 0
    assert _hidden(9001) == {500}


def test_repeatable_task_flag():
    _seed()

    result = _run("session", "hide", "ES-9001", "--task", "E-500", "--task", "E-501")

    assert result.exit_code == 0, result.output
    assert _hidden(9001) == {500, 501}


def test_unknown_task_rejects_the_whole_command():
    """Validated up front, as a set: a typo in the second id must not leave the
    first one applied."""
    _seed()

    result = _run("session", "hide", "ES-9001", "--task", "E-500", "--task", "E-99999")

    assert result.exit_code != 0
    assert "E-99999" in result.output
    assert _hidden(9001) == set()


def test_more_than_one_session_with_task_is_refused():
    _seed(session_ids=(9001, 9002))

    result = _run("session", "hide", "ES-9001", "ES-9002", "--task", "E-500")

    assert result.exit_code != 0
    assert "ONE session" in result.output
    assert _hidden(9001) == set() and _hidden(9002) == set()


# --- no-op contract ---------------------------------------------------------


def test_hiding_twice_is_a_no_op():
    _seed()
    _run("session", "hide", "ES-9001", "--task", "E-500")
    first = db.query(
        "SELECT hidden_at FROM session_hidden_tasks WHERE session_id = 9001"
    )[0]["hidden_at"]

    result = _run("session", "hide", "ES-9001", "--task", "E-500")

    assert result.exit_code == 0, result.output
    assert "already hidden" in result.output
    assert _hidden(9001) == {500}
    # hidden_at is not restamped: --only-hidden orders by how long something has
    # been suppressed, so the ORIGINAL time is the meaningful one.
    assert db.query(
        "SELECT hidden_at FROM session_hidden_tasks WHERE session_id = 9001"
    )[0]["hidden_at"] == first


def test_unhide_restores_and_is_idempotent():
    _seed()
    _run("session", "hide", "ES-9001", "--task", "E-500")

    assert _run("session", "unhide", "ES-9001", "--task", "E-500").exit_code == 0
    assert _hidden(9001) == set()

    result = _run("session", "unhide", "ES-9001", "--task", "E-500")
    assert result.exit_code == 0, result.output
    assert "not hidden" in result.output


# --- the no-flag path is untouched -----------------------------------------


def test_bare_hide_still_hides_the_session():
    _seed()

    result = _run("session", "hide", "9001")

    assert result.exit_code == 0, result.output
    assert db.query("SELECT hidden FROM sessions WHERE id = 9001")[0]["hidden"] == 1
    assert _hidden(9001) == set()


def test_bare_unhide_still_unhides_the_session():
    _seed()
    _run("session", "hide", "9001")

    result = _run("session", "unhide", "9001")

    assert result.exit_code == 0, result.output
    assert db.query("SELECT hidden FROM sessions WHERE id = 9001")[0]["hidden"] == 0


@pytest.mark.parametrize("verb", ["hide", "unhide"])
def test_no_sessions_and_no_task_is_an_error(verb):
    _seed()
    result = _run("session", verb)
    assert result.exit_code != 0
    assert "--task" in result.output

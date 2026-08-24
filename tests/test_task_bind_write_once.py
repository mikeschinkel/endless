"""`task bind` is first-set-only (E-1968, per ED-1560).

`sessions.active_task_id` is write-once: set at claim, never cleared and never
repointed. Bind may fill a session that holds no task; it may not move a session
from one task to another. E-1969 enforces this with a BEFORE UPDATE trigger, but
a SQLite abort is not an answer a user can act on — so the refusal lives here,
in the verb, and names the route.
"""

from unittest.mock import patch

import click
import pytest

from endless import db


def _insert_session(*, pk, session_id, project_id, active_task_id=None):
    db.execute(
        "INSERT INTO sessions (id, session_id, project_id, platform, state, "
        "started_at, active_task_id) "
        "VALUES (?, ?, ?, 'claude', 'working', '2026-08-15T00:00:00', ?)",
        (pk, session_id, project_id, active_task_id),
    )


def _insert_task(*, pk, project_id, status="assumed"):
    db.execute(
        "INSERT INTO tasks (id, project_id, title, status) "
        "VALUES (?, ?, 'test task', ?)",
        (pk, project_id, status),
    )


@pytest.fixture
def project_id(seeded_project_at_cwd):
    return db.query(
        "SELECT id FROM projects WHERE path = ?",
        (str(seeded_project_at_cwd),),
    )[0]["id"]


def test_bind_fills_a_session_with_no_task(project_id):
    from endless.task_cmd import bind_item

    _insert_task(pk=2100, project_id=project_id)
    _insert_session(pk=80, session_id="s-80", project_id=project_id)

    with patch("endless.task_cmd._resolve_session_id_with_prompt",
               return_value=80):
        bind_item(2100)

    assert db.query(
        "SELECT active_task_id FROM sessions WHERE id = 80"
    )[0]["active_task_id"] == 2100


def test_bind_refuses_to_repoint_a_bound_session(project_id, capsys):
    from endless.task_cmd import bind_item

    _insert_task(pk=2200, project_id=project_id)
    _insert_task(pk=2201, project_id=project_id)
    _insert_session(pk=81, session_id="s-81", project_id=project_id,
                    active_task_id=2200)

    with patch("endless.task_cmd._resolve_session_id_with_prompt",
               return_value=81):
        with pytest.raises(click.ClickException) as exc:
            bind_item(2201)

    msg = str(exc.value)
    assert "E-2200" in msg                 # names what it already holds
    assert "task spawn E-2201" in msg      # names the route for the other work
    # And it really did not move.
    assert db.query(
        "SELECT active_task_id FROM sessions WHERE id = 81"
    )[0]["active_task_id"] == 2200


def test_rebinding_the_same_task_is_a_no_op(project_id, capsys):
    """Idempotent, not an error: the invariant is about MOVING a session, and
    re-stating the binding it already has moves nothing."""
    from endless.task_cmd import bind_item

    _insert_task(pk=2300, project_id=project_id)
    _insert_session(pk=82, session_id="s-82", project_id=project_id,
                    active_task_id=2300)

    with patch("endless.task_cmd._resolve_session_id_with_prompt",
               return_value=82), \
         patch("endless.event_bridge.emit_event") as emit:
        bind_item(2300)

    assert not emit.called
    assert "already bound" in capsys.readouterr().out
    assert db.query(
        "SELECT active_task_id FROM sessions WHERE id = 82"
    )[0]["active_task_id"] == 2300

"""Tests for `endless task release` (E-1243, disabled by E-1968).

E-1968 disabled the verb under ED-1560's write-once `sessions.active_task_id`:
a session's task is set at claim and never cleared or repointed, so a verb whose
defining act is the clear cannot survive as a workflow. The command and its CLI
wiring are kept deliberately, as a tombstone that answers with the invariant and
a route, rather than vanishing into "no such command".

These tests pin the tombstone: it refuses, it refuses on every argument shape,
it names the routes, and it performs no write on the way out. The four E-1243
scenarios it used to exercise (bare release, no-claim, stale binding, live other
owner) are gone with the behavior they covered — see `git show` on the E-1968
commit for the body they tested.
"""

from unittest.mock import patch

import click
import pytest

from endless import db


def _insert_session(
    *,
    pk: int,
    session_id: str,
    project_id: int,
    state: str = "working",
    active_task_id: int | None = None,
):
    db.execute(
        "INSERT INTO sessions (id, session_id, project_id, platform, state, "
        "started_at, active_task_id) "
        "VALUES (?, ?, ?, 'claude', ?, '2026-05-11T00:00:00', ?)",
        (pk, session_id, project_id, state, active_task_id),
    )


def _insert_task(*, pk: int, project_id: int, status: str = "underway"):
    db.execute(
        "INSERT INTO tasks (id, project_id, title, status) "
        "VALUES (?, ?, 'test task', ?)",
        (pk, project_id, status),
    )


@pytest.fixture
def project_at_cwd(seeded_project_at_cwd):
    return {
        "project_root": seeded_project_at_cwd,
        "project_id": db.query(
            "SELECT id FROM projects WHERE path = ?",
            (str(seeded_project_at_cwd),),
        )[0]["id"],
    }


@pytest.mark.parametrize(
    "args, kwargs",
    [
        ((None,), {}),                       # bare `task release`
        ((500,), {}),                        # `task release E-500`
        ((500,), {"ignore_missing": True}),  # --ignore-missing is no escape
    ],
)
def test_release_refuses_on_every_argument_shape(args, kwargs):
    from endless.task_cmd import release_item

    with pytest.raises(click.ClickException) as exc:
        release_item(*args, **kwargs)
    assert "deliberately disabled" in str(exc.value)


def test_release_refusal_names_the_invariant_and_both_routes():
    from endless.task_cmd import release_item

    with pytest.raises(click.ClickException) as exc:
        release_item(None)
    msg = str(exc.value)
    assert "one session, one task" in msg.lower()
    # Hand the task back by status, or start a session for different work.
    assert "--status revisit" in msg
    assert "task spawn" in msg


def test_release_leaves_the_binding_intact(project_at_cwd):
    """The refusal is a refusal: no event, no write, binding untouched."""
    from endless.task_cmd import release_item

    _insert_task(pk=800, project_id=project_at_cwd["project_id"])
    _insert_session(
        pk=900, session_id="s-900",
        project_id=project_at_cwd["project_id"], active_task_id=800,
    )

    with patch("endless.task_cmd._current_endless_session_id", return_value=900), \
         patch("endless.event_bridge.emit_event") as emit:
        with pytest.raises(click.ClickException):
            release_item(None)

    assert not emit.called
    assert db.query(
        "SELECT active_task_id FROM sessions WHERE id = 900"
    )[0]["active_task_id"] == 800


def test_release_command_is_still_wired():
    """Kept as a tombstone, not deleted — `task release` must still resolve to
    a command so the user gets the invariant, not "no such command"."""
    from endless.cli import task_cmd as task_group

    assert "release" in task_group.commands

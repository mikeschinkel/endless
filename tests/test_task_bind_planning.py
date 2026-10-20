"""E-2291: `task bind` is the planning step before a claim.

Three things a session needs to find and use it:
  - claim's no-plan refusal names bind as a route, and spawn's and prime's do
    not (they hand the task to a new session, which has nobody to plan with);
  - bind emits its own event, `task.bound`, so the ledger and session_tasks can
    tell a bind from a claim;
  - a user can bind from their own shell pane beside the Claude session, and
    when no single session resolves the refusal says who must run it and where.
"""

from unittest.mock import patch

import click
import pytest

from endless import db, task_cmd

_BIND_LINE = "If you and your user are working out the plan together now:"


def _task(plan: str | None = None) -> int:
    return task_cmd.add_item(title="Fix a thing", description="d", plan=plan)


# --- the refusal ------------------------------------------------------------


def test_claim_refusal_offers_bind(seeded_project_at_cwd):
    item_id = _task()
    with pytest.raises(click.ClickException) as exc:
        task_cmd._require_spawnable(item_id, "claim")
    msg = str(exc.value)
    assert _BIND_LINE in msg
    assert f"endless task bind E-{item_id}" in msg
    assert "Claim once the plan is attached." in msg


@pytest.mark.parametrize("verb", ["spawn", "prime"])
def test_spawn_and_prime_refusals_do_not_offer_bind(seeded_project_at_cwd, verb):
    item_id = _task()
    with pytest.raises(click.ClickException) as exc:
        task_cmd._require_spawnable(item_id, verb)
    msg = str(exc.value)
    assert "has no plan" in msg
    assert "task bind" not in msg


# --- the event --------------------------------------------------------------


def _seed_session(pk: int, task_id: int | None = None) -> None:
    project_id = db.query("SELECT id FROM projects")[0]["id"]
    db.execute(
        "INSERT INTO sessions (id, session_id, project_id, platform, state, "
        "started_at, task_id) "
        "VALUES (?, ?, ?, 'claude', 'working', '2026-10-01T00:00:00', ?)",
        (pk, f"s-{pk}", project_id, task_id),
    )


def test_bind_emits_task_bound(seeded_project_at_cwd):
    item_id = _task()
    _seed_session(77)
    with patch("endless.task_cmd._resolve_bind_session", return_value=77), \
         patch("endless.event_bridge.emit_event") as emit:
        task_cmd.bind_item(item_id)
    assert emit.call_args.kwargs["kind"] == "task.bound"
    assert emit.call_args.kwargs["payload"] == {"session_id": 77}


def test_bind_records_bound_through_the_executor(seeded_project_at_cwd):
    """End to end through the real Go executor: the session owns the task and
    its session_tasks row reads `bound`, not `claimed`."""
    item_id = _task()
    _seed_session(78)
    with patch("endless.task_cmd._resolve_bind_session", return_value=78):
        task_cmd.bind_item(item_id)
    assert db.query(
        "SELECT task_id FROM sessions WHERE id = 78")[0]["task_id"] == item_id
    rows = db.query(
        "SELECT r.slug FROM session_tasks st "
        "JOIN session_task_relations r ON r.id = st.relation_id "
        "WHERE st.session_id = 78 AND st.task_id = ?", (item_id,))
    assert [r["slug"] for r in rows] == ["bound"]


# --- from a shell pane ------------------------------------------------------


@pytest.fixture
def shell_pane(monkeypatch):
    """A plain shell pane in tmux: not Claude, no ENDLESS_SESSION_ID."""
    monkeypatch.setenv("TMUX_PANE", "%1")
    monkeypatch.delenv("ENDLESS_SESSION_ID", raising=False)
    monkeypatch.setattr(task_cmd, "_in_claude_session", lambda: False)


def _siblings(*eids):
    return patch.object(task_cmd, "_list_sibling_claude_session_eids",
                        return_value=list(eids))


def test_shell_pane_beside_one_claude_binds_that_session(
    seeded_project_at_cwd, shell_pane,
):
    item_id = _task()
    _seed_session(81)
    with _siblings(81), \
         patch.object(task_cmd, "_resolve_session_id_with_prompt") as general:
        task_cmd.bind_item(item_id)
    # The general resolver's window-UUID layer is exactly what E-2206 tripped
    # on, so a shell bind must not consult it.
    assert not general.called
    assert db.query(
        "SELECT task_id FROM sessions WHERE id = 81")[0]["task_id"] == item_id


def test_shell_pane_beside_no_claude_refuses_saying_who_and_where(
    seeded_project_at_cwd, shell_pane,
):
    item_id = _task()
    with _siblings(), pytest.raises(click.ClickException) as exc:
        task_cmd.bind_item(item_id)
    msg = str(exc.value)
    assert "No live Claude session in this tmux window" in msg
    assert "from the Claude session that should own" in msg
    assert "shell pane in" in msg


def test_shell_pane_beside_two_claudes_refuses_rather_than_guessing(
    seeded_project_at_cwd, shell_pane,
):
    item_id = _task()
    _seed_session(82)
    _seed_session(83)
    with _siblings(82, 83), pytest.raises(click.ClickException) as exc:
        task_cmd.bind_item(item_id)
    msg = str(exc.value)
    assert "ES-82" in msg and "ES-83" in msg
    assert "from the Claude session that should own" in msg
    assert "ENDLESS_SESSION_ID=<id>" in msg
    assert db.query(
        "SELECT count(*) AS n FROM sessions WHERE task_id = ?",
        (item_id,))[0]["n"] == 0

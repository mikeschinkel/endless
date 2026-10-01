"""Tests for E-2203: an agent must rate a task when its plan would promote it.

Attaching a plan moves an `unplanned` task to `submitted`, and submitting is
where the agent proposes both ratings. So when an AGENT's `task add` or
`task update` attaches a plan with either rating unset — on the call and on the
task — the promotion is refused. On `update` the plan is still written and the
task keeps its status; on `add` nothing is filed, so a re-run cannot duplicate
it. A person attaching a plan is never refused: the rater job rates for them.

Also here: `task approve` on an unrated task addresses the person approving —
the ratings were never proposed, and who proposes them — with the flags second.
"""

import click
import pytest

from endless import agent_help, db, task_cmd


@pytest.fixture(autouse=True)
def _agent_view_off(monkeypatch):
    """conftest strips the harness env vars; pin --agent-view off too, so a test
    that does not call `_as_agent` is a human."""
    monkeypatch.setattr(agent_help, "_AGENT_VIEW", False)


def _as_agent(monkeypatch) -> None:
    monkeypatch.setenv("CLAUDE_CODE_ENTRYPOINT", "cli")


def _row(item_id: int) -> dict:
    rows = db.query(
        "SELECT status, "
        "(SELECT slug FROM complexity_levels WHERE id = complexity_id) AS complexity, "
        "(SELECT slug FROM risk_levels WHERE id = risk_id) AS risk "
        "FROM tasks WHERE id = ?", (item_id,),
    )
    assert rows, f"E-{item_id} not found"
    return dict(rows[0])


def _plan(item_id: int) -> str:
    return db.task_content(item_id).get("plan") or ""


def _task_count() -> int:
    return db.scalar("SELECT count(*) FROM tasks")


# --- task update ------------------------------------------------------------

def test_agent_update_without_ratings_writes_the_plan_and_holds_the_status(
    monkeypatch, seeded_project_at_cwd
):
    item_id = task_cmd.add_item(title="Add a thing", description="short")
    _as_agent(monkeypatch)

    with pytest.raises(click.ClickException) as exc:
        task_cmd.update_plan(item_id=item_id, plan="# plan\nbody\n")

    msg = exc.value.format_message()
    assert "Plan saved" in msg
    assert f"endless task submit E-{item_id} --complexity" in msg
    assert "--risk" in msg
    assert _plan(item_id).startswith("# plan"), "the plan itself is still written"
    assert _row(item_id)["status"] == "unplanned", "the promotion is refused"


def test_agent_update_names_only_the_missing_flag(monkeypatch, seeded_project_at_cwd):
    item_id = task_cmd.add_item(title="Add a thing", description="short",
                                complexity="low")
    _as_agent(monkeypatch)

    with pytest.raises(click.ClickException) as exc:
        task_cmd.update_plan(item_id=item_id, plan="# plan\n")

    msg = exc.value.format_message()
    assert "--risk" in msg
    assert "--complexity" not in msg


def test_agent_update_with_ratings_on_the_call_promotes(monkeypatch, seeded_project_at_cwd):
    item_id = task_cmd.add_item(title="Add a thing", description="short")
    _as_agent(monkeypatch)

    task_cmd.update_plan(item_id=item_id, plan="# plan\n",
                         complexity="low", risk="medium")

    assert _row(item_id) == {"status": "submitted", "complexity": "low",
                             "risk": "medium"}


def test_agent_update_with_ratings_already_on_the_task_promotes(
    monkeypatch, seeded_project_at_cwd
):
    item_id = task_cmd.add_item(title="Add a thing", description="short",
                                complexity="high", risk="low")
    _as_agent(monkeypatch)

    task_cmd.update_plan(item_id=item_id, plan="# plan\n")

    assert _row(item_id)["status"] == "submitted"


def test_agent_update_clearing_a_rating_in_the_same_call_is_refused(
    monkeypatch, seeded_project_at_cwd
):
    """`--risk none` alongside the plan leaves the task unrated on that axis."""
    item_id = task_cmd.add_item(title="Add a thing", description="short",
                                complexity="low", risk="low")
    _as_agent(monkeypatch)

    with pytest.raises(click.ClickException):
        task_cmd.update_plan(item_id=item_id, plan="# plan\n", risk="none")

    assert _row(item_id)["status"] == "unplanned"


def test_agent_keep_status_is_not_refused(monkeypatch, seeded_project_at_cwd):
    """--keep-status asks for no promotion, so there is nothing to refuse."""
    item_id = task_cmd.add_item(title="Add a thing", description="short")
    _as_agent(monkeypatch)

    task_cmd.update_plan(item_id=item_id, plan="# plan\n", keep_status=True)

    assert _row(item_id)["status"] == "unplanned"
    assert _plan(item_id)


def test_human_update_without_ratings_promotes_and_names_the_rater(
    capsys, seeded_project_at_cwd
):
    item_id = task_cmd.add_item(title="Add a thing", description="short")

    task_cmd.update_plan(item_id=item_id, plan="# plan\n")

    assert _row(item_id)["status"] == "submitted"
    assert "rater job" in capsys.readouterr().out


def test_agent_epic_plan_is_not_refused(monkeypatch, seeded_project_at_cwd):
    """An epic's status is derived from its children; it is not ratified."""
    item_id = task_cmd.add_item(title="Add a thing", description="short",
                                task_type="epic")
    _as_agent(monkeypatch)

    task_cmd.update_plan(item_id=item_id, plan="# plan\n", task_type="epic")

    assert _plan(item_id)


# --- task add ---------------------------------------------------------------

def test_agent_add_with_plan_and_no_ratings_files_nothing(
    monkeypatch, seeded_project_at_cwd
):
    before = _task_count()
    _as_agent(monkeypatch)

    with pytest.raises(click.ClickException) as exc:
        task_cmd.add_item(title="Add a thing", description="short",
                          plan="# plan\n")

    msg = exc.value.format_message()
    assert "Nothing was filed" in msg
    assert "--complexity" in msg and "--risk" in msg
    assert _task_count() == before


def test_agent_add_with_plan_and_ratings_files_submitted(monkeypatch, seeded_project_at_cwd):
    _as_agent(monkeypatch)

    item_id = task_cmd.add_item(title="Add a thing", description="short",
                                plan="# plan\n", complexity="low", risk="low")

    assert _row(item_id)["status"] == "submitted"


def test_agent_add_without_a_plan_needs_no_ratings(monkeypatch, seeded_project_at_cwd):
    _as_agent(monkeypatch)

    item_id = task_cmd.add_item(title="Add a thing", description="short")

    assert _row(item_id)["status"] == "unplanned"


def test_human_add_with_plan_and_no_ratings_files_submitted(seeded_project_at_cwd):
    item_id = task_cmd.add_item(title="Add a thing", description="short",
                                plan="# plan\n")

    assert _row(item_id) == {"status": "submitted", "complexity": None,
                             "risk": None}


# --- approve's refusal ------------------------------------------------------

def test_approve_refusal_names_who_proposes_ratings_before_the_flags(
    seeded_project_at_cwd
):
    item_id = task_cmd.add_item(title="Add a thing", description="short",
                                plan="# plan\n")

    with pytest.raises(click.ClickException) as exc:
        task_cmd.approve_item(item_id)

    msg = exc.value.format_message()
    assert "never proposed" in msg
    assert "rater" in msg
    assert msg.index("rater") < msg.index("--complexity"), (
        "the flags are the override, named second"
    )
    assert _row(item_id)["status"] == "submitted"


def test_approve_through_update_status_ready_uses_the_same_wording(
    seeded_project_at_cwd
):
    item_id = task_cmd.add_item(title="Add a thing", description="short",
                                plan="# plan\n")

    with pytest.raises(click.ClickException) as exc:
        task_cmd.update_plan(item_id=item_id, status="ready")

    assert "never proposed" in exc.value.format_message()

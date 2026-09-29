"""E-1993: a task with no plan, or with open questions, cannot be started.

Filing stays cheap; the gate fires in `task claim` and `task spawn`, and its
refusal is the route forward. Open questions also park the task visibly in
`task show`.
"""

import json

import click
import pytest
from click.testing import CliRunner

from endless import db, question_cmd, task_cmd
from endless.cli import main


def _task(plan: str | None = None) -> int:
    item_id = task_cmd.add_item(title="Fix a thing", description="d", plan=plan)
    return item_id


def test_no_plan_is_refused_with_the_route_forward(seeded_project_at_cwd):
    item_id = _task()
    with pytest.raises(click.ClickException) as exc:
        task_cmd._require_spawnable(item_id, "claim")
    msg = str(exc.value)
    assert "has no plan" in msg
    assert f"endless task update E-{item_id} --plan-file" in msg
    assert f"endless task approve E-{item_id}" in msg
    assert "endless question ask" in msg


def test_a_planned_task_with_no_questions_passes(seeded_project_at_cwd):
    item_id = _task(plan="# Plan\nbody\n")
    task_cmd._require_spawnable(item_id, "claim")


def test_an_open_question_parks_a_planned_task(seeded_project_at_cwd):
    item_id = _task(plan="# Plan\nbody\n")
    question_cmd.ask_questions(item_id, ("Which way round?",))

    with pytest.raises(click.ClickException) as exc:
        task_cmd._require_spawnable(item_id, "spawn")
    msg = str(exc.value)
    assert "parked on 1 open question" in msg
    assert "Which way round?" in msg
    assert "has no plan" not in msg


def test_an_answered_question_releases_the_task(seeded_project_at_cwd):
    item_id = _task(plan="# Plan\nbody\n")
    question_cmd.ask_questions(item_id, ("Which way round?",))
    qid = question_cmd.provenance.rows_of(
        question_cmd._go(["task-questions", "--id", str(item_id)]))[0]["id"]
    question_cmd.answer_question(qid, "Clockwise.", by="user")

    task_cmd._require_spawnable(item_id, "spawn")


def test_both_problems_are_reported_together(seeded_project_at_cwd):
    item_id = _task()
    question_cmd.ask_questions(item_id, ("One?", "Two?"))
    with pytest.raises(click.ClickException) as exc:
        task_cmd._require_spawnable(item_id, "claim")
    msg = str(exc.value)
    assert "has no plan" in msg and "parked on 2 open questions" in msg


def test_claim_applies_the_gate(seeded_project_at_cwd):
    item_id = _task()
    with pytest.raises(click.ClickException) as exc:
        task_cmd.claim_item(item_id, unattended=True)
    assert "has no plan" in str(exc.value)
    status = db.query("SELECT status FROM tasks WHERE id = ?", (item_id,))[0]["status"]
    assert status == "unplanned", "a refused claim changes nothing"


def test_spawn_applies_the_gate(seeded_project_at_cwd, monkeypatch):
    import shutil
    item_id = _task()
    real_which = shutil.which
    monkeypatch.setattr(shutil, "which",
                        lambda name: "/usr/bin/tmux" if name == "tmux" else real_which(name))
    monkeypatch.setenv("TMUX", "/tmp/tmux-sock,1,0")
    monkeypatch.setattr(task_cmd, "_check_task_ownership", lambda *a, **k: None)
    with pytest.raises(click.ClickException) as exc:
        task_cmd.spawn_plan(item_id)
    assert "Cannot spawn" in str(exc.value)


def test_show_marks_a_parked_task_in_every_format(seeded_project_at_cwd):
    item_id = _task(plan="# Plan\n")
    question_cmd.ask_questions(item_id, ("Which way round?",))
    runner = CliRunner()

    human = runner.invoke(main, ["task", "show", str(item_id), "--no-color"]).output
    assert "Parked:" in human and "1 open question" in human
    assert "— Open questions —" in human and "Which way round?" in human

    agent = runner.invoke(main, ["task", "show", str(item_id), "--agent"]).output
    assert "open_questions=1" in agent
    assert "## Open questions" in agent

    data = json.loads(runner.invoke(main, ["task", "show", str(item_id), "--json"]).output)
    assert [q["question"] for q in data["open_questions"]] == ["Which way round?"]


def test_show_of_an_unparked_task_says_nothing_about_questions(seeded_project_at_cwd):
    item_id = _task(plan="# Plan\n")
    runner = CliRunner()
    human = runner.invoke(main, ["task", "show", str(item_id), "--no-color"]).output
    assert "Parked:" not in human
    data = json.loads(runner.invoke(main, ["task", "show", str(item_id), "--json"]).output)
    assert data["open_questions"] == []

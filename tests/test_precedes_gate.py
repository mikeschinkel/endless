"""E-2270: a task linked as preceding another refuses the other's start.

`task claim`, `task spawn` and `task prime` refuse while a task that should come
first is unfinished, naming each one; `--out-of-order` overrides that refusal
alone and still lists them. The plan and open-question refusals are untouched.
"""

import shutil

import click
import pytest
from click.testing import CliRunner

from endless import db, question_cmd, task_cmd
from endless.cli import main


def _status(item_id: int) -> str:
    return db.query("SELECT status FROM tasks WHERE id = ?", (item_id,))[0]["status"]


def _set_status(item_id: int, status: str) -> None:
    """A raw write: the setup needs a task sitting IN a status, not a test of
    the lifecycle guard that `task update --status` would run."""
    db.execute("UPDATE tasks SET status = ? WHERE id = ?", (status, item_id))


def _pair(pred_status: str = "submitted") -> tuple[int, int]:
    """(predecessor, follower): both planned, the first linked as preceding."""
    pred = task_cmd.add_item(title="Fix each fault", description="d", plan="# Plan")
    follower = task_cmd.add_item(title="Fix each error", description="d", plan="# Plan")
    task_cmd.link_tasks(pred, follower, "precedes")
    if pred_status != "submitted":
        _set_status(pred, pred_status)
    return pred, follower


# ---------------------------------------------------------------------------
# The helper
# ---------------------------------------------------------------------------

def test_an_unfinished_predecessor_is_listed(seeded_project_at_cwd):
    pred, follower = _pair()
    rows = task_cmd._unfinished_predecessors(follower)
    assert [(r["id"], r["status"], r["title"]) for r in rows] == [
        (pred, "submitted", "Fix each fault")]


def test_the_predecessor_side_has_no_predecessors(seeded_project_at_cwd):
    pred, _ = _pair()
    assert task_cmd._unfinished_predecessors(pred) == []


@pytest.mark.parametrize("status", ["confirmed", "assumed", "superseded"])
def test_a_finished_predecessor_does_not_refuse(seeded_project_at_cwd, status):
    _, follower = _pair(status)
    assert task_cmd._unfinished_predecessors(follower) == []
    for verb in ("claim", "spawn", "prime"):
        task_cmd._require_spawnable(follower, verb)


def test_a_task_with_no_predecessors_is_unaffected(seeded_project_at_cwd):
    item_id = task_cmd.add_item(title="Fix a thing", description="d", plan="# Plan")
    for verb in ("claim", "spawn", "prime"):
        task_cmd._require_spawnable(item_id, verb)


# ---------------------------------------------------------------------------
# The refusal, and the override
# ---------------------------------------------------------------------------

@pytest.mark.parametrize("verb", ["claim", "spawn", "prime"])
def test_each_verb_refuses_naming_the_predecessor(seeded_project_at_cwd, verb):
    pred, follower = _pair()
    with pytest.raises(click.ClickException) as exc:
        task_cmd._require_spawnable(follower, verb)
    msg = str(exc.value)
    assert f"Cannot {verb} E-{follower}" in msg
    assert f"E-{pred}" in msg and "submitted" in msg
    assert f"endless task {verb} E-{follower} --out-of-order" in msg


@pytest.mark.parametrize("verb", ["claim", "spawn", "prime"])
def test_out_of_order_proceeds_and_lists_the_predecessor(seeded_project_at_cwd, capsys, verb):
    pred, follower = _pair("underway")
    task_cmd._require_spawnable(follower, verb, out_of_order=True)
    err = capsys.readouterr().err
    assert f"E-{pred}" in err and "underway" in err and "out of order" in err


def test_out_of_order_does_not_override_the_plan_refusal(seeded_project_at_cwd):
    pred = task_cmd.add_item(title="Fix each fault", description="d", plan="# Plan")
    follower = task_cmd.add_item(title="Fix each error", description="d")
    task_cmd.link_tasks(pred, follower, "precedes")
    with pytest.raises(click.ClickException, match="has no plan"):
        task_cmd._require_spawnable(follower, "claim", out_of_order=True)


def test_out_of_order_does_not_override_the_question_refusal(seeded_project_at_cwd):
    _, follower = _pair()
    question_cmd.ask_questions(follower, ("Which way round?",))
    for verb, need_plan in (("spawn", True), ("prime", False)):
        with pytest.raises(click.ClickException, match="open question"):
            task_cmd._require_spawnable(follower, verb, require_plan=need_plan,
                                        out_of_order=True)


# ---------------------------------------------------------------------------
# Through each command
# ---------------------------------------------------------------------------

def test_claim_refuses_and_changes_nothing(seeded_project_at_cwd):
    pred, follower = _pair()
    with pytest.raises(click.ClickException) as exc:
        task_cmd.claim_item(follower, unattended=True)
    assert f"E-{pred}" in str(exc.value)
    assert _status(follower) == "submitted", "a refused claim changes nothing"


def test_claim_out_of_order_claims(seeded_project_at_cwd):
    _, follower = _pair()
    task_cmd.claim_item(follower, unattended=True, out_of_order=True)
    assert _status(follower) == "underway"


@pytest.fixture
def in_tmux(monkeypatch):
    real_which = shutil.which
    monkeypatch.setattr(shutil, "which",
                        lambda name: "/usr/bin/tmux" if name == "tmux" else real_which(name))
    monkeypatch.setenv("TMUX", "/tmp/tmux-sock,1,0")
    monkeypatch.setattr(task_cmd, "_check_task_ownership", lambda *a, **k: None)


def test_spawn_refuses_and_changes_nothing(seeded_project_at_cwd, in_tmux):
    pred, follower = _pair()
    with pytest.raises(click.ClickException) as exc:
        task_cmd.spawn_plan(follower)
    assert f"Cannot spawn E-{follower}" in str(exc.value)
    assert f"E-{pred}" in str(exc.value)
    assert _status(follower) == "submitted"


def test_prime_refuses_and_changes_nothing(seeded_project_at_cwd, in_tmux):
    pred, follower = _pair()
    with pytest.raises(click.ClickException) as exc:
        task_cmd.prime_task(follower)
    assert f"Cannot prime E-{follower}" in str(exc.value)
    assert f"E-{pred}" in str(exc.value)
    assert _status(follower) == "submitted"


@pytest.mark.parametrize("verb", ["claim", "spawn", "prime"])
def test_each_command_takes_the_flag(verb):
    out = CliRunner().invoke(main, ["task", verb, "--help"]).output
    assert "--out-of-order" in out

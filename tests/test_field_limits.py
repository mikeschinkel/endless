"""E-1993: title and description limits, and no ids in either.

A title names WHAT in at most 60 characters; a description says WHAT the task
is in at most 256 characters, on one line. Neither may cite a task, decision or
session id — the relationship belongs in a task link. The refusal is the
teaching surface, so it names where the text belongs, not only the limit.
"""

import click
import pytest
from click.testing import CliRunner

from endless import db, task_cmd
from endless.cli import main


def _title(n: int) -> str:
    return "Add " + "x" * (n - len("Add "))


# --- the caps ---------------------------------------------------------------

def test_the_caps_are_60_and_256():
    assert task_cmd.TITLE_MAX_LENGTH == 60
    assert task_cmd.DESCRIPTION_MAX_LENGTH == 256


def test_a_title_of_exactly_60_is_accepted(seeded_project_at_cwd):
    item_id = task_cmd.add_item(title=_title(60), description="d")
    assert item_id


def test_a_title_of_61_is_refused_naming_where_the_excess_goes(seeded_project_at_cwd):
    with pytest.raises(click.ClickException) as exc:
        task_cmd.add_item(title=_title(61), description="d")
    msg = str(exc.value)
    assert "61" in msg and "60" in msg
    assert "the how" in msg and "the why" in msg
    assert "--analysis" in msg and "--context" in msg


def test_a_description_of_exactly_256_is_accepted(seeded_project_at_cwd):
    assert task_cmd.add_item(title="Add a thing", description="d" * 256)


def test_a_description_of_257_is_refused_naming_every_destination(seeded_project_at_cwd):
    with pytest.raises(click.ClickException) as exc:
        task_cmd.add_item(title="Add a thing", description="d" * 257)
    msg = str(exc.value)
    assert "257" in msg and "256" in msg
    for dest in ("--context", "--analysis", "--plan", "--notes"):
        assert dest in msg, dest


def test_update_refuses_the_same_caps(seeded_project_at_cwd):
    item_id = task_cmd.add_item(title="Add a thing", description="d")
    with pytest.raises(click.ClickException):
        task_cmd.update_plan(item_id=item_id, title=_title(61))
    with pytest.raises(click.ClickException):
        task_cmd.update_plan(item_id=item_id, description="d" * 257)
    row = db.query("SELECT title, description FROM tasks WHERE id = ?", (item_id,))[0]
    assert (row["title"], row["description"]) == ("Add a thing", "d")


def test_an_update_that_does_not_write_an_old_long_field_is_not_refused(
    seeded_project_at_cwd
):
    """The limits apply to the field being written. Nothing is truncated."""
    item_id = task_cmd.add_item(title="Add a thing", description="d")
    long_title, long_desc = _title(90), "d" * 900
    db.execute("UPDATE tasks SET title = ?, description = ? WHERE id = ?",
               (long_title, long_desc, item_id))

    task_cmd.update_plan(item_id=item_id, phase="later")

    row = db.query("SELECT title, description, phase FROM tasks WHERE id = ?",
                   (item_id,))[0]
    assert row["title"] == long_title
    assert row["description"] == long_desc
    assert row["phase"] == "later"


# --- no ids -----------------------------------------------------------------

@pytest.mark.parametrize("cited", ["E-12", "ED-1538", "ES-1113"])
def test_an_id_in_the_title_is_refused(cited, seeded_project_at_cwd):
    with pytest.raises(click.ClickException) as exc:
        task_cmd.add_item(title=f"Fix the thing from {cited}", description="d")
    msg = str(exc.value)
    assert cited in msg
    assert "task link" in msg


@pytest.mark.parametrize("cited", ["E-12", "ED-1538", "ES-1113"])
def test_an_id_in_the_description_is_refused(cited, seeded_project_at_cwd):
    with pytest.raises(click.ClickException) as exc:
        task_cmd.add_item(title="Fix a thing", description=f"Follow-up to {cited}.")
    assert cited in str(exc.value)


def test_an_id_on_update_is_refused(seeded_project_at_cwd):
    item_id = task_cmd.add_item(title="Fix a thing", description="d")
    with pytest.raises(click.ClickException):
        task_cmd.update_plan(item_id=item_id, description="Split from E-9.")
    with pytest.raises(click.ClickException):
        task_cmd.update_plan(item_id=item_id, title="Fix a thing for E-9")


@pytest.mark.parametrize("text", ["Handle UTF-8 input", "Use SHA-256 sums", "Fix the E-mail link"])
def test_look_alikes_are_not_ids(text, seeded_project_at_cwd):
    assert task_cmd.add_item(title="Fix a thing", description=text)


def test_ids_are_fine_in_every_content_slot(seeded_project_at_cwd):
    item_id = task_cmd.add_item(
        title="Fix a thing", description="d",
        context="Surfaced by E-12.", analysis="Per ED-3.", plan="# Plan\nSee E-4.\n",
    )
    content = db.task_content(item_id)
    assert "E-12" in content["context"]
    assert "ED-3" in content["analysis"]
    assert "E-4" in content["plan"]


def test_every_problem_is_reported_in_one_refusal(seeded_project_at_cwd):
    with pytest.raises(click.ClickException) as exc:
        task_cmd.add_item(title="Fix a thing for E-1", description="d" * 250 + " E-2 ok")
    msg = str(exc.value)
    assert "E-1" in msg and "E-2" in msg and "257" in msg


# --- decisions are not under the task field model ---------------------------

def test_a_decision_description_keeps_its_own_cap():
    task_cmd.validate_description("d" * 1024)
    task_cmd.validate_description("Supersedes ED-12.")
    with pytest.raises(click.ClickException):
        task_cmd.validate_description("d" * 1025)


# --- through the CLI --------------------------------------------------------

def test_the_cli_refusal_names_the_destination(seeded_project_at_cwd):
    result = CliRunner().invoke(main, [
        "task", "add", "Fix a thing", "--description", "x" * 300,
    ])
    assert result.exit_code != 0
    assert "--context" in result.output

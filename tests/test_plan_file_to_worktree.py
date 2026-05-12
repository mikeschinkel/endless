"""Tests for E-1216: plan-file writes go to the task's worktree, never to main.

`task update --text` (and `task add --text`) now:
  - Write the plan file to the task's worktree if one exists.
  - Auto-create the worktree if none exists (with prominent output).
  - Refuse if `--no-create-worktree` is passed and none exists.
  - Never write the plan file to main's working tree.
"""

import click
import pytest
from click.testing import CliRunner

from endless import db, task_cmd
from endless.cli import main


def _add_minimal_task(title: str = "Audit the cache layer") -> int:
    cur = db.execute(
        "INSERT INTO tasks (project_id, title, status, type, phase, created_at) "
        "VALUES (1, ?, 'needs_plan', 'task', 'now', datetime('now'))",
        (title,),
    )
    return cur.lastrowid


# ─── helpers ──────────────────────────────────────────────────────────────────


def test_worktree_for_task_returns_none_when_absent(seeded_project_at_cwd):
    tid = _add_minimal_task()
    assert task_cmd._worktree_for_task(tid) is None


def test_main_root_for_task_resolves_registered_path(seeded_project_at_cwd):
    tid = _add_minimal_task()
    root = task_cmd._main_root_for_task(tid)
    assert root is not None
    assert root == seeded_project_at_cwd


# ─── auto-create on update --text ─────────────────────────────────────────────


def test_update_text_auto_creates_worktree(tmp_path, seeded_project_at_cwd):
    tid = _add_minimal_task()
    plan_src = tmp_path / "plan.md"
    plan_src.write_text("# plan\nbody\n")

    task_cmd.update_plan(tid, text_file=str(plan_src))

    wt = task_cmd._worktree_for_task(tid)
    assert wt is not None
    assert wt == seeded_project_at_cwd / ".endless" / "worktrees" / f"e-{tid}"
    # Plan file landed in the worktree, NOT main:
    plan_in_worktree = wt / ".endless" / "plans" / f"E-{tid}.md"
    plan_in_main = seeded_project_at_cwd / ".endless" / "plans" / f"E-{tid}.md"
    assert plan_in_worktree.exists()
    assert not plan_in_main.exists()
    assert plan_in_worktree.read_text() == "# plan\nbody\n"


def test_update_text_uses_existing_worktree(tmp_path, seeded_project_at_cwd):
    tid = _add_minimal_task()
    plan_src = tmp_path / "plan.md"
    plan_src.write_text("# v1\n")
    # First call creates the worktree
    task_cmd.update_plan(tid, text_file=str(plan_src))

    plan_src.write_text("# v2\n")
    task_cmd.update_plan(tid, text_file=str(plan_src))

    wt = task_cmd._worktree_for_task(tid)
    plan_in_worktree = wt / ".endless" / "plans" / f"E-{tid}.md"
    assert plan_in_worktree.read_text() == "# v2\n"


# ─── --no-create-worktree opt-out ─────────────────────────────────────────────


def test_update_text_refuses_when_no_create_worktree_and_no_worktree(
    tmp_path, seeded_project_at_cwd,
):
    tid = _add_minimal_task()
    plan_src = tmp_path / "plan.md"
    plan_src.write_text("# plan\n")

    with pytest.raises(click.ClickException) as exc:
        task_cmd.update_plan(
            tid, text_file=str(plan_src), allow_create_worktree=False,
        )
    msg = str(exc.value.message).lower()
    assert "no worktree" in msg
    assert "no-create-worktree" in msg or "task claim" in msg

    # No worktree created, no plan file in main.
    assert task_cmd._worktree_for_task(tid) is None
    assert not (
        seeded_project_at_cwd / ".endless" / "plans" / f"E-{tid}.md"
    ).exists()


def test_update_text_with_no_create_worktree_uses_existing_worktree(
    tmp_path, seeded_project_at_cwd,
):
    tid = _add_minimal_task()
    plan_src = tmp_path / "plan.md"
    plan_src.write_text("# initial\n")
    task_cmd.update_plan(tid, text_file=str(plan_src))

    # Worktree now exists; --no-create-worktree should work since we're
    # using the existing one, not creating.
    plan_src.write_text("# updated\n")
    task_cmd.update_plan(
        tid, text_file=str(plan_src), allow_create_worktree=False,
    )

    wt = task_cmd._worktree_for_task(tid)
    plan = wt / ".endless" / "plans" / f"E-{tid}.md"
    assert plan.read_text() == "# updated\n"


# ─── task add --text ──────────────────────────────────────────────────────────


def test_add_text_auto_creates_worktree(tmp_path, seeded_project_at_cwd):
    plan_src = tmp_path / "plan.md"
    plan_src.write_text("# from add\nbody\n")

    item_id = task_cmd.add_item(
        title="Audit the buffer",
        text_file=str(plan_src),
    )

    wt = task_cmd._worktree_for_task(item_id)
    assert wt is not None
    plan_in_worktree = wt / ".endless" / "plans" / f"E-{item_id}.md"
    assert plan_in_worktree.exists()
    assert plan_in_worktree.read_text() == "# from add\nbody\n"


def test_add_text_with_no_create_worktree_refuses(
    tmp_path, seeded_project_at_cwd,
):
    plan_src = tmp_path / "plan.md"
    plan_src.write_text("# plan\n")

    with pytest.raises(click.ClickException):
        task_cmd.add_item(
            title="Audit something",
            text_file=str(plan_src),
            allow_create_worktree=False,
        )


# ─── CLI flag plumbing ────────────────────────────────────────────────────────


def test_cli_update_text_no_create_worktree_flag(
    tmp_path, seeded_project_at_cwd,
):
    tid = _add_minimal_task()
    plan_src = tmp_path / "plan.md"
    plan_src.write_text("# plan\n")
    runner = CliRunner()
    result = runner.invoke(main, [
        "task", "update", f"E-{tid}",
        "--text", str(plan_src),
        "--no-create-worktree",
    ])
    assert result.exit_code != 0
    assert "no worktree" in result.output.lower()


def test_cli_update_text_default_creates_worktree(
    tmp_path, seeded_project_at_cwd,
):
    tid = _add_minimal_task()
    plan_src = tmp_path / "plan.md"
    plan_src.write_text("# plan\n")
    runner = CliRunner()
    result = runner.invoke(main, [
        "task", "update", f"E-{tid}",
        "--text", str(plan_src),
    ])
    assert result.exit_code == 0, result.output
    assert "Worktree created" in result.output
    assert f"E-{tid}" in result.output


def test_cli_add_text_default_creates_worktree(
    tmp_path, seeded_project_at_cwd,
):
    plan_src = tmp_path / "plan.md"
    plan_src.write_text("# plan\n")
    runner = CliRunner()
    result = runner.invoke(main, [
        "task", "add", "Audit something new",
        "--text", str(plan_src),
    ])
    assert result.exit_code == 0, result.output
    assert "Worktree created" in result.output


# ─── output messages distinguish create vs reuse ──────────────────────────────


def test_create_message_on_first_write(tmp_path, seeded_project_at_cwd):
    tid = _add_minimal_task()
    plan_src = tmp_path / "plan.md"
    plan_src.write_text("# plan\n")
    runner = CliRunner()
    result = runner.invoke(main, [
        "task", "update", f"E-{tid}", "--text", str(plan_src),
    ])
    assert "Worktree created for" in result.output
    assert "Using existing" not in result.output


def test_reuse_message_on_subsequent_write(tmp_path, seeded_project_at_cwd):
    tid = _add_minimal_task()
    plan_src = tmp_path / "plan.md"
    plan_src.write_text("# plan v1\n")
    runner = CliRunner()
    # First call creates
    runner.invoke(main, ["task", "update", f"E-{tid}", "--text", str(plan_src)])
    # Second call should report reuse
    plan_src.write_text("# plan v2\n")
    result = runner.invoke(main, [
        "task", "update", f"E-{tid}", "--text", str(plan_src),
    ])
    assert result.exit_code == 0, result.output
    assert "Using existing worktree" in result.output
    assert "Worktree created for" not in result.output

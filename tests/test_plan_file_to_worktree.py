"""Tests for E-1445: `task update --text` never creates a worktree.

Rescinds the E-1216 auto-create default. The contract now:
  - `task update --text` / `task add --text` write `tasks.text` (DB) and, IF a
    worktree already exists, mirror the content into
    `<worktree>/.endless/plans/E-NNN.md`. They NEVER create a worktree.
  - The plan file otherwise materializes when the worktree is born at
    claim/spawn (`worktree_cmd.create_task_worktree` -> `_materialize_plan_file`,
    which reads `tasks.text` via the endless-session-query Go helper).
  - `--no-create-worktree` is removed (the command never creates one).
  - Plan files NEVER land in main's working tree.
"""

import subprocess
import types

from click.testing import CliRunner

from endless import db, task_cmd, worktree_cmd
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


# ─── update/add --text no longer create a worktree (the fix) ───────────────────


def test_update_text_does_not_create_worktree(tmp_path, seeded_project_at_cwd):
    tid = _add_minimal_task()
    plan_src = tmp_path / "plan.md"
    plan_src.write_text("# plan\nbody\n")

    task_cmd.update_plan(tid, text_file=str(plan_src))

    # No worktree, no sandbox-triggering side effects, nothing on disk.
    assert task_cmd._worktree_for_task(tid) is None
    assert not (
        seeded_project_at_cwd / ".endless" / "worktrees" / f"e-{tid}"
    ).exists()
    assert not (
        seeded_project_at_cwd / ".endless" / "plans" / f"E-{tid}.md"
    ).exists()
    # The DB IS updated — tasks.text is the source of truth.
    row = db.query("SELECT text FROM tasks WHERE id = ?", (tid,))
    assert row[0]["text"] == "# plan\nbody\n"


def test_add_text_does_not_create_worktree(tmp_path, seeded_project_at_cwd):
    plan_src = tmp_path / "plan.md"
    plan_src.write_text("# from add\nbody\n")

    item_id = task_cmd.add_item(title="Audit the buffer", text_file=str(plan_src))

    assert task_cmd._worktree_for_task(item_id) is None
    assert not (
        seeded_project_at_cwd / ".endless" / "worktrees" / f"e-{item_id}"
    ).exists()
    row = db.query("SELECT text FROM tasks WHERE id = ?", (item_id,))
    assert row[0]["text"] == "# from add\nbody\n"


# ─── mirror into an existing worktree ─────────────────────────────────────────


def test_update_text_mirrors_into_existing_worktree(
    tmp_path, seeded_project_at_cwd, monkeypatch,
):
    tid = _add_minimal_task()
    fake_wt = tmp_path / "wt"
    fake_wt.mkdir()
    monkeypatch.setattr(task_cmd, "_worktree_for_task", lambda _tid: fake_wt)

    plan_src = tmp_path / "plan.md"
    plan_src.write_text("# v2\n")
    task_cmd.update_plan(tid, text_file=str(plan_src))

    mirrored = fake_wt / ".endless" / "plans" / f"E-{tid}.md"
    assert mirrored.read_text() == "# v2\n"


def test_mirror_plan_to_worktree_noop_without_worktree(
    tmp_path, seeded_project_at_cwd, monkeypatch,
):
    monkeypatch.setattr(task_cmd, "_worktree_for_task", lambda _tid: None)
    assert task_cmd._mirror_plan_to_worktree(123, "# x\n") is None


# ─── materialize-at-claim from DB (create_task_worktree) ──────────────────────


def _fake_run_factory(stdout: str, returncode: int = 0):
    def _run(argv, **kwargs):
        # "task-text" is the subcommand; it may be preceded by the E-1429
        # --config-dir context pair, so assert membership, not position.
        assert "task-text" in argv
        return types.SimpleNamespace(
            returncode=returncode, stdout=stdout, stderr="",
        )
    return _run


def test_materialize_plan_file_writes_from_db_text(tmp_path, monkeypatch):
    wt = tmp_path / "wt"
    wt.mkdir()
    monkeypatch.setattr(worktree_cmd.shutil, "which", lambda _b: "/fake/esq")
    monkeypatch.setattr(
        worktree_cmd.subprocess, "run", _fake_run_factory("# materialized\n"),
    )

    worktree_cmd._materialize_plan_file(777, wt)

    plan = wt / ".endless" / "plans" / "E-777.md"
    assert plan.read_text() == "# materialized\n"


def test_materialize_plan_file_skips_when_db_text_empty(tmp_path, monkeypatch):
    wt = tmp_path / "wt"
    wt.mkdir()
    monkeypatch.setattr(worktree_cmd.shutil, "which", lambda _b: "/fake/esq")
    monkeypatch.setattr(
        worktree_cmd.subprocess, "run", _fake_run_factory("   \n"),
    )

    worktree_cmd._materialize_plan_file(778, wt)

    assert not (wt / ".endless" / "plans" / "E-778.md").exists()


def test_materialize_plan_file_warns_when_binary_missing(tmp_path, monkeypatch, capsys):
    wt = tmp_path / "wt"
    wt.mkdir()
    monkeypatch.setattr(worktree_cmd.shutil, "which", lambda _b: None)

    worktree_cmd._materialize_plan_file(779, wt)

    assert not (wt / ".endless" / "plans" / "E-779.md").exists()
    assert "endless-go not found" in capsys.readouterr().err


# ─── --no-create-worktree is gone ─────────────────────────────────────────────


def test_cli_update_no_create_worktree_flag_removed(
    tmp_path, seeded_project_at_cwd,
):
    tid = _add_minimal_task()
    plan_src = tmp_path / "plan.md"
    plan_src.write_text("# plan\n")
    result = CliRunner().invoke(main, [
        "task", "update", f"E-{tid}",
        "--text", str(plan_src),
        "--no-create-worktree",
    ])
    assert result.exit_code != 0
    assert "no such option" in result.output.lower()


def test_cli_add_no_create_worktree_flag_removed(tmp_path, seeded_project_at_cwd):
    plan_src = tmp_path / "plan.md"
    plan_src.write_text("# plan\n")
    result = CliRunner().invoke(main, [
        "task", "add", "Audit something new",
        "--text", str(plan_src),
        "--no-create-worktree",
    ])
    assert result.exit_code != 0
    assert "no such option" in result.output.lower()


def test_cli_update_text_succeeds_without_worktree(tmp_path, seeded_project_at_cwd):
    tid = _add_minimal_task()
    plan_src = tmp_path / "plan.md"
    plan_src.write_text("# plan\n")
    result = CliRunner().invoke(main, [
        "task", "update", f"E-{tid}", "--text", str(plan_src),
    ])
    assert result.exit_code == 0, result.output
    assert "Worktree created" not in result.output
    assert task_cmd._worktree_for_task(tid) is None

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


def _add_minimal_task(title: str = "Refactor the cache layer") -> int:
    cur = db.execute(
        "INSERT INTO tasks (project_id, title, status, type_id, phase, created_at) "
        "VALUES (1, ?, 'unplanned', 1, 'now', datetime('now'))",
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

    task_cmd.update_plan(tid, text=plan_src.read_text())

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

    item_id = task_cmd.add_item(title="Refactor the buffer", text=plan_src.read_text())

    assert task_cmd._worktree_for_task(item_id) is None
    assert not (
        seeded_project_at_cwd / ".endless" / "worktrees" / f"e-{item_id}"
    ).exists()
    row = db.query("SELECT text FROM tasks WHERE id = ?", (item_id,))
    assert row[0]["text"] == "# from add\nbody\n"


# ─── mirror into an existing worktree ─────────────────────────────────────────


def _git_init_wt(wt) -> None:
    """Initialize a minimal git repo with a HEAD commit (E-1525 commit step)."""
    subprocess.run(["git", "init", "-q", "-b", "main", str(wt)], check=True)
    subprocess.run(
        ["git", "-C", str(wt), "config", "user.email", "test@example.com"],
        check=True,
    )
    subprocess.run(
        ["git", "-C", str(wt), "config", "user.name", "Test"], check=True,
    )
    subprocess.run(
        ["git", "-C", str(wt), "commit", "--allow-empty", "-q", "-m", "init"],
        check=True,
    )


def test_update_text_mirrors_into_existing_worktree(
    tmp_path, seeded_project_at_cwd, monkeypatch,
):
    tid = _add_minimal_task()
    fake_wt = tmp_path / "wt"
    fake_wt.mkdir()
    _git_init_wt(fake_wt)
    monkeypatch.setattr(task_cmd, "_worktree_for_task", lambda _tid: fake_wt)

    plan_src = tmp_path / "plan.md"
    plan_src.write_text("# v2\n")
    task_cmd.update_plan(tid, text=plan_src.read_text())

    mirrored = fake_wt / ".endless" / "plans" / f"E-{tid}.md"
    assert mirrored.read_text() == "# v2\n"
    # E-1525: the mirror must also commit the file on the worktree branch.
    log = subprocess.run(
        ["git", "-C", str(fake_wt), "log", "--format=%s", "-n", "1"],
        capture_output=True, text=True, check=True,
    ).stdout.strip()
    assert log == f"Endless: update plan for E-{tid}"
    # And re-running with the same content is a no-op (no second commit).
    task_cmd.update_plan(tid, text=plan_src.read_text())
    count = subprocess.run(
        ["git", "-C", str(fake_wt), "rev-list", "--count", "HEAD"],
        capture_output=True, text=True, check=True,
    ).stdout.strip()
    assert count == "2"  # init + single plan commit


def test_mirror_plan_to_worktree_noop_without_worktree(
    tmp_path, seeded_project_at_cwd, monkeypatch,
):
    monkeypatch.setattr(task_cmd, "_worktree_for_task", lambda _tid: None)
    assert task_cmd._mirror_plan_to_worktree(123, "# x\n") is None


def test_update_outcome_mirrors_into_worktree(
    tmp_path, seeded_project_at_cwd, monkeypatch,
):
    """E-1747: --outcome writes+commits `.endless/outcomes/E-NNN.md`."""
    tid = _add_minimal_task()
    fake_wt = tmp_path / "wt"
    fake_wt.mkdir()
    _git_init_wt(fake_wt)
    monkeypatch.setattr(task_cmd, "_worktree_for_task", lambda _tid: fake_wt)

    task_cmd.update_plan(tid, outcome="Shipped the cache layer.\n")

    mirrored = fake_wt / ".endless" / "outcomes" / f"E-{tid}.md"
    assert mirrored.read_text() == "Shipped the cache layer.\n"
    log = subprocess.run(
        ["git", "-C", str(fake_wt), "log", "--format=%s", "-n", "1"],
        capture_output=True, text=True, check=True,
    ).stdout.strip()
    assert log == f"Endless: update outcome for E-{tid}"


def test_update_analysis_mirrors_into_worktree(
    tmp_path, seeded_project_at_cwd, monkeypatch,
):
    """E-1747: --analysis writes+commits `.endless/analyses/E-NNN.md`."""
    tid = _add_minimal_task()
    fake_wt = tmp_path / "wt"
    fake_wt.mkdir()
    _git_init_wt(fake_wt)
    monkeypatch.setattr(task_cmd, "_worktree_for_task", lambda _tid: fake_wt)

    task_cmd.update_plan(tid, analysis="# Analysis\nTradeoffs...\n")

    mirrored = fake_wt / ".endless" / "analyses" / f"E-{tid}.md"
    assert mirrored.read_text() == "# Analysis\nTradeoffs...\n"
    log = subprocess.run(
        ["git", "-C", str(fake_wt), "log", "--format=%s", "-n", "1"],
        capture_output=True, text=True, check=True,
    ).stdout.strip()
    assert log == f"Endless: update analysis for E-{tid}"


def test_confirm_outcome_mirrors_into_worktree(
    tmp_path, seeded_project_at_cwd, monkeypatch,
):
    """E-1747: an outcome supplied to a status verb (confirm) also mirrors."""
    tid = _add_minimal_task()
    fake_wt = tmp_path / "wt"
    fake_wt.mkdir()
    _git_init_wt(fake_wt)
    monkeypatch.setattr(task_cmd, "_worktree_for_task", lambda _tid: fake_wt)

    task_cmd.complete_item(tid, outcome="Verified end to end.\n")

    mirrored = fake_wt / ".endless" / "outcomes" / f"E-{tid}.md"
    assert mirrored.read_text() == "Verified end to end.\n"


# ─── materialize-at-claim from DB (create_task_worktree) ──────────────────────


def _fake_run_factory(stdout: str, returncode: int = 0):
    # Capture the real subprocess.run before any test-time monkeypatch so the
    # E-1525 commit step (git status/add/commit) can pass through to a real
    # git repo while the endless-go task-text spawn stays mocked.
    real_run = subprocess.run

    def _run(argv, **kwargs):
        if argv and argv[0] == "git":
            return real_run(argv, **kwargs)
        # "task-field" is the subcommand (E-1747 generalized task-text); it
        # may be preceded by the E-1429 --config-dir context pair, so assert
        # membership, not position.
        assert "task-field" in argv
        return types.SimpleNamespace(
            returncode=returncode, stdout=stdout, stderr="",
        )
    return _run


def test_materialize_plan_file_writes_from_db_text(tmp_path, monkeypatch):
    wt = tmp_path / "wt"
    wt.mkdir()
    _git_init_wt(wt)
    monkeypatch.setattr(worktree_cmd.shutil, "which", lambda _b: "/fake/esq")
    monkeypatch.setattr(
        worktree_cmd.subprocess, "run", _fake_run_factory("# materialized\n"),
    )

    worktree_cmd._materialize_plan_file(777, wt)

    plan = wt / ".endless" / "plans" / "E-777.md"
    assert plan.read_text() == "# materialized\n"
    # E-1525: the materialize step must also commit the file.
    log = subprocess.run(
        ["git", "-C", str(wt), "log", "--format=%s", "-n", "1"],
        capture_output=True, text=True, check=True,
    ).stdout.strip()
    assert log == "Endless: add plan for E-777"


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


def _fake_field_run_factory(by_field: dict[str, str]):
    """subprocess.run stub that returns per-field content keyed on --name.

    Passes `git` through to a real repo (for the commit step) and answers each
    `session-query task-field --name <f>` with by_field[f] (empty if absent).
    """
    real_run = subprocess.run

    def _run(argv, **kwargs):
        if argv and argv[0] == "git":
            return real_run(argv, **kwargs)
        assert "task-field" in argv
        name = argv[argv.index("--name") + 1]
        return types.SimpleNamespace(
            returncode=0, stdout=by_field.get(name, ""), stderr="",
        )
    return _run


def test_materialize_task_docs_seeds_all_fields(tmp_path, monkeypatch):
    """E-1747: worktree birth seeds plan/outcome/analysis mirrors, skipping
    fields with no content, and commits each present one."""
    wt = tmp_path / "wt"
    wt.mkdir()
    _git_init_wt(wt)
    monkeypatch.setattr(worktree_cmd.shutil, "which", lambda _b: "/fake/esq")
    monkeypatch.setattr(
        worktree_cmd.subprocess, "run",
        _fake_field_run_factory({
            "text": "# plan\n",
            "outcome": "done\n",
            # analysis intentionally absent → no file
        }),
    )

    worktree_cmd._materialize_task_docs(800, wt)

    assert (wt / ".endless" / "plans" / "E-800.md").read_text() == "# plan\n"
    assert (wt / ".endless" / "outcomes" / "E-800.md").read_text() == "done\n"
    assert not (wt / ".endless" / "analyses" / "E-800.md").exists()
    subjects = subprocess.run(
        ["git", "-C", str(wt), "log", "--format=%s"],
        capture_output=True, text=True, check=True,
    ).stdout.split("\n")
    assert "Endless: add plan for E-800" in subjects
    assert "Endless: add outcome for E-800" in subjects


# ─── --no-create-worktree is gone ─────────────────────────────────────────────


def test_cli_update_no_create_worktree_flag_removed(
    tmp_path, seeded_project_at_cwd,
):
    tid = _add_minimal_task()
    plan_src = tmp_path / "plan.md"
    plan_src.write_text("# plan\n")
    result = CliRunner().invoke(main, [
        "task", "update", f"E-{tid}",
        "--text-file", str(plan_src),
        "--no-create-worktree",
    ])
    assert result.exit_code != 0
    assert "no such option" in result.output.lower()


def test_cli_add_no_create_worktree_flag_removed(tmp_path, seeded_project_at_cwd):
    plan_src = tmp_path / "plan.md"
    plan_src.write_text("# plan\n")
    result = CliRunner().invoke(main, [
        "task", "add", "Refactor something new",
        "--text-file", str(plan_src),
        "--no-create-worktree",
    ])
    assert result.exit_code != 0
    assert "no such option" in result.output.lower()


def test_cli_update_text_succeeds_without_worktree(tmp_path, seeded_project_at_cwd):
    tid = _add_minimal_task()
    plan_src = tmp_path / "plan.md"
    plan_src.write_text("# plan\n")
    result = CliRunner().invoke(main, [
        "task", "update", f"E-{tid}", "--text-file", str(plan_src),
    ])
    assert result.exit_code == 0, result.output
    assert "Worktree created" not in result.output
    assert task_cmd._worktree_for_task(tid) is None

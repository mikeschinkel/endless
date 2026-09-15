"""E-2137: a task's document mirrors are written to MAIN, never to a worktree.

The contract:
  - `task update --plan/--outcome/--analysis` writes the database column (the
    source of truth) and writes the projection of it to
    `<main>/.endless/tasks/e-NNNN/<kind>.md`, committing it there.
  - Nothing is written into the task's worktree. Worktree birth materializes no
    mirrors at all.
  - Under a per-worktree sandbox database, nothing is written anywhere on disk:
    a file committed on the main checkout is real state, and a sandbox exists so
    a dev worktree cannot touch it.
  - `--no-create-worktree` is gone; these commands never create a worktree
    (E-1445, which this file also still covers).
"""

import subprocess
import types

from click.testing import CliRunner

from endless import config, db, doc_mirror, task_cmd, worktree_cmd
from endless.cli import main


def _add_minimal_task(title: str = "Refactor the cache layer") -> int:
    cur = db.execute(
        "INSERT INTO tasks (project_id, title, status, type_id, phase, created_at) "
        "VALUES (1, ?, 'unplanned', 1, 'now', datetime('now'))",
        (title,),
    )
    return cur.lastrowid


def _no_commit(monkeypatch):
    """Stub out the endless-go commit shellout, leaving the file write alone.

    Replaces the module's own reference to `shutil`, never `shutil.which`
    itself: patching the attribute on the real module would make every OTHER
    lookup of `endless-go` in the process fail too, which is a different test.
    """
    monkeypatch.setattr(
        doc_mirror, "shutil", types.SimpleNamespace(which=lambda _b: None))


# ─── helpers ──────────────────────────────────────────────────────────────────


def test_worktree_for_task_returns_none_when_absent(seeded_project_at_cwd):
    tid = _add_minimal_task()
    assert task_cmd._worktree_for_task(tid) is None


def test_main_root_for_task_resolves_registered_path(seeded_project_at_cwd):
    tid = _add_minimal_task()
    root = task_cmd._main_root_for_task(tid)
    assert root is not None
    assert root == seeded_project_at_cwd


# ─── the paths themselves ─────────────────────────────────────────────────────


def test_task_doc_paths_are_lowercase_and_task_scoped():
    assert doc_mirror.task_doc_path(2137, "plan") == ".endless/tasks/e-2137/plan.md"
    assert doc_mirror.task_doc_path(2137, "analysis") == ".endless/tasks/e-2137/analysis.md"
    assert doc_mirror.decision_doc_path(1550) == ".endless/decisions/ED-1550.md"


def test_a_tasks_own_verify_script_is_not_a_mirror():
    """The consolidated directory holds both kinds of file. Only the database's
    three are mirrors; the suite beside them belongs to the task."""
    assert doc_mirror.is_mirror_path(".endless/tasks/e-2137/plan.md")
    assert not doc_mirror.is_mirror_path(".endless/tasks/e-2137/verify.sh")
    assert not doc_mirror.is_mirror_path(".endless/tasks/e-2137/verify.toml")


def test_legacy_paths_are_still_recognized():
    """A tree the sweep has not reached yet still has mirrors in the old place."""
    assert doc_mirror.is_mirror_path(".endless/plans/E-2137.md")
    assert doc_mirror.is_mirror_path(".endless/outcomes/E-2137.md")
    assert doc_mirror.is_mirror_path(".endless/analyses/E-2137.md")


# ─── update/add --plan never create a worktree (E-1445) ───────────────────────


def test_update_plan_does_not_create_worktree(tmp_path, seeded_project_at_cwd,
                                              monkeypatch):
    _no_commit(monkeypatch)
    tid = _add_minimal_task()

    task_cmd.update_plan(tid, plan="# plan\nbody\n")

    assert task_cmd._worktree_for_task(tid) is None
    assert not (
        seeded_project_at_cwd / ".endless" / "worktrees" / f"e-{tid}"
    ).exists()
    row = db.query("SELECT plan FROM tasks WHERE id = ?", (tid,))
    assert row[0]["plan"] == "# plan\nbody\n"


def test_add_plan_does_not_create_worktree(tmp_path, seeded_project_at_cwd,
                                           monkeypatch):
    _no_commit(monkeypatch)
    item_id = task_cmd.add_item(title="Refactor the buffer", plan="# from add\nbody\n")

    assert task_cmd._worktree_for_task(item_id) is None
    assert not (
        seeded_project_at_cwd / ".endless" / "worktrees" / f"e-{item_id}"
    ).exists()
    row = db.query("SELECT plan FROM tasks WHERE id = ?", (item_id,))
    assert row[0]["plan"] == "# from add\nbody\n"


# ─── every kind lands on main ─────────────────────────────────────────────────


def test_update_plan_writes_the_mirror_on_main(seeded_project_at_cwd, monkeypatch):
    _no_commit(monkeypatch)
    tid = _add_minimal_task()

    task_cmd.update_plan(tid, plan="# v2\n")

    mirror = seeded_project_at_cwd / ".endless" / "tasks" / f"e-{tid}" / "plan.md"
    assert mirror.read_text() == "# v2\n"


def test_update_outcome_writes_the_mirror_on_main(seeded_project_at_cwd, monkeypatch):
    _no_commit(monkeypatch)
    tid = _add_minimal_task()

    task_cmd.update_plan(tid, outcome="Shipped the cache layer.\n")

    mirror = seeded_project_at_cwd / ".endless" / "tasks" / f"e-{tid}" / "outcome.md"
    assert mirror.read_text() == "Shipped the cache layer.\n"


def test_update_analysis_writes_the_mirror_on_main(seeded_project_at_cwd, monkeypatch):
    _no_commit(monkeypatch)
    tid = _add_minimal_task()

    task_cmd.update_plan(tid, analysis="# Analysis\nTradeoffs...\n")

    mirror = seeded_project_at_cwd / ".endless" / "tasks" / f"e-{tid}" / "analysis.md"
    assert mirror.read_text() == "# Analysis\nTradeoffs...\n"


def test_status_verb_outcome_also_lands_on_main(seeded_project_at_cwd, monkeypatch):
    """An outcome supplied to a status verb mirrors by the same route."""
    _no_commit(monkeypatch)
    tid = _add_minimal_task()

    task_cmd.complete_item(tid, outcome="Verified end to end.\n")

    mirror = seeded_project_at_cwd / ".endless" / "tasks" / f"e-{tid}" / "outcome.md"
    assert mirror.read_text() == "Verified end to end.\n"


def test_nothing_is_written_into_the_worktree(tmp_path, seeded_project_at_cwd,
                                              monkeypatch):
    """The regression this task exists to prevent: a mirror on a task branch."""
    _no_commit(monkeypatch)
    tid = _add_minimal_task()
    fake_wt = tmp_path / "wt"
    fake_wt.mkdir()
    monkeypatch.setattr(task_cmd, "_worktree_for_task", lambda _tid: fake_wt)

    task_cmd.update_plan(tid, plan="# v2\n", outcome="done\n", analysis="why\n")

    assert list(fake_wt.iterdir()) == []


# ─── the commit that carries it ───────────────────────────────────────────────


def test_first_write_says_add_and_later_writes_say_update(
    seeded_project_at_cwd, monkeypatch,
):
    """Two subjects, not one: the first commit for a mirror is distinguishable
    from every later one, and identical subjects are what let a re-commit amend
    rather than pile up."""
    calls = []

    def _fake_run(argv, **kwargs):
        calls.append(argv)
        return types.SimpleNamespace(returncode=0, stdout="", stderr="")

    monkeypatch.setattr(
        doc_mirror, "shutil", types.SimpleNamespace(which=lambda _b: "/fake/endless-go"))
    monkeypatch.setattr(doc_mirror, "subprocess", types.SimpleNamespace(run=_fake_run))

    tid = _add_minimal_task()
    task_cmd.update_plan(tid, plan="# one\n")
    task_cmd.update_plan(tid, plan="# two\n")

    subjects = [a[a.index("--subject") + 1] for a in calls if "--subject" in a]
    assert subjects == [
        f"Endless: add plan for E-{tid}",
        f"Endless: update plan for E-{tid}",
    ]
    paths = [a[a.index("--path") + 1] for a in calls if "--path" in a]
    assert paths == [f".endless/tasks/e-{tid}/plan.md"] * 2


def test_a_failed_commit_warns_and_keeps_the_file(seeded_project_at_cwd,
                                                  monkeypatch, capsys):
    """The database write already happened and is authoritative; failing the
    command here would report failure for work that succeeded. The sweep
    repairs the commit later."""
    monkeypatch.setattr(
        doc_mirror, "shutil", types.SimpleNamespace(which=lambda _b: "/fake/endless-go"))
    monkeypatch.setattr(
        doc_mirror, "subprocess",
        types.SimpleNamespace(run=lambda *a, **k: types.SimpleNamespace(
            returncode=1, stdout="", stderr="index.lock exists\n")),
    )

    tid = _add_minimal_task()
    task_cmd.update_plan(tid, plan="# body\n")

    mirror = seeded_project_at_cwd / ".endless" / "tasks" / f"e-{tid}" / "plan.md"
    assert mirror.read_text() == "# body\n"
    assert "could not commit" in capsys.readouterr().err
    row = db.query("SELECT plan FROM tasks WHERE id = ?", (tid,))
    assert row[0]["plan"] == "# body\n"


# ─── the sandbox rule ─────────────────────────────────────────────────────────


def test_sandbox_context_writes_nothing(seeded_project_at_cwd, monkeypatch):
    """A sandbox exists so a dev worktree cannot touch real state, and a file
    committed on the main checkout IS real state. The mirror follows the ledger,
    which skips its auto-commit under a sandbox for the same reason."""
    _no_commit(monkeypatch)
    monkeypatch.setattr(config, "db_context_is_sandbox", lambda: True)

    tid = _add_minimal_task()
    task_cmd.update_plan(tid, plan="# sandbox\n")

    assert not (seeded_project_at_cwd / ".endless" / "tasks" / f"e-{tid}").exists()
    row = db.query("SELECT plan FROM tasks WHERE id = ?", (tid,))
    assert row[0]["plan"] == "# sandbox\n"   # the column is still written


# ─── worktree birth materializes nothing ─────────────────────────────────────


def test_worktree_birth_materializes_no_mirrors(seeded_project_at_cwd, monkeypatch):
    """A newborn worktree gets no mirror WRITTEN into it, and its branch holds
    no commit of its own.

    It does get main's mirrors, because `git worktree add` checks out main and
    they are on main — an ordinary tracked file at the fork point, not something
    Endless put there. That is the "materialize once at worktree creation and
    let a rebase refresh it" the plan asked for, arrived at for free. The copy
    goes stale as main moves on, exactly as `.endless/db-ledger/` already does;
    nothing reads it, and the Claude hook refuses a hand-edit of it.

    What must never come back is the second WRITER: a `task update` that puts a
    new commit on this branch.
    """
    _no_commit(monkeypatch)
    tid = _add_minimal_task()
    task_cmd.update_plan(tid, plan="# a real plan\n")
    # The stub above skipped the commit endless-go normally makes, and ED-1169's
    # guard correctly refuses to claim over an uncommitted mirror. Commit it the
    # way that guard recommends, so this test is about materialization.
    rel = f".endless/tasks/e-{tid}/plan.md"
    subprocess.run(["git", "-C", str(seeded_project_at_cwd), "add", rel], check=True)
    subprocess.run(
        ["git", "-C", str(seeded_project_at_cwd), "commit", "-q", "-m",
         f"Endless: add plan for E-{tid}"], check=True)

    wt, created = worktree_cmd.create_task_worktree(tid, seeded_project_at_cwd)

    assert created is True
    for name in ("plans", "outcomes", "analyses"):
        assert not (wt / ".endless" / name).exists()
    # Inherited from main, unmodified — so the worktree reads clean.
    inherited = wt / ".endless" / "tasks" / f"e-{tid}" / "plan.md"
    assert inherited.read_text() == "# a real plan\n"
    status = subprocess.run(
        ["git", "-C", str(wt), "status", "--porcelain", "--",
         f".endless/tasks/e-{tid}"],
        capture_output=True, text=True, check=True,
    ).stdout.strip()
    assert status == ""
    # And the branch holds no commit of its own.
    count = subprocess.run(
        ["git", "-C", str(wt), "rev-list", "--count", "main..HEAD"],
        capture_output=True, text=True, check=True,
    ).stdout.strip()
    assert count == "0"


def test_later_updates_add_no_commit_to_the_branch(seeded_project_at_cwd,
                                                   monkeypatch):
    """The regression that matters after the worktree exists: `task update`
    must write to main and leave the branch where it was."""
    _no_commit(monkeypatch)
    tid = _add_minimal_task()
    wt, _ = worktree_cmd.create_task_worktree(tid, seeded_project_at_cwd)
    before = subprocess.run(
        ["git", "-C", str(wt), "rev-parse", "HEAD"],
        capture_output=True, text=True, check=True,
    ).stdout.strip()

    task_cmd.update_plan(tid, plan="# written after the worktree existed\n")

    after = subprocess.run(
        ["git", "-C", str(wt), "rev-parse", "HEAD"],
        capture_output=True, text=True, check=True,
    ).stdout.strip()
    assert after == before
    # Scoped to the mirror paths: worktree birth also writes the untracked
    # companion marker and scratch dir, which are not this test's subject.
    assert subprocess.run(
        ["git", "-C", str(wt), "status", "--porcelain", "--",
         ".endless/tasks", ".endless/plans", ".endless/outcomes",
         ".endless/analyses", ".endless/decisions"],
        capture_output=True, text=True, check=True,
    ).stdout.strip() == ""
    mirror = seeded_project_at_cwd / ".endless" / "tasks" / f"e-{tid}" / "plan.md"
    assert mirror.read_text() == "# written after the worktree existed\n"


# ─── --no-create-worktree is gone (E-1445) ───────────────────────────────────


def test_cli_update_no_create_worktree_flag_removed(
    tmp_path, seeded_project_at_cwd,
):
    tid = _add_minimal_task()
    plan_src = tmp_path / "plan.md"
    plan_src.write_text("# plan\n")
    result = CliRunner().invoke(main, [
        "task", "update", f"E-{tid}",
        "--plan-file", str(plan_src),
        "--no-create-worktree",
    ])
    assert result.exit_code != 0
    assert "no such option" in result.output.lower()


def test_cli_add_no_create_worktree_flag_removed(tmp_path, seeded_project_at_cwd):
    plan_src = tmp_path / "plan.md"
    plan_src.write_text("# plan\n")
    result = CliRunner().invoke(main, [
        "task", "add", "Refactor something new",
        "--plan-file", str(plan_src),
        "--no-create-worktree",
    ])
    assert result.exit_code != 0
    assert "no such option" in result.output.lower()


def test_cli_update_plan_succeeds_without_worktree(tmp_path, seeded_project_at_cwd,
                                                   monkeypatch):
    _no_commit(monkeypatch)
    tid = _add_minimal_task()
    plan_src = tmp_path / "plan.md"
    plan_src.write_text("# plan\n")
    result = CliRunner().invoke(main, [
        "task", "update", f"E-{tid}", "--plan-file", str(plan_src),
    ])
    assert result.exit_code == 0, result.output
    assert "Worktree created" not in result.output
    assert task_cmd._worktree_for_task(tid) is None

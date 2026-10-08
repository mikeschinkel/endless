"""The `unlanded` status and the land that settles it (E-2262).

Driven against the endless-go this checkout builds and a real git repository:
`verify-state` reads the test database and diffs the task branch, so nothing
here is stubbed except the line between an agent's run and the user's.
"""

import subprocess

import click
import pytest

from endless import db, unlanded

# These tests ARE the gate's tests, so the conftest stub that opens it for the
# rest of the land suite stays out of the way.
pytestmark = pytest.mark.real_land_gate

TASK = 5150

# What agent_env keys on. A test that wants the user's run clears both; this
# suite itself usually runs under Claude Code, which sets the first.
_AGENT_VARS = ("CLAUDE_CODE_ENTRYPOINT", "__CFBundleIdentifier")


def _git(cwd, *args) -> str:
    return subprocess.run(["git", "-C", str(cwd), *args], check=True,
                          capture_output=True, text=True).stdout.strip()


@pytest.fixture
def as_user(monkeypatch):
    for var in _AGENT_VARS:
        monkeypatch.delenv(var, raising=False)


@pytest.fixture
def as_agent(monkeypatch):
    monkeypatch.setenv("CLAUDE_CODE_ENTRYPOINT", "cli")


@pytest.fixture
def repo(seeded_project_at_cwd):
    """The project's repository with TASK's branch checked out in a worktree
    that carries a verify suite."""
    root = seeded_project_at_cwd
    wt = root / ".endless" / "worktrees" / f"e-{TASK}"
    _git(root, "worktree", "add", "-q", "-b", f"task/{TASK}", str(wt))
    _git(wt, "config", "user.email", "t@example.com")
    _git(wt, "config", "user.name", "T")
    _commit(wt, f".endless/tasks/e-{TASK}/verify.sh", "exit 0\n")
    return {"root": root, "wt": wt}


def _commit(wt, rel, content):
    path = wt / rel
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content)
    _git(wt, "add", "-A")
    _git(wt, "commit", "-q", "-m", f"change {rel}")


def _seed_task(status, type_slug="todo", task_id=TASK):
    db.execute(
        "INSERT INTO tasks (id, project_id, title, status, type_id, phase) "
        "VALUES (?, (SELECT id FROM projects WHERE name = 'test'), ?, ?, "
        "(SELECT id FROM task_types WHERE slug = ?), 'now')",
        (task_id, f"task {task_id}", status, type_slug),
    )


def _state(task_id=TASK) -> dict:
    return unlanded.verify_states(task_id)[0]


def _head(wt) -> str:
    return _git(wt, "rev-parse", "HEAD")


# --- who sets `unlanded` ----------------------------------------------------


def test_a_users_pass_sets_unlanded_at_the_passed_commit(repo, as_user):
    _seed_task("unverified")
    unlanded.record_pass(TASK, _head(repo["wt"]))
    state = _state()
    assert state["status"] == "unlanded"
    assert state["verified_sha"] == _head(repo["wt"])
    assert state["stale"] is False


def test_an_agents_pass_does_not(repo, as_agent, capsys):
    _seed_task("unverified")
    unlanded.record_pass(TASK, _head(repo["wt"]))
    assert _state()["status"] == "unverified"
    assert "only a verify you run yourself" in capsys.readouterr().out


def test_a_pass_on_a_task_not_awaiting_verify_changes_nothing(repo, as_user):
    _seed_task("underway")
    unlanded.record_pass(TASK, _head(repo["wt"]))
    assert _state()["status"] == "underway"


def test_reverifying_an_unlanded_task_records_the_newer_commit(repo, as_user):
    _seed_task("unverified")
    unlanded.record_pass(TASK, _head(repo["wt"]))
    _commit(repo["wt"], "code.py", "x = 2\n")
    unlanded.record_pass(TASK, _head(repo["wt"]))
    state = _state()
    assert (state["status"], state["verified_sha"]) == ("unlanded", _head(repo["wt"]))


# --- staleness ---------------------------------------------------------------


def test_a_commit_after_the_pass_returns_the_task_to_unverified(repo, as_user):
    _seed_task("unverified")
    unlanded.record_pass(TASK, _head(repo["wt"]))
    _commit(repo["wt"], "code.py", "x = 1\n")

    reset = unlanded.reconcile(TASK)

    assert [s["task_id"] for s in reset] == [TASK]
    assert "has changed since the verify passed" in reset[0]["stale_reason"]
    assert _state()["status"] == "unverified"


def test_a_ledger_commit_after_the_pass_does_not(repo, as_user):
    _seed_task("unverified")
    unlanded.record_pass(TASK, _head(repo["wt"]))
    _commit(repo["wt"], ".endless/db-ledger/2026-10.jsonl", "{}\n")

    assert unlanded.reconcile() == []
    assert _state()["status"] == "unlanded"


def test_a_read_announces_the_reset_on_stderr(repo, as_user, capsys):
    _seed_task("unverified")
    unlanded.record_pass(TASK, _head(repo["wt"]))
    _commit(repo["wt"], "code.py", "x = 1\n")
    capsys.readouterr()

    unlanded.reconcile_quietly()

    captured = capsys.readouterr()
    assert captured.out == ""
    assert f"E-{TASK} is unverified again" in captured.err


# --- the land gate -----------------------------------------------------------


def test_land_refuses_research_and_brainstorm(repo):
    for offset, slug in enumerate(("research", "brainstorm")):
        _seed_task("underway", slug, task_id=TASK + 1 + offset)
        with pytest.raises(click.ClickException) as exc:
            unlanded.require_landable(TASK + 1 + offset, repo["wt"])
        assert "nothing to land" in exc.value.message


def test_land_refuses_a_todo_that_is_not_unlanded(repo):
    _seed_task("unverified")
    with pytest.raises(click.ClickException) as exc:
        unlanded.require_landable(TASK, repo["wt"])
    assert f"E-{TASK} is unverified, not unlanded" in exc.value.message


def test_land_refuses_a_stale_pass_and_says_why(repo, as_user):
    _seed_task("unverified")
    unlanded.record_pass(TASK, _head(repo["wt"]))
    _commit(repo["wt"], "code.py", "x = 1\n")
    with pytest.raises(click.ClickException) as exc:
        unlanded.require_landable(TASK, repo["wt"])
    assert "It was `unlanded` until just now" in exc.value.message


def test_land_refuses_a_todo_with_no_suite(repo):
    _git(repo["wt"], "rm", "-q", "-r", f".endless/tasks/e-{TASK}")
    _git(repo["wt"], "commit", "-q", "-m", "drop suite")
    _seed_task("unverified")
    with pytest.raises(click.ClickException) as exc:
        unlanded.require_landable(TASK, repo["wt"])
    assert "has none" in exc.value.message


def test_land_accepts_a_fresh_unlanded_todo(repo, as_user):
    _seed_task("unverified")
    unlanded.record_pass(TASK, _head(repo["wt"]))
    state = unlanded.require_landable(TASK, repo["wt"])
    assert (state["status"], state["settles_on_land"]) == ("unlanded", True)


def test_an_epic_without_a_suite_is_not_gated(repo):
    _seed_task("underway", "epic", task_id=TASK + 9)
    state = unlanded.require_landable(TASK + 9, repo["wt"])
    assert state["settles_on_land"] is False


# --- settling ----------------------------------------------------------------


def test_land_settles_a_todo_as_assumed(repo, as_user):
    _seed_task("unverified")
    unlanded.record_pass(TASK, _head(repo["wt"]))
    unlanded.settle(unlanded.require_landable(TASK, repo["wt"]), keep_status=False)
    state = _state()
    assert state["status"] == "assumed"
    assert state.get("verified_sha") is None


def test_keep_status_leaves_it_unlanded(repo, as_user):
    _seed_task("unverified")
    unlanded.record_pass(TASK, _head(repo["wt"]))
    unlanded.settle(unlanded.require_landable(TASK, repo["wt"]), keep_status=True)
    assert _state()["status"] == "unlanded"


def test_worktree_land_runs_the_gate_before_anything_moves(repo, monkeypatch):
    """The gate is wired into `worktree land` itself, ahead of even the dry
    run's report: nothing is rehearsed, merged or recorded for a task that may
    not land."""
    from endless import worktree_cmd

    _seed_task("unverified")
    monkeypatch.setattr(worktree_cmd, "_enriched_list", lambda root: [])
    monkeypatch.setattr(
        worktree_cmd, "_branch_for_task",
        lambda rows, canonical: {"branch": f"task/{TASK}", "path": str(repo["wt"]),
                                 "companion": {"base_branch": "main"}},
    )
    monkeypatch.setattr(worktree_cmd, "_project_root", lambda: repo["root"])

    def must_not_run(*a, **k):
        raise AssertionError("the land went past its gate")

    monkeypatch.setattr(worktree_cmd, "_refuse_if_land_gated", must_not_run)
    with pytest.raises(click.ClickException) as exc:
        worktree_cmd.land_worktree(f"E-{TASK}", dry_run=True)
    assert "not unlanded" in exc.value.message

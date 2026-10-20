"""E-2405: spawn, prime and claim a task in its OWN project's directory.

Every test registers two projects, each a git repository, and runs from the
first ("here") on a task that belongs to the second ("there"). The worktree,
the hook and the events all belong to the task's project, never to cwd's.
"""

import subprocess
from pathlib import Path

import click
import pytest

from endless import db, event_bridge, task_cmd


HOOK = ".endless/hooks/post-worktree-create.sh"


def _write_marker_hook(repo: Path) -> Path:
    """A post-worktree-create hook that leaves a marker file in its repo."""
    marker = repo / "hook-ran"
    hook = repo / HOOK
    hook.parent.mkdir(parents=True, exist_ok=True)
    hook.write_text(f"#!/bin/sh\ntouch '{marker}'\n")
    hook.chmod(0o755)
    subprocess.run(["git", "add", HOOK], cwd=str(repo), check=True,
                   capture_output=True)
    subprocess.run(["git", "-c", "user.email=t@t.t", "-c", "user.name=t",
                    "commit", "-q", "-m", "hook"], cwd=str(repo), check=True,
                   capture_output=True)
    return marker


def _worktrees(repo: Path) -> list[str]:
    out = subprocess.run(["git", "worktree", "list", "--porcelain"],
                         cwd=str(repo), check=True, capture_output=True,
                         text=True).stdout
    return [l[len("worktree "):] for l in out.splitlines()
            if l.startswith("worktree ")][1:]


@pytest.fixture
def two_projects(seeded_project_at_cwd, tmp_path, git_repo):
    """cwd is project `test`; project `there` owns one ready, planned task."""
    here = seeded_project_at_cwd
    there = git_repo(tmp_path / "there")
    db.execute(
        "INSERT INTO projects (name, path, status, created_at, updated_at) "
        "VALUES ('there', ?, 'active', datetime('now'), datetime('now'))",
        (str(there),),
    )
    there_id = db.query("SELECT id FROM projects WHERE name = 'there'")[0]["id"]
    db.execute(
        "INSERT INTO tasks (project_id, title, description, status, sort_order, "
        "created_at, updated_at) VALUES (?, 'Fix the thing', 'Fix the thing', "
        "'ready', 0, datetime('now'), datetime('now'))",
        (there_id,),
    )
    tid = db.query("SELECT id FROM tasks WHERE title = 'Fix the thing'")[0]["id"]
    db.execute("INSERT INTO task_content (task_id, name, content) "
               "VALUES (?, 'plan', '# Plan')", (tid,))
    return {"here": here, "there": there, "there_id": there_id, "task_id": tid}


@pytest.fixture
def emitted(monkeypatch):
    """Every event emitted, as (kind, project), passed through to the real emit."""
    calls: list[tuple[str, str]] = []
    real = event_bridge.emit_event

    def spy(*args, **kwargs):
        calls.append((kwargs.get("kind"), kwargs.get("project")))
        return real(*args, **kwargs)

    monkeypatch.setattr(event_bridge, "emit_event", spy)
    return calls


@pytest.fixture
def launch(monkeypatch):
    """Satisfy the launch gates and capture the spawn-window call; every other
    subprocess (git, the hook, endless-go) runs for real."""
    import shutil
    from endless import plan_items, tmux_cmd

    real_which = shutil.which
    monkeypatch.setattr(shutil, "which",
                        lambda n: "/usr/bin/tmux" if n == "tmux" else real_which(n))
    monkeypatch.setattr(tmux_cmd, "tasks_server",
                        lambda: ("/s/endless-tasks", "tasks"))
    monkeypatch.setattr(task_cmd, "_claude_binary", lambda: "claude")
    monkeypatch.setattr(task_cmd, "render_handoff", lambda *a, **k: "HANDOFF")
    monkeypatch.setattr(task_cmd, "_open_questions", lambda item_id: [])
    monkeypatch.setattr(plan_items, "require_deliverable", lambda *a, **k: None)
    monkeypatch.setattr(plan_items, "require_approved", lambda *a, **k: None)

    calls: list[list[str]] = []
    real_run = subprocess.run

    def run(cmd, *a, **k):
        if "spawn-window" in cmd:
            calls.append(list(cmd))
            return subprocess.CompletedProcess(cmd, 0, "", "")
        return real_run(cmd, *a, **k)

    monkeypatch.setattr(subprocess, "run", run)
    return calls


def _wt(repo: Path, tid: int) -> Path:
    return repo / ".endless" / "worktrees" / f"e-{tid}"


# --- spawn (P1, P2, P3, P9) --------------------------------------------------

def test_spawn_creates_the_worktree_in_the_tasks_project(two_projects, launch):
    tid = two_projects["task_id"]

    task_cmd.spawn_plan(tid)

    assert _wt(two_projects["there"], tid).is_dir()
    assert _worktrees(two_projects["here"]) == []
    cmd = launch[0]
    assert cmd[cmd.index("--cwd") + 1] == str(_wt(two_projects["there"], tid))
    assert cmd[cmd.index("--project-id") + 1] == str(two_projects["there_id"])


def test_spawn_tags_its_events_with_the_tasks_project(two_projects, launch,
                                                      emitted):
    task_cmd.spawn_plan(two_projects["task_id"])

    assert ("task.status_changed", "there") in emitted
    assert all(p == "there" for _, p in emitted), emitted


def test_spawn_runs_the_tasks_projects_hook(two_projects, launch):
    here_marker = _write_marker_hook(two_projects["here"])
    there_marker = _write_marker_hook(two_projects["there"])

    task_cmd.spawn_plan(two_projects["task_id"])

    assert there_marker.exists()
    assert not here_marker.exists()


def test_spawn_names_the_project_when_it_is_not_cwds(two_projects, launch,
                                                     capsys):
    task_cmd.spawn_plan(two_projects["task_id"])

    out = capsys.readouterr().out
    assert "• Project:" in out
    assert out.index("there") < out.index("Git worktree")


def test_spawn_in_cwds_own_project_does_not_name_it(two_projects, launch,
                                                    capsys):
    here_id = db.query("SELECT id FROM projects WHERE name = 'test'")[0]["id"]
    db.execute("UPDATE tasks SET project_id = ? WHERE id = ?",
               (here_id, two_projects["task_id"]))

    task_cmd.spawn_plan(two_projects["task_id"])

    assert "• Project:" not in capsys.readouterr().out


# --- prime (P3, P4, P9) ------------------------------------------------------

def test_prime_creates_the_worktree_in_the_tasks_project(two_projects, launch,
                                                         capsys):
    here_marker = _write_marker_hook(two_projects["here"])
    there_marker = _write_marker_hook(two_projects["there"])
    tid = two_projects["task_id"]

    task_cmd.prime_task(tid)

    assert _wt(two_projects["there"], tid).is_dir()
    assert _worktrees(two_projects["here"]) == []
    assert there_marker.exists() and not here_marker.exists()
    out = capsys.readouterr().out
    assert out.index("there") < out.index("Git worktree")


# --- claim (P5) --------------------------------------------------------------

def test_claim_across_projects_refuses_and_names_spawn(two_projects, emitted):
    tid = two_projects["task_id"]

    with pytest.raises(click.ClickException) as exc:
        task_cmd.claim_item(tid, unattended=True)

    msg = str(exc.value)
    assert f"endless task spawn E-{tid}" in msg
    assert "there" in msg
    assert db.query("SELECT status FROM tasks WHERE id = ?",
                    (tid,))[0]["status"] == "ready"
    assert _worktrees(two_projects["here"]) == []
    assert _worktrees(two_projects["there"]) == []
    assert emitted == []


# --- unsettled E-N (P8) ------------------------------------------------------

def test_unsettled_inspects_the_tasks_projects_worktree(two_projects, launch,
                                                        capsys):
    tid = two_projects["task_id"]
    task_cmd.spawn_plan(tid)
    capsys.readouterr()

    task_cmd.unsettled_item(tid, as_json=True)

    import json
    out = capsys.readouterr().out
    data = json.loads(out[out.index("{"):])
    assert data["has_worktree"] is True


# --- session goto --resume (P7) ----------------------------------------------

def test_resume_recreates_a_dropped_worktree_in_the_tasks_project(
        two_projects, monkeypatch):
    from endless import session_cmd, worktree_cmd

    tid = two_projects["task_id"]
    seen: list[Path] = []
    monkeypatch.setattr(
        worktree_cmd, "recreate_dropped_worktree",
        lambda task_id, root, base, detached: seen.append(root) or root)
    monkeypatch.setattr(session_cmd, "_resolve_recovery_base",
                        lambda *a: "main")

    session_cmd._recover_dropped_worktree(
        {"task_id": tid, "task_status": "ready", "task_type": "bugfix",
         "project_id": two_projects["there_id"]},
        "review", None)

    assert seen == [two_projects["there"]]


# --- a missing project path (P10) --------------------------------------------

@pytest.mark.parametrize("path", ["", "/no/such/dir"])
def test_every_command_refuses_a_missing_project_path(two_projects, launch,
                                                      emitted, path):
    from endless import session_cmd

    db.execute("UPDATE projects SET path = ? WHERE name = 'there'", (path,))
    tid = two_projects["task_id"]
    calls = [
        lambda: task_cmd.spawn_plan(tid),
        lambda: task_cmd.prime_task(tid),
        lambda: task_cmd.claim_item(tid, unattended=True),
        lambda: task_cmd.unsettled_item(tid),
        lambda: session_cmd._recover_dropped_worktree(
            {"task_id": tid, "task_status": "ready", "task_type": "bugfix",
             "project_id": two_projects["there_id"]}, "review", None),
    ]
    for call in calls:
        with pytest.raises(click.ClickException) as exc:
            call()
        msg = str(exc.value)
        assert "there" in msg
        assert (path or "no registered path") in msg
    assert db.query("SELECT status FROM tasks WHERE id = ?",
                    (tid,))[0]["status"] == "ready"
    assert _worktrees(two_projects["here"]) == []
    assert emitted == []
    assert launch == []


# --- the spawning session tracks what it spawned (P16) -----------------------

def _seed_session(session_id: int, project_id: int) -> None:
    db.execute(
        "INSERT INTO sessions (id, session_id, project_id, state) "
        "VALUES (?, ?, ?, 'working')",
        (session_id, f"uuid-{session_id}", project_id),
    )


def test_spawn_records_the_task_on_the_spawning_session(two_projects, launch,
                                                        emitted):
    here_id = db.query("SELECT id FROM projects WHERE name = 'test'")[0]["id"]
    _seed_session(1, here_id)
    tid = two_projects["task_id"]

    task_cmd.spawn_plan(tid)

    assert ("session_tasks.spawned", "there") in emitted
    rel = db.query(
        "SELECT r.slug FROM session_tasks st JOIN session_task_relations r "
        "ON r.id = st.relation_id WHERE st.session_id = 1 AND st.task_id = ?",
        (tid,))
    assert [r["slug"] for r in rel] == ["spawned"]


def test_an_auto_spawn_records_no_spawning_session(two_projects, launch,
                                                   emitted):
    task_cmd.spawn_plan(two_projects["task_id"], auto=True)

    assert "session_tasks.spawned" not in [k for k, _ in emitted]

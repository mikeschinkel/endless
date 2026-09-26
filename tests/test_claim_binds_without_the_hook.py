"""`task claim` does the whole job, with no help from the Claude hook (E-2177).

The PostToolUse hook used to regex the Bash command text for a claim, then bind
the session and render the claim handoff itself. It fired on any heredoc, quoted
argument or commit message that merely NAMED a claim, so the write moved out of
the hook entirely. These tests prove nothing was lost:

  - the bind and the `underway` promotion happen through the CLI and the event
    executor alone, from the in-process session rung (`CLAUDECODE=1` +
    `CLAUDE_CODE_SESSION_ID`);
  - the session is `working` once its next hook event lands — the one thing the
    hook's write did that the executor does not;
  - the claim handoff (E-1822) is printed by the claim itself, to an agent only.
"""

import json
import subprocess
import uuid

import pytest

from endless import config, db, task_cmd
from endless.event_bridge import _resolve_endless_go


@pytest.fixture
def ready_task(seeded_project_at_cwd):
    proj_id = db.query(
        "SELECT id FROM projects WHERE path = ?", (str(seeded_project_at_cwd),)
    )[0]["id"]
    title = "Bind without the hook"
    db.execute(
        "INSERT INTO tasks (project_id, title, description, status, sort_order, "
        "created_at, updated_at) VALUES (?, ?, ?, 'ready', 0, "
        "datetime('now'), datetime('now'))",
        (proj_id, title, title),
    )
    task_id = db.query("SELECT id FROM tasks WHERE title = ?", (title,))[0]["id"]
    return {"root": seeded_project_at_cwd, "task_id": task_id}


def _as_claude_session(monkeypatch) -> str:
    """Make this process a Claude Code CLI session, exactly as its Bash tool
    would: the in-process rung of the session ladder, and a detectable harness.
    """
    guid = str(uuid.uuid4())
    monkeypatch.setenv("CLAUDECODE", "1")
    monkeypatch.setenv("CLAUDE_CODE_SESSION_ID", guid)
    monkeypatch.setenv("CLAUDE_CODE_ENTRYPOINT", "cli")
    return guid


def _session(guid: str) -> dict:
    return db.query(
        "SELECT task_id, state FROM sessions WHERE session_id = ?", (guid,)
    )[0]


def _status(task_id: int) -> str:
    return db.query("SELECT status FROM tasks WHERE id = ?", (task_id,))[0]["status"]


def _fire_hook(guid: str, cwd) -> None:
    """Deliver one real PostToolUse event for the session through the hook."""
    payload = {
        "hook_event_name": "PostToolUse",
        "session_id": guid,
        "cwd": str(cwd),
        "tool_name": "Bash",
        "tool_input": {"command": "ls"},
    }
    result = subprocess.run(
        [_resolve_endless_go(), *config.go_db_context_args(), "hook", "claude"],
        input=json.dumps(payload), capture_output=True, text=True, check=False,
    )
    assert result.returncode == 0, result.stderr


@pytest.mark.no_session_stub
def test_claim_binds_promotes_and_wakes_without_the_hook(
    ready_task, monkeypatch, capsys,
):
    guid = _as_claude_session(monkeypatch)
    tid = ready_task["task_id"]

    task_cmd.claim_item(tid)
    capsys.readouterr()

    session = _session(guid)
    assert session["task_id"] == tid, "the CLI + executor must bind the session"
    assert _status(tid) == "underway"

    _fire_hook(guid, ready_task["root"])
    assert _session(guid)["state"] == "working"


@pytest.mark.no_session_stub
def test_an_agents_claim_prints_the_handoff(ready_task, monkeypatch, capsys):
    _as_claude_session(monkeypatch)
    tid = ready_task["task_id"]

    task_cmd.claim_item(tid)
    out = capsys.readouterr().out

    assert f"You just claimed E-{tid} into an **already-running** session" in out
    assert f"Stay focused on E-{tid} — one session, one task." in out


def test_a_persons_claim_prints_no_handoff(ready_task, monkeypatch, capsys):
    """No harness in the environment: a person bound a session in another pane.
    They get the next step, not an agent's instructions."""
    monkeypatch.setattr(
        task_cmd, "_resolve_session_id_with_prompt", lambda **kw: 1,
    )
    monkeypatch.setattr(task_cmd, "_in_claude_session", lambda: False)
    task_cmd.claim_item(ready_task["task_id"])
    out = capsys.readouterr().out

    assert "already-running" not in out
    assert "Stay focused on" not in out


@pytest.mark.no_session_stub
def test_an_unattended_claim_prints_no_handoff_and_binds_nothing(
    ready_task, monkeypatch, capsys,
):
    guid = _as_claude_session(monkeypatch)
    tid = ready_task["task_id"]

    task_cmd.claim_item(tid, unattended=True)
    out = capsys.readouterr().out

    assert "already-running" not in out
    rows = db.query("SELECT task_id FROM sessions WHERE session_id = ?", (guid,))
    assert not rows or rows[0]["task_id"] is None

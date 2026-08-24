"""E-1918: `ES-` refs everywhere, and resuming a session that never claimed a task.

Two threads, both pinned here at the unit level (the real DB/git/worktree side is
tests/tasks/e-1918-verify.sh):

  1. Every session-ref entry point accepts `ES-<id>`. The Go resolver is covered
     by internal/monitor/resume_test.go; the live-pane matcher is covered here.
  2. A task-less session gets a container task minted and claimed so it has a
     worktree to be resumed into, instead of the old flat refusal.
"""

import click
import pytest

from endless import db, session_cmd


# ── thread 1: `_match_companions` — session show / cd / use ──────────────────

def _companion(eid: int, uuid: str) -> dict:
    return {"endless_session_id": eid, "harness_session_id": uuid, "pane_id": "%1"}


@pytest.mark.parametrize("ref", ["963", "ES-963", "es-963", "Es-963"])
def test_match_companions_accepts_es_prefix(ref):
    live = [_companion(963, "uuid-a"), _companion(964, "uuid-b")]
    matches = session_cmd._match_companions(live, ref)
    assert [c["endless_session_id"] for c in matches] == [963]


def test_match_companions_es_prefix_does_not_become_a_uuid_prefix():
    """The pre-E-1918 failure mode: `ES-963` skipped the isdigit() branch and was
    matched as a UUID prefix, so it matched nothing at all."""
    live = [_companion(963, "ES-963-looks-like-a-uuid")]
    assert session_cmd._match_companions(live, "ES-963") == live


# ── thread 2: the task-less resume ───────────────────────────────────────────

def _taskless(**over) -> dict:
    base = {
        "endless_id": 963,
        "session_id": "uuid-963",
        "task_id": None,
        "worktree_path": "",
        "state": "idle",
        "project_id": 1,
        "project_path": "/tmp/proj",
    }
    base.update(over)
    return base


@pytest.fixture
def stub_create(monkeypatch, tmp_path):
    """Stub the task-creation + claim half, recording its kwargs."""
    from endless import task_cmd
    calls = {}

    def fake_create(**kw):
        calls.update(kw)
        return 1970, tmp_path / "e-1970"

    monkeypatch.setattr(task_cmd, "create_claimed_task_for_session", fake_create)
    return calls


def _seed_project(name="probe", path="/tmp/proj"):
    db.execute(
        "INSERT INTO projects (id, name, path) VALUES (1, ?, ?)", (name, path)
    )


def test_taskless_session_gets_a_claimed_task_and_worktree(
    monkeypatch, stub_create, tmp_path
):
    _seed_project(path=str(tmp_path / "proj"))
    monkeypatch.setattr(
        session_cmd, "_resume_target",
        lambda ref: _taskless(project_path=str(tmp_path / "proj")),
    )
    decision: dict = {}
    uuid, wt, label, eid = session_cmd._resolve_resume(
        "ES-963", decision_out=decision
    )

    assert uuid == "uuid-963"
    assert wt == str(tmp_path / "e-1970")
    assert label == "E-1970"
    assert eid == 963
    assert decision["created_task"] is True
    assert decision["task_id"] == 1970
    assert decision["recovered"] is False

    # The title is the pinned placeholder, and the task is created in the
    # RESUMED session's project — not in whatever project cwd names.
    assert stub_create["title"] == "Auto-resumed task for session ES-963"
    assert stub_create["project_name"] == "probe"
    assert str(stub_create["project_root"]) == str(tmp_path / "proj")
    assert stub_create["session_id"] == 963


def test_description_names_the_tasks_the_session_touched():
    db.execute(
        "INSERT INTO session_tasks (session_id, task_id, relation_id, "
        "created_at, updated_at) VALUES "
        "(963, 1776, 3, '2026-08-01', '2026-08-01'), "
        "(963, 1801, 3, '2026-08-01', '2026-08-01'), "
        "(964, 1999, 3, '2026-08-01', '2026-08-01')"
    )
    desc = session_cmd._taskless_resume_description(963)
    assert "ES-963" in desc
    assert "E-1776, E-1801" in desc
    assert "E-1999" not in desc          # another session's touches
    assert "\n" not in desc              # validate_description rejects newlines
    assert len(desc) <= 1024


def test_description_is_still_a_sentence_when_nothing_was_touched():
    assert "had not touched any tasks" in session_cmd._taskless_resume_description(963)


def test_projectless_session_errors_and_names_the_uuid(monkeypatch):
    monkeypatch.setattr(
        session_cmd, "_resume_target", lambda ref: _taskless(project_path="")
    )
    with pytest.raises(click.ClickException) as exc:
        session_cmd._resolve_resume("ES-963")
    msg = str(exc.value)
    assert "no registered project" in msg
    assert "claude --resume uuid-963" in msg


def test_worktree_creation_failure_errors_and_names_the_uuid(
    monkeypatch, tmp_path
):
    from endless import task_cmd
    _seed_project(path=str(tmp_path / "proj"))
    monkeypatch.setattr(
        session_cmd, "_resume_target",
        lambda ref: _taskless(project_path=str(tmp_path / "proj")),
    )

    def boom(**kw):
        raise click.ClickException("branch task/1970-x carries unlanded work")

    monkeypatch.setattr(task_cmd, "create_claimed_task_for_session", boom)
    with pytest.raises(click.ClickException) as exc:
        session_cmd._resolve_resume("ES-963")
    msg = str(exc.value)
    assert "carries unlanded work" in msg
    assert "claude --resume uuid-963" in msg


def test_no_uuid_still_refuses_before_any_task_is_minted(monkeypatch, stub_create):
    """The 'background agent that never started?' parenthetical belongs to the
    no-UUID branch, and only to it — a dispatch row with no transcript has
    nothing to resume, so nothing should be created for it."""
    monkeypatch.setattr(
        session_cmd, "_resume_target", lambda ref: _taskless(session_id="")
    )
    with pytest.raises(click.ClickException, match="no Claude UUID"):
        session_cmd._resolve_resume("ES-963")
    assert stub_create == {}


def test_task_bearing_session_creates_nothing(monkeypatch, stub_create, tmp_path):
    wt = tmp_path / "e-10"
    wt.mkdir()
    monkeypatch.setattr(
        session_cmd, "_resume_target",
        lambda ref: _taskless(task_id=10, worktree_path=str(wt)),
    )
    decision: dict = {}
    _, got_wt, label, _ = session_cmd._resolve_resume("ES-963", decision_out=decision)
    assert got_wt == str(wt)
    assert label == "E-10"
    assert decision["created_task"] is False
    assert stub_create == {}

"""Tests for `endless task next revise` (E-1436).

revise_next_list shells out to the endless-event Go binary via emit_event;
these tests stub emit_event so they exercise the Python wiring (file read,
JSON parse, collision/warning/summary rendering) without the binary or a
real session. The transaction, caps, and audit row are covered by Go tests
in internal/events.
"""

import json
from datetime import datetime, timedelta, timezone

import pytest
from click.testing import CliRunner

from endless import task_cmd
from endless.cli import main


class _EmitSpy:
    """Stand-in for emit_event: records the call and returns a canned dict."""

    def __init__(self):
        self.received = None
        self.result = {}

    def __call__(self, **kwargs):
        self.received = kwargs
        return self.result


@pytest.fixture
def fake_emit(monkeypatch):
    spy = _EmitSpy()
    # revise_next_list does `from endless.event_bridge import emit_event` at
    # call time, so patching the module attribute is picked up.
    monkeypatch.setattr("endless.event_bridge.emit_event", spy)
    return spy


def _write(tmp_path, data):
    f = tmp_path / "list.json"
    f.write_text(json.dumps(data))
    return str(f)


def test_revise_first_revision_summary(fake_emit, seeded_project_at_cwd, tmp_path):
    payload = {"lanes": [
        {"id": "a", "priority": 1, "rationale": "r",
         "items": [{"task_id": "E-1", "reason": "x"}, {"task_id": "E-2", "reason": "y"}]},
    ]}
    fake_emit.result = {
        "prior_revision": None,
        "state": {"project": "test", "last_revised": "2026-05-26T04:00:00",
                  "revised_by_session_id": 7, "lanes": payload["lanes"]},
    }
    res = CliRunner().invoke(main, ["task", "next", "revise", "--file", _write(tmp_path, payload)])
    assert res.exit_code == 0, res.output
    assert "first revision" in res.output
    assert "Revised: 1 lane(s), 2 item(s)." in res.output
    # The parsed payload + envelope fields reach emit_event unchanged.
    assert fake_emit.received["kind"] == "project_next.revised"
    assert fake_emit.received["entity_type"] == "project_next"
    assert fake_emit.received["payload"] == payload


def test_revise_prints_collision_notice(fake_emit, seeded_project_at_cwd, tmp_path):
    fake_emit.result = {
        "prior_revision": {"revised_at": "2020-01-01T00:00:00", "session_id": 42},
        "state": {"lanes": []},
    }
    res = CliRunner().invoke(main, ["task", "next", "revise", "--file", _write(tmp_path, {"lanes": []})])
    assert res.exit_code == 0, res.output
    assert "last revised" in res.output
    assert "by session 42" in res.output


def test_revise_json_output(fake_emit, seeded_project_at_cwd, tmp_path):
    state = {"project": "test", "last_revised": "2026-05-26T04:00:00",
             "revised_by_session_id": 7, "lanes": []}
    fake_emit.result = {"prior_revision": None, "state": state}
    res = CliRunner().invoke(
        main, ["task", "next", "revise", "--file", _write(tmp_path, {"lanes": []}), "--json"])
    assert res.exit_code == 0, res.output
    assert '"revised_by_session_id": 7' in res.output
    assert '"project": "test"' in res.output


def test_revise_soft_cap_warning(fake_emit, seeded_project_at_cwd, tmp_path):
    fake_emit.result = {
        "prior_revision": None,
        "warning": "11 items exceeds the soft cap of 10; consider trimming the list",
        "state": {"lanes": []},
    }
    res = CliRunner().invoke(main, ["task", "next", "revise", "--file", _write(tmp_path, {"lanes": []})])
    assert res.exit_code == 0, res.output
    assert "11 items exceeds the soft cap" in res.output


def test_revise_file_not_found(fake_emit, seeded_project_at_cwd):
    res = CliRunner().invoke(main, ["task", "next", "revise", "--file", "/no/such/file.json"])
    assert res.exit_code != 0
    assert "File not found" in res.output


def test_revise_malformed_json(fake_emit, seeded_project_at_cwd, tmp_path):
    f = tmp_path / "bad.json"
    f.write_text("{not json")
    res = CliRunner().invoke(main, ["task", "next", "revise", "--file", str(f)])
    assert res.exit_code != 0
    assert "Invalid JSON" in res.output


def test_bare_next_still_runs_heuristic(monkeypatch, seeded_project_at_cwd):
    # The `next` group must keep invoking the heuristic list when no subcommand
    # is given (invoke_without_command).
    called = {}

    def _stub(**kwargs):
        called["yes"] = True

    monkeypatch.setattr(task_cmd, "next_tasks", _stub)
    res = CliRunner().invoke(main, ["task", "next"])
    assert res.exit_code == 0, res.output
    assert called.get("yes")


def test_format_relative_buckets():
    now = datetime.now(timezone.utc)
    fmt = "%Y-%m-%dT%H:%M:%S"
    assert task_cmd._format_relative((now - timedelta(seconds=5)).strftime(fmt)).endswith("s ago")
    assert task_cmd._format_relative((now - timedelta(minutes=5)).strftime(fmt)).endswith("m ago")
    assert task_cmd._format_relative((now - timedelta(hours=5)).strftime(fmt)).endswith("h ago")
    assert task_cmd._format_relative((now - timedelta(days=5)).strftime(fmt)).endswith("d ago")
    assert task_cmd._format_relative(None) == "an unknown time ago"

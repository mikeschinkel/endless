"""Tests for the session_id attribution gate in event_bridge.emit_event (E-1401).

The gate refuses to emit when actor_kind in {"cli", "hook"} and session_id
cannot be resolved (neither passed explicitly nor produced by the
_current_endless_session_id resolver). Silent attribution loss is a
FIVE-ALARM bug per Mike's directive — these tests pin the refusal in
place.

Every test here opts out of conftest's stub_current_session_id fixture
(the one that makes the resolver return 1 for every other test) because
these tests need to control the resolver themselves.
"""

import json
from pathlib import Path

import click
import pytest

from endless import event_bridge

# Apply to every test in this module: we own the resolver mock.
pytestmark = pytest.mark.no_session_stub


def _seed_node_id(isolated_env):
    """Write a node_id into the test config so _get_or_create_node_id is happy."""
    config_path = isolated_env["config_dir"] / "config.json"
    data = json.loads(config_path.read_text())
    data["node_id"] = "test"
    config_path.write_text(json.dumps(data) + "\n")


@pytest.fixture
def captured_emit(isolated_env, monkeypatch):
    """Stub subprocess.run (the `endless-go event` Go subprocess) and capture
    the --actor-kind / --session-id arguments emit_event was about to pass.

    Returns a dict that callers can read after invocation:
      - calls: list of arg lists, one per subprocess.run invocation
    """
    _seed_node_id(isolated_env)

    # event_bridge resolves project_root by querying the projects table; seed one.
    from endless import db
    db.execute(
        "INSERT INTO projects (name, path, status, created_at, updated_at) "
        "VALUES ('sample', '/tmp/sample', 'active', datetime('now'), datetime('now'))"
    )

    # shutil.which("endless-go") must return SOMETHING truthy so we proceed
    # to the subprocess call (which we then intercept).
    monkeypatch.setattr(event_bridge.shutil, "which", lambda _name: "/fake/endless-go")

    captured: dict = {"calls": []}

    class _FakeResult:
        returncode = 0
        stdout = '{"id": "E-1"}'
        stderr = ""

    def _fake_run(cmd, capture_output=False, text=False):
        captured["calls"].append(list(cmd))
        return _FakeResult()

    monkeypatch.setattr(event_bridge.subprocess, "run", _fake_run)
    return captured


def _force_resolver(monkeypatch, return_value):
    """Force _current_endless_session_id to return the given value (or raise)."""
    import endless.task_cmd as task_cmd

    if isinstance(return_value, Exception):
        def _raise():
            raise return_value
        monkeypatch.setattr(task_cmd, "_current_endless_session_id", _raise)
    else:
        monkeypatch.setattr(
            task_cmd, "_current_endless_session_id", lambda: return_value
        )


def _emit(**overrides):
    """Invoke emit_event with a baseline of valid args, overriding any."""
    args = dict(
        kind="task.added",
        project="sample",
        entity_type="task",
        entity_id=1,
        payload={"title": "x"},
    )
    args.update(overrides)
    return event_bridge.emit_event(**args)


def _session_id_from_cmd(cmd: list[str]) -> str | None:
    """Extract the --session-id value from a captured subprocess argv, or None."""
    if "--session-id" not in cmd:
        return None
    idx = cmd.index("--session-id")
    return cmd[idx + 1]


# --- The six cases from the E-1401 prompt ---


def test_cli_actor_unresolvable_session_raises(captured_emit, monkeypatch):
    """actor_kind='cli' + resolver returns None  →  ClickException, no event."""
    _force_resolver(monkeypatch, None)
    with pytest.raises(click.ClickException) as exc:
        _emit(actor_kind="cli")
    msg = exc.value.message
    # Headline names the actual problem.
    assert "Cannot determine the Endless session" in msg
    # Both actionable fixes are present.
    assert "Claude session pane" in msg
    assert "ENDLESS_SESSION_ID" in msg
    # And the subprocess was never spawned.
    assert captured_emit["calls"] == []


def test_hook_actor_unresolvable_session_raises(captured_emit, monkeypatch):
    """actor_kind='hook' + resolver returns None  →  ClickException, no event."""
    _force_resolver(monkeypatch, None)
    with pytest.raises(click.ClickException) as exc:
        _emit(actor_kind="hook")
    assert "Cannot determine the Endless session" in exc.value.message
    assert captured_emit["calls"] == []


def test_system_actor_unresolvable_session_emits(captured_emit, monkeypatch):
    """actor_kind='system' + resolver returns None  →  emits, no session_id."""
    _force_resolver(monkeypatch, None)
    _emit(actor_kind="system")
    assert len(captured_emit["calls"]) == 1
    assert _session_id_from_cmd(captured_emit["calls"][0]) is None


def test_web_actor_unresolvable_session_emits(captured_emit, monkeypatch):
    """actor_kind='web' + resolver returns None  →  emits, no session_id."""
    _force_resolver(monkeypatch, None)
    _emit(actor_kind="web")
    assert len(captured_emit["calls"]) == 1
    assert _session_id_from_cmd(captured_emit["calls"][0]) is None


def test_cli_actor_resolver_returns_id_emits_with_session(captured_emit, monkeypatch):
    """actor_kind='cli' + resolver returns id  →  emits with that session_id."""
    _force_resolver(monkeypatch, 42)
    _emit(actor_kind="cli")
    assert len(captured_emit["calls"]) == 1
    assert _session_id_from_cmd(captured_emit["calls"][0]) == "42"


def test_explicit_session_id_bypasses_resolver(captured_emit, monkeypatch):
    """Explicit session_id arg wins even when actor_kind='cli' and resolver None.

    The gate fires only when both the caller AND the resolver are silent.
    A caller that knows the session_id (e.g. testing harness, internal
    flow that already resolved it) must be able to emit.
    """
    _force_resolver(monkeypatch, None)
    _emit(actor_kind="cli", session_id="99")
    assert len(captured_emit["calls"]) == 1
    assert _session_id_from_cmd(captured_emit["calls"][0]) == "99"


# --- Extra: resolver raising must not bypass the gate ---


def test_resolver_exception_still_gates_cli(captured_emit, monkeypatch):
    """If the resolver raises, cli/hook still refuse to emit unattributed.

    Prior behavior swallowed resolver exceptions and emitted without
    session_id — exactly the silent-degradation pattern E-1401 forbids.
    """
    _force_resolver(monkeypatch, RuntimeError("resolver broken"))
    with pytest.raises(click.ClickException):
        _emit(actor_kind="cli")
    assert captured_emit["calls"] == []

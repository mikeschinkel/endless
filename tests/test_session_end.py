"""E-2394: `endless session end` discards a ghost session.

Unit-level: the Go resolver, the liveness probe and the end-session write are
stubbed, so these pin the guards, the prompt and what is printed. The write
itself is covered by internal/sessionquerycmd/end_session_test.go, and the
real end-to-end path by .endless/tasks/e-2394/verify.sh.
"""

import subprocess
from datetime import datetime, timedelta, timezone

import click
import pytest
from click.testing import CliRunner

from endless import session_cmd, task_cmd
from endless.cli import main


def _stamp(minutes_ago: float) -> str:
    then = datetime.now(timezone.utc) - timedelta(minutes=minutes_ago)
    return then.strftime("%Y-%m-%dT%H:%M:%S")


def _target(**over) -> dict:
    base = {
        "endless_id": 501,
        "session_id": "uuid-501",
        "task_id": 77,
        "task_title": "A ghost's task",
        "state": "working",
        "last_activity": _stamp(60),
    }
    base.update(over)
    return base


@pytest.fixture
def stubbed(monkeypatch):
    """Stub resolution, liveness and the Go write; record what was written."""
    state = {"target": _target(), "liveness": "unknown", "pane": None,
             "ended": [], "tty": True, "confirm": None}

    monkeypatch.setattr(session_cmd, "_resume_target", lambda ref: state["target"])

    def fake_liveness(touches):
        for t in touches:
            t["liveness"] = state["liveness"]
            t["pane"] = state["pane"]
        return touches
    monkeypatch.setattr(task_cmd, "_attach_liveness", fake_liveness)

    real_run = subprocess.run

    def fake_run(argv, *a, **kw):
        if "end-session" in argv:
            state["ended"].append(argv[argv.index("--id") + 1])
            return subprocess.CompletedProcess(argv, 0, "", "")
        return real_run(argv, *a, **kw)
    monkeypatch.setattr(subprocess, "run", fake_run)
    monkeypatch.setattr(session_cmd.sys.stdin, "isatty", lambda: state["tty"],
                        raising=False)

    def fake_confirm(text, default=False):
        state["prompted"] = text
        return state["confirm"]
    monkeypatch.setattr(click, "confirm", fake_confirm)
    return state


def _refusal(**kw) -> str:
    with pytest.raises(click.ClickException) as excinfo:
        session_cmd.end_session("ES-501", **kw)
    return excinfo.value.format_message()


def test_refuses_a_live_session(stubbed):
    stubbed["liveness"], stubbed["pane"] = "live", "%414"
    msg = _refusal(yes=True, force=True)
    assert "live in tmux pane %414" in msg
    assert stubbed["ended"] == []


def test_refuses_recent_activity_without_force(stubbed):
    stubbed["target"] = _target(last_activity=_stamp(3))
    msg = _refusal(yes=True)
    assert "had activity 3 min ago" in msg
    assert "--force" in msg
    assert stubbed["ended"] == []


def test_force_overrides_recent_activity(stubbed):
    stubbed["target"] = _target(last_activity=_stamp(3))
    session_cmd.end_session("ES-501", force=True, yes=True)
    assert stubbed["ended"] == ["501"]


def test_prompt_yes_ends(stubbed, capsys):
    stubbed["confirm"] = True
    session_cmd.end_session("ES-501")
    out = capsys.readouterr().out
    assert "task:          E-77 — A ghost's task" in out
    assert "tmux:          unknown" in out
    assert stubbed["prompted"] == "End ES-501?"
    assert stubbed["ended"] == ["501"]


def test_prompt_no_leaves_it(stubbed, capsys):
    stubbed["confirm"] = False
    session_cmd.end_session("ES-501")
    assert "Not ended" in capsys.readouterr().out
    assert stubbed["ended"] == []


def test_yes_skips_the_prompt(stubbed):
    session_cmd.end_session("ES-501", yes=True)
    assert "prompted" not in stubbed
    assert stubbed["ended"] == ["501"]


def test_no_terminal_without_yes_refuses(stubbed):
    stubbed["tty"] = False
    msg = _refusal()
    assert "No terminal to confirm on" in msg
    assert "prompted" not in stubbed
    assert stubbed["ended"] == []


def test_already_ended_is_reported_and_exits_zero(stubbed, capsys):
    stubbed["target"] = _target(state="ended")
    session_cmd.end_session("ES-501", yes=True)
    assert "ES-501 (E-77) is already ended" in capsys.readouterr().out
    assert stubbed["ended"] == []


def test_prints_how_to_get_it_back(stubbed, capsys):
    session_cmd.end_session("ES-501", yes=True)
    out = capsys.readouterr().out
    assert "Ended ES-501 (E-77). Its transcript is kept." in out
    assert "endless session resume ES-501" in out
    assert "E-77 is continued only from it" in out
    assert "endless session goto E-77 --resume" in out


def test_taskless_session_omits_the_task_line(stubbed, capsys):
    stubbed["target"] = _target(task_id=None, task_title="")
    session_cmd.end_session("ES-501", yes=True)
    out = capsys.readouterr().out
    assert "Ended ES-501 (no task)." in out
    assert "continued only from it" not in out


def test_cli_wires_flags(stubbed):
    result = CliRunner().invoke(main, ["session", "end", "ES-501", "--yes"])
    assert result.exit_code == 0, result.output
    assert stubbed["ended"] == ["501"]

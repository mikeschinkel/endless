"""`session resume --tmux-session NAME | --all-tmux-sessions` (E-2196).

After a tmux crash, one command resumes every restored task window. The sweep
itself is Go (`endless-go resume-windows`, tested there); the Python surface is
the Click options and a passthrough. These pin the argument rules — the scope
flags replace REF, never combine with it or with each other, and carry none of
the single-REF flags — and exactly what is handed to Go.
"""

import pytest
from click.testing import CliRunner

from endless import config, session_cmd
from endless.cli import main


class _Res:
    returncode = 0


@pytest.fixture
def go(monkeypatch):
    """Record the argv handed to endless-go instead of running it."""
    calls = []

    def fake_run(argv, *a, **kw):
        calls.append(argv)
        return _Res()

    monkeypatch.setattr("shutil.which", lambda name: f"/bin/{name}")
    monkeypatch.setattr("subprocess.run", fake_run)
    monkeypatch.setattr(config, "require_db_context", lambda: None)
    monkeypatch.setattr(config, "db_context_is_sandbox", lambda: False)
    monkeypatch.setattr(config, "go_db_context_args", lambda: ["--db", "main"])
    return calls


def _resume(*args):
    return CliRunner().invoke(main, ["session", "resume", *args])


def test_tmux_session_passes_through(go):
    res = _resume("--tmux-session", "active")
    assert res.exit_code == 0, res.output
    assert go == [["/bin/endless-go", "--db", "main", "resume-windows",
                   "--tmux-session", "active"]]


def test_tmux_session_equals_form_and_dry_run(go):
    res = _resume("--tmux-session=active", "--dry-run")
    assert res.exit_code == 0, res.output
    assert go[0][-3:] == ["--tmux-session", "active", "--dry-run"]


def test_all_tmux_sessions_passes_through(go):
    res = _resume("--all-tmux-sessions")
    assert res.exit_code == 0, res.output
    assert go[0][-1] == "--all-tmux-sessions"


@pytest.mark.parametrize("args", [
    ["E-7", "--tmux-session", "active"],
    ["E-7", "--all-tmux-sessions"],
    ["--tmux-session", "active", "--all-tmux-sessions"],
    ["--tmux-session", "active", "--rebind"],
    ["--tmux-session", "active", "--force"],
    ["--all-tmux-sessions", "--review"],
    ["--all-tmux-sessions", "--no-sibling-panes"],
])
def test_usage_errors(go, args):
    res = _resume(*args)
    assert res.exit_code == 2, res.output
    assert go == []


def test_tmux_session_requires_a_name(go):
    # A required value is what keeps --tmux-session an ordinary option: it
    # cannot swallow REF, and a bare flag is a usage error.
    res = _resume("--tmux-session")
    assert res.exit_code == 2
    assert go == []


def test_no_ref_and_no_scope_is_a_usage_error(go):
    res = _resume()
    assert res.exit_code == 2
    assert "--tmux-session" in res.output


def test_refused_under_sandbox(go, monkeypatch):
    monkeypatch.setattr(config, "db_context_is_sandbox", lambda: True)
    res = _resume("--tmux-session", "active")
    assert res.exit_code != 0
    assert "--db main" in res.output
    assert go == []


def test_go_failure_exit_code_propagates(go, monkeypatch):
    class _Bad:
        returncode = 1
    monkeypatch.setattr("subprocess.run", lambda *a, **kw: _Bad())
    with pytest.raises(SystemExit) as e:
        session_cmd.resume_tmux_windows(None, "active", False)
    assert e.value.code == 1

"""Tests for `endless session trail` rendering (E-1682).

The DB read happens Go-side (`endless-go session-query trail`); these tests mock
that subprocess and assert the Python viewer's rendering: newest-first order,
the via tag, task labels, summary, and the empty state.
"""

import json
import subprocess

import pytest

from endless import session_cmd


class _Completed:
    def __init__(self, returncode=0, stdout="", stderr=""):
        self.returncode = returncode
        self.stdout = stdout
        self.stderr = stderr


@pytest.fixture
def fake_trail(monkeypatch):
    """Install a fake `endless-go session-query trail` returning `edges`, and a
    fake tmux client resolver. Returns a setter for the edge list and captures
    the argv the viewer invoked.
    """
    state = {"edges": [], "argv": None}

    def fake_run(cmd, capture_output=True, text=True, timeout=5):
        state["argv"] = cmd
        return _Completed(stdout=json.dumps(state["edges"]))

    monkeypatch.setattr(subprocess, "run", fake_run)
    monkeypatch.setattr(session_cmd, "_resolve_client_name", lambda: "/dev/ttys001")
    # task_id_display is exercised for real; it just formats E-NNNN.

    def _set(edges):
        state["edges"] = edges

    return _set, state


def _edge(**kw):
    base = {
        "id": 1, "client": "/dev/ttys001", "via": "manual",
        "created_at": "2026-06-29T00:00:00",
        "from_session_id": None, "from_pane": None, "from_task_id": None,
        "from_summary": "",
        "to_session_id": None, "to_pane": None, "to_task_id": None,
        "to_summary": "",
    }
    base.update(kw)
    return base


def test_trail_renders_newest_first_with_via_and_task_label(fake_trail, capsys):
    set_edges, _ = fake_trail
    # The Go reader returns newest-first; the viewer prints in that order.
    set_edges([
        _edge(id=2, via="goto", to_session_id=10, to_task_id=1465,
              to_summary="latest work",
              from_session_id=5, from_task_id=1400),
        _edge(id=1, via="manual", to_session_id=5, to_task_id=1400),
    ])

    session_cmd.session_trail()

    out = capsys.readouterr().out
    lines = [ln for ln in out.splitlines() if ln.startswith("•")]
    assert len(lines) == 2
    # Newest (goto, E-1465) first.
    assert "goto" in lines[0]
    assert "E-1465" in lines[0]
    assert "session 10" in lines[0]
    assert "session 5" in lines[0]  # the `from` endpoint
    # Oldest (manual, E-1400) second.
    assert "manual" in lines[1]
    assert "E-1400" in lines[1]
    # Summary is rendered under the newest edge.
    assert "latest work" in out


def test_trail_untracked_pane_endpoint(fake_trail, capsys):
    set_edges, _ = fake_trail
    set_edges([_edge(to_session_id=None, to_pane="%42", via="manual")])

    session_cmd.session_trail()

    out = capsys.readouterr().out
    assert "pane %42" in out


def test_trail_empty_state_suggests_all(fake_trail, capsys):
    set_edges, _ = fake_trail
    set_edges([])

    session_cmd.session_trail()

    err = capsys.readouterr().err
    assert "No navigation recorded" in err
    assert "--all" in err


def test_trail_default_scopes_to_current_client(fake_trail):
    set_edges, state = fake_trail
    set_edges([])

    session_cmd.session_trail()

    argv = state["argv"]
    assert "trail" in argv
    assert "--client" in argv
    assert argv[argv.index("--client") + 1] == "/dev/ttys001"


def test_trail_all_omits_client_filter(fake_trail):
    set_edges, state = fake_trail
    set_edges([])

    session_cmd.session_trail(show_all=True)

    argv = state["argv"]
    assert "--client" not in argv

"""Tests for E-2294: `endless task sessions <id>`, and the liveness `task show`'s
Touched-by block gained alongside it.

The liveness cases run against a REAL tmux server, private to the test: the
suite's conftest points TMUX_TMPDIR at an empty per-test directory, so the
server started here is the only one `endless-go` can reach. That is what lets
these tests prove the end-to-end claim — a session whose row reads `working`
but whose pane is gone reports `dead` — rather than a mocked one. The four-state
truth table itself is pinned in internal/monitor/liveness_test.go.
"""

import json
import shutil
import subprocess
import tempfile

import pytest
from click.testing import CliRunner

from endless import db
from endless.cli import main

pytestmark = pytest.mark.skipif(
    shutil.which("tmux") is None, reason="needs tmux for the liveness probe")

_SERVER = "e2294-test-server"
# Relation ids mirror session_task_relations' seeded rows (ED-1497).
_SURFACED, _REVISITED = 2, 3


@pytest.fixture
def tmux_pane(seeded_project_at_cwd, monkeypatch):
    """Start a private tmux server identified as _SERVER; yield its one pane.

    The socket directory is re-pointed at a SHORT private temp dir: conftest's
    per-test TMUX_TMPDIR sits under pytest's tmp_path, deep enough that the
    socket path overruns the 104-byte AF_UNIX limit on macOS and the server
    cannot start there. Still private to this test, so no real tmux is reachable.
    """
    sock_dir = tempfile.mkdtemp(prefix="e2294-")
    monkeypatch.setenv("TMUX_TMPDIR", sock_dir)

    def tmux(*args: str) -> str:
        return subprocess.run(
            ["tmux", *args], check=True, capture_output=True, text=True,
        ).stdout.strip()

    tmux("new-session", "-d", "-s", "e2294")
    try:
        tmux("set-option", "-g", "@server_uuid", _SERVER)
        yield tmux("display-message", "-p", "-t", "e2294", "#{pane_id}")
    finally:
        subprocess.run(["tmux", "kill-server"], capture_output=True)
        shutil.rmtree(sock_dir, ignore_errors=True)


def _add_task(title: str) -> int:
    return db.execute(
        "INSERT INTO tasks (project_id, title, status, type_id, phase, created_at) "
        "VALUES (1, ?, 'underway', 1, 'now', datetime('now'))",
        (title,),
    ).lastrowid


def _add_session(
    sid: int, *, state: str = "working", task_id: int | None = None,
    focus: int | None = None, server: str | None = None, pane: str | None = None,
    when: str = "2026-10-01T00:00:00",
) -> int:
    process_id = None
    if pane is not None:
        process_id = db.execute(
            "INSERT INTO processes (kind_id, server_uuid, address) VALUES (1, ?, ?)",
            (server, pane),
        ).lastrowid
    db.execute(
        "INSERT INTO sessions (id, session_id, project_id, state, task_id, "
        "focus_task_id, process_id, started_at, last_activity) "
        "VALUES (?, ?, 1, ?, ?, ?, ?, ?, ?)",
        (sid, f"uuid-{sid}", state, task_id, focus, process_id, when, when),
    )
    return sid


def _touch(sid: int, task_id: int, relation: int, when: str) -> None:
    db.execute(
        "INSERT INTO session_tasks (session_id, task_id, relation_id, created_at, "
        "updated_at) VALUES (?, ?, ?, ?, ?)",
        (sid, task_id, relation, when, when),
    )


def _run(*args: str) -> str:
    result = CliRunner().invoke(main, list(args))
    assert result.exit_code == 0, result.output
    return result.output


def _sessions_json(tid: int, *extra: str) -> dict:
    payload = json.loads(_run("task", "sessions", f"E-{tid}", "--json", *extra))
    return {row["session"]: row for row in payload["sessions"]}


@pytest.fixture
def four_states(tmux_pane):
    """A task held by one session in each liveness state, plus one ended."""
    tid = _add_task("Held four ways")
    other = _add_task("Somewhere else")
    _add_session(101, task_id=tid, server=_SERVER, pane=tmux_pane,
                 when="2026-10-01T00:00:05")
    # Row still says working; its pane is absent from a server we reached.
    _add_session(102, task_id=None, focus=tid, server=_SERVER, pane="%99999",
                 when="2026-10-01T00:00:04")
    # Bound to a server that no longer exists: no opinion, forever.
    _add_session(103, state="idle", task_id=other, server="long-gone-server",
                 pane=tmux_pane, when="2026-10-01T00:00:03")
    _add_session(104, state="idle", when="2026-10-01T00:00:02")
    _add_session(105, state="ended", task_id=other, when="2026-10-01T00:00:01")
    _touch(102, tid, _REVISITED, "2026-10-01T00:00:04")
    _touch(103, tid, _SURFACED, "2026-10-01T00:00:03")
    _touch(104, tid, _REVISITED, "2026-10-01T00:00:02")
    _touch(105, tid, _REVISITED, "2026-10-01T00:00:01")
    return tid, tmux_pane


def test_reports_raw_liveness_relation_and_holds(four_states):
    tid, pane = four_states
    rows = _sessions_json(tid)
    assert list(rows) == ["ES-101", "ES-102", "ES-103", "ES-104"]

    assert rows["ES-101"]["relation"] == "claimed"
    assert rows["ES-101"]["liveness"] == "live"
    assert rows["ES-101"]["pane"] == pane
    assert rows["ES-101"]["holds"] == "own"

    # The acceptance case: `working` on the row, dead in tmux.
    assert rows["ES-102"]["state"] == "working"
    assert rows["ES-102"]["liveness"] == "dead"
    assert rows["ES-102"]["pane"] is None
    assert rows["ES-102"]["holds"] == "focus"

    # A pane id on a server we never reached is no evidence either way, and is
    # not printed: the same id names a live pane on the server we did reach.
    assert rows["ES-103"]["liveness"] == "unknown"
    assert rows["ES-103"]["pane"] is None
    assert rows["ES-103"]["holds"] is None

    assert rows["ES-104"]["liveness"] == "unbound"


def test_ended_sessions_only_with_all(four_states):
    tid, _ = four_states
    assert "ES-105" not in _sessions_json(tid)
    assert _sessions_json(tid, "--all")["ES-105"]["state"] == "ended"


def test_human_and_agent_renderings(four_states):
    tid, pane = four_states
    out = _run("task", "sessions", f"E-{tid}")
    assert f"Sessions holding E-{tid}" in out
    assert pane in out
    assert "dead" in out and "unknown" in out and "unbound" in out
    assert "1 ended session not shown; --all lists them." in out

    agent = _run("task", "sessions", f"E-{tid}", "--agent")
    assert f"claimed ES-101 (E-{tid}) [working] tmux:live {pane} holds=own" in agent
    assert "revisited ES-102 [working] tmux:dead holds=focus" in agent


def test_task_show_agrees_with_task_sessions(four_states):
    tid, pane = four_states
    sessions = _sessions_json(tid, "--all")
    shown = json.loads(_run("task", "show", f"E-{tid}", "--json"))["touched_by"]
    assert {t["session"]: (t["liveness"], t["pane"]) for t in shown} == {
        sid: (row["liveness"], row["pane"]) for sid, row in sessions.items()
    }

    human = _run("task", "show", f"E-{tid}")
    assert f"tmux:live {pane}" in human
    assert "tmux:dead" in human
    assert "tmux:unknown" in human


def test_a_task_no_session_holds_says_so(seeded_project_at_cwd):
    tid = _add_task("Nobody's")
    out = _run("task", "sessions", f"E-{tid}")
    assert f"No session holds E-{tid} now." in out
    assert "Session" not in out.replace("No session", "")

    _add_session(201, state="ended")
    _touch(201, tid, _REVISITED, "2026-10-01T00:00:00")
    out = _run("task", "sessions", f"E-{tid}")
    assert "1 ended session touched it; --all lists them." in out
    assert json.loads(_run("task", "sessions", f"E-{tid}", "--json"))["sessions"] == []


def test_unknown_task_is_refused(seeded_project_at_cwd):
    result = CliRunner().invoke(main, ["task", "sessions", "E-987654"])
    assert result.exit_code != 0
    assert "E-987654 not found" in result.output

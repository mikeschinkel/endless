"""E-2125: the suite must not be able to reach the developer's tmux server.

Deleting $TMUX and $TMUX_PANE (conftest.isolated_env) only stops code that
BRANCHES on those vars. The tmux CLI finds the running server through its
default socket regardless, so anything that shells out unconditionally still
talked to whatever tmux the developer had open — and one `new-window` there
opens a real window, in a real session, in front of a real person.

conftest points TMUX_TMPDIR at an empty per-test directory instead, which is
where tmux derives its socket path from once $TMUX is gone. These tests pin
that, because it is invisible: nothing else in the suite fails if it regresses,
it just quietly starts touching a live server again.
"""

import os
import shutil
import subprocess
from pathlib import Path

import pytest


def _tmux(*args):
    return subprocess.run(
        ["tmux", *args], capture_output=True, text=True, timeout=10,
    )


def test_tmux_env_is_stripped():
    assert "TMUX" not in os.environ
    assert "TMUX_PANE" not in os.environ


def test_tmux_tmpdir_is_per_test(tmp_path):
    socket_dir = os.environ.get("TMUX_TMPDIR")
    assert socket_dir, "TMUX_TMPDIR must be set for every test"
    assert Path(socket_dir).is_dir()
    assert Path(socket_dir) == tmp_path / "tmux", (
        "the socket dir must live under THIS test's tmp_path, so two tests "
        "cannot share a server either"
    )


@pytest.mark.skipif(shutil.which("tmux") is None, reason="tmux not installed")
def test_a_shelled_out_tmux_reaches_no_server():
    """The property that matters, stated end to end: a plain `tmux` subprocess
    — the shape every call site in the product takes — finds nothing to talk
    to. If a developer has a live tmux running while this passes, the
    isolation is real."""
    res = _tmux("list-sessions")
    assert res.returncode != 0, (
        f"tmux found a server from inside the suite: {res.stdout!r}"
    )
    assert "no server running" in (res.stderr + res.stdout).lower() or \
        "error connecting" in (res.stderr + res.stdout).lower(), res.stderr


def test_the_socket_a_stray_window_would_use_is_disposable(tmp_path):
    """The specific accident this exists to prevent, stated without causing it.

    A probe that actually ran `tmux new-window` would open a real window in a
    real session on the very regression it was written to catch — the check and
    the damage would be the same event. So this pins the structural fact
    instead: the socket path tmux derives with $TMUX gone is under this test's
    tmp_path, which no tmux server has ever listened on. `new-window` does not
    start a server, so there is nowhere for a stray window to appear.
    """
    socket = Path(os.environ["TMUX_TMPDIR"]) / f"tmux-{os.getuid()}" / "default"
    assert tmp_path in socket.parents
    assert not socket.exists(), "something started a server on the test socket"

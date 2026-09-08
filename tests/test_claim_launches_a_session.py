"""`task claim` from a shell starts the session it just claimed for (E-2106).

Claim used to end by printing "To work on this task, choose one:" with two
options, and on the case that reaches it most often — a re-claim onto a task
whose worktree already exists — both were wrong. Option 1 was `task spawn`,
which `_check_prior_claim` refuses for any task that has ever been claimed,
which a re-claim always has. Option 2 spelled out `/cd`, `shell-init` and
`eswt`, the last of which `endless shell-init` has never defined (the guide
marks it "planned, not yet shipped"), so its bootstrap line taught a command
that does not exist.

What replaced it is one outcome per caller, decided rather than offered. These
pin the shell caller's: a shell cannot BECOME the session, so one is launched in
this pane — the same thing `task spawn` does, minus the handoff, since
delivering a handoff is what re-reads a task as if it were new. A window that
cannot be launched into safely is refused before the claim, never quietly
redirected somewhere the caller did not ask for.
"""

import pytest

from endless import task_cmd


class _Exec(Exception):
    """Raised by the stub execvp so a test can stop where the real one would."""


@pytest.fixture
def shell_in_tmux(monkeypatch, tmp_path):
    """A shell pane in tmux, with claude, tmux and the layout builder stubbed.

    Yields the ordered trace: `("options", pane)`, `("layout", pane)`,
    `("tmux", args)`, `("exec", argv)`, `("spawn-window", argv)`.
    """
    from endless import session_cmd

    trace: list[tuple] = []
    monkeypatch.setenv("TMUX", "/tmp/tmux-501/default,1,0")
    monkeypatch.setenv("TMUX_PANE", "%3")
    monkeypatch.setattr(task_cmd.shutil, "which", lambda name: "/bin/claude")
    monkeypatch.setattr(task_cmd, "_claude_binary", lambda: "/bin/claude")
    monkeypatch.setattr(
        session_cmd, "_bind_pane_window_options",
        lambda pane, task, project, uuid: trace.append(("options", pane)),
    )
    monkeypatch.setattr(
        session_cmd, "build_pane_layout",
        lambda pane, cwd: trace.append(("layout", pane)),
    )
    monkeypatch.setattr(
        task_cmd, "_tmux_run_quiet", lambda args: trace.append(("tmux", args)),
    )
    monkeypatch.setattr(task_cmd.os, "chdir", lambda p: None)

    def fake_exec(file, argv):
        trace.append(("exec", [file, *argv]))
        raise _Exec()

    monkeypatch.setattr(task_cmd.os, "execvp", fake_exec)

    return trace


def _panes(monkeypatch, ids):
    from endless import session_cmd
    monkeypatch.setattr(session_cmd, "_tmux_window_pane_ids", lambda: ids)


def _kind(trace, kind):
    return [payload for k, payload in trace if k == kind]


def test_the_pane_becomes_the_claude_pane(shell_in_tmux, monkeypatch):
    _panes(monkeypatch, ["%3"])

    with pytest.raises(_Exec):
        task_cmd._launch_claude_for_claim(77, 3, "/wt/e-77")

    assert _kind(shell_in_tmux, "exec") == [["/bin/claude", "claude"]]


def test_the_pane_is_laid_out_before_the_exec(shell_in_tmux, monkeypatch):
    """The exec replaces this process, so anything it meant to orchestrate
    afterward never happens."""
    _panes(monkeypatch, ["%3"])

    with pytest.raises(_Exec):
        task_cmd._launch_claude_for_claim(77, 3, "/wt/e-77")

    kinds = [k for k, _ in shell_in_tmux]
    assert kinds.index("options") < kinds.index("exec")
    assert kinds.index("layout") < kinds.index("exec")
    assert _kind(shell_in_tmux, "layout") == ["%3"]


def test_the_window_is_renamed_for_the_task(shell_in_tmux, monkeypatch):
    """Without this the tab keeps whatever the shell was called, and the window
    that now holds E-77 does not say so."""
    _panes(monkeypatch, ["%3"])

    with pytest.raises(_Exec):
        task_cmd._launch_claude_for_claim(77, 3, "/wt/e-77")

    assert _kind(shell_in_tmux, "tmux") == [
        ["rename-window", "-t", "%3", task_cmd.tmux_window_name(77)]
    ]


def test_a_populated_window_is_refused(shell_in_tmux, monkeypatch):
    """Taking over a pane here would resize panes the user arranged, and
    quietly opening a new window instead is a different outcome from the one
    the command implies — the caller would be left looking at an unchanged
    screen. So it refuses, and says how not to hit it again."""
    import click
    _panes(monkeypatch, ["%3", "%4", "%5"])

    with pytest.raises(click.ClickException) as err:
        task_cmd._require_launchable_window(77)

    msg = err.value.format_message()
    assert "3 panes" in msg
    assert "tmux new-window" in msg
    assert "endless task claim E-77" in msg
    assert "--unattended" in msg


def test_no_tmux_is_refused_with_the_unattended_route(shell_in_tmux, monkeypatch):
    import click
    _panes(monkeypatch, None)

    with pytest.raises(click.ClickException) as err:
        task_cmd._require_launchable_window(77)

    msg = err.value.format_message()
    assert "no tmux" in msg
    assert "endless task claim E-77 --unattended" in msg


def test_a_lone_pane_is_launchable(shell_in_tmux, monkeypatch):
    _panes(monkeypatch, ["%3"])

    task_cmd._require_launchable_window(77)   # does not raise

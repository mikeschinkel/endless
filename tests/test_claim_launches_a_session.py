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
pin the shell caller's: a shell cannot BECOME the session, so one is started —
through the same `spawn-window` seam `task spawn` uses, in a window of its own,
without spawn's handoff.

An earlier revision took over the pane the claim was typed in, exec'ing Claude
over the shell and splitting the window around it. That cost the caller their
shell and forced a refusal for any window holding another pane; both were
consequences of the exec rather than requirements of the claim, and both went
with it.
"""

import click
import pytest

from endless import task_cmd


@pytest.fixture
def shell_in_tmux(monkeypatch):
    """A shell pane in tmux, with claude and the Go launcher stubbed.

    Yields the argv of every `endless-go` invocation the claim made.
    """
    import types

    calls: list[list[str]] = []
    monkeypatch.setenv("TMUX", "/tmp/tmux-501/default,1,0")
    monkeypatch.setenv("TMUX_PANE", "%3")
    monkeypatch.setattr(task_cmd, "_claude_binary", lambda: "/bin/claude")
    monkeypatch.setattr(
        "endless.event_bridge._resolve_endless_go",
        lambda *a, **kw: "/bin/endless-go",
    )

    def fake_run(argv, **kw):
        calls.append(list(argv))

        class _R:
            returncode = 0
        return _R()

    # The NAME in task_cmd, not an attribute of the shared modules: everything
    # else that shells out or resolves a binary reaches the same module objects,
    # and would be stubbed along with them.
    monkeypatch.setattr(task_cmd, "subprocess", types.SimpleNamespace(run=fake_run))
    monkeypatch.setattr(
        task_cmd, "shutil", types.SimpleNamespace(which=lambda n: "/bin/claude"),
    )
    return calls


def _flag(argv, name):
    return argv[argv.index(name) + 1]


def test_it_goes_through_the_same_seam_task_spawn_uses(shell_in_tmux):
    """Not a second way to launch Claude. `spawn-window` sets the window
    options before the exec, builds the layout, and deletes the handoff — all
    of which a hand-rolled launcher would have to re-implement and drift on."""
    task_cmd._launch_claude_for_claim(77, 3, "/wt/e-77")

    argv = shell_in_tmux[0]
    assert argv[:2] == ["/bin/endless-go", "spawn-window"]
    assert _flag(argv, "--task-id") == "77"
    assert _flag(argv, "--project-id") == "3"
    assert _flag(argv, "--cwd") == "/wt/e-77"
    assert _flag(argv, "--claude-bin") == "/bin/claude"


def test_the_callers_shell_is_left_alone(shell_in_tmux, monkeypatch):
    """The whole point of the window of its own. An earlier revision exec'd
    over this pane, taking the user's shell — history, cwd, whatever they were
    part-way through — with it."""
    monkeypatch.setattr(
        task_cmd.os, "execvp",
        lambda *a: pytest.fail("claim must not exec over the caller's shell"),
    )
    monkeypatch.setattr(
        task_cmd.os, "chdir",
        lambda p: pytest.fail("claim must not move the caller's shell"),
    )

    task_cmd._launch_claude_for_claim(77, 3, "/wt/e-77")


def test_no_handoff_is_delivered(shell_in_tmux):
    """Spawn's handoff is what re-reads a task as if it were new, and picking
    work back UP is this task's subject. The launcher spells "a bare
    interactive claude" as an empty handoff file, so that is what it gets."""
    task_cmd._launch_claude_for_claim(77, 3, "/wt/e-77")

    with open(_flag(shell_in_tmux[0], "--handoff-file")) as f:
        assert f.read() == ""


def test_the_spawn_marker_is_set_so_sessionstart_binds(shell_in_tmux):
    """The session does not exist yet, so claim cannot bind it. A non-empty
    `--spawned-by` is what makes SessionStart take the spawn-bind path (reading
    @endless_task_id) rather than falling back to deriving the task from cwd."""
    task_cmd._launch_claude_for_claim(77, 3, "/wt/e-77")

    assert _flag(shell_in_tmux[0], "--spawned-by")


def test_the_window_is_named_for_the_task(shell_in_tmux):
    task_cmd._launch_claude_for_claim(77, 3, "/wt/e-77")

    assert _flag(shell_in_tmux[0], "--window-name") == task_cmd.tmux_window_name(77)


def test_a_populated_window_is_no_longer_refused(shell_in_tmux):
    """The refusal existed only because the launch took over the caller's pane
    and split the window around it. A window of its own disturbs nothing, so
    there is nothing left to refuse."""
    task_cmd._require_tmux_for_claim(77)   # does not raise


def test_no_tmux_is_refused_with_the_unattended_route(monkeypatch):
    monkeypatch.delenv("TMUX", raising=False)

    with pytest.raises(click.ClickException) as err:
        task_cmd._require_tmux_for_claim(77)

    msg = err.value.format_message()
    assert "no tmux" in msg
    assert "endless task claim E-77 --unattended" in msg

"""Resuming a Claude session that is already running is refused (E-2519).

A user ran `session goto E-N --resume` from a window in another project. goto
read the session as not live and relaunched it, so a second `claude --resume`
started on a transcript a running Claude was still writing. Two fixes: goto
now addresses Endless's task-session server from wherever it is typed, so a
session live anywhere has a reachable pane and is simply focused; and every
resume launch checks the target's UUID against the live sessions of every
project, and against the running `claude` processes, before it launches —
the guard that holds even if a lookup elsewhere misses.
"""

import click
import pytest

from endless import session_cmd
from test_session_goto_back import _stage_resumable, goto_env  # noqa: F401


ELSEWHERE = "/somewhere/else/other-project"
UUID = "a1b2c3d4-0000-4000-8000-000000002208"


class _Exec(Exception):
    """Raised by the stub execvp so a test stops where the real one would."""


@pytest.fixture
def running_elsewhere(goto_env, registered_project, monkeypatch, tmp_path):
    """E-2208's session is live under another project, in pane %10 of the
    task-session server. The command is typed in the console server (`here=
    False`): the incident's shape, where goto read the pane as unreachable."""
    stage, make = goto_env
    stage(project_root=ELSEWHERE, endless_session_id=1338, pane_id="%10",
          task_id=2208, harness_session_id=UUID, liveness="live")
    ft = make({"%10"}, current_pane="%3", here=False)
    _stage_resumable(monkeypatch, registered_project, uuid=UUID, eid=1338,
                     task=2208, tmp_path=tmp_path)
    monkeypatch.setattr(session_cmd, "_claude_resume_processes",
                        lambda uuid: [])
    return ft


def test_goto_resume_from_the_console_focuses_a_session_live_in_another_project(
    running_elsewhere, capsys,
):
    """The incident itself: goto reaches the task-session server from the
    console, finds the pane, and shows it — nothing is relaunched."""
    ft = running_elsewhere

    session_cmd.session_goto("E-2208", resume=True)

    assert ft.new_windows == [], "a second claude was launched on a running session"
    assert ft.selected == ["%10"]
    assert "goto E-2208" in capsys.readouterr().err


@pytest.fixture
def running_off_server(goto_env, registered_project, monkeypatch, tmp_path):
    """E-2208's session is live, bound to a pane on some OTHER tmux server:
    no pane on the task-session server, so goto cannot reach it, and the
    running check is what stands between `--resume` and a second Claude."""
    stage, make = goto_env
    stage(project_root=ELSEWHERE, endless_session_id=1338, pane_id="",
          task_id=2208, harness_session_id=UUID, liveness="live")
    ft = make({"%cur"}, current_pane="%cur")
    _stage_resumable(monkeypatch, registered_project, uuid=UUID, eid=1338,
                     task=2208, tmp_path=tmp_path)
    monkeypatch.setattr(session_cmd, "_claude_resume_processes",
                        lambda uuid: [])
    return ft


def test_goto_resume_refuses_a_target_running_on_another_tmux_server(
    running_off_server, capsys,
):
    ft = running_off_server

    with pytest.raises(SystemExit) as exc:
        session_cmd.session_goto("E-2208", resume=True)

    assert exc.value.code == 1
    assert ft.new_windows == [], "a second claude was launched on a running session"
    msg = capsys.readouterr().err
    assert "already running" in msg
    assert "not on Endless's task-session server" in msg
    assert "Reach that pane on its own tmux server" in msg
    assert "goto E-2208" not in msg, "goto cannot reach it either"


def test_session_resume_refuses_a_target_live_on_the_tasks_server(
    goto_env, registered_project, monkeypatch, tmp_path, capsys,
):
    """Typed in a pane of the task-session server while the session is live in
    another of its panes: refused, and goto is the way to it."""
    stage, make = goto_env
    stage(project_root=ELSEWHERE, endless_session_id=1338, pane_id="%10",
          task_id=2208, harness_session_id=UUID, liveness="live")
    make({"%10", "%cur"}, current_pane="%cur")
    _stage_resumable(monkeypatch, registered_project, uuid=UUID, eid=1338,
                     task=2208, tmp_path=tmp_path)
    monkeypatch.setattr(session_cmd, "_claude_resume_processes",
                        lambda uuid: [])
    launched: list[list[str]] = []

    def fake_exec(file, argv):
        launched.append(list(argv))
        raise _Exec()

    monkeypatch.setattr(session_cmd.os, "execvp", fake_exec)
    monkeypatch.setattr(session_cmd.os, "chdir", lambda p: None)
    monkeypatch.setattr(session_cmd, "_current_pane_task", lambda: None)
    monkeypatch.setattr(session_cmd, "_tmux_window_pane_ids", lambda: ["%cur"])
    monkeypatch.setattr(session_cmd, "_require_window_claim",
                        lambda ref, rebind: None)
    monkeypatch.setattr(session_cmd, "build_pane_layout", lambda pane, cwd: None)

    with pytest.raises(click.ClickException) as exc:
        session_cmd.resume_session("E-2208")

    assert launched == [], "claude --resume was exec'd on a running session"
    msg = exc.value.format_message()
    assert "already running" in msg
    assert "pane %10 of Endless's task-session server" in msg
    assert "endless session goto E-2208" in msg
    assert "Drop --resume" not in msg      # there is no flag to drop here


def test_session_resume_refuses_a_target_running_on_another_tmux_server(
    running_off_server, monkeypatch, capsys,
):
    launched: list[list[str]] = []

    def fake_exec(file, argv):
        launched.append(list(argv))
        raise _Exec()

    monkeypatch.setattr(session_cmd.os, "execvp", fake_exec)
    monkeypatch.setattr(session_cmd.os, "chdir", lambda p: None)
    monkeypatch.setattr(session_cmd, "_current_pane_task", lambda: None)
    monkeypatch.setattr(session_cmd, "_tmux_window_pane_ids", lambda: ["%cur"])
    monkeypatch.setattr(session_cmd, "_require_window_claim",
                        lambda ref, rebind: None)
    monkeypatch.setattr(session_cmd, "build_pane_layout", lambda pane, cwd: None)

    with pytest.raises(click.ClickException) as exc:
        session_cmd.resume_session("E-2208")

    assert launched == [], "claude --resume was exec'd on a running session"
    msg = exc.value.format_message()
    assert "already running" in msg
    assert "not on Endless's task-session server" in msg
    assert "goto E-2208" not in msg


# ── the process check ───────────────────────────────────────────────────────


class _PS:
    """A `ps -axo pid=,ppid=,args=` listing: this test process (pid 100, child
    of a shell 50), plus whatever rows a test adds."""

    def __init__(self, rows=()):
        self.rows = [(50, 1, "-zsh"), (100, 50, "python endless session goto E-2208 --resume")]
        self.rows += list(rows)

    def run(self, argv, **kw):
        assert argv[:2] == ["ps", "-axo"]

        class _Out:
            stdout = "".join(f"{pid:>6} {ppid:>6} {cmd}\n" for pid, ppid, cmd in self.rows)
            returncode = 0
        return _Out()


@pytest.fixture
def ps(monkeypatch):
    import subprocess
    monkeypatch.setattr(session_cmd.os, "getpid", lambda: 100)

    def _install(*rows):
        fake = _PS(rows)
        monkeypatch.setattr(subprocess, "run", fake.run)
        return fake
    return _install


def test_a_claude_resume_process_on_the_uuid_is_found(ps):
    ps((7001, 1, f"/Users/me/.local/bin/claude --resume {UUID}"))
    found = session_cmd._claude_resume_processes(UUID)
    assert [pid for pid, _ in found] == [7001]


@pytest.mark.parametrize("cmd", [
    f"claude --resume={UUID}",
    f"claude -r {UUID}",
    f"/bin/zsh -c /usr/local/bin/claude --resume {UUID}",
    f"node /opt/claude-code/cli.js --resume {UUID} --verbose",
])
def test_every_spelling_of_a_resume_is_found(ps, cmd):
    ps((7001, 1, cmd))
    assert len(session_cmd._claude_resume_processes(UUID)) == 1


def test_other_uuids_other_commands_and_our_own_ancestry_are_not(ps):
    ps(
        (7001, 1, "claude --resume a1b2c3d4-0000-4000-8000-000000009999"),
        (7002, 1, f"claude --resume {UUID[:8]}"),           # a prefix is not the uuid
        (7003, 1, f"grep --resume {UUID} notes.txt"),       # not claude
        (7004, 1, f"claude {UUID}"),                        # no --resume
        (101, 100, f"endless session goto --resume {UUID}"),  # our own child? no: not claude
    )
    # Our own argv names the uuid after --resume (pid 100), and so does the
    # shell function that launched us; neither is a running Claude.
    fake = ps((50, 1, f"zsh endless session goto --resume {UUID} claude-wrapper"))
    fake.rows[1] = (100, 50, f"python endless session goto --resume {UUID}")
    assert session_cmd._claude_resume_processes(UUID) == []


def test_ps_failure_finds_nothing(monkeypatch):
    import subprocess

    def boom(*a, **k):
        raise OSError("no ps")
    monkeypatch.setattr(subprocess, "run", boom)
    assert session_cmd._claude_resume_processes(UUID) == []


# ── the guard's two sources and its exemptions ─────────────────────────────


@pytest.fixture
def quiet(goto_env, registered_project, monkeypatch, tmp_path):
    """E-2208's session has ended: not live anywhere, no process resuming it.
    Tests then add one source or the other."""
    stage, make = goto_env
    ft = make({"%cur"}, current_pane="%cur")
    _stage_resumable(monkeypatch, registered_project, uuid=UUID, eid=1338,
                     task=2208, tmp_path=tmp_path)
    monkeypatch.setattr(session_cmd, "_claude_resume_processes", lambda uuid: [])
    return stage, ft


def test_a_claude_resume_process_refuses_the_launch(quiet, monkeypatch, capsys):
    _stage, ft = quiet
    monkeypatch.setattr(
        session_cmd, "_claude_resume_processes",
        lambda uuid: [(7001, f"claude --resume {uuid}")] if uuid == UUID else [],
    )

    with pytest.raises(SystemExit):
        session_cmd.session_goto("E-2208", resume=True)

    assert ft.new_windows == []
    err = capsys.readouterr().err
    assert "already running" in err and "process 7001" in err


@pytest.mark.parametrize("liveness", ["unknown", "unbound"])
def test_a_target_reading_unknown_or_unbound_stays_resumable(quiet, liveness):
    stage, ft = quiet
    stage(project_root=ELSEWHERE, endless_session_id=1338, pane_id="",
          task_id=2208, harness_session_id=UUID, liveness=liveness)

    session_cmd.session_goto("E-2208", resume=True)

    assert len(ft.new_windows) == 1
    assert ft.new_windows[0][-1].endswith(f"claude --resume {UUID}")


def test_a_target_not_running_anywhere_is_resumed(quiet):
    _stage, ft = quiet

    session_cmd.session_goto("E-2208", resume=True)

    assert len(ft.new_windows) == 1


def test_new_transcript_is_not_checked(quiet, monkeypatch):
    """`--new-transcript` opens a NEW transcript; the running session's is not
    touched, so a running target does not refuse it."""
    stage, ft = quiet
    stage(project_root=ELSEWHERE, endless_session_id=1338, pane_id="",
          task_id=2208, harness_session_id=UUID, liveness="live")
    monkeypatch.setattr(
        session_cmd, "_claude_resume_processes",
        lambda uuid: [(7001, f"claude --resume {uuid}")],
    )

    session_cmd.session_goto("E-2208", resume=True, new_transcript=True)

    assert len(ft.new_windows) == 1
    assert "--resume" not in ft.new_windows[0][-1]


def test_background_resume_refuses_a_running_target_naming_goto(
    quiet, monkeypatch, capsys,
):
    stage, ft = quiet
    stage(project_root=ELSEWHERE, endless_session_id=1338, pane_id="",
          task_id=2208, harness_session_id=UUID, liveness="live")
    monkeypatch.delenv("TMUX", raising=False)

    with pytest.raises(SystemExit):
        session_cmd.session_goto("E-2208", resume=True, background=True,
                                 target_session="$3")

    assert ft.new_windows == []
    err = capsys.readouterr().err
    assert "already running" in err
    assert "not on Endless's task-session server" in err
    assert "Drop --resume" not in err


def test_goto_without_resume_does_not_send_a_running_target_to_resume(
    quiet, capsys,
):
    """The not-live hint used to say `--resume`; for a target that is running
    in a pane goto cannot reach, that hint and the new refusal would point at
    each other."""
    stage, ft = quiet
    stage(project_root=ELSEWHERE, endless_session_id=1338, pane_id="",
          task_id=2208, harness_session_id=UUID, liveness="live")

    with pytest.raises(SystemExit):
        session_cmd.session_goto("E-2208")

    err = capsys.readouterr().err
    assert "It is running" in err
    assert "Do not --resume it" in err
    assert "Reach that pane on its own tmux server" in err
    assert "goto E-2208 --resume" not in err


def test_goto_without_resume_names_the_process_holding_a_target(
    quiet, monkeypatch, capsys,
):
    stage, ft = quiet
    monkeypatch.setattr(
        session_cmd, "_claude_resume_processes",
        lambda uuid: [(7001, f"claude --resume {uuid}")],
    )

    with pytest.raises(SystemExit):
        session_cmd.session_goto("E-2208")

    err = capsys.readouterr().err
    assert "process 7001" in err
    assert "Exit or message that" in err
    assert "goto E-2208 --resume" not in err


# ── `session resume` runs only in a task-session server pane (E-2519) ───────
#
# Every Claude Endless runs is on that server, and nowhere else is a place a
# resumed session can be found again. Typed anywhere else, resume refuses and
# names the two routes that open the session there. These carry the
# `tasks_server` marker so conftest leaves the gate in place.


@pytest.fixture
def resumable_quiet(goto_env, registered_project, monkeypatch, tmp_path):
    """E-2208's session has ended and nothing is resuming it; the exec that
    would replace this process is captured instead."""
    stage, make = goto_env
    _stage_resumable(monkeypatch, registered_project, uuid=UUID, eid=1338,
                     task=2208, tmp_path=tmp_path)
    monkeypatch.setattr(session_cmd, "_claude_resume_processes", lambda uuid: [])
    launched: list[list[str]] = []

    def fake_exec(file, argv):
        launched.append(list(argv))
        raise _Exec()

    monkeypatch.setattr(session_cmd.os, "execvp", fake_exec)
    monkeypatch.setattr(session_cmd.os, "chdir", lambda p: None)
    monkeypatch.setattr(session_cmd, "_current_pane_task", lambda: None)
    monkeypatch.setattr(session_cmd, "_tmux_window_pane_ids", lambda: ["%cur"])
    monkeypatch.setattr(session_cmd, "_require_window_claim",
                        lambda ref, rebind: None)
    monkeypatch.setattr(session_cmd, "build_pane_layout", lambda pane, cwd: None)
    return make, launched


@pytest.mark.tasks_server
def test_session_resume_refuses_outside_the_tasks_server(resumable_quiet):
    make, launched = resumable_quiet
    make({"%cur"}, current_pane="%3", here=False)

    with pytest.raises(click.ClickException) as exc:
        session_cmd.resume_session("E-2208")

    assert launched == [], "claude was exec'd outside the task-session server"
    msg = exc.value.format_message()
    assert "not on Endless's task-session server" in msg
    assert "endless session goto E-2208 --resume" in msg
    assert "--new-window" in msg


@pytest.mark.tasks_server
def test_session_resume_in_a_tasks_server_pane_launches(resumable_quiet):
    make, launched = resumable_quiet
    make({"%cur"}, current_pane="%cur")

    with pytest.raises(_Exec):
        session_cmd.resume_session("E-2208")

    assert launched and launched[0][-2:] == ["--resume", UUID]


@pytest.mark.tasks_server
def test_session_resume_dry_run_is_not_gated(resumable_quiet, capsys):
    make, launched = resumable_quiet
    make({"%cur"}, current_pane="%3", here=False)

    session_cmd.resume_session("E-2208", dry_run=True)

    assert launched == []
    assert UUID in capsys.readouterr().out


@pytest.mark.tasks_server
def test_session_resume_refuses_while_endless_is_not_running(
    resumable_quiet, monkeypatch,
):
    from endless import tmux_cmd
    make, launched = resumable_quiet
    make({"%cur"}, current_pane="%cur")
    monkeypatch.setattr(tmux_cmd, "tasks_server", lambda: None)
    session_cmd._tasks_server_cache.clear()

    with pytest.raises(click.ClickException) as exc:
        session_cmd.resume_session("E-2208")

    assert launched == []
    assert "Endless isn't running" in exc.value.format_message()


@pytest.mark.tasks_server
@pytest.mark.parametrize("here", [True, False])
def test_session_resume_new_window_opens_on_the_tasks_server(
    resumable_quiet, capsys, here,
):
    """`--new-window` is `goto <ref> --resume` under resume's name: a new
    window of the task-session server, then focused the way goto focuses."""
    make, launched = resumable_quiet
    ft = make({"%cur"}, current_pane="%cur" if here else "%3", here=here)

    session_cmd.resume_session("E-2208", new_window=True)

    assert launched == [], "nothing is exec'd in this pane"
    assert len(ft.new_windows) == 1
    assert ft.new_windows[0][-1].endswith(f"claude --resume {UUID}")
    if here:
        assert ft.switched == ["%new1"]
    else:
        assert ft.selected == ["%new1"] and ft.switched == []
        assert "task-session server" in capsys.readouterr().err


@pytest.mark.tasks_server
def test_session_resume_new_window_refuses_flags_about_this_pane(resumable_quiet):
    make, launched = resumable_quiet
    ft = make({"%cur"}, current_pane="%cur")

    with pytest.raises(click.ClickException) as exc:
        session_cmd.resume_session("E-2208", new_window=True, force=True,
                                   rebind=True)

    assert ft.new_windows == [] and launched == []
    msg = exc.value.format_message()
    assert "--force" in msg and "--rebind" in msg
    assert "--new-window" in msg

"""Thin Python wrapper around the `endless-go tmux` subcommand (E-1236).

The Go binary owns all logic (DB reads, tmux config calls) so the
status-line printer stays under the latency budget. Python here is
just ergonomic surface: `endless tmux apply` / `endless tmux status-line`.

`run_active_id` is the exception to "tmux surface": it backs the
user-facing `endless task id` (and its `endless tmux task` alias), which
is a session-orientation verb rather than a tmux one. It lives here
because the thing it wraps is `endless-go tmux active-id`, and one
module owning that shellout beats two spellings of it. E-1302.
"""

import os
import shutil
import subprocess

import click

from endless import agent_help


def _binary() -> str:
    """Locate the endless-go Go binary or raise a friendly error."""
    path = shutil.which("endless-go")
    if not path:
        # An agent cannot install a binary or edit the user's PATH, and every
        # tmux verb here is a shellout to it, so there is no second way to try.
        raise agent_help.report(
            "endless-go is not on PATH, so nothing could be run.",
            "how endless-go gets installed, or where it goes on PATH",
            text="endless-go binary not found on PATH.",
        )
    return path


def run_apply(hotkey: str, status_interval: int) -> None:
    """Shell out to `endless-go tmux apply` with the given options."""
    cmd = [
        _binary(), "tmux", "apply",
        "--hotkey", hotkey,
        "--status-interval", str(status_interval),
    ]
    # Inherit stdio so the user sees the tmux output / errors in real time.
    result = subprocess.run(cmd)
    if result.returncode != 0:
        # The child's stderr went straight to the terminal and was never
        # captured, so there is nothing here to classify or relay: endless-go
        # already said what happened, with its own verdict. Anything this
        # wrapper added would be Endless explaining an outcome it did not
        # produce, in a second voice.
        agent_help.passthrough_exit(result.returncode)


def run_init(hotkey: str, status_interval: int) -> None:
    """Shell out to `endless-go tmux init` with the given options.

    Self-gates via @server_uuid: first call after a tmux server start
    runs reset + apply and stamps a UUID; subsequent calls no-op.
    """
    cmd = [
        _binary(), "tmux", "init",
        "--hotkey", hotkey,
        "--status-interval", str(status_interval),
    ]
    result = subprocess.run(cmd)
    if result.returncode != 0:
        agent_help.passthrough_exit(result.returncode)  # as in run_apply


def run_status_line() -> None:
    """Shell out to `endless-go tmux status-line` and pass stdout through.

    Not normally typed by users — tmux's status-format[1] invokes the Go
    binary directly. Provided for parity and manual debugging.
    """
    result = subprocess.run([_binary(), "tmux", "status-line"], capture_output=True, text=True)
    if result.stdout:
        # No newline — `#()` substitution wants the raw bytes.
        click.echo(result.stdout, nl=False)
    if result.returncode == 0:
        return
    if result.stderr:
        # Captured here, unlike apply/init, so Go's own classified refusal can
        # be passed through whole. Relay writes it verbatim: re-wording it here
        # would put a Python verdict on top of a Go one that already says the
        # same thing.
        raise agent_help.relay(result.stderr, exit_code=result.returncode)
    agent_help.passthrough_exit(result.returncode)


def _no_task_refusal(pane: str | None, invoked_as: str, exit_code: int):
    """The no-active-task refusal, phrased for how we got here.

    A session's task binding is keyed by the tmux pane it runs in, so "no
    task" has two very different causes and two different fixes. Saying
    which one applies is the whole value of the message — the Go side exits
    silently because tmux redraws it many times a minute.

    Both are report_if and neither can be resolved here, because what decides
    the class is what the CALLER wanted the id for, which this command never
    sees. It is handed a pane and a spelling, not a purpose: an agent that only
    needed an id to pass to another verb can name its task from its own
    instructions and never bother the user, while one whose next step needs a
    pane-bound session genuinely cannot proceed. So both branches are named and
    the agent, which holds the conversation, picks.
    """
    if not (pane or os.environ.get("TMUX_PANE")):
        return agent_help.report_if(
            f"{invoked_as}: no active task — this shell is not inside tmux, "
            "and a session's task is bound to the tmux pane it runs in.",
            "the next step needs a pane-bound session rather than just the id",
            "name the task explicitly, from your instructions or "
            "`endless task list`",
            "only the user can start the session inside tmux",
            text=(f"{invoked_as}: no active task — a session's task is bound "
                  f"to the tmux pane it runs in, and this shell is not inside "
                  f"tmux."),
            exit_code=exit_code,
        )
    return agent_help.report_if(
        f"{invoked_as}: this session is inside tmux but has claimed no task.",
        "no task was assigned to you",
        "claim the task you were assigned with `endless task claim E-NNNN`",
        "choosing what to work on is the user's call",
        text=(f"{invoked_as}: no active task for this session. "
              f"Claim one with `endless task claim E-NNNN`."),
        exit_code=exit_code,
    )


def run_active_id(pane: str | None, invoked_as: str) -> None:
    """Print the current session's task as one bare `E-NNNN` line.

    Backs both `endless task id` and its `endless tmux task` alias. The
    binding lives in the database (`sessions.task_id`, resolved from the
    pane), so the Go binary owns the read and Python never touches SQLite.

    stdout stays a single bare id so `$(endless task id)` captures
    something usable; every diagnostic goes to stderr.
    """
    cmd = [_binary(), "tmux", "active-id"]
    if pane:
        cmd += ["--pane", pane]
    result = subprocess.run(cmd, capture_output=True, text=True)
    if result.returncode == 0:
        click.echo(result.stdout.strip())
        return
    # active-id exits 1 both for "no task" (silent) and for a real DB failure
    # (which it explains). Relay its explanation when there is one rather than
    # overwriting a genuine error with a guess about panes.
    stderr = result.stderr.strip()
    if stderr:
        raise agent_help.relay(stderr, exit_code=result.returncode or 1)
    raise _no_task_refusal(pane, invoked_as, result.returncode or 1)

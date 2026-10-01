"""CLI implementation for `endless sandbox reset` (E-1608).

Thin wrapper around `endless-go sandbox reset`, which clears the current
worktree's sandbox, adds Endless's standard contents, and runs the project's
.endless/hooks/seed-sandbox.sh. All of that lives in Go
(internal/sandboxcmd/reset.go); here we only exec it, so worktree creation, a
user, and `endless task verify` all go through the one implementation.
"""

import subprocess
from pathlib import Path

import click

from endless import agent_help
from endless.event_bridge import _resolve_endless_go


def run_reset() -> None:
    """Entry point bound by cli.py: reset the sandbox of the worktree at cwd."""
    result = subprocess.run([_resolve_endless_go(), "sandbox", "reset"])
    # The child inherited this process's stdout and stderr, so everything it
    # had to say is already written — including, on a refusal, Go's own
    # classified verdict at both ends of it. Ending with its status and saying
    # nothing is the whole of this side's job: a message here would explain an
    # outcome Endless's Python did not produce, in a second voice, with a
    # second directive for an agent to reconcile against Go's.
    agent_help.passthrough_exit(result.returncode)


def reset_after_create(worktree_path: Path) -> None:
    """Seed a freshly created worktree's sandbox via `sandbox reset`.

    Non-fatal and loud, like the post-worktree-create hook it follows: the
    worktree is kept, and the warning names the command that finishes the job.
    """
    rerun = f"cd {worktree_path} && endless sandbox reset"
    try:
        binary = _resolve_endless_go()
    except agent_help.Refusal as e:
        # Narrowed from ClickException because this branch now reads the
        # refusal's own fields, and _resolve_endless_go raises nothing else.
        #
        # No TSV row — post-create seeding landed after the refusal audit — but
        # the class is not this site's to invent: whether the reader can clear
        # this alone was already decided one layer down. A worktree build that
        # is missing or unbuilt is NO-REPORT (rebuild it and re-run), while no
        # endless-go on PATH at all is REPORT, because installing it happens
        # outside the session entirely. Carrying that class up beats handing
        # the agent a condition it would have to re-derive from the text.
        #
        # `e.text` rather than `e.format_message()`: for a human the two are
        # identical here (none of these refusals carries a human_remedy), and
        # for an agent it keeps the inner refusal's verdict and directive from
        # being nested inside this warning's own, which would leave two
        # directives disagreeing about whether to speak up.
        body = (
            click.style("⚠ could not seed the worktree's sandbox", fg="yellow")
            + f"\n    {e.text}\n    Re-run after fixing:\n        {rerun}"
        )
        summary = (f"The worktree at {worktree_path} was created and KEPT, but "
                   "its sandbox was not seeded: endless-go could not be "
                   "resolved.")
        if e.cls == agent_help.REPORT:
            agent_help.warn.report(summary, e.decision, text=body)
        else:
            agent_help.warn.no_report(
                summary,
                f"Finish the seeding with `{rerun}` once endless-go resolves",
                text=body)
        return
    result = subprocess.run(
        [binary, "sandbox", "reset"],
        cwd=str(worktree_path),
        stdout=subprocess.DEVNULL,
    )
    if result.returncode != 0:
        # No TSV row (post-audit surface). NO-REPORT, because the only fact
        # that belongs to this side is the one the message already acts on: the
        # worktree survived, and a single named command finishes the job — a
        # retry the agent runs itself. The underlying cause is not flattened
        # into that: `sandbox reset` wrote its own classified refusal to the
        # stderr it inherited, so if the project's seed-sandbox.sh is what
        # failed, Go's verdict for that is already on the stream above this.
        agent_help.warn.no_report(
            f"`endless-go sandbox reset` exited {result.returncode} in "
            f"{worktree_path}; the worktree was created and KEPT, but its "
            "sandbox is not seeded.",
            f"Finish the seeding with `{rerun}`",
            text=(
                click.style(
                    f"⚠ sandbox reset exited {result.returncode}", fg="yellow"
                )
                + f"\n    worktree: {worktree_path}\n"
                f"    The worktree was KEPT. Finish seeding its sandbox with:\n"
                f"        {rerun}"
            ),
        )

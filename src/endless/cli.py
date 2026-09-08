"""Endless CLI — Click entry point."""

import os
import re
import sys
from collections.abc import Sequence
from pathlib import Path

import click

from endless import __version__
from endless import agent_help
from endless import help_settings
from endless import project_status_cmd
from endless import rowcap
from endless.agent_help import AgentHelpMixin
from endless import statuses
from endless import session_states
from endless.statuses import TASK_STATUSES, TASK_STATUS_HELP

# Subcommands that are safe to run inside an `endless-go sandbox` subshell
# (no project/global I/O — pure stdout). Anything else is refused at
# CLI entry when ENDLESS_SANDBOX is set. New subcommands inherit the
# refusal automatically; opt in here only with a one-line justification
# in the diff.
SANDBOX_SAFE_SUBCOMMANDS = frozenset({
    "shell-init",  # static stdout, no I/O
})

# Subcommands exempt from the unsupported-harness refusal (E-1962/E-1997).
#
# The banner is advice addressed to an agent that deliberately typed an
# `endless` command. Anything Endless itself wires into a shell's startup is not
# that: it runs on every shell the harness spawns, including the one behind
# every Bash tool call, and the banner goes to stderr — which `$( )` does not
# capture — so it lands in the output of whatever command that shell was
# actually spawned to run. There "This command did not run" is a false
# statement about someone else's command, and "do not retry it, do not work
# around it" tells the agent to abandon real work.
#
# Keep this set to commands that (a) Endless installs into automatic execution
# and (b) have no side effect for the refusal to withhold. New subcommands
# inherit the refusal automatically; opt in here only with a one-line
# justification in the diff.
HARNESS_EXEMPT_SUBCOMMANDS = frozenset({
    # `endless setup shell-helpers` appends 'eval "$(endless shell-init)"' to
    # the user's rc, so this runs on every shell launch. Static stdout, no I/O
    # (see SANDBOX_SAFE_SUBCOMMANDS above) — nothing to refuse.
    "shell-init",
})


class TaskIDType(click.ParamType):
    """Click parameter type that accepts task IDs with optional E- prefix.

    Also routes ED-NN inputs (decision IDs) to a redirect error so users
    pointing a task verb at a decision get a clear, actionable message
    instead of an opaque "not found".
    """
    name = "task_id"

    def convert(self, value, param, ctx):
        if isinstance(value, int):
            return value
        s = str(value).strip()
        if s.upper().startswith("ED-"):
            self.fail(
                f"{value!r} is a decision ID (ED-NN); this command operates on tasks. "
                f"Use the corresponding 'decision' verb instead "
                f"(e.g. 'endless decision accept {value}').",
                param, ctx,
            )
        if s.upper().startswith("E-"):
            s = s[2:]
        try:
            return int(s)
        except ValueError:
            self.fail(f"{value!r} is not a valid task ID (expected integer or E-NNN)", param, ctx)


TASK_ID = TaskIDType()


class DecisionIDType(click.ParamType):
    """Click parameter type that accepts decision IDs with optional ED- prefix.

    E-NN inputs (task IDs) are redirected with a clear error pointing at the
    task verbs, so a user / agent that confuses the namespaces gets actionable
    output rather than a silent "not found".
    """
    name = "decision_id"

    def convert(self, value, param, ctx):
        if isinstance(value, int):
            return value
        s = str(value).strip()
        if s.upper().startswith("ED-"):
            s = s[3:]
        elif s.upper().startswith("E-"):
            self.fail(
                f"{value!r} is a task ID (E-NN); this command operates on decisions. "
                f"Use the corresponding 'task' verb instead.",
                param, ctx,
            )
        try:
            return int(s)
        except ValueError:
            self.fail(
                f"{value!r} is not a valid decision ID (expected integer or ED-NNN)",
                param, ctx,
            )


DECISION_ID = DecisionIDType()


class TaskOrDecisionIDType(click.ParamType):
    """Click type that accepts either E-NN or ED-NN and returns (kind, id).

    Used by kind-agnostic dispatchers (`task link --to <id>`, `decision link
    --to <id>`) so the verb can route on the parsed kind without re-parsing.
    """
    name = "task_or_decision_id"

    def convert(self, value, param, ctx):
        if isinstance(value, tuple):
            return value
        s = str(value).strip()
        upper = s.upper()
        if upper.startswith("ED-"):
            try:
                return ("decision", int(s[3:]))
            except ValueError:
                self.fail(f"{value!r} is not a valid decision ID", param, ctx)
        if upper.startswith("E-"):
            try:
                return ("task", int(s[2:]))
            except ValueError:
                self.fail(f"{value!r} is not a valid task ID", param, ctx)
        try:
            return ("task", int(s))
        except ValueError:
            self.fail(
                f"{value!r} is not a valid ID (expected E-NN, ED-NN, or integer)",
                param, ctx,
            )


TASK_OR_DECISION_ID = TaskOrDecisionIDType()

# TASK_STATUSES / TASK_STATUS_HELP are imported at the top of this module from
# endless.statuses, which owns the vocabulary so update_plan's validator and
# session_status_cmd read the same list (E-1956).


class MultiChoice(click.ParamType):
    """Click parameter type that accepts comma-separated values from a fixed set."""
    name = "multi_choice"

    def __init__(self, choices: Sequence[str]):
        self.choices = choices

    def convert(self, value, param, ctx):
        if value is None:
            return None
        parts = [v.strip() for v in str(value).split(",")]
        invalid = [p for p in parts if p not in self.choices]
        if invalid:
            self.fail(
                f"Invalid value(s): {', '.join(repr(v) for v in invalid)}. "
                f"Choose from: {', '.join(self.choices)}",
                param, ctx,
            )
        return parts


def _scan_db_choice(argv: list[str]) -> str | None:
    """Return the LAST `--db <val>` / `--db=<val>` value in argv, or None.

    Matches DBAwareGroup.main()'s consumption rule (last value wins) so the
    pre-Click re-exec gate (E-1513) sees the same effective --db as the
    cleaned-argv apply_db_choice call further down. Argv-agnostic helper so
    it's testable without a click.testing.CliRunner.
    """
    last: str | None = None
    i = 0
    while i < len(argv):
        a = argv[i]
        if a == "--db":
            if i + 1 < len(argv):
                last = argv[i + 1]
            i += 2
            continue
        if a.startswith("--db="):
            last = a[len("--db="):]
            i += 1
            continue
        i += 1
    return last


class DBAwareGroup(click.Group):
    """Click group that accepts the global --db flag in ANY argument position.

    Click only parses group options *before* the subcommand, which forces an
    awkward `endless --db main task show` and rejects the natural
    `endless task show --db main`. This override pre-extracts `--db <val>` /
    `--db=<val>` from anywhere in argv before normal parsing (mirroring the Go
    side's monitor.ConsumeDBContextFlag), applies it via config.apply_db_choice
    (the single resolver), and hands the cleaned args to Click. Because
    click.testing.CliRunner.invoke also calls Command.main(), the test suite
    gets the same position-agnostic behavior, and there is one extraction site
    (no separate entry-point wrapper, no pyproject change). E-1476.
    """

    def main(self, args=None, **extra):
        from endless import config

        argv = list(args) if args is not None else sys.argv[1:]

        # E-1513: under `--db sandbox` inside a self-dev worktree, re-exec into
        # the worktree's Python source via `uv run --directory <worktree>
        # endless ...`. The global `endless` script is the editable install of
        # main's source (one uv tool install), so without this gate the
        # worktree's Python changes never get exercised against its sandbox
        # DB — the symmetric gap to E-1510 (Go binary self-detect).
        # worktree_python_reexec_target's source_file check is the re-entrancy
        # guard: once we've re-exec'd, this module loads from inside the
        # worktree and the helper returns None.
        if _scan_db_choice(argv) == "sandbox":
            target = config.worktree_python_reexec_target()
            if target is not None:
                os.execvp(
                    "uv",
                    ["uv", "run", "--directory", str(target), "endless", *argv],
                )

        cleaned: list[str] = []
        db_value: str | None = None
        agent_view = False
        no_session = False
        i = 0
        while i < len(argv):
            arg = argv[i]
            if arg == "--db":
                if i + 1 >= len(argv):
                    click.echo("Error: --db requires a value: main or sandbox", err=True)
                    sys.exit(2)
                db_value = argv[i + 1]
                i += 2
                continue
            if arg.startswith("--db="):
                db_value = arg[len("--db="):]
                i += 1
                continue
            # --agent-view forces the agent --help rendering in any position so a
            # human can see/debug what an agent sees. Consumed here (like --db)
            # so per-command --help doesn't reject it as an unknown option.
            if arg == "--agent-view":
                agent_view = True
                i += 1
                continue
            # --no-session (E-1444): downgrade actor.kind to system at the write
            # layer for callers with no Claude session to attribute to (plain
            # shell, cron, scripts). Consumed here so it's accepted in any
            # position and applies uniformly to every subcommand's emit_event.
            if arg == "--no-session":
                no_session = True
                i += 1
                continue
            cleaned.append(arg)
            i += 1
        if agent_view:
            agent_help.set_agent_view(True)
        if no_session:
            config.NO_SESSION = True
        if db_value is not None:
            # apply_db_choice validates the value and resolves+pins the context;
            # it raises ValueError for an unknown value or for --db sandbox
            # outside a worktree. Format like a Click usage error (exit 2).
            try:
                config.apply_db_choice(db_value)
            except ValueError as e:
                click.echo(f"Error: {e}", err=True)
                sys.exit(2)
        return super().main(args=cleaned, **extra)


class SettingAwareMixin:
    """Append the live value of a setting that governs this command (E-2030).

    Opt-in per command via `<cmd>.governing_setting = help_settings.MINIMIZER`.
    Deliberately NOT the agent block's channel: that one renders only for an
    agent, and a human reading `endless minimizer --help` on a project where the
    loop is switched off needs to know that at least as much.

    Runs after `super().format_help`, so the setting is the last thing on the
    page rather than something to scroll past to reach the options.

    Never raises into help rendering — an unreadable config degrades to no
    section, exactly as a garbled map file degrades to no agent block.
    """

    def format_help(self, ctx, formatter):  # type: ignore[override]
        super().format_help(ctx, formatter)
        setting = getattr(self, "governing_setting", None)
        if not setting:
            return
        try:
            from endless import help_settings
            block = help_settings.render(setting)
        except Exception:
            block = None
        if block:
            formatter.write("\n" + block + "\n")


class AgentAwareCommand(AgentHelpMixin, SettingAwareMixin, click.Command):
    """Leaf command whose --help is augmented for agents (E-1502)."""


class AgentAwareGroup(AgentHelpMixin, SettingAwareMixin, DBAwareGroup):
    """Group whose --help is augmented for agents, and whose children inherit
    the augmenting classes so one root `cls=` propagates across the whole tree."""


# Make the decorators on any AgentAwareGroup (including the root) mint augmented
# children, so every command and subgroup gets the agent --help treatment.
AgentAwareGroup.command_class = AgentAwareCommand
AgentAwareGroup.group_class = AgentAwareGroup


@click.group(
    cls=AgentAwareGroup,
    epilog="Inside a self-dev worktree, pass --db (accepted in any position): "
    "--db main (the project's main database) or --db sandbox (this worktree's "
    "sandbox database). "
    "Resolve paths with `endless db path --db=main|sandbox`.",
)
@click.version_option(__version__, prog_name="endless")
@click.pass_context
def main(ctx):
    """Project awareness system for solo developers."""
    try:
        os.getcwd()
    except FileNotFoundError:
        click.echo(
            "endless: current working directory no longer exists "
            "(was it deleted from another shell?). cd to an existing "
            "directory and retry.",
            err=True,
        )
        ctx.exit(1)
    # E-1429/E-1476: --db is consumed and applied by DBAwareGroup.main() before
    # Click parses (so it works in any position). Enforcement of "required
    # inside a worktree" happens at the DB-access choke points (db.get_db /
    # go_db_context_args), so commands that never touch the DB stay flag-free.
    sandbox = os.environ.get("ENDLESS_SANDBOX")
    if sandbox and ctx.invoked_subcommand not in SANDBOX_SAFE_SUBCOMMANDS:
        click.echo(
            f"endless: refusing to run '{ctx.invoked_subcommand}' inside "
            f"endless-go sandbox at {sandbox}",
            err=True,
        )
        click.echo(
            "    Run 'exit' to leave the sandbox subshell, "
            "or open a new terminal.",
            err=True,
        )
        ctx.exit(1)

    # E-1962: last, so a broken cwd and the sandbox guard still report their own
    # (more specific) diagnosis first.
    _refuse_unsupported_agent(ctx)


@main.group("project")
def project_cmd():
    """Manage registered projects."""
    pass


@project_cmd.command("init")
@click.argument("path", default=".", type=click.Path(exists=True))
@click.option("--infer", is_flag=True, help="Auto-detect metadata, skip prompts")
@click.option("--name", default=None, help="Project identifier")
@click.option("--label", default=None, help="Display name")
@click.option("--desc", default=None, help="Description")
@click.option("--lang", default=None, help="Primary language")
@click.option("--status", default=None,
              type=click.Choice(["active", "paused", "archived", "idea"]))
def init(path, infer, name, label, desc, lang, status):
    """Initialize a directory as a project — registers the DB row and scaffolds
    .endless/config.json + .gitignore. Idempotent; safe to re-run."""
    from endless.register import register_project
    register_project(
        Path(path).resolve(),
        name=name, label=label, description=desc,
        language=lang, status=status, infer=infer,
    )


# `register` is an alias for `init` — same callback, provably identical behavior.
project_cmd.add_command(init, name="register")


@project_cmd.command("unregister")
@click.argument("name")
def unregister(name):
    """Unregister a project (preserves .endless config on disk)."""
    from endless.unregister import unregister_project
    unregister_project(name)


@project_cmd.command("purge")
@click.argument("name")
def purge(name):
    """Delete .endless/ directory and add to ignore list."""
    from endless.unregister import purge_project
    purge_project(name)


@project_cmd.command("set")
@click.argument("expression")
@click.option("--path", default=None,
              help="Path segment to disambiguate duplicate names")
def set_cmd(expression, path):
    """Set a project field. Usage: endless project set <name>.<field>=<value>"""
    from endless.set_cmd import set_field
    set_field(expression, path_hint=path)


@project_cmd.command("rename")
@click.argument("old_name")
@click.argument("new_name")
@click.option("--path", default=None,
              help="Path segment to disambiguate duplicate names")
def rename(old_name, new_name, path):
    """Rename a project."""
    from endless.rename import rename_project
    rename_project(old_name, new_name, path_hint=path)


@project_cmd.command("list")
@click.option("--status", default=None,
              type=click.Choice(["active", "paused", "archived", "idea"]),
              help="Filter by status")
@click.option("--group", is_flag=True, help="Group by group name")
@rowcap.limit_options
def list_cmd(status, group, limit, no_limit):
    """List registered projects."""
    from endless.list_cmd import list_projects
    list_projects(status_filter=status, group=group,
                  limit=limit, no_limit=no_limit)


# E-1976 renamed this command. `project status` now names the attention board
# below — the project-scoped counterpart to `session status` — and this, the
# project's metadata card, became `project info`. The pairing that results
# matches the session verbs exactly: `session show` is the card, `session status`
# is the board, and now so is the project trio.
@project_cmd.command("info")
@click.argument("name", default=None, required=False)
def info(name):
    """Show a project's registration card — metadata, notes and dependencies.

    Defaults to the project enclosing the working directory. For what in the
    project needs your attention, see `endless project status`.
    """
    from endless.status import show_status
    show_status(name)


@project_cmd.command("status")
@click.argument("name", default=None, required=False)
@click.option("--all", "show_all", is_flag=True,
              help="Include `ready` tasks — spawnable work, a claim on capacity "
                   "rather than attention")
@click.option("--json", "as_json", is_flag=True,
              help="Emit the rows as JSON, uncapped, each carrying its action")
@project_status_cmd.group_limit_options
def project_status(name, show_all, as_json, limit, no_limit):
    """Show what in this project needs attention — a one-shot snapshot.

    The project-scoped counterpart to `endless session status`. Ranks every
    attention claim in one list, loudest first: sessions blocked waiting on you,
    then unverified work awaiting your verdict, outcomes awaiting a read, plans
    awaiting approval, orphaned tasks nobody is holding, then the sessions that
    are idle or working. A live session and the task it claimed are one row.

    Defaults to the project enclosing the working directory; name another to see
    it from anywhere. For a live, self-updating view, use `endless project
    monitor`.

    The cap is PER GROUP, not per board: a single cap would spend every row on
    the unverified backlog and push the sessions off the bottom. Each truncated
    group says how many it left out.
    """
    project_status_cmd.project_status_resolve(
        name, show_all=show_all, limit=limit, no_limit=no_limit, as_json=as_json,
    )


@project_cmd.command("monitor")
@click.argument("name", default=None, required=False)
@click.option("--all", "show_all", is_flag=True,
              help="Include `ready` tasks — spawnable work, a claim on capacity "
                   "rather than attention")
@click.option("--tmux", "use_tmux", is_flag=True,
              help="Open the board in its own two-pane tmux session (monitor "
                   "above, a bare shell below) and switch to it. Idempotent.")
@click.option("--no-switch", is_flag=True,
              help="With --tmux: create the session but stay where you are.")
@project_status_cmd.group_limit_options
def project_monitor(name, show_all, use_tmux, no_switch, limit, no_limit):
    """Live board: repeatedly render `project status` until interrupted.

    The pane you keep open all day when several sessions are running. Loops the
    same view `project status` prints once, redrawing every 2 seconds and
    repainting only when the frame changes (no flicker). Ctrl-C exits.

    --tmux gives it the home it is designed for: its own tmux session, the board
    on top and a bare shell beneath it for running `endless` commands against
    what the board shows. Focus lands on the shell. Running it again switches to
    the session that already exists rather than making a second one.
    """
    if use_tmux:
        project_status_cmd.project_window_resolve(name, no_switch=no_switch)
        return
    if no_switch:
        raise click.UsageError("--no-switch only applies with --tmux.")
    project_status_cmd.project_status_resolve(
        name, monitor=True, show_all=show_all, limit=limit, no_limit=no_limit,
    )


@project_cmd.command("scan")
@click.option("--project", default=None, help="Scan a single project")
def scan(project):
    """Scan and reconcile projects."""
    from endless.scan import run_scan
    run_scan(project_name=project)


@project_cmd.command("discover")
@click.argument("path", default=None, required=False)
@click.option("--all", "show_all", is_flag=True,
              help="Include dormant projects in review")
@click.option("--reset", is_flag=True,
              help="Forget prior decisions, re-evaluate all directories")
def discover(path, show_all, reset):
    """Find and register unregistered projects."""
    from endless.discover import run_discover
    run_discover(discover_path=path, show_all=show_all, reset=reset)


# E-1756: the project-management verbs moved under `endless project <name>`.
# Leave a hidden hard-error stub at each old top-level name so invoking the bare
# old name exits non-zero with a redirect instead of a confusing "no such
# command". `hidden=True` keeps them out of `endless --help` and out of the
# guide-map tree walk (which skips hidden commands); `add_help_option=False` +
# `ignore_unknown_options` route every invocation — including old flags and
# `--help` — into the callback so it always errors with the redirect.
def _make_moved_stub(old_name):
    @click.argument("args", nargs=-1, type=click.UNPROCESSED)
    def _stub(args):
        raise click.ClickException(
            f"`endless {old_name}` moved to `endless project {old_name}`.\n"
            f"Run: endless project {old_name}"
        )
    return _stub


for _old_name in ("init", "register", "unregister", "purge", "set", "rename",
                  "list", "status", "scan", "discover"):
    main.command(_old_name, hidden=True, add_help_option=False,
                 context_settings=dict(ignore_unknown_options=True))(
        _make_moved_stub(_old_name))


def _refuse_unsupported_agent(ctx) -> None:
    """Refuse to run under an unsupported agent harness (E-1962).

    Endless supports Claude Code in a terminal and nothing else today. On any
    other harness its hooks do not fire the way the guide describes, so a session
    following Endless is following instructions for a machine it is not running
    on. Saying so once, plainly, beats letting it discover this one broken
    command at a time.

    Covers EVERY command, not just `endless guide`. A per-command refusal would
    still leave an agent walking the whole surface to learn what a single banner
    can say up front, and the failures it would hit on the way are the confusing
    kind (session-gated commands failing because there is no tmux pane to bind
    to) rather than the informative kind.

    The one carve-out is HARNESS_EXEMPT_SUBCOMMANDS: commands Endless wires into
    a shell's startup, which are never the deliberate invocation this banner is
    written for. Their stderr belongs to whatever the harness spawned that shell
    to run, and a banner there misreports someone else's command (E-1997). This
    is not a return to per-command gating — the exempt commands are the ones an
    agent never types.

    Fires only on a harness we can NAME, which is the opposite of how the hooks
    gate — and the asymmetry is deliberate. The hooks allow-list, failing closed,
    because they are enforcement and an unrecognized harness must not be silently
    governed. This refusal fails OPEN, because UNKNOWN is overwhelmingly a human
    at a shell prompt, and locking them out of their own tool to defend against a
    harness that may not exist is the worse trade.

    Cites no Endless task id. The message is "do not use Endless here"; pointing
    at a task would be telling the reader to use Endless to look up why it
    cannot. Tracking lives in the ledger, for people who can reach it.

    Exits ZERO, and this is the one place where "the command did not run" is
    better said in prose than in the exit status.

    An earlier version exited 1, reasoning from the ENDLESS_SANDBOX refusal above
    that a success status would be a lie a script could act on. That reasoning
    does not transfer, because the two refusals are different in kind. The
    sandbox refusal is RECOVERABLE — leave the subshell and run it again — so a
    non-zero status correctly says "act on this". This one is terminal: there is
    nothing to fix, nothing to retry, and no other way to run the command here.

    A non-zero status contradicts the message beside it. The banner says "do not
    treat it as a failure to diagnose" while the exit code says "a failure
    occurred", and the exit code is the channel agents read mechanically. A
    Claude Code Desktop session confirmed the pull is real — asked directly, it
    reported that "the exit=1 would normally be a pull toward debugging" and
    that it resisted only because the prose overrode it. An instruction that has
    to win a fight with its own exit status is a badly built instruction.

    The scripting objection does not survive contact either: nothing scripts
    `endless` on a harness Endless does not support, and on supported harnesses
    this never fires. What replaces the status is the explicit "This command did
    not run." line — a claim in the same channel as the rest of the message,
    rather than one buried in a number that says the opposite.
    """
    if ctx.invoked_subcommand in HARNESS_EXEMPT_SUBCOMMANDS:
        return

    from endless import agent_env

    harness = agent_env.detect()
    if harness == agent_env.UNKNOWN or agent_env.supported():
        return

    echo = lambda line="": click.echo(line, err=True)
    echo()
    echo(click.style(
        f"▸ Endless does not support {agent_env.label(harness)}.",
        fg="yellow", bold=True))
    echo()
    echo("  Endless supports Claude Code in the terminal only. Its hooks do not")
    echo("  fire here the way the guide assumes, so session tracking, task")
    echo("  claiming and worktree routing cannot work as documented.")
    echo()
    echo("  This command did not run. That is expected — do not retry it, do not")
    echo("  work around it, and do not treat it as a failure to diagnose.")
    echo()
    echo("  Ignore Endless for the rest of this session, including any Endless")
    echo("  instructions already in your context. A CLAUDE.md telling you to run")
    echo("  `endless guide` does not apply here.")
    echo()
    echo(click.style(
        "  Reading as a human? The guide is at docs/guide/index.md.", dim=True))
    echo()
    ctx.exit(0)


@main.command("guide")
@click.argument("section", required=False)
@click.option("--list", "list_sections", is_flag=True,
              help="List available section slugs and exit.")
def guide(section, list_sections):
    """Output the session guide (or a specific section).

    With no argument, prints the top-level index. Pass a section slug
    (e.g. 'spawn', 'worktree') to print just that section. Use --list
    to enumerate available sections.
    """
    guide_dir = (
        Path(__file__).resolve().parent.parent.parent / "docs" / "guide"
    )
    if not guide_dir.is_dir():
        raise click.ClickException(
            f"Guide directory not found at {guide_dir}"
        )

    available = sorted(
        p.stem for p in guide_dir.glob("*.md") if p.stem != "index"
    )

    if list_sections:
        for slug in available:
            click.echo(slug)
        return

    if section is None:
        target = guide_dir / "index.md"
        if not target.exists():
            raise click.ClickException(
                f"Guide index not found at {target}"
            )
    else:
        if section == "index" or section not in available:
            raise click.ClickException(
                f"Unknown section '{section}'. Available: "
                + ", ".join(available)
            )
        target = guide_dir / f"{section}.md"

    click.echo(render_guide_file(target), nl=False)


def guide_conditions() -> dict[str, bool]:
    """Every condition `docs/guide/*.md` may branch on, resolved for this cwd.

    The registry, not just a vars payload: a guide file that branches on a name
    absent from here renders its else-arm silently, so `{{if .report_gat}}` would
    quietly turn the report channel off for every reader. A test walks the guide
    and refuses any condition this function does not answer.

    `report_gate` is resolved by the same `_report_gate_on` the spawn handoff and
    the wind-down nudge use. A fourth reading of the config would be a fourth
    thing to keep in step, and the told-iff-gated invariant only holds while
    every emitter agrees.
    """
    from endless.task_cmd import _report_gate_on

    return {"report_gate": _report_gate_on()}


def render_guide_file(path: Path) -> str:
    """Render one guide file, resolving its conditional sections (E-2030).

    The guide used to be `cat`ed straight to stdout, which made every word of it
    unconditional — including the report channel's instructions, which
    `reportChannelOn` gates on the project's `report_gate`. A session on a
    gate-off project was therefore told to use a channel that would not gate it,
    the exact case that function's own comment forbids. Static text cannot
    honour a per-project switch, so the guide is now a Go text/template.

    Rendered through `endless-go template render --file` rather than a
    conditional syntax invented here. Endless already has one templating
    language and one set of `{{if .report_gate}}` branches (the handoff
    templates); a second one, in Python, would be two dialects to learn and two
    to keep in step. The `--file` mode exists because the guide lives in
    `docs/guide/` where humans read it, outside the tree `template render`
    resolves names in.

    `report_gate` is resolved by the same `_report_gate_on` the spawn handoff
    and the wind-down nudge use, not by a fourth reading of the config — the
    told-iff-gated invariant only holds while every emitter agrees.

    Fails loudly when the Go binary is unreachable or too old to know `--file`.
    The alternative is printing the file raw, which shows a reader
    `{{if .report_gate}}` and, worse, hands a gate-off session both branches at
    once. The failure names the rebuild, because the way to reach it is to
    upgrade the Python half without the Go half — `endless` and `endless-go` ship
    together and a drifted pair says so in a flag error nobody can read.
    """
    import json
    import subprocess

    from endless.event_bridge import _resolve_endless_go

    binary = _resolve_endless_go()
    result = subprocess.run(
        [binary, "template", "render", "--file", str(path)],
        input=json.dumps(guide_conditions()),
        capture_output=True, text=True, check=False,
    )
    if result.returncode != 0:
        detail = (result.stderr or "").strip()
        hint = ""
        if "not defined: -file" in detail:
            hint = ("\n\nThe endless-go on PATH predates `template render --file`, "
                    "which the guide needs. Rebuild and reinstall: `just install`.")
        raise click.ClickException(
            f"Could not render the guide from {path}"
            + (f": {detail}" if detail else ".") + hint
        )
    return result.stdout


_SHELL_INIT_SNIPPET = """\
# >>> endless shell helpers (regenerate via 'endless shell-init') >>>

# _endless_run — pick the right endless CLI for the current session.
# When ENDLESS_SESSION_ID is set, ask the global endless for the session's
# worktree path; if it returns a real directory, route the command through
# that worktree's checkout via 'uv run --directory ...' so worktree-only
# subcommands work without a global 'just install'. Otherwise fall back to
# the bare 'endless' on PATH. The lookup costs ~one subprocess per helper
# call (≈100ms), which we accept to keep ENDLESS_WORKTREE_PATH out of the
# exported environment — env vars are visible/inheritable forever, latency
# is invisible.
_endless_run() {
    # ${VAR:-} expansion keeps us safe under 'set -u' (nounset) — bare
    # "$ENDLESS_SESSION_ID" would error there when the var is unset.
    #
    # Every endless call here passes --db main. esu/esp/esf only ever wrap
    # 'session use/cd/forget', which operate on the main database
    # (~/.config/endless), never a per-worktree sandbox. --db main is the
    # default database anyway (a no-op for non-self-dev users), but it's
    # mandatory once cwd is a self-dev worktree: esu cd's us into the
    # session's worktree, so without it the self-dev --db gate rejects every
    # subsequent call (the lookup line gates first, then the fallback). It
    # never re-execs — only --db sandbox triggers the worktree re-exec.
    if [ -n "${ENDLESS_SESSION_ID:-}" ]; then
        local wt
        wt="$(endless --db main session cd --target worktree "$ENDLESS_SESSION_ID" 2>/dev/null)"
        if [ -n "$wt" ] && [ -d "$wt" ]; then
            uv run --directory "$wt" endless --db main "$@"
            return $?
        fi
    fi
    endless --db main "$@"
}

# esu — activate a Claude session in this shell (cd to its worktree
#       or cwd, plus export ENDLESS_SESSION_ID).
#   esu          → auto-resolve to sibling Claude pane in tmux
#   esu <id>     → explicit endless integer id or Claude UUID prefix
#   esu e-NNNN   → the live session whose active task is NNNN
esu() {
    local out
    out="$(_endless_run session use "$@")" || return $?
    eval "$out"
}

# esp — cd into a project root.
#   esp          → project root of the current session if one is active,
#                  otherwise the project root resolved from the cwd (so it
#                  works after the Claude session has exited)
#   esp <id>     → explicit endless integer id or Claude UUID prefix
# No session guard: resolving the project root only needs the cwd, not a
# session. 'session cd --target project' with no session-ref walks up from
# cwd to the registered project root.
esp() {
    local target
    target="$(_endless_run session cd --target project "$@")" || return $?
    cd "$target"
}

# esf — forget the current session ref (unset ENDLESS_SESSION_ID).
#       Inverse of esu. The session itself keeps running; only this
#       shell's pointer to it is cleared. Does not cd anywhere;
#       combine with esp if you also want to return to project root.
esf() {
    if [ -z "${ENDLESS_SESSION_ID:-}" ]; then
        echo "esf: no active session" >&2
        return 1
    fi
    local out
    out="$(_endless_run session forget)" || return $?
    eval "$out"
}

# esm — live session dashboard (repeatedly render `session status`
#       every 2s until Ctrl-C). Passes through --all/--tree.
#   esm            → monitor the focal task's session context
#   esm --all      → include done-work rows
#   esm --tree     → one IDs-only implementation-order tree frame
# No session guard: `session monitor` self-resolves the focal task from
# the tmux window / most-recent live session (like esp), so it works
# without ENDLESS_SESSION_ID set. Runs in the foreground (interactive
# dashboard) — no `eval`, since it prints frames, not shell code.
esm() {
    _endless_run session monitor "$@"
}

# eeh — show the recorded errors the session-status badge is counting,
#       plus how to dismiss them.
#   eeh            → list open errors
#   eeh --detail   → include every occurrence's full capture
#   eeh --all      → include already-cleared errors
# Exists because the badge has one row to spend and `endless errors show`
# does not fit beside the incident text it would be explaining.
# No session guard: errors are machine-local, not session-scoped.
eeh() {
    _endless_run errors show "$@"
}

# <<< endless shell helpers <<<
"""


_SQL_READ_PREFIXES = ("select", "with", "explain")


def _is_read_only_sql(sql: str) -> bool:
    stripped = sql.strip()
    while stripped.startswith("--"):
        nl = stripped.find("\n")
        if nl == -1:
            stripped = ""
            break
        stripped = stripped[nl + 1:].lstrip()
    while stripped.startswith("/*"):
        end = stripped.find("*/")
        if end == -1:
            stripped = ""
            break
        stripped = stripped[end + 2:].lstrip()
    head = stripped[:7].lower()
    return any(head.startswith(p) for p in _SQL_READ_PREFIXES)


@main.command("sql")
@click.argument("query")
@click.option("--write", is_flag=True,
              help="Allow mutating statements (INSERT/UPDATE/DELETE/PRAGMA/etc.). "
                   "Default is read-only — only SELECT/WITH/EXPLAIN are accepted.")
@click.option("--tsv", is_flag=True,
              help="Tab-separated output (no header). Useful for piping.")
@rowcap.limit_options
def sql_query(query, write, tsv, limit, no_limit):
    """Run a SQL query against the Endless DB.

    Resolves the DB path internally (no need to know where it lives).
    Read-only by default — pass --write for mutations. Replaces the
    agent instinct to reach for sqlite3 against speculative paths
    under .endless/, which silently creates ghost DB files.

    The table render stops at --limit rows and says how many it dropped;
    --tsv is a machine format and is uncapped unless you pass --limit.
    """
    from endless import db
    import sqlite3

    cap = rowcap.resolve_cap(limit, no_limit, machine=tsv)

    if not write and not _is_read_only_sql(query):
        raise click.ClickException(
            "Refusing to run a non-read-only query without --write. "
            "Allowed prefixes: SELECT, WITH, EXPLAIN.\n"
            "If you need to mutate, pass --write explicitly."
        )

    try:
        conn = db.get_db()
        cursor = conn.execute(query)
        rows = cursor.fetchall()
        # sqlite3's default isolation_level is "deferred" — an implicit
        # BEGIN opens on first mutation, and without an explicit commit()
        # the transaction rolls back on connection close. cursor.rowcount
        # reports affected rows even when uncommitted, which is what made
        # writes appear to succeed while silently being lost.
        if write:
            conn.commit()
    except sqlite3.Error as e:
        raise click.ClickException(f"SQL error: {e}")

    headers = [c[0] for c in cursor.description] if cursor.description else []
    if not rows:
        if write:
            click.echo(f"OK ({cursor.rowcount} rows affected)")
        return
    shown, hidden = rowcap.cap_rows(rows, cap)
    if tsv:
        if not headers:
            for r in shown:
                click.echo("\t".join(str(c) for c in r))
        else:
            for r in shown:
                click.echo("\t".join(str(r[h]) for h in headers))
        rowcap.echo_footer(hidden, llm=True, err=True)
        return

    from tabulate import tabulate
    table = [[r[h] for h in headers] for r in shown]
    click.echo(tabulate(table, headers=headers, tablefmt="simple"))
    rowcap.echo_footer(hidden)


@main.command("shell-init")
def shell_init():
    """Print shell helper functions for bash/zsh.

    Wraps 'endless session use', 'session cd --target project',
    'session forget', 'session monitor', and 'errors show' with short
    functions (esu, esp, esf, esm, eeh).

    To install, run:

      endless setup shell-helpers

    which adds 'eval "$(endless shell-init)"' to your ~/.zshrc so the
    helpers regenerate on every shell launch and always reflect the
    current snippet. For a manual install, add that eval line to your
    rc file directly (or, for bash, your ~/.bashrc).

    Because that eval runs on EVERY shell launch, this command is exempt from
    the unsupported-harness refusal (HARNESS_EXEMPT_SUBCOMMANDS) — the
    banner would otherwise be written to the stderr of every shell the harness
    spawns. The helpers it prints all call `endless`, so an unsupported harness
    still gets the banner the moment one is actually used.
    """
    click.echo(_SHELL_INIT_SNIPPET, nl=False)


@main.group("session")
def session_cmd():
    """View and manage session conversation history."""
    pass


@session_cmd.command("show")
@click.argument("session_ref", required=False, default=None)
@click.option("--json", "as_json", is_flag=True, help="JSON output")
def session_show(session_ref, as_json):
    """Show details for a Claude session — current by default.

    With no arg, in tmux: auto-resolves to the sole sibling Claude pane in
    the current window. Otherwise an endless integer id or Claude UUID
    prefix is required.
    """
    from endless.session_cmd import session_show_resolve
    session_show_resolve(session_ref, as_json=as_json)


@session_cmd.command("history")
@click.argument("session_id", required=False, default=None)
@click.option("--tools", "show_tools", default=None, flag_value="truncated",
              help="Include tool calls (truncated)")
@click.option("--tools-full", "show_tools", flag_value="full",
              help="Include full tool call content")
@click.option("--timestamps", is_flag=True,
              help="Show timestamps on each message")
@click.option("--sort", "sort_order", default="desc",
              type=click.Choice(["asc", "desc"]),
              help="Sort order (default: desc, newest first)")
@click.option("--json", "as_json", is_flag=True, help="JSON output")
@rowcap.limit_options
def session_history(session_id, show_tools, timestamps, limit, sort_order, as_json,
                    no_limit):
    """Show conversation history for a session.

    With no arg, defaults to the current session (same auto-resolution as
    session show).
    """
    from endless.session_cmd import show_history
    show_history(session_id, show_tools=show_tools,
                 show_timestamps=timestamps, limit=limit, no_limit=no_limit,
                 sort_asc=(sort_order == "asc"), as_json=as_json)


@session_cmd.command("status")
@click.option("--all", "show_all", is_flag=True,
              help="Include done-work (terminal-status) rows")
@click.option("--tree", is_flag=True,
              help="Render do/plan tasks as an IDs-only implementation-order tree")
@click.option("--show-hidden", is_flag=True,
              help="Render this session's hidden task rows too, marked ⊘")
@click.option("--only-hidden", is_flag=True,
              help="Render ONLY this session's hidden task rows")
@click.option("--json", "as_json", is_flag=True,
              help="Emit the rows as JSON (every row carries its hidden state)")
def session_status(show_all, tree, show_hidden, only_hidden, as_json):
    """Show the current session's status — a one-shot snapshot.

    Resolves the focal task for the current tmux window (live session's active
    task, else the window's @endless_task_id, else the most-recent live
    session), then renders the focal task, its parent (spawning) task, sibling
    tasks worked by sessions on the focal task, and any cross-session in-flight
    work, with blocked-by/blocks decorations. Reads the main DB. For a live,
    self-updating view, use `endless session monitor`.

    With --tree, render the do/plan backlog as an IDs-only tree in implementation
    order (nesting = order, siblings = parallelizable), derived from the
    blocked-by DAG and overridden by any per-session order (`endless session
    order`). No legend, titles, or icons.

    Tasks this session hid (`endless session hide --task <id>`) are omitted, with
    a '… N hidden' footer so they never vanish silently. --show-hidden renders
    them marked ⊘; --only-hidden renders the hidden set alone, which is how you
    find ids to unhide. Hiding is per-session and display-only: no other
    session's view changes, and nothing about the task does.
    """
    from endless.session_cmd import session_status_resolve
    session_status_resolve(show_all=show_all, tree=tree,
                           show_hidden=show_hidden, only_hidden=only_hidden,
                           as_json=as_json)


@session_cmd.command("monitor")
@click.option("--all", "show_all", is_flag=True,
              help="Include done-work (terminal-status) rows")
@click.option("--tree", is_flag=True,
              help="Render do/plan tasks as an IDs-only implementation-order tree")
@click.option("--show-hidden", is_flag=True,
              help="Render this session's hidden task rows too, marked ⊘")
@click.option("--only-hidden", is_flag=True,
              help="Render ONLY this session's hidden task rows")
def session_monitor(show_all, tree, show_hidden, only_hidden):
    """Live dashboard: repeatedly render `session status` until interrupted.

    The top-like pane you keep open all day. Loops the same view `session
    status` prints once, redrawing every 2 seconds and repainting only when the
    frame changes (no flicker). Ctrl-C exits. Accepts the same --all/--tree
    options as `session status`; --tree renders a single tree frame (the live
    loop drives the table view).

    Per-session task hiding applies here identically, footer included — the
    '… N hidden' line survives the redraw loop like any other part of the frame.
    """
    from endless.session_cmd import session_status_resolve
    session_status_resolve(show_all=show_all, tree=tree, monitor=True,
                           show_hidden=show_hidden, only_hidden=only_hidden)


@session_cmd.command("list")
@click.option("--project", default=None,
              help="List one named project's sessions (usable from anywhere)")
@click.option("--all-projects", is_flag=True,
              help="List every project's sessions (default: the current project)")
@click.option("--state", default=None,
              type=session_states.StateChoice(),
              help="Filter by state")
@click.option("--sort", "sort_by", default=None,
              type=click.Choice(["id", "project", "state", "count"]),
              help="Sort by column (default: state priority)")
@click.option("--all", "show_all", is_flag=True,
              help="Include hidden, empty, and never-claimed-a-task sessions")
@click.option("--hidden", "show_hidden", is_flag=True,
              help="Show only hidden sessions")
@click.option("--empty", "show_empty", is_flag=True,
              help="Include empty/short sessions (<=2 messages)")
@click.option("--json", "as_json", is_flag=True, help="JSON output")
@rowcap.limit_options
def session_list(project, all_projects, state, sort_by, show_all, show_hidden,
                 show_empty, limit, as_json, no_limit):
    """List recent sessions in the current project.

    One row per session: its id, a one-column state glyph (legend below the
    table), the task it is active on, its message count, and that task's title.
    Sessions that never claimed a task have nothing to put in those last two
    columns, so they are omitted until you pass --all.

    Scoped to the project enclosing cwd by default — sessions are machine-wide,
    and the usual question is what is happening HERE. --all-projects widens it
    (and adds a Project column); --project <name> picks another from anywhere.
    The two cannot be combined.

    --hidden/--all refer to hidden SESSIONS, and are unrelated to the per-session
    task hiding of `session hide --task`.
    """
    from endless.session_cmd import list_sessions
    list_sessions(project_name=project, all_projects=all_projects,
                  show_all=show_all,
                  show_hidden=show_hidden, show_empty=show_empty,
                  state_filter=state, sort_by=sort_by,
                  limit=limit, no_limit=no_limit, as_json=as_json)


@session_cmd.command("search")
@click.argument("query")
@click.option("--project", default=None, help="Filter by project")
@click.option("--json", "as_json", is_flag=True, help="JSON output")
@rowcap.limit_options
def session_search(query, project, limit, as_json, no_limit):
    """Search across all session messages."""
    from endless.session_cmd import search_sessions
    search_sessions(query, project_name=project, limit=limit, no_limit=no_limit,
                    as_json=as_json)


@session_cmd.command("use")
@click.argument("session_ref", required=False, default=None)
def session_use(session_ref):
    """Print shell-evaluable activation for a Claude session.

    Designed for `eval "$(endless session use)"`. Emits a minimal block —
    cd to the session's worktree (if its directory exists) or cwd, plus
    ENDLESS_SESSION_ID. Then runs .endless/extensions/use.sh (if present)
    and appends its stdout. With no arg, in tmux: auto-resolves to the
    sole sibling Claude pane in the current window.

    SESSION_REF accepts an endless integer id, a Claude UUID prefix, or an
    `e-NNNN` task-id — the last resolves the live session whose active task
    is NNNN (so `esu e-1655` switches to that task's session).

    Standard env vars exported:
      ENDLESS_SESSION_ID    endless integer id

    Other session fields (harness, project root, worktree path, etc.)
    are looked up on demand via 'endless session show $ENDLESS_SESSION_ID
    --json' so they're never stale.
    """
    from endless.session_cmd import session_use_resolve
    session_use_resolve(session_ref)


@session_cmd.command("forget")
def session_forget():
    """Print shell-evaluable lines that unset session-use env vars.

    Designed for `eval "$(endless session forget)"`. Inverse of
    'session use': emits one 'unset' line per env var that 'session use'
    is documented to export. Makes the current shell forget its session
    reference; the session itself is unaffected. Does not cd anywhere.

    See `endless shell-init` for the esf wrapper function.
    """
    from endless.session_cmd import session_forget_resolve
    session_forget_resolve()


@session_cmd.command("cd")
@click.argument("session_ref", required=False, default=None)
@click.option("--all", "show_all", is_flag=True,
              help="List all live Claude sessions in this project")
@click.option("--target", "target",
              type=click.Choice(["auto", "worktree", "project", "cwd"]),
              default="auto",
              help="Which path to print: auto (default; worktree else cwd), "
                   "worktree (errors if none), project (project root), cwd")
def session_cd(session_ref, show_all, target):
    """Print a path for a Claude session, for `cd $(...)` wrapping.

    With no session-ref, in tmux: auto-resolves to the sole sibling Claude
    pane in the current window. Use --all to list candidates. Provide an
    endless integer id or a Claude UUID prefix to disambiguate.

    See `endless shell-init` for esu/esp wrapper functions.
    """
    from endless.session_cmd import session_cd_resolve
    session_cd_resolve(session_ref, show_all=show_all, target=target)


# E-2106: one sentence, two verbs. `session resume` and `session goto --resume`
# divide by WINDOW — current pane vs. new window — and giving up on a transcript
# is a separate choice from where the replacement session opens, so the flag is
# on both and must read identically on both.
NEW_TRANSCRIPT_HELP = (
    "give up on the target's transcript and start a FRESH Claude session on "
    "the task instead: a plain `claude` in its worktree, no `--resume`. "
    "Required once the transcript file is gone."
)


@session_cmd.command("goto")
@click.argument("target_ref")
@click.option(
    "--resume", is_flag=True,
    help="If the target isn't live, resume it in a NEW tmux window and focus "
         "it, instead of erroring. No-op when the target is already live.",
)
@click.option(
    "--revisit", is_flag=True,
    help="With --resume, on a confirmed/assumed/completed target: flip the "
         "task to `revisit` and open the session to continue work.",
)
@click.option(
    "--no-revisit", "no_revisit", is_flag=True,
    help="With --resume, on a confirmed/assumed/completed target: open the "
         "session to read it back; leave the task's status alone.",
)
@click.option(
    "--new-transcript", "new_transcript", is_flag=True,
    help="With --resume, " + NEW_TRANSCRIPT_HELP,
)
def session_goto(target_ref, resume, revisit, no_revisit, new_transcript):
    """Switch tmux focus to a task's or session's pane, with a back-stack.

    <target_ref> is a task id (E-NNNN or NNNN) or a session id (ES-NNNN, a bare
    integer, or a Claude UUID prefix). A task id resolves to the
    most-recently-active live session working it. Prefer ES-NNNN — the form
    `task show` prints under Created:/Touched by: — since a bare integer that
    matches both a session and a task is rejected as ambiguous. The current pane
    is pushed onto a per-client back-stack so `endless session back` returns
    here. Requires tmux.

    With --resume, a target that has no live pane is relaunched in a new tmux
    window and focused (instead of erroring) — unlike `session resume`, which
    clobbers the current pane.

    Resuming settled work (confirmed/assumed/completed) requires saying which
    you mean: --revisit reopens the task and continues; --no-revisit reads the
    session back without touching its status.

    A target whose Claude transcript file is gone is refused rather than
    launched into a window that dies on the spot; --new-transcript is the route
    on from there.
    """
    from endless.session_cmd import session_goto as run_goto
    run_goto(target_ref, resume=resume, revisit=revisit,
             no_revisit=no_revisit, new_transcript=new_transcript)


@session_cmd.command("resume")
@click.argument("ref")
@click.option(
    "--review", is_flag=False, flag_value=".landed", default=None,
    metavar="[REF]",
    help="Recover a worktree dropped after landing as a DETACHED, read-mostly "
         "inspection tree (no status change). Bare, rebuilds at the latest "
         "landing (`.landed`); pass =<sha-or-branch> to override the base.",
)
@click.option(
    "--reopen", is_flag=False, flag_value=".landed", default=None,
    metavar="[REF]",
    help="Recover a dropped worktree on a WORKING BRANCH to continue work "
         "(reuses the task branch, else forks off the base). A done/rejected "
         "task flips to `revisit`. Bare uses `.landed`; =<ref> overrides.",
)
@click.option(
    "--dry-run", is_flag=True,
    help="Resolve (and create) everything the resume needs, then decline the "
         "`claude --resume` launch and print the resolved target as JSON.",
)
@click.option(
    # E-1918 renamed this to --dry-run once it worked on every path: it is no
    # longer printing a *recovery* decision, it is declining to act, which is
    # what --dry-run already means elsewhere in this CLI (`triage run`). The old
    # spelling keeps working for muscle memory and E-1801's verify script;
    # hidden so only one name is advertised.
    "--print-decision", is_flag=True, hidden=True,
)
@click.option(
    "--force", is_flag=True,
    help="Replace this pane even though the session in it is working a task. "
         "Required whenever that is the case — the exec destroys it.",
)
@click.option(
    "--new-transcript", "new_transcript", is_flag=True,
    help=NEW_TRANSCRIPT_HELP[0].upper() + NEW_TRANSCRIPT_HELP[1:],
)
def session_resume(ref, review, reopen, dry_run, print_decision, force,
                   new_transcript):
    """Relaunch a lost Claude session in the CURRENT tmux pane.

    REF is a task id (E-NNNN, as shown on the tmux tab), a session id
    (ES-NNNN, or a bare integer), or a Claude UUID prefix. A task id resolves
    to that task's most-recent resumable session; ES-NNNN names the session id
    space explicitly and never falls back to a task lookup. cd's to the task's
    worktree, then execs `claude --resume <uuid>` so the resumed session takes
    over this pane.

    Unlike `session goto`, this includes ended sessions — recovering the
    sessions orphaned when tmux crashes is exactly what it is for.

    A session that never claimed a task gets a container task and worktree
    created for it, so it has somewhere to be resumed into.

    When the worktree was dropped after landing, `--review`/`--reopen` rebuild
    it from the surviving transcript so no git ref need be typed.

    This replaces the pane it runs in. When that pane already holds a session
    working a task, it refuses without --force and points at
    `session goto <ref> --resume`, which opens a new window instead. It also
    refuses a window holding more than one pane, since it lays the window out
    around the pane it takes over.

    A target whose Claude transcript file is gone is refused before anything is
    launched — the file may still be recoverable from a backup, and that window
    closes quietly. --new-transcript is the deliberate give-up route.
    """
    from endless.session_cmd import resume_session
    resume_session(ref, review=review, reopen=reopen,
                   dry_run=dry_run or print_decision, force=force,
                   new_transcript=new_transcript)


@session_cmd.command("back")
def session_back():
    """Return to the previous session, browser-style (see `session goto`).

    Pops the per-client back-stack and switches tmux focus there, skipping
    panes/sessions that have since closed. With an empty stack, returns to the
    spawning session if this one was spawned. Requires tmux.
    """
    from endless.session_cmd import session_back as run_back
    run_back()


@session_cmd.command("id")
def session_id():
    """Print the current Endless session's integer id to stdout.

    Designed for shell substitution: `ENDLESS_SESSION_ID="$(endless session id)"`.
    Resolves via the same 3-layer logic as cli/hook event attribution:
    ENDLESS_SESSION_ID env var, TMUX_PANE companion match, or a single
    sibling Claude pane in the current tmux window. On ambiguity or no
    match, exits non-zero with a diagnostic on stderr (stdout stays empty).
    """
    from endless.session_cmd import session_id_resolve
    session_id_resolve()


# --task turns `session hide`/`session unhide` from a SESSION verb into a
# per-session TASK verb (E-1914). The flag is the discriminator and the no-flag
# path is untouched: bare `session hide <id>...` still hides whole sessions from
# `session list`, exactly as before. The two senses of "hidden" never mix — one
# suppresses a session from a list of sessions, the other suppresses a task row
# from one session's own status view.
_HIDE_TASK_OPTION = click.option(
    "--task", "task_refs", multiple=True, metavar="TASK-ID",
    help="Hide/unhide these tasks (E-NNN, repeatable) for ONE session's "
         "`session status` view instead of hiding whole sessions.",
)


@session_cmd.command("hide")
@click.argument("session_ids", nargs=-1)
@_HIDE_TASK_OPTION
def session_hide(session_ids, task_refs):
    """Hide sessions from `session list`, or tasks from one session's status.

    Without --task: hides each named SESSION from `session list` (unchanged).

    With --task: hides those TASKS from the status/monitor view of a single
    session — the one named as the optional positional argument, else the current
    one. Display-scoped: no other session's view moves, `task next` and blocking
    are unaffected, and the task itself is untouched. A hide never expires on its
    own; only `session unhide --task` reverses it. Re-hiding is a no-op.
    """
    if task_refs:
        if len(session_ids) > 1:
            raise click.ClickException(
                "--task hides tasks for ONE session; name at most one session "
                f"(got {len(session_ids)})."
            )
        from endless.session_cmd import hide_session_tasks
        hide_session_tasks(session_ids[0] if session_ids else None,
                           list(task_refs))
        return
    if not session_ids:
        raise click.ClickException(
            "Name at least one session to hide, or pass --task <id> to hide "
            "tasks from a session's status view."
        )
    from endless.session_cmd import hide_sessions
    hide_sessions(list(session_ids))


@session_cmd.command("unhide")
@click.argument("session_ids", nargs=-1)
@_HIDE_TASK_OPTION
def session_unhide(session_ids, task_refs):
    """Unhide sessions, or restore tasks hidden from one session's status.

    Mirrors `session hide`: without --task it unhides whole SESSIONS; with
    --task it restores those task rows to one session's status view.
    `session status --only-hidden` lists what is currently hidden, so you can
    find the ids without having recorded them. Unhiding twice is a no-op.
    """
    if task_refs:
        if len(session_ids) > 1:
            raise click.ClickException(
                "--task unhides tasks for ONE session; name at most one session "
                f"(got {len(session_ids)})."
            )
        from endless.session_cmd import hide_session_tasks
        hide_session_tasks(session_ids[0] if session_ids else None,
                           list(task_refs), unhide=True)
        return
    if not session_ids:
        raise click.ClickException(
            "Name at least one session to unhide, or pass --task <id> to "
            "restore tasks to a session's status view."
        )
    from endless.session_cmd import unhide_sessions
    unhide_sessions(list(session_ids))


@session_cmd.group("snapshot")
def session_snapshot_cmd():
    """Record and query session status snapshots.

    The verb is `snapshot` (renamed from `session status`) so that
    `session status` names the live work-state view. The recorded artifact is
    still a session-status snapshot; only the command word changed.
    """
    pass


@session_snapshot_cmd.command("add")
@click.argument("input_file", required=False,
                type=click.Path(exists=True, dir_okay=False))
@click.option("--session-id", "session_id_override", type=int, default=None,
              help="Use this Endless session id directly instead of "
                   "resolving the current session (test fixtures / "
                   "non-tmux callers).")
def session_snapshot_add(input_file, session_id_override):
    """Record a session status snapshot from XML on stdin or a file path.

    Reads XML matching the <session-status>/<task>/<decision>/<commit>/
    <entry> schema, validates strictly, and emits a
    session_status.recorded event. The Go-side handler dedups against
    the latest row for this session and renders markdown for chat.

    Example:

      \b
      endless session snapshot add <<'EOF'
      <session-status>
        <headline>E-101 v1 landed.</headline>
        <resolved>
          <task id="E-101" status="unverified">CLI + Go handler + tests</task>
        </resolved>
      </session-status>
      EOF
    """
    from endless.session_status_cmd import session_status_add as impl
    impl(input_file, session_id_override)


@session_cmd.group("task")
def session_task_cmd():
    """Correct which tasks this session's list holds.

    `session_tasks` capture is otherwise automatic: the event executors record
    a row for every task a session claims, files or edits, classified by how it
    entered scope (claimed / surfaced / revisited). These verbs cover the two
    cases automation cannot reach — work you have decided on but not yet
    touched, and a capture that should not have happened.

    Note the group is `session task <verb>` (it acts on a TASK within the
    session), not `session <verb>`, which acts on sessions themselves.
    """
    pass


_SESSION_ID_OPTION = click.option(
    "--session-id", "session_id_override", type=int, default=None,
    help="Use this Endless session id directly instead of resolving the "
         "current session (test fixtures / non-tmux callers).",
)


@session_task_cmd.command("add")
@click.argument("task_refs", nargs=-1, metavar="TASK-ID...")
@_SESSION_ID_OPTION
def session_task_add(task_refs, session_id_override):
    """Add tasks to this session as decided work (relation `queued`).

    For work you have committed to but have not touched yet — nothing has
    happened to those tasks, so no automatic capture would ever record them.
    They appear in `session status` immediately, ranked above incidental
    surfaced/revisited rows.

    Promotion is upgrade-only: queuing a task this session merely read or
    edited strengthens its relation, and queuing your own claimed task leaves
    it as `claimed` (reported, not an error). Adding the same task twice is a
    no-op.

    Example:

      \b
      endless session task add E-100 E-101
    """
    from endless.session_task_cmd import session_task_add as impl
    impl(task_refs, session_id_override)


@session_task_cmd.command("remove")
@click.argument("task_refs", nargs=-1, metavar="TASK-ID...")
@_SESSION_ID_OPTION
def session_task_remove(task_refs, session_id_override):
    """Drop tasks from this session's list entirely.

    For a capture that should not have happened. This DELETES the association
    — the touch, its relation and its `session order` position — so
    `task show`'s "Touched by:" stops reporting it, and any hide on the same
    pair is cleared with it.

    Not the same as `session hide --task`, which suppresses a row from this
    session's listing but KEEPS the association: hide is for a capture that is
    real but noisy, remove is for one that was simply wrong. There is no undo
    beyond touching the task again.

    Refused on this session's own claimed task: a claim cannot be dropped.
    Naming a task this session never touched is a reported no-op, not an error.

    Example:

      \b
      endless session task remove E-100
    """
    from endless.session_task_cmd import session_task_remove as impl
    impl(task_refs, session_id_override)


@session_cmd.command("order")
@click.argument("spec")
@click.option("--json", "as_json", is_flag=True,
              help="Parse SPEC as a JSON array-of-groups "
                   '(e.g. [["E-100"], ["E-101", "E-102"]]) '
                   "instead of the compact form.")
@click.option("--session-id", "session_id_override", type=int, default=None,
              help="Use this Endless session id directly instead of "
                   "resolving the current session (test fixtures / "
                   "non-tmux callers).")
def session_order(spec, as_json, session_id_override):
    """Set this session's task implementation order.

    SPEC is a compact sequence where whitespace advances the order and `|`
    groups tasks at the same order (parallelizable). Order is stored per
    session (session_tasks.do_order); equal values = safe to run concurrently.
    Replace-all: tasks this session has touched but omitted from SPEC are
    reset to unordered. Every id must already be a task this session touched.

    Example:

      \b
      endless session order "E-100 E-101|E-102 E-103"
      # E-100=1, E-101=2, E-102=2 (parallel with E-101), E-103=3
    """
    from endless.session_order_cmd import session_order as impl
    impl(spec, as_json, session_id_override)


@session_cmd.command("turn")
@click.argument("target", required=False, default=None)
@click.option("--session", "session_ref", default=None,
              help="Read this session (ES-NNN / id / UUID prefix) instead of "
                   "the sibling Claude pane in this tmux window.")
@click.option("-p", "--paged", is_flag=True, help="Page the output through less.")
def session_turn(target, session_ref, paged):
    """Print a session's RAW draft for a turn — what it wrote before minimizing.

    TARGET counts TURNS BACK, 0-based: no argument (or `0`) is the most recent
    turn, `3` is three turns back. There is no diff and no column layout — keep
    the minimized reply in the adjacent pane and compare by eye.

    TARGET may instead be `A` or `B`, which prints that option of the most
    recent paired minimization in full, rendered.

    By default this reads the sibling Claude pane in your tmux window, because
    the point is to review somebody else's reply. Pass --session to name one.

    Examples:

      \b
      endless session turn            # the raw draft behind the last reply
      endless session turn 3          # three turns back
      endless session turn B -p       # option B in full, paged
    """
    from endless.session_turn_cmd import session_turn as impl
    impl(target, session_ref, paged)


@main.group("minimizer")
def minimizer_cmd():
    """Inspect and control the instruction the minimizer edits replies with.

    `endless task report` shortens an agent's reply before you read it. To do
    that it calls a model and hands it two things: the agent's full draft, and
    an INSTRUCTION saying what to cut. That instruction lives in your database,
    not in a file, and Endless keeps trying to improve it.

    A background job runs every ten minutes. It scores shortenings that already
    happened. Less often it asks a model to write a NEW instruction, tries it
    against the one in use over drafts already stored, and switches to the new
    one if it did better. None of that needs you.

    \b
    Two words the output uses:
      variant   one instruction, stored with the settings that go with it
      champion  the variant currently in use
    \b

    Whether any of it runs is a per-project switch, `minimizer` in
    `.endless/config.json`. Its resolved value here is printed below.

    These commands are for seeing what the job decided, and for overruling it.
    """
    pass


# The description above is faithful whether or not the loop is switched on here,
# which is exactly the problem: a reader on a project that turned it off gets an
# accurate account of machinery that is not running, with nothing on the page
# saying so (E-2030). The setting block supplies that.
minimizer_cmd.governing_setting = help_settings.MINIMIZER


@minimizer_cmd.command("status")
def minimizer_status():
    """Show which instruction is in use, and how well it is doing.

    Reports the variant in use per task type, how often you are shown two
    versions of a reply to choose between, how often the scoring model predicted
    your reaction correctly, and how much text is being cut at each draft size.
    """
    from endless.minimizer_cmd import status
    status()


@minimizer_cmd.command("run")
@click.option("--limit", default=8, type=int, show_default=True,
              help="Maximum turns to judge this tick.")
def minimizer_run(limit):
    """One loop tick: judge what is unjudged, then replay a round if one is due.

    This is what the background job fires. Running it by hand is safe and
    idempotent — the judge writes one row per turn under a unique constraint,
    and the round claims its window through a persisted timestamp.
    """
    from endless.minimizer_cmd import run
    run(limit)


@minimizer_cmd.command("judge")
@click.option("--limit", default=8, type=int, show_default=True,
              help="Maximum turns to judge.")
def minimizer_judge_cmd(limit):
    """Score unjudged turns and close out predictions the user has answered."""
    from endless.minimizer_cmd import judge
    judge(limit)


@minimizer_cmd.command("optimize")
@click.option("--task-type", default=None,
              help="Task-type bucket to run the round for (default: untyped).")
def minimizer_optimize(task_type):
    """Run one paired-replay round now instead of waiting for the tick."""
    from endless.minimizer_cmd import optimize
    optimize(task_type)


@minimizer_cmd.command("variants")
@click.option("--task-type", default=None, help="Only this task-type bucket.")
@click.option("--limit", default=20, type=int, show_default=True)
def minimizer_variants(task_type, limit):
    """List prompt variants with their lineage; `*` marks the champion."""
    from endless.minimizer_cmd import variants
    variants(task_type, limit)


@minimizer_cmd.command("show")
@click.argument("variant_hash")
def minimizer_show(variant_hash):
    """Print one variant in full — prompt, fetch policy and bypass threshold."""
    from endless.minimizer_cmd import show
    show(variant_hash)


@minimizer_cmd.command("reseed")
@click.option("--task-type", default=None,
              help="Task-type bucket to reseed (default: untyped).")
def minimizer_reseed(task_type):
    """Start over from the prompt Endless ships, throwing away the tuned one.

    The minimizer does not read its prompt from a file. Whichever version is in
    use is stored in your database. Four things change it:

    \b
      - THE LOOP promoting a prompt it wrote itself, once that prompt has beaten
        the current one over a frozen set of your past replies. This is the
        usual way and it happens in the background; `endless minimizer status`
        lists the recent ones.
      - `endless minimizer rollback` — back to the one before.
      - this command — back to the one Endless ships.
      - a fresh install, which starts from the shipped one.
    \b

    Upgrading Endless is not on that list. A new shipped prompt is only what a
    brand-new install begins with; yours keeps running whatever your database
    says.

    That is what you want while the shipped prompt is merely being improved on.
    It is not what you want when the shipped prompt was BROKEN and has been
    fixed, because every prompt the loop wrote was derived from the broken one
    and carries the same fault. This command is how you take the fix.

    It is deliberately manual: it discards everything the loop learned, which is
    too much to lose on the strength of an upgrade. `endless minimizer status`
    tells you when your prompt differs from the shipped one.
    """
    from endless.minimizer_cmd import reseed
    reseed(task_type)


@minimizer_cmd.command("rollback")
@click.option("--task-type", default=None,
              help="Task-type bucket to roll back (default: untyped).")
def minimizer_rollback(task_type):
    """Point a champion back at its parent — how an auto-promotion is undone.

    Rollback is a pointer move, which is what makes promoting without asking you
    safe rather than reckless: nothing was rewritten, so nothing has to be
    reconstructed.
    """
    from endless.minimizer_cmd import rollback
    rollback(task_type)


@main.group("task")
def task_cmd():
    """Manage project tasks."""
    pass


@task_cmd.command("import")
@click.argument("file", default=None, required=False)
@click.option("--from-claude", is_flag=True,
              help="Import from ~/.claude/plans/")
@click.option("--json", "json_file", default=None,
              help="Import from JSON file")
@click.option("--project", default=None,
              help="Project name (default: detect from cwd)")
@click.option("--replace", is_flag=True,
              help="Replace items from same source file under same parent")
@click.option("--parent", type=TASK_ID, default=None,
              help="Parent goal ID to import under")
def task_import(file, from_claude, json_file, project, replace, parent):
    """Import a plan file into the DB."""
    if json_file:
        import json as json_mod
        from pathlib import Path
        from endless.task_cmd import import_json
        p = Path(json_file).expanduser()
        if not p.exists():
            raise click.ClickException(f"File not found: {p}")
        data = json_mod.loads(p.read_text())
        import_json(data, project_name=project, clear=replace)
    else:
        from endless.task_cmd import import_plan
        import_plan(
            file_path=file, from_claude=from_claude,
            project_name=project, replace=replace,
            parent_id=parent,
        )


@task_cmd.command("list")
@click.option("--project", default=None,
              help="Project name (default: detect from cwd)")
@click.option("--all", "show_all", is_flag=True,
              help="Include confirmed items")
@click.option("--status", default=None,
              type=MultiChoice(TASK_STATUSES),
              help="Filter by status (comma-separated, e.g. unplanned,ready)")
@click.option("--phase", default=None,
              type=click.Choice(["urgent", "now", "next", "later", "maybe"]),
              help="Filter by phase")
@click.option("--tier", default=None,
              help="Filter by tier (1-4 or auto/quick/deep/discuss)")
@click.option("--parent", "parent_id", default=None,
              help="Filter to children of this task (e.g. E-101), or 'none' for root tasks")
@click.option("--related-to", "--relates-to", "related_to_id", type=TASK_ID, default=None,
              help="Filter to tasks related to this task ID")
@click.option("--rel-type", "rel_type", default=None,
              help="Narrow --related-to by relation type (blocks, implements, informs, ...)")
@click.option("--sort", default=None,
              type=click.Choice(["id", "status", "phase", "tier", "created", "title"]),
              help="Sort by column (default: id)")
@click.option("--removed", "removed_only", is_flag=True,
              help="List REMOVED tasks instead of live ones")
@click.option("--llm", is_flag=True,
              help="Token-efficient output for LLMs")
@click.option("--json", "as_json", is_flag=True,
              help="JSON output")
@rowcap.limit_options
def task_list(project, show_all, status, phase, tier, parent_id, related_to_id, rel_type,
              sort, removed_only, llm, as_json, limit, no_limit):
    """List tasks for a project.

    Stops at --limit rows and says how many it left out; --no-limit renders
    every one. --json is uncapped unless you ask for a limit.
    """
    from endless.task_cmd import show_plan, parse_tier_filter, parse_parent_filter
    tier_val = parse_tier_filter(tier) if tier else None
    parent_val = parse_parent_filter(parent_id) if parent_id else None
    show_plan(project_name=project, show_all=show_all,
              status_filter=status, phase_filter=phase,
              tier_filter=tier_val, parent_id=parent_val,
              related_to_id=related_to_id, rel_type=rel_type,
              sort_by=sort, removed_only=removed_only, llm=llm, as_json=as_json,
              limit=limit, no_limit=no_limit)


@task_cmd.command("show")
@click.argument("item_ids", type=TASK_ID, nargs=-1, required=True)
@click.option("--no-description", is_flag=True,
              help="Hide description")
@click.option("--analysis", "show_analysis", is_flag=True,
              help="Show analysis field")
@click.option("--text", "show_text", is_flag=True,
              help="Show text field")
@click.option("--children", "show_children", is_flag=True,
              help="Show direct children")
@click.option("--outcome", "show_outcome", is_flag=True,
              help="Show the full outcome field (hidden by default; a "
                   "char-count placeholder shows otherwise)")
@click.option("--all-fields", "all_fields", is_flag=True,
              help="Show every content section (description, analysis, text, "
                   "outcome, children)")
@click.option("--llm", is_flag=True,
              help="Token-efficient output for LLMs")
@click.option("--json", "as_json", is_flag=True,
              help="JSON output")
@click.option("-p", "--paged", is_flag=True,
              help="Page colorized output through less (wheel-scrollable)")
@click.option("--no-color", is_flag=True,
              help="Disable ANSI color even on a TTY")
def task_show(item_ids, no_description, show_analysis, show_text,
              show_children, show_outcome, all_fields, llm, as_json,
              paged, no_color):
    """Show detail for one or more tasks."""
    from endless.task_cmd import detail_item
    if all_fields:
        show_analysis = show_text = show_children = show_outcome = True
    for item_id in item_ids:
        detail_item(item_id, show_description=not no_description,
                    show_analysis=show_analysis, show_text=show_text,
                    show_children=show_children, show_outcome=show_outcome,
                    llm=llm, as_json=as_json, paged=paged, no_color=no_color)


task_cmd.add_command(task_show, name="detail")


@task_cmd.group("next", invoke_without_command=True)
@click.option("--project", default=None,
              help="Project name (default: detect from cwd)")
@click.option("--all", "show_all", is_flag=True,
              help="Show tasks from all projects")
@click.option("--llm", is_flag=True,
              help="Token-efficient output for LLMs")
@click.option("--json", "as_json", is_flag=True,
              help="JSON output")
@click.option("--tier", default=None,
              help="Filter by tier (1-4 or auto/quick/deep/discuss)")
@click.option("--phase", default=None,
              type=click.Choice(["urgent", "now", "next", "later", "maybe"]),
              help="Filter by phase")
@click.option("--parent", "parent_id", default=None,
              help="Filter to children of this task (e.g. E-101), or 'none' for root tasks")
@rowcap.limit_options
@click.pass_context
def task_next(ctx, project, show_all, limit, llm, as_json, tier, phase, parent_id,
              no_limit):
    """Show top actionable tasks, ranked by priority."""
    # `next` is a group so it can host `revise` (and future `move`/`briefing`),
    # but bare `endless task next` keeps its heuristic-list behavior.
    if ctx.invoked_subcommand is not None:
        return
    from endless.task_cmd import next_tasks, parse_tier_filter, parse_parent_filter
    tier_val = parse_tier_filter(tier) if tier else None
    parent_val = parse_parent_filter(parent_id) if parent_id else None
    next_tasks(project_name=project, show_all=show_all,
               limit=limit, no_limit=no_limit, llm=llm, as_json=as_json,
               tier=tier_val, phase_filter=phase, parent_id=parent_val)


@task_next.command("revise")
@click.option("--file", "file_path", required=True,
              help="Path to a JSON file holding the full new curated list")
@click.option("--project", default=None,
              help="Project name (default: detect from cwd)")
@click.option("--json", "as_json", is_flag=True,
              help="Emit the resulting list as JSON")
def task_next_revise(file_path, project, as_json):
    """Replace the curated 'next' list from a JSON file (full rewrite)."""
    from endless.task_cmd import revise_next_list
    revise_next_list(file_path=file_path, project_name=project, as_json=as_json)


@task_cmd.command("active")
@click.option("--project", default=None,
              help="Project name (default: detect from cwd)")
@click.option("--all", "show_all", is_flag=True,
              help="Show tasks from all projects")
@click.option("--llm", is_flag=True,
              help="Token-efficient output for LLMs")
@click.option("--json", "as_json", is_flag=True,
              help="JSON output")
@click.option("--parent", "parent_id", default=None,
              help="Filter to children of this task (e.g. E-101), or 'none' for root tasks")
def task_active(project, show_all, llm, as_json, parent_id):
    """Show underway and unverified tasks."""
    from endless.task_cmd import active_tasks, parse_parent_filter
    parent_val = parse_parent_filter(parent_id) if parent_id else None
    active_tasks(project_name=project, show_all=show_all,
                 llm=llm, as_json=as_json, parent_id=parent_val)


@task_cmd.command("id")
@click.option("--pane", default=None,
              help="Resolve for this tmux pane instead of $TMUX_PANE.")
@click.pass_context
def task_id_cmd(ctx, pane):
    """Print the task this session is on — one bare `E-NNNN` line.

    Reads the same database binding the tmux status row shows, so it
    answers "which task am I on?" for a shell, a recipe, or an agent
    without anyone having to remember:

        endless task show "$(endless task id)"

    Exits 1 with a diagnostic on stderr — never on stdout — when the
    session holds no task, so `endless task id || ...` scripts cleanly.
    The binding is keyed by the tmux pane the session runs in; outside
    tmux there is nothing to resolve.

    `endless tmux task` is an alias for this command.
    """
    from endless.tmux_cmd import run_active_id
    run_active_id(pane, ctx.command_path)


@task_cmd.command("recent")
@click.option("--project", default=None,
              help="Project name (default: detect from cwd)")
@click.option("--all", "show_all", is_flag=True,
              help="Show tasks from all projects")
@click.option("--llm", is_flag=True,
              help="Token-efficient output for LLMs")
@click.option("--json", "as_json", is_flag=True,
              help="JSON output")
@click.option("--parent", "parent_id", default=None,
              help="Filter to children of this task (e.g. E-101), or 'none' for root tasks")
@rowcap.limit_options
def task_recent(project, show_all, limit, llm, as_json, parent_id, no_limit):
    """Show most recently updated tasks."""
    from endless.task_cmd import recent_tasks, parse_parent_filter
    parent_val = parse_parent_filter(parent_id) if parent_id else None
    recent_tasks(project_name=project, show_all=show_all,
                 limit=limit, no_limit=no_limit, llm=llm, as_json=as_json,
                 parent_id=parent_val)


@task_cmd.command("landed")
@click.argument("item_id", type=TASK_ID, required=False)
@click.option("--project", default=None,
              help="Project name (default: detect from cwd)")
@click.option("--all", "show_all", is_flag=True,
              help="Show tasks from all projects")
@click.option("--llm", is_flag=True,
              help="Token-efficient output for LLMs")
@click.option("--json", "as_json", is_flag=True,
              help="JSON output")
@rowcap.limit_options
def task_landed(item_id, project, show_all, limit, llm, as_json, no_limit):
    """List landed tasks, or show one task's landing history.

    Bare `task landed` lists tasks that have landed at least once, most
    recent first. `task landed <id>` shows that task's full landing history
    (every land's timestamp, branch, and merge SHA).
    """
    from endless.task_cmd import landed_list, landed_item
    if item_id is not None:
        landed_item(item_id, llm=llm, as_json=as_json)
    else:
        landed_list(project_name=project, show_all=show_all,
                    limit=limit, no_limit=no_limit, llm=llm, as_json=as_json)


@task_cmd.command("unsettled")
@click.argument("item_id", type=TASK_ID, required=False)
@click.option("--project", default=None,
              help="Project name (default: detect from cwd)")
@click.option("--all", "show_all", is_flag=True,
              help="Survey every task worktree in the project")
@click.option("--include-settled", is_flag=True,
              help="With --all, also list settled worktrees")
@click.option("--llm", is_flag=True,
              help="Token-efficient output for LLMs")
@click.option("--json", "as_json", is_flag=True,
              help="JSON output")
@rowcap.limit_options
def task_unsettled(item_id, project, show_all, include_settled, limit, llm, as_json,
                   no_limit):
    """Explain why a task's worktree is unsettled (modified vs unlanded).

    `task unsettled <id>` shows the full breakdown for one task — which files
    are uncommitted (and which of those are endless's own auto-managed files)
    and which commits have not reached the base branch. `task unsettled --all`
    surveys every task worktree in the project, one line each.

    A target is required: the survey walks every worktree on disk and is slow
    enough that it should be asked for, not stumbled into.

    This is the explanation behind the ◆ marker in `session status`: it reads the
    same probe, so the two can never disagree. Unsettled means modified
    (uncommitted changes) OR unlanded (commits whose content is not on the base
    branch) — the fix differs, which is why the marker alone is not enough.
    """
    if item_id is not None and show_all:
        raise click.UsageError("pass a task id or --all, not both.")
    if item_id is None and not show_all:
        raise click.UsageError(
            "specify a task id (endless task unsettled <id>) or --all to survey "
            "every worktree in the project.")

    from endless.task_cmd import unsettled_list, unsettled_item
    if item_id is not None:
        unsettled_item(item_id, llm=llm, as_json=as_json)
    else:
        unsettled_list(project_name=project, limit=limit, no_limit=no_limit,
                       include_settled=include_settled, llm=llm, as_json=as_json)


@task_cmd.command("search")
@click.argument("query")
@click.option("--project", default=None,
              help="Project name (default: detect from cwd)")
@click.option("--all", "show_all", is_flag=True,
              help="Include confirmed/assumed/declined items")
@click.option("--status", default=None,
              type=MultiChoice(TASK_STATUSES),
              help="Filter by status (comma-separated, e.g. unplanned,ready)")
@click.option("--phase", default=None,
              type=click.Choice(["urgent", "now", "next", "later", "maybe"]),
              help="Filter by phase")
@click.option("--parent", "parent_id", default=None,
              help="Filter to children of this task (e.g. E-101), or 'none' for root tasks")
@click.option("--text", "search_text", is_flag=True,
              help="Also search in text field")
@click.option("--llm", is_flag=True,
              help="Token-efficient output for LLMs")
@click.option("--json", "as_json", is_flag=True,
              help="JSON output")
@rowcap.limit_options
def task_search(query, project, show_all, status, phase, parent_id,
                search_text, limit, llm, as_json, no_limit):
    """Search tasks by query string.

    The count under the table is the number of MATCHES, not the number of rows
    rendered; when those differ a footer names the gap and --no-limit closes it.
    """
    from endless.task_cmd import search_tasks, parse_parent_filter
    parent_val = parse_parent_filter(parent_id) if parent_id else None
    search_tasks(query, project_name=project, show_all=show_all,
                 status_filter=status, phase_filter=phase,
                 parent_id=parent_val,
                 search_text=search_text,
                 limit=limit, no_limit=no_limit, llm=llm, as_json=as_json)


# ─── inline-content path gate (E-1744) ───────────────────────────────────────
# Inline flags (--text, --outcome, --description, --analysis) store their argument
# verbatim. Passing a file *path* silently stores the path and discards the intended
# content — the corruption that lost E-1626/E-1564. Every inline/file flag pair
# funnels through _resolve_content_flag, so the gate lives here and covers all
# fields and all tables (task/decision/epic add + update + confirm/assume/complete/
# replace). To load a file's content, use the paired --<name>-file flag.

# A single whitespace-free token ending in one of these is treated as a file path.
_PATH_EXT_RE = re.compile(r"\.[A-Za-z][A-Za-z0-9]{0,5}$")
# Prefixes that name a file on purpose. A token carrying one is a path even
# without an extension; a slash anywhere else in it is not enough (E-1794).
_REL_PREFIXES = ("./", "../", "~/")
# Surrounding punctuation trimmed off a candidate before the path test. Leading '.'
# and '~' are intentionally NOT stripped (they start real relative/home paths).
_LEAD_STRIP = "\"'`([{"
_TRAIL_STRIP = "\"'`.,;:!?)]}"


def _is_absolute_path(token):
    """True if *token* is a genuine absolute path: a leading slash, then a
    directory with something in it — or a single segment carrying a filename
    extension. Purely lexical, so a gone /tmp path still counts.

    A one-segment token with no extension is NOT a path. That is decided, not
    deferred. /whats-left, /loop and /code-review are slash-command names, and
    /tmp, /etc and /Users are directories being NAMED in prose rather than
    pointed at. Nothing this gate exists to catch is lost by letting them
    through: a mis-passed file always arrives with a directory (/tmp/plan.md)
    or an extension (/plan.md), never as a bare /tmp.

    Treating them as paths is what cost E-1794 two rounds — it refused a lesson
    for naming a slash command, and refused this gate's OWN sentence, "a /tmp
    path is lost when a worktree drops". Handing the ambiguity to a model would
    reinstate precisely that: asked whether /tmp is a path, a model says yes,
    and the sentence is unwritable again. The cheap answer is also the correct
    one, so there is no seam here to fill in later.

    Nothing after the slash is not a path either. '/' is punctuation in prose —
    a delimiter being named ("'/' splits family from variant"), a spaced
    alternative ("type / subtype") — and reporting it as an absolute path is
    what forced --allow-path onto ED-1537."""
    if not token.startswith("/"):
        return False
    segments = [seg for seg in token.split("/") if seg]
    if not segments:
        return False
    return len(segments) > 1 or bool(_PATH_EXT_RE.search(segments[0]))


def _is_path_shaped(token):
    """True if a single whitespace-free token names a file on purpose — the
    signature of a path mis-passed to an inline flag. Purely lexical; the file
    need not exist. Three signals, and only these three (E-1794):

      absolute              /tmp/plan.md, /opt/corp/spec.md
      explicitly relative   ./x.md, ../x.md, ~/x.txt
      a filename extension  plan.md, docs/guide/index.md

    A slash alone is not one of them. pytest/uv (a runner type/subtype),
    vnd.newclarity.foo and type/subtype are slash-separated notation, not
    filenames; passing one inline is never the mis-passed-file mistake this gate
    exists to catch, and blocking them cost E-1789 three --allow-path
    workarounds. A URL (contains '://') is never a mis-passed file path — and a
    Git file URL is the recommended way to reference another project — so it is
    not path-shaped."""
    if "://" in token:
        return False
    return (
        _is_absolute_path(token)
        or token.startswith(_REL_PREFIXES)
        or bool(_PATH_EXT_RE.search(token))
    )


def _absolute_path_tokens(content):
    """Yield tokens in *content* that are absolute filesystem paths (after
    ~-expansion). Detection is lexical, so a gone path still counts. Relative
    tokens mid-content are intentionally not yielded."""
    for raw in content.split():
        tok = raw.lstrip(_LEAD_STRIP).rstrip(_TRAIL_STRIP)
        if not tok:
            continue
        if _is_absolute_path(tok):
            yield tok
        elif tok.startswith("~/") and _is_absolute_path(os.path.expanduser(tok)):
            yield tok


# A source citation: a filename with a known extension, then :LINE, optionally
# followed by :COL or -ENDLINE. The path half forbids colons so a clock time or
# a host:port cannot be dragged into the match (E-1934).
_LINE_CITATION_RE = re.compile(
    r"^[^\s:]+?\.(?P<ext>[A-Za-z][A-Za-z0-9]{0,9}):\d+(?:[:-]\d+)?$"
)
# Token shapes that are addresses, not citations. Checked before the extension
# set rather than after, because the set cannot settle this on its own: rs, py,
# pl, sh, ml, cc, md and tf are all ccTLDs as well as source extensions.
_URL_MARKERS = ("://", "@")


def _line_citation_tokens(content, extensions):
    """Yield tokens in *content* that cite a file by line number.

    A bare :NNN is deliberately not matched. Nobody writes a leading-colon line
    number in prose, and the shape collides with clock times, host:port pairs
    and ratios — all of which are legitimate durable content.
    """
    for raw in content.split():
        tok = raw.lstrip(_LEAD_STRIP).rstrip(_TRAIL_STRIP)
        if not tok:
            continue
        if any(m in tok for m in _URL_MARKERS) or tok.lower().startswith("www."):
            continue
        m = _LINE_CITATION_RE.match(tok)
        if m and m.group("ext").lower() in extensions:
            yield tok


def _builtin_allowed_dirs():
    """endless's own config + cache dirs — ALWAYS exempt from the path gate (both
    rules), no --allow-path needed. Docs and plans legitimately reference stable
    endless-owned locations outside the project root (the main database, sandbox
    caches — e.g. ~/.config/endless/endless.db, ~/.cache/endless/sandboxes/…).

    Resolved through endless's own config/cache-dir resolution so XDG_CONFIG_HOME /
    XDG_CACHE_HOME are honored (a sandbox session's redirected endless dir is
    exempt too), UNIONed with the HOME-anchored main dirs so a literal
    ~/.config/endless reference stays exempt even inside an XDG-redirected sandbox
    session. Returned as a data-driven table (a list of resolved dirs), NOT an
    if/switch: it is the single built-in allow-set that project-config
    allowed_paths (a later task) will extend at the same composition point."""
    from endless import config
    dirs = {
        config._config_root() / "endless",   # honors XDG_CONFIG_HOME
        config.main_config_dir(),            # ~/.config/endless (main database)
        config._cache_root() / "endless",    # honors XDG_CACHE_HOME
        config.main_cache_dir(),             # ~/.cache/endless (main database)
    }
    return [d.expanduser() for d in dirs]


def _under_allowed_dir(path, dirs):
    """True if *path* (after ~-expansion) equals or lives under any dir in the
    table. Purely lexical — the path need not exist."""
    p = Path(os.path.expanduser(path))
    return any(p == d or d in p.parents for d in dirs)


# Fields whose value is short inline metadata, where "author it in a file and
# load it" can never be the remedy. A description is a single line capped at
# task_cmd.DESCRIPTION_MAX_LENGTH, so offering --description-file to a caller
# holding one line of prose just earns a second refusal ("Description must be a
# single line") — observed on E-2094 (E-1794). A table rather than an
# `if name == "description"`, so the next such field joins it in one place.
_SHORT_INLINE_FIELDS = ("description",)


def _gate_alternative(name):
    """The second remedy in the absolute-path refusal — what to do when you do
    NOT want to keep the path. Second on purpose: --allow-path leads, because an
    agent takes the first sanctioned option it is offered, and when that option
    was "rephrase the content" the gate got satisfied by distorting the very
    content it exists to protect (E-1794).

    It no longer offers --<name>-file as the way out. E-1934 moved the content
    rules onto resolved content, so a file is judged exactly as inline text is;
    naming it here would hand the reader a route that now refuses them, which is
    the failure this whole gate exists to stop happening quietly."""
    if name in _SHORT_INLINE_FIELDS:
        return (
            f"Otherwise put real content inline — a {name} is short metadata, "
            f"not a document."
        )
    return (
        "Otherwise write the path project-relative, or reference a "
        "cross-project file by Git URL."
    )


def _path_exempt(path, allow_paths):
    """True if *path* escapes the absolute-path checks. Single composition point
    for the effective allowed set: built-in endless config/cache dirs
    (always-on) + per-invocation --allow-path regexes."""
    if _under_allowed_dir(path, _builtin_allowed_dirs()):
        return True
    expanded = os.path.expanduser(path)
    return any(
        re.compile(p).search(path) or re.compile(p).search(expanded)
        for p in allow_paths
    )


def _content_gate_settings():
    """Project settings for the two content checks. Falls back to the built-in
    defaults outside a project, so the gate never silently switches itself off
    for want of a config file."""
    from endless import config
    root = config.enclosing_project_root()
    if root is None:
        return {"extensions": config.CITATION_EXTENSIONS,
                "gates": {"absolute_paths": True, "line_citations": True}}
    try:
        return config.project_content_config(root)
    except ValueError as e:
        raise click.ClickException(str(e))


def _guard_inline_content(inline, name, allow_paths):
    """Block a file path mis-passed to an inline flag: the whole value IS a path
    token, absolute or relative.

    INLINE ONLY, and correctly so — this is a flag-usage check, not a content
    rule. Passing a path to `--<name>-file` is that flag's whole purpose, so
    there is no mistake to catch on the file branch. The two CONTENT rules that
    used to live here moved to _guard_content_rules (E-1934), which runs on
    resolved content instead."""
    stripped = inline.strip()
    if stripped and len(stripped.split()) == 1 and _is_path_shaped(stripped):
        is_abs = _is_absolute_path(os.path.expanduser(stripped))
        if not (is_abs and _path_exempt(stripped, allow_paths)):
            raise click.ClickException(
                f"--{name} received a file path ({stripped!r}). --{name} stores its "
                f"argument verbatim as inline content; to load a file's content use "
                f"--{name}-file."
            )


def _guard_content_rules(content, name, allow_paths, whole_value_checked=False):
    """Refuse durable content that names an absolute path or cites a line number.

    Runs on RESOLVED content — inline OR file-loaded. That placement is the
    point (E-1934): these are rules about what durable content may SAY, and
    content does not become portable, or stop going stale, because it arrived in
    a file. E-2089 measured the cost of the old inline-only placement — a third
    of stored plans carry a file-and-line reference, and a plan is long-form, so
    it arrives by --<name>-file.

    `whole_value_checked` says the caller already ran the mis-passed-flag check,
    so a content that is ENTIRELY one path token has been judged and cleared
    there and must not be re-reported here with a worse message. Only the inline
    branch sets it: on the file branch there is no mis-pass to have cleared, so a
    file holding nothing but an absolute path is judged like any other."""
    settings = _content_gate_settings()
    gates = settings["gates"]

    if gates["absolute_paths"]:
        stripped = content.strip()
        whole_value_path = (
            whole_value_checked
            and stripped
            and len(stripped.split()) == 1
            and _is_path_shaped(stripped)
        )
        if not whole_value_path:
            for tok in _absolute_path_tokens(content):
                if _path_exempt(tok, allow_paths):
                    continue
                raise click.ClickException(
                    f"--{name} content contains an absolute path ({tok!r}). To keep this "
                    f"path, add --allow-path with a regex matching it. {_gate_alternative(name)} "
                    f"Absolute paths don't belong in durable ledger content — they're "
                    f"non-portable, and a /tmp path is lost when a worktree drops."
                )

    if gates["line_citations"]:
        for tok in _line_citation_tokens(content, settings["extensions"]):
            raise click.ClickException(
                f"--{name} content cites a line number ({tok!r}). There is no "
                f"--allow flag for this one, by decision.\n"
                f"  Line numbers go stale the moment anything else lands, so a "
                f"later session cannot tell whether to trust them.\n"
                f"  Name the function, command or symbol instead — or better, "
                f"state the search that finds the site."
            )


# ─── empty-file gate (E-2008) ────────────────────────────────────────────────
# `--<name>-file` writes whatever the file holds, so a path that is empty — or
# produced by an extraction that silently yielded nothing — replaced existing
# description/text/analysis/outcome content with nothing and reported success.
# Observed on E-1817: a sed round-trip produced a zero-byte file and
# `task update --analysis-file` wrote it over 3.5KB, recoverable only because
# the session still had the content in context.
#
# Zero bytes is never a legitimate value for these fields, so the file flags
# refuse an empty or whitespace-only file UNCONDITIONALLY — there is no --force.
# That is deliberate: --force is exactly the flag a mistaken caller appends
# after reading the refusal, which would restore the failure mode with an audit
# trail claiming it was intended. Clearing is instead an explicit, field-named
# act (`--clear <field>`, below) that a failed pipeline cannot reach by accident.

CLEARABLE_CONTENT_FIELDS = ("description", "text", "analysis", "outcome")


def _describe_empty_file(content):
    """Human-readable reason a loaded file counts as empty: distinguishes a
    truly zero-byte file from one holding only whitespace, since which one it is
    tells you which step of the pipeline failed."""
    if not content:
        return "0 bytes"
    return f"{len(content)} bytes, all whitespace"


def _refuse_empty_file(path, content, name, clearable):
    """Raise the empty-`--<name>-file` refusal. Names the offending path (the
    whole point — the caller has to know *which* file came back empty) and, when
    the calling command offers it, the `--clear <field>` recovery, so the way out
    is discoverable at the moment it is needed rather than buried in docs."""
    msg = (
        f"--{name}-file loaded no content from {path} ({_describe_empty_file(content)}).\n"
        f"  Refusing to blank {name}: an empty file is far more often a failed\n"
        f"  extraction than an intent to erase the field. Re-check the command\n"
        f"  that produced the file."
    )
    if clearable:
        msg += (
            f"\n  To erase {name} on purpose, say so: --clear {name}"
        )
    raise click.ClickException(msg)


def _resolve_content_flag(inline, file_path, name, allow_paths=(), clearable=False):
    """Resolve a paired `--<name>` (inline) / `--<name>-file` (path) option pair
    into content. Returns the content string, or None if neither was given.
    Raises if both were given, the file does not exist or is empty (E-2008), if
    an inline value is itself a mis-passed path (see _guard_inline_content), or
    if the resolved content breaks a durable-content rule (see
    _guard_content_rules). The content rules apply to BOTH branches: passing a
    path to `--<name>-file` is that flag's sanctioned use, but what the file
    HOLDS is judged exactly as inline content is.

    `clearable` says whether the calling command carries `--clear <field>`; it
    only shapes the empty-file refusal's recovery line. The `update` verbs set
    it; `add` and the status-transition verbs do not, because there is nothing
    to clear when a field is being written for the first time."""
    if inline is not None and file_path is not None:
        raise click.ClickException(
            f"Pass either --{name} or --{name}-file, not both."
        )
    if file_path is not None:
        p = Path(file_path).expanduser()
        if not p.exists():
            raise click.ClickException(f"File not found: {p}")
        content = p.read_text()
        if not content.strip():
            _refuse_empty_file(p, content, name, clearable)
        _guard_content_rules(content, name, allow_paths)
        return content
    if inline is not None:
        _guard_inline_content(inline, name, allow_paths)
        _guard_content_rules(inline, name, allow_paths, whole_value_checked=True)
    return inline


def _apply_clear_flags(clear_fields, resolved):
    """Fold `--clear <field>` (repeatable) into a map of field name → resolved
    content, as produced by _resolve_content_flag (None where neither the inline
    nor the file flag was passed).

    A cleared field becomes `""` — the same value the inline `--<name> ''` form
    already writes — so nothing downstream learns a new sentinel.

    Refuses `--clear <field>` alongside that field's own `--<field>` /
    `--<field>-file`: the two say opposite things about one column, and letting
    either win silently is the class of bug this gate exists to prevent. Repeating
    the same `--clear <field>` is harmless (the conflict test reads the original
    map, not the accumulating one)."""
    out = dict(resolved)
    for name in clear_fields:
        if resolved.get(name) is not None:
            raise click.ClickException(
                f"--clear {name} conflicts with --{name}/--{name}-file in the "
                f"same command.\n"
                f"  Two flags writing one field is exactly the ambiguity this guard "
                f"exists to remove; pass one or the other."
            )
        out[name] = ""
    return out


@task_cmd.command("add")
@click.argument("title")
@click.option("--description", default=None,
              help="Longer description of the task (inline)")
@click.option("--description-file", default=None,
              help="Load the task description from a file")
@click.option("--text", default=None,
              help="Full task text / plan content (inline)")
@click.option("--text-file", default=None,
              help="Load the full task text / plan from a file")
@click.option("--analysis", "analysis_text", default=None,
              help="Analysis content (inline)")
@click.option("--analysis-file", default=None,
              help="Load the analysis content from a file")
@click.option("--phase", default="now",
              type=click.Choice(["urgent", "now", "next", "later", "maybe"]),
              help="Phase: urgent, now, next, later, maybe (default: now)")
@click.option("--project", default=None,
              help="Project name (default: detect from cwd)")
@click.option("--parent", type=TASK_ID, default=None,
              help="Parent task ID to add under")
@click.option("--after", type=TASK_ID, default=None,
              help="Insert after this task ID")
@click.option("--type", "task_type", default=None,
              type=click.Choice(["todo", "bugfix", "research", "epic", "brainstorm"]),
              help="Task type (default: todo)")
@click.option("--status", default=None,
              type=click.Choice(TASK_STATUSES),
              help="Initial status (default: untriaged; --tier 1 defaults to ready)")
@click.option("--tier", default=None,
              help="Tier (1-4 or auto/quick/deep/discuss)")
@click.option("--force", is_flag=True,
              help="Bypass title validation")
@click.option("--justification", default=None,
              help="Justification text for --type research (stored under '## Justification' in notes). "
                   "Required for --type research unless --parent is an underway epic.")
@click.option("--blocks", "blocks_ids", type=TASK_ID, multiple=True,
              help="Task ID(s) this new task blocks (repeatable)")
@click.option("--blocked-by", "blocked_by_ids", type=TASK_ID, multiple=True,
              help="Task ID(s) that block this new task (repeatable)")
@click.option("--relates-to", "relates_to_ids", type=TASK_ID, multiple=True,
              help="Task ID(s) related to this new task (repeatable)")
@click.option("--implements", "implements_ids", type=TASK_ID, multiple=True,
              help="Task ID(s) that this new task implements (repeatable)")
@click.option("--cleans-up", "cleans_up_ids", type=TASK_ID, multiple=True,
              help="Task ID(s) that this new task cleans up after (repeatable)")
@click.option("--cleaned-up-by", "cleaned_up_by_ids", type=TASK_ID, multiple=True,
              help="Task ID(s) that clean up after this new task (repeatable)")
@click.option("--duplicates", "duplicates_ids", type=TASK_ID, multiple=True,
              help="Task ID(s) this new task duplicates — same concern, already "
                   "filed (repeatable)")
@click.option("--replaces", "replaces_ids", type=TASK_ID, multiple=True,
              help="Task ID(s) this new task supersedes (repeatable). Records the "
                   "relation only; use `task replace <old> --by <new>` to also "
                   "close the replaced task.")
@click.option("--allow-path", "allow_paths", multiple=True,
              help="Regex matching an absolute path to permit in inline content "
                   "(repeatable; escape hatch for the path gate).")
def task_add(title, description, description_file, text, text_file, analysis_text, analysis_file, phase, project, parent, after, task_type, status, tier, force,
             justification,
             blocks_ids, blocked_by_ids, relates_to_ids, implements_ids,
             cleans_up_ids, cleaned_up_by_ids, duplicates_ids, replaces_ids,
             allow_paths):
    """Add a task."""
    from endless.task_cmd import add_item, parse_tier, link_tasks, print_add_hints
    description = _resolve_content_flag(description, description_file, "description", allow_paths)
    text = _resolve_content_flag(text, text_file, "text", allow_paths)
    analysis_text = _resolve_content_flag(analysis_text, analysis_file, "analysis", allow_paths)
    tier_val = parse_tier(tier) if tier else None
    new_id = add_item(title, description=description, text=text, analysis=analysis_text,
                      phase=phase, project_name=project, after=after, parent_id=parent,
                      task_type=task_type, status=status, tier=tier_val, force=force,
                      justification=justification)
    if new_id is None:
        return
    for tid in blocks_ids:
        link_tasks(new_id, tid, "blocks")
    for tid in blocked_by_ids:
        link_tasks(new_id, tid, "blocked_by")
    for tid in relates_to_ids:
        link_tasks(new_id, tid, "relates_to")
    for tid in implements_ids:
        link_tasks(new_id, tid, "implements")
    for tid in cleans_up_ids:
        link_tasks(new_id, tid, "cleans_up")
    for tid in cleaned_up_by_ids:
        link_tasks(new_id, tid, "cleaned_up_by")
    for tid in duplicates_ids:
        link_tasks(new_id, tid, "duplicates")
    for tid in replaces_ids:
        link_tasks(new_id, tid, "replaces")
    # E-1889: file-time hints, after the row and its relations exist. Advisory
    # only — never blocks the add, never raises.
    print_add_hints(new_id, cleans_up_ids)


@task_cmd.command("update")
@click.argument("item_ids", type=TASK_ID, nargs=-1, required=True)
@click.option("--status", default=None, help=TASK_STATUS_HELP)
@click.option("--title", default=None,
              help="New title")
@click.option("--description", default=None,
              help="New description (inline)")
@click.option("--description-file", default=None,
              help="Load the new description from a file")
@click.option("--text", default=None,
              help="Full task text / plan content (inline)")
@click.option("--text-file", default=None,
              help="Load the full task text / plan from a file")
@click.option("--parent", type=TASK_ID, default=None,
              help="Set parent task ID (0 to make root)")
@click.option("--phase", default=None,
              type=click.Choice(["urgent", "now", "next", "later", "maybe"]),
              help="Phase: urgent, now, next, later, maybe")
@click.option("--tier", default=None,
              help="Tier (0=n/a, 1-4 or auto/quick/deep/discuss, none=clear)")
@click.option("--type", "task_type", default=None,
              type=click.Choice(["todo", "bugfix", "research", "epic", "brainstorm"]),
              help="Task type")
@click.option("--analysis", "analysis_text", default=None,
              help="Analysis content (inline)")
@click.option("--analysis-file", default=None,
              help="Load the analysis content from a file")
@click.option("--force", is_flag=True,
              help="Bypass title validation")
@click.option("--outcome", default=None,
              help="Outcome / reason for status (inline; required if status=declined)")
@click.option("--outcome-file", default=None,
              help="Load the outcome from a file")
@click.option("--justification", default=None,
              help="Justification text when setting --type research (stored under '## Justification' in notes). "
                   "Required unless the effective parent is an underway epic.")
@click.option("--allow-path", "allow_paths", multiple=True,
              help="Regex matching an absolute path to permit in inline content "
                   "(repeatable; escape hatch for the path gate).")
@click.option("--keep-status", is_flag=True,
              help="Hold the current status: no auto-transition fires for this edit "
                   "(plan-attach promotion, description-edit reset, tier-1 "
                   "advance). For a typo- or formatting-only edit. Cannot be "
                   "combined with --status.")
@click.option("--clear", "clear_fields", multiple=True,
              type=click.Choice(CLEARABLE_CONTENT_FIELDS),
              help="Erase a content field, naming it (repeatable). --<field>-file "
                   "refuses an empty file, so this is the deliberate way to empty "
                   "description/text/analysis/outcome. Conflicts with the same "
                   "field's --<field>/--<field>-file.")
@click.option("--duplicates", "duplicates_ids", type=TASK_ID, multiple=True,
              help="Task ID(s) each named task duplicates — same concern, filed "
                   "twice (repeatable)")
@click.option("--replaces", "replaces_ids", type=TASK_ID, multiple=True,
              help="Task ID(s) each named task supersedes (repeatable). Records "
                   "the relation only; use `task replace <old> --by <new>` to also "
                   "close the replaced task.")
def task_update(item_ids, status, title, description, description_file, text, text_file, parent, phase, tier,
                task_type, analysis_text, analysis_file, force, outcome, outcome_file, justification, allow_paths,
                keep_status, clear_fields, duplicates_ids, replaces_ids):
    """Update fields on one or more tasks."""
    from endless.task_cmd import update_plan, parse_tier, link_tasks
    resolved = _apply_clear_flags(clear_fields, {
        "description": _resolve_content_flag(description, description_file, "description", allow_paths, clearable=True),
        "text": _resolve_content_flag(text, text_file, "text", allow_paths, clearable=True),
        "analysis": _resolve_content_flag(analysis_text, analysis_file, "analysis", allow_paths, clearable=True),
        "outcome": _resolve_content_flag(outcome, outcome_file, "outcome", allow_paths, clearable=True),
    })
    description = resolved["description"]
    text = resolved["text"]
    analysis_text = resolved["analysis"]
    outcome = resolved["outcome"]
    tier_val = parse_tier(tier) if tier else None
    # E-1185: relation flags are a change on their own. `update_plan` refuses an
    # edit that names no field ("Nothing to update"), which is still right when
    # nothing at all was passed — so it is skipped, not weakened, when the only
    # flags given are relations.
    edits_a_field = any(v is not None for v in (
        status, title, description, text, parent, phase, tier, task_type,
        analysis_text, outcome, justification,
    ))
    relations_only = not edits_a_field and (duplicates_ids or replaces_ids)
    for item_id in item_ids:
        if not relations_only:
            update_plan(item_id, status=status, title=title,
                        description=description, text=text,
                        parent_id=parent,
                        phase=phase, tier=tier_val, task_type=task_type,
                        analysis=analysis_text,
                        outcome=outcome, force=force,
                        justification=justification, keep_status=keep_status)
        for tid in duplicates_ids:
            link_tasks(item_id, tid, "duplicates")
        for tid in replaces_ids:
            link_tasks(item_id, tid, "replaces")


@task_cmd.command("remove")
@click.argument("item_ids", type=TASK_ID, nargs=-1, required=True)
@click.option("--cascade", is_flag=True,
              help="Also remove all descendants")
def task_remove(item_ids, cascade):
    """Remove one or more tasks."""
    from endless.task_cmd import remove_item
    for item_id in item_ids:
        remove_item(item_id, cascade=cascade)


@task_cmd.group("clear")
def task_clear():
    """Clear a field on a task."""
    pass


@task_clear.command("tier")
@click.argument("item_ids", type=TASK_ID, nargs=-1, required=True)
def task_clear_tier(item_ids):
    """Clear tier on one or more tasks (set to NULL/untriaged)."""
    from endless.task_cmd import update_plan, TIER_CLEAR
    for item_id in item_ids:
        update_plan(item_id, tier=TIER_CLEAR)


@task_cmd.command("verify")
@click.argument("item_id", type=TASK_ID, required=False)
@click.option("--keep", is_flag=True,
              help="Keep the per-run temp dir (isolated HOME/XDG + "
                   "intermediates) for debugging instead of removing it.")
def task_verify(item_id, keep):
    """Run a task's Tier-0 verification suite.

    Discovers the task's .endless/tasks/<id>/verify.toml, runs its checks
    under a fresh temp working dir and isolated env (temp HOME and
    XDG_CONFIG_HOME, so the suite cannot touch your real home/config),
    normalizes the results to a CTRF report, prints a pass/fail summary, and
    exits 0 on all-pass / non-zero otherwise. With no id, verifies the current
    session's active task. Run it before `task confirm`/`task assume` to prove
    the task's acceptance criteria.
    """
    from endless.verify_cmd import run_verify
    run_verify(item_id, keep)


@task_cmd.command("confirm")
@click.argument("item_ids", type=TASK_ID, nargs=-1, required=True)
@click.option("--cascade", is_flag=True,
              help="Also confirm all descendants")
@click.option("--outcome", default=None,
              help="Outcome — what was confirmed (inline; applies to root only on cascade)")
@click.option("--outcome-file", default=None,
              help="Load the outcome from a file")
@click.option("--allow-path", "allow_paths", multiple=True,
              help="Regex matching an absolute path to permit in inline content "
                   "(repeatable; escape hatch for the path gate).")
def task_complete(item_ids, cascade, outcome, outcome_file, allow_paths):
    """Confirm one or more tasks."""
    from endless.task_cmd import complete_item
    outcome = _resolve_content_flag(outcome, outcome_file, "outcome", allow_paths)
    for item_id in item_ids:
        complete_item(item_id, cascade=cascade, outcome=outcome)


@task_cmd.command("assume")
@click.argument("item_ids", type=TASK_ID, nargs=-1, required=True)
@click.option("--cascade", is_flag=True,
              help="Also assume all descendants")
@click.option("--outcome", default=None,
              help="Outcome — what was assumed (inline; applies to root only on cascade)")
@click.option("--outcome-file", default=None,
              help="Load the outcome from a file")
@click.option("--allow-path", "allow_paths", multiple=True,
              help="Regex matching an absolute path to permit in inline content "
                   "(repeatable; escape hatch for the path gate).")
def task_assume(item_ids, cascade, outcome, outcome_file, allow_paths):
    """Assume one or more tasks (believed complete, not yet verified)."""
    from endless.task_cmd import assume_item
    outcome = _resolve_content_flag(outcome, outcome_file, "outcome", allow_paths)
    for item_id in item_ids:
        assume_item(item_id, cascade=cascade, outcome=outcome)


@task_cmd.command("report")
@click.argument("item_id", type=TASK_ID, required=False)
@click.option("--draft-file", "draft_file", default=None,
              help="Path to your ENTIRE draft reply, as plain markdown.")
@click.option("--raw", "raw", is_flag=True, default=False,
              help="Print this session's most recent raw draft, unchanged.")
def task_report(item_id, draft_file, raw):
    """Minimize your draft reply into the message you are allowed to send.

    Write the reply you mean to send — exactly as you would send it, tables and
    code blocks and all — to a file, then run this command with --draft-file. An
    adversarial editor deletes what the user did not ask for, and its output is
    your entire final message. Send it verbatim. A Stop hook compares your final
    message against it and also blocks a turn that never ran this command at all.

    It is not a length limit: the objective is to delete what was not asked for,
    so a discussion the user asked for survives at whatever length it takes.

    Your draft is persisted. --raw prints it back unchanged, which is what makes
    an over-aggressive cut recoverable rather than lost.

    The task id is optional — pass it to attribute the report, omit it when you
    have nothing claimed. It does not change the task's status.

    You get ONE appeal per turn: re-run with a draft that argues for content the
    minimizer cut. The appeal is minimized too.

    Whether a Stop hook enforces any of this is the same per-project switch the
    loop uses, `minimizer` in `.endless/config.json`. Its resolved value here is
    printed below.
    """
    # The report channel is an always-main operation, for the same reason the
    # Stop hook that reads it is: `endless-go hook` calls PinMainDB
    # unconditionally, so a checkpoint written anywhere else is one the gate can
    # never see. Without this pin the two halves land on different databases
    # inside a self-dev worktree — the command arms the sandbox, the gate looks
    # in the main database, finds nothing, and blocks the turn as "never
    # reported". Worse, the E-1429 gate would refuse the bare invocation that
    # the SessionStart rule itself prints, so the instruction would be
    # unrunnable in the one repo that develops it.
    #
    # An explicit --db still wins: DBAwareGroup sets RESOLVED_CONFIG_DIR before
    # this body runs, making the pin a no-op. `--db sandbox` is how the E-1975
    # verify script drives the whole channel against a throwaway DB, which is
    # the only case that legitimately wants somewhere other than main.
    from endless import config
    config.default_db_to_main()
    from endless.report_cmd import report_item, show_raw
    if raw:
        if draft_file is not None:
            raise click.ClickException("Pass either --raw or --draft-file, not both.")
        show_raw()
        return
    if draft_file is None:
        raise click.ClickException(
            "--draft-file is required.\n"
            "\n"
            "  Write the reply you were about to send to a file and pass its path.\n"
            "  Pass the whole thing — the minimizer decides what survives."
        )
    report_item(item_id, draft_file)


# Same reason as `minimizer`: the reader typed this command, so it is described
# in full — but where the channel is off, "a Stop hook compares your final
# message against it" is not true, and only the setting block says so.
task_report.governing_setting = help_settings.MINIMIZER


@task_cmd.command("decline")
@click.argument("item_ids", type=TASK_ID, nargs=-1, required=True)
@click.option("--reason", required=True,
              help="Why this task is being declined (stored as outcome)")
def task_decline(item_ids, reason):
    """Decline one or more tasks (sets status=declined; reason required)."""
    from endless.task_cmd import decline_item
    for item_id in item_ids:
        decline_item(item_id, reason=reason)


@task_cmd.command("submit")
@click.argument("item_ids", type=TASK_ID, nargs=-1, required=True)
def task_submit(item_ids):
    """Submit one or more tasks (untriaged/unplanned/revisit → submitted).

    Agent-set signal that a task is spec-complete and awaiting human
    approval — either a plan was attached or the description is a sufficient
    spec. A human then runs `endless task approve` to reach `ready`.
    """
    from endless.task_cmd import submit_item
    for item_id in item_ids:
        submit_item(item_id)


@task_cmd.command("approve")
@click.argument("item_ids", type=TASK_ID, nargs=-1, required=True)
def task_approve(item_ids):
    """Approve one or more submitted tasks (submitted → ready).

    The human approval gate: `ready` provably means human-approved. Refused
    for background sessions.
    """
    from endless.task_cmd import approve_item
    for item_id in item_ids:
        approve_item(item_id)


@task_cmd.command("complete")
@click.argument("item_ids", type=TASK_ID, nargs=-1, required=True)
@click.option("--outcome", default=None,
              help="Findings / deliverable text, inline (required unless --outcome-file — IS the deliverable)")
@click.option("--outcome-file", default=None,
              help="Load the findings / deliverable from a file")
@click.option("--allow-path", "allow_paths", multiple=True,
              help="Regex matching an absolute path to permit in inline content "
                   "(repeatable; escape hatch for the path gate).")
def task_complete_cmd(item_ids, outcome, outcome_file, allow_paths):
    """Mark one or more tasks as `completed`.

    For findings-as-deliverable tasks (audits, research, reviews, etc.)
    whose deliverable is the outcome text itself, not behavior.
    Implementation tasks finish as `confirmed` or `assumed` instead
    (see `task confirm` / `task assume`).
    """
    from endless.task_cmd import mark_completed_item
    outcome = _resolve_content_flag(outcome, outcome_file, "outcome", allow_paths)
    if outcome is None:
        raise click.ClickException("Provide --outcome or --outcome-file.")
    for item_id in item_ids:
        mark_completed_item(item_id, outcome=outcome)


@task_cmd.command("claim")
@click.argument("item_id", type=TASK_ID)
@click.option("--unattended", is_flag=True,
              help="Claim with no Claude session bound — manual work at a "
                   "terminal, a plain shell, cron. Distinct from the global "
                   "--no-session, which governs event attribution, not "
                   "binding.")
# E-2093: `--force` spelled two unrelated decisions and documented only one.
# Both halves are now named — `--unattended` above for the session half, an
# explicit status transition for the settled half — so the flag is on its way
# out. Hidden and warning for one release (it still does what it did), then
# deleted. It must NOT survive as an alias for either half: an alias that
# still spells two decisions is the defect.
@click.option("--force", is_flag=True, hidden=True)
def task_claim(item_id, unattended, force):
    """Claim ownership of a task for this session."""
    from endless.task_cmd import claim_item
    claim_item(item_id, unattended=unattended, force=force)


@task_cmd.command("release")
@click.argument("item_id", type=TASK_ID, required=False, default=None)
@click.option("--ignore-missing", is_flag=True,
              help="When releasing a specific task ID, succeed with an info "
                   "message instead of erroring if no session has it claimed.")
def task_release(item_id, ignore_missing):
    """Release a session's claim on a task (defaults to current session's active task)."""
    from endless.task_cmd import release_item
    release_item(item_id, ignore_missing=ignore_missing)


@task_cmd.command("continue")
def task_continue():
    """Clear a pending epic-revisit prompt and carry on under the current plan.

    The only way out of the gate the hook opens when an ancestor epic goes to
    `revisit`: until the gate is cleared, every tool call in this session is
    blocked. To pause instead, run nothing — leaving the gate open IS pausing,
    and it clears itself once the epic leaves `revisit`. There is no `task
    pause`: its only distinguishing act would be an unbind, which the
    one-session-one-task invariant forbids.
    """
    from endless.task_cmd import continue_item
    continue_item()


@task_cmd.command("bind")
@click.argument("item_id", type=TASK_ID)
def task_bind(item_id):
    """Record this session as a task's owner, without changing its status.

    Bind sets `sessions.task_id`, which is the ownership record — write-once,
    and the only route back to this session's transcript. It is not a display
    field, though the tmux status row does read it.

    Unlike `claim` it changes nothing else: not the task's status, not a
    worktree, and not the session's own state (binding to an idle or
    waiting-on-you session leaves it as it was). Use it when the task's status
    should not move — typically one already `assumed` / `confirmed` /
    `unverified`. To resume WORKING such a task, reopen it and claim:
    `task update <id> --status revisit`, then `task claim <id>`.
    """
    from endless.task_cmd import bind_item
    bind_item(item_id)


@task_cmd.command("start", hidden=True)
@click.argument("item_id", type=TASK_ID, required=False, default=None)
def task_start_deprecated(item_id):
    """Deprecated stub for `task claim`, the verb that replaced it.

    Refuses to execute — prints the rename note and exits non-zero so the
    caller (agent or human) switches to the new verb instead of being
    silently enabled to keep using the old one.
    """
    raise click.ClickException(
        "`task start` was renamed to `task claim`.\n"
        "Run: endless task claim "
        + (f"E-{item_id}" if item_id is not None else "<id>")
    )


@task_cmd.command("move")
@click.argument("item_id", type=TASK_ID, required=False, default=None)
@click.option("--parent", type=TASK_ID, default=None,
              help="Target parent task ID to move under")
@click.option("--root", is_flag=True,
              help="Move to root (no parent)")
@click.option("--with-children", is_flag=True,
              help="Move the task and all its descendants")
@click.option("--children-of", type=TASK_ID, default=None,
              help="Move children of this task instead")
@click.option("--project", default=None,
              help="Project name (default: detect from cwd)")
def task_move(item_id, parent, root, with_children, children_of, project):
    """Move a task to a new parent or to root."""
    from endless.task_cmd import move_task
    move_task(item_id=item_id, parent=parent, root=root,
              with_children=with_children, children_of=children_of,
              project_name=project)


@task_cmd.command("handoff")
@click.argument("item_id", type=TASK_ID)
def task_handoff(item_id):
    """Render the spawn handoff for a task (the text spawn pastes)."""
    from endless.task_cmd import show_handoff
    show_handoff(item_id)


@task_cmd.command("spawn")
@click.argument("item_id", type=TASK_ID)
@click.option("--project", default=None,
              help="Project name (default: detect from cwd)")
@click.option("--permission-mode", "permission_mode", default="auto",
              help="claude --permission-mode for the spawned session "
                   "(default: auto). Foreground spawns only.")
@click.option("--model", default=None,
              help="claude --model for the spawned session (optional). "
                   "Foreground spawns only.")
@click.option("--name", "session_name", default=None,
              help="claude --name for the spawned session (optional). "
                   "Foreground spawns only.")
@click.option("--worktree", default=None,
              help="cd to this path (e.g. a git worktree) before launching "
                   "claude, instead of the spawn-created task worktree. The "
                   "spawned session reads .claude/settings.json from this "
                   "directory, so a worktree-local hook override (see "
                   "'just claude-settings-init') applies.")
# E-2093: deprecated alongside `claim --force`, which it mirrored. Two verbs
# disagreeing about what one flag meant is the condition that made it reachable
# as a catch-all. Hidden and warning for one release, then deleted; the
# settled-status refusal now names the reopen route instead.
@click.option("--force", is_flag=True, hidden=True)
# E-1968 retired --reopen. Its only capability the navigation verbs lacked was
# changing task status, and `session goto --resume --revisit` now provides that
# on the verb that already resumes the session which did the work — a strictly
# better outcome than spawning a fresh session with a rendered summary of it.
# Kept hidden and refusing (the `task start` precedent) so muscle memory gets an
# answer rather than "no such option". --new-session and --print-decision were
# --reopen-only modifiers; they stay accepted-and-hidden purely so a combined
# invocation reaches the --reopen message instead of a click parse error.
@click.option("--reopen", is_flag=True, hidden=True)
@click.option("--print-decision", is_flag=True, hidden=True)
# --bg dispatched the agent headless via `claude --bg`, and --attach opened a
# tmux window onto one. E-2074 removed background agents outright; both flags
# stay accepted-and-hidden, refusing with an explanation, for the same reason
# --reopen does above — muscle memory deserves an answer, not a parse error.
@click.option("--bg", is_flag=True, hidden=True)
@click.option("--attach", is_flag=True, hidden=True)
@click.option("--new-session", is_flag=True, hidden=True)
def task_spawn(item_id, project, permission_mode, model, session_name,
               worktree, force, reopen, print_decision, bg, attach,
               new_session):
    """Spawn Claude working on a task in a new tmux window.

    Spawns launch Claude as the tmux window's command and deliver the
    handoff (generated from the template — no stored prompt) as claude's
    positional prompt argument. Spawned sessions default to --permission-mode
    auto.

    Spawn always starts a NEW session. To pick up work a prior session already
    did, resume that session instead: `endless session goto <ref> --resume`.
    """
    if reopen or new_session or print_decision:
        raise click.ClickException(
            "`task spawn --reopen` is retired. Spawning a fresh "
            "session on reopened work threw away the session that did it.\n"
            "Reopen and continue in that session instead:\n"
            f"    endless session goto E-{item_id} --resume --revisit\n"
            "  (--no-revisit instead, to read it back without reopening the "
            "task.)\n"
            "--new-session and --print-decision went with it; they only ever "
            "modified --reopen."
        )
    if bg or attach:
        raise click.ClickException(
            "`task spawn --bg` and `--attach` are retired: Endless no longer "
            "supports background agents. They never became as reliable as "
            "tmux-hosted sessions, and every surface that read them is gone.\n"
            "Spawn a tmux-hosted session instead:\n"
            f"    endless task spawn E-{item_id}"
        )
    from endless.task_cmd import spawn_plan
    spawn_plan(item_id, project_name=project,
               worktree=worktree, force=force,
               permission_mode=permission_mode, model=model,
               name=session_name)


@task_cmd.command("reopen")
@click.argument("item_id", type=TASK_ID)
def task_reopen(item_id):
    """Reopen a terminal-status task back to `revisit`.

    Flips assumed/confirmed/completed → revisit — the status for work
    whose prior judgment no longer holds, whether that is a stale plan or
    something that shipped and turned out wrong.

    Task state only. It creates no worktree, and it LEAVES ANY EXISTING
    session→task binding INTACT: the session that worked the task is still
    reachable with `endless session goto E-<id> --resume` afterwards. Pick the
    work back up with that (add --revisit to reopen and continue in one step).
    """
    from endless.task_cmd import reopen_item
    reopen_item(item_id)


@task_cmd.command("chat")
def task_chat():
    """Start a chat-only session (no task tracking)."""
    from endless.task_cmd import start_chat
    start_chat()


@task_cmd.command("link")
@click.argument("source_id", type=TASK_ID)
@click.option("--to", "target", type=TASK_OR_DECISION_ID, required=True,
              help="Target ID (E-NN for a task, ED-NN for a decision)")
@click.option("--type", "dep_type", required=True,
              help="Relation type — legal set depends on target kind "
                   "(task→task: blocks, blocked_by, implements, implemented_by, "
                   "replaces, replaced_by, duplicates, duplicated_by, "
                   "documents, documented_by, "
                   "cleans_up, cleaned_up_by, relates_to; "
                   "task→decision: implements, cleans_up, documents, relates_to)")
def task_link(source_id, target, dep_type):
    """Create a typed relation from a task to a task or decision."""
    target_kind, target_id = target
    if target_kind == "decision":
        from endless.decision_cmd import link_task_to_decision
        link_task_to_decision(source_id, target_id, dep_type)
    else:
        from endless.task_cmd import link_tasks
        link_tasks(source_id, target_id, dep_type)


@task_cmd.command("unlink")
@click.argument("source_id", type=TASK_ID)
@click.option("--to", "target", type=TASK_OR_DECISION_ID, required=True,
              help="Target ID (E-NN for a task, ED-NN for a decision)")
@click.option("--type", "dep_type", default=None,
              help="Relation type to remove (omit to auto-detect when unambiguous)")
def task_unlink(source_id, target, dep_type):
    """Remove a typed relation from a task to a task or decision."""
    target_kind, target_id = target
    if target_kind == "decision":
        from endless.decision_cmd import unlink_task_from_decision
        unlink_task_from_decision(source_id, target_id, dep_type)
    else:
        from endless.task_cmd import unlink_tasks
        unlink_tasks(source_id, target_id, dep_type)


@task_cmd.command("block")
@click.argument("item_id", type=TASK_ID)
@click.option("--by", "blocker_id", type=TASK_ID, required=True,
              help="Task ID that blocks this task")
def task_block(item_id, blocker_id):
    """Record that a task is blocked by another task. Shortcut for `link --type blocked_by`."""
    from endless.task_cmd import link_tasks
    link_tasks(item_id, blocker_id, "blocked_by")


@task_cmd.command("replace")
@click.argument("item_id", type=TASK_ID)
@click.option("--by", "replacement_id", type=TASK_ID, required=True,
              help="Task ID that replaces this task")
@click.option("--status", "new_status", default=None,
              type=click.Choice(statuses.get("terminal")),
              help="Status to set on the replaced task. Default: 'obsolete', "
                   "except on work that already shipped "
                   f"({'/'.join(statuses.get('shipped'))}), which keeps the "
                   "status it earned — "
                   "the supersession is the relation, not a status that reads "
                   "as 'never happened'.")
@click.option("--outcome", default=None,
              help="Outcome — why this was replaced (inline; required if --status=declined)")
@click.option("--outcome-file", default=None,
              help="Load the outcome from a file")
@click.option("--allow-path", "allow_paths", multiple=True,
              help="Regex matching an absolute path to permit in inline content "
                   "(repeatable; escape hatch for the path gate).")
def task_replace(item_id, replacement_id, new_status, outcome, outcome_file, allow_paths):
    """Mark a task as replaced by another task, recording a replaced_by relation.

    The replaced task's status defaults to 'obsolete', but work that already
    shipped keeps the status it earned — see --status.
    """
    from endless.task_cmd import replace_task
    outcome = _resolve_content_flag(outcome, outcome_file, "outcome", allow_paths)
    replace_task(item_id, replacement_id, status=new_status, outcome=outcome)


@task_cmd.command("unblock")
@click.argument("item_id", type=TASK_ID)
@click.option("--by", "blocker_id", type=TASK_ID, required=True,
              help="Task ID to remove as blocker")
def task_unblock(item_id, blocker_id):
    """Remove a blocking dependency between tasks."""
    from endless.task_cmd import unlink_tasks
    unlink_tasks(item_id, blocker_id, "blocked_by")


@task_cmd.command("deps")
@click.argument("item_id", type=TASK_ID)
@click.option("--llm", is_flag=True,
              help="Token-efficient output for LLMs")
def task_deps(item_id, llm):
    """Show all relations for a task. Alias of `task relations`."""
    from endless.task_cmd import show_relations
    show_relations(item_id, llm=llm)


@task_cmd.command("relations")
@click.argument("item_id", type=TASK_ID)
@click.option("--llm", is_flag=True,
              help="Token-efficient output for LLMs")
def task_relations(item_id, llm):
    """Show all of a task's relations under a single 'Links:' section."""
    from endless.task_cmd import show_relations
    show_relations(item_id, llm=llm)


@main.command("plan", context_settings={"ignore_unknown_options": True})
@click.argument("args", nargs=-1, type=click.UNPROCESSED)
def plan_redirect(args):
    """Renamed to 'task'. Prints corrected command."""
    corrected = " ".join(["endless", "task"] + list(args))
    click.echo(
        click.style("The 'plan' command has been renamed to 'task'.", fg="yellow")
    )
    click.echo()
    click.echo(f"  Use: {click.style(corrected, bold=True)}")
    click.echo()
    click.echo(
        "If recording a Claude plan (from ~/.claude/plans/), "
        "use --type=plan with 'task add'."
    )
    raise SystemExit(1)


@main.group("decision")
def decision_cmd():
    """Manage project decisions."""
    pass


@decision_cmd.command("list")
@click.option("--project", default=None,
              help="Project name (default: detect from cwd)")
@click.option("--all", "show_all", is_flag=True,
              help="Show decisions from all projects")
@click.option("--sort", default=None,
              type=click.Choice(["id", "created", "title"]),
              help="Sort by column (default: id)")
@click.option("--llm", is_flag=True,
              help="Token-efficient output for LLMs")
@click.option("--json", "as_json", is_flag=True,
              help="JSON output")
@rowcap.limit_options
def decision_list(project, show_all, sort, llm, as_json, limit, no_limit):
    """List decisions for a project."""
    from endless.decision_cmd import list_decisions
    list_decisions(project_name=project, show_all=show_all,
                   sort_by=sort, llm=llm, as_json=as_json,
                   limit=limit, no_limit=no_limit)


@decision_cmd.command("add")
@click.argument("title")
@click.option("--description", default=None,
              help="Longer description of the decision (inline)")
@click.option("--description-file", default=None,
              help="Load the decision description from a file")
@click.option("--project", default=None,
              help="Project name (default: detect from cwd)")
@click.option("--about", "about_ids", type=TASK_ID, multiple=True,
              help="Task ID(s) this decision documents (repeatable; soft link)")
@click.option("--decides", "decides_ids", type=TASK_ID, multiple=True,
              help="Task ID(s) that implement this decision (repeatable; hard link)")
@click.option("--allow-path", "allow_paths", multiple=True,
              help="Regex matching an absolute path to permit in inline content "
                   "(repeatable; escape hatch for the path gate).")
def decision_add(title, description, description_file, project, about_ids, decides_ids, allow_paths):
    """Record a decision (starts as `proposed`)."""
    from endless.decision_cmd import add_decision
    description = _resolve_content_flag(description, description_file, "description", allow_paths)
    add_decision(
        title,
        description=description,
        project_name=project,
        about_task_ids=tuple(about_ids),
        decides_task_ids=tuple(decides_ids),
    )


@decision_cmd.command("update")
@click.argument("item_id", type=DECISION_ID)
@click.option("--title", default=None,
              help="New title for the decision")
@click.option("--description", default=None,
              help="New description (inline; replaces the existing description)")
@click.option("--description-file", default=None,
              help="Load the new description from a file")
@click.option("--allow-path", "allow_paths", multiple=True,
              help="Regex matching an absolute path to permit in inline content "
                   "(repeatable; escape hatch for the path gate).")
@click.option("--clear", "clear_fields", multiple=True,
              type=click.Choice(["description"]),
              help="Erase the description, naming it. --description-file refuses an "
                   "empty file, so this is the deliberate way to empty it. Conflicts "
                   "with --description/--description-file.")
def decision_update(item_id, title, description, description_file, allow_paths, clear_fields):
    """Edit a decision's title and/or description in place (no new ID)."""
    from endless.decision_cmd import update_decision
    resolved = _apply_clear_flags(clear_fields, {
        "description": _resolve_content_flag(description, description_file, "description", allow_paths, clearable=True),
    })
    update_decision(item_id, title=title, description=resolved["description"])


@decision_cmd.command("show")
@click.argument("item_ids", type=DECISION_ID, nargs=-1, required=True)
@click.option("--llm", is_flag=True,
              help="Token-efficient output for LLMs")
@click.option("--json", "as_json", is_flag=True,
              help="JSON output")
def decision_show(item_ids, llm, as_json):
    """Show detail for one or more decisions."""
    from endless.decision_cmd import detail_decision
    for item_id in item_ids:
        detail_decision(item_id, llm=llm, as_json=as_json)


@decision_cmd.command("accept")
@click.argument("item_ids", type=DECISION_ID, nargs=-1, required=True)
def decision_accept(item_ids):
    """Accept one or more decisions (proposed → accepted)."""
    from endless.decision_cmd import accept_decision
    for item_id in item_ids:
        accept_decision(item_id)


@decision_cmd.command("reject")
@click.argument("item_ids", type=DECISION_ID, nargs=-1, required=True)
@click.option("--reason", required=True,
              help="Why this decision is being rejected (stored on the row)")
def decision_reject(item_ids, reason):
    """Reject one or more decisions (proposed → rejected) with a stored reason."""
    from endless.decision_cmd import reject_decision
    for item_id in item_ids:
        reject_decision(item_id, reason)


@decision_cmd.command("unaccept")
@click.argument("item_ids", type=DECISION_ID, nargs=-1, required=True)
def decision_unaccept(item_ids):
    """Undo an accept (accepted → proposed)."""
    from endless.decision_cmd import unaccept_decision
    for item_id in item_ids:
        unaccept_decision(item_id)


@decision_cmd.command("unreject")
@click.argument("item_ids", type=DECISION_ID, nargs=-1, required=True)
def decision_unreject(item_ids):
    """Undo a reject (rejected → proposed); clears the stored reason."""
    from endless.decision_cmd import unreject_decision
    for item_id in item_ids:
        unreject_decision(item_id)


@decision_cmd.command("reconsider")
@click.argument("item_ids", type=DECISION_ID, nargs=-1, required=True)
def decision_reconsider(item_ids):
    """Undo whichever terminal status applies (accepted|rejected → proposed).

    Convenience over `unaccept` / `unreject`. Those name the status they undo
    and refuse if it doesn't match; this one doesn't check.
    """
    from endless.decision_cmd import reconsider_decision
    for item_id in item_ids:
        reconsider_decision(item_id)


@decision_cmd.command("supersede")
@click.argument("item_id", type=DECISION_ID)
@click.option("--by", "by_id", type=DECISION_ID, required=True,
              help="The decision that takes over (ED-NN)")
def decision_supersede(item_id, by_id):
    """Retire a decision in favor of a newer one (accepted -> superseded).

    Records a `supersedes` relation naming the replacement, so the successor
    is on the record and not just implied.
    """
    from endless.decision_cmd import supersede_decision
    supersede_decision(item_id, by_id)


@decision_cmd.command("obsolete")
@click.argument("item_ids", type=DECISION_ID, nargs=-1, required=True)
@click.option("--reason", required=True,
              help="What went away that this decision governed (stored on the row)")
def decision_obsolete(item_ids, reason):
    """Retire a decision with no replacement (accepted -> obsolete).

    For a decision that stopped applying because the code, feature or
    constraint it governed is simply gone. If a newer decision took over
    instead, use `decision supersede` so the successor is named.
    """
    from endless.decision_cmd import obsolete_decision
    for item_id in item_ids:
        obsolete_decision(item_id, reason)


@decision_cmd.command("reinstate")
@click.argument("item_ids", type=DECISION_ID, nargs=-1, required=True)
def decision_reinstate(item_ids):
    """Put a retired decision back in force (superseded|obsolete -> accepted).

    Drops the `supersedes` relation and clears the stored obsolete reason.
    For correcting the record; a decision rightly retired and genuinely back
    in force is better recorded as a new decision.
    """
    from endless.decision_cmd import reinstate_decision
    for item_id in item_ids:
        reinstate_decision(item_id)


@decision_cmd.command("link")
@click.argument("source_id", type=DECISION_ID)
@click.option("--to", "target", type=TASK_OR_DECISION_ID, required=True,
              help="Target ID (E-NN for a task, ED-NN for a decision)")
@click.option("--type", "relation_type", required=True,
              help="Relation type — legal set depends on the pair "
                   "(decision→decision: supersedes, reverses, modifies, "
                   "documents, relates_to)")
def decision_link(source_id, target, relation_type):
    """Link a decision to a task or another decision."""
    from endless.decision_cmd import link_decision
    target_kind, target_id = target
    link_decision(source_id, target_kind, target_id, relation_type)


@decision_cmd.command("unlink")
@click.argument("source_id", type=DECISION_ID)
@click.option("--to", "target", type=TASK_OR_DECISION_ID, required=True,
              help="Target ID (E-NN for a task, ED-NN for a decision)")
@click.option("--type", "relation_type", default=None,
              help="Relation type to remove (omit to auto-detect when unambiguous)")
def decision_unlink(source_id, target, relation_type):
    """Remove a decision-sourced relation."""
    from endless.decision_cmd import unlink_decision
    target_kind, target_id = target
    unlink_decision(source_id, target_kind, target_id, relation_type)


@main.group("epic")
def epic_cmd():
    """Manage epics — task-tree items of type `epic`.

    A thin convenience surface over `endless task ... --type epic`. Either form
    works; `endless epic` is the shorter human-facing alias.
    """
    pass


@epic_cmd.command("add")
@click.argument("title")
@click.option("--description", default=None,
              help="Longer description of the epic (inline)")
@click.option("--description-file", default=None,
              help="Load the epic description from a file")
@click.option("--text", default=None,
              help="Full epic text / plan content (inline)")
@click.option("--text-file", default=None,
              help="Load the full epic text / plan from a file")
@click.option("--phase", default="now",
              type=click.Choice(["urgent", "now", "next", "later", "maybe"]),
              help="Phase: urgent, now, next, later, maybe (default: now)")
@click.option("--project", default=None,
              help="Project name (default: detect from cwd)")
@click.option("--parent", type=TASK_ID, default=None,
              help="Parent task ID to add under")
@click.option("--after", type=TASK_ID, default=None,
              help="Insert after this task ID")
@click.option("--status", default=None,
              type=click.Choice(TASK_STATUSES),
              help="Initial status (default: untriaged; --tier 1 defaults to ready)")
@click.option("--tier", default=None,
              help="Tier (1-4 or auto/quick/deep/discuss)")
@click.option("--force", is_flag=True,
              help="Bypass title validation")
@click.option("--blocks", "blocks_ids", type=TASK_ID, multiple=True,
              help="Task ID(s) this new epic blocks (repeatable)")
@click.option("--blocked-by", "blocked_by_ids", type=TASK_ID, multiple=True,
              help="Task ID(s) that block this new epic (repeatable)")
@click.option("--relates-to", "relates_to_ids", type=TASK_ID, multiple=True,
              help="Task ID(s) related to this new epic (repeatable)")
@click.option("--implements", "implements_ids", type=TASK_ID, multiple=True,
              help="Task ID(s) that this new epic implements (repeatable)")
@click.option("--cleans-up", "cleans_up_ids", type=TASK_ID, multiple=True,
              help="Task ID(s) that this new epic cleans up after (repeatable)")
@click.option("--cleaned-up-by", "cleaned_up_by_ids", type=TASK_ID, multiple=True,
              help="Task ID(s) that clean up after this new epic (repeatable)")
@click.option("--duplicates", "duplicates_ids", type=TASK_ID, multiple=True,
              help="Task ID(s) this new epic duplicates — same concern, already "
                   "filed (repeatable)")
@click.option("--replaces", "replaces_ids", type=TASK_ID, multiple=True,
              help="Task ID(s) this new epic supersedes (repeatable). Records the "
                   "relation only; use `task replace <old> --by <new>` to also "
                   "close the replaced task.")
@click.option("--allow-path", "allow_paths", multiple=True,
              help="Regex matching an absolute path to permit in inline content "
                   "(repeatable; escape hatch for the path gate).")
def epic_add(title, description, description_file, text, text_file, phase, project,
             parent, after, status, tier, force,
             blocks_ids, blocked_by_ids, relates_to_ids, implements_ids,
             cleans_up_ids, cleaned_up_by_ids, duplicates_ids, replaces_ids,
             allow_paths):
    """Add an epic (a task with type=epic)."""
    from endless.epic_cmd import add_epic
    from endless.task_cmd import parse_tier, link_tasks
    description = _resolve_content_flag(description, description_file, "description", allow_paths)
    text = _resolve_content_flag(text, text_file, "text", allow_paths)
    tier_val = parse_tier(tier) if tier else None
    new_id = add_epic(title, description=description, text=text,
                      phase=phase, project_name=project, after=after,
                      parent_id=parent, status=status, tier=tier_val, force=force)
    if new_id is None:
        return
    for tid in blocks_ids:
        link_tasks(new_id, tid, "blocks")
    for tid in blocked_by_ids:
        link_tasks(new_id, tid, "blocked_by")
    for tid in relates_to_ids:
        link_tasks(new_id, tid, "relates_to")
    for tid in implements_ids:
        link_tasks(new_id, tid, "implements")
    for tid in cleans_up_ids:
        link_tasks(new_id, tid, "cleans_up")
    for tid in cleaned_up_by_ids:
        link_tasks(new_id, tid, "cleaned_up_by")
    for tid in duplicates_ids:
        link_tasks(new_id, tid, "duplicates")
    for tid in replaces_ids:
        link_tasks(new_id, tid, "replaces")


@epic_cmd.command("list")
@click.option("--project", default=None,
              help="Project name (default: detect from cwd)")
@click.option("--all", "show_all", is_flag=True,
              help="Include confirmed items")
@click.option("--status", default=None,
              type=MultiChoice(TASK_STATUSES),
              help="Filter by status (comma-separated, e.g. unplanned,ready)")
@click.option("--phase", default=None,
              type=click.Choice(["urgent", "now", "next", "later", "maybe"]),
              help="Filter by phase")
@click.option("--tier", default=None,
              help="Filter by tier (1-4 or auto/quick/deep/discuss)")
@click.option("--parent", "parent_id", default=None,
              help="Filter to children of this task (e.g. E-101), or 'none' for root tasks")
@click.option("--sort", default=None,
              type=click.Choice(["id", "status", "phase", "tier", "created", "title"]),
              help="Sort by column (default: id)")
@click.option("--llm", is_flag=True,
              help="Token-efficient output for LLMs")
@click.option("--json", "as_json", is_flag=True,
              help="JSON output")
@rowcap.limit_options
def epic_list(project, show_all, status, phase, tier, parent_id, sort,
              llm, as_json, limit, no_limit):
    """List epics for a project."""
    from endless.epic_cmd import list_epics
    from endless.task_cmd import parse_tier_filter, parse_parent_filter
    tier_val = parse_tier_filter(tier) if tier else None
    parent_val = parse_parent_filter(parent_id) if parent_id else None
    list_epics(project_name=project, show_all=show_all,
               status_filter=status, phase_filter=phase,
               tier_filter=tier_val, parent_id=parent_val,
               sort_by=sort, llm=llm, as_json=as_json,
               limit=limit, no_limit=no_limit)


@epic_cmd.command("show")
@click.argument("item_ids", type=TASK_ID, nargs=-1, required=True)
@click.option("--no-description", is_flag=True,
              help="Hide description")
@click.option("--analysis", "show_analysis", is_flag=True,
              help="Show analysis field")
@click.option("--text", "show_text", is_flag=True,
              help="Show text field")
@click.option("--no-children", is_flag=True,
              help="Hide child tasks (shown by default)")
@click.option("--outcome", "show_outcome", is_flag=True,
              help="Show the full outcome field (hidden by default; a "
                   "char-count placeholder shows otherwise)")
@click.option("--all-fields", "all_fields", is_flag=True,
              help="Show every content section (description, analysis, text, "
                   "outcome, children)")
@click.option("--llm", is_flag=True,
              help="Token-efficient output for LLMs")
@click.option("--json", "as_json", is_flag=True,
              help="JSON output")
def epic_show(item_ids, no_description, show_analysis, show_text,
              no_children, show_outcome, all_fields, llm, as_json):
    """Show detail for one or more epics (children shown by default)."""
    from endless.epic_cmd import show_epic
    show_children = not no_children
    if all_fields:
        show_analysis = show_text = show_children = show_outcome = True
    for item_id in item_ids:
        show_epic(item_id, show_description=not no_description,
                  show_analysis=show_analysis, show_text=show_text,
                  show_children=show_children, show_outcome=show_outcome,
                  llm=llm, as_json=as_json)


@epic_cmd.command("update")
@click.argument("item_ids", type=TASK_ID, nargs=-1, required=True)
@click.option("--status", default=None, help=TASK_STATUS_HELP)
@click.option("--title", default=None,
              help="New title")
@click.option("--description", default=None,
              help="New description (inline)")
@click.option("--description-file", default=None,
              help="Load the new description from a file")
@click.option("--text", default=None,
              help="Full epic text / plan content (inline)")
@click.option("--text-file", default=None,
              help="Load the full epic text / plan from a file")
@click.option("--parent", type=TASK_ID, default=None,
              help="Set parent task ID (0 to make root)")
@click.option("--phase", default=None,
              type=click.Choice(["urgent", "now", "next", "later", "maybe"]),
              help="Phase: urgent, now, next, later, maybe")
@click.option("--tier", default=None,
              help="Tier (0=n/a, 1-4 or auto/quick/deep/discuss, none=clear)")
@click.option("--analysis", "analysis_text", default=None,
              help="Analysis content (inline)")
@click.option("--analysis-file", default=None,
              help="Load the analysis content from a file")
@click.option("--force", is_flag=True,
              help="Bypass title validation")
@click.option("--outcome", default=None,
              help="Outcome / reason for status (inline; required if status=declined)")
@click.option("--outcome-file", default=None,
              help="Load the outcome from a file")
@click.option("--allow-path", "allow_paths", multiple=True,
              help="Regex matching an absolute path to permit in inline content "
                   "(repeatable; escape hatch for the path gate).")
@click.option("--clear", "clear_fields", multiple=True,
              type=click.Choice(CLEARABLE_CONTENT_FIELDS),
              help="Erase a content field, naming it (repeatable). --<field>-file "
                   "refuses an empty file, so this is the deliberate way to empty "
                   "description/text/analysis/outcome. Conflicts with the same "
                   "field's --<field>/--<field>-file.")
def epic_update(item_ids, status, title, description, description_file, text,
                text_file, parent, phase, tier, analysis_text, analysis_file,
                force, outcome, outcome_file, allow_paths, clear_fields):
    """Update one or more epics (promotes type to epic).

    Updating an existing task-typed row through this verb also promotes it to
    an epic — e.g. `endless epic update E-NNN --status ready`.
    """
    from endless.epic_cmd import update_epic
    from endless.task_cmd import parse_tier
    resolved = _apply_clear_flags(clear_fields, {
        "description": _resolve_content_flag(description, description_file, "description", allow_paths, clearable=True),
        "text": _resolve_content_flag(text, text_file, "text", allow_paths, clearable=True),
        "analysis": _resolve_content_flag(analysis_text, analysis_file, "analysis", allow_paths, clearable=True),
        "outcome": _resolve_content_flag(outcome, outcome_file, "outcome", allow_paths, clearable=True),
    })
    description = resolved["description"]
    text = resolved["text"]
    analysis_text = resolved["analysis"]
    outcome = resolved["outcome"]
    tier_val = parse_tier(tier) if tier else None
    for item_id in item_ids:
        update_epic(item_id, status=status, title=title,
                    description=description, text=text, parent_id=parent,
                    phase=phase, tier=tier_val, analysis=analysis_text,
                    outcome=outcome, force=force)


@main.group("worktree")
def worktree_cmd():
    """Inspect git worktrees managed by endless (read-only)."""
    pass


@worktree_cmd.command("list")
@click.option("--state", "state_filter", default=None,
              type=click.Choice(["main", "active", "foreign"]),
              help="Filter by lifecycle state")
@click.option("--json", "as_json", is_flag=True, help="JSON output")
@rowcap.limit_options
def worktree_list(state_filter, as_json, limit, no_limit):
    """List worktrees for the current project."""
    from endless.worktree_cmd import list_worktrees
    list_worktrees(state_filter, as_json, limit=limit, no_limit=no_limit)


@worktree_cmd.command("current")
@click.option("--json", "as_json", is_flag=True, help="JSON output")
def worktree_current(as_json):
    """Show the worktree for the current cwd."""
    from endless.worktree_cmd import current_worktree
    current_worktree(as_json)


@worktree_cmd.command("show")
@click.argument("name_or_path")
@click.option("--json", "as_json", is_flag=True, help="JSON output")
def worktree_show(name_or_path, as_json):
    """Show detail for one worktree (by trailing path segment or absolute path)."""
    from endless.worktree_cmd import show_worktree
    show_worktree(name_or_path, as_json)


@worktree_cmd.command("for-task")
@click.argument("task_id")
@click.option("--json", "as_json", is_flag=True, help="JSON output")
def worktree_for_task(task_id, as_json):
    """Resolve a task ID to its worktree path (or report none)."""
    from endless.worktree_cmd import for_task
    for_task(task_id, as_json)


@worktree_cmd.command("land")
@click.argument("task_id")
@click.option("--dry-run", is_flag=True,
              help="Show what would happen without making changes")
@click.option("--record-only", is_flag=True,
              help="Record a landing that already happened (no git). Requires --sha.")
@click.option("--sha", default=None,
              help="Merge commit SHA for --record-only.")
@click.option("--branch", default=None,
              help="Branch for --record-only; omit to record NULL (branch gone).")
@click.option("--at", default=None,
              help="Landing timestamp (RFC3339) for --record-only; default: the --sha commit date.")
def worktree_land(task_id, dry_run, record_only, sha, branch, at):
    """Auto-commit endless-managed modifications, rebase, ff-merge, remove worktree."""
    from endless.worktree_cmd import land_worktree
    land_worktree(task_id, dry_run, record_only=record_only, sha=sha, branch=branch, at=at)


@worktree_cmd.command("diagnose")
@click.argument("task_id", required=False)
@click.option("--json", "as_json", is_flag=True, help="JSON output")
def worktree_diagnose(task_id, as_json):
    """Classify the rebase conflict a land recorded, and prescribe only what is proven.

    When `worktree land` hits a rebase conflict it records the state — the
    unmerged paths, both sides of every conflicting hunk, the commit that failed
    to replay — before aborting the rebase, and refuses to interpret any of it
    on the spot. This interprets it.

    It reports which of five kinds of conflict this is, and prints a recovery
    ONLY for the kinds it can prove. For the two it cannot — your branch using
    names the base branch has deleted, and a genuine overlap between two
    intentional edits — it prints the evidence, names what is at stake, and
    stops. A recovery you cannot prove is a guess, and a guess printed by a tool
    is read as an instruction.

    With no task id, diagnoses the worktree you are standing in. Exits non-zero
    when there is no recorded conflict; nothing is reproduced.
    """
    from endless.worktree_cmd import diagnose_land_conflict
    diagnose_land_conflict(task_id, as_json)


@worktree_cmd.command("drop")
@click.argument("name_or_path")
@click.option("--force", is_flag=True,
              help="Drop even if in use/modified/unlanded/foreign")
def worktree_drop(name_or_path, force):
    """Remove a worktree (refuses in-use/modified/unlanded/foreign without --force)."""
    from endless.worktree_cmd import drop_worktree
    drop_worktree(name_or_path, force)


@worktree_cmd.command("reap")
def worktree_reap():
    """Sweep stale settled worktrees.

    Removes worktree directories that are settled — clean, and holding no
    commit whose content the base branch lacks — whose owning task has been
    untouched for longer than worktree_ttl (.endless/config.json, default
    14d) AND that no live process holds a cwd inside. A recorded landing is
    not required: a branch sitting at the base with nothing to land is just
    as disposable as one that landed. A directory nothing was ever recorded
    about is skipped — there is no moment to age off.
    """
    from endless.worktree_cmd import _project_root, _reap_stale_worktrees
    _reap_stale_worktrees(_project_root())


@worktree_cmd.command("sync")
@click.option("--apply", is_flag=True,
              help="Actually rebase. Without it, sync only reports what it would do.")
def worktree_sync(apply):
    """Rebase this project's task worktrees onto the default branch.

    A worktree branched before a change landed does not have that change until
    someone rebases it, so a fix to a shared file can reach main and reach no
    working checkout at all. This reports that drift, and with --apply closes
    it.

    Dry run by default. Skips any worktree with uncommitted changes (that is a
    session's in-flight work) and the one you are standing in. A conflicting
    rebase is aborted, reported, and left exactly as it was; the sweep carries
    on. Nothing is ever removed.
    """
    from endless.worktree_cmd import sync_worktrees
    sync_worktrees(apply)


@worktree_cmd.command("check")
def worktree_check():
    """Report genuine git/worktree handoff anomalies for the current worktree.

    Prints one terse line per real anomaly (uncommitted user files,
    detached/wrong branch, a prunable/locked checkout) and nothing when clean.
    Exit 0 clean, 1 anomalies present, 2 on error. Run it at session handoff:
    empty output means there is genuinely nothing git-side to narrate.
    """
    from endless.worktree_cmd import check_worktree
    check_worktree()


@main.group("jobs")
def jobs_cmd():
    """Inspect and drive the fire-once background job runner."""
    pass


@jobs_cmd.command("list")
def jobs_list():
    """Show registered jobs and their schedule, last run, and failure count."""
    from endless.jobs_cmd import jobs_list as impl
    impl()


@jobs_cmd.command("run")
@click.option("--job", default=None,
              help="Run only this job, bypassing the due check (the lease still applies)")
def jobs_run(job):
    """Run every due job once, then exit.

    Repetition lives in the trigger, not the runner: the session monitor fires
    this on each refresh. Running it by hand fires the same single pass.
    """
    from endless.jobs_cmd import jobs_run as impl
    impl(job)


@jobs_cmd.command("retry")
@click.argument("name")
def jobs_retry(name):
    """Clear a job's backoff and make it due immediately.

    The "I fixed the underlying problem" verb. Deliberately separate from
    `endless errors clear`, which only means "I have seen this" — so tidying
    your error list cannot silently re-arm a job that is still broken.
    """
    from endless.jobs_cmd import jobs_retry as impl
    impl(name)


@main.group("triage")
def triage_cmd():
    """Route untriaged tasks by judging description sufficiency."""
    pass


@triage_cmd.command("run")
@click.option("--task", "task_ref", default=None,
              help="Triage exactly this task (E-N), ignoring the queue")
@click.option("--limit", type=int, default=None,
              help="Max tasks to triage in one sweep "
                   "(default: 10; every task is a model call)")
@click.option("--project", default=None,
              help="Registered project name to sweep (default: the project cwd is in)")
@click.option("--all-projects", is_flag=True,
              help="Sweep every project — what the background job does")
@click.option("--dry-run", is_flag=True,
              help="Print the decision and rationale; write nothing")
def triage_run(task_ref, limit, project, all_projects, dry_run):
    """Decide, for each untriaged task, whether its description is a sufficient
    spec (-> submitted) or design work is needed first (-> unplanned).

    Fail-open by design: a model timeout, a missing `claude`, or an
    unparseable reply leaves the task `untriaged` for the next sweep and exits
    zero. The worst outcome is the status quo — you route it by hand with
    `endless task submit`, which stays the permanent override.

    A task that stopped being `untriaged` between selection and the write is
    never overwritten, so a human's call always beats the triager's.
    """
    from endless import triage
    from endless.task_cmd import parse_task_id

    if project and all_projects:
        raise click.ClickException(
            "--project and --all-projects are mutually exclusive."
        )
    triage.run(
        task_id=parse_task_id(task_ref) if task_ref else None,
        limit=limit if limit is not None else triage.DEFAULT_BATCH_LIMIT,
        project=project,
        all_projects=all_projects,
        dry_run=dry_run,
    )


@main.group("errors")
def errors_cmd():
    """Inspect and clear recorded errors."""
    pass


@errors_cmd.command("show")
@click.option("--all", "show_all", is_flag=True, help="Include cleared errors")
@click.option("--detail", is_flag=True, help="Print every occurrence's full capture")
@click.option("--id", "error_id", type=int, default=None, help="Show only this error id")
@click.option("--project", default="", help="Scope to this project instead of the one you are in")
@click.option("--all-projects", "all_projects", is_flag=True,
              help="Cover every project on the machine")
def errors_show(show_all, detail, error_id, project, all_projects):
    """List recorded errors, most recently seen first.

    Only uncleared errors are shown by default — the same set the badge on
    `session status` / `session monitor` counts.

    Scoped to the project you are standing in, plus the faults attributed to no
    project (the background job runner's own failures belong to the machine, not
    to a project, and would otherwise be reportable nowhere). --all-projects
    widens to the whole machine and adds a PROJECT column; outside any registered
    project that is what you get anyway.
    """
    from endless.jobs_cmd import errors_show as impl
    impl(show_all, detail, error_id, project, all_projects)


@errors_cmd.command("clear")
@click.argument("ids", nargs=-1, type=int)
@click.option("--project", default="", help="Scope to this project instead of the one you are in")
@click.option("--all-projects", "all_projects", is_flag=True,
              help="Cover every project on the machine")
def errors_clear(ids, project, all_projects):
    """Mark errors cleared. Clears every open error when given no ids.

    Clearing NEVER deletes: the row stays as history, and a recurrence opens a
    NEW error beside it, so a problem that came back is visibly distinct from
    one that never left.

    With no ids the scope bounds what is cleared — the same set `errors show`
    lists under the same flags, so "dismiss what you just showed me" cannot reach
    another project's incidents. Named ids are cleared wherever they live: you
    typed the id, so the scope has nothing left to decide.
    """
    from endless.jobs_cmd import errors_clear as impl
    impl(ids, project, all_projects)


@errors_cmd.command("record", hidden=True)
@click.option("--code", required=True, help="Catalog code ID, e.g. ERR-0008")
@click.option("--summary", required=True, help="Short text shown in lists and the badge")
@click.option("--source", default="", help="Subsystem raising it, e.g. triage:inline")
@click.option("--detail", default="", help="Long capture; goes to the detail log")
@click.option("--fingerprint", default="", help="Grouping key (defaults to the summary)")
def errors_record(code, summary, source, detail, fingerprint):
    """Record a real catalog fault.

    Hidden because it is an internal bridge, not a verb a person needs: the
    detached `endless triage run` child uses it to put a failed triage on the
    session-status badge, since Python cannot write the fault store directly.
    Shipped rather than Go-only so it is reachable from the CLI a user
    actually types.
    """
    from endless.jobs_cmd import errors_record as impl
    impl(code, summary, source, detail, fingerprint)


@errors_cmd.command("codes")
def errors_codes():
    """Print the documented error catalog (see docs/errors.md)."""
    from endless.jobs_cmd import errors_codes as impl
    impl()


@errors_cmd.command("raise")
@click.option("--severity", type=click.Choice(["warning", "error"]),
              default="warning", show_default=True,
              help="Severity to raise")
@click.option("--summary", default=None,
              help="Incident summary (defaults to the code's title)")
@click.option("--source", default=None,
              help="Source subsystem to attribute it to")
@click.option("--repeat", type=int, default=1, show_default=True,
              help="Record this many occurrences (they collapse into one incident)")
def errors_raise(severity, summary, source, repeat):
    """Record a SYNTHETIC fault, to see this surface work.

    Nothing is wrong when one appears. It exists so the session-status badge,
    this listing and the detail log can be exercised on demand instead of only
    when something genuinely breaks.

    It records through the same path a real fault takes, so what you get is
    shaped exactly like the real thing; only the code marks it synthetic
    (ERR-0006 warning / ERR-0007 error). Dismiss it with `errors clear <id>`.
    """
    from endless.jobs_cmd import errors_raise as impl
    impl(severity, summary, source, repeat)


@main.group("verb")
def verb_cmd():
    """Manage verbs — the registered actions that can start task titles."""
    pass


@verb_cmd.command("add")
@click.argument("value")
@click.option("--definition", default=None,
              help="Short 'to ___' definition (required, e.g., 'to deliberate over')")
@click.option("--category", "category", multiple=True,
              type=click.Choice(["action", "investigation"]),
              help="Verb category, repeatable. 'action' verbs lead "
                   "changed-behavior/artifact work (todo/bugfix); 'investigation' "
                   "verbs lead findings/decision work (research/brainstorm). Pass "
                   "both for a genuine dual. Omit ⇒ 'action'.")
@click.option("--machine-only", is_flag=True,
              help="Skip the project config write (machine layer only)")
def verb_add(value, definition, category, machine_only):
    """Register a new verb."""
    from endless.verb_cmd import add_verb
    add_verb(value, definition, category, machine_only)


@verb_cmd.command("list")
@click.option("--json", "as_json", is_flag=True, help="JSON output")
@rowcap.limit_options
def verb_list(as_json, limit, no_limit):
    """List registered verbs from project + machine layers."""
    from endless.verb_cmd import list_verbs
    list_verbs(as_json, limit=limit, no_limit=no_limit)


@verb_cmd.command("update")
@click.argument("value")
@click.option("--definition", default=None,
              help="Replace the 'to ___' definition. Omit to leave it as-is.")
@click.option("--category", "category", multiple=True,
              type=click.Choice(["action", "investigation"]),
              help="Replace the category set, repeatable. 'action' verbs lead "
                   "changed-behavior/artifact work (todo/bugfix); 'investigation' "
                   "verbs lead findings/decision work (research/brainstorm). Pass "
                   "both for a genuine dual. Omit to leave the set as-is.")
@click.option("--machine-only", is_flag=True,
              help="Update the machine layer only (skip the project config write)")
def verb_update(value, definition, category, machine_only):
    """Correct a registered verb's definition or category.

    Changes only the fields you pass and leaves the rest exactly as they are.
    That is the difference from remove-then-add, where a field you forgot to
    restate was silently rewritten instead of left alone. A built-in verb with
    no entry of its own gets one holding just the corrected fields; the rest
    still resolves from the built-in.
    """
    from endless.verb_cmd import update_verb
    update_verb(value, definition, category, machine_only)


@verb_cmd.command("remove")
@click.argument("value")
@click.option("--machine-only", is_flag=True,
              help="Remove from machine layer only")
def verb_remove(value, machine_only):
    """Remove a verb."""
    from endless.verb_cmd import remove_verb
    remove_verb(value, machine_only)


@main.group("lesson")
def lesson_cmd():
    """Record corrections in the project's lessons log."""
    pass


@lesson_cmd.command("write")
@click.argument("summary")
@click.option("--text", default=None,
              help="The lesson itself — what went wrong, why, and the rule "
                   "that replaces it (inline)")
@click.option("--text-file", default=None,
              help="Load the lesson from a file")
@click.option("--allow-path", "allow_paths", multiple=True,
              help="Regex matching an absolute path to permit in inline content "
                   "(repeatable; escape hatch for the path gate).")
def lesson_write(summary, text, text_file, allow_paths):
    """Append a lesson to the project's log and commit it on main.

    SUMMARY is the lesson's one-line rule, up to 384 characters. It is written
    to the log verbatim, and a Conventional-Commits-shaped subject
    `lesson: <summary>` is derived from it — truncated with an ellipsis if the
    summary does not fit inside 60 characters. The detail goes in --text, has
    no cap, and becomes the commit body.

    The append lands in <project>/.endless/LESSONS.md on the MAIN checkout —
    never a worktree copy — and is committed there in the same step, so
    recording a correction never waits on `worktree land`.
    """
    from endless.lesson_cmd import write_lesson
    text = _resolve_content_flag(text, text_file, "text", allow_paths)
    write_lesson(summary, text)


@main.group("phrase")
def phrase_cmd():
    """Manage matchers (action regexes) in config files."""
    pass


@phrase_cmd.command("add")
@click.argument("type_", metavar="TYPE")
@click.argument("value")
@click.option("--scope", default=None,
              help="Optional scope qualifier (e.g., 'task')")
@click.option("--method", default=None,
              type=click.Choice(["exact", "substring", "regex"]),
              help="Match algorithm (default: regex)")
@click.option("--case-sensitive", is_flag=True,
              help="Match exact case (default: case-insensitive)")
@click.option("--machine-only", is_flag=True,
              help="Skip the project config write (machine layer only)")
def phrase_add(type_, value, scope, method, case_sensitive, machine_only):
    """Add a matcher: TYPE VALUE [--scope ...] [--method ...] [--case-sensitive] [--machine-only]."""
    from endless.phrase_cmd import add_phrase
    add_phrase(type_, value, scope, method, case_sensitive, machine_only)


@phrase_cmd.command("list")
@click.option("--type", "type_filter", default=None,
              help="Filter to one type")
@click.option("--scope", "scope_filter", default=None,
              help="Filter to one scope")
@click.option("--all", "show_disabled", is_flag=True,
              help="Include disabled matchers")
@click.option("--json", "as_json", is_flag=True, help="JSON output")
@rowcap.limit_options
def phrase_list(type_filter, scope_filter, show_disabled, as_json, limit, no_limit):
    """List matchers from project + machine config layers, merged."""
    from endless.phrase_cmd import list_phrases
    list_phrases(type_filter, scope_filter, show_disabled, as_json,
                 limit=limit, no_limit=no_limit)


@phrase_cmd.command("disable")
@click.argument("type_", metavar="TYPE")
@click.argument("value")
@click.option("--scope", default=None, help="Scope qualifier")
@click.option("--machine-only", is_flag=True,
              help="Operate on machine layer only")
def phrase_disable(type_, value, scope, machine_only):
    """Disable a matcher value (it stops matching but isn't removed)."""
    from endless.phrase_cmd import disable_phrase
    disable_phrase(type_, value, scope, machine_only)


@phrase_cmd.command("enable")
@click.argument("type_", metavar="TYPE")
@click.argument("value")
@click.option("--scope", default=None, help="Scope qualifier")
def phrase_enable(type_, value, scope):
    """Re-enable a previously disabled matcher value."""
    from endless.phrase_cmd import enable_phrase
    enable_phrase(type_, value, scope)


@phrase_cmd.command("remove")
@click.argument("type_", metavar="TYPE")
@click.argument("value")
@click.option("--scope", default=None, help="Scope qualifier")
@click.option("--machine-only", is_flag=True,
              help="Remove from machine layer only")
def phrase_remove(type_, value, scope, machine_only):
    """Remove a matcher value (use disable for reversible silencing)."""
    from endless.phrase_cmd import remove_phrase
    remove_phrase(type_, value, scope, machine_only)


@main.command("docs")
@click.argument("name", default=None, required=False)
@click.option("--type", "type_filter", default=None,
              help="Filter by document type")
def docs_cmd(name, type_filter):
    """List tracked documents for a project (temporarily disabled)."""
    click.echo(
        click.style("•", fg="yellow")
        + " Document tracking is temporarily disabled."
        + " It will return with shadow git repos."
    )


@main.command("notes")
@click.argument("name", default=None, required=False)
@click.option("--all", "show_all", is_flag=True,
              help="Include resolved notes")
def notes_cmd(name, show_all):
    """Show pending notes for a project."""
    from endless.notes_cmd import list_notes
    list_notes(name=name, show_all=show_all)


@main.group("note")
def note_cmd():
    """Manage notes."""
    pass


@note_cmd.command("add")
@click.argument("message")
@click.option("--project", default=None,
              help="Project name (default: detect from cwd)")
def note_add(message, project):
    """Add a note to a project."""
    from endless.notes_cmd import add_note
    add_note(name=project, message=message)


@note_cmd.command("resolve")
@click.argument("note_id", type=int)
def note_resolve(note_id):
    """Mark a note as resolved."""
    from endless.notes_cmd import resolve_note
    resolve_note(note_id=note_id)



@main.group()
def setup():
    """Install hooks and integrations."""
    pass


@setup.command("prompt-hook")
def setup_prompt_hook():
    """Install the ZSH prompt hook for activity monitoring."""
    from endless.setup import setup_prompt_hook
    setup_prompt_hook()


@setup.command("remove-prompt-hook")
def setup_remove_prompt_hook():
    """Remove the ZSH prompt hook."""
    from endless.setup import remove_prompt_hook
    remove_prompt_hook()


@setup.command("claude-hook")
def setup_claude_hook_cmd():
    """Install the Claude Code hook for activity monitoring."""
    from endless.setup import setup_claude_hook
    setup_claude_hook()


@setup.command("remove-claude-hook")
def setup_remove_claude_hook():
    """Remove the Claude Code hook."""
    from endless.setup import remove_claude_hook
    remove_claude_hook()


@setup.command("shell-helpers")
def setup_shell_helpers_cmd():
    """Install the esu/esp/esf session shell helpers into ~/.zshrc."""
    from endless.setup import install_shell_helpers
    install_shell_helpers()


@setup.command("remove-shell-helpers")
def setup_remove_shell_helpers_cmd():
    """Remove the esu/esp/esf session shell helpers."""
    from endless.setup import remove_shell_helpers
    remove_shell_helpers()


@setup.command("output-style")
@click.option("--activate", is_flag=True,
              help="Also select the style in .claude/settings.json.")
@click.option("--force", is_flag=True, help="Overwrite an existing style file.")
@click.option("--project", default=None, help="Project name (default: detect from cwd).")
def setup_output_style_cmd(activate, force, project):
    """Install the Endless Claude Code output style into this project."""
    from endless.setup import setup_output_style
    setup_output_style(activate=activate, project=project, force=force)


@setup.command("remove-output-style")
@click.option("--project", default=None, help="Project name (default: detect from cwd).")
def setup_remove_output_style_cmd(project):
    """Remove the Endless output style and deactivate it."""
    from endless.setup import remove_output_style
    remove_output_style(project=project)


# tmux integration command group (E-1236)
@main.group("tmux")
def tmux_cmd():
    """Endless tmux integration (ephemeral status line + popup menus)."""
    pass


@tmux_cmd.command("apply")
@click.option("--hotkey", default="e",
              help="Prefix-table key to bind for the popup menu (default: e)")
@click.option("--status-interval", type=int, default=2,
              help="tmux status-interval seconds (default: 2)")
def tmux_apply(hotkey, status_interval):
    """Configure the running tmux server for Endless (ephemeral).

    Enables a second status row, wires it to the printer, installs hotkey
    and right-click popup menus. No files are touched; reverses when the
    tmux server exits. Run once per tmux server start.
    """
    from endless.tmux_cmd import run_apply
    run_apply(hotkey, status_interval)


@tmux_cmd.command("status-line")
def tmux_status_line():
    """Print one styled line for tmux's status-format[1].

    Normally invoked by tmux on each refresh, not by humans. The tmux
    config calls the `endless-go tmux` Go binary directly to stay under the
    latency budget; this Python wrapper exists for parity and debugging.
    """
    from endless.tmux_cmd import run_status_line
    run_status_line()


@tmux_cmd.command("init")
@click.option("--hotkey", default="e",
              help="Prefix-table key to bind for the popup menu (default: e)")
@click.option("--status-interval", type=int, default=2,
              help="tmux status-interval seconds (default: 2)")
def tmux_init(hotkey, status_interval):
    """Init the current tmux server for Endless (gated by @server_uuid).

    Target for `set-hook -g session-created "run-shell 'endless tmux init'"`
    in ~/.tmux.conf. First call after a tmux server start runs reset +
    apply and stamps a fresh @server_uuid; subsequent calls no-op.
    """
    from endless.tmux_cmd import run_init
    run_init(hotkey, status_interval)


# `tmux task` is an alias for `task id` — the same command object, so the two
# spellings are provably identical. `task id` is the primary form (it is a
# question about your task, not about tmux); this one exists for whoever
# reaches for the tmux surface first. E-1302.
tmux_cmd.add_command(task_id_cmd, name="task")


@main.group("db")
def db_cmd():
    """Database administration."""
    pass


@db_cmd.command("apply-change")
@click.argument("path", type=click.Path(exists=True))
def db_apply_change(path):
    """Apply one per-ticket schema-change file (internal/schema/changes/<name>).

    Records the change in _schema_version; re-applying an already-applied change
    is a no-op. Driven by `just land`, one file per invocation.
    """
    # Schema apply-change is an always-main operation driven by land; pin the
    # real DB when run from a sandbox-routed session with no explicit --db, so
    # the _schema_version marker lands in the real DB rather than the sandbox. E-1628.
    from endless import config
    config.default_db_to_main()
    from endless.event_bridge import apply_change
    result = apply_change(path)
    name = result.get("name") or path
    status = result.get("status") or "applied"
    if status == "skipped":
        click.echo(f"Change {name}: already applied (skipped).")
    else:
        click.echo(f"Change {name}: applied.")


@db_cmd.command("backup")
def db_backup():
    """Back up the database (VACUUM INTO a timestamped copy under backups/)."""
    # Backup during land is an always-main operation; pin the real DB when run
    # from a sandbox-routed session with no explicit --db. E-1628.
    from endless import config
    config.default_db_to_main()
    from endless.event_bridge import backup_db
    result = backup_db()

    # E-1942: name the file. "Database backed up." is unusable as the first half
    # of a restore — the whole point of a backup is being able to say which one.
    path = result.get("path")
    if not path:
        # An endless-go older than E-1942 reports no path. Can happen in a
        # self-dev worktree, where --db main runs the worktree's Python against
        # the globally installed binary.
        click.echo("Database backed up.")
        return
    if result.get("status") == "skipped":
        click.echo("Database already backed up within the last 60s — "
                   "nothing written.")
        click.echo(f"Existing backup: {config.tilde(path)}")
    else:
        click.echo(f"Database backed up to {config.tilde(path)}")

    # E-2121: the backup and the retention sweep can fail independently. A
    # written backup is still a success — a land depends on that — so a failed
    # sweep arrives as a warning beside the path rather than as an exit code.
    # Saying nothing would let a backups directory stop being pruned in silence.
    warning = result.get("warning")
    if warning:
        click.echo(f"Warning: backup retention did not complete: {warning}",
                   err=True)


@db_cmd.command("restore")
@click.argument("backup", required=False)
@click.option("--dry-run", is_flag=True,
              help="Print holders, journal modes, sizes and paths; change nothing.")
@click.option("--force", is_flag=True,
              help="Restore even though processes still hold the database open.")
def db_restore(backup, dry_run, force):
    """Restore the database from a backup (default: the newest one).

    Reports which processes still have the database open — by pid and command —
    and refuses rather than killing them, because copying over an open database
    is what left every reader failing with 'database is locked' on 2026-08-10.
    Parks the pre-restore database (and its -wal/-shm/-journal sidecars) under
    <config dir>/pre-restore/ so the restore itself is reversible, then
    re-establishes journal_mode=WAL — `db backup` uses VACUUM INTO, which writes
    a rollback-journal file — and runs PRAGMA integrity_check, failing loudly on
    anything but 'ok'.

    BACKUP is a path, or a bare filename resolved inside the backups directory.
    Use --dry-run first: mid-incident it is the report you actually want.
    """
    # Deliberately NOT config.default_db_to_main(): unlike `db backup`, which
    # `just land` fires unattended, a restore is a destructive operation a human
    # aims by hand. Inside a self-dev worktree it therefore takes the ordinary
    # explicit --db, enforced by require_db_context() in run_restore.
    from endless.db_restore import run_restore
    run_restore(backup, dry_run, force)


@db_cmd.command("path")
def db_path():
    """Print the absolute path to the database selected by the global --db.

    For SQL-client debugging or scripting, and referenced by the --db gate's
    refusal message. Uses the single global --db: run
    `endless db path --db=main` or `endless db path --db=sandbox`. Resolving
    --db=sandbox requires running from inside a self-dev worktree (the global
    --db handler errors otherwise). This command does not open the DB, so it is
    not subject to the worktree gate.
    """
    from endless import config

    if config.RESOLVED_CONFIG_DIR is None:
        raise click.ClickException(
            "db path needs an explicit --db value: "
            "'endless db path --db=main' or 'endless db path --db=sandbox'."
        )
    click.echo(str(config.DB_PATH))


# Hidden `endless internal` group: debug surfaces that shell through to
# endless-go internals. Hidden so they don't clutter the main help; reachable
# by name for debugging (E-1565).
@main.group("internal", hidden=True)
def internal_cmd():
    """Hidden helpers that shell through to endless-go internals."""
    pass


@internal_cmd.group("template")
def internal_template_cmd():
    """Render templates via the embedded Go renderer."""
    pass


@internal_template_cmd.command("render")
@click.argument("name")
@click.option("--project", default=None,
              help="Registered project name (overrides cwd-based resolution)")
def internal_template_render(name, project):
    """Render <name> from embedded templates; reads JSON vars on stdin.

    Lookup order at render time: <project_root>/.endless/templates/<name>.local.tmpl,
    then <name>.tmpl, then embedded. The committed `.tmpl` is materialized
    from embed on first render so users can customize it on disk.
    """
    import subprocess
    import sys

    from endless.event_bridge import _resolve_endless_go

    binary = _resolve_endless_go()
    args = [binary, "template", "render"]
    if project:
        args.extend(["--project", project])
    args.append(name)
    result = subprocess.run(
        args,
        input=sys.stdin.read(),
        capture_output=True, text=True, check=False,
    )
    sys.stdout.write(result.stdout)
    if result.stderr:
        sys.stderr.write(result.stderr)
    sys.exit(result.returncode)

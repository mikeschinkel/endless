"""Every answer names the store it came from (E-1668).

Endless can read from more than one place. In a self-dev project there are two
databases — the project's main one and each worktree's sandbox — and in any
project there is more than one project row to resolve against. Until this
module, an answer never said which it used, and a wrong one is indistinguishable
from a right one: `endless-go session-query list-live --project-root <main
checkout>` returned 2 rows where main held 59, and the session that ran it
reported the shortfall as a product defect, having also unset the wrong
environment variable in the belief that it was what selected the sandbox.

THE RULE, stated once so the three cases below are a rule and not a list:

    ANNOUNCE WHAT THE INVOCATION RESOLVED, WHEN IT COULD HAVE RESOLVED
    OTHERWISE.

  - The DATABASE, in a self-dev project: ALWAYS. Either database could have been
    right on any invocation, so there is no case where the answer was never in
    doubt. Outside a self-dev project there is one database and nothing to say.
  - The PROJECT: only when the resolved project is not the one enclosing cwd — a
    `--project` flag, a session binding, or a cwd outside any registered project.
    With usually one project in play, always-on is repetition, and repetition
    where nothing varies is how a line stops being read.
  - A context PINNED IN CODE: NEVER. `default_db_to_main` (and the Go side's
    PinMainDB/ForceRealDB) choose for the caller; if the caller could not have
    influenced the choice there is nothing to disambiguate. A pin is not a
    resolution, which makes this the rule rather than an exception to it.

WRITES announce as well as reads. E-1429's founding incident was a write — a
test `endless task add` that landed in the real ledger as E-1425 — and a write
to the wrong database is the damaging case, where a wrong read only misleads.
Nothing here distinguishes the two: both touch the store, both say so.

WHERE IT GOES, and why not stderr. Machine formats carry it as a field in the
payload; human and agent output carry it in band on stdout. This departs from
`rowcap.echo_footer`, which sends its omission trace to stderr for `--json` so
`| jq` will not choke, and the departure is deliberate: that footer is prose
COMMENTARY ABOUT an answer and would corrupt a payload, whereas the store a
result came from is DATA ABOUT THE RESULT and belongs in it.

Ranked by what actually reaches an agent, from the incident's own transcript —
which wrote `2>/dev/null`, `2>&1 | head`, `| tail -5` and `2>&1 | python3 -c`,
each of which discards stderr or buries it under a truncation:

    1. a field in the structure it parses   — near-unmissable; it destructures it
    2. stdout it quotes back                — high; what is in the paste is in the claim
    3. stderr                               — moderate, and hardest to see when the
                                              command SUCCEEDED and returned data
    4. documentation read at session start  — near zero at the moment of error

BOTH ENDS, for an agent. The in-band line is emitted before the output and again
after it, byte for byte identical, on the E-2097 rule `authority.banner` already
follows: whichever end a truncating pipe leaves has to be sufficient alone. A
human gets the trailing copy only — they read top-down, and a header would push
the answer they asked for down the screen.

`--tsv` carries nothing. It is one surface (`endless sql`), its rows are the
columns of arbitrary SQL, and it has no header row — so a provenance column
would silently change the shape of every consumer's `cut -f`/`read` without any
schema to announce itself in.
"""

from pathlib import Path

import click

from endless import config

# The metadata key machine payloads carry. Leading underscore because it is data
# ABOUT the result rather than a column OF it: a consumer destructuring known
# fields is unaffected, and one that iterates can tell the difference.
FIELD = "_answered_from"

# _touched records that this invocation actually reached the store — a direct
# SQLite read (db.get_db) or a Go shellout that opens one (go_db_context_args).
# It is the whole reason a command that never touched a database announces
# nothing: the rule is about what answered a question, and a command that asked
# none has no answer to attribute.
_touched: bool = False

# _machine records that stdout is carrying a payload a program parses, so the
# in-band line must not be written there. Set from two independent directions —
# `attach()` (the site that injects the field) and an argv scan (below) — so a
# machine surface that was never converted to `attach()` still SUPPRESSES the
# line. That asymmetry is the safe one: the cost of a missed `attach()` is a
# missing field, and the cost of a missed suppression is a corrupted payload.
_machine: bool = False

# _agent records the agent-facing output mode, and _project the project this
# invocation resolved. Both are set by the code that knows, not inferred here.
_project: str | None = None

# _head_done guards the leading copy so a command that resolves its project in
# two steps still prints one header, not two.
_head_done: bool = False


def reset() -> None:
    """Clear the per-invocation state.

    Called by `install`, once per invocation. A production process runs one
    command and would not need it — but the test suite runs hundreds through
    CliRunner in a single process, and module state that survived between them
    made one command's resolved project show up on the next one's output. That
    is not a test artifact to work around: state scoped to an invocation should
    be cleared when an invocation begins, and this is where one begins.
    """
    global _touched, _machine, _project, _head_done
    _touched = False
    _machine = False
    _project = None
    _head_done = False


def mark_touched() -> None:
    """Record that this invocation reached the store.

    Called from the two choke points that open one: `db.get_db` for a direct
    SQLite read, and `config.go_db_context_args` for a shellout to a Go binary
    that will. Both already exist and both are unavoidable, which is why the
    mark lives there rather than in each command.
    """
    global _touched
    _touched = True


def mark_machine() -> None:
    """Record that stdout carries a machine payload, suppressing the in-band line."""
    global _machine
    _machine = True


def record_project(name: str | None) -> None:
    """Record the project this invocation resolved.

    Called by the project resolvers. `None` is not "no project" — it is "this
    command did not resolve one", which is why it is ignored rather than stored:
    a later resolver in the same invocation must still be able to speak.
    """
    global _project
    if name:
        _project = name


def _scan_argv_for_machine_format() -> bool:
    """Whether argv asks for a machine format, by the same pre-Click scan
    `DBAwareGroup.main` already uses for `--db`.

    `--json` and `--tsv` are boolean flags everywhere they appear in this CLI,
    so a bare occurrence in argv means the flag was passed. `endless task import
    --json FILE` and `endless session order --json SPEC` take a VALUE — but both
    are inputs to a command whose stdout is not a payload, so treating them as
    machine renders costs a line neither would have printed usefully anyway.
    """
    import sys

    return any(a in ("--json", "--tsv") or a.startswith(("--json=", "--tsv="))
               for a in sys.argv[1:])


def _describe_db() -> tuple[str, str] | None:
    """(short name, resolved dir) for the database, or None when there is
    nothing to say about it.

    Nothing to say in two cases, both of which are the rule and not exceptions:
    a project that is not self-dev has one database, and a context pinned in
    code was not the caller's to influence.

    The name is derived from the dir that was RESOLVED rather than from the flag
    that was typed, so it describes reality: `default_db_to_main` and a test that
    pins a directory get the same treatment as `--db main`.
    """
    if config.PINNED_DB_CONTEXT:
        return None
    if not config.enclosing_project_is_self_dev():
        return None
    resolved = Path(config.CONFIG_DIR)
    if resolved == config.main_config_dir():
        return "main", str(resolved)
    dir_name = config.worktree_dir_name()
    if dir_name and resolved == config.sandbox_config_dir(dir_name):
        return f"sandbox ({dir_name})", str(resolved)
    return config.tilde(resolved), str(resolved)


def _describe_project() -> str | None:
    """The resolved project's name, or None when it is the one enclosing cwd.

    "The one enclosing cwd" is the answer nobody had to ask for, so saying it
    back is the repetition this rule exists to avoid. Everything else — a
    `--project` flag, a session binding, a cwd in no registered project — is a
    resolution that could have gone another way.
    """
    if _project is None:
        return None
    from endless.project_path import project_name_for_cwd

    try:
        here = project_name_for_cwd(config.resolution_cwd())
    except Exception:
        here = None
    return None if here == _project else _project


def fields() -> dict | None:
    """The provenance as a machine payload's value, or None when there is
    nothing to announce.

    The dir accompanies the name because a machine render has room for the
    unambiguous form, and because "sandbox" alone does not say WHICH machine's
    sandbox when the payload travels.
    """
    if not _touched:
        return None
    out: dict[str, str] = {}
    db = _describe_db()
    if db is not None:
        out["db"], out["db_dir"] = db
    project = _describe_project()
    if project is not None:
        out["project"] = project
    return out or None


def attach(payload):
    """Return `payload` carrying its provenance, and mark stdout as machine.

    Per the plan's shape rule: once at top level for an object payload, per ROW
    for an array one. Per-row rather than in a new envelope because an envelope
    is a breaking change to every consumer, while an added key is not — and
    these rows already carry per-row context of exactly this kind (`list-live`'s
    `project_id`).

    A payload that is neither a dict nor a list of dicts (a bare list of scalars,
    a string) has nowhere to put a key, and is returned untouched rather than
    reshaped. Marking machine still happens, so such a render stays uncorrupted.
    """
    mark_machine()
    value = fields()
    if value is None:
        return payload
    if isinstance(payload, dict):
        return {**payload, FIELD: value}
    if isinstance(payload, list):
        return [{**row, FIELD: value} if isinstance(row, dict) else row
                for row in payload]
    return payload


def line(*, llm: bool = False) -> str | None:
    """The in-band trace, or None when there is nothing to announce.

    `#`-prefixed for the agent-facing mode, matching `rowcap.footer`'s idiom so
    an agent meets one comment convention across Endless's output rather than
    one per surface.
    """
    if not _touched:
        return None
    parts = []
    db = _describe_db()
    if db is not None:
        parts.append(f"db: {db[0]}")
    project = _describe_project()
    if project is not None:
        parts.append(f"project: {project}")
    if not parts:
        return None
    body = " · ".join(parts)
    return f"# {body}" if llm else body


def _agent_mode() -> bool:
    """Whether this output is being read by an agent.

    Asked through `agent_help.agent_facing()` rather than re-spelled, for the
    reason that function's own docstring gives: E-1966, E-2006 and E-2097 each
    folded a competing spelling of this question back into one place.
    """
    try:
        from endless import agent_help

        return agent_help.agent_facing()
    except Exception:
        return False


def echo_head() -> None:
    """Emit the leading copy of the trace, at the top of an agent-facing render.

    Paired with the trailing copy `echo_tail` writes at close, byte for byte
    identical — E-2097's rule, which `authority.banner` already follows for the
    same reason: whichever end a truncating pipe leaves has to be sufficient
    alone, and an agent's `| head` and `| tail` are both routine.

    Not gated on `_agent_mode()`, unlike `authority.banner`: the call sites ARE
    the agent-facing renders, so reaching here already answers that question. A
    human's render calls this only under `--llm`, which is a human asking to see
    what an agent sees — the same reason `--agent-view` exists.

    A human's ordinary render gets the trailing copy only. They read top-down,
    and a provenance header would push the answer they asked for down the screen
    for a fact that is almost always the expected one.

    Idempotent: a render that reaches this twice (a per-project loop, a command
    that lists and then details) prints one header, not one per group.
    """
    global _head_done
    if _head_done or _machine:
        return
    text = line(llm=True)
    if text is None:
        return
    _head_done = True
    click.echo(text)


def echo_tail() -> None:
    """Emit the trailing copy of the trace.

    Registered once via `ctx.call_on_close` on the root group, so it fires for
    every command — including one that exits through `ctx.exit()` or a raised
    refusal, since Click closes the context on the way out. A command that never
    reached the store announces nothing, which is what keeps this from becoming
    a line on output that had no database behind it.
    """
    if _machine:
        return
    llm = _agent_mode()
    text = line(llm=llm)
    if text is None:
        return
    click.echo(text if llm else click.style(text, dim=True))


def install(ctx: click.Context) -> None:
    """Wire this invocation up: suppress the line for a machine render, and
    arrange the trailing copy. Called once, from the root group.

    Deliberately NOT `ctx.call_on_close`, which fires on the way out of a
    REFUSAL too. E-2097 guarantees that an agent-facing refusal's first and last
    lines are byte-identical, so that whichever end a truncating pipe leaves
    still carries the verdict; a provenance line appended after the closing copy
    breaks that guarantee — and breaks it on the output where being understood
    matters most. A refusal is not an answer, so it has no store to attribute.

    The root group's result callback is the mechanism that says exactly this: it
    runs on the value a command RETURNED, so it does not run when the command
    raised or exited instead. It fires for nested subcommands too, because each
    group's invoke returns its child's result up to this one.
    """
    reset()
    if _scan_argv_for_machine_format():
        mark_machine()

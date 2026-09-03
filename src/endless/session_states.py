"""Session state vocabulary — a pass-through client for `endless-go session-state`.

This module holds NO vocabulary and NO group list of its own. Every answer comes
from `internal/sessionstate`, which owns the slugs, the labels, the glyphs and
every curated grouping the system reasons about (E-2105). The wrappers below are
one-line shellouts: they know verb names and nothing else — not which groups
exist, not what shape a group's value is, not how a SQL list is quoted. That is
the whole point. Adding a group in Go needs no change here at all, because
``get(<group>)`` already accepts it.

The sibling of `endless.statuses`, deliberately identical in shape. The two
vocabularies answer the same kinds of question, and a reader who has understood
one file should not have to read the second.

Why convert Python NOW, when the all-Go port would delete this file anyway?
Because the next task adds a fifth state. If Python kept its own copy, that
state would be rejected by `session list --state`'s Click choice and would render
as the unknown glyph — the exact missed-site incident this pattern exists to
prevent, in the very task this one was sequenced ahead of.

Why not a JSON payload with everything in it? Because even shipping derived
output, this side would have to know which group keys exist, that ``sql_list``
is a string and ``rank`` is an integer. That is structural knowledge of the
registry living in two places, and it drifts the moment a group is added in Go.

Deliberately light on imports: `config` for binary resolution and `click` for
the error type, but never `db` or `session_cmd` — the CLI layer imports this at
module scope, so anything expensive imported here is paid by every command.

Failure is closed, not silent. If `endless-go` cannot be resolved or is too old
to know `session-state`, there is no fallback to fall back TO — a hardcoded copy
would be the duplicate this module exists to delete. It says so and stops.
"""

import shutil
import subprocess

import click

from endless import config


class SessionStateVocabularyError(click.ClickException):
    """`endless-go session-state` could not answer.

    A ClickException so a call-time failure inside a command renders as a clean
    CLI error rather than a traceback. The import-time call at the bottom of
    this module catches it separately — click's handler is not installed yet
    that early.
    """


# The resolved binary, memoized for the process. Resolution can cost a probe
# (see _resolve_binary), and every wrapper call would otherwise pay it. This
# caches WHICH BINARY to ask, never what it answered — the vocabulary itself is
# always fetched live, which is the property that keeps this a client instead of
# a second registry.
_BINARY: str | None = None


def _knows_session_state(binary: str) -> bool:
    """Whether `binary` is new enough to have the `session-state` subcommand."""
    return subprocess.run(
        [binary, "session-state", "groups"], capture_output=True, text=True,
    ).returncode == 0


def _resolve_binary() -> str:
    """Pick the endless-go to ask.

    Prefers the worktree's own build when running inside a self-dev worktree:
    the worktree's Python is already what executes there, so the worktree's Go
    is its coherent partner. It is also what lets the branch that ADDS
    `session-state` run before it lands.

    But only if that build can actually answer. A worktree branched before
    `session-state` landed has a binary that cannot, and rebuilding it does not
    help — the branch has no `session-state` code to build. Preferring it anyway
    is how E-1891 briefly bricked every `endless` command in every worktree
    older than itself, fatally rather than degraded; the probe is the fix that
    incident bought, inherited here rather than rediscovered. Falling back to
    the PATH-resolved global is always safe: `endless` and `endless-go` ship
    together, so a global new enough to serve the Python asking this question is
    the same install.
    """
    worktree_bin = config.worktree_endless_go()
    if worktree_bin is not None and worktree_bin.is_file():
        if _knows_session_state(str(worktree_bin)):
            return str(worktree_bin)
    found = shutil.which("endless-go")
    if found is None:
        raise SessionStateVocabularyError(
            "endless-go binary not found on PATH, so the session state "
            "vocabulary cannot be read.\n\n"
            "`endless` and `endless-go` ship together — install both with "
            "`just install`."
        )
    return found


def _binary() -> str:
    """The endless-go to ask, resolved once per process."""
    global _BINARY
    if _BINARY is None:
        _BINARY = _resolve_binary()
    return _BINARY


def _run(*args: str, allow_false: bool = False) -> tuple[str, int]:
    """Invoke `endless-go session-state <args>`; return (stdout, exit code).

    Exit 1 is an ANSWER (`has` saying no) and only tolerated when the caller
    asks for it. Everything else non-zero is a real failure — an unknown group
    or state, a bad arity, or an endless-go too old to know the subcommand —
    and is raised with whatever it said on stderr.
    """
    argv = [_binary(), "session-state", *args]
    result = subprocess.run(argv, capture_output=True, text=True)
    if result.returncode == 0 or (allow_false and result.returncode == 1):
        return result.stdout, result.returncode
    detail = result.stderr.strip() or f"exited {result.returncode}"
    raise SessionStateVocabularyError(
        f"could not read the session state vocabulary.\n\n"
        f"    {' '.join(argv)}\n"
        f"    -> {detail}\n\n"
        "If endless-go does not know `session-state`, it predates the session "
        "state registry — rebuild it with `just install`."
    )


def groups() -> tuple[str, ...]:
    """Every group name."""
    stdout, _ = _run("groups")
    return tuple(stdout.split())


def get(group: str) -> tuple[str, ...]:
    """The group's members, in the group's own order."""
    stdout, _ = _run("get", group)
    return tuple(stdout.split())


def has(group: str, state: str) -> bool:
    """Whether `state` is a member of `group`."""
    _, code = _run("has", group, state, allow_false=True)
    return code == 0


def sql_list(group: str) -> str:
    """The group rendered for a SQL IN clause: ``'a','b'``.

    The single highest-value wrapper here. A state list inside a SQL string
    literal is invisible to every tool — nothing searches inside a query — so
    those literals rot longest and most silently.
    """
    stdout, _ = _run("sql-list", group)
    return stdout.strip()


def rank(group: str, state: str) -> int:
    """Index of `state` within an ordered group; -1 when it is not a member."""
    stdout, _ = _run("rank", group, state)
    return int(stdout.strip())


def label(state: str) -> str:
    """The human display string for a state."""
    stdout, _ = _run("label", state)
    return stdout.strip()


def glyph(state: str) -> str:
    """The semantic glyph for a state — no color; each surface maps its own.

    Unlike every other wrapper here, this one accepts a string that is NOT a
    state and answers for it: the registry defines a should-never-happen marker
    (⁇) for exactly that case, so ``glyph("")`` is how this side obtains the
    marker without holding a copy of it. See internal/sessionstate.Glyph.
    """
    stdout, _ = _run("glyph", state)
    return stdout.strip()


def all_states() -> tuple[str, ...]:
    """The whole vocabulary, in lifecycle order.

    A FUNCTION, not a module constant, and the difference is load-bearing
    (E-2105). `endless.statuses` reads its vocabulary at import for
    `click.Choice(TASK_STATUSES)`, which is evaluated as cli.py loads — so every
    `endless` command, whatever it does, cannot start unless `endless-go` can
    answer. That is fine for a subcommand every installed binary already has,
    and fatal for one being introduced: `just land` fast-forwards main's Python
    source into place and rebuilds the global binary only at the END of the
    recipe, so in between, every `endless` invocation from the main checkout
    runs new Python against an old binary. `endless worktree land` — which has
    nothing to say about session state — died there, and no resolver could have
    saved it, because during that window no binary on the machine knows the
    verb yet.

    So nobody pays for this vocabulary who does not use it. See StateChoice for
    the click side.
    """
    return get("all")


class StateChoice(click.ParamType):
    """`click.Choice` over the vocabulary, resolved on USE rather than on
    decoration (E-2105).

    click evaluates a `type=` argument when the decorator runs, i.e. at cli.py
    import. Deferring the lookup to conversion and metavar time is what keeps a
    command that never mentions `--state` from depending on the registry at all
    — see all_states above for why that matters at land time.

    Every method delegates to a real `click.Choice` built on the spot, so the
    metavar, the shell completions and the rejection message are click's own
    rather than a re-implementation that could drift from them.
    """

    name = "choice"

    def _choice(self) -> click.Choice:
        return click.Choice(all_states())

    def convert(self, value, param, ctx):
        return self._choice().convert(value, param, ctx)

    def get_metavar(self, *args, **kwargs):
        # Signature moved between click 8.1 and 8.2 (`param` gained `ctx`);
        # forwarding verbatim keeps this working on either.
        return self._choice().get_metavar(*args, **kwargs)

    def shell_complete(self, ctx, param, incomplete):
        return self._choice().shell_complete(ctx, param, incomplete)

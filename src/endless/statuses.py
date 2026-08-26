"""Task status vocabulary — a pass-through client for `endless-go task-status`.

This module holds NO vocabulary and NO group list of its own. Every answer comes
from `internal/taskstatus`, which owns the slugs, the labels, the glyphs, and
every curated grouping the system reasons about (E-1891). The wrappers below are
one-line shellouts: they know verb names and nothing else — not which groups
exist, not what shape a group's value is, not how a SQL list is quoted. That is
the whole point. Adding a group in Go needs no change here at all, because
``get(<group>)`` already accepts it.

Why not a JSON payload with everything in it? Because even shipping derived
output, this side would have to know which group keys exist, that ``sql_list``
is a string and ``rank`` is a map. That is structural knowledge of the registry
living in two places, and it drifts the moment a group is added in Go.

Why not a Python copy of the vocabulary? Because that is the defect. Status
previously lived as bare literals across ~20 sites in two languages, and twice —
E-1648's `submitted`, E-1845's `untriaged` — a status was added and a site was
missed. Python holding even one authoritative list would leave two places that
must agree.

Deliberately light on imports: `config` for binary resolution and `click` for
the error type, but never `db` or `task_cmd` — the CLI layer imports this at
module scope, and `task_cmd`'s 200ms import is exactly what its lazy
per-command imports exist to avoid.

The Python half is transitional. Endless is being ported to all-Go; when that
lands, this file is deleted outright and the Go registry is simply the source.
Keeping the wrappers trivial is what makes them both cheap to write now and
cheap to delete later. Do not add caching or cleverness here.

Failure is closed, not silent. If `endless-go` cannot be resolved or is too old
to know `task-status`, there is no fallback to fall back TO — a hardcoded copy
would be the duplicate this module exists to delete. It says so and stops.
"""

import shutil
import subprocess

import click

from endless import config


class StatusVocabularyError(click.ClickException):
    """`endless-go task-status` could not answer.

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


def _knows_task_status(binary: str) -> bool:
    """Whether `binary` is new enough to have the `task-status` subcommand."""
    return subprocess.run(
        [binary, "task-status", "groups"], capture_output=True, text=True,
    ).returncode == 0


def _resolve_binary() -> str:
    """Pick the endless-go to ask.

    Prefers the worktree's own build when running inside a self-dev worktree:
    the worktree's Python is already what executes there, so the worktree's Go
    is its coherent partner. It is also what lets the branch that ADDS
    `task-status` run before it lands.

    But only if that build can actually answer. A worktree branched before
    `task-status` landed has a binary that cannot, and rebuilding it does not
    help — the branch has no `task-status` code to build. Preferring it anyway
    is how E-1891 briefly bricked every `endless` command in every worktree
    older than itself, fatally rather than degraded. Falling back to the
    PATH-resolved global is always safe here: `endless` and `endless-go` ship
    together, so a global new enough to serve the Python asking this question is
    the same install.

    The probe costs one extra spawn, and only inside a self-dev worktree —
    outside one there is no worktree candidate to test. That is the right place
    for the cost to land.

    No --db gate, unlike event_bridge's resolver: this opens no database, so
    there is no schema baseline to mismatch.
    """
    worktree_bin = config.worktree_endless_go()
    if worktree_bin is not None and worktree_bin.is_file():
        if _knows_task_status(str(worktree_bin)):
            return str(worktree_bin)
    found = shutil.which("endless-go")
    if found is None:
        raise StatusVocabularyError(
            "endless-go binary not found on PATH, so the task status "
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
    """Invoke `endless-go task-status <args>`; return (stdout, exit code).

    Exit 1 is an ANSWER (`has` saying no) and only tolerated when the caller
    asks for it. Everything else non-zero is a real failure — an unknown group
    or status, a bad arity, or an endless-go too old to know the subcommand —
    and is raised with whatever it said on stderr.
    """
    argv = [_binary(), "task-status", *args]
    result = subprocess.run(argv, capture_output=True, text=True)
    if result.returncode == 0 or (allow_false and result.returncode == 1):
        return result.stdout, result.returncode
    detail = result.stderr.strip() or f"exited {result.returncode}"
    raise StatusVocabularyError(
        f"could not read the task status vocabulary.\n\n"
        f"    {' '.join(argv)}\n"
        f"    -> {detail}\n\n"
        "If endless-go does not know `task-status`, it predates the status "
        "registry — rebuild it with `just install`."
    )


def groups() -> tuple[str, ...]:
    """Every group name."""
    stdout, _ = _run("groups")
    return tuple(stdout.split())


def get(group: str) -> tuple[str, ...]:
    """The group's members, in the group's own order."""
    stdout, _ = _run("get", group)
    return tuple(stdout.split())


def has(group: str, status: str) -> bool:
    """Whether `status` is a member of `group`."""
    _, code = _run("has", group, status, allow_false=True)
    return code == 0


def sql_list(group: str) -> str:
    """The group rendered for a SQL IN / NOT IN clause: ``'a','b'``.

    The single highest-value wrapper here. A status list inside a SQL string
    literal is invisible to every tool — nothing searches inside a query — so
    those literals rot longest and most silently.
    """
    stdout, _ = _run("sql-list", group)
    return stdout.strip()


def rank(group: str, status: str) -> int:
    """Index of `status` within an ordered group; -1 when it is not a member."""
    stdout, _ = _run("rank", group, status)
    return int(stdout.strip())


def label(status: str) -> str:
    """The human display string for a status."""
    stdout, _ = _run("label", status)
    return stdout.strip()


def glyph(status: str) -> str:
    """The semantic glyph for a status — no color; each surface maps its own."""
    stdout, _ = _run("glyph", status)
    return stdout.strip()


def lifecycle() -> str:
    """The generated mermaid body of docs/status-lifecycle.mmd (E-2018).

    Returned verbatim, newline for newline — this is an artifact whose BYTES are
    compared, so `.strip()` here would make every drift check fail on whitespace
    the renderer never emitted. `endless.lifecycle_map` writes it into the
    canonical file and its two embedded copies.
    """
    stdout, _ = _run("lifecycle")
    return stdout


# The whole vocabulary, in lifecycle order. Read once at import because the
# click decorators in cli.py consume it at decoration time — `click.Choice(...)`
# is evaluated as the module loads, long before any argument is parsed.
#
# That is also why the failure below is handled here rather than raised: click's
# exception handler is not installed during import, so an escaping
# ClickException would print a traceback. This prints the message and stops.
try:
    TASK_STATUSES = get("all")
except StatusVocabularyError as exc:
    raise SystemExit(f"endless: {exc.format_message()}") from exc

# The shared `--status` help string. Derived, never typed: a status added in Go
# shows up in every `--help` for free, which is the failure E-1956 fixed.
TASK_STATUS_HELP = "Status: " + ", ".join(TASK_STATUSES)

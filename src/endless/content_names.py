"""Task content-name vocabulary — a pass-through client for `endless-go task-content`.

A task's typed prose (plan, analysis, outcome, reason, notes) lives in the
task_content table, one row per name (E-1531). The names are the
`taskcontent.Name` Go enum in `internal/taskcontent`: its slug is the stored
token, the CLI flag and the mirror file stem, and its label is the heading a
section renders under. This module holds no copy of either — it asks, for the
reason `statuses` gives: a Python list beside the Go one is two places that must
agree, and the whole point of the row store is that adding a name is one edit.

Read once per process, at first use. Unlike the status vocabulary this is not
consumed at click decoration time, so nothing forces an import-time call; and a
process cannot outlive a vocabulary change, because the binary it asks ships
with it.

Failure is closed, as in `statuses`: with no endless-go to ask there is no list
to fall back to, and inventing one would be the duplicate this module exists to
avoid.
"""

import shutil
import subprocess
from typing import NamedTuple

import click

from endless import config


class ContentNameError(click.ClickException):
    """`endless-go task-content` could not answer."""


class ContentName(NamedTuple):
    """One content kind: the stored token and its display label."""

    slug: str
    label: str


_NAMES: tuple[ContentName, ...] | None = None


def _answers(binary: str) -> subprocess.CompletedProcess | None:
    """Ask `binary` for the vocabulary; None when it cannot answer."""
    try:
        result = subprocess.run(
            [binary, "task-content", "names"], capture_output=True, text=True,
        )
    except OSError:
        return None
    return result if result.returncode == 0 else None


def _fetch() -> tuple[ContentName, ...]:
    """Read the vocabulary from the worktree's endless-go, else the global one.

    The worktree's build first, for the reason `statuses._resolve_binary` gives —
    it is the Python running here's coherent partner — but only if it can
    answer: a worktree branched before `task-content` existed has a binary that
    cannot, and the PATH-resolved global ships with the Python asking.
    """
    candidates = []
    worktree_bin = config.worktree_endless_go()
    if worktree_bin is not None and worktree_bin.is_file():
        candidates.append(str(worktree_bin))
    found = shutil.which("endless-go")
    if found is not None:
        candidates.append(found)
    for binary in candidates:
        result = _answers(binary)
        if result is None:
            continue
        names = []
        for line in result.stdout.splitlines():
            slug, _, label = line.partition("\t")
            if slug:
                names.append(ContentName(slug, label or slug))
        return tuple(names)
    raise ContentNameError(
        "could not read the task content vocabulary: no endless-go that knows "
        "`task-content` was found.\n\n"
        "`endless` and `endless-go` ship together — rebuild both with "
        "`just install`."
    )


def names() -> tuple[ContentName, ...]:
    """Every content kind, in display order."""
    global _NAMES
    if _NAMES is None:
        _NAMES = _fetch()
    return _NAMES


def slugs() -> tuple[str, ...]:
    """Every stored token, in display order."""
    return tuple(n.slug for n in names())


def label(slug: str) -> str:
    """The heading a content kind renders under ("Plan")."""
    for n in names():
        if n.slug == slug:
            return n.label
    return slug.capitalize()

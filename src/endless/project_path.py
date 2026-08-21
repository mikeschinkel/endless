"""Canonical spelling for project paths — the two forms, and the one rule.

The Python half of a rule the Go side implements identically in
`monitor.StoredProjectPath` / `monitor.ResolvedProjectPath` /
`monitor.MatchProjectPath` (`internal/monitor/project_path.go`). The two halves
MUST agree: this CLI writes `projects.path`, the Go hook reads it on every
Claude event, and a disagreement makes the hook miss the registered row and
auto-register a second project for the same directory — leaving the session
bound to an empty duplicate that reports "no tasks yet" for a project full of
them (E-2002).

A project path has TWO forms and they must never be confused (ED-1562):

- the **stored** form — home-relative, `~/Projects/acme`, absolute only for a
  directory outside `$HOME`. This is what `projects.path` holds and what
  comparisons run in. `stored()` returns a `str`, never a `Path`, because it is
  not a filesystem path: `Path("~/Projects/acme")` is a two-component relative
  path whose first component is named `~`, and every `open`, `exists` and
  `subprocess(cwd=...)` on it is quietly wrong.
- the **resolved** form — absolute, every symlink component resolved. This is
  what touches disk, and `resolved()` returns a `Path` to say so.

The type is the signal: hold a `Path` and you may use it; hold a `str` from
this module and you may only compare or store it.

Home-relative since E-2011, for legibility — `endless sql` is a supported
surface, and an ad-hoc query over the database reads better with
`~/Projects/acme` than with a column of identical 20-character prefixes. The
byte saving is not the reason; it is under a kilobyte.

Normalization happens at the BOUNDARIES — on write, and on the read that hands
a path to the filesystem — not per comparison, so a later contributor cannot
get it wrong by omission. `match_project_path` is the one place that also
compares in resolved form, as a fallback for rows written in an older
spelling: E-2002's and E-2011's change scripts
(`internal/schema/changes/e-*-project-paths.go`, whose logic is
`monitor.RepairProjectPaths`) rewrite those, so the fallback is for rows
written by hand, restored from an older backup, or living in a DB that has not
run the changes yet.
"""

import os
from pathlib import Path

import click

from endless import config, db


def home() -> Path:
    """`$HOME`, absolute and symlink-resolved.

    Read from the environment rather than via `Path.home()` so the two halves
    agree exactly: Go's `os.UserHomeDir()` is `$HOME` on Unix and fails when it
    is empty, while `os.path.expanduser` would silently fall back to the `pwd`
    database and could answer differently.

    Resolved, because the paths it is compared against are: a `$HOME` reached
    through a symlink would never prefix-match a resolved project path, and
    every project would silently store absolute.

    Raises rather than guessing (E-2011). Both forms need this value — one to
    expand a stored tilde, the other to decide whether to write one — and a
    silent wrong answer is `<cwd>/~/Projects/acme`, or a second spelling of a
    directory that already has a row.
    """
    value = os.environ.get("HOME", "")
    if not value:
        raise click.ClickException(
            "Cannot determine the home directory: $HOME is unset. "
            "Endless stores project paths relative to it."
        )
    return Path(value).resolve()


def resolved(path: Path | str) -> Path:
    """The RESOLVED form: absolute, symlinks resolved, leading `~` expanded.
    Use for anything that touches the filesystem. Mirrors
    monitor.ResolvedProjectPath on the Go side.

    `Path.resolve()` is exactly the absolute-and-symlink-resolved part, and is
    non-strict — a directory that does not exist yet still gets its existing
    prefix resolved. The Go half reproduces the non-strict part by hand,
    because `filepath.EvalSymlinks` fails outright on a missing leaf.

    The tilde is expanded here rather than by `Path.expanduser()` so that an
    unset `$HOME` raises instead of falling back; `Path("~/x").resolve()`
    without it treats the tilde as an ordinary directory name and silently
    returns `<cwd>/~/x`.
    """
    text = str(path)
    if text == "~":
        return home()
    if text.startswith("~/"):
        return (home() / text[2:]).resolve()
    return Path(text).resolve()


def stored(path: Path | str) -> str:
    """The STORED form: the resolved form rewritten home-relative with a `~/`
    prefix, or left absolute when the directory is not under `$HOME`. Use for
    every write to `projects.path` and every comparison against it — and for
    nothing else, which is why this returns `str` and not `Path`. Mirrors
    monitor.StoredProjectPath on the Go side.

    Symlinks are resolved first: relativizing a raw path would make the stored
    spelling depend on how the caller's shell spelled it, which is exactly what
    E-2002 closed.
    """
    target = resolved(path)
    root = home()
    if target == root:
        return "~"
    try:
        return "~/" + str(target.relative_to(root))
    except ValueError:
        return str(target)


def match_project_path(path: Path | str) -> str | None:
    """The `projects.path` AS STORED for the row denoting `path`, else None.

    Exact match on the stored form first — the indexed fast path every
    canonically-written row takes — then a comparison in resolved form across
    the table, which sees through any older spelling. Ordered by id so the pick
    is deterministic when two rows denote the same directory: the older row
    wins, which for the bug that motivated this is the genuine registration
    rather than the auto-registered duplicate.
    """
    rows = db.query(
        "SELECT path FROM projects WHERE path = ?", (stored(path),)
    )
    if rows:
        return rows[0]["path"]
    target = resolved(path)
    for row in db.query("SELECT path FROM projects ORDER BY id"):
        if resolved(row["path"]) == target:
            return row["path"]
    return None


def project_name_for_cwd(cwd: Path | str) -> str | None:
    """The project name for a working directory, or None if it is in no
    registered project.

    Order matters and is preserved from the six hand-rolled copies this
    replaced: the on-disk `<cwd>/.endless/config.json` is consulted first (it
    is the project's own declaration and needs no DB), and only then the
    registered row whose path denotes cwd.
    """
    pcfg = config.project_config_read(Path(cwd))
    if pcfg and pcfg.get("name"):
        return pcfg["name"]
    stored_path = match_project_path(cwd)
    if stored_path is None:
        return None
    rows = db.query(
        "SELECT name FROM projects WHERE path = ?", (stored_path,)
    )
    return rows[0]["name"] if rows else None

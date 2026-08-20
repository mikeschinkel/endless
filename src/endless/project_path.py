"""Canonical normalization for project paths.

The Python half of a rule the Go side implements identically in
`monitor.NormalizeProjectPath` / `monitor.MatchProjectPath`
(`internal/monitor/project_path.go`). The two halves MUST agree: this CLI
writes `projects.path`, the Go hook reads it on every Claude event, and a
disagreement makes the hook miss the registered row and auto-register a second
project for the same directory — leaving the session bound to an empty
duplicate that reports "no tasks yet" for a project full of them (E-2002).

The rule: a project path is stored and compared absolute, `~` expanded, and
with every symlink component resolved. `Path.resolve()` is exactly that, and is
non-strict — a directory that does not exist yet still gets its existing prefix
resolved. The Go half reproduces the non-strict part by hand, because
`filepath.EvalSymlinks` fails outright on a missing leaf.

Normalization is applied at the COMPARISON boundary as well as on write:
`match_project_path` falls back to comparing normalized stored paths when the
indexed exact match misses, so a row in the old spelling is still found.
Existing rows were rewritten by E-2002's change script
(`internal/schema/changes/e-2002-normalize-project-paths.go`, whose logic is
`monitor.RepairProjectPaths`), which also merged the duplicate projects the bug
created; the fallback remains for rows written by hand, restored from an older
backup, or living in a DB that has not run the change yet.
"""

from pathlib import Path

from endless import config, db


def normalize(path: Path | str) -> Path:
    """The canonical form of a project path: absolute, ~ expanded, symlinks
    resolved. Mirrors monitor.NormalizeProjectPath on the Go side."""
    return Path(path).expanduser().resolve()


def match_project_path(path: Path | str) -> str | None:
    """The `projects.path` AS STORED for the row denoting `path`, else None.

    Exact match first — the indexed fast path every canonically-stored row
    takes — then a normalized comparison across the table for rows that predate
    this rule. Ordered by id so the pick is deterministic when two rows
    normalize to the same directory: the older row wins, which for the bug that
    motivated this is the genuine registration rather than the auto-registered
    duplicate.
    """
    target = normalize(path)
    rows = db.query(
        "SELECT path FROM projects WHERE path = ?", (str(target),)
    )
    if rows:
        return rows[0]["path"]
    for row in db.query("SELECT path FROM projects ORDER BY id"):
        if normalize(row["path"]) == target:
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
    stored = match_project_path(cwd)
    if stored is None:
        return None
    rows = db.query(
        "SELECT name FROM projects WHERE path = ?", (stored,)
    )
    return rows[0]["name"] if rows else None

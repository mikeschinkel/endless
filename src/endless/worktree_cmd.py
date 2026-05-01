"""Read-only inspection CLI for git worktrees managed by endless.

Mutation operations (land, drop, auto-create on session/task start) come
in subsequent E-971 layers. This module focuses on inspection:

- list: enumerate all git worktrees of the current project, classified
- current: report the worktree for cwd
- show: detail for one worktree
- for-task: resolve a task ID to its worktree path

Worktree state is filesystem-authoritative (per E-971 design): no DB
tables. Each endless-managed worktree has a companion JSON file at
<worktree-root>/.endless/worktree.json with task_id, base_branch,
branch, created_at. Worktrees without the companion are 'foreign'
(created by another tool or by hand) and are listed but never mutated.

Lifecycle states (derived):
- active: git knows about it AND endless companion is present
- foreign: git knows about it AND no companion
- merged: branch is in `git branch --merged <base>`
- abandoned: heuristic — unmerged, no live session bound
"""

import json
import re
import subprocess
from pathlib import Path

import click

from endless.task_cmd import _resolve_project


COMPANION_FILENAME = ".endless/worktree.json"


def _project_root() -> Path:
    """Return the registered project root path for cwd's project."""
    project_id, _ = _resolve_project(None)
    from endless import db
    row = db.query("SELECT path FROM projects WHERE id = ? LIMIT 1", (project_id,))
    if not row:
        raise click.ClickException(f"Project id {project_id} has no registered path")
    return Path(row[0]["path"]).expanduser().resolve()


def _git(args: list[str], cwd: Path) -> str:
    """Run a git command in cwd; return trimmed stdout. Raises on non-zero exit."""
    res = subprocess.run(
        ["git", *args],
        capture_output=True, text=True, check=True, cwd=str(cwd),
    )
    return res.stdout.rstrip("\n")


def _parse_worktree_porcelain(out: str) -> list[dict]:
    """Parse `git worktree list --porcelain`. Returns list of dicts.

    Stanzas are blank-line separated. Keys: worktree, HEAD, branch,
    bare, detached, locked, prunable.
    """
    worktrees: list[dict] = []
    cur: dict | None = None
    for raw in out.splitlines():
        line = raw.rstrip("\n")
        if not line:
            if cur is not None:
                worktrees.append(cur)
                cur = None
            continue
        if cur is None:
            cur = {}
        if line.startswith("worktree "):
            cur["path"] = line[len("worktree "):]
        elif line.startswith("HEAD "):
            cur["head"] = line[len("HEAD "):]
        elif line.startswith("branch "):
            cur["branch"] = line[len("branch "):]
        elif line == "detached":
            cur["detached"] = True
        elif line == "bare":
            cur["bare"] = True
        elif line == "locked":
            cur["locked"] = True
        elif line.startswith("locked "):
            cur["locked"] = True
            cur["lock_reason"] = line[len("locked "):]
        elif line == "prunable":
            cur["prunable"] = True
        elif line.startswith("prunable "):
            cur["prunable"] = True
            cur["prunable_reason"] = line[len("prunable "):]
    if cur is not None:
        worktrees.append(cur)
    return worktrees


def _read_companion(worktree_path: Path) -> dict | None:
    """Read <worktree-root>/.endless/worktree.json. Returns dict or None."""
    companion = worktree_path / COMPANION_FILENAME
    if not companion.exists():
        return None
    try:
        return json.loads(companion.read_text())
    except (OSError, json.JSONDecodeError):
        return None


def _short_branch(ref: str | None) -> str:
    """Convert 'refs/heads/foo' to 'foo'. Pass-through if not a branch ref."""
    if not ref:
        return ""
    return ref.removeprefix("refs/heads/")


def _classify(wt: dict, companion: dict | None, root: Path) -> str:
    """Return the lifecycle state label for a worktree row.

    For now: 'main' for the main checkout, 'active' if endless-managed,
    'foreign' otherwise. 'merged'/'abandoned' classification is deferred
    to a future layer (requires base-branch lookup).
    """
    if Path(wt.get("path", "")).resolve() == root:
        return "main"
    if companion is not None:
        return "active"
    return "foreign"


def _enriched_list(project_root: Path) -> list[dict]:
    """Run `git worktree list --porcelain` and merge with companion metadata."""
    out = _git(["worktree", "list", "--porcelain"], cwd=project_root)
    rows = _parse_worktree_porcelain(out)
    enriched = []
    for wt in rows:
        path = Path(wt.get("path", ""))
        companion = _read_companion(path) if path.exists() else None
        enriched.append({
            "path": str(path),
            "branch": _short_branch(wt.get("branch")),
            "branch_ref": wt.get("branch", ""),
            "head": wt.get("head", ""),
            "detached": wt.get("detached", False),
            "bare": wt.get("bare", False),
            "locked": wt.get("locked", False),
            "prunable": wt.get("prunable", False),
            "lock_reason": wt.get("lock_reason"),
            "prunable_reason": wt.get("prunable_reason"),
            "state": _classify(wt, companion, project_root),
            "companion": companion,
        })
    return enriched


# --- CLI command implementations -------------------------------------------

def list_worktrees(state_filter: str | None, as_json: bool) -> None:
    """List worktrees for the current project."""
    root = _project_root()
    rows = _enriched_list(root)
    if state_filter:
        rows = [r for r in rows if r["state"] == state_filter]

    if as_json:
        click.echo(json.dumps(rows, indent=2))
        return

    if not rows:
        click.echo("No worktrees match.")
        return

    click.echo(f"{'State':<8}  {'Branch':<40}  {'Task':<8}  Path")
    click.echo("-" * 8 + "  " + "-" * 40 + "  " + "-" * 8 + "  " + "-" * 40)
    for r in rows:
        task = ""
        if r["companion"]:
            task = r["companion"].get("task_id", "")
        branch = r["branch"] or ("(detached)" if r["detached"] else "")
        if len(branch) > 40:
            branch = branch[:39] + "…"
        path = r["path"]
        if len(path) > 60:
            path = "…" + path[-59:]
        click.echo(f"{r['state']:<8}  {branch:<40}  {task:<8}  {path}")


def current_worktree(as_json: bool) -> None:
    """Show the worktree for the current cwd."""
    cwd = Path.cwd().resolve()
    try:
        toplevel_str = _git(["rev-parse", "--show-toplevel"], cwd=cwd)
    except subprocess.CalledProcessError:
        raise click.ClickException("Not inside a git repository")
    toplevel = Path(toplevel_str).resolve()

    root = _project_root()
    rows = _enriched_list(root)
    match = next((r for r in rows if Path(r["path"]).resolve() == toplevel), None)
    if match is None:
        raise click.ClickException(
            f"cwd {cwd} resolves to a working tree {toplevel} that "
            f"git worktree list does not report. Inconsistent state."
        )

    if as_json:
        click.echo(json.dumps(match, indent=2))
        return

    click.echo(f"State:   {match['state']}")
    click.echo(f"Path:    {match['path']}")
    click.echo(f"Branch:  {match['branch'] or '(detached)'}")
    click.echo(f"HEAD:    {match['head']}")
    if match["companion"]:
        sc = match["companion"]
        if "task_id" in sc:
            click.echo(f"Task:    {sc['task_id']}")
        if "base_branch" in sc:
            click.echo(f"Base:    {sc['base_branch']}")
        if "created_at" in sc:
            click.echo(f"Created: {sc['created_at']}")
    if match["locked"]:
        click.echo(f"Locked:  {match.get('lock_reason') or 'yes'}")
    if match["prunable"]:
        click.echo(f"Prunable: {match.get('prunable_reason') or 'yes'}")


def show_worktree(name_or_path: str, as_json: bool) -> None:
    """Show detail for one worktree, identified by trailing path segment or full path."""
    root = _project_root()
    rows = _enriched_list(root)

    target = None
    candidate = Path(name_or_path)
    if candidate.is_absolute():
        target_path = candidate.resolve()
        target = next((r for r in rows if Path(r["path"]).resolve() == target_path), None)
    if target is None:
        # Match by trailing path segment (e.g. 'e-967' matches '.endless/worktrees/e-967')
        for r in rows:
            if Path(r["path"]).name == name_or_path:
                target = r
                break

    if target is None:
        raise click.ClickException(f"No worktree matches: {name_or_path}")

    if as_json:
        click.echo(json.dumps(target, indent=2))
        return

    click.echo(f"State:   {target['state']}")
    click.echo(f"Path:    {target['path']}")
    click.echo(f"Branch:  {target['branch'] or '(detached)'}")
    click.echo(f"HEAD:    {target['head']}")
    if target["companion"]:
        sc = target["companion"]
        click.echo(f"--- companion ---")
        click.echo(json.dumps(sc, indent=2))
    if target["locked"]:
        click.echo(f"Locked:  {target.get('lock_reason') or 'yes'}")
    if target["prunable"]:
        click.echo(f"Prunable: {target.get('prunable_reason') or 'yes'}")


def for_task(task_id: str, as_json: bool) -> None:
    """Resolve a task ID (e.g. E-967 or 967) to its worktree path."""
    m = re.fullmatch(r"(?:[Ee]-)?(\d+)", task_id.strip())
    if m is None:
        raise click.ClickException(f"Invalid task id: {task_id}")
    canonical = f"E-{m.group(1)}"

    root = _project_root()
    rows = _enriched_list(root)
    match = next(
        (r for r in rows if r["companion"] and r["companion"].get("task_id") == canonical),
        None,
    )

    if match is None:
        if as_json:
            click.echo(json.dumps({"task_id": canonical, "worktree": None}))
        else:
            click.echo(f"No endless-managed worktree for {canonical}.")
        return

    if as_json:
        click.echo(json.dumps({
            "task_id": canonical,
            "worktree": match["path"],
            "branch": match["branch"],
            "head": match["head"],
        }))
    else:
        click.echo(match["path"])

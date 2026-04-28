"""Plan snapshot inspection and management.

Snapshots are written by the endless-hook Go binary on every plan-file Write
captured by Claude's PostToolUse event. Each snapshot is two files in
<project>/.endless/plans/snapshots/:

  <YYYYMMDDTHHMMSS>-<sha8>.md      content
  <YYYYMMDDTHHMMSS>-<sha8>.json    sidecar { session_id, written_at, source_path, sha256 }

This module provides read-only inspection. Pruning is a separate task (E-984).
"""

import json
from datetime import date, datetime
from pathlib import Path

import click

from endless import db
from endless.task_cmd import _resolve_project


def _snapshots_dir(project_path: str) -> Path:
    return Path(project_path).expanduser() / ".endless" / "plans" / "snapshots"


def _project_path() -> str:
    """Return the registered path of the current project (cwd-resolved)."""
    project_id, _ = _resolve_project(None)
    row = db.query("SELECT path FROM projects WHERE id = ?", (project_id,))
    if not row:
        raise click.ClickException(f"Project id {project_id} has no path")
    return row[0]["path"]


def _read_sidecar(md_path: Path) -> dict:
    sidecar = md_path.with_suffix(".json")
    if not sidecar.exists():
        return {}
    try:
        return json.loads(sidecar.read_text())
    except (json.JSONDecodeError, OSError):
        return {}


def _first_line(md_path: Path, max_chars: int = 80) -> str:
    try:
        with md_path.open("r") as f:
            line = f.readline().rstrip("\n").strip()
    except OSError:
        return ""
    if len(line) > max_chars:
        return line[: max_chars - 1] + "…"
    return line


def list_snapshots(
    session_id: str | None = None,
    today_only: bool = False,
    as_json: bool = False,
) -> None:
    """List plan snapshots in the current project's snapshots dir."""
    snaps_dir = _snapshots_dir(_project_path())
    if not snaps_dir.exists():
        if as_json:
            click.echo("[]")
        else:
            click.echo(f"No snapshots directory at {snaps_dir}")
        return

    today_str = date.today().strftime("%Y%m%d")

    rows = []
    for md in sorted(snaps_dir.glob("*.md")):
        snap_id = md.stem  # <ts>-<sha8>
        sidecar = _read_sidecar(md)

        if session_id is not None:
            if sidecar.get("session_id") != session_id:
                continue

        if today_only:
            if not snap_id.startswith(today_str):
                continue

        rows.append({
            "id": snap_id,
            "session_id": sidecar.get("session_id", ""),
            "written_at": sidecar.get("written_at", ""),
            "source_path": sidecar.get("source_path", ""),
            "sha256": sidecar.get("sha256", ""),
            "first_line": _first_line(md),
            "size_bytes": md.stat().st_size,
        })

    if as_json:
        click.echo(json.dumps(rows, indent=2))
        return

    if not rows:
        click.echo("No snapshots match.")
        return

    # Tabular output
    click.echo(f"{'ID':<24}  {'Session':<16}  {'First line':<60}")
    click.echo("-" * 24 + "  " + "-" * 16 + "  " + "-" * 60)
    for r in rows:
        sid = r["session_id"][:16] if r["session_id"] else "-"
        line = r["first_line"][:60] if r["first_line"] else "(empty)"
        click.echo(f"{r['id']:<24}  {sid:<16}  {line:<60}")


def show_snapshot(snap_id: str, as_json: bool = False) -> None:
    """Show full detail for a single snapshot."""
    snaps_dir = _snapshots_dir(_project_path())
    md = snaps_dir / f"{snap_id}.md"
    if not md.exists():
        raise click.ClickException(f"Snapshot not found: {snap_id}")

    sidecar = _read_sidecar(md)
    content = md.read_text()

    if as_json:
        out = {
            "id": snap_id,
            "metadata": sidecar,
            "content": content,
            "size_bytes": md.stat().st_size,
        }
        click.echo(json.dumps(out, indent=2))
        return

    click.echo(f"Snapshot: {snap_id}")
    click.echo(f"Session:  {sidecar.get('session_id', '-')}")
    click.echo(f"Written:  {sidecar.get('written_at', '-')}")
    click.echo(f"Source:   {sidecar.get('source_path', '-')}")
    click.echo(f"SHA-256:  {sidecar.get('sha256', '-')}")
    click.echo(f"Bytes:    {md.stat().st_size}")
    click.echo("---")
    click.echo(content)

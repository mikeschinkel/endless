"""Scan command logic — index documents and detect staleness."""

import hashlib
import json
from datetime import datetime, timezone
from pathlib import Path

import click

from endless import db, config
from endless.doc_types import classify_doc, SINGLETON_TYPES

SKIP_DIRS = {".git", "vendor", "node_modules", ".endless"}



def hash_file(path: Path) -> str:
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(8192), b""):
            h.update(chunk)
    return h.hexdigest()


def scan_documents(project_id: int, project_path: Path) -> list[str]:
    """Scan for .md files, return list of changed relative paths."""
    now = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%S")
    changed = []
    doc_count = 0

    for md_file in project_path.rglob("*.md"):
        # Skip excluded directories
        parts = md_file.relative_to(project_path).parts
        if any(p in SKIP_DIRS for p in parts):
            continue
        # Skip archived docs
        if ".endless" in parts and "archive" in parts:
            continue

        rel_path = str(md_file.relative_to(project_path))
        doc_type = classify_doc(rel_path)
        content_hash = hash_file(md_file)
        stat = md_file.stat()
        size_bytes = stat.st_size
        last_modified = datetime.fromtimestamp(
            stat.st_mtime, tz=timezone.utc
        ).strftime("%Y-%m-%dT%H:%M:%S")

        doc_count += 1

        # Check existing
        existing = db.query(
            "SELECT content_hash FROM documents "
            "WHERE project_id = ? AND relative_path = ?",
            (project_id, rel_path),
        )

        if not existing:
            db.execute(
                "INSERT INTO documents "
                "(project_id, relative_path, doc_type, content_hash, "
                "size_bytes, last_modified, last_scanned) "
                "VALUES (?, ?, ?, ?, ?, ?, ?)",
                (project_id, rel_path, doc_type, content_hash,
                 size_bytes, last_modified, now),
            )
            changed.append(rel_path)
        elif existing[0]["content_hash"] != content_hash:
            db.execute(
                "UPDATE documents SET doc_type=?, content_hash=?, "
                "size_bytes=?, last_modified=?, last_scanned=? "
                "WHERE project_id=? AND relative_path=?",
                (doc_type, content_hash, size_bytes,
                 last_modified, now, project_id, rel_path),
            )
            changed.append(rel_path)
        else:
            db.execute(
                "UPDATE documents SET last_scanned=? "
                "WHERE project_id=? AND relative_path=?",
                (now, project_id, rel_path),
            )

    click.echo(f"    {doc_count} document(s), {len(changed)} changed")
    return changed


def check_dependency_rules(
    project_id: int, project_path: Path, changed: list[str]
):
    cfg = config.project_config_read(project_path)
    if not cfg:
        return
    rules = cfg.get("documents", {}).get("rules", [])
    if not rules or not changed:
        return

    now = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%S")

    for rule in rules:
        dependent = rule.get("dependent", "")
        depends_on = rule.get("depends_on", [])

        for dep_target in depends_on:
            matched = dep_target in changed
            if not matched and "*" in dep_target:
                # Glob pattern — check if any changed file matches
                import fnmatch
                matched = any(
                    fnmatch.fnmatch(c, dep_target) for c in changed
                )

            if matched:
                # Check for existing unresolved note
                existing = db.scalar(
                    "SELECT id FROM notes "
                    "WHERE project_id=? AND target_doc=? "
                    "AND source=? AND resolved=0",
                    (project_id, dependent, dep_target),
                )
                if existing is None:
                    msg = (
                        f"{dependent} may need updating "
                        f"because {dep_target} changed"
                    )
                    db.execute(
                        "INSERT INTO notes "
                        "(project_id, note_type, message, "
                        "source, target_doc, created_at) "
                        "VALUES (?, 'staleness', ?, ?, ?, ?)",
                        (project_id, msg, dep_target,
                         dependent, now),
                    )
                    click.echo(
                        "    "
                        + click.style("note:", fg="yellow")
                        + f" {msg}"
                    )
                else:
                    db.execute(
                        "UPDATE notes SET created_at=? WHERE id=?",
                        (now, existing),
                    )


def check_sprawl(project_id: int):
    for doc_type in SINGLETON_TYPES:
        count = db.scalar(
            "SELECT count(*) FROM documents "
            "WHERE project_id=? AND doc_type=? AND is_archived=0",
            (project_id, doc_type),
        )
        if count and count > 1:
            files = db.query(
                "SELECT relative_path FROM documents "
                "WHERE project_id=? AND doc_type=? AND is_archived=0",
                (project_id, doc_type),
            )
            file_list = ", ".join(r["relative_path"] for r in files)

            existing = db.scalar(
                "SELECT id FROM notes "
                "WHERE project_id=? AND note_type='sprawl' "
                "AND source=? AND resolved=0",
                (project_id, doc_type),
            )
            if existing is None:
                now = datetime.now(timezone.utc).strftime(
                    "%Y-%m-%dT%H:%M:%S"
                )
                msg = (
                    f"Multiple {doc_type} files detected: "
                    f"{file_list}. Consider consolidating."
                )
                db.execute(
                    "INSERT INTO notes "
                    "(project_id, note_type, message, "
                    "source, created_at) "
                    "VALUES (?, 'sprawl', ?, ?, ?)",
                    (project_id, msg, doc_type, now),
                )
                click.echo(
                    "    "
                    + click.style("sprawl:", fg="yellow")
                    + f" {msg}"
                )


def scan_project(project_id: int, project_name: str, project_path: Path):
    if not project_path.is_dir():
        click.echo(
            click.style("warning:", fg="yellow")
            + f" Project path missing: {project_path} ({project_name})"
        )
        return

    click.echo(
        f"  {click.style(project_name, bold=True)} "
        + click.style(str(project_path), dim=True)
    )

    changed = scan_documents(project_id, project_path)
    check_dependency_rules(project_id, project_path, changed)
    check_sprawl(project_id)

    now = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%S")
    db.execute(
        "UPDATE projects SET updated_at=? WHERE id=?",
        (now, project_id),
    )


def run_scan(project_name: str | None = None, docs_only: bool = False):
    from endless.reconcile import reconcile
    reconcile()

    now = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%S")
    scan_type = "documents" if docs_only else "full"
    db.execute(
        "INSERT INTO scan_log (scan_type, started_at) VALUES (?, ?)",
        (scan_type, now),
    )
    scan_id = db.scalar("SELECT last_insert_rowid()")

    click.echo(click.style("•", fg="cyan") + " Scanning projects...")
    click.echo()

    projects_scanned = 0

    if project_name:
        rows = db.query(
            "SELECT id, name, path FROM projects WHERE name=?",
            (project_name,),
        )
        if not rows:
            raise click.ClickException(
                f"No project found with name '{project_name}'"
            )
    else:
        rows = db.query(
            "SELECT id, name, path FROM projects "
            "WHERE status != 'archived' ORDER BY name"
        )

    if not rows:
        click.echo(click.style("•", fg="cyan") + " No projects to scan.")
        return

    for row in rows:
        scan_project(row["id"], row["name"], Path(row["path"]))
        projects_scanned += 1

    end = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%S")
    db.execute(
        "UPDATE scan_log SET completed_at=?, projects_scanned=? "
        "WHERE id=?",
        (end, projects_scanned, scan_id),
    )

    click.echo()
    click.echo(
        click.style("•", fg="cyan")
        + f" Scan complete: {projects_scanned} project(s)"
    )

"""Reconcile DB state with filesystem truth."""

from datetime import datetime, timezone
from pathlib import Path

import click

from endless import db, config
from endless.project_path import resolved, stored


def reconcile():
    """Scan roots for .endless/config.json files and sync DB.

    - Projects found on disk but not in DB → insert
    - Projects in DB whose path moved → update path
    - Projects in DB whose path no longer exists → remove
    - Projects on disk whose config changed → update DB
    - Relation rows whose task endpoint is gone → remove (E-1915)
    """
    roots = config.get_roots()

    # Collect all projects found on disk
    found_on_disk: dict[str, tuple[Path, dict]] = {}  # name → (path, cfg)

    for root in roots:
        _scan_dir_for_projects(root, found_on_disk)

    # Get all projects in DB
    db_rows = db.query("SELECT id, name, path FROM projects")
    db_by_name: dict[str, dict] = {
        row["name"]: dict(row) for row in db_rows
    }
    # Keyed on the RESOLVED form, not the stored string (E-2002): a project
    # whose row predates this normalization must still be recognized as the
    # same project as the directory found on disk, or reconcile inserts a
    # second row for it.
    db_by_path: dict[str, dict] = {
        str(resolved(row["path"])): dict(row) for row in db_rows
    }

    now = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%S")

    # Reconcile: disk → DB
    for name, (disk_path, cfg) in found_on_disk.items():
        # Canonicalized on the way into the DB, so a root reached through a
        # symlink is stored the one way both halves compare against. Repairs a
        # stale row in passing: a project matched by name whose stored path is
        # in an older spelling gets rewritten to the canonical form.
        disk_path = resolved(disk_path)
        stored_path = stored(disk_path)
        # db_by_path is keyed on the resolved form, so the lookup below uses
        # that; only the column write uses the stored form.
        resolved_str = str(disk_path)

        if name in db_by_name:
            db_entry = db_by_name[name]
            if db_entry["path"] != stored_path:
                # Path changed (moved/renamed), or the row is in an older
                # spelling → update
                db.execute(
                    "UPDATE projects SET path=?, "
                    "group_name=?, updated_at=? "
                    "WHERE id=?",
                    (stored_path, _detect_group(disk_path),
                     now, db_entry["id"]),
                )
            # Also sync any config changes
            _sync_config_to_db(db_entry["id"], cfg, now)
        elif resolved_str in db_by_path:
            # Same path but name changed → update name
            db_entry = db_by_path[resolved_str]
            db.execute(
                "UPDATE projects SET name=?, updated_at=? "
                "WHERE id=?",
                (name, now, db_entry["id"]),
            )
            _sync_config_to_db(db_entry["id"], cfg, now)
        else:
            # New project on disk, not in DB → insert
            # But skip if status is "unregistered"
            if cfg.get("status") != "unregistered":
                _insert_from_config(disk_path, cfg, now)

    # Reconcile: DB entries whose paths no longer exist
    for row in db_rows:
        path = resolved(row["path"])
        if not path.exists():
            # Path gone and name not found elsewhere on disk
            if row["name"] not in found_on_disk:
                db.execute(
                    "DELETE FROM projects WHERE id=?",
                    (row["id"],),
                )

    repair_orphan_relations()


def repair_orphan_relations() -> int:
    """Delete relation rows whose task endpoint no longer exists (E-1915).

    `task remove` used to delete the task row and leave every `task_deps` /
    `decision_relations` row pointing at it. Nothing surfaced the orphan while
    the id stayed free — every consumer reaches relations through a join to
    `tasks` — but task ids are reused, so a later task taking the freed id
    silently inherited the dead relations and reported them as fact.

    The `task remove` guard stops NEW orphans; this clears what the ledger
    already accumulated. Ordering hazard: this can only find orphans whose id is
    still FREE. An orphan whose id has since been reused is indistinguishable
    from a genuine relation and is not recoverable by query.

    Returns the number of rows removed. Prints what it removed — a silent repair
    of silent corruption teaches nothing.

    Reads the raw `tasks` table, NOT live_tasks (E-1929). "Orphan" here means the
    row NO LONGER EXISTS — the only case where a reused id could inherit it. A
    relation pointing at a removed-but-retained row is not that: the id can never
    be re-minted, and the row is there to explain the reference. Pointing this at
    live_tasks would turn a one-time repair of real corruption into a routine
    deleter of every removed task's relations.
    """
    dep_rows = db.query(
        "SELECT id, source_type, source_id, target_type, target_id, dep_type "
        "FROM task_deps "
        "WHERE (source_type = 'task' AND source_id NOT IN (SELECT id FROM tasks)) "
        "   OR (target_type = 'task' AND target_id NOT IN (SELECT id FROM tasks)) "
        "ORDER BY id"
    )
    rel_rows = db.query(
        "SELECT id, source_decision_id, target_id, relation_type "
        "FROM decision_relations "
        "WHERE target_kind = 'task' AND target_id NOT IN (SELECT id FROM tasks) "
        "ORDER BY id"
    )
    if not dep_rows and not rel_rows:
        return 0

    def show(kind: str, item_id: int) -> str:
        return f"ED-{item_id}" if kind == "decision" else f"E-{item_id}"

    click.echo(
        click.style("•", fg="cyan")
        + f" Removed {len(dep_rows) + len(rel_rows)} orphaned relation row(s) "
        "— the task they referenced no longer exists:"
    )
    for row in dep_rows:
        click.echo(
            f"    task_deps #{row['id']}: "
            f"{show(row['source_type'], row['source_id'])} {row['dep_type']} "
            f"{show(row['target_type'], row['target_id'])}"
        )
        db.execute("DELETE FROM task_deps WHERE id = ?", (row["id"],))
    for row in rel_rows:
        click.echo(
            f"    decision_relations #{row['id']}: "
            f"ED-{row['source_decision_id']} {row['relation_type']} "
            f"E-{row['target_id']}"
        )
        db.execute(
            "DELETE FROM decision_relations WHERE id = ?", (row["id"],)
        )

    return len(dep_rows) + len(rel_rows)


def _scan_dir_for_projects(
    dir_path: Path,
    found: dict[str, tuple[Path, dict]],
):
    """Recursively scan a directory for .endless/config.json files."""
    if not dir_path.is_dir():
        return
    for child in dir_path.iterdir():
        if not child.is_dir() or child.name.startswith("."):
            continue

        cfg_file = child / ".endless" / "config.json"
        if cfg_file.is_file():
            cfg = config.project_config_read(child)
            if cfg and cfg.get("type") == "group":
                # It's a group dir — scan its children
                for subdir in child.iterdir():
                    if not subdir.is_dir():
                        continue
                    if subdir.name.startswith("."):
                        continue
                    sub_cfg_file = subdir / ".endless" / "config.json"
                    if sub_cfg_file.is_file():
                        sub_cfg = config.project_config_read(subdir)
                        if sub_cfg and sub_cfg.get("type") != "group":
                            name = sub_cfg.get("name", subdir.name)
                            found[name] = (subdir, sub_cfg)
            else:
                name = cfg.get("name", child.name)
                found[name] = (child, cfg)
        else:
            # Check if it's a group (has .endless/config.json
            # with type=group) or contains project subdirs
            if config.is_group_dir(child):
                for subdir in child.iterdir():
                    if not subdir.is_dir():
                        continue
                    if subdir.name.startswith("."):
                        continue
                    sub_cfg_file = subdir / ".endless" / "config.json"
                    if sub_cfg_file.is_file():
                        sub_cfg = config.project_config_read(subdir)
                        if sub_cfg and sub_cfg.get("type") != "group":
                            name = sub_cfg.get("name", subdir.name)
                            found[name] = (subdir, sub_cfg)


def _detect_group(project_path: Path) -> str | None:
    """Detect group_name from parent directory."""
    parent = project_path.parent
    roots = config.get_roots()
    if parent in roots:
        return None
    return parent.name


def _sync_config_to_db(project_id: int, cfg: dict, now: str):
    """Update DB fields from config values."""
    db.execute(
        "UPDATE projects SET "
        "label=?, description=?, language=?, status=?, "
        "updated_at=? WHERE id=?",
        (
            cfg.get("label", ""),
            cfg.get("description", ""),
            cfg.get("language", ""),
            cfg.get("status", "active"),
            now,
            project_id,
        ),
    )


def _insert_from_config(
    project_path: Path, cfg: dict, now: str,
):
    """Insert a new project from its on-disk config."""
    name = cfg.get("name", project_path.name)
    db.execute(
        "INSERT INTO projects "
        "(name, label, path, group_name, description, "
        "status, language, created_at, updated_at) "
        "VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
        (
            name,
            cfg.get("label", ""),
            stored(project_path),
            _detect_group(project_path),
            cfg.get("description", ""),
            cfg.get("status", "active"),
            cfg.get("language", ""),
            now,
            now,
        ),
    )

"""Reconcile DB state with filesystem truth."""

from datetime import datetime, timezone
from pathlib import Path

from endless import db, config


def reconcile():
    """Scan roots for .endless/config.json files and sync DB.

    - Projects found on disk but not in DB → insert
    - Projects in DB whose path moved → update path
    - Projects in DB whose path no longer exists → remove
    - Projects on disk whose config changed → update DB
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
    db_by_path: dict[str, dict] = {
        row["path"]: dict(row) for row in db_rows
    }

    now = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%S")

    # Reconcile: disk → DB
    for name, (disk_path, cfg) in found_on_disk.items():
        path_str = str(disk_path)

        if name in db_by_name:
            db_entry = db_by_name[name]
            if db_entry["path"] != path_str:
                # Path changed (moved/renamed) → update
                db.execute(
                    "UPDATE projects SET path=?, "
                    "group_name=?, updated_at=? "
                    "WHERE id=?",
                    (path_str, _detect_group(disk_path),
                     now, db_entry["id"]),
                )
            # Also sync any config changes
            _sync_config_to_db(db_entry["id"], cfg, now)
        elif path_str in db_by_path:
            # Same path but name changed → update name
            db_entry = db_by_path[path_str]
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
        path = Path(row["path"])
        if not path.exists():
            # Path gone and name not found elsewhere on disk
            if row["name"] not in found_on_disk:
                db.execute(
                    "DELETE FROM projects WHERE id=?",
                    (row["id"],),
                )


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
            str(project_path),
            _detect_group(project_path),
            cfg.get("description", ""),
            cfg.get("status", "active"),
            cfg.get("language", ""),
            now,
            now,
        ),
    )

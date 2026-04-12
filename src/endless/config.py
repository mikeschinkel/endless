"""Global and project configuration management."""

import json
from pathlib import Path

CONFIG_DIR = Path.home() / ".config" / "endless"
CONFIG_FILE = CONFIG_DIR / "config.json"
DB_PATH = CONFIG_DIR / "endless.db"

DEFAULT_CONFIG = {
    "roots": ["~/Projects"],
    "scan_interval": 300,
    "ignore": [],
}


def ensure_config_dir():
    CONFIG_DIR.mkdir(parents=True, exist_ok=True)


def load_config() -> dict:
    ensure_config_dir()
    if not CONFIG_FILE.exists():
        save_config(DEFAULT_CONFIG)
        return dict(DEFAULT_CONFIG)
    with open(CONFIG_FILE) as f:
        return json.load(f)


def save_config(cfg: dict):
    ensure_config_dir()
    with open(CONFIG_FILE, "w") as f:
        json.dump(cfg, f, indent=2)
        f.write("\n")


def get_roots() -> list[Path]:
    cfg = load_config()
    roots = []
    for r in cfg.get("roots", []):
        expanded = Path(r).expanduser()
        if expanded.is_dir():
            roots.append(expanded)
    return roots


def is_ignored(path: Path) -> bool:
    """Check if a path or any of its ancestors is in the ignore list."""
    cfg = load_config()
    ignore_list = cfg.get("ignore", [])
    if not ignore_list:
        return False
    home = str(Path.home())
    # Check the path itself and all its parents
    check = path
    while True:
        check_str = str(check)
        check_short = check_str.replace(home, "~")
        if check_str in ignore_list or check_short in ignore_list:
            return True
        parent = check.parent
        if parent == check:
            break
        check = parent
    return False


def add_ignore(path: Path):
    if is_ignored(path):
        return
    cfg = load_config()
    short = str(path).replace(str(Path.home()), "~")
    cfg.setdefault("ignore", []).append(short)
    cfg["ignore"] = sorted(set(cfg["ignore"]))
    save_config(cfg)


def endless_config_path(dir_path: Path) -> Path:
    return dir_path / ".endless" / "config.json"


def project_config_path(project_path: Path) -> Path:
    return endless_config_path(project_path)


def project_config_read(project_path: Path) -> dict | None:
    p = project_config_path(project_path)
    if not p.exists():
        return None
    with open(p) as f:
        return json.load(f)


def project_config_write(project_path: Path, data: dict):
    p = project_config_path(project_path)
    p.parent.mkdir(parents=True, exist_ok=True)
    with open(p, "w") as f:
        json.dump(data, f, indent=2)
        f.write("\n")


def is_group_dir(dir_path: Path) -> bool:
    """Check if a directory is marked as a group."""
    cfg = project_config_read(dir_path)
    if cfg and cfg.get("type") in ("group", "project_group"):
        return True
    return False


def mark_as_group(dir_path: Path):
    """Write .endless/config.json marking this dir as a group."""
    p = endless_config_path(dir_path)
    p.parent.mkdir(parents=True, exist_ok=True)
    data = {
        "type": "group",
        "name": dir_path.name,
    }
    with open(p, "w") as f:
        json.dump(data, f, indent=2)
        f.write("\n")

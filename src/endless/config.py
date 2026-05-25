"""Global and project configuration management."""

import json
import os
import re
from pathlib import Path


def _config_root() -> Path:
    xdg = os.environ.get("XDG_CONFIG_HOME")
    if xdg:
        return Path(xdg)
    return Path.home() / ".config"


CONFIG_DIR = _config_root() / "endless"
CONFIG_FILE = CONFIG_DIR / "config.json"
DB_PATH = CONFIG_DIR / "endless.db"

# RESOLVED_CONFIG_DIR records an explicit DB/config directory chosen for this
# invocation via the root `--db main|worktree` flag (E-1429). None means no
# explicit choice was made. When set, it both (a) satisfies the self-dev
# worktree gate and (b) is threaded to Go subprocesses via --config-dir.
RESOLVED_CONFIG_DIR: Path | None = None

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


def project_wants_worktree_sandbox(project_path: Path) -> bool:
    """True if the project opts into per-worktree DB sandboxing.

    Set by adding `"worktree_sandbox": true` to <project>/.endless/config.json.
    Endless's own config has this enabled so dev-time worktrees don't pollute
    the user's real DB; downstream projects using endless as a tool leave it
    unset so their tasks land in the real DB.
    """
    cfg = project_config_read(project_path)
    if cfg is None:
        return False
    return bool(cfg.get("worktree_sandbox", False))


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


# === E-1429: explicit DB selection inside self-dev worktrees ===
#
# Inside a self-dev worktree (a .endless/worktrees/e-NNN checkout of a project
# whose config.json sets "worktree_sandbox": true), the implicit XDG-driven DB
# routing is replaced by a mandatory, per-invocation --db main|worktree flag.
# The choice is never an env var: an exported var could silently route every
# later command to the wrong DB. The flag resolves to a config directory, which
# pins this process's reads and is threaded to Go subprocesses via --config-dir.

# Matches the canonical task-worktree path segment, capturing the task-id
# digits. Mirrors monitor.TaskIDFromWorktreePath on the Go side.
_WORKTREE_PATH_RE = re.compile(r"/\.endless/worktrees/e-(\d+)(?:-[a-z0-9-]+)?(?:/|$)")

# The locked refusal message. Click prepends "Error: " to produce the final
# wording. Intentionally has no E-NNN ticket refs (user-facing).
WORKTREE_DB_REFUSAL = (
    "running inside self-dev worktree requires an explicit --db value:\n\n"
    "  --db main      the real ledger — managing the project\n"
    "  --db worktree  this worktree's sandbox — testing endless itself\n\n"
    "Need paths? Run `endless db path --db=main|worktree`."
)


def _cache_root() -> Path:
    xdg = os.environ.get("XDG_CACHE_HOME")
    if xdg:
        return Path(xdg)
    return Path.home() / ".cache"


def main_config_dir() -> Path:
    """The real ledger's config dir: ~/.config/endless, ignoring any injected
    XDG_CONFIG_HOME (the whole point of --db main is to escape the sandbox)."""
    return Path.home() / ".config" / "endless"


def sandbox_config_dir(task_id: str) -> Path:
    """The endless config dir inside a worktree's per-worktree sandbox.

    Matches the on-disk layout produced by the XDG mechanism today: the
    sandbox dir is <cache>/endless/sandboxes/worktree-e-<id>, and endless
    appends its own "endless" segment (as ConfigDir does to XDG_CONFIG_HOME),
    so the DB lives at <sandbox>/endless/endless.db.
    """
    sandbox = _cache_root() / "endless" / "sandboxes" / f"worktree-e-{task_id}"
    return sandbox / "endless"


def worktree_task_id(cwd: Path | None = None) -> str | None:
    """Task-id digits if cwd is inside a .endless/worktrees/e-NNN worktree,
    else None. Pure: no filesystem or config reads."""
    s = str(cwd if cwd is not None else Path.cwd())
    m = _WORKTREE_PATH_RE.search(s)
    return m.group(1) if m else None


def gated_worktree_root(cwd: Path | None = None) -> Path | None:
    """Project root if cwd is inside a self-dev worktree of a worktree_sandbox
    project (so --db is required), else None."""
    s = str(cwd if cwd is not None else Path.cwd())
    m = _WORKTREE_PATH_RE.search(s)
    if not m:
        return None
    root = Path(s[: m.start()])
    return root if project_wants_worktree_sandbox(root) else None


def set_db_context(config_dir: Path):
    """Pin this process's config dir (and DB) to config_dir, overriding the
    XDG-derived defaults. Re-assigns the module paths so db.py (which reads
    config.DB_PATH dynamically) and config readers follow it, and records
    RESOLVED_CONFIG_DIR for the gate and Go subprocess threading."""
    global CONFIG_DIR, CONFIG_FILE, DB_PATH, RESOLVED_CONFIG_DIR
    CONFIG_DIR = config_dir
    CONFIG_FILE = config_dir / "config.json"
    DB_PATH = config_dir / "endless.db"
    RESOLVED_CONFIG_DIR = config_dir


def apply_db_choice(choice: str):
    """Resolve a --db main|worktree choice to a config dir and pin it.

    Raises ValueError for --db worktree outside a worktree.
    """
    if choice == "main":
        set_db_context(main_config_dir())
    elif choice == "worktree":
        task_id = worktree_task_id()
        if task_id is None:
            raise ValueError(
                "--db worktree only applies inside a self-dev worktree "
                "(.endless/worktrees/e-NNN); cwd is not in one"
            )
        set_db_context(sandbox_config_dir(task_id))
    else:  # pragma: no cover - click.Choice prevents this
        raise ValueError(f"unknown --db value: {choice!r}")


def require_db_context():
    """Enforce the self-dev worktree gate at a DB-access choke point.

    No-op when a --db choice was resolved (RESOLVED_CONFIG_DIR set) or when not
    inside a gated worktree. Otherwise raises click.ClickException with the
    locked refusal message. Imported lazily so config.py stays click-free.
    """
    if RESOLVED_CONFIG_DIR is not None:
        return
    if gated_worktree_root() is None:
        return
    import click

    raise click.ClickException(WORKTREE_DB_REFUSAL)


def go_db_context_args() -> list[str]:
    """The --config-dir argument pair to thread the resolved DB context to a
    CLI-path Go subprocess, or [] when no explicit context is active.

    Call require_db_context() first at any site that spawns a DB-opening Go
    binary, so a missing --db refuses with the friendly message before the Go
    backstop refuses with its terser one.
    """
    if RESOLVED_CONFIG_DIR is None:
        return []
    return ["--config-dir", str(RESOLVED_CONFIG_DIR)]

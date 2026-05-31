"""Global and project configuration management."""

import json
import os
import re
import subprocess
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
# invocation via the global `--db main|sandbox` flag (E-1429/E-1476). None means
# no explicit choice was made. When set, it both (a) satisfies the self-dev
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
# routing is replaced by a mandatory, per-invocation --db main|sandbox flag.
# The choice is never an env var: an exported var could silently route every
# later command to the wrong DB. The flag resolves to a config directory, which
# pins this process's reads and is threaded to Go subprocesses via --config-dir.

# Matches the canonical task-worktree path segment, capturing the task-id
# digits. Mirrors monitor.TaskIDFromWorktreePath on the Go side.
_WORKTREE_PATH_RE = re.compile(r"/\.endless/worktrees/e-(\d+)(?:-[a-z0-9-]+)?(?:/|$)")

# The locked refusal message. Click prepends "Error: " to produce the final
# wording. Intentionally has no E-NNN ticket refs (user-facing).
WORKTREE_DB_REFUSAL = (
    "running inside a self-dev worktree requires an explicit --db value "
    "(accepted in any position):\n\n"
    "  --db main     the real ledger — managing the project\n"
    "  --db sandbox  this worktree's throwaway test DB — testing endless itself\n\n"
    "Need paths? Run `endless db path --db=main|sandbox`."
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
    """Resolve a --db main|sandbox choice to a config dir and pin it.

    Raises ValueError for an unknown value, or for --db sandbox outside a
    worktree. This is the single validator for the flag (DBAwareGroup consumes
    --db from argv and calls here; there is no Click Choice to pre-validate).
    """
    if choice == "main":
        set_db_context(main_config_dir())
    elif choice == "sandbox":
        task_id = worktree_task_id()
        if task_id is None:
            raise ValueError(
                "--db sandbox only applies inside a self-dev worktree "
                "(.endless/worktrees/e-NNN); cwd is not in one"
            )
        set_db_context(sandbox_config_dir(task_id))
    else:
        raise ValueError(
            f"unknown --db value {choice!r}: expected 'main' or 'sandbox'"
        )


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


def resolution_cwd() -> Path:
    """Effective cwd for project resolution.

    When --db main is in effect AND cwd is inside a git worktree (not the
    main checkout), walk to the main checkout via the git-dir vs
    git-common-dir discriminator and return that. Otherwise return Path.cwd().

    Lets cwd-keyed project lookups (`<cwd>/.endless/config.json`, then
    `SELECT ... FROM projects WHERE path = ?`) find the canonical project
    row in the main DB when run from inside a per-task worktree — without
    forcing the caller to pass --project explicitly.
    """
    cwd = Path.cwd()
    if RESOLVED_CONFIG_DIR != main_config_dir():
        return cwd
    try:
        git_dir = subprocess.run(
            ["git", "rev-parse", "--git-dir"],
            cwd=str(cwd), capture_output=True, text=True, check=True,
        ).stdout.strip()
        common_dir = subprocess.run(
            ["git", "rev-parse", "--git-common-dir"],
            cwd=str(cwd), capture_output=True, text=True, check=True,
        ).stdout.strip()
    except (subprocess.CalledProcessError, FileNotFoundError):
        return cwd
    git_dir_abs = (cwd / git_dir).resolve()
    common_dir_abs = (cwd / common_dir).resolve()
    if git_dir_abs == common_dir_abs:
        return cwd
    return common_dir_abs.parent


def resolved_worktree_endless_go(cwd: Path | None = None) -> Path | None:
    """Path to <worktree>/bin/endless-go when --db sandbox is the active DB
    context AND cwd is inside a self-dev worktree, else None.

    Used by event_bridge to prefer the worktree-built binary (which embeds
    the worktree's schema.sql) over the PATH-resolved global. The global
    symlink points at main's binary, so additive schema in this branch is
    silently absent unless the worktree binary is used.

    Does not check existence — callers handle the missing-binary case
    explicitly and surface a loud error naming the bad state (E-1510).
    """
    if RESOLVED_CONFIG_DIR is None:
        return None
    task_id = worktree_task_id(cwd)
    if task_id is None:
        return None
    # Only fire when RESOLVED_CONFIG_DIR is EXACTLY the sandbox path for this
    # worktree. Anything else (--db main, conftest's tmp config dir, a stray
    # external override) keeps the PATH-resolved global.
    if RESOLVED_CONFIG_DIR != sandbox_config_dir(task_id):
        return None
    root = gated_worktree_root(cwd)
    if root is None:
        return None
    return root / ".endless" / "worktrees" / f"e-{task_id}" / "bin" / "endless-go"


def worktree_python_reexec_target(
    cwd: Path | None = None,
    source_file: Path | None = None,
) -> Path | None:
    """Path of the self-dev worktree whose Python source `endless` should
    re-exec into, or None when the current process is already inside that
    source (or isn't in a gated worktree at all).

    The global `endless` script is the editable install of main's source, so
    running it inside a worktree exercises main's Python — not the worktree's
    candidate changes — against the worktree's sandbox DB. cli.DBAwareGroup
    calls this when `--db sandbox` is in argv and execvp's `uv run --directory
    <target> endless ...` so the worktree's source runs instead. The
    symmetric Python-layer fix to E-1510 (Go binary self-detect).

    `source_file` defaults to this module's __file__ and is the re-entrancy
    guard: after the uv-run exec, this module loads from inside the worktree
    and the helper returns None so the process doesn't loop. Exposed for
    tests that simulate a different source location.
    """
    task_id = worktree_task_id(cwd)
    if task_id is None:
        return None
    root = gated_worktree_root(cwd)
    if root is None:
        return None
    worktree = (root / ".endless" / "worktrees" / f"e-{task_id}").resolve()
    src = (source_file if source_file is not None else Path(__file__)).resolve()
    try:
        src.relative_to(worktree)
    except ValueError:
        return worktree
    return None

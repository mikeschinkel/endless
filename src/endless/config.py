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
# worktree gate and (b) is threaded to Go subprocesses as --db main|sandbox.
RESOLVED_CONFIG_DIR: Path | None = None

# PINNED_DB_CONTEXT records that the DB context was chosen IN CODE rather than
# by the caller — `default_db_to_main()`, the Python analogue of the Go side's
# PinMainDB. It is the E-1668 announce exemption: if the caller could not have
# influenced the choice there is nothing to disambiguate, so a pinned context
# says nothing about itself. A pin is not a resolution.
PINNED_DB_CONTEXT: bool = False

# NO_SESSION records the global `--no-session` flag (E-1444). When True,
# emit_event downgrades actor_kind from cli/hook to system and skips session
# resolution — for plain-shell triage filings, cron, and scripts with no Claude
# session to attribute to. Set by DBAwareGroup.main() via the same
# position-anywhere argv pre-extractor that consumes --db.
NO_SESSION: bool = False

DEFAULT_CONFIG = {
    "roots": ["~/Projects"],
    "scan_interval": 300,
    "ignore": [],
}


def tilde(p: Path | str) -> str:
    """Display a path with $HOME collapsed to ~, for output a human reads.

    Only a leading $HOME is collapsed, and only once: a path is being shown so
    it can be copied and pasted, and rewriting a home-shaped segment in the
    middle of one would break that.
    """
    s = str(p)
    home = str(Path.home())
    return s.replace(home, "~", 1) if s.startswith(home) else s


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


# === E-1859: one model resolver, per-purpose values ===
#
# Endless makes internal, headless model calls for a handful of small
# judgments. Each such call site names a PURPOSE and resolves its model here,
# so there is exactly one mechanism (and one place to look) rather than a model
# alias hardcoded at each call site.
#
# Values are claude model aliases or full model names, resolved:
#   <project>/.endless/config.json  "models": {"<purpose>": "..."}   (per-project)
#   <config dir>/config.json        "models": {"<purpose>": "..."}   (per-user)
#   INTERNAL_MODEL_DEFAULTS                                          (shipped)
#
# The defaults differ per purpose on purpose: "is this word a verb?" is a
# lookup, while "is this description a sufficient spec?" is a judgment over a
# paragraph of prose plus its parent and sibling context. A user who wants them
# identical sets one value.
#
# NOT covered here: `endless task report`'s two per-entry gates, which stay
# pinned to Haiku. E-1911 is actively reworking that contract; changing model
# selection there would collide with it.
INTERNAL_MODEL_DEFAULTS: dict[str, str] = {
    # E-1264: is the first word of a task title an actionable verb?
    "verb_check": "haiku",
    # E-1859: is a task's description already a sufficient spec?
    "triage": "sonnet",
}


def internal_model(purpose: str, project_root: Path | None = None) -> str:
    """Resolve the model alias for an internal call of the named `purpose`.

    An unknown purpose raises KeyError: purposes are a closed set declared in
    INTERNAL_MODEL_DEFAULTS, so a typo at a call site is a programming error
    that should fail loudly rather than silently fall through to claude's
    default model.

    A configured-but-blank value is treated as unset (falls through to the next
    layer), so clearing a key in project config does not send an empty
    `--model ''` to claude.
    """
    if purpose not in INTERNAL_MODEL_DEFAULTS:
        raise KeyError(
            f"unknown internal model purpose {purpose!r}; "
            f"expected one of {sorted(INTERNAL_MODEL_DEFAULTS)}"
        )

    if project_root is None:
        project_root = enclosing_project_root()
    if project_root is not None:
        proj = project_config_read(project_root) or {}
        value = (proj.get("models") or {}).get(purpose)
        if isinstance(value, str) and value.strip():
            return value.strip()

    try:
        user = load_config()
    except (OSError, json.JSONDecodeError):
        # A missing or corrupt user config must not break a model call that
        # has a perfectly good shipped default.
        user = {}
    value = (user.get("models") or {}).get(purpose)
    if isinstance(value, str) and value.strip():
        return value.strip()

    return INTERNAL_MODEL_DEFAULTS[purpose]


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


def project_is_self_dev(project_path: Path) -> bool:
    """True if the project is endless self-development, so its worktrees
    sandbox DB writes.

    Set by adding `"self_dev": true` to <project>/.endless/config.json.
    Endless's own config has this enabled so dev-time worktrees don't pollute
    the user's real DB; downstream projects using endless as a tool leave it
    unset so their tasks land in the real DB.
    """
    cfg = project_config_read(project_path)
    if cfg is None:
        return False
    return bool(cfg.get("self_dev", False))


# Extensions whose `name.ext:NNN` form is read as a source citation (E-1934).
# Deliberately broad: under-matching lets the defect back in, and a project that
# finds an entry noisy removes it with content.extensions.unblock. Many of these
# are also ccTLDs (rs, py, pl, sh, ml, cc, md, tf), which is why the citation
# scan must skip URL-shaped tokens rather than lean on this set alone.
CITATION_EXTENSIONS: frozenset[str] = frozenset({
    # systems
    "go", "rs", "zig", "nim", "c", "h", "cc", "cpp", "cxx", "hpp", "hh", "m", "mm",
    # jvm / .net
    "java", "kt", "kts", "scala", "clj", "cljs", "cs", "fs",
    # scripting
    "py", "rb", "php", "pl", "lua", "sh", "bash", "zsh", "fish", "ps1", "tcl",
    # js / ts / web
    "js", "jsx", "mjs", "cjs", "ts", "tsx", "vue", "svelte", "astro",
    "html", "htm", "css", "scss", "sass", "less",
    # functional and other
    "ex", "exs", "erl", "hs", "ml", "swift", "dart", "r", "jl",
    # data and config
    "json", "yaml", "yml", "toml", "ini", "cfg", "conf", "xml", "csv", "tsv",
    "env", "properties",
    # docs
    "md", "markdown", "rst", "adoc", "txt", "tex",
    # build and schema
    "mk", "cmake", "gradle", "bzl", "just", "tf", "tfvars", "proto",
    "graphql", "gql", "sql",
})


def project_content_config(project_path: Path) -> dict:
    """The project's durable-content gate settings (E-1934).

        "content": {
          "extensions": {"block": ["phtml"], "unblock": ["md"]},
          "gates": {"absolute_paths": true, "line_citations": true}
        }

    Absent file, absent key, or unreadable all mean BOTH GATES ON with the
    built-in extension set, so a project that has never heard of the setting is
    still protected.

    `block` and `unblock` layer over CITATION_EXTENSIONS rather than replacing
    it: the set runs to eighty-odd entries and a project adding one extension
    must not have to restate the rest.

    Listing an extension in BOTH is refused, not resolved. Two settings spelling
    contradictory intent is the ambiguity `--clear` against a field flag and
    `--status` against `--keep-status` already refuse; picking a winner here
    would mean the config file says one thing and the gate does another.

    Raises ValueError on that conflict. The caller converts it — this module
    does not import click.
    """
    gates = {"absolute_paths": True, "line_citations": True}
    default = {"extensions": CITATION_EXTENSIONS, "gates": gates}

    cfg = project_config_read(project_path)
    if cfg is None:
        return default
    content = cfg.get("content")
    if not isinstance(content, dict):
        return default

    out_gates = dict(gates)
    declared_gates = content.get("gates")
    if isinstance(declared_gates, dict):
        for key in ("absolute_paths", "line_citations"):
            if isinstance(declared_gates.get(key), bool):
                out_gates[key] = declared_gates[key]

    exts = set(CITATION_EXTENSIONS)
    declared_exts = content.get("extensions")
    if isinstance(declared_exts, dict):
        block = {e.lower().lstrip(".") for e in declared_exts.get("block") or ()}
        unblock = {e.lower().lstrip(".") for e in declared_exts.get("unblock") or ()}
        both = block & unblock
        if both:
            raise ValueError(
                "content.extensions lists "
                + ", ".join(sorted(both))
                + " under both block and unblock; they say opposite things. "
                "Remove the extension from one of the two lists."
            )
        exts = (exts | block) - unblock

    return {"extensions": frozenset(exts), "gates": out_gates}


def project_minimizer_config(project_path: Path) -> dict[str, bool]:
    """The project's minimizer switches (E-1953, reshaped by E-1975).

    Mirrors monitor.readMinimizer on the Go side, including the defaults and the
    three accepted spellings:

        "minimizer": {"enabled": true, "optimizer": false}   the current shape
        "minimizer": false                                   both off
        "report_gate": false                                 E-1953's name

    Absent file, absent key, or unreadable all mean BOTH ON. Only an explicit
    false turns either off, so a project that has never heard of the setting
    still gets the channel and the loop behind it.

    The old scalar is still read because the rename must not silently re-enable
    a gate a project had switched off — a config change that turns enforcement
    back on by doing nothing is the one migration failure the user cannot see.

    Read here so the spawn handoff can omit the reporting instructions entirely
    for a project that switched the channel off. Rendering them anyway would
    hand every spawned session a per-turn model round trip that nothing
    enforces and nothing reads — pure overhead, and worse, an instruction the
    project has explicitly declined.
    """
    default = {"enabled": True, "optimizer": True}
    cfg = project_config_read(project_path)
    if cfg is None:
        return default

    value = cfg.get("minimizer")
    if isinstance(value, bool):
        return {"enabled": value, "optimizer": value}
    if isinstance(value, dict):
        out = dict(default)
        for key in ("enabled", "optimizer"):
            if isinstance(value.get(key), bool):
                out[key] = value[key]
        return out

    legacy = cfg.get("report_gate")
    if isinstance(legacy, bool):
        return {"enabled": legacy, "optimizer": default["optimizer"]}
    return default


def minimizer_config_declared(dir_path: Path) -> tuple[dict[str, bool], bool]:
    """The switches declared AT dir_path, and whether it declared anything.

    Python mirror of Go's `monitor.readMinimizer`. `declared` is false when the
    file is absent, unreadable, malformed, or silent about the minimizer — which
    is what lets a caller tell "this layer says nothing" apart from "this layer
    says false" and fall through to the next one.
    """
    default = {"enabled": True, "optimizer": True}
    try:
        cfg = project_config_read(dir_path)
    except (OSError, json.JSONDecodeError):
        return default, False
    if cfg is None:
        return default, False

    value = cfg.get("minimizer")
    if isinstance(value, bool):
        return {"enabled": value, "optimizer": value}, True
    if isinstance(value, dict):
        out = dict(default)
        for key in ("enabled", "optimizer"):
            if isinstance(value.get(key), bool):
                out[key] = value[key]
        return out, True
    if value is not None:
        return default, False

    legacy = cfg.get("report_gate")
    if isinstance(legacy, bool):
        return {"enabled": legacy, "optimizer": default["optimizer"]}, True
    return default, False


def minimizer_config_for_cwd(
    cwd: Path | None = None, project_root: Path | None = None,
) -> dict[str, bool]:
    """The switches governing a session working in `cwd` (E-2030).

    Python mirror of Go's `monitor.MinimizerConfigForCwd`, and the mirror is the
    point. `enclosing_project_root` deliberately maps a worktree back to the MAIN
    checkout, so reading the switches from there gave a worktree the project's
    answer while the Stop hook — which walks up from cwd — gave it the
    worktree's. On a self-dev branch that had enabled the minimizer for itself
    those disagreed, and the guide told the session the channel was off while the
    hook held its turn against it. Told-iff-gated, violated from the gated side.

    Precedence is nearest-wins, and only an explicit key counts: a worktree that
    says nothing inherits the project's answer rather than resetting it to the
    default, so deleting the key from a branch cannot silently re-enable a gate
    the project had switched off.
    """
    start = Path.cwd() if cwd is None else cwd
    if project_root is None:
        project_root = enclosing_project_root(start)

    for parent in [start] + list(start.parents):
        cfg, declared = minimizer_config_declared(parent)
        if declared:
            return cfg
        if project_root is not None and parent == project_root:
            break

    if project_root is None:
        return {"enabled": True, "optimizer": True}
    return minimizer_config_declared(project_root)[0]


def report_gate_for_cwd(cwd: Path | None = None) -> bool:
    """The enabled half of `minimizer_config_for_cwd` — what gate-side callers want."""
    return minimizer_config_for_cwd(cwd)["enabled"]


def project_report_gate(project_path: Path) -> bool:
    """True if the minimizer's report channel is live for this project."""
    return project_minimizer_config(project_path)["enabled"]


def project_minimizer_optimizer(project_path: Path) -> bool:
    """True if the autoresearch loop may tune this project's minimizer."""
    return project_minimizer_config(project_path)["optimizer"]


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
# whose config.json sets "self_dev": true), the implicit XDG-driven DB
# routing is replaced by a mandatory, per-invocation --db main|sandbox flag.
# The choice is never an env var: an exported var could silently route every
# later command to the wrong DB. The flag resolves to a config directory, which
# pins this process's reads and is threaded to Go subprocesses as --db main|sandbox.

# Matches the canonical task-worktree path segment. Group 1 captures the
# worktree dir basename (e-NNN) — the source of the per-worktree sandbox dir
# name. Only the bare `e-NNN` form is recognized (ED-1515); a trailing `-slug`
# no longer resolves as the task's worktree. Mirrors monitor.worktreeDirName /
# monitor.TaskIDFromWorktreePath on the Go side.
_WORKTREE_PATH_RE = re.compile(
    r"/\.endless/worktrees/(e-\d+)(?:/|$)"
)

# Rejection message when --db is used in a project that is not self-dev. Such a
# project has exactly one database, so the flag has nothing to select — accepting
# it (main included) would silently mask confusion, and --db sandbox would mkdir a
# stray sandbox endless.db in the cache. Plain wording, no ticket refs (user-facing).
DB_NOT_SELF_DEV_REFUSAL = (
    "--db only applies to projects with self-dev enabled; this project has a "
    "single database, so --db has nothing to select. Remove the flag."
)

# The locked refusal message. Click prepends "Error: " to produce the final
# wording. Intentionally has no E-NNN ticket refs (user-facing).
WORKTREE_DB_REFUSAL = (
    "running inside a self-dev worktree requires an explicit --db value "
    "(accepted in any position):\n\n"
    "  --db main     the project's main database — managing the project\n"
    "  --db sandbox  this worktree's sandbox database — testing endless itself\n\n"
    "Need paths? Run `endless db path --db=main|sandbox`."
)


def _cache_root() -> Path:
    xdg = os.environ.get("XDG_CACHE_HOME")
    if xdg:
        return Path(xdg)
    return Path.home() / ".cache"


def main_config_dir() -> Path:
    """The main database's config dir: ~/.config/endless, ignoring any injected
    XDG_CONFIG_HOME (the whole point of --db main is to escape the sandbox)."""
    return Path.home() / ".config" / "endless"


def main_cache_dir() -> Path:
    """The main database's cache dir: ~/.cache/endless, ignoring any injected
    XDG_CACHE_HOME (mirror of main_config_dir for the cache root; sandboxes live
    under here at ~/.cache/endless/sandboxes/<worktree>/)."""
    return Path.home() / ".cache" / "endless"


def sandbox_root(worktree_dir_name: str) -> Path:
    """A worktree's per-worktree sandbox: the isolated state that belongs to
    that worktree and shares its lifetime.

    Sandbox dir basename is the worktree dir's basename (e-NNN), so each
    worktree maps 1-to-1 to its own sandbox.

    The single seam every sandbox path composes from, so a sandbox that moves
    moves here and nowhere else — which is what ED-1554 (relocating sandboxes
    into the worktree itself) will do. Callers name what they want UNDER the
    sandbox; they do not spell out where the sandbox is.
    """
    return _cache_root() / "endless" / "sandboxes" / worktree_dir_name


def sandbox_config_dir(worktree_dir_name: str) -> Path:
    """The endless config dir inside a worktree's per-worktree sandbox.

    Endless appends its own "endless" segment (as ConfigDir does to
    XDG_CONFIG_HOME), so the DB lives at
    <sandbox_root>/endless/endless.db. Endless is one client of the sandbox
    among however many the project has, and this is its corner of it.
    """
    return sandbox_root(worktree_dir_name) / "endless"


def worktree_dir_name(cwd: Path | None = None) -> str | None:
    """Worktree dir basename (e-NNN) if cwd is inside a
    .endless/worktrees/e-NNN worktree, else None. Pure: no filesystem
    or config reads. Used to derive the per-worktree sandbox dir name."""
    s = str(cwd if cwd is not None else Path.cwd())
    m = _WORKTREE_PATH_RE.search(s)
    return m.group(1) if m else None


def gated_worktree_root(cwd: Path | None = None) -> Path | None:
    """Project root if cwd is inside a self-dev worktree of a self_dev
    project (so --db is required), else None."""
    s = str(cwd if cwd is not None else Path.cwd())
    m = _WORKTREE_PATH_RE.search(s)
    if not m:
        return None
    root = Path(s[: m.start()])
    return root if project_is_self_dev(root) else None


def enclosing_project_root(cwd: Path | None = None) -> Path | None:
    """The project root that encloses cwd, or None if cwd is in no project.

    Inside a .endless/worktrees/e-NNN worktree, that's the main checkout above
    the worktree segment (mirroring gated_worktree_root). Otherwise it's the
    nearest ancestor holding a .endless/config.json. Used to decide whether a
    --db choice is valid: --db only applies to self-dev projects."""
    start = cwd if cwd is not None else Path.cwd()
    m = _WORKTREE_PATH_RE.search(str(start))
    if m:
        return Path(str(start)[: m.start()])
    for parent in [start] + list(start.parents):
        if (parent / ".endless" / "config.json").exists():
            return parent
    return None


def enclosing_project_is_self_dev(cwd: Path | None = None) -> bool:
    """True if the project enclosing cwd has self-dev enabled — the gate for
    accepting a --db choice at all."""
    root = enclosing_project_root(cwd)
    return root is not None and project_is_self_dev(root)


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

    Raises ValueError for an unknown value, for --db in a project that is not
    self-dev (both values — such a project has one DB, so the flag is invalid),
    or for --db sandbox outside a worktree. This is the single validator for the
    flag (DBAwareGroup consumes --db from argv and calls here; there is no Click
    Choice to pre-validate).
    """
    if choice == "main":
        if not enclosing_project_is_self_dev():
            raise ValueError(DB_NOT_SELF_DEV_REFUSAL)
        set_db_context(main_config_dir())
    elif choice == "sandbox":
        dir_name = worktree_dir_name()
        if dir_name is None:
            raise ValueError(
                "--db sandbox only applies inside a self-dev worktree "
                "(.endless/worktrees/e-NNN); cwd is not in one"
            )
        if not enclosing_project_is_self_dev():
            raise ValueError(DB_NOT_SELF_DEV_REFUSAL)
        set_db_context(sandbox_config_dir(dir_name))
    else:
        raise ValueError(
            f"unknown --db value {choice!r}: expected 'main' or 'sandbox'"
        )


def default_db_to_main():
    """Pin the real/main DB for an always-main operation when the caller gave
    no explicit --db.

    The Python analogue of the Go-side PinMainDB (E-1450/E-1429) used by
    hook/tmux: some operations are inherently real/main regardless of
    caller routing — worktree land, schema apply-change, db backup. When run
    from a self-dev session whose XDG_CONFIG_HOME points at a per-worktree
    sandbox, the default config dir resolves to that sandbox, mis-targeting the
    landing (the landed task lives only in the real DB; the task_landings FK
    fails). Calling this at such a command's entry pins main_config_dir() so
    Python reads use the real DB and go_db_context_args() threads
    --db main to every downstream endless-go shellout.

    An explicit --db main|sandbox is honored: it sets RESOLVED_CONFIG_DIR via
    DBAwareGroup before the command body runs, so this is a no-op then.

    Pins main_config_dir() directly rather than through apply_db_choice: this is
    a forced-main operation valid in ANY project (worktree land, db backup, and
    apply-change run in downstream non-self-dev projects too), so it must bypass
    apply_db_choice's self-dev gate on the --db flag.
    """
    global PINNED_DB_CONTEXT
    if RESOLVED_CONFIG_DIR is None:
        set_db_context(main_config_dir())
        # Chosen here, not by the caller — so nothing announces it (E-1668).
        PINNED_DB_CONTEXT = True


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
    """The flag pair that threads the resolved DB context to a CLI-path Go
    subprocess, or [] when no explicit context is active.

    Since E-1668 this speaks the SAME vocabulary the user typed: `endless --db
    main` threads `--db main`, not a directory under a flag name (`--config-dir`)
    that no user has ever heard of. One word means one thing across both layers,
    which is what lets the Go binary's own refusal name a remedy a reader can
    type.

    The spelling is derived from what was RESOLVED, not from what was typed, so
    every route into a pinned context — `--db`, `default_db_to_main`, a test
    monkeypatching RESOLVED_CONFIG_DIR — threads something the child can act on:

      - the main config dir          -> `--db main`
      - this worktree's sandbox dir  -> `--db sandbox`
      - anything else                -> `--db-dir <path>`

    `--db main` rather than `--db-dir <main>` on purpose: main_config_dir()
    follows $HOME, so a caller already running under a temp HOME (the verify
    runner does) gets its own isolated main in the child exactly as it did in
    the parent. Threading the absolute path would instead hand the child a
    directory resolved against whichever HOME the parent happened to have.

    `--db sandbox` is likewise safe to thread rather than spell out: the Go side
    resolves it from cwd, and every endless-go spawn inherits this process's cwd
    (none of them pass cwd=). We re-derive the sandbox dir here from the live cwd
    so the two agree by construction rather than by assumption.

    `--db-dir` is the escape, and it stays an escape: it is reached only when the
    resolved dir is neither named database.

    Call require_db_context() first at any site that spawns a DB-opening Go
    binary, so a missing --db refuses with the friendly message before the Go
    backstop refuses with its terser one.
    """
    # The shellout opens a database, so this invocation has touched the store
    # even though no SQLite handle was opened in this process (E-1668).
    from endless import provenance

    provenance.mark_touched()
    if RESOLVED_CONFIG_DIR is None:
        return []
    if RESOLVED_CONFIG_DIR == main_config_dir():
        return ["--db", "main"]
    dir_name = worktree_dir_name()
    if dir_name and RESOLVED_CONFIG_DIR == sandbox_config_dir(dir_name):
        return ["--db", "sandbox"]
    return ["--db-dir", str(RESOLVED_CONFIG_DIR)]


def migrate_db_context_args() -> list[str]:
    """The flag pair that threads the resolved DB context to `endless-migrate`.

    A SECOND spelling, and deliberately not a duplicate of the one above: the two
    binaries have different contracts, so one function cannot serve both.

    `endless-go` is an application surface. It resolves a database the way every
    other surface does — `--db main|sandbox`, with cwd supplying the address of a
    worktree's sandbox — and E-1668 gave it that vocabulary so the word a user
    types is the word the binary hears.

    `endless-migrate` is the opposite by design (ED-1571): a migration-only
    executable that links none of the application and resolves its target from
    what the caller NAMED, never from where it happens to be standing. So it
    takes a directory outright, through `internal/dbcontext`, and `--config-dir`
    is its flag rather than a retired one.

    Threading `--db main` to it would not be a rename, it would be an argument it
    cannot parse: `--db` survives the strip, lands in `args[1]` where the
    subcommand belongs, and the migration fails during a land.
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


def worktree_endless_go(cwd: Path | None = None) -> Path | None:
    """Path to <worktree>/bin/endless-go when cwd is inside a self-dev
    worktree, else None. Does not check existence.

    The unconditional form: it asks only "am I in a self-dev worktree", with no
    opinion about which database is in play. Two callers want different gates on
    top of it, so the path itself is computed once here.

    E-1891 added the second caller (`endless.statuses`) and with it the reason
    this is separate from resolved_worktree_endless_go below: that one cannot
    answer before the global --db flag has been parsed, and the status
    vocabulary is needed at cli.py IMPORT time, which is earlier than that. The
    looser gate is right for a caller that touches no database — the worktree's
    Python is already what runs here, so the worktree's Go is the coherent
    partner for a pure lookup.
    """
    dir_name = worktree_dir_name(cwd)
    if dir_name is None:
        return None
    root = gated_worktree_root(cwd)
    if root is None:
        return None
    return root / ".endless" / "worktrees" / dir_name / "bin" / "endless-go"


def resolved_worktree_endless_go(cwd: Path | None = None) -> Path | None:
    """Path to <worktree>/bin/endless-go when --db sandbox is the active DB
    context AND cwd is inside a self-dev worktree, else None.

    Used by event_bridge to prefer the worktree-built binary (which embeds
    the worktree's schema.sql) over the PATH-resolved global. The global
    symlink points at main's binary, so additive schema in this branch is
    silently absent unless the worktree binary is used.

    The --db sandbox gate is load-bearing HERE and deliberately absent from
    worktree_endless_go above: this path opens a database, so using the
    worktree binary against the MAIN database would reintroduce the exact
    schema-baseline mismatch the routing exists to prevent.

    Does not check existence — callers handle the missing-binary case
    explicitly and surface a loud error naming the bad state (E-1510).
    """
    if RESOLVED_CONFIG_DIR is None:
        return None
    dir_name = worktree_dir_name(cwd)
    if dir_name is None:
        return None
    # Only fire when RESOLVED_CONFIG_DIR is EXACTLY the sandbox path for this
    # worktree. Anything else (--db main, conftest's tmp config dir, a stray
    # external override) keeps the PATH-resolved global.
    if RESOLVED_CONFIG_DIR != sandbox_config_dir(dir_name):
        return None
    return worktree_endless_go(cwd)


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
    dir_name = worktree_dir_name(cwd)
    if dir_name is None:
        return None
    root = gated_worktree_root(cwd)
    if root is None:
        return None
    worktree = (root / ".endless" / "worktrees" / dir_name).resolve()
    src = (source_file if source_file is not None else Path(__file__)).resolve()
    try:
        src.relative_to(worktree)
    except ValueError:
        return worktree
    return None

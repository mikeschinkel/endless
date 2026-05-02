"""Matcher patterns: pivots, action regexes — sourced from config files.

Verbs were extracted from this module in E-1117. They live as their own
top-level `verbs` array of objects (`{value, definition, ...}`); see verb_cmd.py.
This module is now the home for non-verb pattern matchers (pivot, regex
command-patterns, channel matchers).

Two layers, merged additively at read time:

- Project: <project-root>/.endless/config.json under "matchers"
- Machine: ~/.config/endless/config.json under "matchers"

A matcher object:

    {
      "type":            <required, e.g. "pivot" | "start" | "complete" | ...>
      "scope":           <optional, e.g. "task" | "channel">
      "method":          <required, "exact" | "substring" | "regex">
      "match":           <required, list[str] for exact/substring, str for regex>
      "case_sensitive":  <optional bool, default false>
      "enabled":         <optional bool, default true>
    }

The verb-gate calls get_verbs() which reads from the top-level `verbs`
array. Pattern detection helpers (get_pivot_matchers, get_action_regex)
still read from `matchers`.
"""

import json
import re
from pathlib import Path
from typing import Any

from endless import config


# Default matchers seeded into the machine config on first run if no
# "matchers" property exists. Includes the verb whitelist that previously
# lived in src/endless/task_cmd.py:_TITLE_VERBS, the pivot triggers
# planned for the W3 behavioral gate, and the action regexes lifted from
# cmd/endless-hook/claude.go.
DEFAULT_MATCHERS: list[dict[str, Any]] = [
    {
        "type": "pivot",
        "method": "substring",
        "match": [
            "actually", "wait", "also can you", "by the way", "btw",
            "different", "switch to", "while you're at it", "instead",
            "and can you",
        ],
    },
    {
        "type": "pivot",
        "method": "substring",
        "case_sensitive": True,
        "match": ["PIVOT"],
    },
    {
        "type": "start", "scope": "task", "method": "regex",
        "match": r"endless\s+task\s+start\s+(?:[Ee]-)?(\d+)",
    },
    {
        "type": "complete", "scope": "task", "method": "regex",
        "match": r"endless\s+task\s+complete\s+(?:[Ee]-)?(\d+)",
    },
    {
        "type": "chat", "scope": "task", "method": "regex",
        "match": r"endless\s+task\s+chat",
    },
    {
        "type": "beacon", "scope": "channel", "method": "regex",
        "match": r"endless\s+channel\s+beacon",
    },
    {
        "type": "connect", "scope": "channel", "method": "regex",
        "match": r"endless\s+channel\s+connect\s+(\S+)",
    },
    {
        "type": "send", "scope": "channel", "method": "regex",
        "match": r"endless\s+channel\s+send",
    },
]


# --- Layer-aware path resolution -------------------------------------------

def _git_main_worktree_root(start: Path) -> Path | None:
    """Return the working dir of the git main checkout containing `start`.

    `git rev-parse --git-common-dir` returns the path to the shared `.git`
    directory regardless of which worktree we are inside. Its parent is the
    main checkout's working dir.

    Returns None if `start` is not inside a git repo (or git is unavailable).
    """
    import subprocess
    try:
        result = subprocess.run(
            ["git", "rev-parse", "--git-common-dir"],
            cwd=str(start),
            capture_output=True,
            text=True,
            check=True,
        )
    except (subprocess.CalledProcessError, FileNotFoundError, OSError):
        return None
    common_dir = Path(result.stdout.strip())
    if not common_dir.is_absolute():
        common_dir = (start / common_dir).resolve()
    return common_dir.parent


def project_config_path() -> Path | None:
    """Return <main-checkout>/.endless/config.json if cwd is in a project, else None.

    When cwd is inside a git worktree, resolves to the *main checkout's*
    .endless/config.json — not the worktree-local copy. This prevents
    project-layer config from silently diverging across worktrees and being
    lost when a worktree is removed.

    When cwd is not in a git repo, falls back to walking up from cwd.
    """
    cwd = Path.cwd()
    main_root = _git_main_worktree_root(cwd)
    if main_root is not None:
        candidate = main_root / ".endless" / "config.json"
        if candidate.exists():
            return candidate
        return None
    for parent in [cwd] + list(cwd.parents):
        candidate = parent / ".endless" / "config.json"
        if candidate.exists():
            return candidate
    return None


def machine_config_path() -> Path:
    """Path to the machine-layer config file."""
    return config.CONFIG_FILE


def project_verbs_path() -> Path | None:
    """Return <main-checkout>/.endless/verbs.json if cwd is in a project, else None.

    Co-located with project_config_path's resolution: writes always target
    the main checkout's working dir even when cwd is in a worktree (E-1111).
    Returns the path even if the file does not yet exist — callers may need
    to create it. Returns None only when cwd is not in a project at all.
    """
    cwd = Path.cwd()
    main_root = _git_main_worktree_root(cwd)
    if main_root is not None:
        if (main_root / ".endless" / "config.json").exists():
            return main_root / ".endless" / "verbs.json"
        return None
    for parent in [cwd] + list(cwd.parents):
        if (parent / ".endless" / "config.json").exists():
            return parent / ".endless" / "verbs.json"
    return None


def machine_verbs_path() -> Path:
    """Path to the machine-layer verbs file."""
    return config.CONFIG_DIR / "verbs.json"


# --- File IO ----------------------------------------------------------------

def _load_json(path: Path) -> dict:
    if not path.exists():
        return {}
    try:
        return json.loads(path.read_text())
    except (OSError, json.JSONDecodeError):
        return {}


def _save_json(path: Path, data: dict) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(data, indent=2) + "\n")


def _read_matchers_from(path: Path) -> list[dict]:
    data = _load_json(path)
    raw = data.get("matchers", [])
    return raw if isinstance(raw, list) else []


def _load_verbs_list(path: Path) -> list[dict]:
    """Load verbs from a verbs.json file (top-level array of objects)."""
    if not path.exists():
        return []
    try:
        raw = json.loads(path.read_text())
    except (OSError, json.JSONDecodeError):
        return []
    return raw if isinstance(raw, list) else []


def _save_verbs_list(path: Path, verbs: list[dict]) -> None:
    """Save verbs as a top-level JSON array."""
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(verbs, indent=2) + "\n")


DEFAULT_VERBS: list[dict[str, str]] = [
    {"value": "accept", "definition": "to receive or agree to"},
    {"value": "add", "definition": "to introduce or include something new"},
    {"value": "apply", "definition": "to put into effect"},
    {"value": "assume", "definition": "to take to be complete pending verification"},
    {"value": "audit", "definition": "to examine systematically"},
    {"value": "backfill", "definition": "to fill in missing data after the fact"},
    {"value": "build", "definition": "to construct or compile"},
    {"value": "capture", "definition": "to record or take in"},
    {"value": "change", "definition": "to alter"},
    {"value": "clean", "definition": "to remove unwanted state"},
    {"value": "clear", "definition": "to remove or empty"},
    {"value": "confirm", "definition": "to verify and finalize"},
    {"value": "configure", "definition": "to set options or parameters"},
    {"value": "consolidate", "definition": "to combine multiple things into one"},
    {"value": "convert", "definition": "to change form or representation"},
    {"value": "create", "definition": "to bring into existence"},
    {"value": "decide", "definition": "to make a determination"},
    {"value": "define", "definition": "to specify meaning or scope"},
    {"value": "defer", "definition": "to postpone"},
    {"value": "deploy", "definition": "to release for use"},
    {"value": "design", "definition": "to plan structure or behavior"},
    {"value": "disable", "definition": "to turn off or block"},
    {"value": "distinguish", "definition": "to make a difference between"},
    {"value": "document", "definition": "to record in writing"},
    {"value": "enable", "definition": "to turn on or allow"},
    {"value": "enforce", "definition": "to compel observance of"},
    {"value": "evaluate", "definition": "to assess"},
    {"value": "expand", "definition": "to make larger or more inclusive"},
    {"value": "extract", "definition": "to take out or pull from"},
    {"value": "fix", "definition": "to repair or correct"},
    {"value": "generate", "definition": "to produce"},
    {"value": "hide", "definition": "to conceal from view"},
    {"value": "implement", "definition": "to build the working form of"},
    {"value": "improve", "definition": "to make better"},
    {"value": "increase", "definition": "to raise in number or magnitude"},
    {"value": "integrate", "definition": "to combine into a working whole"},
    {"value": "investigate", "definition": "to examine in depth"},
    {"value": "merge", "definition": "to combine branches or items"},
    {"value": "migrate", "definition": "to move from one system to another"},
    {"value": "move", "definition": "to change location"},
    {"value": "omit", "definition": "to leave out intentionally"},
    {"value": "package", "definition": "to bundle for distribution"},
    {"value": "print", "definition": "to output to stdout or paper"},
    {"value": "prune", "definition": "to remove unwanted parts"},
    {"value": "raise", "definition": "to lift or signal (as in raise an error)"},
    {"value": "read", "definition": "to examine and interpret"},
    {"value": "reconcile", "definition": "to bring into agreement"},
    {"value": "redesign", "definition": "to design again"},
    {"value": "refactor", "definition": "to restructure code without changing behavior"},
    {"value": "remove", "definition": "to take away"},
    {"value": "rename", "definition": "to give a new name"},
    {"value": "render", "definition": "to produce visual or textual output"},
    {"value": "replace", "definition": "to substitute"},
    {"value": "require", "definition": "to demand as necessary"},
    {"value": "research", "definition": "to investigate systematically"},
    {"value": "resolve", "definition": "to settle or fix"},
    {"value": "search", "definition": "to look for"},
    {"value": "show", "definition": "to display"},
    {"value": "simplify", "definition": "to make simpler"},
    {"value": "skip", "definition": "to bypass"},
    {"value": "split", "definition": "to divide into parts"},
    {"value": "support", "definition": "to provide for or assist with"},
    {"value": "surface", "definition": "to bring to attention"},
    {"value": "sync", "definition": "to bring into alignment"},
    {"value": "test", "definition": "to check behavior or correctness"},
    {"value": "track", "definition": "to follow or monitor"},
    {"value": "update", "definition": "to revise"},
    {"value": "validate", "definition": "to confirm correctness"},
    {"value": "verify", "definition": "to check truth or accuracy"},
]


def _ensure_default_seeds() -> None:
    """Seed defaults into the machine layer when missing.

    Idempotent. Matchers seed into ~/.config/endless/config.json; verbs seed
    into the separate ~/.config/endless/verbs.json file (E-1124). Project
    layer is never auto-seeded.
    """
    cfg_path = machine_config_path()
    cfg_data = _load_json(cfg_path)
    if "matchers" not in cfg_data:
        cfg_data["matchers"] = DEFAULT_MATCHERS
        _save_json(cfg_path, cfg_data)

    verbs_path = machine_verbs_path()
    if not verbs_path.exists():
        _save_verbs_list(verbs_path, DEFAULT_VERBS)


def _migrate_verbs_to_separate_file(config_path: Path, verbs_path: Path) -> None:
    """One-time: extract verbs from a config.json file into a sibling verbs.json.

    Handles two pre-E-1124 shapes that may exist in `config_path`:

      1. Pre-E-1117: a `type=verb` matcher entry inside `matchers`, optionally
         with a sibling `definitions: {value: def}` map (E-1108 leftover).
      2. Post-E-1117 / pre-E-1124: a top-level `verbs: [{value, definition}]`
         array key on the config object.

    Both are extracted into the `verbs.json` file at `verbs_path` (top-level
    JSON array), deduplicated by value (existing verbs.json entries take
    precedence on conflict). The corresponding fields are removed from
    `config_path` afterwards.

    Idempotent. No-op when `config_path` doesn't exist, has no migration-
    eligible content, or the config layer is empty.
    """
    if not config_path.exists():
        return
    data = _load_json(config_path)
    matchers = data.get("matchers")
    inline_verbs = data.get("verbs")

    has_verb_matcher = (
        isinstance(matchers, list)
        and any(isinstance(m, dict) and m.get("type") == "verb" for m in matchers)
    )
    has_inline_verbs = isinstance(inline_verbs, list) and inline_verbs
    if not has_verb_matcher and not has_inline_verbs:
        return

    existing_verbs = _load_verbs_list(verbs_path)
    seen = {v.get("value") for v in existing_verbs if isinstance(v, dict)}

    # 1. Migrate inline verbs: array
    if has_inline_verbs:
        for v in inline_verbs:
            if not isinstance(v, dict):
                continue
            value = v.get("value")
            if not isinstance(value, str) or value in seen:
                continue
            existing_verbs.append(v)
            seen.add(value)

    # 2. Migrate verb matchers (with optional `definitions` map)
    if has_verb_matcher:
        for m in matchers:
            if not isinstance(m, dict) or m.get("type") != "verb":
                continue
            match_list = m.get("match", []) or []
            defs = m.get("definitions") or {}
            if not isinstance(defs, dict):
                defs = {}
            for value in match_list:
                if not isinstance(value, str) or value in seen:
                    continue
                entry: dict[str, str] = {"value": value}
                if value in defs and isinstance(defs[value], str):
                    entry["definition"] = defs[value]
                existing_verbs.append(entry)
                seen.add(value)

    _save_verbs_list(verbs_path, existing_verbs)

    # Strip the migrated content out of config_path
    if isinstance(matchers, list):
        data["matchers"] = [m for m in matchers if not (isinstance(m, dict) and m.get("type") == "verb")]
    if "verbs" in data:
        del data["verbs"]
    _save_json(config_path, data)


# Maps (type, scope, method) -> {old_match: new_match}. Only rewrites when
# the existing match is EXACTLY the old default — leaves any user
# customization alone. Each entry is a one-way migration; once a config is
# migrated, the new pattern won't re-trigger on subsequent runs.
_STALE_DEFAULTS: dict[tuple, dict[str, str]] = {
    ("start", "task", "regex"): {
        # E-1028: original pattern only matched bare integers; CLI accepts
        # E-prefixed IDs, so 'endless task start E-1027' was a silent no-op.
        r"endless\s+task\s+start\s+(\d+)":
            r"endless\s+task\s+start\s+(?:[Ee]-)?(\d+)",
    },
    ("complete", "task", "regex"): {
        r"endless\s+task\s+complete\s+(\d+)":
            r"endless\s+task\s+complete\s+(?:[Ee]-)?(\d+)",
    },
}


def _migrate_stale_defaults() -> None:
    """Rewrite known-stale default matchers in the machine config in place.

    Idempotent. Only rewrites matchers whose `match` field is exactly the
    old default value; user-customized matchers are left untouched.
    """
    path = machine_config_path()
    data = _load_json(path)
    matchers = data.get("matchers")
    if not isinstance(matchers, list):
        return
    changed = False
    for m in matchers:
        key = (m.get("type"), m.get("scope"), m.get("method"))
        rewrites = _STALE_DEFAULTS.get(key)
        if not rewrites:
            continue
        if m.get("match") in rewrites:
            m["match"] = rewrites[m["match"]]
            changed = True
    if changed:
        _save_json(path, data)


# --- Matcher identity / merge logic ----------------------------------------

def _matcher_signature(m: dict) -> tuple:
    """Stable identity for grouping matchers that can share a 'match' list.

    Two matchers with the same (type, scope, method, case_sensitive,
    enabled-or-default) can be merged into one entry by extending the
    match list. Regex matchers are single-string and never merge.
    """
    return (
        m.get("type"),
        m.get("scope"),
        m.get("method"),
        bool(m.get("case_sensitive", False)),
        bool(m.get("enabled", True)),
    )


def _is_regex(m: dict) -> bool:
    return m.get("method") == "regex"


# --- Public load API --------------------------------------------------------

def load_all_matchers() -> list[dict]:
    """Project + machine matchers merged additively, with defaults seeded.

    Returns a flat list. Duplicates across layers (same signature + same
    match value) are de-duplicated.
    """
    _ensure_default_seeds()
    _migrate_stale_defaults()
    project_path = project_config_path()
    project_vp = project_verbs_path()
    if project_path is not None and project_vp is not None:
        _migrate_verbs_to_separate_file(project_path, project_vp)
    _migrate_verbs_to_separate_file(machine_config_path(), machine_verbs_path())
    project = _read_matchers_from(project_path) if project_path else []
    machine = _read_matchers_from(machine_config_path())

    # Merge: project entries take precedence in iteration order, but we
    # merge match lists with same signature instead of dropping.
    by_sig: dict[tuple, dict] = {}
    order: list[tuple] = []

    def absorb(m: dict) -> None:
        sig = _matcher_signature(m)
        if sig not in by_sig:
            by_sig[sig] = json.loads(json.dumps(m))  # deep copy
            order.append(sig)
            return
        existing = by_sig[sig]
        if _is_regex(m):
            return  # regex matchers are single-string; first one wins
        ex_match = existing.get("match", [])
        new_match = m.get("match", [])
        if isinstance(ex_match, list) and isinstance(new_match, list):
            for v in new_match:
                if v not in ex_match:
                    ex_match.append(v)
            existing["match"] = ex_match

    for m in project + machine:
        absorb(m)

    return [by_sig[s] for s in order]


# --- Lookup helpers consumed by validate_title, hooks, etc. ----------------

def load_all_verbs() -> list[dict]:
    """Project + machine verbs merged additively, deduplicated by value.

    Project entries take precedence on conflict (same value, different
    definition). Triggers migration + default seeding via load_all_matchers.
    Reads from .endless/verbs.json (project) and ~/.config/endless/verbs.json
    (machine) per E-1124.
    """
    load_all_matchers()
    project = _load_verbs_list(project_verbs_path()) if project_verbs_path() else []
    machine = _load_verbs_list(machine_verbs_path())

    seen: set[str] = set()
    out: list[dict] = []
    for v in project + machine:
        if not isinstance(v, dict):
            continue
        value = v.get("value")
        if not isinstance(value, str) or value in seen:
            continue
        seen.add(value)
        out.append(v)
    return out


def get_verbs() -> set[str]:
    """Return the lowercased set of enabled verb tokens (case-folded).

    Reads from the top-level `verbs` array. Verbs are case-insensitive by
    convention; case-sensitivity is not currently surfaced per-verb.
    """
    out: set[str] = set()
    for v in load_all_verbs():
        if v.get("enabled", True) is False:
            continue
        value = v.get("value")
        if isinstance(value, str):
            out.add(value.lower())
    return out


def get_verb_definition(value: str) -> str | None:
    """Return the definition for a verb, or None if missing or unknown."""
    target = value.lower()
    for v in load_all_verbs():
        candidate = v.get("value")
        if isinstance(candidate, str) and candidate.lower() == target:
            d = v.get("definition")
            return d if isinstance(d, str) else None
    return None


def get_pivot_matchers() -> list[dict]:
    """Return enabled pivot matchers in match-evaluation order."""
    return [
        m for m in load_all_matchers()
        if m.get("type") == "pivot" and m.get("enabled", True) is not False
    ]


def get_action_regex(action_type: str, scope: str) -> re.Pattern | None:
    """Return the compiled regex for a (type, scope) action matcher, or None."""
    for m in load_all_matchers():
        if m.get("type") != action_type:
            continue
        if m.get("scope") != scope:
            continue
        if m.get("enabled", True) is False:
            continue
        if m.get("method") != "regex":
            continue
        pattern = m.get("match")
        if not isinstance(pattern, str):
            continue
        try:
            return re.compile(pattern)
        except re.error:
            return None
    return None


# --- Mutation API used by the phrase CLI -----------------------------------

def add_match_value(
    *,
    type_: str,
    value: str,
    scope: str | None = None,
    method: str = "exact",
    case_sensitive: bool = False,
    machine_only: bool = False,
) -> tuple[bool, bool]:
    """Add a matcher value to the appropriate config files.

    Returns (wrote_project, wrote_machine). Either may be False if the
    value was already present (no-op) or if writing was skipped (e.g.,
    no project config and machine_only=False).
    """
    if type_ == "verb":
        raise ValueError(
            "verbs are no longer matchers; use add_verb() (E-1117)"
        )
    matcher_template = {
        "type": type_,
        "method": method,
    }
    if scope:
        matcher_template["scope"] = scope
    if case_sensitive:
        matcher_template["case_sensitive"] = True

    if method == "regex":
        matcher_template["match"] = value
    else:
        matcher_template["match"] = [value]

    wrote_project = False
    wrote_machine = False

    project_path = project_config_path()
    if project_path is not None and not machine_only:
        wrote_project = _add_to_file(project_path, matcher_template)

    wrote_machine = _add_to_file(machine_config_path(), matcher_template)

    return wrote_project, wrote_machine


def _add_to_file(path: Path, new_matcher: dict) -> bool:
    """Add a matcher to a config file, merging with same-signature entries.

    Returns True if anything was written, False if the value was already
    present (no-op).
    """
    data = _load_json(path)
    matchers_list = data.setdefault("matchers", [])
    if not isinstance(matchers_list, list):
        # Corrupted property; reset
        matchers_list = []
        data["matchers"] = matchers_list

    new_sig = _matcher_signature(new_matcher)
    target = None
    for m in matchers_list:
        if isinstance(m, dict) and _matcher_signature(m) == new_sig:
            target = m
            break

    if _is_regex(new_matcher):
        if target is None:
            matchers_list.append(new_matcher)
            _save_json(path, data)
            return True
        # Regex single-string: refuse silent overwrite of a different value
        return False

    new_values = new_matcher.get("match", [])
    if target is None:
        matchers_list.append(new_matcher)
        _save_json(path, data)
        return True

    target_match = target.setdefault("match", [])
    if not isinstance(target_match, list):
        return False
    added = False
    for v in new_values:
        if v not in target_match:
            target_match.append(v)
            added = True
    if added:
        _save_json(path, data)
    return added


def remove_match_value(
    *,
    type_: str,
    value: str,
    scope: str | None = None,
    machine_only: bool = False,
) -> tuple[int, int]:
    """Remove a value from matchers across both layers.

    Returns (project_removals, machine_removals).
    """
    pr = 0
    mr = 0
    project_path = project_config_path()
    if project_path is not None and not machine_only:
        pr = _remove_from_file(project_path, type_, value, scope)
    mr = _remove_from_file(machine_config_path(), type_, value, scope)
    return pr, mr


def _remove_from_file(path: Path, type_: str, value: str, scope: str | None) -> int:
    data = _load_json(path)
    matchers_list = data.get("matchers", [])
    if not isinstance(matchers_list, list):
        return 0
    removed = 0
    for m in list(matchers_list):
        if not isinstance(m, dict):
            continue
        if m.get("type") != type_ or m.get("scope") != scope:
            continue
        match = m.get("match")
        if isinstance(match, str):
            if match == value:
                matchers_list.remove(m)
                removed += 1
        elif isinstance(match, list):
            if value in match:
                match.remove(value)
                removed += 1
                if not match:
                    matchers_list.remove(m)
    if removed:
        _save_json(path, data)
    return removed


def set_enabled(
    *,
    type_: str,
    value: str,
    enabled: bool,
    scope: str | None = None,
) -> tuple[int, int]:
    """Toggle the enabled flag on the entry holding `value`.

    For multi-value (exact/substring) matchers, this splits the entry: the
    target value moves to a separate matcher object with the new enabled
    flag, leaving siblings untouched. Returns (project_changes, machine_changes).
    """
    project_path = project_config_path()
    pr = _toggle_in_file(project_path, type_, value, scope, enabled) if project_path else 0
    mr = _toggle_in_file(machine_config_path(), type_, value, scope, enabled)
    return pr, mr


def _toggle_in_file(
    path: Path | None, type_: str, value: str, scope: str | None, enabled: bool
) -> int:
    if path is None:
        return 0
    data = _load_json(path)
    matchers_list = data.get("matchers", [])
    if not isinstance(matchers_list, list):
        return 0
    changes = 0
    for m in list(matchers_list):
        if not isinstance(m, dict):
            continue
        if m.get("type") != type_ or m.get("scope") != scope:
            continue
        match = m.get("match")
        current = m.get("enabled", True)
        if isinstance(match, str):
            if match == value and bool(current) != enabled:
                m["enabled"] = enabled
                changes += 1
        elif isinstance(match, list):
            if value not in match:
                continue
            if bool(current) == enabled:
                continue
            # Split: remove value from existing entry; add new entry with toggled enabled
            match.remove(value)
            new_entry = {k: v for k, v in m.items() if k != "match"}
            new_entry["enabled"] = enabled
            new_entry["match"] = [value]
            matchers_list.append(new_entry)
            if not match:
                matchers_list.remove(m)
            changes += 1
    if changes:
        _save_json(path, data)
    return changes


# --- Verb mutation API (E-1117 / E-1124) -----------------------------------

def add_verb(
    *,
    value: str,
    definition: str,
    machine_only: bool = False,
) -> tuple[bool, bool]:
    """Add a verb to the appropriate verbs.json files.

    Returns (wrote_project, wrote_machine). Either may be False if the
    value was already present (no-op) or writing was skipped (e.g.,
    no project at cwd and machine_only=False).
    """
    if not value or not value.strip():
        raise ValueError("verb value is required")
    if not definition or not definition.strip():
        raise ValueError("verb definition is required")
    entry = {"value": value.strip(), "definition": definition.strip()}

    wrote_project = False
    wrote_machine = False
    project_vp = project_verbs_path()
    if project_vp is not None and not machine_only:
        wrote_project = _add_verb_to_file(project_vp, entry)
    wrote_machine = _add_verb_to_file(machine_verbs_path(), entry)
    return wrote_project, wrote_machine


def _add_verb_to_file(path: Path, entry: dict) -> bool:
    """Append a verb entry to the verbs.json at `path`. No-op if value exists."""
    verbs = _load_verbs_list(path)
    for v in verbs:
        if isinstance(v, dict) and v.get("value") == entry["value"]:
            return False
    verbs.append(entry)
    _save_verbs_list(path, verbs)
    return True


def remove_verb(*, value: str, machine_only: bool = False) -> tuple[int, int]:
    """Remove a verb from the appropriate verbs.json files.

    Returns (project_removals, machine_removals).
    """
    pr = 0
    mr = 0
    project_vp = project_verbs_path()
    if project_vp is not None and not machine_only:
        pr = _remove_verb_from_file(project_vp, value)
    mr = _remove_verb_from_file(machine_verbs_path(), value)
    return pr, mr


def _remove_verb_from_file(path: Path, value: str) -> int:
    """Remove all entries with matching `value` from verbs.json at `path`."""
    if not path.exists():
        return 0
    verbs = _load_verbs_list(path)
    keep: list[dict] = []
    removed = 0
    for v in verbs:
        if isinstance(v, dict) and v.get("value") == value:
            removed += 1
            continue
        keep.append(v)
    if removed:
        _save_verbs_list(path, keep)
    return removed

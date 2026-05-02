"""Matcher patterns: verbs, pivots, action regexes — sourced from
config files (not the DB).

Two layers, merged additively at read time:

- Project: <project-root>/.endless/config.json under "matchers"
- Machine: ~/.config/endless/config.json under "matchers"

A matcher object:

    {
      "type":            <required, e.g. "verb" | "pivot" | "start" | ...>
      "scope":           <optional, e.g. "task" | "channel">
      "method":          <required, "exact" | "substring" | "regex">
      "match":           <required, list[str] for exact/substring, str for regex>
      "case_sensitive":  <optional bool, default false>
      "enabled":         <optional bool, default true>
    }

This module owns load/save, default seeding, and lookup helpers consumed
by validate_title, the future UserPromptSubmit hook (W3), and any other
matching consumer.
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
        "type": "verb",
        "method": "exact",
        "match": [
            "accept", "add", "apply", "assume", "audit", "backfill",
            "build", "capture", "change", "clean", "clear", "confirm",
            "configure", "consolidate", "convert", "create", "decide",
            "define", "defer", "deploy", "design", "disable",
            "distinguish", "document", "enable", "enforce", "evaluate",
            "expand", "extract", "fix", "generate", "implement",
            "improve", "integrate", "investigate", "hide", "increase",
            "merge", "migrate", "move", "omit", "package", "print",
            "prune", "raise", "read", "reconcile", "redesign",
            "refactor", "remove", "rename", "render", "replace",
            "require", "research", "resolve", "search", "show",
            "simplify", "skip", "split", "support", "surface", "sync",
            "test", "track", "update", "validate", "verify",
        ],
    },
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

def project_config_path() -> Path | None:
    """Return <project-root>/.endless/config.json if cwd is in a project, else None.

    Walks up from cwd looking for .endless/config.json. The first match is
    treated as the project root. Returns None if no project found above cwd.
    """
    cwd = Path.cwd()
    for parent in [cwd] + list(cwd.parents):
        candidate = parent / ".endless" / "config.json"
        if candidate.exists():
            return candidate
    return None


def machine_config_path() -> Path:
    """Path to the machine-layer config file."""
    return config.CONFIG_FILE


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


def _ensure_default_seeds() -> None:
    """If the machine config has no 'matchers' property, write the defaults.

    Idempotent: subsequent calls are no-ops once the property exists. Project
    config is never auto-seeded; users add to it explicitly via 'phrase add'.
    """
    path = machine_config_path()
    data = _load_json(path)
    if "matchers" in data:
        return
    data["matchers"] = DEFAULT_MATCHERS
    _save_json(path, data)


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

def get_verbs() -> set[str]:
    """Return the lowercased set of enabled verb tokens (case-folded)."""
    out: set[str] = set()
    for m in load_all_matchers():
        if m.get("type") != "verb":
            continue
        if m.get("enabled", True) is False:
            continue
        if m.get("method") != "exact":
            continue
        cs = bool(m.get("case_sensitive", False))
        for v in m.get("match", []) or []:
            out.add(v if cs else v.lower())
    return out


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
    definition: str | None = None,
) -> tuple[bool, bool]:
    """Add a matcher value to the appropriate config files.

    Returns (wrote_project, wrote_machine). Either may be False if the
    value was already present (no-op) or if writing was skipped (e.g.,
    no project config and machine_only=False).

    If `definition` is provided (only meaningful for verb-type, exact-method
    matchers), it is stored in a sibling `definitions` map keyed by value.
    """
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

    if definition and method != "regex":
        matcher_template["definitions"] = {value: definition.strip()}

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

    new_defs = new_matcher.get("definitions")
    if isinstance(new_defs, dict) and new_defs:
        target_defs = target.setdefault("definitions", {})
        if isinstance(target_defs, dict):
            for k, v in new_defs.items():
                if k not in target_defs:
                    target_defs[k] = v
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

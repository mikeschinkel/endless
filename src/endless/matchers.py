"""Verb registry — sourced from layered verbs.jsonl files.

Verbs were extracted from matcher config in E-1117 and moved to their own files
in E-1124 (JSONL per E-1268); see verb_cmd.py. The pattern-matcher half this
module once hosted was removed in E-2180.

Two layers, plus the built-in `DEFAULT_VERBS`, resolved field-wise at read time:

- Project: <project-root>/.endless/verbs.jsonl
- Machine: ~/.config/endless/verbs.jsonl

The verb-gate calls get_verbs(); category and definition lookups share the same
route (`_resolved_verbs`).
"""

import json
from pathlib import Path
from typing import Any, NamedTuple

from endless import config, main_commit
from endless.project_path import project_root


# --- Layer-aware path resolution -------------------------------------------

def project_config_path() -> Path | None:
    """Return the nearest .endless/config.json walking up from cwd, else None.

    Per E-1140 (which reverses E-1112): writes from a worktree go to the
    worktree's own .endless/config.json; writes from main go to main's. The
    worktree's copy is checked out from main on `git worktree add`, so it
    exists from the moment the worktree is created. Cross-worktree dedup
    (e.g. for verbs) is handled at land time, not at write time.
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


def project_verbs_path() -> Path | None:
    """Return the registered project's .endless/verbs.jsonl path, else None.

    Diverges from project_config_path's cwd-walk-up resolution. Per E-1208,
    the verb registry is global config: writes route to main's tree (the
    registered project path) regardless of which worktree the caller is in,
    and a single-file commit lands directly on main. Worktree-local copies
    are dead weight after E-1208 — read paths consult main's file, not the
    worktree's local one.

    Returns None when no project is registered for cwd (typical at first
    install or in a non-project directory). Callers must handle None as a
    machine-layer-only operation.

    E-1268: file is JSONL (line-per-entry) with `merge=union` in
    .gitattributes so concurrent appends merge cleanly.
    """
    root = project_root()
    if root is None:
        return None
    return root / ".endless" / "verbs.jsonl"


def machine_verbs_path() -> Path:
    """Path to the machine-layer verbs file."""
    return config.CONFIG_DIR / "verbs.jsonl"


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


def _legacy_json_path_for(jsonl_path: Path) -> Path:
    """Return the legacy verbs.json path co-located with a verbs.jsonl path."""
    return jsonl_path.with_name("verbs.json")


def _load_verbs_list(path: Path) -> list[dict]:
    """Load verbs from a verbs.jsonl file (one JSON object per line).

    E-1268 migration: if a legacy verbs.json (top-level array) sits next to
    the jsonl path, its entries are merged in and the .json file is removed.
    Same-`value` duplicates resolved against the jsonl entries (jsonl wins).
    """
    legacy = _legacy_json_path_for(path)
    legacy_entries: list[dict] = []
    if legacy.exists():
        try:
            raw = json.loads(legacy.read_text())
            if isinstance(raw, list):
                legacy_entries = [e for e in raw if isinstance(e, dict)]
        except (OSError, json.JSONDecodeError):
            legacy_entries = []

    entries: list[dict] = []
    if path.exists():
        try:
            for line in path.read_text().splitlines():
                line = line.strip()
                if not line:
                    continue
                try:
                    obj = json.loads(line)
                except json.JSONDecodeError:
                    continue
                if isinstance(obj, dict):
                    entries.append(obj)
        except OSError:
            entries = []

    if legacy_entries:
        seen = {e.get("value") for e in entries if isinstance(e, dict)}
        for e in legacy_entries:
            v = e.get("value")
            if v not in seen:
                entries.append(e)
                seen.add(v)
        _save_verbs_list(path, entries)
        try:
            legacy.unlink()
        except OSError:
            pass

    return entries


def _save_verbs_list(path: Path, verbs: list[dict]) -> None:
    """Save verbs as JSONL (one JSON object per line).

    E-1268: line-oriented format so concurrent appends auto-merge via
    `merge=union` in .gitattributes.
    """
    path.parent.mkdir(parents=True, exist_ok=True)
    lines = [json.dumps(v, separators=(", ", ": ")) for v in verbs]
    path.write_text("\n".join(lines) + ("\n" if lines else ""))


def _resolved_verbs() -> list[dict]:
    """The single verb-resolution route (E-2079): project + machine +
    `DEFAULT_VERBS`, layered with FIELD-WISE fall-through per value
    (case-insensitive). Each field is taken from the highest-priority layer that
    specifies it, so a verb auto-registered into a project as `{value,
    definition}` still inherits its `category` (and any other field) from a lower
    layer rather than shadowing it.

    Replaces the former first-non-empty-source behavior, under which a fresh
    project's single auto-registered verb shadowed all `DEFAULT_VERBS` — which,
    once the type gate moved to creation time, turned into a front-door lockout
    (`task add 'Research …' --type research` refused because 'research' resolved
    to no category). All readers (`get_verbs`, `get_verb_definition`,
    `verb_categories`, `load_all_verbs`) share this one route, so existence,
    definition, and category can never disagree about which layer wins.

    Triggers migration + default seeding via `_prepare_verb_layers`, matching
    the former `load_all_verbs`.
    """
    _prepare_verb_layers()
    layers: list[list[dict]] = []
    proj_path = project_verbs_path()
    if proj_path is not None:
        layers.append(_load_verbs_list(proj_path))
    layers.append(_load_verbs_list(machine_verbs_path()))
    layers.append(DEFAULT_VERBS)

    order: list[str] = []
    merged: dict[str, dict] = {}
    for layer in layers:                     # high → low priority
        for entry in layer:
            if not isinstance(entry, dict):
                continue
            value = entry.get("value")
            if not isinstance(value, str):
                continue
            key = value.lower()
            if key not in merged:
                order.append(key)
                merged[key] = dict(entry)
            else:
                for field, val in entry.items():
                    merged[key].setdefault(field, val)   # lower layer fills gaps
    return [merged[key] for key in order]


# The two verb categories (E-1658). A verb's `category` field is a subset of
# these. "investigation" verbs lead findings/decision/knowledge deliverables —
# their tasks terminate via 'completed'; "action" verbs lead changed-behavior or
# artifact work. Genuine duals (e.g. design, document) carry both. Absence of the
# field defaults to {"action"}, mirroring the old completable convention where an
# absent flag meant "not completable" (i.e. an action verb). This replaces
# E-1240's boolean `completable` (completable:true ⇔ category contains
# 'investigation').
VERB_CATEGORIES: frozenset[str] = frozenset({"action", "investigation"})
DEFAULT_VERB_CATEGORY: frozenset[str] = frozenset({"action"})


def verb_categories(verb: str) -> frozenset[str]:
    """Return the category set for `verb`, resolved through the single layered
    route (`_resolved_verbs`: project + machine + defaults, field-wise), matched
    case-insensitively.

    A verb with no `category` field in any layer — or an unknown/empty verb —
    defaults to {"action"}. A bare string category is accepted as a one-element
    set. Unrecognized tokens are dropped; if that leaves the set empty it falls
    back to the default so a malformed entry never yields an un-typed verb.
    """
    if not verb:
        return DEFAULT_VERB_CATEGORY
    target = verb.strip().lower()
    for entry in _resolved_verbs():
        if not isinstance(entry, dict):
            continue
        if str(entry.get("value", "")).lower() == target:
            raw = entry.get("category")
            if raw is None:
                return DEFAULT_VERB_CATEGORY
            if isinstance(raw, str):
                raw = [raw]
            if not isinstance(raw, (list, tuple, set)):
                return DEFAULT_VERB_CATEGORY
            cats = frozenset(
                str(c).strip().lower() for c in raw if isinstance(c, str)
            ) & VERB_CATEGORIES
            return cats or DEFAULT_VERB_CATEGORY
    return DEFAULT_VERB_CATEGORY


DEFAULT_VERBS: list[dict] = [
    {"value": "accept", "definition": "to receive or agree to"},
    {"value": "add", "definition": "to introduce or include something new"},
    {"value": "analyze", "definition": "to break down systematically into components", "category": ["investigation"]},
    {"value": "apply", "definition": "to put into effect"},
    {"value": "assess", "definition": "to form a judgment about", "category": ["investigation"]},
    {"value": "assume", "definition": "to take to be complete pending verification"},
    {"value": "audit", "definition": "to examine systematically", "category": ["investigation"]},
    {"value": "backfill", "definition": "to fill in missing data after the fact"},
    {"value": "build", "definition": "to construct or compile"},
    {"value": "capture", "definition": "to record or take in"},
    {"value": "change", "definition": "to alter"},
    {"value": "clean", "definition": "to remove unwanted state"},
    {"value": "clear", "definition": "to remove or empty"},
    {"value": "compare", "definition": "to identify similarities and differences", "category": ["investigation"]},
    {"value": "confirm", "definition": "to verify and finalize"},
    {"value": "configure", "definition": "to set options or parameters"},
    {"value": "consolidate", "definition": "to combine multiple things into one"},
    {"value": "convert", "definition": "to change form or representation"},
    {"value": "create", "definition": "to bring into existence"},
    {"value": "decide", "definition": "to make a determination", "category": ["investigation"]},
    {"value": "define", "definition": "to specify meaning or scope"},
    {"value": "defer", "definition": "to postpone"},
    {"value": "deploy", "definition": "to release for use"},
    {"value": "design", "definition": "to plan structure or behavior", "category": ["action", "investigation"]},
    {"value": "diagnose", "definition": "to identify the cause of a problem", "category": ["investigation"]},
    {"value": "disable", "definition": "to turn off or block"},
    {"value": "distinguish", "definition": "to make a difference between"},
    {"value": "document", "definition": "to record in writing", "category": ["action", "investigation"]},
    {"value": "enable", "definition": "to turn on or allow"},
    {"value": "enforce", "definition": "to compel observance of"},
    {"value": "evaluate", "definition": "to assess", "category": ["investigation"]},
    {"value": "examine", "definition": "to inspect closely", "category": ["investigation"]},
    {"value": "expand", "definition": "to make larger or more inclusive"},
    {"value": "explore", "definition": "to investigate possibilities in a space", "category": ["investigation"]},
    {"value": "extract", "definition": "to take out or pull from"},
    {"value": "fix", "definition": "to repair or correct"},
    {"value": "generate", "definition": "to produce"},
    {"value": "hide", "definition": "to conceal from view"},
    {"value": "implement", "definition": "to build the working form of"},
    {"value": "improve", "definition": "to make better"},
    {"value": "increase", "definition": "to raise in number or magnitude"},
    {"value": "integrate", "definition": "to combine into a working whole"},
    {"value": "investigate", "definition": "to examine in depth", "category": ["investigation"]},
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
    {"value": "redesign", "definition": "to design again", "category": ["action", "investigation"]},
    {"value": "refactor", "definition": "to restructure code without changing behavior"},
    {"value": "remove", "definition": "to take away"},
    {"value": "rename", "definition": "to give a new name"},
    {"value": "render", "definition": "to produce visual or textual output"},
    {"value": "replace", "definition": "to substitute"},
    {"value": "require", "definition": "to demand as necessary"},
    {"value": "research", "definition": "to investigate systematically", "category": ["investigation"]},
    {"value": "resolve", "definition": "to settle or fix"},
    {"value": "review", "definition": "to examine critically and form a judgment", "category": ["investigation"]},
    {"value": "search", "definition": "to look for"},
    {"value": "show", "definition": "to display"},
    {"value": "simplify", "definition": "to make simpler"},
    {"value": "skip", "definition": "to bypass"},
    {"value": "split", "definition": "to divide into parts"},
    {"value": "support", "definition": "to provide for or assist with"},
    {"value": "surface", "definition": "to bring to attention"},
    {"value": "survey", "definition": "to take stock of a landscape", "category": ["investigation"]},
    {"value": "sync", "definition": "to bring into alignment"},
    {"value": "test", "definition": "to check behavior or correctness"},
    {"value": "track", "definition": "to follow or monitor"},
    {"value": "triage", "definition": "to sort and prioritize by urgency", "category": ["investigation"]},
    {"value": "update", "definition": "to revise"},
    {"value": "validate", "definition": "to confirm correctness"},
    {"value": "verify", "definition": "to check truth or accuracy"},
]


def _ensure_default_seeds() -> None:
    """Seed `DEFAULT_VERBS` into the machine verbs.jsonl when it is missing.

    Idempotent. The file is ~/.config/endless/verbs.jsonl (E-1124, JSONL per
    E-1268). Project layer is never auto-seeded.
    """
    verbs_path = machine_verbs_path()
    if not verbs_path.exists():
        _save_verbs_list(verbs_path, DEFAULT_VERBS)


def _migrate_verbs_to_separate_file(config_path: Path, verbs_path: Path) -> None:
    """One-time: extract verbs from a config.json file into a sibling verbs.jsonl.

    Handles two pre-E-1124 shapes that may exist in `config_path`:

      1. Pre-E-1117: a `type=verb` matcher entry inside `matchers`, optionally
         with a sibling `definitions: {value: def}` map (E-1108 leftover).
      2. Post-E-1117 / pre-E-1124: a top-level `verbs: [{value, definition}]`
         array key on the config object.

    Both are extracted into the `verbs.jsonl` file at `verbs_path` (one JSON
    object per line, JSONL per E-1268), deduplicated by value (existing
    verbs.jsonl entries take precedence on conflict). The corresponding fields
    are removed from `config_path` afterwards.

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


def _prepare_verb_layers() -> None:
    """Seed the machine verbs file and migrate any pre-E-1124 verbs out of the
    project and machine config.json files. Idempotent; run before every read.
    """
    _ensure_default_seeds()
    project_path = project_config_path()
    project_vp = project_verbs_path()
    if project_path is not None and project_vp is not None:
        _migrate_verbs_to_separate_file(project_path, project_vp)
    _migrate_verbs_to_separate_file(machine_config_path(), machine_verbs_path())


# --- Lookup helpers consumed by validate_title, hooks, etc. ----------------

def load_all_verbs() -> list[dict]:
    """All active verbs, resolved once through the single layered route
    (project + machine + `DEFAULT_VERBS`, field-wise fall-through). Public alias
    for `_resolved_verbs` (E-2079 unified the two former resolution routes so
    `verb list`, existence, definition, and category all agree). E-1124 layer
    files; JSONL per E-1268.
    """
    return _resolved_verbs()


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


# --- Verb mutation API (E-1117 / E-1124) -----------------------------------

def _normalize_categories(category: list[str] | None) -> list[str]:
    """Canonicalize a category argument: lowercased, de-duped, stably ordered,
    and narrowed to `VERB_CATEGORIES`.

    Unrecognized tokens are dropped rather than raising, so the empty list is
    the single "nothing usable was passed" answer for both callers. `add_verb`
    reads it as "omit the field" (which resolves back to {"action"});
    `update_verb` reads it as an error, because an update that silently dropped
    the field would leave the verb miscategorized exactly as before.
    """
    if not category:
        return []
    seen: set[str] = set()
    cats: list[str] = []
    for c in category:
        token = str(c).strip().lower()
        if token in VERB_CATEGORIES and token not in seen:
            seen.add(token)
            cats.append(token)
    return cats


def add_verb(
    *,
    value: str,
    definition: str,
    category: list[str] | None = None,
    machine_only: bool = False,
) -> tuple[bool, bool]:
    """Add a verb to the appropriate verbs.jsonl files.

    Returns (wrote_project, wrote_machine). Either may be False if the
    value was already present (no-op) or writing was skipped (e.g.,
    no registered project and machine_only=False).

    `category` (E-1658) is the verb's category set — a subset of
    {"action", "investigation"}. When None or empty the field is omitted, which
    reads back as {"action"} (see `verb_categories`). Unrecognized tokens are
    dropped; duplicates are de-duped in a stable order.

    E-1208: when wrote_project is True, the project verbs.jsonl is also
    committed to main as `Endless: register verb '<value>'`. Raises
    RuntimeError on git failure (the file write persists regardless).
    """
    if not value or not value.strip():
        raise ValueError("verb value is required")
    if not definition or not definition.strip():
        raise ValueError("verb definition is required")
    entry = {"value": value.strip(), "definition": definition.strip()}
    cats = _normalize_categories(category)
    if cats:
        entry["category"] = cats

    wrote_project = False
    wrote_machine = False
    project_vp = project_verbs_path()
    if project_vp is not None and not machine_only:
        wrote_project = _add_verb_to_file(project_vp, entry)
    wrote_machine = _add_verb_to_file(machine_verbs_path(), entry)
    if wrote_project:
        _commit_project_verbs(entry["value"])
    return wrote_project, wrote_machine


def _add_verb_to_file(path: Path, entry: dict) -> bool:
    """Append a verb entry to the verbs.jsonl at `path`. No-op if value exists."""
    verbs = _load_verbs_list(path)
    for v in verbs:
        if isinstance(v, dict) and v.get("value") == entry["value"]:
            return False
    verbs.append(entry)
    _save_verbs_list(path, verbs)
    return True


def _commit_project_verbs(verb_value: str, action: str = "register") -> None:
    """Commit just .endless/verbs.jsonl on main (E-1208).

    The commit mechanics — single-path add + `commit -o`, git-locating env vars
    stripped — live in `main_commit` (E-2055), shared with the lessons log.
    Raises RuntimeError on git failure; the file write that preceded this call
    is not rolled back.

    `action` names what happened to the verb in the subject line — "register"
    for `add_verb`, "update" for `update_verb` (E-2114) — so main's log
    distinguishes a new verb from a corrected one at a glance.
    """
    project_vp = project_verbs_path()
    if project_vp is None:
        raise RuntimeError("no registered project; cannot commit verbs.jsonl")
    main_commit.commit_path(
        project_vp.parent.parent,
        ".endless/verbs.jsonl",
        f"Endless: {action} verb '{verb_value}'",
    )


class UnknownVerbError(ValueError):
    """Raised when a mutation names a verb no layer knows.

    A ValueError subclass so the existing `except ValueError` handlers keep
    working, but distinguishable, so the CLI can answer "no such verb" with a
    recovery line pointing at `verb add` instead of restating the message.
    """


class VerbUpdate(NamedTuple):
    """What `update_verb` did.

    `value` is the registered spelling (the argument is matched
    case-insensitively, so it need not be what the caller typed), `layers`
    names the layers actually rewritten — empty when every target already held
    those values — `fields` names the fields set, and `materialized` says the
    addressed layer had no entry for the verb and one was created holding only
    those fields, the rest still resolving from the layer below.
    """

    value: str
    layers: tuple[str, ...]
    fields: tuple[str, ...]
    materialized: bool


def _canonical_verb_value(value: str) -> str | None:
    """The registered spelling of `value`, matched case-insensitively through
    the resolution route, or None when no layer knows the verb.

    Resolution, not a file scan: a built-in from `DEFAULT_VERBS` is a known
    verb even though no verbs.jsonl mentions it.
    """
    target = value.strip().lower()
    for entry in _resolved_verbs():
        if not isinstance(entry, dict):
            continue
        candidate = entry.get("value")
        if isinstance(candidate, str) and candidate.lower() == target:
            return candidate
    return None


def update_verb(
    *,
    value: str,
    definition: str | None = None,
    category: list[str] | None = None,
    machine_only: bool = False,
) -> VerbUpdate:
    """Change only the named fields of an existing verb (E-2114).

    `definition` and `category` are applied when passed and left untouched when
    None, so correcting one never requires restating the others — which is the
    whole difference from the remove-then-add round trip this replaces, where a
    field you forgot to retype was silently rewritten rather than left alone.
    `category` is a set, so passing it REPLACES the whole set; there is no
    append.

    Layer targeting is by SCOPE, not by where the verb happens to sit today.
    The layer being addressed — the project verbs.jsonl, or the machine one
    under `machine_only` — is written whether or not it already carries the
    verb: an absent entry is materialized holding ONLY the passed fields, and
    `_resolved_verbs`' field-wise fall-through supplies the rest from the layer
    below, so `update_verb(value="research", category=["action"])` corrects a
    `DEFAULT_VERBS` built-in for the project while keeping its built-in
    definition. Presence-first targeting cannot express that: `_ensure_default_seeds`
    writes every built-in into the machine verbs.jsonl on first run, so a
    built-in IS present in a file, and following presence would send a
    project-scoped correction machine-wide.

    The other layer is then kept in step but never created: it is rewritten
    only if it already carries the verb, so correcting a verb that arrived with
    a fresh clone (in the committed project file, absent from this machine's)
    does not install it machine-wide as a side effect.

    Returns a `VerbUpdate`. Raises `UnknownVerbError` for a verb no layer
    knows, ValueError for an empty field set or a field passed with no usable
    content, and propagates RuntimeError from the main-branch commit as
    `add_verb` does.
    """
    if not value or not value.strip():
        raise ValueError("verb value is required")

    fields: dict = {}
    if definition is not None:
        if not definition.strip():
            raise ValueError("verb definition cannot be empty")
        fields["definition"] = definition.strip()
    if category is not None:
        cats = _normalize_categories(category)
        if not cats:
            raise ValueError(
                "verb category must name at least one of: "
                + ", ".join(sorted(VERB_CATEGORIES))
            )
        fields["category"] = cats
    if not fields:
        raise ValueError("nothing to update: pass a definition and/or a category")

    canonical = _canonical_verb_value(value)
    if canonical is None:
        raise UnknownVerbError(f"no verb matched: value={value!r}")

    # (layer, path, create-if-absent). The addressed layer is first and is the
    # only one an absent entry is created in; the other is kept in step only.
    targets: list[tuple[str, Path, bool]] = []
    project_vp = project_verbs_path()
    if project_vp is not None and not machine_only:
        targets.append(("project", project_vp, True))
        targets.append(("machine", machine_verbs_path(), False))
    else:
        targets.append(("machine", machine_verbs_path(), True))

    changed: list[str] = []
    materialized = False
    for layer, path, create in targets:
        outcome = _update_verb_in_file(path, canonical, fields, create=create)
        if outcome in ("changed", "created"):
            changed.append(layer)
        if outcome == "created":
            materialized = True

    if "project" in changed:
        _commit_project_verbs(canonical, action="update")
    return VerbUpdate(canonical, tuple(changed), tuple(fields), materialized)


def _update_verb_in_file(
    path: Path, value: str, fields: dict, *, create: bool,
) -> str:
    """Apply `fields` to every entry for `value` in the verbs.jsonl at `path`.

    Returns "changed" when matching entries were rewritten, "unchanged" when
    they already held exactly those values, "absent" when nothing matched and
    `create` is False, and "created" when nothing matched and `create` appended
    an entry holding `value` plus the fields and nothing else.

    Matching is case-insensitive, mirroring verb resolution, and every matching
    entry is updated rather than the first: `merge=union` on this file can leave
    two lines for one verb after concurrent registrations, and fixing only the
    line that happens to win resolution would leave the other as a trap for
    whichever side of a later merge wins instead.
    """
    verbs = _load_verbs_list(path)
    target = value.lower()
    matched = False
    dirty = False
    for entry in verbs:
        if not isinstance(entry, dict):
            continue
        if str(entry.get("value", "")).lower() != target:
            continue
        matched = True
        if any(entry.get(k) != v for k, v in fields.items()):
            entry.update(fields)
            dirty = True
    if matched:
        if not dirty:
            return "unchanged"
        _save_verbs_list(path, verbs)
        return "changed"
    if not create:
        return "absent"
    verbs.append({"value": value, **fields})
    _save_verbs_list(path, verbs)
    return "created"


def remove_verb(*, value: str, machine_only: bool = False) -> tuple[int, int]:
    """Remove a verb from the appropriate verbs.jsonl files.

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
    """Remove all entries with matching `value` from verbs.jsonl at `path`."""
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

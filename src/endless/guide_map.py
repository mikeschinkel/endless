"""Deterministic primitives for the agent-facing guide cross-reference.

The *mapping* of a command to the guide section that explains it is a semantic
judgement made by an LLM via the `/regenerate-guide` slash command. Everything
in this module is the deterministic scaffolding around that judgement: walking
the Click command tree, listing guide sections, reading the per-command map
files, validating coverage, and assembling the cross-reference table that lives
in `docs/guide/index.md`.

Map-file inheritance: a command resolves to the nearest map file walking up its
path — `task clear tier` tries `task-clear-tier.md`, then `task-clear.md`, then
`task.md`. So one `task.md` covers every task subcommand, and a leaf file is
needed only when a subcommand belongs to a *different* section than its group
(e.g. `task-spawn.md` -> orchestration while `task.md` -> tasks).

Kept import-light and free of CLI-runtime dependencies so it can later graduate
into a shipped `endless` verb without a rewrite. `walk_commands()` imports the
CLI lazily to avoid an import cycle (cli imports `agent_help`, which imports
`load_map` from here).
"""

from __future__ import annotations

import re
from dataclasses import dataclass, field
from pathlib import Path

# docs/ lives at the repo root, three parents up from this file
# (src/endless/guide_map.py -> src/endless -> src -> repo root).
_REPO_ROOT = Path(__file__).resolve().parent.parent.parent
GUIDE_DIR = _REPO_ROOT / "docs" / "guide"
HELP_DIR = GUIDE_DIR / "help"
INDEX_FILE = GUIDE_DIR / "index.md"
TOPICS_FILE = HELP_DIR / "_topics.md"

BEGIN_MARKER = "<!-- BEGIN generated: command/topic cross-reference (regenerate via /regenerate-guide) -->"
END_MARKER = "<!-- END generated -->"


# ---------------------------------------------------------------------------
# Command tree
# ---------------------------------------------------------------------------

def walk_commands() -> list[str]:
    """Every command path under the root group, e.g. 'task', 'task spawn'.

    Includes group paths themselves (an agent running `endless task --help`
    should be pointed at the tasks section too). Skips hidden commands. Sorted
    for determinism.
    """
    import click

    from endless.cli import main  # lazy: avoids import cycle

    paths: list[str] = []

    def visit(group: click.Group, prefix: str) -> None:
        for name, cmd in group.commands.items():
            if getattr(cmd, "hidden", False):
                continue
            path = f"{prefix} {name}".strip()
            paths.append(path)
            if isinstance(cmd, click.Group):
                visit(cmd, path)

    visit(main, "")
    return sorted(set(paths))


def command_path_to_filename(command_path: str) -> str:
    """'task spawn' -> 'task-spawn'. Space-joined parts become hyphenated."""
    return command_path.replace(" ", "-")


# ---------------------------------------------------------------------------
# Guide sections
# ---------------------------------------------------------------------------

_HEADER_RE = re.compile(r"^#{2,3}\s+(.*?)\s*$")


def guide_sections() -> dict[str, list[str]]:
    """Map each guide section slug to its list of `##`/`###` header titles.

    Slugs are the section filenames (sans .md), excluding 'index'. The header
    titles feed the scaffold so the LLM can see what each section covers.
    """
    out: dict[str, list[str]] = {}
    if not GUIDE_DIR.is_dir():
        return out
    for path in sorted(GUIDE_DIR.glob("*.md")):
        if path.stem == "index":
            continue
        headers: list[str] = []
        for line in path.read_text().splitlines():
            m = _HEADER_RE.match(line)
            if m:
                headers.append(m.group(1))
        out[path.stem] = headers
    return out


# ---------------------------------------------------------------------------
# Per-command / per-topic map files
# ---------------------------------------------------------------------------

@dataclass
class MapEntry:
    """A parsed `docs/guide/help/<command>.md` file (or a _topics record)."""
    key: str                       # command path ('task spawn') or topic name
    sections: list[str] = field(default_factory=list)
    covers: str = ""
    note: str = ""                 # optional command-specific note (commands only)
    gap: str = ""                  # set instead of section when no section fits yet
    inherited_from: str = ""       # ancestor command path the file came from


@dataclass
class _Parsed:
    sections: list[str]
    covers: str
    note: str
    gap: str


def _parse_entry(text: str) -> _Parsed:
    """Parse a `key: value` header block, optional blank line, optional note."""
    parts = text.split("\n\n", 1)
    header_block = parts[0]
    note = parts[1].strip() if len(parts) > 1 else ""
    headers: dict[str, str] = {}
    for line in header_block.splitlines():
        line = line.strip()
        if not line or ":" not in line:
            continue
        k, v = line.split(":", 1)
        headers[k.strip().lower()] = v.strip()
    sections = [s.strip() for s in headers.get("section", "").split(",") if s.strip()]
    return _Parsed(sections, headers.get("covers", ""), note, headers.get("gap", ""))


def _file_for(command_path: str) -> Path:
    return HELP_DIR / f"{command_path_to_filename(command_path)}.md"


def load_map(command_path: str) -> MapEntry | None:
    """Resolve a command to its nearest map file, walking up the path.

    Returns None if neither the command nor any ancestor has a map file.
    """
    parts = command_path.split(" ")
    while parts:
        ancestor = " ".join(parts)
        path = _file_for(ancestor)
        if path.exists():
            p = _parse_entry(path.read_text())
            return MapEntry(
                key=command_path, sections=p.sections, covers=p.covers,
                note=p.note, gap=p.gap,
                inherited_from="" if ancestor == command_path else ancestor,
            )
        parts = parts[:-1]
    return None


def load_topics() -> list[MapEntry]:
    """Parse `_topics.md` into a list of topic entries (blank-line separated)."""
    if not TOPICS_FILE.exists():
        return []
    entries: list[MapEntry] = []
    for record in TOPICS_FILE.read_text().split("\n\n"):
        record = record.strip()
        if not record:
            continue
        headers: dict[str, str] = {}
        for line in record.splitlines():
            if ":" in line:
                k, v = line.split(":", 1)
                headers[k.strip().lower()] = v.strip()
        topic = headers.get("topic", "")
        if not topic:
            continue
        sections = [s.strip() for s in headers.get("section", "").split(",") if s.strip()]
        entries.append(MapEntry(key=topic, sections=sections, covers=headers.get("covers", "")))
    return entries


def _present_stems() -> set[str]:
    if not HELP_DIR.is_dir():
        return set()
    return {p.stem for p in HELP_DIR.glob("*.md") if p.stem != "_topics"}


# ---------------------------------------------------------------------------
# Validation
# ---------------------------------------------------------------------------

@dataclass
class Report:
    missing: list[str] = field(default_factory=list)        # commands that resolve to nothing
    missing_fields: list[str] = field(default_factory=list)  # file lacks both section+covers and gap
    bad_section: list[str] = field(default_factory=list)     # section slug doesn't exist
    orphan_files: list[str] = field(default_factory=list)    # map file with no matching command
    collisions: list[str] = field(default_factory=list)      # two paths -> same filename
    stale_topics: list[str] = field(default_factory=list)    # topic -> bad section slug
    gaps: list[str] = field(default_factory=list)            # acknowledged gaps (informational)
    index_stale: bool = False                                # index block out of sync

    def ok(self) -> bool:
        # gaps are informational and do NOT fail the gate — they're the signal
        # that drives guide improvement, not a blocker.
        return not (self.missing or self.missing_fields or self.bad_section
                    or self.orphan_files or self.collisions or self.stale_topics
                    or self.index_stale)

    def render(self) -> str:
        lines: list[str] = []
        if self.collisions:
            lines.append("FAIL Filename collisions (two command paths map to one file):")
            lines += [f"  - {c}" for c in self.collisions]
        if self.missing:
            lines.append("FAIL Commands with no map file at all (add one with section: or gap:):")
            lines += [f"  - endless {c}" for c in self.missing]
        if self.missing_fields:
            lines.append("FAIL Map files needing a 'section:'+'covers:' or a 'gap:' field:")
            lines += [f"  - {c}" for c in self.missing_fields]
        if self.bad_section:
            lines.append("FAIL Map files pointing at a non-existent guide section:")
            lines += [f"  - {c}" for c in self.bad_section]
        if self.orphan_files:
            lines.append("FAIL Map files with no matching command (renamed/removed command?):")
            lines += [f"  - docs/guide/help/{c}.md" for c in self.orphan_files]
        if self.stale_topics:
            lines.append("FAIL Topics pointing at a non-existent guide section:")
            lines += [f"  - {c}" for c in self.stale_topics]
        if self.index_stale:
            lines.append("FAIL index.md cross-reference block is stale (run 'just guide-index').")
        if self.gaps:
            lines.append("")
            lines.append(f"Guide-coverage gaps ({len(self.gaps)}) — commands with no fitting "
                         "section yet; grow the guide to cover them:")
            lines += [f"  - endless {c}" for c in self.gaps]
        if self.ok():
            lines.insert(0, "guide map: OK — every command resolves and all references exist.")
        return "\n".join(lines)


def validate() -> Report:
    rep = Report()
    valid_slugs = set(guide_sections().keys())
    commands = walk_commands()
    cmd_stems = {command_path_to_filename(c): c for c in commands}

    # Filename collisions across command paths.
    by_file: dict[str, list[str]] = {}
    for cmd in commands:
        by_file.setdefault(command_path_to_filename(cmd), []).append(cmd)
    for fname, cmds in by_file.items():
        if len(cmds) > 1:
            rep.collisions.append(f"{fname}.md <- {', '.join(cmds)}")

    present = _present_stems()

    # Integrity of each map file that exists at an exact command path.
    for stem in sorted(present):
        if stem not in cmd_stems:
            rep.orphan_files.append(stem)
            continue
        cmd = cmd_stems[stem]
        p = _parse_entry((HELP_DIR / f"{stem}.md").read_text())
        if not p.gap and (not p.sections or not p.covers):
            rep.missing_fields.append(cmd)
        for slug in p.sections:
            if slug not in valid_slugs:
                rep.bad_section.append(f"endless {cmd} -> '{slug}'")

    # Coverage: every command must resolve (own file or an ancestor's); a
    # resolved entry with only a gap is acknowledged, not failing.
    for cmd in commands:
        entry = load_map(cmd)
        if entry is None:
            rep.missing.append(cmd)
        elif entry.gap and not entry.sections and not entry.inherited_from:
            rep.gaps.append(cmd)

    for topic in load_topics():
        for slug in topic.sections:
            if slug not in valid_slugs:
                rep.stale_topics.append(f"{topic.key} -> '{slug}'")

    rep.index_stale = not _index_in_sync()
    return rep


# ---------------------------------------------------------------------------
# index.md cross-reference block
# ---------------------------------------------------------------------------

def assemble_index_block() -> str:
    """Build the generated block (markers included) from the map files.

    One row per map file that exists at an exact command path (not per leaf —
    inherited subcommands would be repetitive), in command order, then topics.
    """
    present = _present_stems()
    rows: list[str] = []
    for cmd in walk_commands():
        if command_path_to_filename(cmd) not in present:
            continue
        p = _parse_entry(_file_for(cmd).read_text())
        if p.gap and not p.sections:
            rows.append(f"| `endless {cmd}` | _(none yet)_ | {p.gap} |")
        else:
            rows.append(f"| `endless {cmd}` | {', '.join(p.sections)} | {p.covers} |")
    for topic in load_topics():
        rows.append(f"| _topic:_ {topic.key} | {', '.join(topic.sections)} | {topic.covers} |")

    body = [
        BEGIN_MARKER,
        "## Where to look (command / topic → section)",
        "",
        "Have a command or topic and need the guidance for it? Find the row, then",
        "run `endless guide <section>`. Subcommands inherit their group's row unless",
        "listed separately. (Generated — do not hand-edit; run `/regenerate-guide`.)",
        "",
        "| Command / topic | Section | Covers |",
        "|---|---|---|",
        *rows,
        END_MARKER,
    ]
    return "\n".join(body)


def _current_index_block(text: str) -> str | None:
    """Extract the existing generated block from index.md text, if present."""
    if BEGIN_MARKER not in text or END_MARKER not in text:
        return None
    start = text.index(BEGIN_MARKER)
    end = text.index(END_MARKER) + len(END_MARKER)
    return text[start:end]


def _index_in_sync() -> bool:
    if not INDEX_FILE.exists():
        return False
    current = _current_index_block(INDEX_FILE.read_text())
    return current is not None and current == assemble_index_block()


def update_index_block() -> bool:
    """Rewrite the generated block in index.md from the map files.

    Requires the BEGIN/END markers to already be present in index.md (placed
    once by hand). Returns True if the file changed.
    """
    text = INDEX_FILE.read_text()
    current = _current_index_block(text)
    if current is None:
        raise SystemExit(
            f"{INDEX_FILE} has no generated-block markers. Add a "
            f"'{BEGIN_MARKER}' / '{END_MARKER}' pair where the table should go."
        )
    new_block = assemble_index_block()
    if current == new_block:
        return False
    INDEX_FILE.write_text(text.replace(current, new_block))
    return True


# ---------------------------------------------------------------------------
# Scaffold (skeleton the LLM fills in /regenerate-guide)
# ---------------------------------------------------------------------------

def scaffold_text() -> str:
    """Human/LLM-readable skeleton: every command + every section's headers."""
    lines = ["# Guide-map scaffold", "",
             "## Guide sections and their headers", ""]
    for slug, headers in guide_sections().items():
        lines.append(f"### {slug}")
        for h in headers:
            lines.append(f"  - {h}")
        lines.append("")
    lines += ["## Commands (each must resolve to a map file, own or inherited)", ""]
    for cmd in walk_commands():
        entry = load_map(cmd)
        if entry is None:
            mark = "MISSING"
        elif entry.inherited_from:
            mark = f"<- {entry.inherited_from}"
        else:
            mark = "own"
        lines.append(f"  [{mark:>16}] endless {cmd}")
    return "\n".join(lines)


# ---------------------------------------------------------------------------
# Dev CLI: `python -m endless.guide_map {scaffold|index|check}`
# (wrapped by the just recipes; the LLM step is the /regenerate-guide command)
# ---------------------------------------------------------------------------

def run_cli(argv: list[str]) -> int:
    cmd = argv[0] if argv else ""
    if cmd == "scaffold":
        print(scaffold_text())
        return 0
    if cmd == "index":
        changed = update_index_block()
        print("index.md cross-reference block updated."
              if changed else "index.md already in sync.")
        return 0
    if cmd == "check":
        rep = validate()
        print(rep.render())
        return 0 if rep.ok() else 1
    print("usage: python -m endless.guide_map {scaffold|index|check}")
    return 2


if __name__ == "__main__":
    import sys

    sys.exit(run_cli(sys.argv[1:]))

"""Document type definitions — single source of truth.

Each type defines:
- name: the type identifier stored in DB
- singleton: whether only one file of this type should exist per project
- stems: exact filename stems (without .md) that match this type
- dirs: directory names that classify all .md files within as this type
- patterns: substring patterns in the stem (last resort heuristic)
"""

from dataclasses import dataclass, field
from pathlib import Path


@dataclass
class DocType:
    name: str
    singleton: bool = False
    stems: list[str] = field(default_factory=list)
    dirs: list[str] = field(default_factory=list)
    patterns: list[str] = field(default_factory=list)


# Ordered by priority — first match wins
DOC_TYPES: list[DocType] = [
    DocType(
        name="readme",
        singleton=True,
        stems=["readme"],
    ),
    DocType(
        name="plan",
        singleton=True,
        stems=["plan"],
        patterns=["plan"],
    ),
    DocType(
        name="design_brief",
        singleton=True,
        stems=["design_brief", "design-brief"],
        dirs=["design-briefs", "design_briefs"],
        patterns=["design"],
    ),
    DocType(
        name="changelog",
        singleton=True,
        stems=["changelog", "changes"],
        patterns=["changelog"],
    ),
    DocType(
        name="claude_md",
        singleton=True,
        stems=["claude", "agents"],
    ),
    DocType(
        name="roadmap",
        singleton=True,
        stems=["roadmap"],
        patterns=["roadmap"],
    ),
    DocType(
        name="spec",
        singleton=True,
        stems=["spec"],
        patterns=["spec"],
    ),
    DocType(
        name="todo",
        singleton=True,
        stems=["todo"],
    ),
    DocType(
        name="done",
        singleton=True,
        stems=["done"],
    ),
    DocType(
        name="lessons",
        singleton=True,
        stems=["lessons", "lessons_learned"],
    ),
    DocType(
        name="vision",
        singleton=True,
        stems=["vision"],
        dirs=["docs/vision"],
        patterns=["vision"],
    ),
    DocType(
        name="contributing",
        singleton=True,
        stems=["contributing"],
    ),
    DocType(
        name="license",
        singleton=True,
        stems=["license", "licensing"],
        patterns=["licensing"],
    ),
    DocType(
        name="adr",
        singleton=False,
        dirs=["adrs", "adr", "docs/adrs"],
        patterns=["adr"],
    ),
    DocType(
        name="research",
        singleton=False,
        dirs=["research", "docs/research"],
        patterns=["research"],
    ),
    DocType(
        name="guide",
        singleton=False,
        dirs=["docs/how-to"],
        patterns=["guide"],
    ),
]

# Quick lookups
DOC_TYPE_NAMES: set[str] = {dt.name for dt in DOC_TYPES} | {"other"}
SINGLETON_TYPES: set[str] = {dt.name for dt in DOC_TYPES if dt.singleton}


def classify_doc(rel_path: str) -> str:
    """Classify a document by its relative path.

    Checks in order: exact stem match, directory match,
    then substring pattern match. Returns 'other' if no match.
    """
    stem = Path(rel_path).stem.lower()
    parent = Path(rel_path).parent.name.lower()
    rel_lower = rel_path.lower()

    for dt in DOC_TYPES:
        # Exact stem match
        if stem in dt.stems:
            return dt.name

        # Directory match
        for d in dt.dirs:
            if parent == d or rel_lower.startswith(d + "/"):
                return dt.name

        # Substring pattern match (last resort)
        for p in dt.patterns:
            if p in stem:
                return dt.name

    return "other"


def is_valid_type(doc_type: str) -> bool:
    return doc_type in DOC_TYPE_NAMES

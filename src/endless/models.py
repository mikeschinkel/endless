"""Data models for Endless."""

from dataclasses import dataclass, field
from pathlib import Path


VALID_STATUSES = ("active", "paused", "archived", "idea")

# Re-export from doc_types for backwards compatibility
from endless.doc_types import DOC_TYPE_NAMES as DOC_TYPES
from endless.doc_types import SINGLETON_TYPES as SINGLETON_DOC_TYPES


@dataclass
class Project:
    name: str
    path: Path
    id: int | None = None
    label: str = ""
    group_name: str | None = None
    description: str = ""
    status: str = "active"
    language: str = ""
    created_at: str = ""
    updated_at: str = ""
    pending_notes: int = 0
    doc_count: int = 0


@dataclass
class Document:
    project_id: int
    relative_path: str
    id: int | None = None
    doc_type: str = "other"
    content_hash: str = ""
    size_bytes: int = 0
    last_modified: str = ""
    last_scanned: str = ""
    is_archived: bool = False


@dataclass
class Signal:
    path: Path
    claude_dir: bool = False
    claude_md: bool = False
    agents_md: bool = False
    git: bool = False
    lang_file: bool = False
    build_file: bool = False
    readme: bool = False
    has_markdown: bool = False
    language: str = ""
    newest_mtime: float | None = None
    age_str: str = "unknown"
    description: str = ""
    tier: int = 5

    @property
    def name(self) -> str:
        return self.path.name

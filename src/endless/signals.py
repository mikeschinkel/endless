"""Signal detection for project discovery."""

import time
from pathlib import Path

from endless.models import Signal
from endless.register import detect_language

SKIP_DIRS = {".git", "node_modules", "vendor", "__pycache__"}

LANG_FILES = {
    "go.mod": "go",
    "package.json": "javascript",
    "Cargo.toml": "rust",
    "pyproject.toml": "python",
    "setup.py": "python",
}


def newest_mtime(dir_path: Path) -> float | None:
    """Find the newest file mtime in a directory (up to depth 2)."""
    best = None
    try:
        for f in dir_path.iterdir():
            if f.name in SKIP_DIRS:
                continue
            if f.is_file():
                mt = f.stat().st_mtime
                if best is None or mt > best:
                    best = mt
            elif f.is_dir():
                for f2 in f.iterdir():
                    if f2.is_file():
                        try:
                            mt = f2.stat().st_mtime
                            if best is None or mt > best:
                                best = mt
                        except OSError:
                            pass
    except OSError:
        pass
    return best


def format_age(mtime: float | None) -> str:
    if mtime is None:
        return "unknown"
    days = int((time.time() - mtime) / 86400)
    if days < 1:
        return "today"
    if days == 1:
        return "1 day ago"
    if days < 30:
        return f"{days} days ago"
    months = days // 30
    if months < 12:
        return f"{months} month{'s' if months > 1 else ''} ago"
    years = days // 365
    return f"{years} year{'s' if years > 1 else ''} ago"


def detect_signals(dir_path: Path) -> Signal:
    sig = Signal(path=dir_path)

    sig.claude_dir = (dir_path / ".claude").is_dir()
    sig.claude_md = (dir_path / "CLAUDE.md").is_file()
    sig.agents_md = (
        (dir_path / "AGENTS.md").is_file()
        or (dir_path / ".codex").is_dir()
    )
    sig.git = (dir_path / ".git").is_dir()
    sig.readme = (dir_path / "README.md").is_file()
    sig.build_file = (
        (dir_path / "Makefile").is_file()
        or (dir_path / "justfile").is_file()
    )

    # Language file detection
    for fname, lang in LANG_FILES.items():
        if (dir_path / fname).is_file():
            sig.lang_file = True
            sig.language = lang
            break

    # Markdown file detection (any .md at top level)
    try:
        sig.has_markdown = any(
            f.suffix == ".md" for f in dir_path.iterdir()
            if f.is_file()
        )
    except OSError:
        pass

    # Fallback language detection
    if not sig.language:
        sig.language = detect_language(dir_path)

    # Recency
    sig.newest_mtime = newest_mtime(dir_path)
    sig.age_str = format_age(sig.newest_mtime)

    # Build description
    parts = []
    if sig.claude_dir:
        parts.append(".claude")
    if sig.claude_md:
        parts.append("CLAUDE.md")
    if sig.agents_md:
        parts.append("AGENTS.md")
    if sig.git:
        parts.append(".git")
    if sig.lang_file:
        parts.append("lang")
    if sig.build_file:
        parts.append("build")
    if sig.has_markdown and not sig.readme:
        parts.append(".md")
    sig.description = "+".join(parts) if parts else "none"

    # Classify tier
    sig.tier = classify_tier(sig)

    return sig


def classify_tier(sig: Signal) -> int:
    has_ai = sig.claude_dir or sig.claude_md or sig.agents_md

    days_old = 9999
    if sig.newest_mtime is not None:
        days_old = int((time.time() - sig.newest_mtime) / 86400)

    if has_ai and days_old <= 90:
        return 1  # Active AI project
    if has_ai:
        return 2  # AI-configured but dormant
    if sig.git and sig.lang_file and days_old <= 365:
        return 3  # Active dev project
    if sig.git:
        return 4  # Dormant project
    if sig.has_markdown or sig.readme or sig.build_file:
        return 4  # Has content but no git — still worth showing
    return 5  # Not a project


def count_git_subdirs(dir_path: Path) -> int:
    count = 0
    try:
        for child in dir_path.iterdir():
            if child.is_dir() and (child / ".git").is_dir():
                count += 1
    except OSError:
        pass
    return count

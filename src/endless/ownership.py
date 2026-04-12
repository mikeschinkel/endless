"""Repo ownership detection and filtering."""

import re
import subprocess
from fnmatch import fnmatch
from pathlib import Path

from endless import config

# Patterns to normalize git remote URLs to github.com/org/repo
_SSH_PATTERN = re.compile(r"^git@([^:]+):(.+?)(?:\.git)?$")
_HTTPS_PATTERN = re.compile(r"^https?://([^/]+)/(.+?)(?:\.git)?$")


def get_repo_id(dir_path: Path) -> str | None:
    """Extract a normalized repo identifier from git remote.

    Returns e.g. "github.com/mikeschinkel/go-tealeaves" or None.
    """
    try:
        result = subprocess.run(
            ["git", "-C", str(dir_path), "remote", "get-url", "origin"],
            capture_output=True, text=True, timeout=5,
        )
        if result.returncode != 0:
            return None
        url = result.stdout.strip()
    except (subprocess.TimeoutExpired, FileNotFoundError):
        return None

    # Try SSH format: git@github.com:org/repo.git
    m = _SSH_PATTERN.match(url)
    if m:
        return f"{m.group(1)}/{m.group(2)}"

    # Try HTTPS format: https://github.com/org/repo.git
    m = _HTTPS_PATTERN.match(url)
    if m:
        return f"{m.group(1)}/{m.group(2)}"

    return None


def is_mine(dir_path: Path) -> bool:
    """Check if a directory belongs to the user based on ownership rules.

    Rules are in global config under "ownership.mine" as a list of
    glob patterns matched against the normalized repo ID.

    Repos with no remote are assumed to be the user's own.
    Repos matching no ownership rules are NOT mine.
    If no ownership rules are configured, everything is mine
    (backwards compatible).
    """
    cfg = config.load_config()
    ownership = cfg.get("ownership", {})
    mine_patterns = ownership.get("mine", [])

    # No ownership rules configured → everything is mine
    if not mine_patterns:
        return True

    repo_id = get_repo_id(dir_path)

    # No git remote → local-only project, assume mine
    if repo_id is None:
        return True

    # Check against mine patterns
    for pattern in mine_patterns:
        if fnmatch(repo_id, pattern):
            return True

    return False

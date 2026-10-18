"""A project's committed identity (E-2217).

The identity is the SHA of the repo's root commit. Every clone and every fork
shares it, so each checkout derives the same value with no coordination and no
race, which is what a value minted at registration could not offer: two people
registering before either pushes would mint two. Endless requires a git repo
and one project per repo, so a repo is a project and its root commit names it.

Derived once, at registration, then stored in `.endless/config.json` under
`identity`. The stored copy wins from then on, so a later history rewrite that
changes the root commit does not change the project's identity.

No database here: callers decide what to do with the value.
"""

from __future__ import annotations

import subprocess
from pathlib import Path

from endless import config
from endless.main_commit import sanitized_git_env

CONFIG_KEY = "identity"


class NoIdentity(Exception):
    """The directory has no root commit to derive an identity from.

    `reason` is "not_a_repo" or "no_commits", so a caller can name the fix
    (`git init`, or a first commit) rather than restate the failure.
    """

    def __init__(self, reason: str, detail: str):
        super().__init__(detail)
        self.reason = reason


def root_commit(project_path: Path) -> str:
    """The SHA of `project_path`'s root commit.

    A repo with several root commits (unrelated histories merged) answers with
    the oldest by committer date, ties broken by SHA, so every clone picks the
    same one. Raises NoIdentity when there is no repo or no commit yet.
    """
    env = sanitized_git_env()
    inside = subprocess.run(
        ["git", "-C", str(project_path), "rev-parse", "--is-inside-work-tree"],
        capture_output=True, text=True, env=env,
    )
    if inside.returncode != 0 or inside.stdout.strip() != "true":
        raise NoIdentity("not_a_repo", f"{project_path} is not a git repository")
    res = subprocess.run(
        ["git", "-C", str(project_path), "log", "--max-parents=0",
         "--format=%ct %H", "HEAD"],
        capture_output=True, text=True, env=env,
    )
    if res.returncode != 0:
        # An unborn HEAD is the only way a work tree has no log.
        raise NoIdentity("no_commits", f"{project_path} has no commits yet")
    roots = []
    for line in res.stdout.splitlines():
        ts, _, sha = line.strip().partition(" ")
        if sha:
            roots.append((int(ts), sha))
    if not roots:
        raise NoIdentity("no_commits", f"{project_path} has no commits yet")
    return min(roots)[1]


def stored_identity(cfg: dict | None) -> str | None:
    """The identity a project's config already carries, or None."""
    if not cfg:
        return None
    value = cfg.get(CONFIG_KEY)
    return value if isinstance(value, str) and value else None


def identity_for(project_path: Path, cfg: dict | None = None) -> str:
    """The project's identity: the stored copy when there is one, else derived.

    `cfg` is the project's config when the caller has already read it. Raises
    NoIdentity when nothing is stored and there is no root commit.
    """
    if cfg is None:
        cfg = config.project_config_read(project_path)
    return stored_identity(cfg) or root_commit(project_path)

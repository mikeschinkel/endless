"""Commit ONE endless-managed file directly on a project's main checkout.

The shared half of the write-time commit pattern: an endless-managed file that
belongs to the project rather than to a task branch is written on main and
committed there in the same step, so it never waits on a land and never dirties
a worktree. `.endless/verbs.jsonl` established the pattern (E-1208);
`.endless/LESSONS.md` joined it (E-2055). Sanctioned by ED-1199's global-config
exception to the no-direct-commits-to-main rule — the commit is single-file and
carries no task id, so it is distinguishable at a glance from session work.

Deliberately knows nothing about which file it is committing: it takes the main
root, the repo-relative path, and the message. No database, no config, no
project resolution — the caller has already answered those.
"""

from __future__ import annotations

import os
import subprocess
from pathlib import Path

# Git env vars that override `git -C <path>` for repo resolution. Stripped from
# the subprocess env so a stray GIT_DIR anywhere in the caller chain cannot
# silently redirect the commit to a linked worktree's gitdir (E-1309). Mirrors
# gitRedirectVars in internal/events/commit.go.
GIT_REDIRECT_VARS = (
    "GIT_DIR",
    "GIT_WORK_TREE",
    "GIT_INDEX_FILE",
    "GIT_OBJECT_DIRECTORY",
    "GIT_COMMON_DIR",
    "GIT_NAMESPACE",
    "GIT_ALTERNATE_OBJECT_DIRECTORIES",
)


def sanitized_git_env() -> dict:
    """os.environ minus the git-locating vars (E-1309)."""
    env = dict(os.environ)
    for k in GIT_REDIRECT_VARS:
        env.pop(k, None)
    return env


def commit_path(
    main_root: Path, rel_path: str, subject: str, body: str | None = None,
) -> None:
    """Commit just `rel_path` in `main_root` as `subject` (plus `body`).

    Two steps: `git add <path>` then `git commit -o <path>`. The add is needed
    because a brand-new file — the first verb ever registered, the first lesson
    on a fresh clone — is not yet known to git, and `commit -o` alone fails with
    a pathspec error. The `-o` flag then commits ONLY that path, leaving the
    rest of main's index and working tree exactly as they were, so unrelated
    work in flight on main is preserved staged-or-unstaged as it was.

    Raises RuntimeError on either subprocess failure. The file write that
    preceded the call is NOT rolled back: the caller surfaces the git failure
    but the content is still on disk, which is the right trade for an
    append-only record — a lost commit can be redone, a lost lesson cannot.
    """
    env = sanitized_git_env()
    add_res = subprocess.run(
        ["git", "-C", str(main_root), "add", "--", rel_path],
        capture_output=True, text=True, env=env,
    )
    if add_res.returncode != 0:
        raise RuntimeError(
            f"git add failed for {rel_path}: "
            f"{(add_res.stderr or add_res.stdout or '').strip()}"
        )
    cmd = ["git", "-C", str(main_root), "commit", "-o", rel_path, "-m", subject]
    if body and body.strip():
        cmd += ["-m", body.strip()]
    res = subprocess.run(cmd, capture_output=True, text=True, env=env)
    if res.returncode != 0:
        raise RuntimeError(
            f"git commit failed for {rel_path}: "
            f"{(res.stderr or res.stdout or '').strip()}"
        )

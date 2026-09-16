"""Bringing a project's worktrees onto the in-worktree sandbox layout.

Sandboxes used to live under `~/.cache/endless/sandboxes/<worktree-dir-name>/`.
ED-1554 moved them inside their worktrees, and this is the one-shot, idempotent
command that gets an existing installation there: it relocates what is in the
cache root and provisions what never had a sandbox at all.

Why a command rather than a lazy rename inside the path resolver, which is where
the design first put it:

  - A function that resolves a path must not mutate the filesystem. Every caller
    would inherit a side effect it never asked for, in a code path that runs on
    essentially every invocation.
  - Two sessions on one worktree would race the same rename.
  - A migration you can watch has an observable moment and a report. A lazy one
    happens invisibly, in whichever process went first, and leaves nobody with
    an account of what moved.

The consequence is that this command is the LAST moment a missing sandbox is
expected. Nothing provisions one afterwards — not the resolver, not the next
command — so an absent sandbox after this has run means something unanticipated
happened, and the refusal says so rather than quietly rebuilding one.

It survives the migration rather than being deleted after it: a worktree
restored from a backup, or a machine that skipped a release, arrives in exactly
the state this fixes.
"""

from __future__ import annotations

import json
import shutil
from pathlib import Path

import click


# The pre-ED-1554 sandbox root, relative to the cache root. Read, never written:
# migration's whole job is to empty it, and nothing puts a worktree sandbox
# there again.
_LEGACY_SANDBOX_SEGMENTS = ("endless", "sandboxes")


def _legacy_sandbox_root() -> Path:
    from endless import config
    return config._cache_root().joinpath(*_LEGACY_SANDBOX_SEGMENTS)


def _project_root_from_cwd() -> Path:
    """The project this migration is for, resolved from the filesystem.

    Deliberately NOT the database-backed worktree_cmd._project_root: this
    command has to run in a worktree whose sandbox is missing, which is the one
    state where every database-touching command refuses. Filesystem resolution
    answers the same question and works there.
    """
    from endless import config
    root = config.enclosing_project_root()
    if root is None:
        raise click.ClickException(
            "not inside an endless project (no .endless/config.json in any "
            "parent directory), so there are no worktrees to migrate"
        )
    return root


def _dir_size(path: Path) -> int:
    """Bytes held below path. Best-effort: an unreadable entry contributes 0
    rather than aborting a report."""
    total = 0
    for p in path.rglob("*"):
        try:
            if p.is_file() and not p.is_symlink():
                total += p.stat().st_size
        except OSError:
            continue
    return total


def _fmt_size(size: int) -> str:
    value = float(size)
    for unit in ("B", "KB", "MB", "GB"):
        if value < 1024 or unit == "GB":
            return f"{value:.0f} {unit}"
        value /= 1024
    return f"{value:.0f} GB"


def _worktree_dirs(project_root: Path) -> list[Path]:
    """This project's task worktrees, lowest id first.

    Only the canonical `e-NNN` spelling is a task worktree (ED-1515); anything
    else under `.endless/worktrees/` is somebody's own directory and is left
    alone.
    """
    root = project_root / ".endless" / "worktrees"
    if not root.is_dir():
        return []
    out = [
        child for child in root.iterdir()
        if child.is_dir() and child.name.startswith("e-") and child.name[2:].isdigit()
    ]
    return sorted(out, key=lambda p: int(p.name[2:]))


def _strip_stale_injection(worktree: Path, legacy_root: Path, dry_run: bool) -> bool:
    """Remove the worktree's inherited XDG_CONFIG_HOME if it points into the
    legacy sandbox root. Returns whether there was one to remove.

    `sandbox bind` wrote this key into `<worktree>/.claude/settings.json` so
    every process a Claude session spawned inherited the sandbox. Deleting the
    code that writes it does not delete the copies already on disk, and after
    relocation each of those names a directory that is no longer there — which
    is worse than merely stale. A process whose cwd is outside the worktree gets
    no self-detection, falls back to the inherited value, and creates a fresh
    empty config directory at the vacated path: the ambient-routing failure the
    injection's deletion exists to close, resurrected by its own leftovers.

    Only a value under the legacy root is removed. A developer who set
    XDG_CONFIG_HOME for their own reasons keeps it — this deletes endless's
    injection, not the variable.
    """
    settings = worktree / ".claude" / "settings.json"
    if not settings.is_file():
        return False
    try:
        data = json.loads(settings.read_text() or "{}")
    except (OSError, ValueError):
        return False
    env = data.get("env") if isinstance(data, dict) else None
    if not isinstance(env, dict):
        return False
    value = env.get("XDG_CONFIG_HOME")
    if not isinstance(value, str):
        return False
    try:
        Path(value).relative_to(legacy_root)
    except ValueError:
        return False
    if dry_run:
        return True
    del env["XDG_CONFIG_HOME"]
    if env:
        data["env"] = env
    else:
        data.pop("env", None)
    settings.write_text(json.dumps(data, indent=2) + "\n")
    return True


def migrate_sandboxes(dry_run: bool) -> None:
    """Relocate and provision this project's worktree sandboxes.

    Three passes, in this order, because each depends on the one before it:

    1. **Relocate.** Every directory in the legacy cache root whose name matches
       a live worktree of this project moves into that worktree. Both paths are
       under $HOME on one filesystem, so this is a rename, not a copy. A worktree
       that already has a sandbox is a COLLISION: both are left exactly as they
       are, the pair is reported, and the run exits non-zero. Merging two
       directories that each claim to be the state a task is exercised against
       is a guess, and a hand-inspected case beats a guessed one. A directory
       with no surviving worktree of this project is reported as an orphan and
       NOT deleted — deleting directories is not something this command does on
       its own authority, and that cache root is shared with every other project
       on the machine.

    2. **Provision.** Every worktree still without a sandbox gets one: the
       directory, its self-ignoring .gitignore, and a run of the project's
       post-worktree-create hook, exactly as worktree-create does. Not optional
       and not a separate command — nothing provisions on a miss any more, so a
       worktree this pass skips is a worktree whose every command refuses, from
       now on. A hook failure is non-fatal and loud, matching worktree-create:
       the sandbox stays, the runner names the script and the re-run command,
       and the count is reported separately so the summary line says whether
       anything needs looking at.

    3. **Unpin.** Strip the dead XDG_CONFIG_HOME injection from each worktree's
       Claude settings (see _strip_stale_injection).

    `--dry-run` does every read and none of the writes — no rename, no
    directory, no hook — so the report can be read before anything changes.
    """
    from endless import config
    from endless.worktree_cmd import (
        _display_path,
        _run_post_worktree_create_hook,
        provision_worktree_sandbox,
    )

    project_root = _project_root_from_cwd()
    legacy_root = _legacy_sandbox_root()

    moved: list[tuple[str, Path]] = []
    collided: list[tuple[Path, Path]] = []
    orphaned: list[tuple[Path, int]] = []
    provisioned: list[Path] = []
    hook_failed: list[Path] = []
    unpinned: list[Path] = []

    # ── 1. relocate ────────────────────────────────────────────────────────
    if legacy_root.is_dir():
        for legacy in sorted(legacy_root.iterdir(), key=lambda p: p.name):
            if not legacy.is_dir():
                continue
            worktree = project_root / ".endless" / "worktrees" / legacy.name
            if not worktree.is_dir():
                orphaned.append((legacy, _dir_size(legacy)))
                continue
            target = config.sandbox_root(worktree)
            if target.exists():
                collided.append((legacy, target))
                continue
            if not dry_run:
                target.parent.mkdir(parents=True, exist_ok=True)
                shutil.move(str(legacy), str(target))
            moved.append((legacy.name, target))

    # ── 2. provision what never had one ────────────────────────────────────
    relocated = {target for _, target in moved}
    for worktree in _worktree_dirs(project_root):
        sandbox = config.sandbox_root(worktree)
        if sandbox.is_dir() or sandbox in relocated:
            continue
        provisioned.append(worktree)
        if dry_run:
            continue
        provision_worktree_sandbox(worktree)
        if not _run_post_worktree_create_hook(project_root, worktree):
            hook_failed.append(worktree)

    # ── 3. unpin ───────────────────────────────────────────────────────────
    for worktree in _worktree_dirs(project_root):
        if _strip_stale_injection(worktree, legacy_root, dry_run):
            unpinned.append(worktree)

    # ── report ─────────────────────────────────────────────────────────────
    # One shape for both runs, so the counts a --dry-run reports are directly
    # comparable with the ones the real run then reports. A dry run says so in
    # a prefix rather than by conjugating every noun.
    summary = (
        f"{len(moved)} moved, {len(provisioned)} provisioned, "
        f"{len(hook_failed)} hook failure(s), {len(collided)} collided, "
        f"{len(orphaned)} orphaned, {len(unpinned)} unpinned"
    )
    if dry_run:
        summary = "DRY RUN — nothing changed. " + summary
    click.echo(summary)
    for name, target in moved:
        click.echo(f"  moved       {name} → {_display_path(target)}")
    for worktree in provisioned:
        click.echo(f"  provisioned {_display_path(worktree)}")
    for worktree in hook_failed:
        click.echo(f"  hook failed {_display_path(worktree)}", err=True)
    for worktree in unpinned:
        click.echo(f"  unpinned    {_display_path(worktree)}")
    for legacy, target in collided:
        click.echo(
            click.style("  COLLISION   ", fg="red")
            + f"{_display_path(legacy)} and {_display_path(target)} both exist; "
            "neither was touched",
            err=True,
        )
    if orphaned:
        total = sum(size for _, size in orphaned)
        click.echo(
            f"  {len(orphaned)} orphan(s) under {_display_path(legacy_root)}, "
            f"{_fmt_size(total)} — no worktree of this project claims them. "
            "Nothing was deleted."
        )
        for legacy, size in orphaned:
            click.echo(f"    orphan    {legacy.name}  {_fmt_size(size)}")

    if collided:
        raise SystemExit(1)

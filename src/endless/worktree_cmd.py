"""Inspection and mutation CLI for git worktrees managed by endless.

Inspection (foundation, E-971):
- list: enumerate all git worktrees of the current project, classified
- current: report the worktree for cwd
- show: detail for one worktree
- for-task: resolve a task ID to its worktree path

Mutation (this slice, E-971 + E-987 + E-1056):
- land: auto-commit endless-managed files, rebase worktree onto main,
  ff-merge, remove worktree
- drop: explicit cleanup (refuses dirty/unmerged without --force)

Auto-creation triggers (next slice): SessionStart hook, plan-bearing
task start.

Worktree state is filesystem-authoritative (per E-971 design): no DB
tables. Each endless-managed worktree has a companion JSON file at
<worktree-root>/.endless/worktree.json with task_id, base_branch,
branch, created_at. Worktrees without the companion are 'foreign'
(created by another tool or by hand) and are listed but never mutated
by land/drop.

Lifecycle states (derived):
- active: git knows about it AND endless companion is present
- foreign: git knows about it AND no companion
- merged: branch is in `git branch --merged <base>`
- abandoned: heuristic — unmerged, no live session bound
"""

import fnmatch
import json
import re
import subprocess
from pathlib import Path

import click

from endless.task_cmd import _resolve_project


COMPANION_FILENAME = ".endless/worktree.json"

# Auto-committed file globs per E-987 (locked), modified by E-1141:
# verbs.json is in (ambient agent-driven churn); config.json is out
# (deliberate human/agent edits whose attribution the user controls).
# Land treats these as endless-managed: dirty state in any of these does
# not block land; instead, land auto-commits them as a separate commit
# before the worktree's commits.
AUTO_COMMIT_GLOBS = (
    ".endless/events/*.jsonl",
    ".endless/plans/snapshots/*",
    ".endless/verbs.json",
)

# Land's retry cap for the race-with-concurrent-writers loop (E-987).
LAND_MAX_RETRIES = 8


def _project_root() -> Path:
    """Return the registered project root path for cwd's project."""
    project_id, _ = _resolve_project(None)
    from endless import db
    row = db.query("SELECT path FROM projects WHERE id = ? LIMIT 1", (project_id,))
    if not row:
        raise click.ClickException(f"Project id {project_id} has no registered path")
    return Path(row[0]["path"]).expanduser().resolve()


def _git(args: list[str], cwd: Path) -> str:
    """Run a git command in cwd; return trimmed stdout. Raises on non-zero exit."""
    res = subprocess.run(
        ["git", *args],
        capture_output=True, text=True, check=True, cwd=str(cwd),
    )
    return res.stdout.rstrip("\n")


def _parse_worktree_porcelain(out: str) -> list[dict]:
    """Parse `git worktree list --porcelain`. Returns list of dicts.

    Stanzas are blank-line separated. Keys: worktree, HEAD, branch,
    bare, detached, locked, prunable.
    """
    worktrees: list[dict] = []
    cur: dict | None = None
    for raw in out.splitlines():
        line = raw.rstrip("\n")
        if not line:
            if cur is not None:
                worktrees.append(cur)
                cur = None
            continue
        if cur is None:
            cur = {}
        if line.startswith("worktree "):
            cur["path"] = line[len("worktree "):]
        elif line.startswith("HEAD "):
            cur["head"] = line[len("HEAD "):]
        elif line.startswith("branch "):
            cur["branch"] = line[len("branch "):]
        elif line == "detached":
            cur["detached"] = True
        elif line == "bare":
            cur["bare"] = True
        elif line == "locked":
            cur["locked"] = True
        elif line.startswith("locked "):
            cur["locked"] = True
            cur["lock_reason"] = line[len("locked "):]
        elif line == "prunable":
            cur["prunable"] = True
        elif line.startswith("prunable "):
            cur["prunable"] = True
            cur["prunable_reason"] = line[len("prunable "):]
    if cur is not None:
        worktrees.append(cur)
    return worktrees


def _read_companion(worktree_path: Path) -> dict | None:
    """Read <worktree-root>/.endless/worktree.json. Returns dict or None."""
    companion = worktree_path / COMPANION_FILENAME
    if not companion.exists():
        return None
    try:
        return json.loads(companion.read_text())
    except (OSError, json.JSONDecodeError):
        return None


def _short_branch(ref: str | None) -> str:
    """Convert 'refs/heads/foo' to 'foo'. Pass-through if not a branch ref."""
    if not ref:
        return ""
    return ref.removeprefix("refs/heads/")


def _classify(wt: dict, companion: dict | None, root: Path) -> str:
    """Return the lifecycle state label for a worktree row.

    For now: 'main' for the main checkout, 'active' if endless-managed,
    'foreign' otherwise. 'merged'/'abandoned' classification is deferred
    to a future layer (requires base-branch lookup).
    """
    if Path(wt.get("path", "")).resolve() == root:
        return "main"
    if companion is not None:
        return "active"
    return "foreign"


def _enriched_list(project_root: Path) -> list[dict]:
    """Run `git worktree list --porcelain` and merge with companion metadata."""
    out = _git(["worktree", "list", "--porcelain"], cwd=project_root)
    rows = _parse_worktree_porcelain(out)
    enriched = []
    for wt in rows:
        path = Path(wt.get("path", ""))
        companion = _read_companion(path) if path.exists() else None
        enriched.append({
            "path": str(path),
            "branch": _short_branch(wt.get("branch")),
            "branch_ref": wt.get("branch", ""),
            "head": wt.get("head", ""),
            "detached": wt.get("detached", False),
            "bare": wt.get("bare", False),
            "locked": wt.get("locked", False),
            "prunable": wt.get("prunable", False),
            "lock_reason": wt.get("lock_reason"),
            "prunable_reason": wt.get("prunable_reason"),
            "state": _classify(wt, companion, project_root),
            "companion": companion,
        })
    return enriched


# --- CLI command implementations -------------------------------------------

def list_worktrees(state_filter: str | None, as_json: bool) -> None:
    """List worktrees for the current project."""
    root = _project_root()
    rows = _enriched_list(root)
    if state_filter:
        rows = [r for r in rows if r["state"] == state_filter]

    if as_json:
        click.echo(json.dumps(rows, indent=2))
        return

    if not rows:
        click.echo("No worktrees match.")
        return

    click.echo(f"{'State':<8}  {'Branch':<40}  {'Task':<8}  Path")
    click.echo("-" * 8 + "  " + "-" * 40 + "  " + "-" * 8 + "  " + "-" * 40)
    for r in rows:
        task = ""
        if r["companion"]:
            task = r["companion"].get("task_id", "")
        branch = r["branch"] or ("(detached)" if r["detached"] else "")
        if len(branch) > 40:
            branch = branch[:39] + "…"
        path = r["path"]
        if len(path) > 60:
            path = "…" + path[-59:]
        click.echo(f"{r['state']:<8}  {branch:<40}  {task:<8}  {path}")


def current_worktree(as_json: bool) -> None:
    """Show the worktree for the current cwd."""
    cwd = Path.cwd().resolve()
    try:
        toplevel_str = _git(["rev-parse", "--show-toplevel"], cwd=cwd)
    except subprocess.CalledProcessError:
        raise click.ClickException("Not inside a git repository")
    toplevel = Path(toplevel_str).resolve()

    root = _project_root()
    rows = _enriched_list(root)
    match = next((r for r in rows if Path(r["path"]).resolve() == toplevel), None)
    if match is None:
        raise click.ClickException(
            f"cwd {cwd} resolves to a working tree {toplevel} that "
            f"git worktree list does not report. Inconsistent state."
        )

    if as_json:
        click.echo(json.dumps(match, indent=2))
        return

    click.echo(f"State:   {match['state']}")
    click.echo(f"Path:    {match['path']}")
    click.echo(f"Branch:  {match['branch'] or '(detached)'}")
    click.echo(f"HEAD:    {match['head']}")
    if match["companion"]:
        sc = match["companion"]
        if "task_id" in sc:
            click.echo(f"Task:    {sc['task_id']}")
        if "base_branch" in sc:
            click.echo(f"Base:    {sc['base_branch']}")
        if "created_at" in sc:
            click.echo(f"Created: {sc['created_at']}")
    if match["locked"]:
        click.echo(f"Locked:  {match.get('lock_reason') or 'yes'}")
    if match["prunable"]:
        click.echo(f"Prunable: {match.get('prunable_reason') or 'yes'}")


def show_worktree(name_or_path: str, as_json: bool) -> None:
    """Show detail for one worktree, identified by trailing path segment or full path."""
    root = _project_root()
    rows = _enriched_list(root)

    target = None
    candidate = Path(name_or_path)
    if candidate.is_absolute():
        target_path = candidate.resolve()
        target = next((r for r in rows if Path(r["path"]).resolve() == target_path), None)
    if target is None:
        # Match by trailing path segment (e.g. 'e-967' matches '.endless/worktrees/e-967')
        for r in rows:
            if Path(r["path"]).name == name_or_path:
                target = r
                break

    if target is None:
        raise click.ClickException(f"No worktree matches: {name_or_path}")

    if as_json:
        click.echo(json.dumps(target, indent=2))
        return

    click.echo(f"State:   {target['state']}")
    click.echo(f"Path:    {target['path']}")
    click.echo(f"Branch:  {target['branch'] or '(detached)'}")
    click.echo(f"HEAD:    {target['head']}")
    if target["companion"]:
        sc = target["companion"]
        click.echo(f"--- companion ---")
        click.echo(json.dumps(sc, indent=2))
    if target["locked"]:
        click.echo(f"Locked:  {target.get('lock_reason') or 'yes'}")
    if target["prunable"]:
        click.echo(f"Prunable: {target.get('prunable_reason') or 'yes'}")


def for_task(task_id: str, as_json: bool) -> None:
    """Resolve a task ID (e.g. E-967 or 967) to its worktree path."""
    m = re.fullmatch(r"(?:[Ee]-)?(\d+)", task_id.strip())
    if m is None:
        raise click.ClickException(f"Invalid task id: {task_id}")
    canonical = f"E-{m.group(1)}"

    root = _project_root()
    rows = _enriched_list(root)
    match = next(
        (r for r in rows if r["companion"] and r["companion"].get("task_id") == canonical),
        None,
    )

    if match is None:
        if as_json:
            click.echo(json.dumps({"task_id": canonical, "worktree": None}))
        else:
            click.echo(f"No endless-managed worktree for {canonical}.")
        return

    if as_json:
        click.echo(json.dumps({
            "task_id": canonical,
            "worktree": match["path"],
            "branch": match["branch"],
            "head": match["head"],
        }))
    else:
        click.echo(match["path"])


# --- Mutation: land + drop -------------------------------------------------

def _is_auto_commit_path(rel_path: str) -> bool:
    """True if rel_path matches any AUTO_COMMIT_GLOBS pattern."""
    for pat in AUTO_COMMIT_GLOBS:
        if fnmatch.fnmatch(rel_path, pat):
            return True
    return False


def _git_status_partition(repo_root: Path) -> tuple[list[str], list[str]]:
    """Run `git status --porcelain -z` from repo_root and partition file paths.

    Returns (auto_commit_files, user_work_files) — both lists of repo-relative
    paths. Untracked files included.
    """
    out = subprocess.run(
        ["git", "status", "--porcelain", "-z"],
        capture_output=True, text=True, check=True, cwd=str(repo_root),
    ).stdout
    auto, user = [], []
    if not out:
        return auto, user
    # -z output is NUL-separated entries: "XY <path>\0" (and "XY <path>\0<oldpath>\0" for renames)
    entries = out.split("\0")
    i = 0
    while i < len(entries):
        entry = entries[i]
        if not entry:
            i += 1
            continue
        if len(entry) < 4:
            i += 1
            continue
        status = entry[:2]
        path = entry[3:]
        # Renames have an oldpath in the next entry
        if "R" in status or "C" in status:
            i += 2
        else:
            i += 1
        if _is_auto_commit_path(path):
            auto.append(path)
        else:
            user.append(path)
    return auto, user


def _git_run(args: list[str], cwd: Path, check: bool = True) -> subprocess.CompletedProcess:
    """Run a git command with text capture, returning the CompletedProcess.

    Distinct from `_git` (which returns trimmed stdout): land/drop logic
    needs access to stderr and exit code for branching, not just stdout.
    """
    return subprocess.run(
        ["git", *args],
        capture_output=True, text=True, check=check, cwd=str(cwd),
    )


def _read_verbs_list(path: Path) -> list[dict]:
    """Read a verbs.json file as a list of dicts. Returns [] if missing or malformed."""
    if not path.exists():
        return []
    try:
        data = json.loads(path.read_text())
    except (OSError, json.JSONDecodeError):
        return []
    return data if isinstance(data, list) else []


def _dedup_worktree_verbs_against_main(worktree_path: Path, main_root: Path) -> bool:
    """Bundle the worktree's verbs.json additions into a single commit on
    the worktree's branch, deduped against main's verbs.json (E-1141 / E-1138).

    Per E-1141: agents adding verbs in worktree sessions accumulate dirt in
    the worktree's verbs.json (post-E-1137). At land time, two worktrees that
    independently added the same verb would otherwise produce a textual
    rebase conflict. This step computes a set-union by `value` key —
    main's entries first (preserving order), then worktree's new ones —
    and writes the deduped result to the worktree before rebase. The result
    is a strict superset of main, so rebase replays cleanly.

    Returns True if a commit was created on the worktree's branch, False
    otherwise (no dirt, or dedup result equals current committed state).
    """
    wt_verbs = worktree_path / ".endless" / "verbs.json"
    main_verbs = main_root / ".endless" / "verbs.json"

    if not wt_verbs.exists():
        return False

    initial_status = _git_run(
        ["status", "--porcelain", "--", ".endless/verbs.json"],
        cwd=worktree_path,
    ).stdout
    if not initial_status.strip():
        return False

    main_entries = _read_verbs_list(main_verbs)
    wt_entries = _read_verbs_list(wt_verbs)
    main_values = {e.get("value") for e in main_entries if isinstance(e, dict)}
    new_from_wt = [
        e for e in wt_entries
        if isinstance(e, dict) and e.get("value") not in main_values
    ]
    merged = main_entries + new_from_wt
    wt_verbs.write_text(json.dumps(merged, indent=2) + "\n")

    post_status = _git_run(
        ["status", "--porcelain", "--", ".endless/verbs.json"],
        cwd=worktree_path,
    ).stdout
    if not post_status.strip():
        return False

    _git_run(["add", "--", ".endless/verbs.json"], cwd=worktree_path)
    _git_run(
        ["commit", "-m", "Endless: bundle worktree verb additions"],
        cwd=worktree_path,
    )
    return True


def _branch_for_task(rows: list[dict], task_id: str) -> dict | None:
    """Find the worktree row whose companion's task_id matches."""
    for r in rows:
        if r["companion"] and r["companion"].get("task_id") == task_id:
            return r
    return None


def _normalize_task_id(task_id: str) -> str:
    m = re.fullmatch(r"(?:[Ee]-)?(\d+)", task_id.strip())
    if m is None:
        raise click.ClickException(f"Invalid task id: {task_id}")
    return f"E-{m.group(1)}"


def land_worktree(task_id: str, dry_run: bool) -> None:
    """Land the worktree for <task-id> into main per E-987's algorithm.

    Loop:
      1. Partition git status into auto-commit vs user-work.
      2. If user-work is non-empty: refuse with actionable message.
      3. If auto-commit is non-empty: 'git add' and commit them as
         'Endless: auto-record session activity'.
      4. Rebase the worktree branch onto main (in the worktree).
      5. ff-merge from main.
      6. Remove the worktree.

    Retry up to LAND_MAX_RETRIES if a concurrent writer dirties auto-files
    between auto-commit and merge attempt.
    """
    canonical = _normalize_task_id(task_id)
    main_root = _project_root()
    rows = _enriched_list(main_root)
    target = _branch_for_task(rows, canonical)
    if target is None:
        raise click.ClickException(
            f"No endless-managed worktree for {canonical}. "
            f"(Use 'endless worktree list' to see available worktrees.)"
        )
    branch = target["branch"]
    if not branch:
        raise click.ClickException(
            f"Worktree for {canonical} has no branch (detached HEAD); cannot land."
        )
    worktree_path = Path(target["path"])
    base_branch = (target["companion"] or {}).get("base_branch", "main")

    if dry_run:
        click.echo(f"Would land: {canonical}")
        click.echo(f"  Worktree: {worktree_path}")
        click.echo(f"  Branch:   {branch}")
        click.echo(f"  Base:     {base_branch}")
        click.echo(f"  Main:     {main_root}")
        return

    last_error = None
    for attempt in range(1, LAND_MAX_RETRIES + 1):
        # Step 1: partition main's working-tree dirt.
        try:
            auto_files, user_files = _git_status_partition(main_root)
        except subprocess.CalledProcessError as e:
            raise click.ClickException(f"git status failed: {e.stderr or e}")

        # Step 2: refuse if user-work dirty.
        if user_files:
            file_list = "\n  ".join(user_files[:20])
            more = "" if len(user_files) <= 20 else f"\n  ... and {len(user_files) - 20} more"
            raise click.ClickException(
                f"main has uncommitted user changes; cannot land {canonical}.\n\n"
                f"Files:\n  {file_list}{more}\n\n"
                f"Resolve them: commit (in a worktree), move to a worktree, or set them aside, then retry."
            )

        # Step 3: auto-commit endless-managed dirt, if any.
        if auto_files:
            try:
                _git_run(["add", "--", *auto_files], cwd=main_root)
                _git_run(
                    ["commit", "-m", "Endless: auto-record session activity"],
                    cwd=main_root,
                )
            except subprocess.CalledProcessError as e:
                raise click.ClickException(
                    f"auto-commit failed: {e.stderr or e}"
                )

        # Step 3.5: dedup the worktree's verbs.json against main's, committing
        # the bundled result on the worktree's branch (E-1141 / E-1138).
        try:
            _dedup_worktree_verbs_against_main(worktree_path, main_root)
        except subprocess.CalledProcessError as e:
            raise click.ClickException(
                f"verbs.json dedup on worktree failed: {e.stderr or e}"
            )

        # Step 4: rebase the worktree branch onto main.
        try:
            _git_run(["rebase", base_branch], cwd=worktree_path)
        except subprocess.CalledProcessError as e:
            err_text = (e.stderr or "") + (e.stdout or "")
            # Try to recover and report
            _git_run(["rebase", "--abort"], cwd=worktree_path, check=False)
            raise click.ClickException(
                f"rebase of {branch} onto {base_branch} failed.\n\n"
                f"Likely cause: a worktree branch commit modifies an "
                f"endless-managed auto-file (events log, snapshot, or "
                f"config.json). This violates the E-972 routing rule and "
                f"shouldn't normally happen.\n\n"
                f"Recover: from the worktree, run\n"
                f"  git checkout {base_branch} -- "
                f"{' '.join(AUTO_COMMIT_GLOBS)}\n"
                f"then retry land. (E-1019 will eventually automate this.)\n\n"
                f"Git output:\n{err_text}"
            )

        # Step 5: ff-merge.
        try:
            _git_run(["merge", "--ff-only", branch], cwd=main_root)
        except subprocess.CalledProcessError as e:
            err_text = (e.stderr or "") + (e.stdout or "")
            if "uncommitted" in err_text.lower() or "would be overwritten" in err_text.lower():
                # Concurrent writer dirtied auto-files between our auto-commit
                # and the merge attempt. Loop.
                last_error = err_text
                continue
            raise click.ClickException(
                f"ff-merge failed: {err_text}"
            )

        # Step 6: remove the worktree.
        try:
            _git_run(["worktree", "remove", str(worktree_path)], cwd=main_root)
        except subprocess.CalledProcessError as e:
            err_text = (e.stderr or "") + (e.stdout or "")
            click.echo(
                click.style("•", fg="yellow")
                + f" Landed {canonical} but worktree removal failed: {err_text}\n"
                f"Manual cleanup: git worktree remove {worktree_path}"
            )
        else:
            # Also delete the branch since it's fully merged
            _git_run(["branch", "-d", branch], cwd=main_root, check=False)

        click.echo(
            click.style("•", fg="green")
            + f" Landed {canonical} ({branch}) into {base_branch}"
        )
        return

    raise click.ClickException(
        f"Land of {canonical} failed after {LAND_MAX_RETRIES} retries; "
        f"another session is appending to auto-files faster than land "
        f"can converge. Try again later.\n\nLast error:\n{last_error or '(none)'}"
    )


def drop_worktree(name_or_path: str, force: bool) -> None:
    """Remove a worktree explicitly. Refuses dirty/unmerged/foreign without --force."""
    main_root = _project_root()
    rows = _enriched_list(main_root)

    # Find by trailing path segment or absolute path
    target = None
    candidate = Path(name_or_path)
    if candidate.is_absolute():
        target_path = candidate.resolve()
        target = next(
            (r for r in rows if Path(r["path"]).resolve() == target_path),
            None,
        )
    if target is None:
        for r in rows:
            if Path(r["path"]).name == name_or_path:
                target = r
                break
    if target is None:
        raise click.ClickException(f"No worktree matches: {name_or_path}")
    if target["state"] == "main":
        raise click.ClickException("Refusing to drop the main checkout.")

    worktree_path = Path(target["path"])

    if not force:
        if target["state"] == "foreign":
            raise click.ClickException(
                f"Refusing to drop foreign worktree (no endless companion): "
                f"{worktree_path}\n"
                f"Use --force to drop anyway, or remove via 'git worktree remove'."
            )
        # Check for uncommitted changes in the worktree
        try:
            res = _git_run(
                ["status", "--porcelain"],
                cwd=worktree_path,
                check=True,
            )
            if res.stdout.strip():
                raise click.ClickException(
                    f"Worktree has uncommitted changes: {worktree_path}\n"
                    f"Commit or discard them, or use --force."
                )
        except subprocess.CalledProcessError as e:
            raise click.ClickException(f"git status check failed: {e.stderr or e}")

    cmd = ["worktree", "remove"]
    if force:
        cmd.append("--force")
    cmd.append(str(worktree_path))
    try:
        _git_run(cmd, cwd=main_root)
    except subprocess.CalledProcessError as e:
        raise click.ClickException(
            f"git worktree remove failed: {e.stderr or e}"
        )

    click.echo(
        click.style("•", fg="cyan")
        + f" Dropped worktree: {worktree_path}"
    )

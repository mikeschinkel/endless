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
task claim.

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
import os
import re
import shutil
import subprocess
from datetime import datetime, timezone
from pathlib import Path

import click

from endless.task_cmd import _resolve_project, recover_task_text


COMPANION_FILENAME = ".endless/worktree.json"
LOCK_FILENAME = ".endless/worktree.lock"

# Auto-committed file globs per E-987 (locked), modified by E-1141:
# verbs.json is in (ambient agent-driven churn); config.json is out
# (deliberate human/agent edits whose attribution the user controls).
# Land treats these as endless-managed: dirty state in any of these does
# not block land; instead, land auto-commits them as a separate commit
# before the worktree's commits.
AUTO_COMMIT_GLOBS = (
    ".endless/db-ledger/*.jsonl",
    ".endless/verbs.jsonl",
    ".endless/verbs.json",  # legacy — still seen during E-1268 migration
)

# Mirrors internal/events/commit.go (E-1342). Subjects whose auto-commits
# can amend in place via canAmend, producing orphans at the base of task
# branches when main amends past a branch's fork-point SHA. The orphan-drop
# pre-step in land_worktree() filters on this set.
AMENDABLE_COMMIT_SUBJECTS = (
    "Endless: record ledger entry",   # LedgerCommitSubject
)

# Land's retry cap for the race-with-concurrent-writers loop (E-987).
LAND_MAX_RETRIES = 8

# E-1500: minimum stripped length for tasks.text (or a committed plan file)
# to count as a viable plan. Empirically derived from the task ledger: every
# junk/placeholder plan is <=34 chars and every genuine plan is >=351 chars,
# so 128 rejects all observed junk while accepting all observed real plans.
PLAN_VIABILITY_MIN_CHARS = 128


def _is_retryable_ff_merge_error(err_text: str) -> bool:
    """True when a Step 5 ff-merge failure is from a concurrent writer
    rather than a hard error, and the land loop should retry.

    Two race windows produce retryable errors:

    1. Worktree-side: a concurrent writer dirtied auto-files between
       Step 3's auto-commit and Step 5's merge. Git surfaces this as
       "uncommitted changes" / "would be overwritten".
    2. Main-side (E-1351): a concurrent writer appended a commit to
       main between Step 4's rebase and Step 5's ff-merge, so the
       branches diverge. Git surfaces this as "diverging branches" /
       "not possible to fast-forward".

    In both cases the next loop iteration converges (re-runs auto-commit
    and rebase against main's new tip).
    """
    err_lower = err_text.lower()
    return (
        "uncommitted" in err_lower
        or "would be overwritten" in err_lower
        or "diverging" in err_lower
        or "not possible to fast-forward" in err_lower
    )


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


# E-1301: path convention is the canonical source of truth for "what task
# does this worktree belong to". The companion's task_id field is no longer
# trusted (it can outlive the worktree's actual identity — see E-1298's
# E-1186 stale-companion incident). Match anchored to /.endless/worktrees/
# under any project root so trailing path components (subdirs of the
# worktree) match too. Optional `-slug` suffix is allowed per the
# create_task_worktree convention.
_WORKTREE_TASK_ID_RE = re.compile(
    r"/\.endless/worktrees/e-(\d+)(?:-[a-z0-9-]+)?(?:/|$)"
)


def _task_id_from_worktree_path(path: Path) -> str | None:
    """Return the canonical 'E-NNN' task id encoded in a worktree path,
    or None if the path is not under a recognized worktree directory.

    Pure function — no filesystem or DB I/O. The directory name is the
    authoritative source per E-971's convention + E-1301's audit.
    """
    m = _WORKTREE_TASK_ID_RE.search(str(path))
    if m is None:
        return None
    return f"E-{m.group(1)}"


def _warn_if_companion_disagrees(worktree_path: Path, companion: dict | None) -> None:
    """If a legacy companion carries a task_id that disagrees with the
    path-derived task_id, emit a stderr warning. Path always wins; the
    warning is informational so stale companions become visible (E-1301).

    No-op for new companions (task_id no longer written) and for path/
    companion pairs that agree.
    """
    if not companion:
        return
    legacy = companion.get("task_id")
    if not legacy:
        return
    from_path = _task_id_from_worktree_path(worktree_path)
    if from_path is not None and legacy != from_path:
        import sys
        sys.stderr.write(
            f"endless: stale companion in {worktree_path}/.endless/worktree.json: "
            f"task_id={legacy!r} disagrees with path-derived {from_path!r}; "
            f"using {from_path!r}.\n"
        )


def _check_worktree_lock_liveness(worktree_path: Path) -> tuple[str, dict | None]:
    """Inspect <worktree>/.endless/worktree.lock for liveness (E-1209).

    Returns (state, lock_data) where state is one of:
      - "absent":    no lock file
      - "alive":     lock owner's PID responds to kill(pid, 0)
      - "stale":     PID is gone (ESRCH) or invalid
      - "malformed": file present but unparseable

    Mirrors monitor.IsWorktreeLockStale semantics from E-971: never
    reclaim a lock we cannot conclusively prove dead (PermissionError
    on kill(pid, 0) means a different uid owns it, treat as alive).
    """
    lock_path = worktree_path / LOCK_FILENAME
    if not lock_path.exists():
        return ("absent", None)
    try:
        data = json.loads(lock_path.read_text())
    except (json.JSONDecodeError, OSError):
        return ("malformed", None)
    pid = data.get("pid")
    if not isinstance(pid, int) or pid <= 0:
        return ("malformed", data)
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return ("stale", data)
    except PermissionError:
        return ("alive", data)
    except OSError:
        return ("alive", data)
    return ("alive", data)


def _drop_orphan_amendable_commits(
    worktree_path: Path, base_branch: str
) -> tuple[int, str | None]:
    """Drop contiguous orphan auto-amend commits at branch base (E-1342).

    canAmend (internal/events/commit.go) rewrites the SHA of ledger
    auto-commits on main as new events are appended. Branches
    forked off the old SHA carry an orphan that conflicts on rebase even
    though main has the equivalent (superset) content under a new SHA.
    This helper detects contiguous orphans at the BASE of the branch
    and strips them via a single 'rebase --onto base last-orphan HEAD'.

    Returns (count_dropped, first_subject):
      - (0, None) when no orphans found; helper is a no-op.
      - (N, subj) when N >= 1 orphans dropped; subj is the oldest
        dropped commit's subject (for the caller's advisory log).

    Mid-branch orphans (a non-amendable commit followed by an amendable
    one) are out of scope per D2: such layouts only arise from
    pre-E-1309 contamination, and dropping a mid-branch commit risks
    deleting work the user intended.
    """
    out = _git_run(
        ["log", "--reverse", "--format=%H %s", f"{base_branch}..HEAD"],
        cwd=worktree_path,
    )
    lines = [ln for ln in out.stdout.splitlines() if ln.strip()]
    if not lines:
        return (0, None)

    last_orphan_sha: str | None = None
    first_subject: str | None = None
    n = 0
    for line in lines:
        sha, _, subject = line.partition(" ")
        if subject in AMENDABLE_COMMIT_SUBJECTS:
            last_orphan_sha = sha
            if first_subject is None:
                first_subject = subject
            n += 1
        else:
            break

    if last_orphan_sha is None:
        return (0, None)

    # NOTE: do NOT pass "HEAD" as the third positional arg. `git rebase
    # --onto X Y HEAD` detaches HEAD before replaying commits, leaving
    # the branch ref pinned at its pre-rebase tip (E-1355). When the
    # subsequent ff-merge in land Step 5 targets the branch name, it
    # tries to fast-forward main to an ancestor commit and fails with
    # "diverging branches" — permanently, regardless of retry count.
    # Omitting the third arg keeps HEAD attached and moves the branch
    # ref with the rebase.
    _git_run(
        ["rebase", "--onto", base_branch, last_orphan_sha],
        cwd=worktree_path,
    )
    return (n, first_subject)


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
        # E-1301: task_id comes from the path convention, not the
        # companion's task_id field (which can lie when stale).
        task = _task_id_from_worktree_path(Path(r["path"])) or ""
        if r["companion"]:
            _warn_if_companion_disagrees(Path(r["path"]), r["companion"])
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
    # E-1301: Task id comes from the path convention, not from the companion's
    # task_id field. The companion's other fields (base_branch, created_at)
    # remain authoritative — they're set on create and don't drift.
    if task_id := _task_id_from_worktree_path(Path(match["path"])):
        click.echo(f"Task:    {task_id}")
    if match["companion"]:
        _warn_if_companion_disagrees(Path(match["path"]), match["companion"])
        sc = match["companion"]
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
    # E-1301: match by path-derived task id, not companion task_id.
    match = next(
        (r for r in rows
         if _task_id_from_worktree_path(Path(r["path"])) == canonical),
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


def _guard_dirty_worktree(worktree_path: Path, branch: str, canonical: str) -> None:
    """Refuse land if the worktree's working tree has uncommitted files (E-1416).

    Step 1's partition runs on main; Step 4's rebase runs in the worktree.
    Without this guard, uncommitted files in the worktree make rebase abort
    with git's generic "You have unstaged changes" error — no file list,
    wrong recovery hint.

    Refuses separately for auto-managed dirt (an upstream writer bug worth
    surfacing rather than papering over) and unmanaged user dirt (offers
    worktree-specific recovery options).
    """
    try:
        wt_auto, wt_user = _git_status_partition(worktree_path)
    except subprocess.CalledProcessError as e:
        raise click.ClickException(
            f"git status in worktree failed: {e.stderr or e}"
        )
    if wt_auto:
        file_list = "\n  ".join(wt_auto[:20])
        more = "" if len(wt_auto) <= 20 else f"\n  ... and {len(wt_auto) - 20} more"
        raise click.ClickException(
            f"worktree for {canonical} has uncommitted auto-managed files; "
            f"cannot land.\n\n"
            f"Files:\n  {file_list}{more}\n\n"
            f"These paths are owned by endless writers that commit them at "
            f"write time. Their presence here means a writer is broken or "
            f"skipped its commit. Report the writer that produced these "
            f"files; do not auto-commit them manually."
        )
    if wt_user:
        file_list = "\n  ".join(wt_user[:20])
        more = "" if len(wt_user) <= 20 else f"\n  ... and {len(wt_user) - 20} more"
        raise click.ClickException(
            f"worktree for {canonical} has uncommitted user changes; "
            f"cannot land.\n\n"
            f"Files:\n  {file_list}{more}\n\n"
            f"Resolve from inside the worktree:\n"
            f"  - commit on {branch} (most common)\n"
            f"  - move the file aside (mv outside the worktree)\n"
            f"  - revert if unwanted (git checkout -- <file>)\n"
            f"then retry land."
        )


def _read_verbs_list(path: Path) -> list[dict]:
    """Read a verbs.jsonl file as a list of dicts (E-1268).

    Reads JSONL (one object per line). For backward compatibility with the
    pre-E-1268 array format, if `path` ends in `.json` the file is parsed
    as a top-level JSON array. Returns [] if missing or malformed.
    """
    if not path.exists():
        return []
    try:
        text = path.read_text()
    except OSError:
        return []
    if path.suffix == ".json":
        try:
            data = json.loads(text)
        except json.JSONDecodeError:
            return []
        return data if isinstance(data, list) else []
    entries: list[dict] = []
    for line in text.splitlines():
        line = line.strip()
        if not line:
            continue
        try:
            obj = json.loads(line)
        except json.JSONDecodeError:
            continue
        if isinstance(obj, dict):
            entries.append(obj)
    return entries


def _write_verbs_jsonl(path: Path, verbs: list[dict]) -> None:
    """Write a list of verb dicts as JSONL — one object per line."""
    path.parent.mkdir(parents=True, exist_ok=True)
    lines = [json.dumps(v, separators=(", ", ": ")) for v in verbs]
    path.write_text("\n".join(lines) + ("\n" if lines else ""))


def _dedup_worktree_verbs_against_main(worktree_path: Path, main_root: Path) -> bool:
    """Bundle the worktree's verbs.jsonl additions into a single commit on
    the worktree's branch, deduped against main's verbs.jsonl (E-1141 / E-1138).

    Per E-1141: agents adding verbs in worktree sessions accumulate dirt in
    the worktree's verbs file. At land time, two worktrees that independently
    added the same verb would otherwise produce a textual rebase conflict.
    This step computes a set-union by `value` key — main's entries first
    (preserving order), then worktree's new ones — and writes the deduped
    result to the worktree before rebase. The result is a strict superset
    of main, so rebase replays cleanly.

    With E-1268 the file is JSONL and `.gitattributes` carries a merge=union
    driver, which makes concurrent appends auto-merge even without this
    dedup. The dedup remains as belt-and-suspenders for same-value-on-both-
    sides edits where union would produce duplicates.

    Returns True if a commit was created on the worktree's branch, False
    otherwise (no dirt, or dedup result equals current committed state).
    """
    wt_verbs = worktree_path / ".endless" / "verbs.jsonl"
    main_verbs = main_root / ".endless" / "verbs.jsonl"

    if not wt_verbs.exists():
        return False

    initial_status = _git_run(
        ["status", "--porcelain", "--", ".endless/verbs.jsonl"],
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
    _write_verbs_jsonl(wt_verbs, merged)

    post_status = _git_run(
        ["status", "--porcelain", "--", ".endless/verbs.jsonl"],
        cwd=worktree_path,
    ).stdout
    if not post_status.strip():
        return False

    _git_run(["add", "--", ".endless/verbs.jsonl"], cwd=worktree_path)
    _git_run(
        ["commit", "-m", "Endless: bundle worktree verb additions"],
        cwd=worktree_path,
    )
    return True


def _branch_for_task(rows: list[dict], task_id: str) -> dict | None:
    """Find the worktree row whose path encodes the given task id (E-1301).

    Path convention `.endless/worktrees/e-NNN[-slug]` is the canonical
    source; the companion's task_id field is no longer trusted.
    """
    for r in rows:
        if _task_id_from_worktree_path(Path(r["path"])) == task_id:
            return r
    return None


def _reap_stale_worktrees(project_root: Path) -> None:
    """Run the worktree reaper sweep (E-1337). Best-effort: shells out
    to `endless-go event reap-worktrees`. Stderr from the helper is
    forwarded so reaped-dir log lines reach the user; non-zero exit
    raises subprocess.CalledProcessError (caller decides how loud).
    """
    from endless import config

    binary = shutil.which("endless-go")
    if not binary:
        return
    # E-1429: thread the resolved --db context so this DB-opening subprocess
    # isn't refused by the self-dev-worktree gate when land runs from inside a
    # worktree. Empty (no flag) outside a gated worktree, so a no-op there.
    subprocess.run(
        [binary, *config.go_db_context_args(), "event", "reap-worktrees",
         "--project-root", str(project_root)],
        check=True,
    )


def _normalize_task_id(task_id: str) -> str:
    m = re.fullmatch(r"(?:[Ee]-)?(\d+)", task_id.strip())
    if m is None:
        raise click.ClickException(f"Invalid task id: {task_id}")
    return f"E-{m.group(1)}"


_FILLER_WORDS = frozenset({
    "a", "an", "the", "to", "from", "of", "for", "with",
    "in", "on", "at", "by", "and", "or",
})


def _tilde(p: Path) -> str:
    """Display a Path with $HOME collapsed to ~. Falls back to absolute."""
    s = str(p)
    home = str(Path.home())
    return s.replace(home, "~", 1) if s.startswith(home) else s


def _slugify_title(title: str) -> str:
    """Slug per E-971 spec for task branch names.

    Lowercase, drop filler words, replace non-alnum with '-', collapse
    repeats, truncate to 40 chars at a word boundary. Returns 'task' if
    the input contains only filler/punctuation.
    """
    cleaned = re.sub(r"[^a-z0-9]+", " ", title.lower())
    words = [w for w in cleaned.split() if w and w not in _FILLER_WORDS]
    slug = "-".join(words)
    if len(slug) > 40:
        truncated = slug[:40]
        # Only back up to the last '-' if the cut landed mid-word.
        if slug[40] != "-" and "-" in truncated:
            truncated = truncated.rsplit("-", 1)[0]
        slug = truncated
    return slug or "task"


def _default_base_branch(project_root: Path) -> str:
    """Best-effort default-branch detection. Falls back to 'main'.

    Limitation tracked in E-1166: origin/HEAD may be unset on fresh
    clones, leaving us with the literal 'main' fallback even when the
    repo's actual default is master/develop.
    """
    try:
        ref = _git(["symbolic-ref", "--quiet", "refs/remotes/origin/HEAD"], cwd=project_root)
    except subprocess.CalledProcessError:
        return "main"
    return ref.removeprefix("refs/remotes/origin/") or "main"


def _check_plan_file_committed(task_id: int, project_root: Path) -> str | None:
    """If .endless/plans/E-<id>.md exists but is dirty/untracked in main,
    return an error message with recommended commands. Otherwise None.

    The plan file lives in main's working tree but won't propagate to a
    new worktree (git worktree add starts from a commit, not the index).
    Per E-1169, refuse with recommendations rather than auto-commit.
    """
    plan_rel = f".endless/plans/E-{task_id}.md"
    plan_abs = project_root / plan_rel
    if not plan_abs.exists():
        return None
    res = _git_run(
        ["status", "--porcelain", "--", plan_rel],
        cwd=project_root, check=False,
    )
    if res.returncode != 0 or not res.stdout.strip():
        return None
    root_display = _tilde(project_root)
    return (
        f"Plan file {plan_rel} is uncommitted in main; it will not "
        f"appear in the new worktree.\n\n"
        f"Capture it before starting the task. Recommended:\n"
        f"  git -C {root_display} add {plan_rel}\n"
        f"  git -C {root_display} commit -m 'Add plan for E-{task_id}'\n"
        f"\nThen retry: endless task claim E-{task_id}"
    )


# --- E-1500: orphan-branch recovery ----------------------------------------
#
# `worktree drop` (git worktree remove) and the land/reap path both leave the
# task branch behind after the worktree directory is gone. The next claim/spawn
# then hits `git worktree add -b <branch>` -> "a branch already exists" with no
# remediation. These helpers let create_task_worktree recover instead: classify
# the orphan branch's unique delta from main, and either recreate fresh
# (plan-only / no work) or refuse with an actionable message (real work).


def _branch_exists(branch: str, project_root: Path) -> bool:
    return _git_run(
        ["rev-parse", "--verify", "--quiet", f"refs/heads/{branch}"],
        cwd=project_root, check=False,
    ).returncode == 0


def _branch_unique_files(base: str, branch: str, project_root: Path) -> list[str]:
    """Repo-relative paths changed on `branch` since it forked from `base`.

    Three-dot diff: changes on the branch side of the merge-base only. Empty
    when the branch is an ancestor of base (behind/equal, no unique work).
    """
    res = _git_run(
        ["diff", "--name-only", f"{base}...{branch}"],
        cwd=project_root, check=False,
    )
    if res.returncode != 0:
        # Don't risk deleting a branch we couldn't analyze — refuse loudly.
        raise click.ClickException(
            f"Could not compare {branch} to {base}:\n{res.stderr or res.stdout}"
        )
    return [ln for ln in res.stdout.splitlines() if ln.strip()]


def _read_branch_file(branch: str, rel_path: str, project_root: Path) -> str | None:
    res = _git_run(
        ["show", f"{branch}:{rel_path}"], cwd=project_root, check=False,
    )
    return res.stdout if res.returncode == 0 else None


def _read_task_text(task_id: int, project_root: Path) -> str:
    """Current tasks.text via the `endless-go session-query` Go helper.

    Returns '' when empty/absent or the helper is unavailable. Python SQLite
    reads are forbidden (E-894), so there is no DB fallback.
    """
    from endless import config

    binary = shutil.which("endless-go")
    if not binary:
        return ""
    try:
        result = subprocess.run(
            [binary, *config.go_db_context_args(), "session-query", "task-text", "--id", str(task_id)],
            capture_output=True, text=True,
        )
    except OSError:
        return ""
    return result.stdout if result.returncode == 0 else ""


def _plan_viable(text: str) -> bool:
    return len(text.strip()) >= PLAN_VIABILITY_MIN_CHARS


def _plan_preview(text: str, n: int = 80) -> str:
    """One-line, length-capped preview for error messages (never a full dump)."""
    one_line = " ".join(text.strip().split())
    return one_line[:n] + ("…" if len(one_line) > n else "")


def _delete_orphan_branch(branch: str, project_root: Path) -> None:
    """Delete an orphan branch so creation can recreate it fresh.

    If a stale/prunable worktree registration still claims the branch (its dir
    is already gone), `git worktree prune` clears that bookkeeping — it touches
    no live work — and we retry the delete once.
    """
    res = _git_run(["branch", "-D", branch], cwd=project_root, check=False)
    if res.returncode == 0:
        return
    err = (res.stderr or "") + (res.stdout or "")
    if "checked out" in err or "used by worktree" in err:
        _git_run(["worktree", "prune"], cwd=project_root, check=False)
        res = _git_run(["branch", "-D", branch], cwd=project_root, check=False)
        if res.returncode == 0:
            return
        err = (res.stderr or "") + (res.stdout or "")
    root = _tilde(project_root)
    raise click.ClickException(
        f"Could not delete orphan branch {branch}:\n{err}\n"
        f"Resolve manually, then retry:\n"
        f"  git -C {root} worktree prune\n"
        f"  git -C {root} branch -D {branch}"
    )


def _orphan_real_work_msg(
    task_id: int, branch: str, base: str, non_plan: list[str], project_root: Path,
) -> str:
    root = _tilde(project_root)
    shown = "\n  ".join(non_plan[:20])
    more = "" if len(non_plan) <= 20 else f"\n  ... and {len(non_plan) - 20} more"
    return (
        f"E-{task_id}: branch {branch} has commits beyond {base} touching "
        f"non-plan files:\n  {shown}{more}\n\n"
        f"Inspect:\n"
        f"  git -C {root} log {base}..{branch}\n"
        f"  git -C {root} diff {base}...{branch}\n"
        f"Resume that work manually, or discard it and retry:\n"
        f"  git -C {root} branch -D {branch}"
    )


def _orphan_plan_mismatch_msg(
    task_id: int, branch: str, db_text: str, file_text: str, project_root: Path,
) -> str:
    root = _tilde(project_root)
    plan_rel = f".endless/plans/E-{task_id}.md"
    return (
        f"E-{task_id}: the plan in tasks.text differs from the plan committed "
        f"on branch {branch}.\n\n"
        f"  tasks.text  ({len(db_text.strip())} chars): \"{_plan_preview(db_text)}\"\n"
        f"  branch file ({len(file_text.strip())} chars): \"{_plan_preview(file_text)}\"\n\n"
        f"View full:\n"
        f"  endless task show E-{task_id} --text\n"
        f"  git -C {root} show {branch}:{plan_rel}\n"
        f"Keep the DB version, discard the branch:\n"
        f"  git -C {root} branch -D {branch}          # then retry\n"
        f"Adopt the branch's version into the DB:\n"
        f"  git -C {root} show {branch}:{plan_rel} > /tmp/E-{task_id}.md\n"
        f"  endless task update E-{task_id} --text /tmp/E-{task_id}.md   # then retry"
    )


def _orphan_text_not_viable_msg(
    task_id: int, branch: str, db_text: str, file_text: str, project_root: Path,
) -> str:
    root = _tilde(project_root)
    plan_rel = f".endless/plans/E-{task_id}.md"
    extra = ""
    if _plan_viable(file_text):
        extra = (
            f"\nThe branch's committed plan ({len(file_text.strip())} chars) may "
            f"be the one you want:\n  git -C {root} show {branch}:{plan_rel}"
        )
    return (
        f"E-{task_id}: tasks.text is too short to be a viable plan "
        f"({len(db_text.strip())} chars):\n  \"{_plan_preview(db_text)}\"\n\n"
        f"Write a real plan, then retry:\n"
        f"  endless task update E-{task_id} --text <file>{extra}"
    )


def _orphan_no_viable_plan_msg(task_id: int, branch: str) -> str:
    return (
        f"E-{task_id}: no viable plan in tasks.text or on branch {branch}.\n\n"
        f"Add one, then retry:\n"
        f"  endless task update E-{task_id} --text <file>"
    )


def _reconcile_orphan_plan(
    task_id: int, branch: str, plan_rel: str, project_root: Path,
) -> None:
    """Plan-only orphan branch. tasks.text (the DB) is the source of truth; the
    committed plan file is a derived mirror. Decide adopt / proceed / refuse.

    Returns normally when it's safe to delete the branch and recreate fresh
    (the plan re-materializes from tasks.text). Raises ClickException, with an
    actionable message, when the DB and file disagree or no viable plan exists.
    """
    file_text = _read_branch_file(branch, plan_rel, project_root) or ""
    db_text = _read_task_text(task_id, project_root)
    db_s, file_s = db_text.strip(), file_text.strip()

    if not db_s:
        # The DB has no plan; the committed file is all we have.
        if _plan_viable(file_s):
            recover_task_text(task_id, file_text)
            click.echo(
                click.style("•", fg="cyan")
                + f" Recovered plan for E-{task_id} from branch {branch} "
                f"into tasks.text"
            )
            return
        raise click.ClickException(_orphan_no_viable_plan_msg(task_id, branch))

    if not _plan_viable(db_s):
        raise click.ClickException(
            _orphan_text_not_viable_msg(task_id, branch, db_text, file_text, project_root)
        )

    if not file_s or file_s == db_s:
        return  # DB and file agree (or no file) -> recreate fresh from tasks.text

    raise click.ClickException(
        _orphan_plan_mismatch_msg(task_id, branch, db_text, file_text, project_root)
    )


def _handle_orphan_branch(
    task_id: int, branch: str, base: str, project_root: Path,
) -> None:
    """The branch exists but its worktree dir is gone. Either delete the branch
    (caller recreates fresh) or raise with actionable guidance.
    """
    plan_rel = f".endless/plans/E-{task_id}.md"
    unique = _branch_unique_files(base, branch, project_root)
    non_plan = [f for f in unique if f != plan_rel]
    if non_plan:
        raise click.ClickException(
            _orphan_real_work_msg(task_id, branch, base, non_plan, project_root)
        )
    if plan_rel in unique:
        _reconcile_orphan_plan(task_id, branch, plan_rel, project_root)
    # Empty delta, or a plan-only delta that reconciled cleanly -> safe.
    _delete_orphan_branch(branch, project_root)


def create_task_worktree(
    task_id: int, title: str, project_root: Path,
) -> tuple[Path, bool]:
    """Create the per-task worktree for E-<id>.

    Returns (worktree_path, created). 'created' is False if the worktree
    already existed for this task (idempotent no-op). Raises
    ClickException on path collision with a foreign worktree, on
    uncommitted plan files (per E-1169), or on git-add failure.
    """
    canonical = f"E-{task_id}"
    slug = _slugify_title(title)
    branch = f"task/{task_id}-{slug}"
    wt_dir = project_root / ".endless" / "worktrees" / f"e-{task_id}"
    base = _default_base_branch(project_root)

    if wt_dir.exists():
        # E-1301: path convention IS the identity. The directory's name
        # (`e-{task_id}`) is its identity by construction here; the
        # companion's existence is the "endless-managed marker" check.
        if _task_id_from_worktree_path(wt_dir) == canonical and _read_companion(wt_dir):
            return wt_dir, False
        raise click.ClickException(
            f"Path {_tilde(wt_dir)} exists but does not belong to {canonical}. "
            f"Resolve manually before retrying."
        )

    msg = _check_plan_file_committed(task_id, project_root)
    if msg:
        raise click.ClickException(msg)

    # E-1500: the dir is gone but the branch may still exist (orphan branch
    # left by `worktree drop` / land-reap). Recover instead of failing on
    # `git worktree add -b`: either delete the branch so we recreate it fresh
    # below, or raise with actionable guidance if it carries real work.
    if _branch_exists(branch, project_root):
        _handle_orphan_branch(task_id, branch, base, project_root)

    wt_dir.parent.mkdir(parents=True, exist_ok=True)
    try:
        _git_run(
            ["worktree", "add", "-b", branch, str(wt_dir), base],
            cwd=project_root,
        )
    except subprocess.CalledProcessError as e:
        raise click.ClickException(
            f"git worktree add failed for {canonical}:\n{e.stderr or e}"
        )

    companion_dir = wt_dir / ".endless"
    companion_dir.mkdir(parents=True, exist_ok=True)
    # E-1301: `task_id` is no longer written. The path convention
    # (`.endless/worktrees/e-NNN`) is the canonical source. The companion
    # file's other fields document the worktree's provenance; its mere
    # presence is the "this is an endless-managed worktree" marker.
    companion = {
        "kind": "task",
        "base_branch": base,
        "branch": branch,
        "created_at": datetime.now(timezone.utc).isoformat(),
    }
    (companion_dir / "worktree.json").write_text(
        json.dumps(companion, indent=2) + "\n"
    )
    _materialize_plan_file(task_id, wt_dir)
    _maybe_auto_sandbox_bind(project_root, wt_dir, task_id)
    return wt_dir, True


def _materialize_plan_file(task_id: int, worktree_path: Path) -> None:
    """Write <worktree>/.endless/plans/E-NNN.md from tasks.text (E-1445).

    This is the single point where a plan file is created on disk. `task
    update --text` no longer provisions a worktree; instead the plan
    materializes here when the worktree is born (at claim/spawn).

    Reads tasks.text via the `endless-go session-query` Go helper — Python DB
    reads are forbidden (E-894). Empty/absent text writes nothing. Failures
    warn and skip rather than abort worktree creation; a missing plan file is
    recoverable by re-running `endless task update --text` once the worktree
    exists (which mirrors into it).
    """
    from endless import config

    binary = shutil.which("endless-go")
    if not binary:
        click.echo(
            "  warning: endless-go not found on PATH; plan file "
            "not materialized.",
            err=True,
        )
        return
    try:
        # E-1429: thread the resolved --db context (no-op outside a gated
        # worktree) so this DB read isn't refused when claim runs from a
        # worktree cwd.
        result = subprocess.run(
            [binary, *config.go_db_context_args(), "session-query", "task-text", "--id", str(task_id)],
            capture_output=True, text=True,
        )
    except OSError as e:
        click.echo(f"  warning: endless-go session-query task-text: {e}", err=True)
        return
    if result.returncode != 0:
        click.echo(
            f"  warning: could not read plan text for E-{task_id}: "
            f"{(result.stderr or '').strip()}",
            err=True,
        )
        return
    if not result.stdout.strip():
        return
    plans_dir = worktree_path / ".endless" / "plans"
    plans_dir.mkdir(parents=True, exist_ok=True)
    target = plans_dir / f"E-{task_id}.md"
    target.write_text(result.stdout)
    click.echo(
        click.style("✓", fg="green")
        + f" Materialized plan to {_tilde(target)}"
    )
    _commit_plan_file_in_worktree(
        worktree_path, task_id, f"Endless: add plan for E-{task_id}",
    )


def _commit_plan_file_in_worktree(
    worktree_path: Path, task_id: int, subject: str,
) -> None:
    """Stage and commit <worktree>/.endless/plans/E-NNN.md on the worktree branch (E-1525).

    Called at both plan-file write sites — claim/spawn materialization and
    `task update --text` mirror — so the file rides to main on `worktree
    land` instead of sitting untracked and getting rejected by the dirty-
    worktree guard.

    `commit -o <plan_rel>` scopes the commit to just the plan file even if
    the worktree has unrelated dirt (user mid-edit, other auto-managed
    files). Returns silently when the file already matches HEAD — re-running
    a write with identical content is a no-op.
    """
    plan_rel = f".endless/plans/E-{task_id}.md"
    status = _git_run(
        ["status", "--porcelain", "--", plan_rel],
        cwd=worktree_path,
    ).stdout
    if not status.strip():
        return
    try:
        _git_run(["add", "--", plan_rel], cwd=worktree_path)
        _git_run(
            ["commit", "-o", plan_rel, "-m", subject], cwd=worktree_path,
        )
    except subprocess.CalledProcessError as e:
        detail = (e.stderr or e.stdout or str(e)).strip()
        raise click.ClickException(
            f"Failed to commit {plan_rel} in worktree: {detail}"
        )


def _maybe_auto_sandbox_bind(project_root: Path, worktree_path: Path, task_id: int) -> None:
    """If the project opts in, provision and bind a per-worktree sandbox DB.

    Triggered by `self_dev: true` in the project's .endless/config.json
    (see config.project_is_self_dev). Endless's own config has the flag set
    so dev-time worktrees don't pollute the user's real DB; downstream
    projects using endless as a tool leave it unset.

    Failures are surfaced as warnings rather than aborting the worktree
    creation — a failed sandbox setup is recoverable via `just dev-sandbox-init`
    or direct `endless-go sandbox init` / `bind` invocation.
    """
    from endless import config
    if not config.project_is_self_dev(project_root):
        return
    binary = shutil.which("endless-go")
    if not binary:
        click.echo(
            "  warning: endless-go binary not found on PATH; "
            "sandbox setup skipped.",
            err=True,
        )
        return
    name = f"worktree-e-{task_id}"
    for cmd in (
        [binary, "sandbox", "init", "--mode", "worktree", name],
        [binary, "sandbox", "bind", str(worktree_path), name],
    ):
        try:
            # cwd is the worktree so `init --mode worktree` can resolve the
            # main checkout via git-common-dir from there.
            result = subprocess.run(
                cmd, capture_output=True, text=True, cwd=str(worktree_path),
            )
        except OSError as e:
            click.echo(f"  warning: {' '.join(cmd)}: {e}", err=True)
            return
        if result.returncode != 0:
            click.echo(
                f"  warning: {' '.join(cmd)} failed: "
                f"{(result.stderr or result.stdout).strip()}",
                err=True,
            )
            return
    click.echo(
        click.style("•", fg="cyan")
        + f" sandbox provisioned: ~/.cache/endless/sandboxes/{name}"
    )


def _record_landing(
    item_id: int,
    proj_name: str,
    branch: str,
    base_branch: str,
    canonical: str,
    merge_sha: str,
) -> None:
    """Emit the task.landed event after a successful ff-merge (E-1337).

    The ff-merge in land_worktree() Step 5 has already advanced main, so the
    land has HAPPENED by the time this runs. If the emit fails (e.g. the
    session-attribution gate, or any transient), main is still advanced — so
    re-raise as a clearly re-runnable error that says main was advanced and
    recording failed, rather than one that implies nothing landed. The
    ff-merge is idempotent, so re-running `just land` records the landing once
    the cause is resolved (E-1474).
    """
    from endless.event_bridge import emit_event

    try:
        emit_event(
            kind="task.landed",
            project=proj_name,
            entity_type="task",
            entity_id=str(item_id),
            payload={
                "branch": branch,
                "merge_commit_sha": merge_sha,
            },
            prompt_verb="landed for",
        )
    except Exception as e:
        detail = e.message if isinstance(e, click.ClickException) else str(e)
        raise click.ClickException(
            f"Landed {canonical} ({branch}) into {base_branch}: main was "
            f"advanced, but recording the landing failed:\n\n{detail}\n\n"
            f"The ff-merge is idempotent. Re-run `just land {canonical}` to "
            f"record the landing once the cause above is resolved."
        )


def land_worktree(task_id: str, dry_run: bool) -> None:
    """Land the worktree for <task-id> into main per E-987 + E-1337.

    Loop:
      1. Partition git status into auto-commit vs user-work.
      2. If user-work is non-empty: refuse with actionable message.
      3. If auto-commit is non-empty: 'git add' and commit them as
         'Endless: auto-record session activity'.
      4. Rebase the worktree branch onto main (in the worktree).
      5. ff-merge from main.
      6. Emit task.landed event. Worktree dir and branch stay; a
         separate reaper sweep removes them after worktree_ttl.

    Retry up to LAND_MAX_RETRIES if a concurrent writer dirties auto-files
    between auto-commit and merge attempt. Re-landing after a follow-up
    commit is supported: each successful land appends a new row to
    task_landings; the dir and branch are reused.
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

        # Step 3.7: drop orphan auto-amend commits at branch base (E-1342).
        # canAmend in commit.go rewrites the ledger commit SHAs on
        # main as new events are appended; a branch forked off the old SHA
        # carries an orphan that conflicts on rebase. Strip them before
        # Step 4 so the rebase sees only the user's real commits.
        try:
            n_orphans, first_subj = _drop_orphan_amendable_commits(
                worktree_path, base_branch
            )
        except subprocess.CalledProcessError as e:
            _git_run(["rebase", "--abort"], cwd=worktree_path, check=False)
            raise click.ClickException(
                f"orphan auto-amend cleanup failed: "
                f"{(e.stderr or '') + (e.stdout or '')}"
            )
        if n_orphans:
            noun = "commit" if n_orphans == 1 else "commits"
            click.echo(
                click.style("•", fg="yellow")
                + f" Dropped {n_orphans} orphan auto-amend {noun} ({first_subj})"
            )

        # Step 3.8 (E-1416): guard against dirty worktree tree before rebase.
        _guard_dirty_worktree(worktree_path, branch, canonical)

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
                f"endless-managed auto-file (db-ledger entry or "
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
            if _is_retryable_ff_merge_error(err_text):
                last_error = err_text
                continue
            raise click.ClickException(
                f"ff-merge failed: {err_text}"
            )

        # Step 6 (E-1337): record the landing in task_landings via the
        # events bridge. Worktree directory and branch stay in place; a
        # separate reaper sweep removes them after worktree_ttl. The
        # state files .endless/worktree.{json,lock} are gitignored
        # (E-1218) and stay too — the reaper deletes the dir wholesale
        # when it eventually runs.
        try:
            merge_sha = subprocess.check_output(
                ["git", "rev-parse", "HEAD"],
                cwd=main_root,
                text=True,
            ).strip()
        except subprocess.CalledProcessError as e:
            raise click.ClickException(
                f"Landed {canonical} but reading merge SHA failed: {e.stderr or e}"
            )

        _, proj_name = _resolve_project(None)
        item_id = int(canonical[2:])
        # E-1474: the ff-merge above already advanced main, so the land has
        # happened. _record_landing surfaces any task.landed emit failure as a
        # re-runnable "main advanced, recording failed" error rather than one
        # that implies nothing landed.
        _record_landing(
            item_id, proj_name, branch, base_branch, canonical, merge_sha
        )

        click.echo(
            click.style("•", fg="green")
            + f" Landed {canonical} ({branch}) into {base_branch}"
        )

        # Best-effort sweep: clean up older landed worktrees that have
        # passed their TTL. Failure here doesn't unwind the land.
        try:
            _reap_stale_worktrees(main_root)
        except Exception as e:
            click.echo(
                click.style("•", fg="yellow")
                + f" reap sweep after land failed (non-fatal): {e}"
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

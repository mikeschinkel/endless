"""Inspection and mutation CLI for git worktrees managed by endless.

Inspection (foundation, E-971):
- list: enumerate all git worktrees of the current project, classified
- current: report the worktree for cwd
- show: detail for one worktree
- for-task: resolve a task ID to its worktree path

Mutation (this slice, E-971 + E-987 + E-1056):
- land: auto-commit endless-managed files, rebase worktree onto main,
  ff-merge, remove worktree
- drop: explicit cleanup (refuses modified/unlanded without --force)

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

from endless.task_cmd import _display_path, _resolve_project, recover_task_text
from endless.project_path import normalize


COMPANION_FILENAME = ".endless/worktree.json"
LOCK_FILENAME = ".endless/worktree.lock"

# Auto-committed file globs per E-987 (locked), modified by E-1141:
# verbs.jsonl is in (ambient agent-driven churn); config.json is out
# (deliberate human/agent edits whose attribution the user controls).
# Land treats these as endless-managed: modified state in any of these does
# not block land; instead, land auto-commits them as a separate commit
# before the worktree's commits.
#
# Mirrors internal/monitor.AutoManagedStatusGlobs (E-1758) — keep the two in
# sync. That Go definition is the one `endless worktree check` / `session
# status` partition against so a worktree modified only in these paths still
# reads as a clean handoff.
AUTO_COMMIT_GLOBS = (
    ".endless/db-ledger/*.jsonl",
    ".endless/verbs.jsonl",
)

# E-1736: the DB ledger directory, as a git pathspec. A commit under here
# on a task branch violates the ledger routing policy (ledger entries are
# recorded on the main checkout only) and must never ride a land into main.
DB_LEDGER_DIR = ".endless/db-ledger"


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
    return normalize(row[0]["path"])


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
# worktree) match too. Only the canonical bare `e-NNN` dir is recognized
# (ED-1515); a trailing `-slug` no longer resolves as the task's worktree.
_WORKTREE_TASK_ID_RE = re.compile(
    r"/\.endless/worktrees/e-(\d+)(?:/|$)"
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


def _ledger_touching_commits(
    worktree_path: Path, base_branch: str
) -> list[tuple[str, str]]:
    """Return (sha, subject) for every commit in base..HEAD that modifies a
    file under the DB ledger dir. Empty list when none.

    Backstop to the ledger routing policy: ledger entries are auto-committed
    on the main checkout only, so a branch-side commit under DB_LEDGER_DIR is
    always wrong and would be rebased into main by land. Intended to run AFTER
    Step 3.7's orphan-drop, so legitimately-orphaned base ledger commits are
    already gone and only genuine offenders remain.
    """
    out = _git_run(
        ["log", "--reverse", "--format=%H %s",
         f"{base_branch}..HEAD", "--", DB_LEDGER_DIR],
        cwd=worktree_path,
    )
    commits: list[tuple[str, str]] = []
    for ln in out.stdout.splitlines():
        if not ln.strip():
            continue
        sha, _, subject = ln.partition(" ")
        commits.append((sha, subject))
    return commits


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


def worktree_root_for_cwd() -> Path | None:
    """Return the endless-managed worktree root containing cwd, or None.

    E-1747: a decision authored from inside a task worktree lands its
    `.endless/decisions/ED-NNN.md` mirror there (riding that worktree's land);
    outside any endless worktree the caller falls back to committing on main.
    Best-effort — any git-resolution failure or a non-worktree toplevel
    returns None.
    """
    try:
        toplevel_str = _git(["rev-parse", "--show-toplevel"], cwd=Path.cwd())
    except (subprocess.CalledProcessError, OSError):
        return None
    toplevel = Path(toplevel_str).resolve()
    if not (toplevel / ".endless" / "worktree.json").exists():
        return None
    if _task_id_from_worktree_path(toplevel) is None:
        return None
    return toplevel


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


def check_worktree() -> None:
    """Report genuine git/worktree handoff anomalies for the current worktree.

    E-1758: the durable fix for handoff "nothing to report" noise. Prints one
    terse line per real anomaly (uncommitted user files, detached/wrong branch,
    a prunable/locked checkout) and NOTHING when clean — empty output IS the
    representation of a clean handoff. Deliberately silent on non-anomalies:
    commits ahead of main (expected before land) and git tags (endless makes
    none).

    Resolves the worktree from cwd and hands the Go core the worktree path plus
    the repo main checkout, so the shared anomaly probe (`session-query
    worktree-anomalies`) runs without any DB read (E-1766). The inspection is
    DB-free — git state from the worktree, expected branch from the companion
    file — so this surface and `session status` still share one definition.
    Exit code: 0 clean, 1 anomalies present, 2 on error.
    """
    root = worktree_root_for_cwd()
    if root is None:
        raise click.ClickException(
            "not inside an endless-managed worktree — run this from within a "
            "task worktree (.endless/worktrees/e-NNN)"
        )

    binary = shutil.which("endless-go")
    if not binary:
        raise click.ClickException("endless-go not found on PATH")

    # E-971 path convention: a worktree root is <main>/.endless/worktrees/e-NNN,
    # so its 3rd-level parent is the repo main checkout (which enables the
    # repo-level prunable/locked probe). Both paths are already in hand from the
    # cwd resolution above; passing them means the Go core never round-trips the
    # DB to rediscover them. That lookup was the ONLY DB touch — and in a
    # self-dev worktree it routed to the per-worktree sandbox (which lacks the
    # task row) and errored (E-1766). No --db context is threaded: no DB opens.
    main_root = str(root.parents[2])
    result = subprocess.run(
        [binary, "session-query", "worktree-anomalies",
         "--worktree-path", str(root), "--project-root", main_root],
        capture_output=True, text=True,
    )
    if result.stdout:
        click.echo(result.stdout, nl=False)
    if result.stderr:
        click.echo(result.stderr, nl=False, err=True)
    raise SystemExit(result.returncode)


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


def _is_auto_file(rel_path: str) -> bool:
    """True if rel_path is an endless-managed auto-file: it matches
    AUTO_COMMIT_GLOBS or lives under the DB ledger dir. A conflict confined to
    such paths has a mechanical recovery (restore from base); a conflict that
    touches anything else is real source work the user must reconcile."""
    return _is_auto_commit_path(rel_path) or rel_path.startswith(DB_LEDGER_DIR + "/")


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


def _display_path(p: Path) -> str:
    """Display a Path with $HOME collapsed to ~ (never a raw absolute path)."""
    s = str(p)
    home = str(Path.home())
    return s.replace(home, "~", 1) if s.startswith(home) else s


def _rebase_conflict_message(
    worktree_path: Path, base_branch: str, *, phase: str
) -> str:
    """Build the user-facing message for a rebase conflict encountered by land.

    Called from BOTH conflict handlers (Step 3.7 orphan-replay and Step 4 main
    rebase) WHILE the rebase is still in progress — before `git rebase --abort`
    — so it can read the conflict state (unmerged paths + REBASE_HEAD).

    Reports the FACTS confidently: which step (via `phase`), which of the user's
    commits failed to replay, and which files conflict. It offers recoveries as
    CANDIDATES to judge between, never one confident prescription — the confident
    misattribution is exactly the failure mode this task removes. Only when every
    conflicting path is an endless-managed auto-file is a single mechanical
    recovery presented with confidence; any source file makes the cause genuinely
    ambiguous, so the candidates are flagged as possibly-wrong.
    """
    unmerged = _git_run(
        ["diff", "--name-only", "--diff-filter=U"],
        cwd=worktree_path, check=False,
    ).stdout
    files = [ln for ln in unmerged.splitlines() if ln.strip()]

    # Name the commit that failed to replay, if git exposes REBASE_HEAD, so the
    # user knows which of their commits hit the conflict.
    head = _git_run(
        ["rev-parse", "--short", "REBASE_HEAD"],
        cwd=worktree_path, check=False,
    )
    commit_line = ""
    if head.returncode == 0 and head.stdout.strip():
        short = head.stdout.strip()
        subj = _git_run(
            ["log", "-1", "--format=%s", "REBASE_HEAD"],
            cwd=worktree_path, check=False,
        ).stdout.strip()
        commit_line = (
            f"Your commit that failed to replay: {short} {subj}\n\n"
            if subj else f"Your commit that failed to replay: {short}\n\n"
        )

    wt = _display_path(worktree_path)
    file_block = (
        "\n".join(f"  {f}" for f in files) if files else "  (none reported)"
    )
    header = (
        f"rebase conflict while {phase}.\n\n"
        f"{commit_line}"
        f"Conflicting files:\n{file_block}\n\n"
    )

    only_auto = bool(files) and all(_is_auto_file(f) for f in files)
    if only_auto:
        globs = " ".join(AUTO_COMMIT_GLOBS)
        return header + (
            f"Every conflicting file is an endless-managed auto-file; restoring "
            f"them from {base_branch} is safe. Recover, then retry land:\n"
            f"  git -C {wt} checkout {base_branch} -- {globs}\n"
            f"  endless worktree land <id>\n"
        )

    # A source file conflicts — the cause is genuinely ambiguous. Present the
    # plausible recoveries as candidates the user must judge between.
    return header + (
        f"Likely causes (inspect and choose; the wrong recovery can duplicate "
        f"or lose work):\n\n"
        f"  1. {base_branch} advanced with edits that overlap yours. Resolve "
        f"the conflict in place:\n"
        f"       cd {wt}\n"
        f"       git rebase {base_branch}\n"
        f"       # edit the conflicting files to resolve, then mark resolved:\n"
        f"       git add <files>\n"
        f"       git rebase --continue\n"
        f"     then re-run: endless worktree land <id>\n\n"
        f"  2. the branch re-introduces content already landed for this task "
        f"(e.g. an amended, already-landed commit). Do NOT rebase-continue "
        f"(it duplicates the commit); capture only your delta, reset to "
        f"{base_branch}, re-apply it:\n"
        f"       git -C {wt} diff {base_branch}...HEAD > /tmp/land-delta.patch\n"
        f"       git -C {wt} reset --hard {base_branch}\n"
        f"       git -C {wt} apply /tmp/land-delta.patch\n"
        f"       git -C {wt} commit -am \"<describe your change>\"\n"
        f"     then re-run: endless worktree land <id>\n\n"
        f"Inspect first:\n"
        f"  git -C {wt} log {base_branch}..HEAD\n"
        f"  git -C {wt} diff {base_branch}...HEAD\n"
    )


def _guard_modified_worktree(worktree_path: Path, branch: str, canonical: str) -> None:
    """Refuse land if the worktree's working tree has uncommitted files (E-1416).

    Step 1's partition runs on main; Step 4's rebase runs in the worktree.
    Without this guard, uncommitted files in the worktree make rebase abort
    with git's generic "You have unstaged changes" error — no file list,
    wrong recovery hint.

    Refuses separately for auto-managed modifications (an upstream writer bug
    worth surfacing rather than papering over) and unmanaged user modifications
    (offers worktree-specific recovery options).
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

    Per E-1141: agents adding verbs in worktree sessions accumulate modifications
    in the worktree's verbs file. At land time, two worktrees that independently
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
    otherwise (no modifications, or dedup result equals current committed state).
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

    Path convention `.endless/worktrees/e-NNN` is the canonical
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
    from endless import config
    return config.tilde(p)


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
    """If .endless/plans/E-<id>.md exists but is modified/untracked in main,
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
        f"  git -C {root} show {branch}:{plan_rel} > .endless/tmp/E-{task_id}.md\n"
        f"  endless task update E-{task_id} --text-file .endless/tmp/E-{task_id}.md   # then retry"
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
        f"  endless task update E-{task_id} --text-file <path>{extra}"
    )


def _orphan_no_viable_plan_msg(task_id: int, branch: str) -> str:
    return (
        f"E-{task_id}: no viable plan in tasks.text or on branch {branch}.\n\n"
        f"Add one, then retry:\n"
        f"  endless task update E-{task_id} --text-file <path>"
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

    _bootstrap_task_worktree(task_id, wt_dir, base, branch, project_root)
    return wt_dir, True


def _bootstrap_task_worktree(
    task_id: int,
    wt_dir: Path,
    base: str,
    branch: str | None,
    project_root: Path,
) -> None:
    """Post-`git worktree add` bootstrap shared by claim and session recovery.

    Writes the companion marker (`.endless/worktree.json` + scratch dir),
    materializes the task's doc mirrors, binds the self-dev DB sandbox, then runs
    the project's post-worktree-create hook (go-work-init, bin copy,
    claude-settings-init, ...). It performs NO status transition: `task claim`
    flips the task to `underway`, while `session resume --review`/`--reopen`
    (E-1801) each own their own status side effect (none / per-status), so the
    physical worktree setup had to be decoupled from the status change.

    `branch` is None for a detached `--review` worktree (no working branch).
    """
    companion_dir = wt_dir / ".endless"
    companion_dir.mkdir(parents=True, exist_ok=True)
    # Project-local scratch dir: agents author throwaway content here (gitignored,
    # co-located, recoverable before this worktree drops) instead of system /tmp.
    (companion_dir / "tmp").mkdir(parents=True, exist_ok=True)
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
    _materialize_task_docs(task_id, wt_dir)
    _maybe_auto_sandbox_bind(project_root, wt_dir, task_id)
    _run_post_worktree_create_hook(project_root, wt_dir)


def recreate_dropped_worktree(
    task_id: int,
    title: str,
    project_root: Path,
    base: str,
    *,
    detached: bool,
) -> Path:
    """Recreate a task worktree that was dropped after landing (E-1801).

    Backs `session resume --review`/`--reopen`: a landed task's worktree is
    gone but its transcript survives, so we rebuild the directory at `base` (a
    git ref/sha) to cd into before `claude --resume`.

    detached=True (`--review`): `git worktree add --detach <path> <base>` — a
    read-mostly inspection tree with no working branch. detached=False
    (`--reopen`): a working branch — the original `task/<id>-<slug>` branch is
    reused if it still exists, else a fresh branch is cut off `base`.

    Runs the shared bootstrap but performs NO status transition (the caller
    owns that). Returns the worktree path.
    """
    canonical = f"E-{task_id}"
    slug = _slugify_title(title)
    branch = f"task/{task_id}-{slug}"
    wt_dir = project_root / ".endless" / "worktrees" / f"e-{task_id}"

    if wt_dir.exists():
        # Recovery is only for a dropped worktree. A live dir means the caller
        # mis-detected the drop; treat an already-ours dir as an idempotent
        # no-op, and refuse a foreign collision (mirrors create_task_worktree).
        if _task_id_from_worktree_path(wt_dir) == canonical and _read_companion(wt_dir):
            return wt_dir
        raise click.ClickException(
            f"Path {_tilde(wt_dir)} exists but does not belong to {canonical}. "
            f"Resolve manually before retrying."
        )

    wt_dir.parent.mkdir(parents=True, exist_ok=True)
    # Clear any stale worktree registration whose directory is already gone, so
    # a reused branch isn't reported as "already used by worktree". Touches only
    # bookkeeping for absent dirs, never live work.
    _git_run(["worktree", "prune"], cwd=project_root, check=False)

    if detached:
        add_args = ["worktree", "add", "--detach", str(wt_dir), base]
        companion_branch: str | None = None
    elif _branch_exists(branch, project_root):
        add_args = ["worktree", "add", str(wt_dir), branch]
        companion_branch = branch
    else:
        add_args = ["worktree", "add", "-b", branch, str(wt_dir), base]
        companion_branch = branch

    try:
        _git_run(add_args, cwd=project_root)
    except subprocess.CalledProcessError as e:
        raise click.ClickException(
            f"git worktree add failed for {canonical}:\n{e.stderr or e}"
        )

    _bootstrap_task_worktree(task_id, wt_dir, base, companion_branch, project_root)
    return wt_dir


POST_WORKTREE_CREATE_HOOK = ".endless/hooks/post-worktree-create.sh"

# E-1799: directory (under the existing hooks root) holding per-task post-land
# scripts. Each is `<task>.sh` with the lowercase id, matching the
# `.endless/worktrees/e-NNN` convention. Nested in a subdir so the hooks root
# stays free for project-wide lifecycle hooks.
POST_LAND_HOOK_DIR = ".endless/hooks/post-land"


def _run_post_worktree_create_hook(project_root: Path, worktree_path: Path) -> None:
    """Run the project's post-worktree-create bootstrap hook, if present (E-986).

    Worktree creation can't bake in every project's bootstrap needs (Go go.mod
    replace paths, npm install, venv recreation, Rust target/ cleanup, ...).
    Each project ships its own hook at
    `<project-root>/.endless/hooks/post-worktree-create.sh`; Endless ships no
    default. The runner is generic, like a git hook.

    Discovery: the main checkout's tracked, version-controlled script.
    Invocation: exec'd directly (its own shebang) with cwd = the freshly-created
    worktree and argv[1] = the worktree path. No shell-string interpolation.

    Failure handling is non-fatal, idempotent, and loud: on non-zero exit the
    worktree is KEPT and a loud error names the script, exit code, worktree
    path, and the re-run command. The hook contract REQUIRES idempotency /
    re-runnability — completing a failed bootstrap is just re-running the hook —
    so the runner needs no teardown and a partial worktree is never silently
    usable-but-broken.

    The hook's stdout/stderr stream live so long bootstraps (npm install, go
    build) show progress.
    """
    hook = project_root / POST_WORKTREE_CREATE_HOOK
    if not hook.exists():
        return
    if not os.access(hook, os.X_OK):
        click.echo(
            click.style("⚠ post-worktree-create hook is not executable", fg="yellow")
            + f"\n    {_tilde(hook)}\n"
            f"    Make it executable and re-run:\n"
            f"        chmod +x {_tilde(hook)}\n"
            f"        cd {_tilde(worktree_path)} && {_tilde(hook)} {_tilde(worktree_path)}",
            err=True,
        )
        return
    click.echo(
        click.style("•", fg="cyan")
        + f" running post-worktree-create hook: {_tilde(hook)}"
    )
    try:
        result = subprocess.run(
            [str(hook), str(worktree_path)], cwd=str(worktree_path),
        )
    except OSError as e:
        click.echo(
            click.style("⚠ post-worktree-create hook failed to start", fg="yellow")
            + f"\n    {_tilde(hook)}: {e}\n"
            f"    Worktree kept. Re-run after fixing:\n"
            f"        cd {_tilde(worktree_path)} && {_tilde(hook)} {_tilde(worktree_path)}",
            err=True,
        )
        return
    if result.returncode != 0:
        click.echo(
            click.style(
                f"⚠ post-worktree-create hook exited {result.returncode}", fg="yellow"
            )
            + f"\n    script:   {_tilde(hook)}\n"
            f"    worktree: {_tilde(worktree_path)}\n"
            f"    The worktree was KEPT. The hook must be idempotent/re-runnable;\n"
            f"    finish bootstrap by re-running it:\n"
            f"        cd {_tilde(worktree_path)} && {_tilde(hook)} {_tilde(worktree_path)}",
            err=True,
        )


def _run_post_land_script(
    worktree_path: Path,
    main_root: Path,
    canonical: str,
    merge_sha: str,
    base_branch: str,
) -> None:
    """Run the task's committed post-land script after the merge (E-1799).

    The *solution* half of self-completing land: a task whose change needs a
    one-time action on main after it lands — most often removing the untracked
    files a newly-un-ignored path leaves behind (a commit only moves tracked
    content, so no merge can delete them), or a fixup git won't perform on merge
    — commits an idempotent `.endless/hooks/post-land/e-<task>.sh` on its branch.
    It lands into main with the task; this runner execs it once, right after the
    ff-merge advanced main, and nothing ever re-execs it.

    Discovery: after the ff-merge the script is in main's tree at
    `<main_root>/.endless/hooks/post-land/<task>.sh` (lowercase id). Absent →
    silent no-op. Present but not executable → loud warning (path + chmod + the
    re-run command), then skip.

    Invocation mirrors the create hook: exec'd directly via its own shebang (no
    shell-string interpolation), with cwd = the main checkout (the working tree
    it reconciles) and argv[1] = the main root. It inherits the land process's
    env plus ENDLESS_TASK_ID / ENDLESS_MERGE_SHA / ENDLESS_WORKTREE_PATH /
    ENDLESS_BASE_BRANCH. Output streams live.

    Failure is non-fatal and loud (forced: Step-5's ff-merge already advanced
    main, so the land HAS happened and cannot be unwound). On non-zero exit a
    loud error names the script, exit code, cwd, and the exact re-run command;
    the land still succeeds. The contract REQUIRES the script be idempotent /
    re-runnable, so recovery is just re-running it.
    """
    script = main_root / POST_LAND_HOOK_DIR / f"{canonical.lower()}.sh"
    if not script.exists():
        return
    rerun = f"cd {_tilde(main_root)} && {_tilde(script)} {_tilde(main_root)}"
    if not os.access(script, os.X_OK):
        click.echo(
            click.style("⚠ post-land script is not executable", fg="yellow")
            + f"\n    {_tilde(script)}\n"
            f"    The land succeeded, but this step was skipped.\n"
            f"    Make it executable and re-run:\n"
            f"        chmod +x {_tilde(script)}\n"
            f"        {rerun}",
            err=True,
        )
        return
    click.echo(
        click.style("•", fg="cyan")
        + f" running post-land script: {_tilde(script)}"
    )
    env = {
        **os.environ,
        "ENDLESS_TASK_ID": canonical,
        "ENDLESS_MERGE_SHA": merge_sha,
        "ENDLESS_WORKTREE_PATH": str(worktree_path),
        "ENDLESS_BASE_BRANCH": base_branch,
    }
    try:
        result = subprocess.run(
            [str(script), str(main_root)], cwd=str(main_root), env=env,
        )
    except OSError as e:
        click.echo(
            click.style("⚠ post-land script failed to start", fg="yellow")
            + f"\n    {_tilde(script)}: {e}\n"
            f"    Main already advanced; the land succeeded. Re-run after fixing:\n"
            f"        {rerun}",
            err=True,
        )
        return
    if result.returncode != 0:
        click.echo(
            click.style(
                f"⚠ post-land script exited {result.returncode}", fg="yellow"
            )
            + f"\n    script: {_tilde(script)}\n"
            f"    cwd:    {_tilde(main_root)}\n"
            f"    The land SUCCEEDED (main was already advanced); this step did not.\n"
            f"    The script must be idempotent/re-runnable; finish by re-running it:\n"
            f"        {rerun}",
            err=True,
        )


def _ignored_present_files(repo_root: Path) -> set[str]:
    """Files ignored-and-present in repo_root under the CURRENT ignore rules.

    `git ls-files --others --ignored --exclude-standard` lists untracked files
    that git's own ignore machinery (.gitignore, .git/info/exclude,
    core.excludesFile) currently ignores AND that exist on disk. Captured on
    main *before* a land, this snapshots exactly the set a land might un-ignore
    (E-1800). Compares git's own classification — never parses `.gitignore`.
    """
    out = subprocess.run(
        ["git", "ls-files", "--others", "--ignored", "--exclude-standard", "-z"],
        capture_output=True, text=True, check=True, cwd=str(repo_root),
    ).stdout
    return {p for p in out.split("\0") if p}


def _untracked_present_files(repo_root: Path) -> set[str]:
    """Untracked, not-ignored, present files in repo_root under CURRENT rules.

    `git ls-files --others --exclude-standard` lists untracked files git does
    NOT ignore. Captured on main *after* a land (post-merge, post-script), this
    is the set from which residue under a newly-un-ignored path is drawn
    (E-1800).
    """
    out = subprocess.run(
        ["git", "ls-files", "--others", "--exclude-standard", "-z"],
        capture_output=True, text=True, check=True, cwd=str(repo_root),
    ).stdout
    return {p for p in out.split("\0") if p}


def _check_post_land_residue(
    main_root: Path,
    canonical: str,
    ignored_before: set[str],
) -> None:
    """Verify the land left no untracked residue under paths it un-ignored (E-1800).

    Removing a `.gitignore` entry leaves the formerly-ignored files on disk as
    *untracked* — a commit only moves tracked content, so no land deletes them.
    E-1799's post-land script is meant to clean them, but a script can be absent
    or miss some. This step tests the *outcome* instead of trusting a script
    exists: residue that remains is what a *future* land could silently sweep
    into a commit.

    Residue = `ignored_before` ∩ `_untracked_present_files(main_root)`, comparing
    git's own classifications (never reading `.gitignore`):
      - in both  → ignored-and-present before, untracked-and-present after → this
        land un-ignored it and nothing removed it.
      - tracked content drops out (not untracked); script-removed files drop out
        (not present); paths this land did not un-ignore drop out (still ignored
        → absent from the post-land untracked set).

    Empty residue → silent success (also the common no-op: a land that
    un-ignored nothing yields an empty intersection). Non-empty → a loud,
    actionable error, **non-fatal to the merge** (Step 5 already advanced main
    and cannot be unwound) but raised so land **exits non-zero**, mirroring
    _record_landing's "main advanced, follow-up step failed" surfacing so
    automation notices.
    """
    untracked_after = _untracked_present_files(main_root)
    residue = sorted(ignored_before & untracked_after)
    if not residue:
        return

    script = main_root / POST_LAND_HOOK_DIR / f"{canonical.lower()}.sh"
    listing = "\n  ".join(residue)
    if script.exists():
        script_note = (
            f"    The post-land script ran but left these behind:\n"
            f"        {_tilde(script)}\n"
            f"    Extend it to remove them (or remove the files) so the next land is clean."
        )
    else:
        script_note = (
            f"    No post-land script was shipped for {canonical}. Add one to remove them:\n"
            f"        {_tilde(script)}\n"
            f"    (or remove the files manually)."
        )
    noun = "path" if len(residue) == 1 else "paths"
    raise click.ClickException(
        f"Landed {canonical}: main was advanced, but the land un-ignored "
        f"{len(residue)} {noun} left as untracked residue on main:\n\n"
        f"  {listing}\n\n"
        f"{script_note}\n\n"
        f"    A later land could sweep this residue into a commit. The land "
        f"itself SUCCEEDED and cannot be unwound; this check is non-fatal but "
        f"exits non-zero so automation notices."
    )


# E-1747: the multiline document fields that mirror to committed
# .endless/<subdir>/E-NNN.md files. Each tuple is (tasks column, subdir,
# human label used in the commit subject and progress line). `text` is the
# original plan mirror (E-1445); `outcome`/`analysis` are added here. Short
# metadata (description, title) is deliberately excluded — not documents.
_TASK_DOC_FIELDS: tuple[tuple[str, str, str], ...] = (
    ("text", "plans", "plan"),
    ("outcome", "outcomes", "outcome"),
    ("analysis", "analyses", "analysis"),
)


def _materialize_task_docs(task_id: int, worktree_path: Path) -> None:
    """Seed every version-controlled task-doc mirror at worktree birth (E-1747).

    Extends the original plan-only materialization (E-1445) to the full set of
    multiline document fields (`_TASK_DOC_FIELDS`), so a freshly born worktree
    carries git-backed copies of the task's plan, outcome, and analysis — not
    just the plan. Each field is independent: a missing one warns and skips
    without aborting worktree creation.
    """
    for field, subdir, label in _TASK_DOC_FIELDS:
        _materialize_task_doc(task_id, worktree_path, field, subdir, label)


def _materialize_task_doc(
    task_id: int, worktree_path: Path, field: str, subdir: str, label: str,
) -> None:
    """Write <worktree>/.endless/<subdir>/E-NNN.md from tasks.<field> and commit.

    The single point where a task-doc mirror is created on disk at worktree
    birth. `task update` no longer provisions a worktree; the mirror
    materializes here when the worktree is born (at claim/spawn).

    Reads the field via the `endless-go session-query task-field` Go helper —
    Python DB reads are forbidden (E-894). Empty/absent content writes
    nothing. Failures warn and skip rather than abort worktree creation; a
    missing mirror is recoverable by re-running the write once the worktree
    exists (which mirrors into it).
    """
    from endless import config

    binary = shutil.which("endless-go")
    if not binary:
        click.echo(
            f"  warning: endless-go not found on PATH; {label} file "
            "not materialized.",
            err=True,
        )
        return
    try:
        # E-1429: thread the resolved --db context (no-op outside a gated
        # worktree) so this DB read isn't refused when claim runs from a
        # worktree cwd.
        result = subprocess.run(
            [binary, *config.go_db_context_args(), "session-query",
             "task-field", "--id", str(task_id), "--name", field],
            capture_output=True, text=True,
        )
    except OSError as e:
        click.echo(
            f"  warning: endless-go session-query task-field {field}: {e}",
            err=True,
        )
        return
    if result.returncode != 0:
        click.echo(
            f"  warning: could not read {label} for E-{task_id}: "
            f"{(result.stderr or '').strip()}",
            err=True,
        )
        return
    if not result.stdout.strip():
        return
    docs_dir = worktree_path / ".endless" / subdir
    docs_dir.mkdir(parents=True, exist_ok=True)
    target = docs_dir / f"E-{task_id}.md"
    target.write_text(result.stdout)
    click.echo(
        click.style("✓", fg="green")
        + f" Materialized {label} to {_tilde(target)}"
    )
    _commit_doc_in_worktree(
        worktree_path, f".endless/{subdir}/E-{task_id}.md",
        f"Endless: add {label} for E-{task_id}",
    )


def _materialize_plan_file(task_id: int, worktree_path: Path) -> None:
    """Back-compat alias: materialize just the plan (text) mirror.

    Prefer `_materialize_task_docs`, which seeds every mirrored field (E-1747).
    Retained because existing callers/tests reference this name.
    """
    _materialize_task_doc(task_id, worktree_path, "text", "plans", "plan")


def _commit_doc_in_worktree(
    worktree_path: Path, rel_path: str, subject: str,
) -> None:
    """Stage and commit one .endless/<subdir>/E-NNN.md mirror on the worktree branch (E-1525/E-1747).

    Called at both mirror write sites — claim/spawn materialization and the
    `task update` write-time mirror — so the file rides to main on `worktree
    land` instead of sitting untracked and getting rejected by the modified-
    worktree guard.

    `commit -o <rel_path>` scopes the commit to just this file even if the
    worktree has unrelated modifications (user mid-edit, other auto-managed
    files).
    Returns silently when the file already matches HEAD — re-running a write
    with identical content is a no-op.
    """
    status = _git_run(
        ["status", "--porcelain", "--", rel_path],
        cwd=worktree_path,
    ).stdout
    if not status.strip():
        return
    try:
        _git_run(["add", "--", rel_path], cwd=worktree_path)
        _git_run(
            ["commit", "-o", rel_path, "-m", subject], cwd=worktree_path,
        )
    except subprocess.CalledProcessError as e:
        detail = (e.stderr or e.stdout or str(e)).strip()
        raise click.ClickException(
            f"Failed to commit {rel_path} in worktree: {detail}"
        )


def _commit_plan_file_in_worktree(
    worktree_path: Path, task_id: int, subject: str,
) -> None:
    """Back-compat alias for committing the plan mirror. Prefer
    `_commit_doc_in_worktree` for arbitrary doc mirrors (E-1747)."""
    _commit_doc_in_worktree(
        worktree_path, f".endless/plans/E-{task_id}.md", subject,
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
    name = worktree_path.name
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


def _resolve_land_endless_go(worktree_path: Path, project_root: Path) -> str | None:
    """The endless-go binary a land's task.landed emit must use, or None.

    For a self_dev project (E-1664), a land applies this branch's schema change
    before recording the landing, so the only binary whose embedded schema/enums
    match the just-written rows is the worktree's own build — never the
    not-yet-refreshed global. Binary selection here is therefore an INVARIANT of
    the land, not a choice: return <worktree>/bin/endless-go. If that build is
    absent, fail loudly (an unbuilt self_dev worktree is a bug to surface, not
    mask by sliding to the stale global — E-1662); a missing build silently
    standing in is the exact skew that produced the original failure.

    Returns None for a non-self_dev project, where the global is correct and no
    worktree binary exists (downstream users never build endless-go).
    """
    from endless import config

    if not config.project_is_self_dev(project_root):
        return None
    wt_bin = worktree_path / "bin" / "endless-go"
    if not wt_bin.is_file() or not os.access(wt_bin, os.X_OK):
        raise click.ClickException(
            f"The self-dev worktree's endless-go binary is missing or not "
            f"executable:\n\n    {_display_path(wt_bin)}\n\n"
            f"Build it before landing: run `just build` in the worktree."
        )
    return str(wt_bin)


def _rebuild_worktree_binary(worktree_path: Path, canonical: str) -> None:
    """Rebuild the worktree's endless-go AFTER the rebase onto base (E-1941).

    This is the whole staleness fix, and it replaces the behind-base REFUSAL that
    shipped first. That refusal asked a proxy question — "is the source behind
    base?" — as a stand-in for "is the binary stale?". The proxy was wrong three
    times over (it counted ledger auto-commits, then Python/justfile/test drift,
    then hardcoded directories), and even once tuned it was structurally
    unworkable: main takes a Go commit every few hours, so every worktree in the
    repo goes "behind" continuously and every land is refused until its branch is
    hand-rebased. The remedy it printed was that hand-rebase — the one operation
    that risks the E-1943 ledger conflict. Cost exceeded benefit.

    Answer the real question instead. Step 4 has just rebased this branch onto
    base, so the worktree's SOURCE is current by definition. Rebuilding here makes
    the BINARY current by construction, and there is nothing left to check: the
    binary that Step 5.5 and Step 6 point at the real DB is built from exactly
    main + this branch.

    Note this adds no rebase. Step 4's rebase is pre-existing; the plan's
    "refuse, do not auto-rebase" ruled out the JUSTFILE rebasing ahead of the
    land, which is still not done.

    Placed before Step 5 so a broken build aborts while main and the DB are
    untouched — strictly safer than the pre-rebase `just go` in the Justfile,
    which could only ever build stale source. Runs `just go` rather than an
    inlined `go build` so the build command has one definition.

    self_dev only: downstream users never build endless-go.
    """
    from endless import config
    if not config.project_is_self_dev(worktree_path):
        return
    if shutil.which("just") is None:
        raise click.ClickException(
            f"cannot land {canonical}: `just` is not on PATH, so the worktree's "
            f"endless-go cannot be rebuilt after the rebase onto base. That "
            f"rebuild is what guarantees the binary about to touch the real "
            f"database matches the code being landed."
        )
    result = subprocess.run(
        ["just", "go"], cwd=str(worktree_path), capture_output=True, text=True,
    )
    if result.returncode != 0:
        raise click.ClickException(
            f"cannot land {canonical}: rebuilding the worktree's endless-go "
            f"after the rebase onto base failed.\n\n"
            f"{(result.stderr or result.stdout).strip()}\n\n"
            f"Nothing has been merged or migrated — base and the database are "
            f"untouched. Fix the build and retry."
        )
    click.echo(
        click.style("•", fg="cyan")
        + " Rebuilt worktree endless-go from the rebased source"
    )


def _branch_schema_changes(worktree_path: Path, base_branch: str) -> list[str]:
    """Repo-relative schema-change files this branch ADDS since base (E-1941).

    Must be called while `base_branch` and the branch still differ — i.e. BEFORE
    the ff-merge. Afterwards they are the same commit, the three-dot diff is
    empty, and every change would be silently skipped.

    The runner/ package is excluded: library code, not a change script.
    """
    out = _git_run(
        [
            "diff", f"{base_branch}...HEAD", "--diff-filter=A", "--name-only",
            "--", "internal/schema/changes/",
            ":(exclude)internal/schema/changes/runner/",
        ],
        cwd=worktree_path,
    )
    return [
        ln.strip() for ln in out.stdout.splitlines()
        if ln.strip().endswith((".sql", ".go"))
    ]


def _apply_branch_schema_changes(
    rel_paths: list[str],
    worktree_path: Path,
    canonical: str,
    base_branch: str,
    endless_go_bin: str | None,
) -> None:
    """Back up, then apply this branch's schema changes — AFTER the ff-merge.

    Ordering is the whole point of E-1941. Applying BEFORE the merge meant a
    merge failure left the real DB migrated to a schema no installed binary
    understood: unrecoverable without a restore, and on 2026-08-10 it froze
    session tracking machine-wide. Applying AFTER inverts that asymmetry — main
    has the code and the DB merely lags, which `endless db apply-change` fixes on
    a re-run (it is idempotent, gated by _schema_version).

    It cannot move later still: `_record_landing` runs this same binary against
    the real DB, and for a branch adding a mirrored-enum value that binary
    carries a constant the DB lacks until these changes land — E-1664's failure
    inverted. Between the ff-merge and the record is the only correct place.

    The backup is retained from the Justfile original: a change set can be
    several files, so one can apply and the next fail, leaving a partial
    migration no re-run heals — and there is still no `endless db restore`
    (E-1942).
    """
    from endless.event_bridge import apply_change, backup_db

    def _post_merge_failure(what: str, detail: str) -> click.ClickException:
        return click.ClickException(
            f"Landed {canonical} into {base_branch}: main was advanced, but "
            f"{what} failed:\n\n{detail}\n\n"
            f"The code is on {base_branch}; the database has not been migrated "
            f"yet. Nothing is lost and no restore is needed — resolve the cause "
            f"above and re-run `just land {canonical}`. The ff-merge is "
            f"idempotent and each schema change is gated by _schema_version, so "
            f"the retry applies only what is still outstanding."
        )

    click.echo(
        click.style("•", fg="cyan") + " Backing up DB before applying schema changes"
    )
    try:
        backup_db(endless_go_bin=endless_go_bin)
    except Exception as e:
        detail = e.message if isinstance(e, click.ClickException) else str(e)
        raise _post_merge_failure("the pre-apply database backup", detail)

    for rel in rel_paths:
        click.echo(click.style("•", fg="cyan") + f" Applying schema change: {rel}")
        try:
            apply_change(str(worktree_path / rel), endless_go_bin=endless_go_bin)
        except Exception as e:
            detail = e.message if isinstance(e, click.ClickException) else str(e)
            raise _post_merge_failure(f"applying schema change {rel}", detail)


def _record_only_landing(
    canonical: str,
    sha: str | None,
    branch: str | None,
    at: str | None,
    dry_run: bool,
) -> None:
    """Record a landing that already happened, without touching git (E-1719).

    This is the record-only slice of `worktree land`: it skips the whole
    rebase/ff-merge/discovery/session-resolution machinery and just emits a
    `task.landed` for a known (task, merge_commit_sha) pair. It exists to
    backfill historical landings that predate reliable landing-recording, whose
    worktree and branch are long gone.

    The emitted event is attributed to the system actor with no session
    (`session_id` NULL), records `branch` NULL when none is given (the original
    branch is unrecoverable), and stamps `landed_at` at the merge commit's date
    — derived here from `git show -s --format=%cI <sha>` unless `at` is passed —
    so the row reflects when the work actually landed, not now().
    """
    if not sha:
        raise click.ClickException("--record-only requires --sha <merge-commit-sha>.")

    main_root = _project_root()
    _, proj_name = _resolve_project(None)
    item_id = int(canonical[2:])

    landed_at = at
    if not landed_at:
        try:
            landed_at = _git(["show", "-s", "--format=%cI", sha], cwd=main_root)
        except subprocess.CalledProcessError as e:
            raise click.ClickException(
                f"Cannot read the commit date for {sha} in "
                f"{_project_root()}: {(e.stderr or e).strip() if hasattr(e, 'stderr') else e}\n\n"
                f"Confirm the SHA exists on this checkout, or pass --at <RFC3339>."
            )
    if not landed_at:
        raise click.ClickException(
            f"commit {sha} produced no date; pass --at <RFC3339> explicitly."
        )

    if dry_run:
        click.echo(f"Would record-only land: {canonical}")
        click.echo(f"  Project:   {proj_name}")
        click.echo(f"  SHA:       {sha}")
        click.echo(f"  Branch:    {branch or '(none — records NULL)'}")
        click.echo(f"  Landed at: {landed_at}")
        return

    from endless.event_bridge import emit_event

    emit_event(
        kind="task.landed",
        project=proj_name,
        entity_type="task",
        entity_id=str(item_id),
        payload={"branch": branch or "", "merge_commit_sha": sha},
        actor_kind="system",
        actor_id="backfill",
        session_id=None,
        ts=landed_at,
    )
    click.echo(
        click.style("•", fg="green")
        + f" Recorded landing for {canonical} at {sha[:8]} ({landed_at})"
    )


def _record_landing(
    item_id: int,
    proj_name: str,
    branch: str,
    base_branch: str,
    canonical: str,
    merge_sha: str,
    endless_go_bin: str | None = None,
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
            endless_go_bin=endless_go_bin,
        )
    except Exception as e:
        detail = e.message if isinstance(e, click.ClickException) else str(e)
        raise click.ClickException(
            f"Landed {canonical} ({branch}) into {base_branch}: main was "
            f"advanced, but recording the landing failed:\n\n{detail}\n\n"
            f"The ff-merge is idempotent. Re-run `just land {canonical}` to "
            f"record the landing once the cause above is resolved."
        )


def land_worktree(
    task_id: str,
    dry_run: bool,
    record_only: bool = False,
    sha: str | None = None,
    branch: str | None = None,
    at: str | None = None,
) -> None:
    """Land the worktree for <task-id> into main per E-987 + E-1337.

    With record_only (E-1719), skips all git work and just emits `task.landed`
    for the given (task, --sha) pair — used to backfill historical landings
    whose worktree is gone. See _record_only_landing.

    Loop:
      1. Partition git status into auto-commit vs user-work.
      2. If user-work is non-empty: refuse with actionable message.
      3. If auto-commit is non-empty: 'git add' and commit them as
         'Endless: auto-record session activity'.
      4. Rebase the worktree branch onto main (in the worktree).
      4.2 Rebuild the worktree's endless-go from the now-current source,
         self_dev only (E-1941), so the binary Steps 5.5/6 point at the real DB
         provably matches what is being landed.
      4.5 List the schema changes this branch adds (while main and the branch
         still differ — after Step 5 the diff is empty).
      5. ff-merge from main.
      5.5 Apply those schema changes, self_dev only (E-1941). AFTER the merge,
         so a failure leaves the DB lagging landed code (a re-run fixes it)
         rather than migrated ahead of code that never landed.
      6. Emit task.landed event. Worktree dir and branch stay; a
         separate reaper sweep removes them after worktree_ttl.

    Step 4.2 is why there is no behind-base refusal: rather than gating on a
    proxy for staleness, the land removes the staleness (E-1941).

    Retry up to LAND_MAX_RETRIES if a concurrent writer dirties auto-files
    between auto-commit and merge attempt. Re-landing after a follow-up
    commit is supported: each successful land appends a new row to
    task_landings; the dir and branch are reused.
    """
    # Landing is an always-main operation: the landed task lives only in the
    # real DB, schema changes belong to it, and the merge is into main. When run
    # from a self-dev session whose XDG points at the worktree sandbox and no
    # explicit --db was given, pin main so the task.landed emit (and every
    # downstream endless-go shellout) targets the real DB rather than the
    # sandbox (which lacks the task row, failing the task_landings FK). E-1628.
    from endless import config
    config.default_db_to_main()

    canonical = _normalize_task_id(task_id)

    # E-1719: the record-only path records a historical landing that already
    # happened; there is no live worktree/branch to rebase or ff-merge, so it
    # dispatches before all of that.
    if record_only:
        _record_only_landing(canonical, sha, branch, at, dry_run)
        return

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

    # Resolve the binary the record-landing emit must use BEFORE the ff-merge,
    # so a self_dev worktree that isn't built fails loudly here rather than
    # after main has advanced (E-1664). None for non-self_dev (global is used).
    endless_go_bin = _resolve_land_endless_go(worktree_path, main_root)

    # E-1800: snapshot the ignored-and-present files on main under the CURRENT
    # (pre-land) ignore rules — pure data, captured once before any merge, not a
    # gate. After the land + post-land script, any of these still present as
    # untracked (now un-ignored) is residue. Stable across the retry loop (the
    # retries only re-attempt the ff-merge; they don't change main's ignore
    # rules), so it's captured here rather than per attempt.
    try:
        ignored_before = _ignored_present_files(main_root)
    except subprocess.CalledProcessError as e:
        raise click.ClickException(
            f"git ls-files (pre-land ignored snapshot) failed: {e.stderr or e}"
        )

    last_error = None
    for attempt in range(1, LAND_MAX_RETRIES + 1):
        # Step 1: partition main's working-tree modifications.
        try:
            auto_files, user_files = _git_status_partition(main_root)
        except subprocess.CalledProcessError as e:
            raise click.ClickException(f"git status failed: {e.stderr or e}")

        # Step 2: refuse if user-work modified.
        if user_files:
            file_list = "\n  ".join(user_files[:20])
            more = "" if len(user_files) <= 20 else f"\n  ... and {len(user_files) - 20} more"
            raise click.ClickException(
                f"main has uncommitted user changes; cannot land {canonical}.\n\n"
                f"Files:\n  {file_list}{more}\n\n"
                f"Resolve them: commit (in a worktree), move to a worktree, or set them aside, then retry."
            )

        # Step 3: auto-commit endless-managed modifications, if any.
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

        # Step 3.5: dedup the worktree's verbs.jsonl against main's, committing
        # the bundled result on the worktree's branch (E-1141 / E-1138).
        try:
            _dedup_worktree_verbs_against_main(worktree_path, main_root)
        except subprocess.CalledProcessError as e:
            raise click.ClickException(
                f"verbs.jsonl dedup on worktree failed: {e.stderr or e}"
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
        except subprocess.CalledProcessError:
            # The orphan drop and the replay of the user's commits share one
            # rebase; a conflict here is the replay conflicting, not the drop.
            # Read the conflict state BEFORE aborting, then abort.
            msg = _rebase_conflict_message(
                worktree_path, base_branch,
                phase="replaying your commits after dropping base auto-amend commits",
            )
            _git_run(["rebase", "--abort"], cwd=worktree_path, check=False)
            raise click.ClickException(msg)
        if n_orphans:
            noun = "commit" if n_orphans == 1 else "commits"
            click.echo(
                click.style("•", fg="yellow")
                + f" Dropped {n_orphans} orphan auto-amend {noun} ({first_subj})"
            )

        # Step 3.75 (backstop to the ledger routing policy): refuse if any
        # commit surviving Step 3.7 still touches the DB ledger. Step 3.7
        # already dropped the legitimately-orphaned base ledger commits;
        # anything still under DB_LEDGER_DIR is a genuine branch-side ledger
        # commit that would be rebased into main and corrupt shared history.
        offenders = _ledger_touching_commits(worktree_path, base_branch)
        if offenders:
            noun = "commit" if len(offenders) == 1 else "commits"
            listing = "\n".join(
                f"  {sha[:12]}  {subject}" for sha, subject in offenders
            )
            raise click.ClickException(
                f"cannot land {canonical}: the branch has {len(offenders)} "
                f"{noun} modifying the database ledger ({DB_LEDGER_DIR}/):\n\n"
                f"{listing}\n\n"
                f"Ledger entries are recorded on the main checkout, never on a "
                f"task branch — landing these would rebase a branch-authored "
                f"ledger segment into main and corrupt the shared database "
                f"history. Remove these commits from the branch before retrying "
                f"(inspect each with `git show <sha>`)."
            )

        # Step 3.8 (E-1416): guard against modified worktree tree before rebase.
        _guard_modified_worktree(worktree_path, branch, canonical)

        # Step 4: rebase the worktree branch onto main.
        try:
            _git_run(["rebase", base_branch], cwd=worktree_path)
        except subprocess.CalledProcessError:
            # Read the conflict state (files + failing commit) BEFORE aborting,
            # then abort. The message reports facts confidently and offers
            # recoveries as candidates rather than misattributing every conflict
            # to an auto-file.
            msg = _rebase_conflict_message(
                worktree_path, base_branch,
                phase=f"rebasing your branch onto {base_branch}",
            )
            _git_run(["rebase", "--abort"], cwd=worktree_path, check=False)
            raise click.ClickException(msg)

        # Step 4.2 (E-1941): the branch is now rebased onto base, so the
        # worktree's source is current — rebuild endless-go from it. This is what
        # makes the binary that Steps 5.5 and 6 point at the real DB provably
        # match the code being landed, and it replaces the behind-base refusal
        # that shipped first (see _rebuild_worktree_binary). Before Step 5, so a
        # broken build aborts with base and the DB untouched.
        _rebuild_worktree_binary(worktree_path, canonical)

        # Step 4.5 (E-1941): list this branch's schema changes while base and the
        # branch are still different commits. After Step 5 they are the same
        # commit and the three-dot diff is empty, so computing it later would
        # silently skip every change. Listing is read-only — the DB is not
        # touched until Step 5.5, after main has actually advanced.
        try:
            schema_changes = _branch_schema_changes(worktree_path, base_branch)
        except subprocess.CalledProcessError as e:
            raise click.ClickException(
                f"listing schema changes on the branch failed: {e.stderr or e}"
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

        # Step 5.5 (E-1941): apply this branch's schema changes now that main
        # HAS advanced. Before the merge this was the irreversible case (DB
        # migrated, code not landed, no installed binary able to read it);
        # after it, a failure merely leaves the DB lagging code that is already
        # on main, which a re-run fixes. Must precede Step 6, which runs this
        # same binary against the real DB and needs the rows these changes
        # write (E-1664 inverted).
        # self_dev only: `internal/schema/changes/` is endless's OWN schema, so a
        # downstream branch has no business migrating the user's DB even if a
        # path happened to match.
        if schema_changes and config.project_is_self_dev(main_root):
            _apply_branch_schema_changes(
                schema_changes, worktree_path, canonical, base_branch,
                endless_go_bin,
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
            item_id, proj_name, branch, base_branch, canonical, merge_sha,
            endless_go_bin=endless_go_bin,
        )

        click.echo(
            click.style("•", fg="green")
            + f" Landed {canonical} ({branch}) into {base_branch}"
        )

        # E-1799: run the task's committed post-land script, if any, now that
        # the ff-merge has advanced main. Non-fatal + loud; never unwinds the
        # land. After _record_landing and the Landed echo, before the
        # best-effort reap sweep; not reached by the record_only early return.
        _run_post_land_script(
            worktree_path, main_root, canonical, merge_sha, base_branch,
        )

        # E-1800: verify the land left no untracked residue under paths it
        # newly un-ignored — tests the outcome rather than trusting a post-land
        # script exists. Runs after the script (so a script's cleanup is
        # credited) and before the best-effort reap. Non-fatal to the merge
        # (already advanced) but raises to exit non-zero if residue remains.
        _check_post_land_residue(main_root, canonical, ignored_before)

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
    """Remove a worktree explicitly. Refuses modified/unlanded/foreign without --force."""
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

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
import tempfile
from datetime import datetime, timezone
from pathlib import Path

import click

from endless import land_conflict, rowcap
from endless.task_cmd import _display_path, _resolve_project, recover_task_text
from endless.project_path import resolved


COMPANION_FILENAME = ".endless/worktree.json"
LOCK_FILENAME = ".endless/worktree.lock"

# Auto-committed file globs per E-987 (locked), modified by E-1141: verbs.jsonl
# is in (ambient agent-driven churn); config.json is out (deliberate
# human/agent edits whose attribution the user controls).
# Land treats these as endless-managed: modified state in any of these does
# not block land; instead, land auto-commits them as a separate commit
# before the worktree's commits.
#
# .endless/LESSONS.md was here between E-2051 and E-2055 and is deliberately
# NOT any more. `endless lesson write` commits the corrections log on the main
# checkout at write time (lesson_cmd.py), so land has nothing left to sweep and
# a modified LESSONS.md inside a WORKTREE is what it now looks like: ordinary
# user work, to be committed on the task branch like any other edit.
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
# to count as a viable plan. Empirically derived from the existing tasks: every
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
    return resolved(row[0]["path"])


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

def list_worktrees(state_filter: str | None, as_json: bool,
                   limit: int | None = None, no_limit: bool = False) -> None:
    """List worktrees for the current project."""
    cap = rowcap.resolve_cap(limit, no_limit, machine=as_json)
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

    rows, hidden = rowcap.cap_rows(rows, cap)

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

    rowcap.echo_footer(hidden)


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


def _git_state_anomaly(path: Path) -> str:
    """Name what is going on in a worktree that is not simply sitting on a branch.

    A rebase run into a detached HEAD or a half-finished merge/cherry-pick does
    not just fail — `git rebase --abort` in that state can disturb the operation
    already in flight. The sweep does not go near one. Empty string means the
    worktree is on a branch with nothing in progress.
    """
    if _git_run(["symbolic-ref", "-q", "HEAD"], cwd=path, check=False).returncode != 0:
        return "detached HEAD"
    gitdir = _git_run(["rev-parse", "--git-dir"], cwd=path, check=False).stdout.strip()
    if not gitdir:
        return "not a git worktree"
    root = Path(gitdir) if Path(gitdir).is_absolute() else path / gitdir
    for marker, label in (
        ("rebase-merge", "a rebase"), ("rebase-apply", "a rebase"),
        ("MERGE_HEAD", "a merge"), ("CHERRY_PICK_HEAD", "a cherry-pick"),
        ("REVERT_HEAD", "a revert"), ("BISECT_LOG", "a bisect"),
    ):
        if (root / marker).exists():
            return f"{label} is in progress"
    return ""


def _sync_state(path: Path, base: str, here: Path | None) -> tuple[str, str]:
    """Classify one worktree for a sync sweep: (disposition, reason).

    Disposition is "rebase", "skip" or "error". The reason is shown verbatim,
    so it says what is true of THIS worktree rather than naming a rule the
    reader then has to apply.
    """
    if here is not None and path.resolve() == here.resolve():
        return "skip", "you are in it"
    # _git_status_partition sorts for `land`, which treats the DB ledger as user
    # work; for a sweep the ledger is endless-managed like the rest, so re-sort
    # with the broader rule. The distinction only changes what the skip SAYS —
    # both are skipped — and saying "its session is mid-flight" about a plan
    # mirror would be false.
    auto_raw, user_raw = _git_status_partition(path)
    auto = auto_raw + [f for f in user_raw if _is_auto_file(f)]
    user = [f for f in user_raw if not _is_auto_file(f)]
    if user:
        return "skip", f"{len(user)} uncommitted file(s), e.g. {user[0]}"
    if auto:
        return "skip", f"{len(auto)} uncommitted endless-managed file(s)"
    anomaly = _git_state_anomaly(path)
    if anomaly:
        return "skip", anomaly

    # Liveness LAST among the skips, because it costs a subprocess per worktree
    # and the cheap checks above have already removed most candidates.
    #
    # It is the check this sweep most needs and least obviously needs. A clean
    # worktree is not an idle one: a session that has just committed is clean
    # and about to keep working, and rebasing under it changes every file
    # beneath a process that has already READ them. An agent that then edits
    # from what it read silently reverts whatever arrived in the rebase — which
    # is the failure this project exists to prevent, arriving by our own hand.
    verdict, detail = _worktree_in_use_probe(path)
    if verdict == "in-use":
        return "skip", detail
    if verdict != "free":
        # Fail closed, as `drop` does: a sweep that cannot tell whether someone
        # is standing here does not rebase on the assumption that nobody is.
        return "skip", f"cannot tell whether it is in use ({detail})"

    res = _git_run(["merge-base", "--is-ancestor", base, "HEAD"], cwd=path, check=False)
    if res.returncode == 0:
        return "skip", f"already on {base}"
    if res.returncode != 1:
        return "error", (res.stderr.strip() or "could not compare against " + base)
    return "rebase", f"behind {base}"


def sync_worktrees(apply: bool) -> None:
    """Rebase this project's task worktrees onto the default branch.

    A worktree branched before a change landed does not have that change, and
    keeps not having it for as long as nobody rebases: a fix to a shared file
    reaches `main` and reaches nothing else. That is not an Endless-specific
    condition — it is what worktrees do — but it is invisible until something
    depends on the shared file being current, at which point it is invisible in
    a hundred checkouts at once.

    So this reports the drift and, with --apply, closes it. It is deliberately
    conservative, because every branch here belongs to a task somebody else may
    be working on right now:

      - Dry run by default. A sweep that rewrites ninety branches shows its work
        before it does it, not after.
      - A worktree with ANY uncommitted change is skipped and named. `git
        rebase` would refuse there anyway, and the refusal matters more than the
        sweep: those changes are a session's in-flight work.
      - The worktree you are standing in is skipped. Rebasing it would rewrite
        the branch under the process doing the rewriting.
      - A conflicting rebase is aborted and reported, and the sweep continues.
        One worktree's conflict must strand neither the sweep nor the worktree.

    Nothing is ever removed. A worktree that cannot be swept is left exactly as
    it was, for its own session to deal with.
    """
    root = _project_root()
    base = _default_base_branch(root)
    here = worktree_root_for_cwd()
    rows = [w for w in _enriched_list(root) if w["state"] == "active"]
    if not rows:
        click.echo("No task worktrees for this project.")
        return

    plan: list[tuple[Path, str, str, str]] = []
    for w in rows:
        path = Path(w["path"])
        if not path.is_dir():
            continue
        disposition, reason = _sync_state(path, base, here)
        plan.append((path, w["branch"], disposition, reason))

    todo = [r for r in plan if r[2] == "rebase"]
    skipped = [r for r in plan if r[2] == "skip"]
    errored = [r for r in plan if r[2] == "error"]

    if not apply:
        for path, branch, _, reason in todo:
            click.echo(f"  would rebase  {_display_path(path)}  ({reason})")
        for path, branch, _, reason in skipped:
            click.echo(f"  skip          {_display_path(path)}  ({reason})")
        for path, branch, _, reason in errored:
            click.echo(f"  error         {_display_path(path)}  ({reason})")
        click.echo(
            f"\n{len(todo)} would be rebased onto {base}, {len(skipped)} skipped"
            f"{f', {len(errored)} errored' if errored else ''}."
        )
        if todo:
            click.echo("Re-run with --apply to rebase them.")
        return

    done, failed, undo, moved = 0, [], [], []
    for path, branch, _, _reason in todo:
        # Re-check immediately before acting. The plan above was built for the
        # whole fleet at once, and rebasing it takes minutes — long enough for a
        # session to wake up, start editing, or begin a merge in a worktree that
        # was idle and clean when it was surveyed. The window cannot be closed
        # entirely, but it can be made a moment wide instead of a sweep wide.
        disposition, reason = _sync_state(path, base, here)
        if disposition != "rebase":
            moved.append((path, reason))
            click.echo(f"  skip      {_display_path(path)}  (changed while sweeping: {reason})")
            continue
        # The pre-rebase tip, captured before anything moves. `git rebase` also
        # leaves it in ORIG_HEAD, but ORIG_HEAD is overwritten by the next
        # operation in that worktree — so the sweep records it here and prints
        # it, and the way back stays available after the session works on.
        was = _git_run(["rev-parse", "HEAD"], cwd=path, check=False).stdout.strip()
        res = _git_run(["rebase", base], cwd=path, check=False)
        if res.returncode == 0:
            done += 1
            undo.append((path, was))
            click.echo(f"  rebased   {_display_path(path)}  ({branch})")
            continue
        _git_run(["rebase", "--abort"], cwd=path, check=False)
        # Report what git SAID, not a guess at why. Most of these are not merge
        # conflicts at all — a file git refuses to overwrite on checkout looks
        # identical from here, and calling that a conflict sends the reader
        # hunting for markers that do not exist.
        lines = [ln for ln in (res.stderr + "\n" + res.stdout).splitlines() if ln.strip()]
        reason = next((ln.strip() for ln in lines if ln.startswith("error:")), "")
        detail = reason or (lines[-1].strip() if lines else "rebase failed")
        failed.append((path, branch, detail))
        click.echo(f"  FAILED    {_display_path(path)}  ({branch}) — aborted: {detail}")

    click.echo(
        f"\n{done} rebased onto {base}, {len(skipped) + len(moved)} skipped, "
        f"{len(failed)} conflicted."
    )
    if moved:
        click.echo(f"  {len(moved)} of those became busy after the survey and were left alone.")
    for path, branch, detail in failed:
        click.echo(f"  {_display_path(path)}: {detail}")
    if failed:
        click.echo(
            "\nEach failed worktree was restored to where it was. Its own session "
            "resolves it, in place — `git rebase " + base + "` there."
        )
    if undo:
        click.echo("\nTo put any of them back exactly as they were:")
        for path, was in undo:
            click.echo(f"  git -C {_display_path(path)} reset --hard {was[:12]}")


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


def sandbox_dir(task_id: str | None) -> None:
    """Print the absolute path of a worktree's sandbox directory (E-1428).

    The sandbox is per-worktree state that endless keeps outside the checkout —
    for a self-dev project, the throwaway database `--db sandbox` writes to. It
    is deliberately absent from `task claim`'s output, because it is not
    somewhere to cd: it is not a project, so an endless command run from it
    fails. There is one case that genuinely wants it — pointing a SQL client at
    a worktree's database — and this is that case's answer, asked for rather
    than pushed.

    With no argument, resolves the worktree cwd is inside. With `E-NNNN`,
    resolves that task's worktree, which need not be the one you are standing
    in.

    Refuses rather than printing a path that does not exist: a sandbox is
    provisioned only for a self-dev project, so on any other project the honest
    answer is that there is none, not a plausible-looking directory nothing
    ever wrote to.
    """
    from endless import config

    if task_id is None:
        name = config.worktree_dir_name()
        if name is None:
            raise click.ClickException(
                "Not inside a task worktree, so there is no sandbox to "
                "resolve.\n"
                "  Name the task instead:\n"
                "      endless worktree sandbox E-<id>"
            )
        canonical = _task_id_from_worktree_path(Path.cwd()) or name
    else:
        canonical = _normalize_task_id(task_id)
        root = _project_root()
        wt_dir = root / ".endless" / "worktrees" / f"e-{canonical[2:]}"
        if not wt_dir.is_dir():
            raise click.ClickException(
                f"No endless-managed worktree for {canonical}, so it has no "
                f"sandbox."
            )
        # The sandbox dir's basename IS the worktree dir's basename; that
        # 1-to-1 mapping is what `endless-go sandbox init` writes against.
        name = wt_dir.name

    project_root = config.enclosing_project_root()
    if project_root is None or not config.project_is_self_dev(project_root):
        raise click.ClickException(
            f"{canonical}'s project does not sandbox its worktrees, so there "
            f"is no sandbox directory.\n"
            "  Sandboxes are provisioned only for a project whose "
            ".endless/config.json\n"
            '  sets "self_dev": true.'
        )

    path = config.sandbox_root(name)
    if not path.is_dir():
        raise click.ClickException(
            f"{canonical}'s sandbox has not been provisioned:\n\n"
            f"    {path}\n\n"
            f"Provision it with:  endless-go sandbox init --mode worktree {name}"
        )
    click.echo(str(path))


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


def _rebase_in_progress(worktree_path: Path) -> bool:
    """True when a rebase is stopped mid-flight in this worktree.

    `git rebase` keeps its state in the worktree's OWN git dir, so this is
    per-worktree rather than per-repo: a rebase paused in a sibling worktree
    does not make this one true.

    Land reads conflict state (unmerged paths, REBASE_HEAD) only when this is
    true for a rebase it started itself. Read unconditionally, that state can
    belong to an entirely different operation — see `_rebase_failure_message`.
    """
    probe = _git_run(
        ["rev-parse", "--absolute-git-dir"], cwd=worktree_path, check=False,
    )
    if probe.returncode != 0 or not probe.stdout.strip():
        return False
    git_dir = Path(probe.stdout.strip())
    return (git_dir / "rebase-merge").exists() or (git_dir / "rebase-apply").exists()


def _git_said(stderr: str | None) -> str:
    """Git's own words about a failure, indented for quoting into a message.

    The whole of E-2122 is that this text was captured in CalledProcessError
    and thrown away, and a guess printed in its place. When git failed for a
    reason land cannot classify, this IS the report.
    """
    lines = [ln.rstrip() for ln in (stderr or "").strip().splitlines() if ln.strip()]
    if not lines:
        return "  (git printed nothing on stderr)"
    return "\n".join(f"  {ln}" for ln in lines)


def _rebase_branch_name(worktree_path: Path) -> str:
    """The branch a rebase in progress is rebasing, or "" if it cannot be told.

    HEAD is detached partway through a replay, so it names the machinery rather
    than the subject. git records the real answer in the rebase state directory
    as `head-name`; `--git-path` resolves it without this having to know whether
    the repository is a linked worktree.

    Falls back to HEAD for the no-rebase-in-progress case, which is how the
    rehearsal path and any future caller outside a rebase get a sane answer.
    """
    for state in ("rebase-merge", "rebase-apply"):
        path_str = _git_run(
            ["rev-parse", "--git-path", f"{state}/head-name"],
            cwd=worktree_path, check=False,
        ).stdout.strip()
        if not path_str:
            continue
        head_name = Path(path_str)
        if not head_name.is_absolute():
            head_name = worktree_path / head_name
        try:
            return _short_branch(head_name.read_text().strip())
        except OSError:
            continue
    current = _git_run(
        ["rev-parse", "--abbrev-ref", "HEAD"], cwd=worktree_path, check=False,
    ).stdout.strip()
    return "" if current in ("", "HEAD") else current


def _rebase_conflict_message(
    worktree_path: Path, base_branch: str, *, phase: str,
    stderr: str | None = None,
) -> str:
    """Capture a live rebase conflict, persist it, and build land's message.

    Only for a rebase that actually stopped on conflicting content. The caller
    establishes that (non-empty `--diff-filter=U`) before choosing this over the
    other reports in `_rebase_failure_message`; reaching here on a rebase that
    never started produces the fiction E-2122 removed.

    Called from BOTH conflict handlers (Step 3.7 orphan-replay and Step 4 main
    rebase) WHILE the rebase is still in progress — before `git rebase --abort`
    — because that abort destroys everything worth knowing. REBASE_HEAD, the
    unmerged set, and both sides of every conflicting hunk exist only until it
    runs, so the capture happens HERE rather than at the call sites: it has to
    be impossible to add a third handler that reports a conflict without first
    recording it.

    Reports the FACTS confidently: which step (via `phase`), which of the user's
    commits failed to replay, and which files conflict.

    It prescribes NOTHING for a source conflict, and that absence is the point.
    The message used to offer two numbered recoveries as candidates to judge
    between — resolve in place, or reset and re-apply. Both put the branch's
    side of the hunk back, and when the base branch has DELETED something that
    side still references, both reintroduce a name with nothing behind it: the
    land succeeds and ships code that fails on first use. Someone who knows the
    codebase catches that. Someone reading two numbered steps as instructions
    from the tool does not. So the message hands off to `endless worktree
    diagnose`, which classifies from the capture and prescribes only what it can
    prove.

    The one confident path stays: when every conflicting file is an
    endless-managed auto-file, endless wrote all of them and none carries
    authored work, so restoring them from the base branch is lossless by
    construction — proven, not guessed.
    """
    branch = _rebase_branch_name(worktree_path)
    task_id = _task_id_from_worktree_path(worktree_path) or ""
    # The REBASE_HEAD gate is E-2122's and travels with the read it guards.
    # REBASE_HEAD is a plain ref: with no rebase running it either does not
    # resolve or still holds a value from a DIFFERENT operation, and reading it
    # unconditionally reported someone else's commit as the cause of this
    # failure. That read now happens inside the capture, so the gate goes there.
    ev = land_conflict.capture_evidence(
        worktree_path, base_branch, branch, task_id=task_id, phase=phase,
        rebase_in_progress=_rebase_in_progress(worktree_path),
    )
    stored = land_conflict.store_evidence(worktree_path, ev)
    return _conflict_message(ev, captured=stored is not None, stderr=stderr)


def _conflict_message(
    ev: land_conflict.ConflictEvidence, *,
    captured: bool = True, stderr: str | None = None,
) -> str:
    """Render land's refusal from captured evidence. Pure — no git, no writes.

    `captured` is whether the evidence actually reached disk. It is threaded in
    rather than assumed because the alternative is sending someone to a command
    that will tell them there is nothing recorded, which reads as the tool
    losing their conflict rather than as a directory it could not write.

    `stderr` is git's own words about the failure (E-2122), quoted verbatim
    under the facts. A conflict land can classify still benefits from what git
    said about it, and the two are not in competition.
    """
    wt = _display_path(Path(ev.worktree_path))
    commit_line = ""
    if ev.rebase_head:
        short = ev.rebase_head[:12]
        commit_line = (
            f"Your commit that failed to replay: {short} {ev.rebase_head_subject}\n\n"
            if ev.rebase_head_subject
            else f"Your commit that failed to replay: {short}\n\n"
        )

    file_block = (
        "\n".join(f"  {f}" for f in ev.unmerged_paths)
        if ev.unmerged_paths else "  (none reported)"
    )
    said = f"git said:\n{_git_said(stderr)}\n\n" if stderr else ""
    header = (
        f"rebase conflict while {ev.phase}.\n\n"
        f"{commit_line}"
        f"Conflicting files:\n{file_block}\n\n"
        f"{said}"
    )

    files = ev.unmerged_paths
    only_auto = bool(files) and all(_is_auto_file(f) for f in files)
    if only_auto:
        globs = " ".join(AUTO_COMMIT_GLOBS)
        return header + (
            f"Every conflicting file is an endless-managed auto-file; restoring "
            f"them from {ev.base_branch} is safe. Recover, then retry land:\n"
            f"  git -C {wt} checkout {ev.base_branch} -- {globs}\n"
            f"  endless worktree land <id>\n"
        )

    target = ev.task_id or "<id>"
    if not captured:
        return header + (
            f"A source file conflicts, and the conflict state could NOT be "
            f"written to {_display_path(land_conflict.evidence_path(Path(ev.worktree_path)))} "
            f"— so `endless worktree diagnose` has nothing to read and this "
            f"message is all that survives the abort. Fix that path and re-run "
            f"the land to get a diagnosable failure.\n\n"
            f"No recovery is offered here. The recoveries that fit most "
            f"conflicts restore your side of the hunk, and when {ev.base_branch} "
            f"has deleted something that side still uses, they reintroduce a "
            f"reference with nothing behind it: the land succeeds and the code "
            f"fails the first time it runs.\n"
        )
    hint_note = (
        f"git's hints above are its generic advice for any conflict, and one of "
        f"them is `git rebase --continue`. Do not follow it yet — see below.\n\n"
        if stderr and "rebase --continue" in stderr else ""
    )
    return header + hint_note + (
        f"A source file conflicts. The rebase has been aborted and your branch "
        f"is exactly as it was, but the conflict state was recorded first — "
        f"nothing about the failure is lost.\n\n"
        f"Classify it before you touch anything:\n"
        f"  endless worktree diagnose {target}\n\n"
        f"No recovery is offered here on purpose. The recoveries that fit most "
        f"conflicts — resolving in place, or resetting and re-applying your "
        f"delta — both restore your side of the hunk, and when {ev.base_branch} "
        f"has deleted something that side still uses, they reintroduce a "
        f"reference with nothing behind it: the land succeeds and the code "
        f"fails the first time it runs. `diagnose` tells you which kind of "
        f"conflict this is, and prescribes a recovery only when it can prove "
        f"one.\n"
    )


def _rebase_failure_message(
    worktree_path: Path, base_branch: str, *, phase: str,
    stderr: str | None, pre_existing: bool,
) -> str:
    """Report a non-zero `git rebase` as what it actually was (E-2122).

    Land used to treat EVERY non-zero exit as a content conflict. `git rebase`
    also exits non-zero when it refuses to start at all — a dirty worktree, a
    rebase already in progress, a plain operational failure — and none of those
    produce unmerged paths or are fixed by a conflict's recoveries. The report
    that resulted asserted a conflict that never happened, listed no files, and
    offered candidate recoveries for a cause it had not established.

    Three outcomes, distinguished by facts rather than by exit code:

    - A rebase was already running before land touched this worktree. Nothing
      land did caused the failure, and the state in the worktree belongs to that
      other operation. Land does not abort it.
    - Our rebase stopped on conflicting content — unmerged paths exist. A real
      conflict; report it as one, with git's words as context.
    - Our rebase failed for any other reason. Git named it on stderr, so quote
      that verbatim and offer nothing: there is nothing to judge between when
      the cause is already stated.
    """
    wt = _display_path(worktree_path)

    if pre_existing:
        return (
            f"cannot rebase: a rebase was already in progress in this worktree "
            f"before land started, so land did not begin one.\n\n"
            f"git said:\n{_git_said(stderr)}\n\n"
            f"That rebase has been left exactly as it was — land does not abort "
            f"an operation it did not start. Finish or abandon it yourself, then "
            f"retry:\n"
            f"  cd {wt}\n"
            f"  git status                # see what it stopped on\n"
            f"  git rebase --continue     # if you can resolve it\n"
            f"  git rebase --abort        # to discard it\n"
            f"then re-run: endless worktree land <id>\n"
        )

    unmerged = _git_run(
        ["diff", "--name-only", "--diff-filter=U"],
        cwd=worktree_path, check=False,
    ).stdout
    if any(ln.strip() for ln in unmerged.splitlines()):
        return _rebase_conflict_message(
            worktree_path, base_branch, phase=phase, stderr=stderr,
        )

    return (
        f"rebase failed while {phase}.\n\n"
        f"This was NOT a content conflict — no files are in conflict, so there "
        f"is nothing to resolve. Git reported why:\n\n"
        f"git said:\n{_git_said(stderr)}\n\n"
        f"Act on what git said above. No recovery candidates are offered here: "
        f"the cause is stated, so there is nothing to guess between.\n\n"
        f"Inspect:\n"
        f"  git -C {wt} status\n"
        f"  git -C {wt} log {base_branch}..HEAD\n"
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


def _tilde(p: Path) -> str:
    """Display a Path with $HOME collapsed to ~. Falls back to absolute."""
    from endless import config
    return config.tilde(p)


def task_branch(task_id: int) -> str:
    """The git branch a task's worktree sits on: `task/<id>` (ED-1587).

    The one place the pattern is written down, and the reason it is a function
    at all: a branch name is now a pure function of the task id, so any code
    that has the id can CONSTRUCT the name instead of looking it up. That is
    what retired `task_landings.branch` — the column existed because the name
    was unknowable from the id, back when it carried a title slug frozen at
    creation (E-971, ED-1167).

    Deliberately not configurable. A pattern the user can change reintroduces
    the lookup this removes, and strands every existing branch the moment it
    changes; ED-1587 rejects it explicitly.
    """
    return f"task/{task_id}"


class DefaultBranchUnresolved(click.ClickException):
    """Raised when no resolution step could name this repo's default branch."""


def _read_default_branch_config(project_root: Path) -> str:
    """The `default_branch` field of <root>/.endless/config.json, or ''."""
    try:
        data = json.loads((project_root / ".endless" / "config.json").read_text())
    except (OSError, ValueError):
        return ""
    value = data.get("default_branch") or ""
    return value.strip() if isinstance(value, str) else ""


def _branch_if_exists(project_root: Path, name: str) -> str:
    """Return name when it resolves to a commit here, else ''."""
    if not name:
        return ""
    res = _git_run(
        ["rev-parse", "--verify", "--quiet", f"{name}^{{commit}}"],
        cwd=project_root, check=False,
    )
    return name if res.returncode == 0 else ""


def _git_config_value(project_root: Path, key: str) -> str:
    res = _git_run(["config", "--get", key], cwd=project_root, check=False)
    return res.stdout.strip() if res.returncode == 0 else ""


def _default_base_branch(project_root: Path) -> str:
    """Resolve the branch this repo's work lands into (E-1940, absorbing E-1166).

    Resolution order, mirroring monitor.DefaultBranch in Go exactly:

      1. `.endless/config.json`'s `default_branch` — explicit beats detection.
      2. `git symbolic-ref --short refs/remotes/origin/HEAD`, minus `origin/`.
      3. `git config init.defaultBranch`.
      4. `main`, then `master`, whichever exists.

    Every candidate must resolve to a commit here, and step 1 does not fall
    through when it fails — an explicit branch that does not exist is a typo in
    the project's own config, not an invitation to guess around it.

    Raises rather than falling back to the literal 'main'. That fallback was
    E-1166's open limitation and E-1940's bug: on a repo whose default branch is
    something else it made every probe exit 128, which read as a permanent
    all-clear. tests/test_default_branch_parity.py asserts the two
    implementations agree case for case.
    """
    configured = _read_default_branch_config(project_root)
    if configured:
        if not _branch_if_exists(project_root, configured):
            raise DefaultBranchUnresolved(
                f"{_tilde(project_root)}/.endless/config.json sets default_branch "
                f"{configured!r}, which does not exist in this repository."
            )
        return configured

    res = _git_run(
        ["symbolic-ref", "--short", "--quiet", "refs/remotes/origin/HEAD"],
        cwd=project_root, check=False,
    )
    origin_head = ""
    if res.returncode == 0:
        origin_head = res.stdout.strip().removeprefix("origin/")
    for candidate in (
        origin_head,
        _git_config_value(project_root, "init.defaultBranch"),
        "main",
        "master",
    ):
        if _branch_if_exists(project_root, candidate):
            return candidate

    raise DefaultBranchUnresolved(
        f"Cannot resolve the default branch of {_tilde(project_root)}. "
        f"Set it explicitly: add \"default_branch\": \"<branch>\" to "
        f".endless/config.json, or run `git remote set-head origin --auto`."
    )


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


def _orphan_task_branches(task_id: int, project_root: Path) -> list[str]:
    """Every existing local branch that belongs to this task.

    `task/<id>` — the name this task's worktree is cut on — plus any surviving
    `task/<id>-<slug>` from before ED-1587 renamed them. The legacy sweep is the
    one place a branch name is still looked up rather than constructed, and it
    earns it: E-1500's guarantee is that a claim can never silently strand an
    orphan branch holding real work, and an orphan cut before the rename would
    walk straight past a check that only knows the new name. Once no
    slug-branches remain in a repo this returns exactly the constructed name.

    Ordered constructed-first so the message a user sees names their own task's
    branch before any legacy one.
    """
    names = []
    if _branch_exists(task_branch(task_id), project_root):
        names.append(task_branch(task_id))
    res = _git_run(
        ["for-each-ref", "--format=%(refname:short)",
         f"refs/heads/task/{task_id}-*"],
        cwd=project_root, check=False,
    )
    if res.returncode == 0:
        names.extend(
            ln.strip() for ln in res.stdout.splitlines() if ln.strip()
        )
    return names


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
    task_id: int, project_root: Path,
) -> tuple[Path, bool]:
    """Create the per-task worktree for E-<id>.

    Returns (worktree_path, created). 'created' is False if the worktree
    already existed for this task (idempotent no-op). Raises
    ClickException on path collision with a foreign worktree, on
    uncommitted plan files (per E-1169), or on git-add failure.
    """
    canonical = f"E-{task_id}"
    branch = task_branch(task_id)
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

    # E-1500: the dir is gone but a branch for this task may still exist (an
    # orphan left by `worktree drop` / land-reap). Recover instead of failing on
    # `git worktree add -b`: either delete the branch so we recreate it fresh
    # below, or raise with actionable guidance if it carries real work.
    for orphan in _orphan_task_branches(task_id, project_root):
        _handle_orphan_branch(task_id, orphan, base, project_root)

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
    (`--reopen`): a working branch — the original `task/<id>` branch is
    reused if it still exists, else a fresh branch is cut off `base`.

    Runs the shared bootstrap but performs NO status transition (the caller
    owns that). Returns the worktree path.
    """
    canonical = f"E-{task_id}"
    branch = task_branch(task_id)
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
    # Silent on success (E-1428). This used to print the sandbox cache path as
    # a bullet directly above the worktree path, and readers scanning a claim
    # for somewhere to cd took the first path they saw — landing in a cache
    # directory that is not a project, so every endless command after it failed
    # with "Not in a registered project directory". The path has no routine
    # user-facing purpose; `endless worktree sandbox` prints it on demand for
    # the rare case (pointing a SQL client at a worktree's database) that wants
    # it.


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
    (`session_id` NULL) and stamps `landed_at` at the merge commit's date —
    derived here from `git show -s --format=%cI <sha>` unless `at` is passed —
    so the row reflects when the work actually landed, not now().

    E-2108 removed the `--branch` this used to accept. A landing records no
    branch at all now (the task branch is `task/<id>`, derivable from the id),
    so the flag had nothing left to fill in and the "records NULL" case it
    existed for stopped being a case.
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
        click.echo(f"  Landed at: {landed_at}")
        return

    from endless.event_bridge import emit_event

    emit_event(
        kind="task.landed",
        project=proj_name,
        entity_type="task",
        entity_id=str(item_id),
        payload={"merge_commit_sha": sha},
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

    base_branch rides in the payload (E-2005) so task_landings records the
    branch the work landed ON. It is what the "E-NNNN landed on main (1dd0006)"
    notice reads back to a session, and it is known only here — the Go executor
    sees the event, never the git repo it came from. The task branch it landed
    FROM is not in the payload: `task/<id>` follows from the entity ref the
    event already carries (ED-1587, E-2108). `branch` stays a parameter only
    because the failure message below names the branch the user was landing.
    """
    from endless.event_bridge import emit_event

    try:
        emit_event(
            kind="task.landed",
            project=proj_name,
            entity_type="task",
            entity_id=str(item_id),
            payload={
                "base_branch": base_branch,
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


def _no_worktree_to_land_message(canonical: str) -> str:
    """The message for `worktree land <id>` when no worktree exists (E-1308).

    A landed worktree is REMOVED by the reaper once its recorded landing ages
    past worktree_ttl, so "no worktree" is the ordinary end state of successful
    work — yet the only message was "No endless-managed worktree for E-NNN",
    which reads as "your work is lost". Consult the recorded landing before
    saying that; today's message is still right when nothing is recorded.

    E-1308 originally proposed detecting this with `git branch --merged`. That
    cannot work: `land` rebases, so a landed branch is not an ancestor of the
    base and the probe fails for exactly the case it targets. The recorded
    landing is the reliable signal — the same one the unsettled probe and the
    reaper now use.
    """
    from endless.task_cmd import _task_landings

    try:
        landings = _task_landings(int(canonical.removeprefix("E-")))
    except Exception:
        landings = []
    if landings:
        latest = landings[0]
        sha = (latest["merge_commit_sha"] or "")[:12]
        return (
            f"{canonical} already landed (commit {sha} at {latest['landed_at']}); "
            f"nothing to do. Its worktree was removed after the landing aged "
            f"past worktree_ttl. Full history: endless task landed {canonical}"
        )
    return (
        f"No endless-managed worktree for {canonical}, and no landing is "
        f"recorded for it. "
        f"(Use 'endless worktree list' to see available worktrees.)"
    )


def _resolve_land_target(task_id: str | None) -> tuple[str, Path, str, str]:
    """Resolve a task id to (canonical, worktree_path, branch, base_branch).

    The SAME resolution `land` performs, deliberately: `diagnose` reads a
    capture keyed to the worktree `land` would have used, so any drift between
    the two would have it reading someone else's failure.

    `task_id` may be None, which means "the task whose worktree I am standing
    in" — `land` cannot offer that (it is not a command to run by accident),
    but a read-only diagnostic run mid-session should not make anyone retype
    the id already encoded in cwd.
    """
    if task_id:
        canonical = _normalize_task_id(task_id)
    else:
        here = worktree_root_for_cwd()
        canonical = _task_id_from_worktree_path(here) if here else None
        if not canonical:
            raise click.ClickException(
                "Not inside a task worktree, so there is no task to diagnose. "
                "Name one: endless worktree diagnose E-NNNN"
            )

    rows = _enriched_list(_project_root())
    target = _branch_for_task(rows, canonical)
    if target is None:
        raise click.ClickException(_no_worktree_to_land_message(canonical))
    branch = target["branch"]
    if not branch:
        raise click.ClickException(
            f"Worktree for {canonical} has no branch (detached HEAD)."
        )
    base_branch = (target["companion"] or {}).get("base_branch") \
        or _default_base_branch(_project_root())
    return canonical, Path(target["path"]), branch, base_branch


def diagnose_land_conflict(task_id: str | None, as_json: bool) -> None:
    """Classify the rebase conflict a land recorded, and prescribe only what is
    proven (E-1957).

    This is the second half of the split `land` makes when it fails: `land`
    records the facts and refuses to interpret them mid-abort; this interprets
    them, on demand, with the whole repository still available to test against.

    Reproduces nothing. If there is no capture, that is the answer — said
    plainly, with a non-zero exit — because a diagnosis invented from a
    re-derived conflict would describe a rebase nobody ran.
    """
    canonical, worktree_path, _branch, base_branch = _resolve_land_target(task_id)

    ev = land_conflict.load_evidence(worktree_path)
    if ev is None:
        raise click.ClickException(
            f"No land conflict is recorded for {canonical}.\n\n"
            f"A capture is written only when `endless worktree land` actually "
            f"hits a rebase conflict, and it is stored with the worktree, so it "
            f"is gone once the worktree is reaped. Nothing is reproduced here on "
            f"purpose: a conflict re-derived now would be against today's "
            f"{base_branch}, not the one the land failed against.\n\n"
            f"To see whether a land WOULD conflict, rehearse it:\n"
            f"  endless worktree land {canonical} --dry-run"
        )

    cl = land_conflict.classify(ev, worktree_path)
    if as_json:
        click.echo(land_conflict.render_json(ev, cl), nl=False)
    else:
        click.echo(land_conflict.render_human(ev, cl), nl=False)


def _rehearse_land_rebase(
    worktree_path: Path, branch: str, base_branch: str, main_root: Path,
    canonical: str,
) -> land_conflict.ConflictEvidence | None:
    """Run land's rebase for real, on a copy, and return the conflict evidence
    it produced — or None when it went through cleanly (E-1957).

    `--dry-run` existed to preview a land and could not preview the one failure
    worth previewing, because printing four paths tells you nothing about
    whether the rebase works. This runs the actual sequence — the orphan drop,
    then the rebase onto the base branch — against a throwaway branch in a
    throwaway checkout, so the answer is git's rather than an estimate of it.

    Same machinery as the post-mortem, deliberately: one rebase, one capture,
    one classifier. A predictor built separately from the thing it predicts
    drifts from it, and a `--dry-run` that disagrees with the land is worse than
    no `--dry-run`.

    The base branch, the task branch and the database are untouched. The
    throwaway branch and checkout are removed in a `finally`, including when the
    rehearsal itself raises.

    One deliberate divergence: the evidence is not persisted. The capture slot
    holds "the conflict this worktree's land hit", and a rehearsal overwriting
    it would replace a real post-mortem with a hypothetical.
    """
    scratch = Path(tempfile.mkdtemp(prefix="endless-land-rehearsal-"))
    # git worktree add wants to create the leaf itself.
    checkout = scratch / "wt"
    # Named from the scratch dir, whose suffix mkdtemp guarantees unique. A pid
    # would repeat after a hard kill left the previous run's branch behind, and
    # the collision would surface as a land that cannot rehearse.
    rehearsal_branch = f"endless/land-rehearsal/{canonical.lower()}-{scratch.name}"

    made_branch = False
    made_checkout = False
    try:
        _git_run(["branch", rehearsal_branch, branch], cwd=worktree_path)
        made_branch = True
        _git_run(
            ["worktree", "add", str(checkout), rehearsal_branch],
            cwd=worktree_path,
        )
        made_checkout = True

        # Land's Step 3.5 folds the worktree's pending verb additions into the
        # branch before rebasing, which is what keeps verbs.jsonl from
        # conflicting. A fresh checkout has no pending additions, so copy the
        # live file across first — otherwise the rehearsal predicts a conflict
        # the land itself would have dissolved.
        live_verbs = worktree_path / ".endless" / "verbs.jsonl"
        if live_verbs.exists():
            rehearsed_verbs = checkout / ".endless" / "verbs.jsonl"
            rehearsed_verbs.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(live_verbs, rehearsed_verbs)
            _dedup_worktree_verbs_against_main(checkout, main_root)

        phase = None
        try:
            _drop_orphan_amendable_commits(checkout, base_branch)
        except subprocess.CalledProcessError:
            phase = ("replaying your commits after dropping base auto-amend "
                     "commits")
        if phase is None:
            try:
                _git_run(["rebase", base_branch], cwd=checkout)
            except subprocess.CalledProcessError:
                phase = f"rebasing your branch onto {base_branch}"
        if phase is None:
            return None

        ev = land_conflict.capture_evidence(
            checkout, base_branch, rehearsal_branch,
            task_id=canonical, phase=phase, rehearsal=True,
            rebase_in_progress=_rebase_in_progress(checkout),
        )
        _git_run(["rebase", "--abort"], cwd=checkout, check=False)
        # Re-point at the real worktree: the classifier re-reads the repository
        # (`git grep` over the base branch and the fork point), and the checkout
        # this ran in is about to stop existing. Same repository, same objects.
        ev.worktree_path = str(worktree_path)
        ev.branch = branch
        return ev
    finally:
        if made_checkout:
            _git_run(
                ["worktree", "remove", "--force", str(checkout)],
                cwd=worktree_path, check=False,
            )
        if made_branch:
            _git_run(
                ["branch", "-D", rehearsal_branch],
                cwd=worktree_path, check=False,
            )
        shutil.rmtree(scratch, ignore_errors=True)
        _git_run(["worktree", "prune"], cwd=worktree_path, check=False)


def land_worktree(
    task_id: str,
    dry_run: bool,
    record_only: bool = False,
    sha: str | None = None,
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
        _record_only_landing(canonical, sha, at, dry_run)
        return

    main_root = _project_root()
    rows = _enriched_list(main_root)
    target = _branch_for_task(rows, canonical)
    if target is None:
        raise click.ClickException(_no_worktree_to_land_message(canonical))
    branch = target["branch"]
    if not branch:
        raise click.ClickException(
            f"Worktree for {canonical} has no branch (detached HEAD); cannot land."
        )
    worktree_path = Path(target["path"])
    # E-1940: the companion records the base the worktree was cut from; resolve
    # it only when absent. The old `"main"` default was a silent wrong answer on
    # any project whose default branch differs — and it is the LAND path, so it
    # would have rebased onto a branch that is not the one being landed into.
    base_branch = (target["companion"] or {}).get("base_branch") \
        or _default_base_branch(main_root)

    if dry_run:
        click.echo(f"Would land: {canonical}")
        click.echo(f"  Worktree: {worktree_path}")
        click.echo(f"  Branch:   {branch}")
        click.echo(f"  Base:     {base_branch}")
        click.echo(f"  Main:     {main_root}")
        click.echo("")
        # E-1957: rehearse the rebase rather than describe it. A preview that
        # cannot preview the failure it exists to preview is a preview of
        # nothing, and the rebase is the only step of a land that fails in a way
        # the operator has to reason about.
        try:
            ev = _rehearse_land_rebase(
                worktree_path, branch, base_branch, main_root, canonical,
            )
        except subprocess.CalledProcessError as e:
            raise click.ClickException(
                f"could not rehearse the rebase: {e.stderr or e}"
            )
        if ev is None:
            click.echo(
                click.style("✓", fg="green")
                + f" Rehearsed the rebase onto {base_branch} on a throwaway "
                f"branch: no conflict."
            )
            return
        click.echo(
            click.style("✗", fg="red")
            + f" Rehearsed the rebase onto {base_branch} on a throwaway "
            f"branch: it conflicts.\n"
        )
        cl = land_conflict.classify(ev, worktree_path)
        click.echo(land_conflict.render_human(ev, cl), nl=False)
        raise SystemExit(1)

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
        # Whether a rebase was ALREADY stopped here before land ran. Captured
        # before, because afterwards the two are indistinguishable — and land
        # must neither blame nor abort an operation it did not start (E-2122).
        # This step's rebase runs before Step 3.8's dirty-worktree guard, so an
        # uncommitted edit reaches git here and it refuses to start.
        rebase_was_running = _rebase_in_progress(worktree_path)
        try:
            n_orphans, first_subj = _drop_orphan_amendable_commits(
                worktree_path, base_branch
            )
        except subprocess.CalledProcessError as e:
            # The orphan drop and the replay of the user's commits share one
            # rebase; a conflict here is the replay conflicting, not the drop.
            # Read the state BEFORE aborting, then abort — but only a rebase
            # this step actually started.
            msg = _rebase_failure_message(
                worktree_path, base_branch,
                phase="replaying your commits after dropping base auto-amend commits",
                stderr=e.stderr, pre_existing=rebase_was_running,
            )
            if not rebase_was_running:
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
        rebase_was_running = _rebase_in_progress(worktree_path)
        try:
            _git_run(["rebase", base_branch], cwd=worktree_path)
        except subprocess.CalledProcessError as e:
            # Read the state (files + failing commit) BEFORE aborting, then
            # abort. What git printed decides which report this is: a conflict
            # names files and offers candidates, anything else quotes git and
            # offers none (E-2122).
            msg = _rebase_failure_message(
                worktree_path, base_branch,
                phase=f"rebasing your branch onto {base_branch}",
                stderr=e.stderr, pre_existing=rebase_was_running,
            )
            if not rebase_was_running:
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


def _worktree_in_use_probe(worktree_path: Path) -> tuple[str, str]:
    """Ask `endless-go worktree in-use` whether anything depends on a directory.

    Returns (verdict, detail); verdict is "free", "in-use", "unknown" or
    "no-binary". Callers decide what to DO about each — dropping refuses on
    anything but "free", and so does the sync sweep, for the same reason in a
    milder form: rebasing a branch under a session that is standing in it does
    not orphan its cwd, but it does change every file beneath a process that
    has already read them.

    This shells out rather than probing here, because monitor.WorktreeInUse is
    the one implementation of the question and it runs two complementary probes
    (an active-session row, and a live process holding cwd). Reimplementing
    either in Python is what the verb exists to prevent — see
    internal/monitor/worktree_inuse.go.
    """
    from endless import config

    task_id = _task_id_from_worktree_path(worktree_path)
    # `--task 0` means "no owning task", which the verb reads as "run only the
    # live-process probe". A worktree outside the e-NNN convention has no task
    # row to look up, so that is the whole answer available for it.
    task_arg = task_id.removeprefix("E-") if task_id else "0"

    binary = shutil.which("endless-go")
    if not binary:
        return "no-binary", "endless-go is not on PATH"

    # E-1429: the verb READS the sessions table, so thread the resolved --db
    # context. Without it a probe run from a self-dev worktree would ask the
    # real ledger about a sandbox's sessions and be told nobody is home.
    result = subprocess.run(
        [binary, *config.go_db_context_args(), "worktree", "in-use",
         "--dir", str(worktree_path), "--task", task_arg],
        capture_output=True, text=True,
    )
    detail = (result.stdout.strip() or result.stderr.strip()
              or f"exit {result.returncode}")
    if result.returncode == 0:
        return "free", ""
    if result.returncode == 3:
        return "in-use", detail
    return "unknown", detail


def _guard_worktree_in_use(worktree_path: Path) -> None:
    """Refuse to drop a worktree anything is still using (E-1947).

    Shells out to `endless-go worktree in-use`, which is monitor.WorktreeInUse
    — the SAME predicate the worktree reaper applies before removing a
    directory. It is not reimplemented here on purpose: two copies of a check
    that gates `rm -rf`, in two languages, is the shape of the failure that
    already cost two-plus weeks. Do not inline an `lsof` call or a sessions
    query in this module.

    Exit codes from the verb: 0 not in use, 3 in use (reason on stdout),
    anything else undetermined. Undetermined refuses, as does a missing
    binary — a guard that cannot answer must not wave the caller through.

    Callers pass --force to skip this entirely, as with drop's other refusals.
    """
    verdict, detail = _worktree_in_use_probe(worktree_path)
    if verdict == "free":
        return
    if verdict == "no-binary":
        raise click.ClickException(
            f"Cannot verify whether this worktree is in use: endless-go is "
            f"not on PATH.\n{worktree_path}\n"
            f"Install it (`just install`) or use --force to drop anyway."
        )
    if verdict == "unknown":
        raise click.ClickException(
            f"Cannot verify whether this worktree is in use: {detail}\n"
            f"{worktree_path}\n"
            f"Resolve the error, or use --force to drop anyway."
        )
    raise click.ClickException(
        f"Refusing to drop a worktree that is in use: {worktree_path}\n"
        f"  {detail}\n\n"
        f"Dropping removes the directory out from under whatever is standing "
        f"in it, orphaning that session's cwd.\n"
        f"If the goal is to discard diverged history rather than the "
        f"directory, reset or rebase the branch in place — the worktree "
        f"survives and the session keeps working.\n"
        f"Use --force only once you know nothing is using it."
    )


def drop_worktree(name_or_path: str, force: bool) -> None:
    """Remove a worktree explicitly. Refuses in-use/modified/foreign without --force."""
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
        # Before the git-state checks: a worktree in use must say so FIRST.
        # "uncommitted changes" invites --force, and reaching for --force on a
        # directory a live session is sitting in is the exact damage E-1947
        # exists to prevent.
        _guard_worktree_in_use(worktree_path)
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

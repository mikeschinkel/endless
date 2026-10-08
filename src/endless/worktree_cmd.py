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
import time
import tomllib
from datetime import datetime, timezone
from pathlib import Path

import click

from endless import agent_help, doc_mirror, land_conflict, provenance, rowcap
from endless.task_cmd import _display_path, _resolve_project
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

# E-2174: the first backoff step before land re-attempts a git call that lost a
# race for the index lock; each attempt doubles it, so the eight attempts above
# span roughly 0.05s + 0.1 + 0.2 + ... ≈ 6s in total.
#
# Deliberately longer than internal/events/commit.go's 10ms base (E-2137), which
# spans ≈2.5s, because the two paths pay different costs for waiting. That one
# sits on the hot path of every event emit and must not hang a CLI command; land
# is human-initiated, already rebuilds a binary, and a spurious failure there
# costs the operator a diagnosis. Waiting seconds is cheap; failing is not.
LAND_LOCK_BACKOFF_BASE = 0.05

# E-1500's plan-viability threshold lived here and was retired by E-2137. It
# existed to judge whether a plan recovered from an orphan BRANCH was worth
# adopting back into the database. Mirrors are no longer written to branches, so
# there is nothing to recover and nothing to judge: a branch whose mirrors match
# their columns is discarded, and one whose mirrors DIFFER is reported to a
# person rather than measured against a character count.


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


def _is_lock_contention(err_text: str) -> bool:
    """True when a git failure is another process holding the index lock.

    Deliberately SEPARATE from _is_retryable_ff_merge_error (E-2174). Both
    answer "should land retry?", but about different repositories: that
    predicate's matches describe a repository whose content moved under the
    land, this one describes a repository that was merely busy. Folding them
    together would make a diverged branch and a live `git status` indis-
    tinguishable in every message that reports either.

    Endless is the usual holder. `git status --porcelain` against every worktree
    is what the session monitor repaints on, what the per-minute
    worktree-unlanded job sweeps with, and what `worktree check` / `worktree
    sync` shell out to — so the odds of a land colliding scale with the worktree
    count, which is one per active task by design.

    Matched on git's message rather than an exit code, because git returns the
    same code here as for a dozen unrelated fatals; the text is what separates
    them. Both spellings are matched: the lock file's own name (every git
    version) and the advisory sentence git adds when it recognizes a concurrent
    process. Mirrors isIndexLocked in internal/events/commit.go — keep in sync.
    """
    err_lower = err_text.lower()
    return (
        "index.lock" in err_lower
        or "another git process seems to be running" in err_lower
    )


def _lock_contention_text(e: subprocess.CalledProcessError) -> str | None:
    """Git's own words when this failure is lock contention, else None.

    Returns the text rather than a bool so a caller can record it as the
    land's last error in the same breath it decides to retry.
    """
    text = (e.stderr or "") + (e.stdout or "")
    return text if _is_lock_contention(text) else None


def _lock_backoff(attempt: int) -> None:
    """Wait before land re-attempts a git call that lost the index lock.

    Exponential from LAND_LOCK_BACKOFF_BASE, so the retries do not all land
    inside the single monitor repaint that took the lock. Never removes the
    lock file: the holder is a live process (the one observed in E-2174 was
    still running at diagnosis and released it on its own moments later), and
    deleting another process's lock is how an index gets corrupted.
    """
    time.sleep(LAND_LOCK_BACKOFF_BASE * (2 ** (attempt - 1)))


def _project_root() -> Path:
    """Return the registered project root path for cwd's project."""
    project_id, _ = _resolve_project(None)
    from endless import db
    row = db.query("SELECT path FROM projects WHERE id = ? LIMIT 1", (project_id,))
    if not row:
        # The id resolved but the row it names has no path: the project
        # registration itself disagrees with itself. Nothing an agent can do
        # from here — re-registering a project is a statement about which
        # directory on this machine IS the project, which only the person who
        # owns the machine can make.
        raise agent_help.report(
            f"Project id {project_id} has no registered path",
            "whether to repair or re-register this project, since its id "
            "resolves but its path row does not exist",
        )
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
        # Nothing is blocked and nothing is wrong with the answer: the
        # path-derived id is used and the command finishes. The warning exists
        # so a stale companion stops being invisible, which is why it is
        # no_report for the reader of this command.
        agent_help.warn.no_report(
            f"endless: stale companion in {worktree_path}/.endless/worktree.json: "
            f"task_id={legacy!r} disagrees with path-derived {from_path!r}; "
            f"using {from_path!r}.",
            f"The path-derived {from_path} is what was used, so continue; the "
            f"legacy task_id key can be removed from the companion",
        )
        # ...and recorded, because the disagreement is a standing condition of
        # that worktree that repeats on every command reading the companion
        # until someone edits the file (E-2213). Fingerprinted on the
        # worktree, so it is one incident however many commands hit it.
        agent_help.warn.record(
            "WARN-0028",
            f"stale companion {_tilde(worktree_path)}/.endless/worktree.json: "
            f"task_id={legacy} disagrees with path-derived {from_path}",
            source="worktree:companion",
            fingerprint=f"companion={worktree_path}",
        )


def _check_worktree_lock_liveness(worktree_path: Path) -> tuple[str, dict | None]:
    """Inspect <worktree>/.endless/worktree.lock for liveness (E-1209).

    Returns (state, lock_data) where state is one of:
      - "absent":    no lock file
      - "alive":     lock holder's PID responds to kill(pid, 0)
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
    and strips them via a single 'rebase --onto base <upstream>', where
    upstream merges base and the last orphan so copies of base's commits
    are dropped rather than replayed (E-2242).

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
    #
    # The upstream is a throwaway merge of base and the last orphan, not the
    # orphan alone (E-2242). Git drops a commit whose change is already in
    # upstream...HEAD's left side; with the orphan as upstream that side is
    # empty, so a branch forked before base was rewritten replays its old
    # copies of base's commits onto base and conflicts. With base as a parent,
    # git compares against base and drops them, while the replayed range
    # (upstream..HEAD) still starts after the orphans.
    upstream = _git_run(
        ["commit-tree", f"{base_branch}^{{tree}}",
         "-p", base_branch, "-p", last_orphan_sha,
         "-m", "endless: land orphan-drop upstream"],
        cwd=worktree_path,
    ).stdout.strip()
    _git_run(
        ["rebase", "--onto", base_branch, upstream],
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
        click.echo(json.dumps(provenance.attach(rows), indent=2))
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
        # The TSV left this CONDITIONAL, and the condition is answerable here
        # rather than by the agent: ask whether the REGISTERED project is a git
        # repository. If it is, cwd simply is not inside it and the agent only
        # has to cd; if it is not, the project was registered against a
        # directory git does not manage, and which directory this project
        # actually is — or whether to `git init` the one named — is the user's
        # to settle. Both branches read the same sentence, which is why `text`
        # pins it and only the verdict differs.
        here = "Not inside a git repository"
        project_root = _project_root()
        project_is_repo = _git_run(
            ["rev-parse", "--git-dir"], cwd=project_root, check=False,
        ).returncode == 0
        if project_is_repo:
            raise agent_help.no_report(
                f"{here}: cwd {cwd} is outside {project_root}, which is one.",
                f"cd into {project_root} (or one of its task worktrees) and "
                f"re-run",
                text=here,
            )
        raise agent_help.report(
            f"{here}, and neither is the registered project root "
            f"{project_root}.",
            "whether this project is registered against the right directory, "
            "or whether that directory should be a git repository at all",
            text=here,
        )
    toplevel = Path(toplevel_str).resolve()

    root = _project_root()
    rows = _enriched_list(root)
    match = next((r for r in rows if Path(r["path"]).resolve() == toplevel), None)
    if match is None:
        inconsistent = (
            f"cwd {cwd} resolves to a working tree {toplevel} that "
            f"git worktree list does not report. Inconsistent state."
        )
        # Also CONDITIONAL in the TSV, and also answerable here. Two different
        # situations produce the same sentence:
        #
        #  - cwd is in the project's OWN checkout, which `git worktree list`
        #    always reports first. Not being in the list means the registry
        #    disagrees with the disk, and reconciling that (prune? re-add? is
        #    this even the same repository?) is not something to attempt under
        #    a command that was only asked to print a path.
        #  - cwd is in some OTHER repository — a nested clone or a submodule
        #    under the project root. Nothing is broken; the agent is standing
        #    in the wrong tree.
        if toplevel == Path(root).resolve():
            raise agent_help.report(
                f"{inconsistent} Nothing was changed: the project's own "
                f"checkout is missing from its git worktree registry.",
                "how to reconcile a git worktree registry that disagrees with "
                "what is on disk for the project's own checkout",
                text=inconsistent,
            )
        raise agent_help.no_report(
            f"{inconsistent} It is a different repository nested under "
            f"{root}, not one of this project's worktrees.",
            f"cd to {root} or to a task worktree under it and re-run",
            text=inconsistent,
        )

    if as_json:
        click.echo(json.dumps(provenance.attach(match), indent=2))
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
        raise agent_help.no_report(
            "not inside an endless-managed worktree — run this from within a "
            "task worktree (.endless/worktrees/e-NNN)",
            "cd into the task worktree and re-run `endless worktree check`",
        )

    binary = shutil.which("endless-go")
    if not binary:
        # Installing Endless on the machine it is missing from is not something
        # an agent does inside somebody's project checkout.
        raise agent_help.report(
            "endless-go not found on PATH",
            "whether to install or repair the Endless install on this machine",
        )

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
    if result.stderr and result.returncode != 0:
        # endless-go's own refusal. Go classified it at the site that raised it
        # and its verdict lines already bracket the text, so relaying is the
        # whole job here: a second directive from this side would contradict
        # the first, and the exit code is the child's own.
        raise agent_help.relay(result.stderr, exit_code=result.returncode)
    if result.stderr:
        # Exit 0 with something on stderr is a progress line, not a refusal.
        agent_help.info(result.stderr.rstrip("\n"), err=True)
    # 0 clean, 1 anomalies (the stdout listing above IS the answer), 2 error.
    # Nothing left to say in Endless's voice, so the status travels alone.
    agent_help.passthrough_exit(result.returncode)


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
        #
        # Collapsed to one line: since E-2159 fixed which stream the probe's
        # detail comes from, an undetermined verdict carries endless-go's whole
        # refusal rather than the word `undetermined`, and this sweep prints one
        # line per worktree.
        return "skip", f"cannot tell whether it is in use ({' '.join(detail.split())})"

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
        raise agent_help.no_report(
            f"No worktree matches: {name_or_path}",
            "Re-check the name against `endless worktree list` and retry",
        )

    if as_json:
        click.echo(json.dumps(provenance.attach(target), indent=2))
        return

    click.echo(f"State:   {target['state']}")
    click.echo(f"Path:    {target['path']}")
    click.echo(f"Branch:  {target['branch'] or '(detached)'}")
    click.echo(f"HEAD:    {target['head']}")
    if target["companion"]:
        sc = target["companion"]
        click.echo(f"--- companion ---")
        click.echo(json.dumps(provenance.attach(sc), indent=2))
    if target["locked"]:
        click.echo(f"Locked:  {target.get('lock_reason') or 'yes'}")
    if target["prunable"]:
        click.echo(f"Prunable: {target.get('prunable_reason') or 'yes'}")


def for_task(task_id: str, as_json: bool) -> None:
    """Resolve a task ID (e.g. E-967 or 967) to its worktree path."""
    m = re.fullmatch(r"(?:[Ee]-)?(\d+)", task_id.strip())
    if m is None:
        raise agent_help.no_report(
            f"Invalid task id: {task_id}",
            "Pass E-NNN or NNN and retry",
        )
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
            click.echo(json.dumps(
                provenance.attach({"task_id": canonical, "worktree": None})))
        else:
            click.echo(f"No endless-managed worktree for {canonical}.")
        return

    if as_json:
        click.echo(json.dumps(provenance.attach({
            "task_id": canonical,
            "worktree": match["path"],
            "branch": match["branch"],
            "head": match["head"],
        })))
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

    Refuses rather than printing a path that does not exist. Since ED-1554
    every project's worktrees have a sandbox — `self_dev` no longer gates
    provisioning, only whether endless routes its OWN database there — so the
    only reason there is nothing to print is that the worktree has none yet.
    """
    from endless import config

    if task_id is None:
        wt_dir = config.worktree_path()
        if wt_dir is None:
            raise agent_help.no_report(
                "Not inside a task worktree, so there is no sandbox path to "
                "print. Nothing was changed.",
                "Re-run naming the task: `endless worktree sandbox E-<id>`",
                text=("Not inside a task worktree, so there is no sandbox to "
                      "resolve.\n"
                      "  Name the task instead:\n"
                      "      endless worktree sandbox E-<id>"),
            )
        canonical = _task_id_from_worktree_path(Path.cwd()) or wt_dir.name
    else:
        canonical = _normalize_task_id(task_id)
        root = _project_root()
        wt_dir = root / ".endless" / "worktrees" / f"e-{canonical[2:]}"
        if not wt_dir.is_dir():
            raise agent_help.no_report(
                f"No endless-managed worktree for {canonical}, so it has no "
                f"sandbox.",
                "Check `endless worktree list` and retry with an id that has "
                "a worktree",
            )

    path = config.sandbox_root(wt_dir)
    if not path.is_dir():
        remedy = "Recreate and seed it from the worktree with:  endless sandbox reset"
        # Mechanical and local. A sandbox is throwaway state by construction,
        # so recreating one destroys nothing anybody has to be consulted about.
        raise agent_help.no_report(
            f"{canonical}'s sandbox directory {path} does not exist, so no "
            f"path was printed.",
            "Run `endless sandbox reset` from the worktree to recreate and "
            "seed it",
            text=(f"{canonical}'s sandbox is missing:\n\n"
                  f"    {path}\n\n"
                  "A sandbox is created with its worktree, so something "
                  "removed it.\n"
                  f"{remedy}"),
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
    belong to an entirely different operation — see `_rebase_failure_refusal`.
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
    """Just the message. See _rebase_conflict_report, which does the work.

    Kept as its own name because the message is a thing on its own: what land
    puts in front of a reader when a rebase conflicts, which is what
    tests/test_worktree_land_conflict_msg.py is about and what E-2122 and
    E-1957 reshaped. The classification needs two more facts than a string can
    carry, so it reads them from the report.
    """
    msg, _ev, _captured = _rebase_conflict_report(
        worktree_path, base_branch, phase=phase, stderr=stderr,
    )
    return msg


def _rebase_conflict_report(
    worktree_path: Path, base_branch: str, *, phase: str,
    stderr: str | None = None,
) -> tuple[str, land_conflict.ConflictEvidence, bool]:
    """Capture a live rebase conflict, persist it, and build land's message.

    Returns (message, evidence, captured). The last two exist for E-2159: the
    refusal's CLASS turns on which of the three reports `_conflict_message`
    rendered, and that is not recoverable from the finished string.

    Only for a rebase that actually stopped on conflicting content. The caller
    establishes that (non-empty `--diff-filter=U`) before choosing this over the
    other reports in `_rebase_failure_refusal`; reaching here on a rebase that
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
    captured = stored is not None
    return (
        _conflict_message(ev, captured=captured, stderr=stderr),
        ev,
        captured,
    )


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


def _rebase_failure_refusal(
    worktree_path: Path, base_branch: str, *, phase: str,
    stderr: str | None, pre_existing: bool,
) -> agent_help.Refusal:
    """Report a non-zero `git rebase` as what it actually was (E-2122).

    Returns the refusal to RAISE, not a string. The three outcomes below are
    three different answers to "can the agent continue without asking?", and
    this is the only place that knows which one happened: by the time the
    caller has it, `git rebase --abort` has already run and the evidence it
    classified from is gone. The human's text is unchanged in each case.

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
    # For the verdict only — the human's text is built from `wt` as before.
    who = _task_id_from_worktree_path(worktree_path) or wt

    if pre_existing:
        # The TSV's 2026-09-18 decision for this row: name both branches. The
        # rebase in this worktree is not land's, and land will not touch it. If
        # the agent started it, continuing or aborting it is its own cleanup;
        # if somebody else did, `git rebase --abort` throws away a conflict
        # resolution in progress, which is not recoverable. Nothing here can
        # tell whose it is — a rebase leaves no author — so the reader holding
        # the conversation decides.
        return agent_help.report_if(
            f"A rebase was already in progress in {who}'s "
            f"worktree before land started, so land did not begin one and "
            f"nothing was changed.",
            "the agent did not start that rebase itself",
            "finish or abort it in the worktree, then retry the land",
            "aborting a rebase somebody else is in the middle of discards "
            "their conflict resolution",
            text=(f"cannot rebase: a rebase was already in progress in this "
                  f"worktree before land started, so land did not begin "
                  f"one.\n\n"
                  f"git said:\n{_git_said(stderr)}\n\n"
                  f"That rebase has been left exactly as it was — land does "
                  f"not abort an operation it did not start. Finish or abandon "
                  f"it yourself, then retry:\n"
                  f"  cd {wt}\n"
                  f"  git status                # see what it stopped on\n"
                  f"  git rebase --continue     # if you can resolve it\n"
                  f"  git rebase --abort        # to discard it\n"
                  f"then re-run: endless worktree land <id>\n"),
        )

    unmerged = _git_run(
        ["diff", "--name-only", "--diff-filter=U"],
        cwd=worktree_path, check=False,
    ).stdout
    if any(ln.strip() for ln in unmerged.splitlines()):
        msg, ev, captured = _rebase_conflict_report(
            worktree_path, base_branch, phase=phase, stderr=stderr,
        )
        target = ev.task_id or "<id>"
        files = ev.unmerged_paths
        if files and all(_is_auto_file(f) for f in files):
            # The one proven path. Endless wrote every one of these files and
            # none carries authored work, so restoring them from the base is
            # lossless by construction and the retry continues a land the user
            # already asked for.
            return agent_help.no_report(
                f"{target}'s rebase conflicts, and all {len(files)} "
                f"conflicting file(s) are endless-managed auto-files. The "
                f"rebase was aborted; the branch is exactly as it was.",
                f"Restore them from {ev.base_branch} with the printed git "
                f"checkout, then re-run the land",
                text=msg,
            )
        if not captured:
            # CONDITIONAL in the TSV, and it stays one: the capture failed to
            # reach disk, and whether that path can be made writable is a fact
            # about this machine — a directory the agent created, or a
            # permission or a full disk that it cannot do anything about.
            return agent_help.report_if(
                f"{target}'s rebase conflicts on source files AND the conflict "
                f"state could not be written, so `diagnose` has nothing to "
                f"read. The rebase was aborted; the branch is exactly as it "
                f"was.",
                "the capture path cannot be made writable from here — a "
                "permission or a full disk rather than a missing directory",
                "fix the path named below and re-run the land, which produces "
                "a diagnosable failure",
                "a source conflict whose evidence is lost cannot be classified "
                "later, and the recoveries that fit most conflicts can ship "
                "code that fails on first use",
                text=msg,
            )
        # Source conflict, recorded. NO-REPORT: the next step is one read-only
        # command, `endless worktree diagnose`, which is what decides whether
        # anything after it needs a person. Asking before running it would be
        # asking without the one piece of information that settles the question.
        return agent_help.no_report(
            f"{target}'s rebase conflicts on source files. The rebase was "
            f"aborted and the branch is exactly as it was, but the conflict "
            f"state WAS recorded first.",
            f"Run `endless worktree diagnose {target}` — its classification "
            f"decides the next step, and it prescribes one only when it can "
            f"prove it",
            text=msg,
        )

    # Not a conflict at all: git refused and said why. The TSV leaves this
    # inheriting git's class, and the inheritance is real — "fatal: invalid
    # upstream" is the agent's to fix, a repository git cannot read is not —
    # so both branches are named rather than one guessed. Nothing merged.
    return agent_help.report_if(
        f"The rebase of {who} failed while "
        f"{phase}, and NOT on a content conflict — no files are in conflict. "
        f"Nothing was merged.",
        "what git said below names something you cannot change — the "
        "repository, the machine, or another process's state",
        "act on what git said and retry the land",
        "a rebase that keeps failing for a reason outside this worktree will "
        "not be fixed by trying again",
        text=(f"rebase failed while {phase}.\n\n"
              f"This was NOT a content conflict — no files are in conflict, so "
              f"there is nothing to resolve. Git reported why:\n\n"
              f"git said:\n{_git_said(stderr)}\n\n"
              f"Act on what git said above. No recovery candidates are offered "
              f"here: the cause is stated, so there is nothing to guess "
              f"between.\n\n"
              f"Inspect:\n"
              f"  git -C {wt} status\n"
              f"  git -C {wt} log {base_branch}..HEAD\n"),
    )


#: The same function under the name land's call sites use. E-2122 named it for
#: what it returned — the message — and E-2159 changed that to the classified
#: refusal carrying the message, so the new name is the accurate one. The old
#: one stays live because it is what the call sites read as, and what
#: tests/test_worktree_land_lock_contention.py looks for in land's own source
#: to prove the contention check comes first: the thing that must not regress
#: is the ORDER of those two calls, and it is spelled out there under this name.
_rebase_failure_message = _rebase_failure_refusal


def _guard_modified_worktree(worktree_path: Path, branch: str, canonical: str) -> None:
    """Refuse land if the worktree's working tree has uncommitted files (E-1416).

    Step 1's partition runs on main; Step 4's rebase runs in the worktree.
    Without this guard, uncommitted files in the worktree make rebase abort
    with git's generic "You have unstaged changes" error — no file list,
    wrong recovery hint.

    Refuses separately for auto-managed modifications (an upstream writer bug
    worth surfacing rather than papering over) and unmanaged user modifications
    (offers worktree-specific recovery options).

    Raises CalledProcessError, rather than a ClickException, when its own `git
    status` lost the index lock (E-2174) — see the handler below.
    """
    try:
        wt_auto, wt_user = _git_status_partition(worktree_path)
    except subprocess.CalledProcessError as e:
        # E-2174: lock contention is not this guard's question to answer. It
        # says nothing about whether the worktree is modified — the status
        # never ran — so it propagates to land's retry loop, which waits the
        # holder out. Every other failure is still reported here.
        if _lock_contention_text(e) is not None:
            raise
        # git is a FOREIGN child: nothing upstream classified this, so the site
        # does, and git's own words ride along as detail — a git error quoted
        # verbatim is usually the whole diagnosis.
        #
        # Definite rather than report_if, and the reason is the branch just
        # above: lock contention — the one cause of a failing `git status` that
        # retrying fixes — has already been filtered out and sent to land's
        # retry loop. What reaches here is a worktree whose git plumbing does
        # not work, and repairing or recreating somebody's checkout is not a
        # land's business.
        raise agent_help.relay_foreign(
            agent_help.report(
                f"git status in the worktree for {canonical} failed, so land "
                f"could not tell whether it is modified. Nothing was landed.",
                "how to repair a worktree whose own `git status` fails",
                text="git status in worktree failed:",
            ),
            str(e.stderr or e),
        )
    if wt_auto:
        file_list = "\n  ".join(wt_auto[:20])
        more = "" if len(wt_auto) <= 20 else f"\n  ... and {len(wt_auto) - 20} more"
        # The message itself tells the reader to report this and forbids the
        # obvious fix, which is exactly what REPORT means: an Endless writer
        # failed to commit its own file, and whether those files are committed
        # or discarded is the user's call — along with hearing about the bug.
        raise agent_help.report(
            f"{len(wt_auto)} auto-managed file(s) are uncommitted in "
            f"{canonical}'s worktree, so an Endless writer skipped its own "
            f"commit. Nothing was landed.",
            "whether those auto-managed files are committed or discarded, and "
            "which writer bug produced them",
            text=(f"worktree for {canonical} has uncommitted auto-managed "
                  f"files; cannot land.\n\n"
                  f"Files:\n  {file_list}{more}\n\n"
                  f"These paths are owned by endless writers that commit them "
                  f"at write time. Their presence here means a writer is "
                  f"broken or skipped its commit. Report the writer that "
                  f"produced these files; do not auto-commit them manually."),
        )
    if wt_user:
        file_list = "\n  ".join(wt_user[:20])
        more = "" if len(wt_user) <= 20 else f"\n  ... and {len(wt_user) - 20} more"
        # The worktree is the agent's own, so uncommitted work in it is the
        # agent's own: committing it on the task branch is what it was going to
        # do anyway. Files of unknown provenance here would be the only reason
        # to ask, and a task worktree does not get those.
        raise agent_help.no_report(
            f"{len(wt_user)} uncommitted user file(s) in {canonical}'s "
            f"worktree block the rebase. Nothing was landed.",
            f"Commit them on {branch} (or move them aside, or revert them), "
            f"then retry the land",
            text=(f"worktree for {canonical} has uncommitted user changes; "
                  f"cannot land.\n\n"
                  f"Files:\n  {file_list}{more}\n\n"
                  f"Resolve from inside the worktree:\n"
                  f"  - commit on {branch} (most common)\n"
                  f"  - move the file aside (mv outside the worktree)\n"
                  f"  - revert if unwanted (git checkout -- <file>)\n"
                  f"then retry land."),
        )



# E-2184: how the pasteable half of a land-gate refusal is marked off. Fixed
# strings rather than a box: a person selects the lines between them with a
# mouse, and an agent reading the scrollback finds them with a search.
LAND_GATE_BLOCK_START = "──── paste this to the agent ────"
LAND_GATE_BLOCK_END = "──── end ────"


def land_gate_text(summary: str, block: str) -> str:
    """A land-gate refusal's text: one plain line, then the marked block (E-2184).

    ONE renderer for both halves of the gate — the built-in migration check and
    a project's pre-land hook both reduce to a one-line summary and a block, so
    a hook's refusal reads exactly like Endless's own. The verdict an agent
    reads at both ends is added by agent_help.Refusal, from `summary`.
    """
    if not block.strip():
        return summary
    return (
        f"{summary}\n\n{LAND_GATE_BLOCK_START}\n{block.rstrip()}\n"
        f"{LAND_GATE_BLOCK_END}"
    )


def land_gate_refusal(verdict: dict, canonical: str) -> agent_help.Refusal:
    """Classify a refused land-gate verdict (E-2159) by what refused.

    - A migration collision is the agent's to fix: every step is in the block.
      So is a rewritten base (E-2232): the fix is `git rebase` in the worktree.
    - A hook's veto may or may not be: only its own words say whether the fix
      is in the worktree or needs the user, so the agent is told to read them.
    - A hook that cannot run is fixed on main, which is the user's checkout.
    """
    summary = verdict.get("summary") or f"cannot land {canonical}: the land gate refused."
    text = land_gate_text(summary, verdict.get("block") or "")
    source = verdict.get("source")
    if source in ("migrations", "base_rewritten"):
        return agent_help.no_report(
            summary,
            "follow the steps between the markers in the worktree, then land again",
            text=text,
        )
    if source == "hook_not_executable":
        return agent_help.report(
            summary,
            "making the project's pre-land hook on main executable",
            text=text,
        )
    return agent_help.report_if(
        summary,
        "the hook's explanation does not name a fix you can make in the worktree",
        "make the fix it names and land again",
        "whether to land past the project's own pre-land rule",
        text=text,
    )


def _land_gate(
    main_root: Path, worktree_path: Path, base_branch: str, canonical: str,
) -> dict:
    """Ask `endless-go worktree land-gate` whether this land may proceed.

    E-2184. The check itself is Go (internal/landgate): a git diff over the
    project's declared migration directories, then a refusal for a branch
    holding copies of a rewritten base's commits (E-2242), then the project's
    optional `.endless/hooks/pre-land.sh`. Returns the verdict JSON.

    Runs the INSTALLED endless-go — main's build — even in self_dev, where the
    rest of the land uses the worktree's (E-1664). The gate is main's rule, like
    the config and hook it reads from main: a branch must not supply the code
    that judges it. And a worktree cut before this gate existed has a binary
    that does not know the verb, so asking it would refuse every such land.

    Fails CLOSED. A gate that could not run has not said yes, and the migration
    collision it exists to catch is silent in git and loud only after main has
    advanced. endless-go classifies its own failures (E-2159), so its stderr is
    relayed as-is; only a failure it said nothing about is classified here.

    Must run BEFORE anything rebases the branch onto base — Step 3.7 can, and
    Step 4 does. Afterwards the merge-base is base's tip, base has "gained"
    nothing, and the check passes the very collision it is for.
    """
    binary = shutil.which("endless-go")
    if not binary:
        raise agent_help.report(
            f"cannot land {canonical}: endless-go is not on PATH, so the land "
            f"gate (migration collisions, rewritten base, pre-land hook) cannot run. Nothing "
            f"was merged.",
            "installing endless-go",
        )
    r = subprocess.run(
        [binary, "worktree", "land-gate",
         "--project", str(main_root), "--worktree", str(worktree_path),
         "--base", base_branch, "--task", canonical],
        capture_output=True, text=True, check=False,
    )
    if r.returncode != 0 and r.stderr.strip():
        raise agent_help.relay(r.stderr, exit_code=r.returncode)
    try:
        verdict = json.loads(r.stdout) if r.returncode == 0 else None
    except ValueError:
        verdict = None
    if not isinstance(verdict, dict):
        raise agent_help.fault(
            f"cannot land {canonical}: the land gate could not decide, so "
            f"nothing was merged (endless-go exit {r.returncode}, no verdict).",
            detail=r.stdout.strip(),
        )
    return verdict


def _refuse_if_land_gated(
    main_root: Path, worktree_path: Path, base_branch: str, canonical: str,
) -> None:
    verdict = _land_gate(main_root, worktree_path, base_branch, canonical)
    if verdict.get("refused"):
        raise land_gate_refusal(verdict, canonical)


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
    forwarded so reaped-dir log lines reach the user.

    E-2159 changed what a non-zero exit becomes. It used to be a bare
    `CalledProcessError` from `check=True`: land caught it and printed its
    non-fatal warning, `task claim` swallowed it, and `endless worktree reap`
    let it escape as a Python traceback — the only one of the three that a
    person ever saw, and the least informative thing the helper could have
    produced. The sweep's stderr is now captured instead, so the failure can be
    relayed as what it is: endless-go's own refusal, classified by Go at the
    site that raised it, with its verdict already at both ends of the text.
    Nothing is added here, and the exit code is the child's.

    Stdout stays inherited, so anything the helper streams there is unchanged.
    """
    from endless import config

    binary = shutil.which("endless-go")
    if not binary:
        return
    # E-1429: thread the resolved --db context so this DB-opening subprocess
    # isn't refused by the self-dev-worktree gate when land runs from inside a
    # worktree. Empty (no flag) outside a gated worktree, so a no-op there.
    result = subprocess.run(
        [binary, *config.go_db_context_args(), "event", "reap-worktrees",
         "--project-root", str(project_root)],
        stderr=subprocess.PIPE, text=True,
    )
    if result.returncode != 0:
        raise agent_help.relay(result.stderr, exit_code=result.returncode)
    if result.stderr:
        # The reaped-dir log lines, which the helper writes to stderr by
        # design and which were always forwarded to the terminal. Classified
        # as a notice so this site still names a class, which is what keeps
        # the AST check enforceable without an exemption list.
        agent_help.info(result.stderr.rstrip("\n"), err=True)


def _warm_unlanded_cache(worktree_path: Path) -> None:
    """Record one worktree's unlanded verdict now (E-2128). Best-effort.

    Called at the two moments a worktree's branch tip moves under Endless's own
    hand, which are also the two moments somebody is about to look at the row:

    * **Creation.** `git worktree add -b <branch> <dir> <base>` puts the new
      branch AT the base, so it holds nothing the base lacks.
    * **Land.** The rebase-and-fast-forward leaves the branch tip equal to the
      base tip again, so the same thing is true.

    In both cases the answer is known without comparing anything, and the probe
    costs two or three cheap git calls — the comparison short-circuits on a branch
    contained in its base, never reaching `git range-diff`. Without this the row
    reads `~` (not yet determined) until the background job's next pass, for the
    one worktree whose state definitely just changed. `session resume --reopen`
    reusing an existing branch is the exception: its tip is wherever that branch
    was, so the full comparison runs once, which is the same cost
    `task unsettled <id>` pays and still better than showing `~`.

    It goes through `session-query worktree-unsettled` rather than writing the
    marker here on purpose: the cache layout is Go's, and a second writer in a
    second language is how two halves of one format drift apart.

    Never fatal — the worktree exists either way, and a cache is an optimization.
    """
    from endless import config

    binary = shutil.which("endless-go")
    if not binary:
        return
    subprocess.run(
        [binary, *config.go_db_context_args(),
         "session-query", "worktree-unsettled", str(worktree_path)],
        capture_output=True, check=False,
    )


def _normalize_task_id(task_id: str) -> str:
    m = re.fullmatch(r"(?:[Ee]-)?(\d+)", task_id.strip())
    if m is None:
        raise agent_help.no_report(
            f"Invalid task id: {task_id}",
            "Pass E-NNN or NNN and retry",
        )
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


# E-2159 retired `class DefaultBranchUnresolved(click.ClickException)`. It
# existed so a caller could catch "the default branch is unknown" apart from
# any other refusal, and nothing ever did: every call site let it reach the
# user. A refusal now names its class through the factory that built it, which
# is the distinction that was actually wanted, and a bare subclass could not
# make — it carried a message and no verdict.


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
            # The user wrote this branch name into their own project config.
            # An agent "fixing" an obvious typo would be choosing, silently,
            # which branch this project's work lands into — and the config is
            # the one place that choice is recorded on purpose.
            raise agent_help.report(
                f"{_tilde(project_root)}/.endless/config.json sets default_branch "
                f"{configured!r}, which does not exist in this repository.",
                "which branch is this project's default, since its own config "
                "names one that is not in the repository",
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

    unresolved = (
        f"Cannot resolve the default branch of {_tilde(project_root)}. "
        f"Set it explicitly: add \"default_branch\": \"<branch>\" to "
        f".endless/config.json, or run `git remote set-head origin --auto`."
    )
    # The TSV left this CONDITIONAL on something this function can simply look
    # up: whether the repository has an `origin` remote. With one, the second
    # half of the message is a command the agent can run — `set-head` asks
    # origin which branch it points at, which is a fact, not a choice — and the
    # retry then resolves. With no origin and no main/master, there is no fact
    # to recover: which branch this project's work lands into has never been
    # stated anywhere, and stating it is the user's.
    if _git_config_value(project_root, "remote.origin.url"):
        raise agent_help.no_report(
            f"{unresolved} Nothing was changed.",
            "Run `git remote set-head origin --auto` in the project root and "
            "retry",
            text=unresolved,
        )
    raise agent_help.report(
        f"{unresolved} Nothing was changed; the repository has no origin "
        f"remote and no main or master branch.",
        "which branch this project's work lands into, since nothing in the "
        "repository or its config says",
        text=unresolved,
    )


def _check_plan_file_committed(task_id: int, project_root: Path) -> str | None:
    """If the task's plan mirror exists but is modified/untracked in main,
    return an error message with recommended commands. Otherwise None.

    Per ED-1169, refuse with recommendations rather than auto-commit: an
    uncommitted mirror in main's working tree is a HUMAN's edit, and
    auto-committing it would hide their intent.

    Since E-2137 the mirror is written and committed on main at write time, so
    reaching this means something bypassed that — a hand-edit, or a commit that
    failed and warned. Both are worth stopping for, and both are exactly what
    the recommendation resolves. Checks the legacy path too, since a tree the
    `doc-mirrors` sweep has not reached yet still has its mirrors there.
    """
    kind = doc_mirror.kind_for("plan")
    candidates = [
        doc_mirror.task_doc_path(task_id, kind.stem),
        doc_mirror.legacy_task_doc_path(kind, task_id),
    ]
    for plan_rel in candidates:
        if not (project_root / plan_rel).exists():
            continue
        res = _git_run(
            ["status", "--porcelain", "--", plan_rel],
            cwd=project_root, check=False,
        )
        if res.returncode != 0 or not res.stdout.strip():
            continue
        root_display = _tilde(project_root)
        return (
            f"Plan mirror {plan_rel} is uncommitted in main; it will not "
            f"appear in the new worktree.\n\n"
            f"Capture it before starting the task. Recommended:\n"
            f"  git -C {root_display} add {plan_rel}\n"
            f"  git -C {root_display} commit -m 'Add plan for E-{task_id}'\n"
            f"\nThen retry: endless task claim E-{task_id}"
        )
    return None


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
        #
        # git is foreign here, so the site classifies and git's words come
        # along as detail. REPORT because of what the refusal is protecting:
        # the next step after a successful compare is deleting the branch, and
        # the compare is the only thing that proves the branch holds nothing.
        # Without it there is no safe retry, only a guess about somebody's
        # commits.
        raise agent_help.relay_foreign(
            agent_help.report(
                f"git could not compare {branch} to {base}, so the orphan "
                f"branch was left in place and the claim stopped.",
                "whether the orphan branch may be deleted without anything "
                "having established what it holds",
                text=f"Could not compare {branch} to {base}:",
            ),
            res.stderr or res.stdout,
        )
    return [ln for ln in res.stdout.splitlines() if ln.strip()]


def _read_branch_file(branch: str, rel_path: str, project_root: Path) -> str | None:
    res = _git_run(
        ["show", f"{branch}:{rel_path}"], cwd=project_root, check=False,
    )
    return res.stdout if res.returncode == 0 else None


def doc_mirror_content(rel_path: str) -> str | None:
    """The authoritative content behind a document mirror path, from the DB.

    Delegates to the `endless-go session-query doc-content` Go helper, which
    resolves the path to the row and column that own it. Python SQLite reads are
    forbidden (E-894), so there is no DB fallback.

    Returns None — distinct from "" — when the answer is UNKNOWN: the helper is
    missing, the path is not a mirror, or the read failed. A caller deciding
    whether a branch's copy may be discarded must be able to tell "the database
    says this file should be empty" from "I could not ask", because only the
    first is a reason to discard anything.
    """
    from endless import config

    binary = shutil.which("endless-go")
    if not binary:
        return None
    try:
        result = subprocess.run(
            [binary, *config.go_db_context_args(),
             "session-query", "doc-content", "--path", rel_path],
            capture_output=True, text=True,
        )
    except OSError:
        return None
    return result.stdout if result.returncode == 0 else None


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
    # Not relay_foreign: git's own stderr is already inside the message, between
    # the facts and the remedy, and attaching it again as detail would print it
    # twice and move it after the commands it is supposed to explain.
    #
    # REPORT, and the TSV's condition resolves itself: the two commands the
    # message prints are the two this function has just run — `worktree prune`,
    # then `branch -D` again — so the agent-fixable branch has already been
    # taken and failed. What is left is a live worktree still holding the branch
    # or a permissions problem, neither of which a retry reaches.
    #
    # The printed commands stay in the message rather than moving to
    # `human_remedy`: this branch was proven to hold no unique work before
    # anything tried to delete it (see _handle_orphan_branch), so `branch -D`
    # here destroys nothing — it is the remedy, not a bypass of a safety check.
    raise agent_help.report(
        f"git refused to delete orphan branch {branch} even after a worktree "
        f"prune, so the claim stopped and nothing was created.",
        "how to clear an orphan branch git will not delete — a live worktree "
        "still holding it, or the permissions on the repository",
        text=(f"Could not delete orphan branch {branch}:\n{err}\n"
              f"Resolve manually, then retry:\n"
              f"  git -C {root} worktree prune\n"
              f"  git -C {root} branch -D {branch}"),
    )


def _orphan_real_work_msg(
    task_id: int, branch: str, base: str, real_work: list[str], project_root: Path,
) -> tuple[str, str]:
    """The refusal's (text, human_remedy).

    The discard command is the human remedy (E-2213): the branch holds commits
    nothing else has a copy of, so `branch -D` is irreversible loss, and a
    refusal that names it is one an agent will take.
    """
    root = _tilde(project_root)
    shown = "\n  ".join(real_work[:20])
    more = "" if len(real_work) <= 20 else f"\n  ... and {len(real_work) - 20} more"
    return (
        f"E-{task_id}: branch {branch} has commits beyond {base} touching "
        f"files no document mirror accounts for:\n  {shown}{more}\n\n"
        f"Inspect:\n"
        f"  git -C {root} log {base}..{branch}\n"
        f"  git -C {root} diff {base}...{branch}"
    ), (
        f"\nResume that work manually, or discard it and retry:\n"
        f"  git -C {root} branch -D {branch}"
    )


def _orphan_mirror_mismatch_msg(
    task_id: int, branch: str, mismatched: list[str], project_root: Path,
) -> tuple[str, str]:
    """The refusal's (text, human_remedy).

    The discard command is the human remedy (E-2213): the branch is the only
    place the differing text exists, so `branch -D` erases it.
    """
    root = _tilde(project_root)
    rel = mismatched[0]
    more = (
        "" if len(mismatched) == 1
        else "\n  " + "\n  ".join(mismatched[1:])
    )
    return (
        f"E-{task_id}: branch {branch} holds document mirrors whose content the "
        f"database does not have:\n  {rel}{more}\n\n"
        f"A mirror is a projection of a task's row, so ordinarily discarding the "
        f"branch loses nothing. These differ, which means the branch is the only "
        f"place that text exists — and which side is right is not this command's "
        f"to guess.\n\n"
        f"Read the branch's copy:\n"
        f"  git -C {root} show {branch}:{rel}\n"
        f"Adopt it into the database, then retry:\n"
        f"  git -C {root} show {branch}:{rel} > .endless/tmp/E-{task_id}.md\n"
        f"  endless task update E-{task_id} --plan-file .endless/tmp/E-{task_id}.md\n"
        f"    (--analysis-file / --outcome-file for those kinds)"
    ), (
        f"\nOr keep the database's version and discard the branch:\n"
        f"  git -C {root} branch -D {branch}"
    )


def _orphan_unreadable_mirror_msg(
    task_id: int, branch: str, unreadable: list[str], project_root: Path,
) -> str:
    root = _tilde(project_root)
    shown = "\n  ".join(unreadable[:10])
    return (
        f"E-{task_id}: branch {branch} holds document mirrors, and the database "
        f"could not be asked what they should contain:\n  {shown}\n\n"
        f"Deleting the branch would be safe only if the database already has "
        f"this content, which is exactly what could not be checked. Refusing "
        f"rather than guessing.\n\n"
        f"Inspect, then retry:\n"
        f"  git -C {root} show {branch}:{unreadable[0]}"
    )


def _check_orphan_mirrors(
    task_id: int, branch: str, mirrors: list[str], project_root: Path,
) -> None:
    """A mirror-only orphan branch. Decide whether it may be discarded.

    The database is the source of truth and a mirror is derived from it, so a
    branch whose only unique content is mirrors matching their columns carries
    nothing: return, and the caller deletes it.

    Raises when a mirror's committed content DIFFERS from its column, or when
    the column could not be read at all. E-1500's guarantee is that a claim can
    never silently strand a branch holding something the database lacks, and
    that guarantee does not weaken just because the thing is a file Endless
    wrote.

    This replaces `_reconcile_orphan_plan` (E-2137). That version knew only
    about plans, carried a character-count heuristic for deciding whether a plan
    was "viable", and would silently ADOPT a branch's plan when the column was
    empty. All three were shaped by mirrors living on branches; once they do
    not, a mismatch is rare enough that reporting it and letting a person choose
    beats any rule this code could apply.
    """
    mismatched: list[str] = []
    unreadable: list[str] = []
    for rel in mirrors:
        db_text = doc_mirror_content(rel)
        if db_text is None:
            unreadable.append(rel)
            continue
        branch_text = _read_branch_file(branch, rel, project_root) or ""
        if branch_text.strip() != db_text.strip():
            mismatched.append(rel)

    if mismatched:
        # Which of two texts governs the task is a judgment about intent, and
        # either answer throws the other away. The TSV left the old plan-only
        # version of this CONDITIONAL on "is one plainly a stale prefix of the
        # other"; that heuristic is what E-2137 removed along with the
        # character-count viability rule, and nothing replaced it because
        # nothing could. Definite REPORT.
        #
        # The closing `git branch -D` is a destructive escape, so it is the
        # human remedy: a person reads it where it always was, and an agent is
        # never offered it (E-2213).
        text, human_remedy = _orphan_mirror_mismatch_msg(
            task_id, branch, mismatched, project_root,
        )
        raise agent_help.report(
            f"E-{task_id}: {len(mismatched)} document mirror(s) on branch "
            f"{branch} differ from the database's copy. The claim stopped; "
            f"nothing was created or deleted.",
            "which copy of the task's documents governs — adopting the "
            "branch's or keeping the database's discards the other",
            text=text,
            human_remedy=human_remedy,
        )
    if unreadable:
        # Not "the database says discard this" — "the database could not be
        # asked". The one thing that would make deleting the branch safe is the
        # thing that failed, so the refusal cannot resolve into a retry: whether
        # to accept the risk is the user's.
        raise agent_help.report(
            f"E-{task_id}: the database could not be asked what "
            f"{len(unreadable)} document mirror(s) on branch {branch} should "
            f"contain, so the branch was left alone and the claim stopped.",
            "whether the branch's mirrors may be discarded without anything "
            "having confirmed the database already holds their content",
            text=_orphan_unreadable_mirror_msg(
                task_id, branch, unreadable, project_root,
            ),
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

    "Real work" is everything that is not a document mirror. The exclusion
    covers every mirror kind and both layouts (E-2137) rather than the single
    legacy plan path it started as: a branch cut before mirrors
    became main-bound may hold a plan, an analysis, an outcome and a decision
    body, at either the consolidated or the legacy path, and none of them is
    work a person did here.
    """
    unique = _branch_unique_files(base, branch, project_root)
    mirrors = [f for f in unique if doc_mirror.is_mirror_path(f)]
    real_work = [f for f in unique if not doc_mirror.is_mirror_path(f)]
    if real_work:
        # Commits nothing else holds. Keep-or-discard is the user's, and the
        # discard half is irreversible, which is why the closing `git branch -D`
        # is the human remedy and never reaches an agent (E-2213).
        text, human_remedy = _orphan_real_work_msg(
            task_id, branch, base, real_work, project_root,
        )
        raise agent_help.report(
            f"E-{task_id}: orphan branch {branch} holds {len(real_work)} "
            f"file(s) of real work beyond {base}. No worktree was created and "
            f"the branch was left exactly as it was.",
            "whether to resume the prior work on that branch or discard it "
            "permanently",
            text=text,
            human_remedy=human_remedy,
        )
    if mirrors:
        _check_orphan_mirrors(task_id, branch, mirrors, project_root)
    # Empty delta, or a mirror-only delta that checked out clean -> safe.
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
        # A directory Endless did not create is sitting on the path this task's
        # worktree needs. Moving or removing somebody else's directory is not
        # an agent's act, whatever is in it.
        raise agent_help.report(
            f"Path {_tilde(wt_dir)} exists but does not belong to {canonical}. "
            f"Resolve manually before retrying.",
            "what to do with the foreign directory occupying the task's "
            "worktree path",
        )

    msg = _check_plan_file_committed(task_id, project_root)
    if msg:
        # NO-REPORT: the mirror is Endless's own file, uncommitted in main, and
        # the message prints the two git commands that capture it. Committing a
        # plan mirror on main is what `task update` does at write time anyway,
        # so the retry is the ordinary path rather than a decision.
        raise agent_help.no_report(
            f"E-{task_id}'s plan mirror is uncommitted in main, so the "
            f"worktree was not created.",
            "Run the printed git add/commit on the main checkout, then retry "
            f"`endless task claim E-{task_id}`",
            text=msg,
        )

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
        # git is foreign; the site classifies and git's words ride as detail.
        #
        # NO-REPORT, because everything this command can collide with has
        # already been cleared above: a foreign directory on the path and an
        # orphan branch holding the name each have their own refusal, and both
        # are REPORT. What is left for `worktree add` to fail on is a stale
        # registration, a missing base ref, a path the agent chose — things git
        # names precisely and the agent acts on. Nothing was created.
        raise agent_help.relay_foreign(
            agent_help.no_report(
                f"git worktree add failed for {canonical}; no worktree was "
                f"created and nothing was changed.",
                "Act on what git said below, then retry the claim",
                text=f"git worktree add failed for {canonical}:",
            ),
            str(e.stderr or e),
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
    creates the worktree's sandbox, runs the project's post-worktree-create
    hook (go-work-init, bin copy, claude-settings-init, ...), then seeds the
    sandbox with `endless sandbox reset` (E-1608).

    It materializes NO document mirrors (E-2137). Those live on main, where
    `task update` writes them; a worktree copy would be a second home for
    content the database owns, and the agent working here would be the one most
    likely to edit it. It performs NO status transition: `task claim`
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
    provision_worktree_sandbox(wt_dir)
    # E-2128: the branch was just cut at the base, so its unlanded verdict is
    # known without comparing anything. Recording it here means the row this
    # session is about to look at reads a verdict rather than `~`. Before the
    # post-create hook, which on some projects takes seconds.
    try:
        _warm_unlanded_cache(wt_dir)
    except Exception:
        # Deliberately silent: a cold cache entry costs one monitor tick showing
        # `~`, and saying so would be noise on a worktree that was created fine.
        pass
    _run_post_worktree_create_hook(project_root, wt_dir)
    # E-1608: seed through the one front door, after the create hook (which
    # may build the binary the seed hook uses).
    from endless.sandbox_cmd import reset_after_create
    reset_after_create(wt_dir)


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
        # Same refusal as create_task_worktree's, for the same reason: a
        # directory Endless did not create is in the way, and clearing it is
        # the user's act.
        raise agent_help.report(
            f"Path {_tilde(wt_dir)} exists but does not belong to {canonical}. "
            f"Resolve manually before retrying.",
            "what to do with the foreign directory occupying the task's "
            "worktree path",
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
        # As in create_task_worktree: the collisions that need a person are
        # refused above, so what reaches here is git naming something the agent
        # can act on, and the recovery is to act on it and retry the resume.
        raise agent_help.relay_foreign(
            agent_help.no_report(
                f"git worktree add failed for {canonical}; the worktree was "
                f"not recreated and nothing was changed.",
                "Act on what git said below, then retry the resume",
                text=f"git worktree add failed for {canonical}:",
            ),
            str(e.stderr or e),
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
        # Nothing is blocked: the worktree exists and is usable, only its
        # project-specific bootstrap did not run. The remedy deliberately does
        # NOT say chmod — that edits a tracked file in the user's main
        # checkout, a project change nobody asked for — it says run the script
        # through its interpreter, which finishes the bootstrap and changes
        # nothing.
        agent_help.warn.no_report(
            f"post-worktree-create hook {_tilde(hook)} is not executable, so "
            f"the worktree was created without running it.",
            f"Run it through its interpreter in the new worktree "
            f"(`sh {_tilde(hook)} {_tilde(worktree_path)}`) to finish bootstrap",
            text=(
                click.style("⚠ post-worktree-create hook is not executable",
                            fg="yellow")
                + f"\n    {_tilde(hook)}\n"
                f"    Make it executable and re-run:\n"
                f"        chmod +x {_tilde(hook)}\n"
                f"        cd {_tilde(worktree_path)} && {_tilde(hook)} {_tilde(worktree_path)}"
            ),
        )
        # ...and recorded, because only the user can clear it: the hook is
        # tracked on main, and the agent was told above to leave its mode alone
        # (E-2213). Fingerprinted on the hook, so every worktree created before
        # it is fixed is one incident.
        agent_help.warn.record(
            "WARN-0026",
            f"post-worktree-create hook {_tilde(hook)} is not executable; "
            f"{_tilde(worktree_path)} was created without running it",
            source="worktree:post-create-hook",
            fingerprint=f"hook={hook}",
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
        # The TSV left this CONDITIONAL on "can the agent run the hook without
        # changing it". The branch above already answered it: the file IS
        # executable (os.X_OK passed), so the exec itself failing means the
        # shebang names an interpreter that is not there, or the file is not in
        # a format this kernel can run. No invocation of a script in that state
        # succeeds, so there is no retry to hand the agent — the project's
        # bootstrap script is the user's to fix.
        agent_help.warn.report(
            f"post-worktree-create hook {_tilde(hook)} could not be executed "
            f"at all ({e}); the worktree was created and kept, unbootstrapped.",
            "how to repair the project's bootstrap script, which is executable "
            "but cannot be run (usually its shebang's interpreter)",
            text=(
                click.style("⚠ post-worktree-create hook failed to start",
                            fg="yellow")
                + f"\n    {_tilde(hook)}: {e}\n"
                f"    Worktree kept. Re-run after fixing:\n"
                f"        cd {_tilde(worktree_path)} && {_tilde(hook)} {_tilde(worktree_path)}"
            ),
        )
        return
    if result.returncode != 0:
        # The hook's own stdout/stderr streamed live just above, so its
        # diagnosis is already in front of the reader and there is nothing to
        # relay — only to classify. NO-REPORT: the hook contract REQUIRES
        # idempotency, the worktree was kept, and re-running it is the whole
        # recovery. A bootstrap that fails the same way twice stops being this
        # warning's problem — whatever it was supposed to install will refuse
        # on its own, with its own class.
        agent_help.warn.no_report(
            f"post-worktree-create hook {_tilde(hook)} exited "
            f"{result.returncode}; the worktree was created and KEPT, but its "
            f"bootstrap did not finish.",
            f"Re-run the hook in the worktree (`cd {_tilde(worktree_path)} && "
            f"{_tilde(hook)} {_tilde(worktree_path)}`) — it is required to be "
            f"idempotent",
            text=(
                click.style(
                    f"⚠ post-worktree-create hook exited {result.returncode}",
                    fg="yellow",
                )
                + f"\n    script:   {_tilde(hook)}\n"
                f"    worktree: {_tilde(worktree_path)}\n"
                f"    The worktree was KEPT. The hook must be idempotent/re-runnable;\n"
                f"    finish bootstrap by re-running it:\n"
                f"        cd {_tilde(worktree_path)} && {_tilde(hook)} {_tilde(worktree_path)}"
            ),
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
        # The land SUCCEEDED; only this one-time step did not run. As with the
        # create hook, the remedy runs the script through its interpreter
        # rather than chmod'ing it: the file is now on main, so making it
        # executable for good needs another commit landed there.
        agent_help.warn.no_report(
            f"post-land script {_tilde(script)} is not executable, so it was "
            f"skipped. The land itself succeeded.",
            f"Run it through its interpreter on the main checkout "
            f"(`sh {_tilde(script)} {_tilde(main_root)}`)",
            text=(
                click.style("⚠ post-land script is not executable", fg="yellow")
                + f"\n    {_tilde(script)}\n"
                f"    The land succeeded, but this step was skipped.\n"
                f"    Make it executable and re-run:\n"
                f"        chmod +x {_tilde(script)}\n"
                f"        {rerun}"
            ),
        )
        # ...and recorded: the step the script carries has still not happened,
        # and a land is often driven by an agent whose reply scrolls away
        # (E-2213).
        agent_help.warn.record(
            "WARN-0027",
            f"{canonical}'s post-land script {_tilde(script)} is not "
            f"executable, so it was skipped; the land itself succeeded",
            source="worktree:post-land",
            fingerprint=f"script={script}",
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
        # Same resolution as the create hook's: the file passed os.X_OK, so a
        # failing exec means the script cannot be run in the state it is in and
        # no re-run reaches it. And it is now ON main, so fixing it means
        # landing another change there — the user's act either way.
        agent_help.warn.report(
            f"post-land script {_tilde(script)} could not be executed at all "
            f"({e}); the land succeeded and main is advanced, but this step "
            f"did not run.",
            "how to repair a post-land script that is already on main and "
            "cannot be executed — fixing it means landing another change",
            text=(
                click.style("⚠ post-land script failed to start", fg="yellow")
                + f"\n    {_tilde(script)}: {e}\n"
                f"    Main already advanced; the land succeeded. Re-run after fixing:\n"
                f"        {rerun}"
            ),
        )
        return
    if result.returncode != 0:
        # The script's own output streamed live above, so there is nothing to
        # relay. NO-REPORT for the create hook's reason: the contract requires
        # the script be idempotent, the land stands either way, and re-running
        # it is the documented recovery.
        agent_help.warn.no_report(
            f"post-land script {_tilde(script)} exited {result.returncode}; "
            f"the land SUCCEEDED and main is advanced, but this step did not "
            f"complete.",
            f"Re-run the script (`{rerun}`) — it is required to be idempotent",
            text=(
                click.style(
                    f"⚠ post-land script exited {result.returncode}", fg="yellow"
                )
                + f"\n    script: {_tilde(script)}\n"
                f"    cwd:    {_tilde(main_root)}\n"
                f"    The land SUCCEEDED (main was already advanced); this step did not.\n"
                f"    The script must be idempotent/re-runnable; finish by re-running it:\n"
                f"        {rerun}"
            ),
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
    # The land SUCCEEDED and main is advanced — this exits non-zero only so
    # automation notices. REPORT because of what the residue is: files that
    # were ignored and present on the USER's main checkout a moment ago, which
    # means they may be their own local data. The message's own "or remove the
    # files manually" is not an instruction an agent may take, and deleting
    # untracked files in somebody's checkout is not recoverable.
    raise agent_help.report(
        f"Landed {canonical} — main IS advanced — but the land un-ignored "
        f"{len(residue)} {noun} now sitting on main as untracked residue.",
        "whether to delete, track or re-ignore those untracked files on main; "
        "they were ignored local files and may be the user's own data",
        text=(
            f"Landed {canonical}: main was advanced, but the land un-ignored "
            f"{len(residue)} {noun} left as untracked residue on main:\n\n"
            f"  {listing}\n\n"
            f"{script_note}\n\n"
            f"    A later land could sweep this residue into a commit. The "
            f"land itself SUCCEEDED and cannot be unwound; this check is "
            f"non-fatal but exits non-zero so automation notices."
        ),
    )


# E-1747: the multiline document fields that mirror to committed
# .endless/<subdir>/E-NNN.md files. Each tuple is (tasks column, subdir,
# human label used in the commit subject and progress line). `plan` is the
# original plan mirror (E-1445); `outcome`/`analysis` are added here. Short
# metadata (description, title) is deliberately excluded — not documents.
# E-2137 retired five functions that lived here: `_TASK_DOC_FIELDS`,
# `_materialize_task_docs`, `_materialize_task_doc`, `_materialize_plan_file`,
# `_commit_doc_in_worktree` and `_commit_plan_file_in_worktree`.
#
# Together they seeded a newborn worktree with committed copies of the task's
# plan, outcome and analysis, and re-committed each one on the branch every time
# `task update` rewrote it. Those commits were the single largest reason the
# fleet read as holding unlanded work: 123 of 139 genuinely-unlanded commits
# across 133 worktrees, and 44 of 56 worktrees unlanded ONLY because of them.
#
# Nothing in Endless read the worktree copy. `task update` wrote it and
# `task show` renders from the database, so it existed for browsing — which
# main satisfies, now that every mirror is written there at write time.
#
# The worktree copy did do one thing reliably: invite an agent to hand-edit
# content the database owns, the same failure `.endless/LESSONS.md` needed a
# CLAUDE.md rule to prevent. A file that is not there cannot be hand-edited.
#
# The tempting middle option — write the worktree copy but leave it
# uncommitted — is exactly what E-1525 removed. It makes every worktree
# permanently dirty, which trips land's modified-worktree guard and pushes
# another hundred worktrees into reading as modified.


# Content endless writes at the root of every sandbox it creates. Mirrors
# sandboxcmd.SandboxGitignore on the Go side; the two must stay identical, or a
# worktree provisioned by one and re-provisioned by the other shows a diff.
#
# The sandbox self-ignores rather than being listed in the project's own
# .gitignore, and that is what makes adoption free: `*` ignores every path in
# this directory including this file, so git sees the directory as empty and
# never reports it. A rule in the project's .gitignore would be endless editing
# a file in a repo that is not its own, once per project, forever.
_SANDBOX_GITIGNORE = """\
# Endless per-worktree sandbox: isolated state this worktree's task is
# exercised against, with a lifetime exactly equal to this worktree's.
# Self-ignoring — '*' covers every path here, this file included — so the
# project's own .gitignore needs no entry for it.
*
"""


def provision_worktree_sandbox(worktree_path: Path) -> Path:
    """Create this worktree's sandbox: an EMPTY directory that self-ignores.

    Every project, not just endless (ED-1554). A sandbox is where a worktree
    keeps the state its task is exercised against — a throwaway database, a
    fixture spreadsheet, an API document, credentials that must not be the real
    ones — and essentially no real project has none of that. The `self_dev`
    flag that used to gate this now gates only whether endless routes its OWN
    database here.

    Empty, and seeded with nothing. Endless cannot know which of a checkout's
    files a task needs, and copying them in is the exact failure a sandbox
    exists to prevent: a worktree quietly pointed at the real database or a live
    account. What goes in is the project's declaration, made in its own
    seed-sandbox hook, which `endless sandbox reset` runs once the worktree's
    bootstrap is done (E-1608).

    Pure Python, no shellout. Provisioning is now on the path of every worktree
    for every project, so it must not depend on a Go binary being installed and
    findable — the old `endless-go sandbox init && sandbox bind` pair degraded
    to a warning and no sandbox when it was not.

    Returns the sandbox directory, and is idempotent: an existing sandbox keeps
    its contents.
    """
    from endless import config

    sandbox = config.sandbox_root(worktree_path)
    sandbox.mkdir(parents=True, exist_ok=True)
    gitignore = sandbox / ".gitignore"
    if not gitignore.exists() or gitignore.read_text() != _SANDBOX_GITIGNORE:
        gitignore.write_text(_SANDBOX_GITIGNORE)
    # Silent on success (E-1428). This used to print the sandbox path as a
    # bullet directly above the worktree path, and readers scanning a claim for
    # somewhere to cd took the first path they saw — landing somewhere that is
    # not a project, so every endless command after it failed with "Not in a
    # registered project directory". `endless worktree sandbox` prints it on
    # demand for the rare case (pointing a SQL client at a worktree's database)
    # that wants it.
    return sandbox


def _resolve_land_endless_go(worktree_path: Path, project_root: Path) -> str | None:
    """The endless-go a self_dev land records its landing with, or None.

    Since ED-1601 that is the MAIN checkout's `bin/endless-go` — the installed
    binary — and never the worktree's. A worktree build never opens the main
    database, so the E-1664 arrangement (record with the worktree's binary,
    because only it matched the rows the land just migrated) is refused outright
    now. Step 5.2 builds this binary from the just-advanced main and Step 5.6
    swaps it in, so by Step 6 it is built from exactly main + this branch and
    matches the database Step 5.5 migrated.

    Resolved BEFORE the ff-merge so a land that cannot rebuild it — no `just` on
    PATH — aborts while main and the database are untouched.

    Returns None for a non-self_dev project, where the global is correct and
    nothing is built (downstream users never build endless-go).
    """
    from endless import config

    if not config.project_is_self_dev(project_root):
        return None
    if shutil.which("just") is None:
        canonical = _task_id_from_worktree_path(worktree_path) or str(worktree_path)
        # Installing a build tool on the machine is the user's. Resolved before
        # the ff-merge, so nothing has moved.
        raise agent_help.report(
            f"cannot land {canonical}: `just` is not on PATH. Nothing was "
            f"merged or migrated.",
            "whether to install `just`, which this machine needs before a "
            "self-dev land can rebuild the binary it records with",
            text=(f"cannot land {canonical}: `just` is not on PATH, so the "
                  f"main checkout's endless-go cannot be rebuilt after the "
                  f"merge. The land records itself with that binary, and it "
                  f"has to match the database the land migrates."),
        )
    return str(project_root / "bin" / "endless-go")


def _build_main_binary_next(main_root: Path, canonical: str, base_branch: str) -> None:
    """Build the main checkout's endless-go — the installed binary — from the
    just-advanced main, to `bin/endless-go.next` (Step 5.2, E-2205).

    Runs AFTER Step 5's ff-merge and BEFORE Step 5.5 migrates the database. The
    compile is the slow part of replacing the binary, and it must not sit
    between the migration and the swap: until E-2205 it did, and every reader on
    the machine that connected through the still-old binary in those seconds —
    the tmux status line polls about once a second — met a database ahead of it
    and recorded ERR-0020 for a condition the land itself had caused. Built
    first, the binary goes live at Step 5.6 with a rename, and the window
    between the migration committing and the new binary answering is one
    syscall.

    It is built to the side, not over bin/endless-go: a new binary meeting the
    still-behind database would migrate it on its next connect, racing the
    land's own backup-then-migrate.

    A failure is post-merge but PRE-migration — main advanced, the database
    untouched, nothing recorded — and is reported as re-runnable: the re-run's
    ff-merge is a no-op, and it builds, migrates and records.
    """
    from endless import config
    if not config.project_is_self_dev(main_root):
        return
    result = subprocess.run(
        ["just", "go-build-next"], cwd=str(main_root),
        capture_output=True, text=True,
    )
    if result.returncode != 0:
        # `just go-build-next` is a foreign child, but its compiler output is
        # already inside the message where it belongs — between the facts and
        # the recovery — so attaching it again as detail would print it twice.
        #
        # NO-REPORT: the build is of code that is now ON main, the message
        # names the exact re-run, and the one step it repeats (the ff-merge) is
        # a no-op. Nothing is lost and nothing needs deciding.
        raise agent_help.no_report(
            f"Landed {canonical} into {base_branch}, but building the "
            f"installed endless-go failed, so the database was NOT migrated "
            f"and the landing is NOT recorded yet.",
            f"Fix the build and re-run `just land {canonical}` — the ff-merge "
            f"is a no-op on the re-run, which builds, migrates and records",
            text=(f"Landed {canonical} into {base_branch}, but building the "
                  f"installed endless-go failed:\n\n"
                  f"{(result.stderr or result.stdout).strip()}\n\n"
                  f"Neither the database migration nor the landing record has "
                  f"happened. Fix the build and re-run `just land {canonical}`: "
                  f"the ff-merge is a no-op on the re-run, which builds, "
                  f"migrates and records."),
        )


def _swap_main_binary(main_root: Path, canonical: str, base_branch: str) -> None:
    """Rename the binary Step 5.2 built over the main checkout's endless-go
    (Step 5.6, E-2205).

    Runs immediately after Step 5.5's migration commits and before Step 6
    records the landing with this binary. A rename within one directory is
    atomic, so every reader sees the old binary or the new one, never neither.

    It is the same rename `just go-swap` performs, done in-process: spawning
    `just` costs tens of milliseconds, and every one of them is time the old
    binary spends in front of a migrated database.

    A failure is post-migration — main advanced, the database migrated, the old
    binary still installed and older than the database — and is reported as
    re-runnable: the re-run builds again, finds the database current, swaps and
    records.
    """
    from endless import config
    if not config.project_is_self_dev(main_root):
        return
    bin_dir = main_root / "bin"
    try:
        os.replace(bin_dir / "endless-go.next", bin_dir / "endless-go")
    except OSError as e:
        # NO-REPORT: as for the build — the message names the exact re-run and
        # every step it repeats is idempotent.
        raise agent_help.no_report(
            f"Landed {canonical} into {base_branch} and migrated the database, "
            f"but installing the rebuilt endless-go failed, so the landing is "
            f"NOT recorded yet.",
            f"Fix the cause and re-run `just land {canonical}` — the ff-merge "
            f"and the migration are idempotent, so the re-run rebuilds, swaps "
            f"and records",
            text=(f"Landed {canonical} into {base_branch} and migrated the "
                  f"database, but installing the rebuilt endless-go failed:"
                  f"\n\n{e}\n\n"
                  f"The landing is not recorded yet. Fix the cause and re-run "
                  f"`just land {canonical}`: the ff-merge and the migration "
                  f"are idempotent, so the re-run rebuilds, swaps and "
                  f"records."),
        )
    click.echo(
        click.style("•", fg="cyan")
        + " Rebuilt the installed endless-go from the advanced main"
    )


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
    the BINARY current by construction.

    Since ED-1601 no step points this binary at the real DB — Step 6 records
    with the installed binary, swapped in at Step 5.6 — so what this still buys is
    the compile check: a rebased branch that does not build aborts here, while
    base and the database are untouched.

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
        raise agent_help.report(
            f"cannot land {canonical}: `just` is not on PATH. Nothing was "
            f"merged or migrated.",
            "whether to install `just`, which this machine needs before a "
            "self-dev land can prove the rebased branch compiles",
            text=(f"cannot land {canonical}: `just` is not on PATH, so the "
                  f"worktree's endless-go cannot be rebuilt after the rebase "
                  f"onto base. That rebuild is what proves the rebased branch "
                  f"compiles before main advances."),
        )
    result = subprocess.run(
        ["just", "go"], cwd=str(worktree_path), capture_output=True, text=True,
    )
    if result.returncode != 0:
        # Compiler output stays inline (already in the message, before the
        # recovery), so no relay_foreign.
        #
        # The TSV left this CONDITIONAL between "errors in the branch's code"
        # and "a broken toolchain". It resolves at the step before: `just` was
        # found, and the source being compiled is this branch's, freshly
        # rebased onto base. A toolchain that cannot build at all would have
        # failed the same way for every worktree in the repo, not this one, so
        # the overwhelmingly likely fault is in the branch — which is the
        # agent's own work to fix and re-land. Nothing has moved.
        raise agent_help.no_report(
            f"cannot land {canonical}: the rebased branch does not build. "
            f"Nothing was merged or migrated — base and the database are "
            f"untouched.",
            "Fix the build errors on the task branch, commit, and retry the "
            "land",
            text=(f"cannot land {canonical}: rebuilding the worktree's "
                  f"endless-go after the rebase onto base failed.\n\n"
                  f"{(result.stderr or result.stdout).strip()}\n\n"
                  f"Nothing has been merged or migrated — base and the "
                  f"database are untouched. Fix the build and retry."),
        )
    click.echo(
        click.style("•", fg="cyan")
        + " Rebuilt worktree endless-go from the rebased source"
    )


def _resolve_land_migrate_bin(worktree_path: Path, project_root: Path) -> str | None:
    """The migration-only executable a self_dev land must migrate with, or None.

    ED-1571. Under ED-1567 a CANDIDATE binary may never migrate the real ledger,
    and a self_dev land has only candidates to offer: the worktree's own
    endless-go is the one whose embedded schema and enums match the rows the land
    just wrote (E-1664), and its path carries the worktree marker, which is what
    `candidateBuild()` means by unlanded code. The installed binary is permitted
    but does not carry the change being landed. Neither can apply it.

    So the apply step runs neither. It runs <worktree>/bin/endless-migrate, built
    from the landing branch by `_build_migration_executable`, which carries the
    migration set and nothing else — no hook, no task command, no query, no
    schema of its own — and therefore has no expectation of the database it is
    about to change. Having none is precisely what makes it safe where a
    candidate endless-go is not.

    Missing means a bug to surface, not to mask: the land built this binary a
    moment ago, before the ff-merge, so an absent one says the build silently did
    not happen rather than that the global would do instead (E-1662).

    Returns None for a non-self_dev project, which has no land, no worktree
    binaries and no business applying endless's own schema migrations.
    """
    from endless import config

    if not config.project_is_self_dev(project_root):
        return None
    migrate_bin = worktree_path / "bin" / "endless-migrate"
    if not migrate_bin.is_file() or not os.access(migrate_bin, os.X_OK):
        # A land reaches here immediately after building this binary, so an
        # absent one says the build silently did not happen: Endless failing at
        # its own sequence. The TSV calls the kind `fault` and the class
        # NO-REPORT because the message carries a workaround that works — the
        # named `just migrate-bin` — and `fault`'s directive ("do not retry")
        # would be wrong about a step that a re-run fixes. So: no_report with
        # the build command, and the bug it indicates is named in the summary
        # rather than hidden behind it.
        raise agent_help.no_report(
            f"The land-time migration executable {_display_path(migrate_bin)} "
            f"is missing, although the land just built it. Nothing was merged "
            f"or migrated.",
            "Run `just migrate-bin` in the worktree and retry the land",
            text=(f"The land-time migration executable is missing or not "
                  f"executable:\n\n    {_display_path(migrate_bin)}\n\n"
                  f"It is built from the landing branch by the land itself; if "
                  f"you are running one by hand, build it with `just "
                  f"migrate-bin` in the worktree."),
        )
    return str(migrate_bin)


def _build_migration_executable(worktree_path: Path, canonical: str) -> None:
    """Build ED-1571's migration executable from the landing branch.

    Called on EVERY self_dev land (E-2192), because every self_dev land runs
    `endless-migrate up`, and whether a branch carries a goose migration the
    database lacks is a question about the database, not the diff — a branch
    landing after a migration that never reached the ledger has the same gap.
    `up` against a current database is a no-op, so always building is cheaper
    than being right about when not to.

    Placed BEFORE the ff-merge, for E-1941's reason applied to a second binary: a
    broken build must abort while base and the database are still untouched. The
    executable is *invoked* at the apply step, between the ff-merge and the
    record, where a failure leaves the DB merely lagging code already on base —
    but a tree that cannot compile its own migration tool should never get as far
    as advancing base.

    Runs `just migrate-bin` rather than an inlined `go build`, so the build
    command has one definition and the land runs the same one a developer would.

    self_dev only: downstream users never build endless's binaries.
    """
    from endless import config
    if not config.project_is_self_dev(worktree_path):
        return
    if shutil.which("just") is None:
        raise agent_help.report(
            f"cannot land {canonical}: `just` is not on PATH. Nothing was "
            f"merged or migrated.",
            "whether to install `just`, which this machine needs before a "
            "self-dev land can build the executable that applies its schema "
            "migrations",
            text=(f"cannot land {canonical}: `just` is not on PATH, so the "
                  f"migration-only executable cannot be built from the "
                  f"landing branch. That build is what lets this land apply "
                  f"its own schema migrations at all — a binary built inside a "
                  f"task worktree is unlanded code and may not migrate the "
                  f"real database."),
        )
    result = subprocess.run(
        ["just", "migrate-bin"], cwd=str(worktree_path),
        capture_output=True, text=True,
    )
    if result.returncode != 0:
        # Resolved as _rebuild_worktree_binary's twin, for the same reason:
        # `just` exists and the source is this branch's, so the failure is in
        # the branch. Nothing has moved.
        raise agent_help.no_report(
            f"cannot land {canonical}: the branch's migration-only executable "
            f"does not build. Nothing was merged or migrated — base and the "
            f"database are untouched.",
            "Fix the build errors on the task branch, commit, and retry the "
            "land",
            text=(f"cannot land {canonical}: building the migration-only "
                  f"executable from the landing branch failed.\n\n"
                  f"{(result.stderr or result.stdout).strip()}\n\n"
                  f"Nothing has been merged or migrated — base and the "
                  f"database are untouched. Fix the build and retry."),
        )
    click.echo(
        click.style("•", fg="cyan")
        + " Built the migration executable from the landing branch"
    )


def _migrate_up(migrate_bin: str) -> dict:
    """Bring the database to the newest goose migration the landing branch
    carries (E-2192).

    Shells to `endless-migrate up` — schema.Migrate, goose Up and then the enum
    seeds — and returns its parsed JSON, {"status", "from", "to", "db"}, with
    status "migrated" or "current".

    The DB context is threaded as a per-invocation flag (E-1429), never an
    environment variable, so a stale export cannot silently redirect a
    migration. `land_worktree` has already pinned main, so during a land this
    always names the real ledger and threads `--db main`.

    `migrate_db_context_args`, NOT `go_db_context_args` — but since E-2157 the
    difference is one rewrite rather than a second dialect: both binaries take
    `--db main` and `--db-dir` from the same parser, and only `--db sandbox` is
    resolved here into `--db-dir <path>`, because ED-1571 leaves this executable
    no cwd routing to resolve it with. See config.migrate_db_context_args.

    Without this a branch's goose migration reached the real ledger only when an
    installed binary next connected, so the binary that records the landing
    could meet a database missing its own new column. E-2188 was
    that: `no such column: focus_task_id`, main advanced, landing unrecorded.
    """
    return _run_migrate(migrate_bin, ["up"], "migrate up")


def _run_migrate(migrate_bin: str, args: list[str], what: str) -> dict:
    """Run one `endless-migrate` subcommand against the resolved database and
    return its parsed JSON document.

    The DB context rides as a per-invocation flag (E-1429), never an exported
    variable. A failure raises a fault carrying the executable's own "error"
    text, else its stderr — a bare "it failed" teaches nothing.
    """
    from endless import config

    config.require_db_context()
    result = subprocess.run(
        [migrate_bin, *config.migrate_db_context_args(), *args],
        capture_output=True, text=True,
    )
    if result.returncode != 0:
        try:
            payload = json.loads(result.stdout.strip()) if result.stdout.strip() else {}
        except json.JSONDecodeError:
            payload = {}
        msg = (
            payload.get("error")
            or result.stderr.strip()
            or "the migration executable failed"
        )
        # Always wrapped: the one caller runs inside _migrate_landed_schema,
        # which catches this, reads `.message` and re-raises it as
        # _post_merge_failure — the refusal that knows main has already
        # advanced and what that means. So the class chosen here is the one
        # nobody reads, and `fault` is the honest answer for the day somebody
        # adds a third caller that lets it escape: endless-migrate is
        # Endless's own executable, and it refusing is Endless failing.
        raise agent_help.fault(f"{what} failed: {msg}")

    if not result.stdout.strip():
        return {}
    return json.loads(result.stdout.strip())


# land.toml: the per-branch land settings a task ships in its own directory
# (E-2192). Keys live in tables, never at top level, so each later setting gets
# its own; Endless-internal ones live under [self_dev]. No key exists today:
# E-2158 retired the only one, so every key is refused, and the file stays as
# the home a later setting will need.
LAND_SETTINGS_FILENAME = "land.toml"
_LAND_SETTINGS_KEYS: dict[str, set[str]] = {}

# Keys a land once understood, each with why it went. Refused like any unknown
# key, but by name: a branch cut before the retirement still carries one, and
# "unknown key" would read as a typo rather than as a setting that no longer
# has anything to do. (`schema_order` was retired by E-2158; the message names
# no task id because the people reading it cannot open this project's ledger.)
_RETIRED_LAND_SETTINGS = {
    ("self_dev", "schema_order"): (
        "goose migrations are the only schema step a land runs, so there is "
        "nothing left to order"
    ),
}


def _land_settings_path(worktree_path: Path, canonical: str) -> Path:
    return (
        worktree_path / ".endless" / "tasks" / f"e-{canonical[2:]}"
        / LAND_SETTINGS_FILENAME
    )


def _check_land_settings(worktree_path: Path, canonical: str, self_dev: bool) -> None:
    """Read and validate the landing branch's land settings (E-2192).

    Read from `.endless/tasks/e-<id>/land.toml` in the WORKTREE — the landing
    branch — because the agent that wrote the branch is the one who knows how it
    must land, not whoever happens to run the land.

    A missing file is fine, and so is an empty one. Anything the land does not
    understand — unreadable TOML, a setting at the top level, an unknown table
    or key — refuses, naming the file and the offender. A typo in a landing
    instruction must not be silently ignored. A retired key is refused by name,
    saying why it went. Called before the ff-merge, so the refusal
    leaves base and the database untouched.

    `[self_dev]` is known only on a self_dev project. Everywhere else no table
    is: nothing in it would do anything there, so it is refused like any other
    unknown table, and the refusal does not teach an Endless-only setting to a
    project it cannot apply to.
    """
    path = _land_settings_path(worktree_path, canonical)
    if not path.is_file():
        return

    def refuse(problem: str) -> agent_help.Refusal:
        # No TSV row: land.toml arrived with E-2192, after the inventory.
        # NO-REPORT on the same reading every other "the branch's own file is
        # wrong" refusal gets: land.toml is written by the agent that wrote the
        # branch, it lives on the task branch, the message names the
        # offending key, and the fix is a commit on that branch. Nothing has
        # moved — this runs before the ff-merge.
        return agent_help.no_report(
            f"cannot land {canonical}: its land.toml {problem} Nothing was "
            f"merged or migrated.",
            "Fix the named key in the task's land.toml, commit it on the task "
            "branch, and retry the land",
            text=(f"cannot land {canonical}: {_display_path(path)} {problem}\n\n"
                  f"Nothing has been merged or migrated — base and the "
                  f"database are untouched. Fix the file on the task branch, "
                  f"commit, and retry."),
        )

    try:
        settings = tomllib.loads(path.read_text())
    except (OSError, tomllib.TOMLDecodeError) as e:
        raise refuse(f"is not readable TOML: {e}")

    known = {t: k for t, k in _LAND_SETTINGS_KEYS.items()
             if t != "self_dev" or self_dev}
    known_tables = (
        "the known tables are: " + ", ".join(f"[{t}]" for t in known) + "."
        if known else "no land settings apply to this project yet."
    )
    for table, value in settings.items():
        if not isinstance(value, dict):
            raise refuse(
                f"sets `{table}` at the top level. Settings live in tables; "
                f"{known_tables}"
            )
        for key in value:
            why = _RETIRED_LAND_SETTINGS.get((table, key))
            if why:
                raise refuse(
                    f"sets [{table}].{key}, which is retired: {why}. "
                    f"Delete the key (and the file, if nothing else is in "
                    f"it); {known_tables}"
                )
        if table not in known:
            raise refuse(f"has an unknown table [{table}]; {known_tables}")
        for key in value:
            if key not in known[table]:
                raise refuse(
                    f"has an unknown key `{key}` in [{table}]. Known keys: "
                    + (", ".join(sorted(known[table])) or "none") + "."
                )


def _migrate_landed_schema(
    canonical: str,
    base_branch: str,
    migrate_bin: str | None,
) -> dict | None:
    """Back up, then bring the database to the landing branch's newest goose
    migration — AFTER the ff-merge. Returns `endless-migrate up`'s result,
    {"status", "from", "to", "db"}, which the land uses to clear the ERR-0020
    its migration caused (E-2205).

    One step (E-2158): `endless-migrate up`, which runs every migration the
    branch carries that the database lacks and is a no-op against a current
    database. It runs on every self_dev land, behind one backup.

    Ordering is the whole point of E-1941. Migrating BEFORE the merge meant a
    merge failure left the real DB migrated to a schema no installed binary
    understood: unrecoverable without a restore, and on 2026-08-10 it froze
    session tracking machine-wide. Migrating AFTER inverts that asymmetry — main
    has the code and the DB merely lags, which a re-run fixes: goose records
    every step in goose_db_version and `up` applies only what is missing.

    It cannot move later still: `_record_landing` runs the rebuilt installed
    binary against the real DB, and for a branch adding a column or a
    mirrored-enum value that binary expects what the DB lacks until this runs —
    E-1664's failure inverted, and E-2188's exactly. Between the ff-merge and
    the record is the only correct place.

    The backup is kept because a migration set can be several steps, so one can
    apply and the next fail — and there is still no `endless db restore`
    (E-1942).

    WHAT MIGRATES is ED-1571's: `migrate_bin`, the migration-only executable,
    built from this same branch at Step 4.6, which carries the migration set and
    no application at all. The worktree's endless-go is a candidate build, and
    ED-1567 forbids a candidate migrating the real ledger.

    The BACKUP runs on the PATH-resolved installed endless-go, not pinned: the
    main checkout's own build does not exist yet in a fresh checkout — Step 5.6
    installs it — and nothing about a backup needs a particular one. It is a VACUUM
    INTO of the file — monitor.BackupDB opens the database itself and never goes
    through the application's connect, so it has no schema expectation to
    disappoint and no gate to fall foul of. Moving it would buy nothing and give
    the migration executable a second job.
    """
    from endless.event_bridge import backup_db

    def _post_merge_failure(what: str, detail: str) -> agent_help.Refusal:
        # The one place in this file where report_if is the honest answer.
        #
        # `detail` is whatever failed — a backup that could not be written, a
        # goose migration refusing against the real ledger, or the
        # self-declared land bug below. Those do not share a
        # class: some are re-run-and-forget, and some mean the real database is
        # part-migrated with no `endless db restore` to undo it (E-1942). This
        # function cannot tell them apart from a string, and guessing wrong in
        # the second direction is the expensive one — so both branches are
        # named and the agent, which can read `detail`, decides.
        return agent_help.report_if(
            f"Landed {canonical} into {base_branch} — main IS advanced — but "
            f"{what} failed, so the database is NOT migrated.",
            "the cause below is a backup or migration failing against the real "
            "database, or names no cause at all",
            "re-run `just land " + canonical + "`, which applies only what is "
            "still outstanding",
            "a part-migrated real database cannot be rolled back — there is no "
            "`endless db restore`",
            text=(f"Landed {canonical} into {base_branch}: main was advanced, "
                  f"but {what} failed:\n\n{detail}\n\n"
                  f"The code is on {base_branch}; the database has not been "
                  f"migrated yet. Nothing is lost and no restore is needed — "
                  f"resolve the cause above and re-run `just land "
                  f"{canonical}`. The ff-merge is idempotent and goose "
                  f"records each migration it applies, so the retry applies "
                  f"only what is still outstanding."),
        )

    click.echo(
        click.style("•", fg="cyan") + " Backing up DB before migrating"
    )
    try:
        backup_db()
    except Exception as e:
        detail = e.message if isinstance(e, click.ClickException) else str(e)
        raise _post_merge_failure("the pre-migration database backup", detail)

    if migrate_bin is None:
        raise _post_merge_failure(
            "migrating the database",
            "no migration executable was resolved for a self_dev land; this is "
            "a bug in the land, not in the branch.",
        )

    up_result: dict | None = None

    def migrate_up() -> None:
        nonlocal up_result
        try:
            res = _migrate_up(migrate_bin)
        except Exception as e:
            detail = e.message if isinstance(e, click.ClickException) else str(e)
            raise _post_merge_failure("migrating the database (`up`)", detail)
        up_result = res
        if res.get("status") == "migrated":
            click.echo(
                click.style("•", fg="cyan")
                + f" Migrated DB from version {res.get('from')} to {res.get('to')}"
            )

    migrate_up()
    return up_result


def _clear_land_schema_faults(
    up_result: dict | None, since: str, canonical: str,
    endless_go_bin: str | None,
) -> None:
    """Clear the ERR-0020 this land's own migration caused (E-2205).

    Step 5.2 builds before the migration and Step 5.6 swaps by rename right
    after it, which shrinks the window in which the installed binary is older
    than the database from a compile to milliseconds — but not to nothing. A
    reader that started on the old binary just before the migration committed
    reads the new version and records "database is at schema v<to>, endless-go
    carries v<from>". No ordering inside the land can close that; the land is
    the one party that knows it caused it, so it clears it.

    Exactly that incident: the fingerprint names both versions, and `since` —
    taken just before the migration — excludes an incident opened earlier, which
    is somebody's real problem. Runs after the landing is recorded, so a reader
    still in flight at the swap has finished and recorded by then.

    Never fatal. The land has happened; at worst an incident is left for a
    person to clear, which is where every land was before E-2205.
    """
    if not up_result or up_result.get("status") != "migrated":
        return
    from endless.event_bridge import clear_land_schema_faults
    try:
        cleared = clear_land_schema_faults(
            int(up_result["to"]), int(up_result["from"]), since,
            f"worktree land {canonical}", endless_go_bin=endless_go_bin,
        )
    except Exception as e:
        detail = e.message if isinstance(e, click.ClickException) else str(e)
        agent_help.warn.no_report(
            f"Landed {canonical}, but clearing the schema-version error its "
            f"migration may have caused failed: {detail}. The land itself "
            f"succeeded.",
            "Run `endless errors list`; clear any ERR-0020 naming "
            f"v{up_result['to']} and v{up_result['from']} with "
            "`endless errors clear <id>`",
            text=(click.style("⚠ could not clear this land's schema-version "
                              "error", fg="yellow")
                  + f"\n    {detail}\n"
                  f"    The land succeeded. `endless errors list` shows any "
                  f"ERR-0020 naming v{up_result['to']} and "
                  f"v{up_result['from']}; clear it with "
                  f"`endless errors clear <id>`."),
        )
        return
    if cleared:
        click.echo(
            click.style("•", fg="cyan")
            + f" Cleared the schema-version error this land's migration caused "
              f"(v{up_result['from']} → v{up_result['to']})"
        )


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
        raise agent_help.no_report(
            "--record-only requires --sha <merge-commit-sha>.",
            "Pass --sha <merge-commit-sha> and retry",
        )

    main_root = _project_root()
    _, proj_name = _resolve_project(None)
    item_id = int(canonical[2:])

    landed_at = at
    if not landed_at:
        try:
            landed_at = _git(["show", "-s", "--format=%cI", sha], cwd=main_root)
        except subprocess.CalledProcessError as e:
            # git's words are already inside the message, so no relay_foreign.
            # NO-REPORT: the whole input is a SHA the caller supplied, and both
            # ways out — a SHA that exists here, or an explicit --at — are the
            # caller's to supply. Nothing was recorded.
            raise agent_help.no_report(
                f"git could not read a commit date for {sha}, so no landing "
                f"was recorded for {canonical}.",
                "Verify the SHA with `git log`, or pass --at <RFC3339>, and "
                "retry",
                text=(f"Cannot read the commit date for {sha} in "
                      f"{_project_root()}: "
                      f"{(e.stderr or e).strip() if hasattr(e, 'stderr') else e}\n\n"
                      f"Confirm the SHA exists on this checkout, or pass --at "
                      f"<RFC3339>."),
            )
    if not landed_at:
        raise agent_help.no_report(
            f"commit {sha} produced no date; pass --at <RFC3339> explicitly.",
            "Take the date from `git log` and pass it as --at, then retry",
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
        # CONDITIONAL in the TSV, and genuinely so: the common cause is the
        # session-attribution gate, which the agent clears by re-running from
        # the bound session, while a schema or database fault leaves merged work
        # unrecorded until a person intervenes. The emit's own text is the only
        # thing that separates them, and it is right here in `detail` — so the
        # agent that can read it gets both branches rather than a guess made
        # without it.
        raise agent_help.report_if(
            f"Landed {canonical} ({branch}) into {base_branch} — main IS "
            f"advanced — but the task.landed event did NOT record, so the "
            f"landing is unrecorded.",
            "the cause below is a database or schema fault rather than the "
            "session-attribution gate",
            f"re-run `just land {canonical}` from the bound session, which "
            f"records the landing without re-merging anything",
            "work that is merged into the base branch but recorded nowhere "
            "will not be found again by any Endless surface",
            text=(f"Landed {canonical} ({branch}) into {base_branch}: main was "
                  f"advanced, but recording the landing failed:\n\n{detail}\n\n"
                  f"The ff-merge is idempotent. Re-run `just land {canonical}` "
                  f"to record the landing once the cause above is resolved."),
        )


def _latest_landing(canonical: str) -> dict | None:
    """The newest recorded landing for a task, or None.

    Landing is append-only — a follow-up commit lands again — so the row that
    answers "where did my work go?" is the newest.
    """
    from endless.task_cmd import _task_landings

    try:
        landings = _task_landings(int(canonical.removeprefix("E-")))
    except Exception:
        return None
    return landings[0] if landings else None


def _no_worktree_to_land_message(
    canonical: str, latest: dict | None = None,
) -> str:
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
    if latest is None:
        latest = _latest_landing(canonical)
    if latest:
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


def _no_worktree_to_land_refusal(canonical: str) -> agent_help.Refusal:
    """Classify the two outcomes `_no_worktree_to_land_message` renders.

    They are not one refusal wearing two texts, they are two refusals, and the
    TSV classed them apart:

    * A recorded landing. The work IS in the base branch and the reaper removed
      the directory on schedule — an idempotent no-op dressed as a non-zero
      exit (which it keeps, so automation's reading does not change). There is
      nothing for a person to decide about a land that already happened.
    * No landing and no worktree. Which of the two readings applies depends on
      who named the id: an agent that reached for the wrong one retries with
      the right one, while a task the USER asked to land that has neither a
      worktree nor a landing is a real gap in their fleet. This function cannot
      see which, so both branches are named — the TSV's 2026-09-18 decision for
      this row.
    """
    latest = _latest_landing(canonical)
    text = _no_worktree_to_land_message(canonical, latest=latest)
    if latest:
        sha = (latest["merge_commit_sha"] or "")[:12]
        return agent_help.no_report(
            f"{canonical} already landed at {sha}; nothing was done and "
            f"nothing needed to be. Its worktree was reaped after the landing "
            f"aged past worktree_ttl.",
            "Treat the land as already done; `endless task landed "
            f"{canonical}` has the history",
            text=text,
        )
    return agent_help.report_if(
        f"No worktree and no recorded landing exist for {canonical}; nothing "
        f"was landed.",
        "the user named this task to land",
        "re-check `endless worktree list` and retry with the id that has a "
        "worktree",
        "a task the user expected to be landable having neither a worktree nor "
        "a landing is a gap in their fleet, not a mistyped id",
        text=text,
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
            raise agent_help.no_report(
                "Not inside a task worktree, so there is no task to diagnose. "
                "Name one: endless worktree diagnose E-NNNN",
                "Re-run naming the task id",
            )

    rows = _enriched_list(_project_root())
    target = _branch_for_task(rows, canonical)
    if target is None:
        raise _no_worktree_to_land_refusal(canonical)
    branch = target["branch"]
    if not branch:
        # Read-only diagnostic: there is simply nothing to diagnose on a
        # detached tree, and the agent has two ways on (check out the branch
        # there, or diagnose another task) without anybody being consulted.
        raise agent_help.no_report(
            f"Worktree for {canonical} has no branch (detached HEAD), so there "
            f"is nothing to diagnose. Nothing was changed.",
            f"Check out task/{canonical[2:]} in that worktree, or diagnose "
            f"another task",
            text=f"Worktree for {canonical} has no branch (detached HEAD).",
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
        # Non-zero by design, and nothing is wrong: this is the command's
        # answer, which happens to be "nothing to diagnose". The message names
        # the one next step, `--dry-run`, which the agent can run itself.
        raise agent_help.no_report(
            f"No land conflict is recorded for {canonical}, so there is "
            f"nothing to classify. Nothing was changed.",
            f"Rehearse a land instead — `endless worktree land {canonical} "
            f"--dry-run` runs the real rebase on a throwaway branch",
            text=(f"No land conflict is recorded for {canonical}.\n\n"
                  f"A capture is written only when `endless worktree land` "
                  f"actually hits a rebase conflict, and it is stored with the "
                  f"worktree, so it is gone once the worktree is reaped. "
                  f"Nothing is reproduced here on purpose: a conflict "
                  f"re-derived now would be against today's {base_branch}, not "
                  f"the one the land failed against.\n\n"
                  f"To see whether a land WOULD conflict, rehearse it:\n"
                  f"  endless worktree land {canonical} --dry-run"),
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
      3.6 Refuse if the branch and main both added migrations since they
         diverged, or the project's pre-land hook vetoes (E-2184).
      4. Rebase the worktree branch onto main (in the worktree).
      4.2 Rebuild the worktree's endless-go from the now-current source,
         self_dev only (E-1941): the rebased branch must compile before main
         advances.
      4.6 Validate land.toml, and build the migration-only executable from the
         landing branch, self_dev only (ED-1571).
      5. ff-merge from main.
      5.2 Build the installed (main checkout's) endless-go to the side,
         self_dev only (E-2205): the compile happens before the migration, so
         a failure leaves the database untouched.
      5.5 Migrate (`endless-migrate up`, E-2192), self_dev only (E-1941).
         AFTER the merge, so a failure leaves the DB lagging landed code (a
         re-run fixes it) rather than migrated ahead of code that never landed.
      5.6 Swap Step 5.2's build in, self_dev only (E-2020, E-2205): a
         worktree build never opens main (ED-1601), so Step 6 records with the
         installed binary, which must match the database. Done by rename right
         after the migration, so no reader meets a database ahead of it.
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
    # real DB, schema migrations belong to it, and the merge is into main. When run
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
        raise _no_worktree_to_land_refusal(canonical)
    branch = target["branch"]
    if not branch:
        detached = (
            f"Worktree for {canonical} has no branch (detached HEAD); cannot land."
        )
        # The TSV's 2026-09-18 decision for this row was to resolve the
        # condition in code rather than hand it to the agent, and the
        # companion file already holds the answer. `session resume --review`
        # creates its inspection tree with `git worktree add --detach` and
        # writes `"branch": null` (recreate_dropped_worktree), so a companion
        # that names no branch IS a review tree — one that was never meant to
        # land, and putting a branch on it would change what the user is
        # inspecting. A companion that NAMES a branch while git reports
        # detached is a worktree somebody detached after the fact, in the
        # agent's own tree, and checking the branch back out restores it.
        companion = target["companion"] or {}
        if "branch" in companion and companion["branch"] is None:
            raise agent_help.report(
                f"{canonical}'s worktree is a detached `session resume "
                f"--review` inspection tree, which has no branch to land. "
                f"Nothing was landed.",
                "whether to turn a read-only inspection tree into a working "
                "one, which changes what is being inspected",
                text=detached,
            )
        working_branch = (
            companion.get("branch") or task_branch(int(canonical[2:]))
        )
        raise agent_help.no_report(
            f"{canonical}'s worktree is on a detached HEAD although its "
            f"companion names {working_branch}. Nothing was landed.",
            f"Check out {working_branch} in that worktree and retry the land",
            text=detached,
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
        # E-2184: the gate is part of what a real land would do first.
        _refuse_if_land_gated(main_root, worktree_path, base_branch, canonical)
        # E-1957: rehearse the rebase rather than describe it. A preview that
        # cannot preview the failure it exists to preview is a preview of
        # nothing, and the rebase is the only step of a land that fails in a way
        # the operator has to reason about.
        try:
            ev = _rehearse_land_rebase(
                worktree_path, branch, base_branch, main_root, canonical,
            )
        except subprocess.CalledProcessError as e:
            # git is foreign, so the site classifies and git's words ride as
            # detail. NO-REPORT: the rehearsal happens entirely on a throwaway
            # branch in a throwaway checkout, both removed in a `finally`, so a
            # failure here changed nothing anywhere — and `--dry-run` is itself
            # the safe thing to retry.
            raise agent_help.relay_foreign(
                agent_help.no_report(
                    f"could not rehearse {canonical}'s rebase; nothing was "
                    f"changed — the rehearsal runs on a throwaway branch.",
                    "Act on what git said below and re-run the --dry-run",
                    text="could not rehearse the rebase:",
                ),
                str(e.stderr or e),
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
        # The classification above stays on STDOUT, where it has always been:
        # it is this command's answer, and `--dry-run > report.txt` is a real
        # way to read it. What changes is the exit, which was a bare
        # SystemExit(1) — non-zero so automation can gate on a predicted
        # conflict, and silent about whether the reader has to do anything.
        # Same exit code, now with a verdict.
        #
        # The CONDITIONAL resolves off the classification itself, which is
        # already in hand: `prescription` is non-empty exactly when the class
        # is proven AND the steps are safe to run (land_conflict asserts that
        # invariant), so a prescription means the agent can carry it out, and
        # its absence means the repository could not settle the question —
        # mid-branch orphaned-ledger, symbol supersession, semantic overlap.
        # Those are choices between two people's intent, which is the one
        # thing this command says it does not have.
        conflict_summary = (
            f"Rehearsed {canonical}'s rebase onto {base_branch} on a throwaway "
            f"branch: it conflicts ({cl.klass}). Nothing was landed and "
            f"nothing was changed; the classification is on stdout above."
        )
        if cl.prescription:
            raise agent_help.no_report(
                conflict_summary,
                "Run the proven recovery printed above, then land",
                text=conflict_summary,
            )
        raise agent_help.report(
            conflict_summary,
            "what this branch should become where it overlaps "
            f"{base_branch} — nothing in the repository settles it, and no "
            f"recovery is safe to prescribe",
            text=conflict_summary,
        )

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
        # Pre-flight on the MAIN checkout, before anything moves. git is
        # foreign, so the site classifies; NO-REPORT because `git ls-files` on
        # a repository that is otherwise working fails for reasons git names
        # and the agent can clear, and the land has not started.
        raise agent_help.relay_foreign(
            agent_help.no_report(
                f"git ls-files failed on {main_root}, so land could not take "
                f"its pre-land ignored-file snapshot. Nothing was landed.",
                "Act on what git said below and retry the land",
                text="git ls-files (pre-land ignored snapshot) failed:",
            ),
            str(e.stderr or e),
        )

    last_error = None
    # E-2174: which of the two races the last attempt lost, so the exhaustion
    # message below names the right one. A busy repository and a repository
    # moving under the land need different things from the reader.
    last_was_contention = False
    for attempt in range(1, LAND_MAX_RETRIES + 1):
        # Step 1: partition main's working-tree modifications.
        try:
            auto_files, user_files = _git_status_partition(main_root)
        except subprocess.CalledProcessError as e:
            # E-2174: a monitor probe holding main's index makes this a busy
            # repository, not a broken one. Retry the attempt.
            if (busy := _lock_contention_text(e)) is not None:
                last_error, last_was_contention = busy, True
                _lock_backoff(attempt)
                continue
            # Lock contention is filtered out above and retried, so what
            # reaches here is main's git plumbing not working. NO-REPORT: git
            # names it, nothing has moved, and `git status` on the main
            # checkout is not a question about anybody's intent.
            raise agent_help.relay_foreign(
                agent_help.no_report(
                    f"git status failed on {main_root}, so land could not "
                    f"partition its working tree. Nothing was landed.",
                    "Act on what git said below and retry the land",
                    text="git status failed:",
                ),
                str(e.stderr or e),
            )

        # Step 2: refuse if user-work modified.
        if user_files:
            file_list = "\n  ".join(user_files[:20])
            more = "" if len(user_files) <= 20 else f"\n  ... and {len(user_files) - 20} more"
            # CONDITIONAL, and the TSV's 2026-09-18 decision was to name both
            # branches rather than guess: these are uncommitted files on the
            # USER's main checkout, and whose they are is the whole question.
            # The agent's own stray writes into main it can move to its
            # worktree and commit there; another session's in-flight work, or
            # the user's, it must not touch at all. Only the reader holding the
            # conversation can tell which, from the file list.
            raise agent_help.report_if(
                f"main has {len(user_files)} uncommitted user file(s), so "
                f"{canonical} was not landed. Nothing was changed.",
                "the listed files are not the agent's own stray writes into "
                "main",
                "move them into the task worktree, commit them there, and "
                "retry the land",
                "moving or setting aside another session's or the user's "
                "in-flight work on main is not recoverable from here",
                text=(f"main has uncommitted user changes; cannot land "
                      f"{canonical}.\n\n"
                      f"Files:\n  {file_list}{more}\n\n"
                      f"Resolve them: commit (in a worktree), move to a "
                      f"worktree, or set them aside, then retry."),
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
                if (busy := _lock_contention_text(e)) is not None:
                    last_error, last_was_contention = busy, True
                    _lock_backoff(attempt)
                    continue
                # E-2275: the background recorder commits these same paths on
                # main and shares no lock with the land. When it committed them
                # after Step 1's status read, git has nothing left to commit
                # and exits 1 with only stdout. Nothing staged means the
                # recorder already did this step's work, so carry on. Checked
                # after the failure rather than before the commit, which would
                # leave a window between the check and the commit.
                if _git_run(["diff", "--cached", "--quiet"], cwd=main_root,
                            check=False).returncode != 0:
                    # The files are Endless's own auto-managed paths on main,
                    # and lock contention has already been sent back to the
                    # retry loop. What is left is git refusing the add or the
                    # commit — a pre-commit hook, an unwritable index — which
                    # git names and the agent clears. Nothing merged. Git
                    # sometimes explains on stdout alone, so fall back to it.
                    raise agent_help.relay_foreign(
                        agent_help.no_report(
                            f"git could not auto-commit main's endless-managed "
                            f"files, so {canonical} was not landed. Nothing "
                            f"was merged.",
                            "Act on what git said below and retry the land",
                            text="auto-commit failed:",
                        ),
                        str(e.stderr or e.stdout or e),
                    )

        # Step 3.5: dedup the worktree's verbs.jsonl against main's, committing
        # the bundled result on the worktree's branch (E-1141 / E-1138).
        try:
            _dedup_worktree_verbs_against_main(worktree_path, main_root)
        except subprocess.CalledProcessError as e:
            if (busy := _lock_contention_text(e)) is not None:
                last_error, last_was_contention = busy, True
                _lock_backoff(attempt)
                continue
            # Step 3.5 works inside the agent's OWN worktree, on an auto-file
            # Endless writes. Nothing has merged, and git has named why.
            raise agent_help.relay_foreign(
                agent_help.no_report(
                    f"git could not commit the deduped verbs.jsonl in "
                    f"{canonical}'s worktree, so nothing was landed.",
                    "Act on what git said below and retry the land",
                    text="verbs.jsonl dedup on worktree failed:",
                ),
                str(e.stderr or e),
            )

        # Step 3.6 (E-2184): refuse when the branch and base both added
        # migrations since they diverged, when the branch holds copies of base's
        # commits because base was rewritten (E-2242), or the project's pre-land
        # hook vetoes.
        # BEFORE Step 3.7, whose rebase can move the merge-base to base's tip and
        # blind the check. Per attempt, not once: a retry means base moved, and
        # what it moved by may be a migration.
        _refuse_if_land_gated(main_root, worktree_path, base_branch, canonical)

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
            # E-2174: before reading conflict state — a rebase that could not
            # create index.lock never started, so there is nothing to classify
            # and nothing to abort.
            if (busy := _lock_contention_text(e)) is not None:
                last_error, last_was_contention = busy, True
                _lock_backoff(attempt)
                continue
            # The orphan drop and the replay of the user's commits share one
            # rebase; a conflict here is the replay conflicting, not the drop.
            # Read the state BEFORE aborting, then abort — but only a rebase
            # this step actually started.
            # Already classified: _rebase_failure_message returns the refusal,
            # because only it can still see which of the three failures this
            # was — the abort below destroys the evidence.
            refusal = _rebase_failure_message(
                worktree_path, base_branch,
                phase="replaying your commits after dropping base auto-amend commits",
                stderr=e.stderr, pre_existing=rebase_was_running,
            )
            if not rebase_was_running:
                _git_run(["rebase", "--abort"], cwd=worktree_path, check=False)
            raise refusal
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
            # The message says "remove these commits", which the rule would
            # normally make NO-REPORT. It is not. An Endless writer broke the
            # ledger routing invariant to put them there, and the remedy
            # rewrites durable history: the db-ledger is the permanent record
            # the SQLite database is only a projection of, so commits dropped
            # here may be recorded task state that exists nowhere else.
            # Deciding they can go is the user's, and the broken writer is
            # something they need told about.
            raise agent_help.report(
                f"cannot land {canonical}: {len(offenders)} commit(s) on the "
                f"branch modify the database ledger, which only the main "
                f"checkout may do. Nothing was merged.",
                "whether those branch-side ledger commits can be thrown away — "
                "they may hold recorded task state that exists nowhere else, "
                "and an Endless writer put them there",
                text=(f"cannot land {canonical}: the branch has "
                      f"{len(offenders)} {noun} modifying the database ledger "
                      f"({DB_LEDGER_DIR}/):\n\n"
                      f"{listing}\n\n"
                      f"Ledger entries are recorded on the main checkout, "
                      f"never on a task branch — landing these would rebase a "
                      f"branch-authored ledger segment into main and corrupt "
                      f"the shared database history. Remove these commits from "
                      f"the branch before retrying (inspect each with `git "
                      f"show <sha>`)."),
            )

        # Step 3.8 (E-1416): guard against modified worktree tree before rebase.
        try:
            _guard_modified_worktree(worktree_path, branch, canonical)
        except subprocess.CalledProcessError as e:
            # E-2174: the guard re-raises rather than classifies when its own
            # `git status` lost the index lock — the same race as Step 1, one
            # worktree over. Anything it CAN classify it raises as a
            # Refusal, which is not caught here.
            if (busy := _lock_contention_text(e)) is not None:
                last_error, last_was_contention = busy, True
                _lock_backoff(attempt)
                continue
            # Same refusal the guard itself would have raised, for the same
            # reason: contention is handled above, so this is the worktree's
            # git plumbing not working, and that is not a land's to repair.
            raise agent_help.relay_foreign(
                agent_help.report(
                    f"git status in {canonical}'s worktree failed, so land "
                    f"could not tell whether it is modified. Nothing was "
                    f"landed.",
                    "how to repair a worktree whose own `git status` fails",
                    text="git status in worktree failed:",
                ),
                str(e.stderr or e),
            )

        # Step 4: rebase the worktree branch onto main.
        rebase_was_running = _rebase_in_progress(worktree_path)
        try:
            _git_run(["rebase", base_branch], cwd=worktree_path)
        except subprocess.CalledProcessError as e:
            # E-2174: this is the failure the task was filed for. A rebase that
            # could not create index.lock never detached HEAD, so there is no
            # conflict to report and no rebase to abort — retry the attempt
            # instead of leaving the loop this step is already standing inside.
            if (busy := _lock_contention_text(e)) is not None:
                last_error, last_was_contention = busy, True
                _lock_backoff(attempt)
                continue
            # Read the state (files + failing commit) BEFORE aborting, then
            # abort. What git printed decides which report this is: a conflict
            # names files and offers candidates, anything else quotes git and
            # offers none (E-2122).
            refusal = _rebase_failure_message(
                worktree_path, base_branch,
                phase=f"rebasing your branch onto {base_branch}",
                stderr=e.stderr, pre_existing=rebase_was_running,
            )
            if not rebase_was_running:
                _git_run(["rebase", "--abort"], cwd=worktree_path, check=False)
            raise refusal

        # Step 4.2 (E-1941): the branch is now rebased onto base, so the
        # worktree's source is current — rebuild endless-go from it, which proves
        # the rebased branch compiles. Before Step 5, so a broken build aborts
        # with base and the DB untouched. (No step points this binary at the real
        # DB any more; see _rebuild_worktree_binary.)
        _rebuild_worktree_binary(worktree_path, canonical)

        # Step 4.6 (ED-1571, E-2192): with base still unadvanced, read the
        # branch's land.toml and build the migration-only executable from the
        # landing branch. Every self_dev land builds it, because every self_dev
        # land runs `endless-migrate up` at Step 5.5 — whether the database
        # lacks a goose migration is not something the diff can answer. Before
        # Step 5 for E-1941's reason: a bad land.toml or a tree that cannot
        # compile its own migration tool must abort while base and the database
        # are untouched. The land.toml read is not self_dev-gated: a typo in a
        # landing instruction is refused on any project.
        _check_land_settings(
            worktree_path, canonical, config.project_is_self_dev(main_root),
        )
        migrate_bin = None
        if config.project_is_self_dev(main_root):
            _build_migration_executable(worktree_path, canonical)
            migrate_bin = _resolve_land_migrate_bin(worktree_path, main_root)

        # Step 5: ff-merge.
        try:
            _git_run(["merge", "--ff-only", branch], cwd=main_root)
        except subprocess.CalledProcessError as e:
            err_text = (e.stderr or "") + (e.stdout or "")
            if _is_lock_contention(err_text):
                last_error, last_was_contention = err_text, True
                _lock_backoff(attempt)
                continue
            if _is_retryable_ff_merge_error(err_text):
                last_error, last_was_contention = err_text, False
                continue
            # The last step before main moves, and the one whose failure says
            # most about main itself. The retryable races — a diverged or
            # dirty main, lock contention — are consumed above, so what is
            # left is main not being where a fast-forward can happen: on
            # another branch, or holding state the agent must not alter. The
            # TSV's own note says REPORT is the typical reading, and it is the
            # safe one: nothing merged, and "make main fast-forwardable" can
            # mean moving somebody's checkout.
            raise agent_help.relay_foreign(
                agent_help.report(
                    f"git refused the fast-forward merge of {branch} into "
                    f"{base_branch} on {main_root}, so {canonical} was NOT "
                    f"landed and nothing was merged.",
                    "how to make the main checkout fast-forwardable — it is "
                    "not where this land needs it, and moving it may disturb "
                    "work in progress there",
                    text="ff-merge failed:",
                ),
                err_text,
            )

        # Step 5.2 (E-2205): build the installed endless-go from the advanced
        # main, to the side. Before Step 5.5, so the compile does not hold the
        # old binary in place against a migrated database (ERR-0020), and a
        # build failure stops the land while the database is untouched.
        _build_main_binary_next(main_root, canonical, base_branch)

        # Step 5.5 (E-1941): migrate the database now that main HAS advanced. Before the merge this was the irreversible case (DB
        # migrated, code not landed, no installed binary able to read it);
        # after it, a failure merely leaves the DB lagging code that is already
        # on main, which a re-run fixes. Must precede Steps 5.6 and 6: the
        # installed binary is swapped in at 5.6 and records the landing at
        # 6, and it needs the schema these migrations write (E-1664 inverted,
        # E-2188).
        #
        # ED-1571: the migrator is the migration executable built at Step 4.6,
        # NOT the endless-go Step 6 uses. A worktree build may not open the real
        # ledger at all (ED-1601), so the two steps run two different programs
        # against one database — one that carries migrations and no schema
        # expectation, then the installed binary, swapped in at Step 5.6, whose
        # embedded schema matches what the first just wrote.
        #
        # self_dev only: the migration set is endless's OWN schema, so a
        # downstream land has no business migrating the user's DB.
        #
        # E-2192: this runs on EVERY self_dev land, whatever the diff says. That
        # includes the re-run after "recording the landing failed": its ff-merge
        # is a no-op, but `up` still brings the database to the branch's
        # migrations before the record is retried.
        # E-2205: the moment the migration window opens, in the errors table's
        # own form, so the land can clear the ERR-0020 it causes and nothing
        # older.
        migrate_started = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%S")
        up_result = None
        if config.project_is_self_dev(main_root):
            up_result = _migrate_landed_schema(canonical, base_branch, migrate_bin)

        # Step 5.6 (E-2020, ED-1601, E-2205): swap Step 5.2's build into place
        # the moment the migration has committed, so Step 6 records with a
        # binary that matches the database Step 5.5 just migrated, and no
        # reader is left on the old one. A worktree build may not open main.
        _swap_main_binary(main_root, canonical, base_branch)

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
            # Main HAS advanced. `git rev-parse HEAD` on a checkout that just
            # took a merge should not fail, and this message names no recovery
            # because there is no obvious one: the work is in the base branch
            # and the landing is unrecorded. Re-running the land would record
            # it (the ff-merge is idempotent), but nothing has ever said so
            # here, so the agent is not told it as a remedy it can rely on.
            raise agent_help.relay_foreign(
                agent_help.report(
                    f"Landed {canonical} — main IS advanced — but git could "
                    f"not read the merge SHA, so the landing was not "
                    f"recorded.",
                    "what to do about work that is merged into the base branch "
                    "but recorded nowhere, on a checkout whose `git rev-parse` "
                    "is failing",
                    text=f"Landed {canonical} but reading merge SHA failed:",
                ),
                str(e.stderr or e),
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

        # E-2205: clear the ERR-0020 a reader recorded in the instant between
        # Step 5.5's migration and Step 5.6's swap. After the record, so any
        # reader still in flight at the swap has finished.
        _clear_land_schema_faults(
            up_result, migrate_started, canonical, endless_go_bin,
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

        # E-2128: the branch tip now equals the base, so this worktree's unlanded
        # verdict is known without computing it. Warm the cache before the reap
        # sweep below, whose condition 4 reads the same cache.
        try:
            _warm_unlanded_cache(worktree_path)
        except Exception:
            # Deliberately silent, unlike the sweep below. A cold cache entry costs
            # one monitor tick showing `~`; saying so would be noise on a land that
            # succeeded.
            pass

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

    if last_was_contention:
        # Both exhaustion paths are NO-REPORT and for the same reason: nothing
        # is wrong, nothing moved, and waiting IS the recovery. Reporting a
        # busy repository to the user would be reporting the weather.
        raise agent_help.no_report(
            f"Land of {canonical} lost the git index lock on all "
            f"{LAND_MAX_RETRIES} attempts. Nothing was merged; the branch is "
            f"untouched and there is nothing to resolve.",
            "Wait for the repository to go quiet and re-run the land; do not "
            "delete the lock file, its holder is a live process",
            text=(f"Land of {canonical} failed after {LAND_MAX_RETRIES} "
                  f"retries; the repository stayed busy throughout — every "
                  f"attempt lost the git index lock to another process holding "
                  f"it.\n\n"
                  f"This is NOT a conflict and nothing is wrong with your "
                  f"branch: there is nothing to resolve and nothing to "
                  f"inspect. Endless's own surfaces are the usual holders (the "
                  f"session monitor, the per-minute unlanded sweep, `worktree "
                  f"check`), so a quieter moment is normally all it takes. Do "
                  f"NOT delete the lock file — its holder is a live process, "
                  f"and removing it corrupts the index.\n\n"
                  f"Last error:\n{last_error or '(none)'}"),
        )
    raise agent_help.no_report(
        f"Land of {canonical} failed after {LAND_MAX_RETRIES} retries: another "
        f"session appends to the auto-files faster than land converges. "
        f"Nothing was merged.",
        "Wait for the other session to go quiet and re-run the land",
        text=(f"Land of {canonical} failed after {LAND_MAX_RETRIES} retries; "
              f"another session is appending to auto-files faster than land "
              f"can converge. Try again later.\n\nLast error:\n"
              f"{last_error or '(none)'}"),
    )


def _worktree_in_use_probe(worktree_path: Path) -> tuple[str, str]:
    """Ask `endless-go worktree in-use` whether anything depends on a directory.

    Returns (verdict, detail); verdict is "free", "in-use", "unknown" or
    "no-binary". Callers decide what to DO about each — dropping refuses on
    anything but "free", and so does the sync sweep, for the same reason in a
    milder form: rebasing a branch under a session that is standing in it does
    not orphan its cwd, but it does change every file beneath a process that
    has already read them.

    `detail` comes from a different stream per verdict, and getting that wrong
    is the defect E-2159 found here. It used to be one expression,
    `stdout or stderr`, and on the UNDETERMINED path those are two different
    things: worktreecmd.report writes the literal reason word `undetermined` to
    stdout, and the actual failure — classified, as a Go refusal, with the
    error inside it — to stderr. Preferring stdout therefore quoted the probe's
    own placeholder and dropped the diagnosis, so `worktree drop` refused with
    "Cannot verify whether this worktree is in use: undetermined" and the
    reason went nowhere. Taking stderr first on that path is what lets the
    caller relay Go's refusal, which is already classified, instead of
    re-describing it from nothing. On the IN-USE path the precedence is the
    other way round and always was: there stdout carries the answer and nothing
    is written to stderr at all.

    This shells out rather than probing here, because monitor.WorktreeInUse is
    the one implementation of the question and it runs two complementary probes
    (an active-session row, and a live process holding cwd). Reimplementing
    either in Python is what the verb exists to prevent — see
    internal/monitor/worktree_inuse.go.
    """
    from endless import config

    task_id = _task_id_from_worktree_path(worktree_path)
    # `--task 0` means "belongs to no task", which the verb reads as "run only the
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
    if result.returncode == 0:
        return "free", ""
    if result.returncode == 3:
        # Exit 3 IS the answer, and the answer is the reason on stdout — the
        # session row or the live process monitor.WorktreeInUse found.
        return "in-use", result.stdout.strip() or "in use"
    # Undetermined: stderr first, because that is where the cause is.
    return "unknown", (result.stderr.strip() or result.stdout.strip()
                       or f"exit {result.returncode}")


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

    The refusals this site raises are REPORT, and that is a decision about the
    COMMAND rather than about each cause (E-2162): removing a directory is the
    user's act, so a guard that will not clear it hands the question to them
    whatever stopped it. Each message also offers `--force`, and that sentence
    is the one an agent must never be handed — told about the flag, it uses it,
    which is the opposite of stopping to ask, and here the thing it would be
    forcing past is a live session's working directory. So `--force` travels in
    `human_remedy`: dropped from the agent's copy, re-joined for a person,
    whose text is the sentence that was always there.

    The undetermined case is the exception, and deliberately: it relays Go's
    own refusal instead of wrapping it, so neither the Endless-side sentence
    nor the `--force` it offered survives. Go classified that failure at the
    site that knows what failed, to the same verdict — "whether to remove a
    worktree Endless could not prove is idle" — and saying it twice, once in
    each language, is how the two drift apart.
    """
    verdict, detail = _worktree_in_use_probe(worktree_path)
    if verdict == "free":
        return
    if verdict == "no-binary":
        raise agent_help.report(
            f"Cannot verify whether {worktree_path} is in use: endless-go is "
            f"not on PATH. Nothing was dropped.",
            "whether to drop this worktree, which Endless cannot prove is "
            "idle without the binary the user has to install",
            text=(f"Cannot verify whether this worktree is in use: endless-go "
                  f"is not on PATH.\n{worktree_path}\n"
                  f"Install it (`just install`)"),
            human_remedy="or use --force to drop anyway.",
        )
    if verdict == "unknown":
        # endless-go's own refusal, already classified by Go: worktreecmd's
        # undetermined path raises refusal.Report with the decision "whether to
        # remove a worktree Endless could not prove is idle" — the same verdict
        # this site would reach, written by the code that knows what failed. So
        # it relays, verbatim, with no second directive to contradict the
        # first. Reaching Go's words here at all is what the probe's stdout/
        # stderr fix bought; before it, this site had `undetermined` and
        # nothing else.
        raise agent_help.relay(detail, exit_code=1)
    raise agent_help.report(
        f"Refusing to drop {worktree_path}: it is in use ({detail}). Nothing "
        f"was dropped.",
        "whether to remove a worktree a live session or process is using — it "
        "orphans that session's working directory",
        text=(f"Refusing to drop a worktree that is in use: {worktree_path}\n"
              f"  {detail}\n\n"
              f"Dropping removes the directory out from under whatever is "
              f"standing in it, orphaning that session's cwd.\n"
              f"If the goal is to discard diverged history rather than the "
              f"directory, reset or rebase the branch in place — the worktree "
              f"survives and the session keeps working."),
        human_remedy="Use --force only once you know nothing is using it.",
    )


def drop_worktree(name_or_path: str, force: bool) -> None:
    """Remove a worktree explicitly. Refuses in-use/modified/foreign without --force.

    Every refusal below is REPORT, decided per COMMAND rather than per cause
    (E-2162): `drop` deletes a directory, which is the user's act, so anything
    that stops it is theirs to clear. That includes the one refusal the TSV
    left CONDITIONAL — a name that matches no worktree — because the branch
    that would have been NO-REPORT is "retry with a different target", and
    substituting a different directory to delete is the last thing this command
    may do on its own.

    The escapes these messages offer — `--force`, and `git worktree remove` —
    are in `human_remedy` for the reason `human_remedy` exists: named in a
    refusal, a bypass is what an agent reaches for, and what it would be
    bypassing here is the guard between a live session and `rm -rf`.
    """
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
        raise agent_help.report(
            f"No worktree matches: {name_or_path}",
            "which worktree was meant — a drop must not be retried against a "
            "different directory than the one named",
        )
    if target["state"] == "main":
        raise agent_help.report(
            "Refusing to drop the main checkout.",
            "what was meant, since the main checkout can never be dropped",
        )

    worktree_path = Path(target["path"])

    if not force:
        if target["state"] == "foreign":
            raise agent_help.report(
                f"Refusing to drop {worktree_path}: it has no endless "
                f"companion, so Endless did not create it. Nothing was "
                f"dropped.",
                "whether to remove a worktree Endless did not create",
                text=(f"Refusing to drop foreign worktree (no endless "
                      f"companion): {worktree_path}"),
                human_remedy=("Use --force to drop anyway, or remove via "
                              "'git worktree remove'."),
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
                raise agent_help.report(
                    f"Refusing to drop {worktree_path}: it has uncommitted "
                    f"changes, which removing it would destroy. Nothing was "
                    f"dropped.",
                    "whether to remove a worktree holding uncommitted work",
                    text=(f"Worktree has uncommitted changes: "
                          f"{worktree_path}\n"
                          f"Commit or discard them,"),
                    human_remedy="or use --force.",
                )
        except subprocess.CalledProcessError as e:
            # A relayed git failure, narrowed by the site: the safety check
            # that would have decided whether this drop is safe did not run,
            # and a drop nobody cleared is the user's.
            raise agent_help.relay_foreign(
                agent_help.report(
                    f"git status failed in {worktree_path}, so the drop's "
                    f"uncommitted-work check never ran. Nothing was dropped.",
                    "whether to remove a worktree whose safety check could not "
                    "be completed",
                    text="git status check failed:",
                ),
                str(e.stderr or e),
            )

    cmd = ["worktree", "remove"]
    if force:
        cmd.append("--force")
    cmd.append(str(worktree_path))
    try:
        _git_run(cmd, cwd=main_root)
    except subprocess.CalledProcessError as e:
        # The last step, and the one that also runs under --force. A relayed
        # git failure, narrowed by the site for the same reason as the rest of
        # drop: the removal did not happen, and whether to pursue it is the
        # user's.
        raise agent_help.relay_foreign(
            agent_help.report(
                f"git worktree remove failed for {worktree_path}; it was NOT "
                f"dropped.",
                "whether to pursue removing this worktree, which git refused",
                text="git worktree remove failed:",
            ),
            str(e.stderr or e),
        )

    click.echo(
        click.style("•", fg="cyan")
        + f" Dropped worktree: {worktree_path}"
    )

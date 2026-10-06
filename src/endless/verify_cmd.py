"""CLI implementation for `endless task verify` (E-1603).

Thin wrapper around the `endless-go verify` subcommand — the verification
runner. This module holds no verification logic: discovery, per-run isolation
(temp working dir + temp HOME/XDG_CONFIG_HOME), the own-task-only refusal,
running the suite's checks, normalizing to CTRF, and the pass/fail exit code all
live in Go (internal/verifycmd). Here we only resolve WHICH task to verify and
WHERE to run it, then pass stdout/stderr and the exit code straight through.

Both resolutions are deliberately the Python side's job: it owns
session/worktree context. The Go runner stays a pure function of a task id and
a working directory, so nothing about sessions, worktrees or projects has to
exist inside it.

So is git (E-2243). Every PASSING run is recorded: its CTRF report is moved out
of the runner's cache onto the main checkout, as
`.endless/tasks/e-NNNN/verify-<UTC timestamp>-<short sha>.ctrf.json`, and
committed there by `endless-go event commit-verify-report`. For the SHA in that
name to be exactly the code the suite tested, a run is refused before anything
executes while the tree it would test has uncommitted changes. The runner
itself never learns any of this, so it stays extractable as a standalone tool.
"""

import re
import shutil
import subprocess
from pathlib import Path

from endless import agent_help
from endless.event_bridge import _display_path, _resolve_endless_go
from endless.main_commit import sanitized_git_env
from endless.task_cmd import _current_session_task_id

_WORKTREE_RE = re.compile(r"/\.endless/worktrees/e-(\d+)(?:/|$)")

# The suffix every per-run report carries, in the cache and on main. Mirrors
# ReportFileSuffix in internal/verifycmd/report.go.
_REPORT_SUFFIX = ".ctrf.json"

# The single report file the runner wrote before E-2243 gave each run its own.
# Cleared with the task's failed reports so an upgraded machine is left tidy.
_LEGACY_REPORT = "ctrf.json"

# What Endless itself writes into a worktree and commits on its own schedule.
# These are not the task's code, so they do not make a tree "dirty" for the
# purpose of naming what a run tested — the same set the guide's `git add`
# for committing your work excludes.
_ENDLESS_MANAGED = (".endless/verbs.jsonl", ".endless/db-ledger/")


def run_verify(item_id: int | None, keep: bool) -> None:
    """Entry point bound by cli.py.

    `item_id` is the E- stripped task number (via the TASK_ID click type), or
    None to resolve the task from context. Shells to
    `endless-go verify [--keep] E-<id>` and exits with its return code.
    """
    resolved = item_id
    if resolved is None:
        resolved = _resolve_task_id()
    if resolved is None:
        raise agent_help.no_report(
            "No task id was given and neither this session nor the current "
            "directory names one, so nothing was verified.",
            "Retry naming the task: `endless task verify E-<id>`",
            text=("no task id given, and neither this session nor the current "
                  "directory names one; pass an explicit task id, e.g. "
                  "`endless task verify E-101`."),
        )

    task_id = f"E-{resolved}"
    binary = _resolve_endless_go()
    run_dir = _run_dir(resolved)
    tree = Path(run_dir) if run_dir is not None else Path.cwd()

    _require_clean_tree(task_id, tree)
    sha = _head_sha(tree)

    cmd = [binary, "verify"]
    if keep:
        cmd.append("--keep")
    cmd.append(task_id)

    result = subprocess.run(cmd, cwd=run_dir)
    # stdout and stderr were inherited, so the runner's own output — a passing
    # suite, a failing check, its own classified refusal — is already on this
    # terminal. A failing run's report stays in the cache, where the runner
    # already named it. Only a pass has more to do: record it.
    if result.returncode == 0:
        _record_passing_run(binary, task_id, resolved, sha, tree)
    agent_help.passthrough_exit(result.returncode)


def _git(tree: Path, *args: str) -> subprocess.CompletedProcess:
    """`git -C tree <args>`, captured, with the git-locating env stripped."""
    return subprocess.run(
        ["git", "-C", str(tree), *args],
        capture_output=True, text=True, env=sanitized_git_env(),
    )


def _dirty_paths(tree: Path) -> list[str]:
    """Paths `git status` lists in tree, minus the files Endless writes there.

    Gitignored files never appear, so they never count. Raises the refusal for
    a tree git cannot read at all: a run is recorded as a commit, so verify has
    nothing to work with outside a repository.
    """
    res = _git(tree, "status", "--porcelain=v1", "-z", "--untracked-files=all")
    if res.returncode != 0:
        raise agent_help.report(
            f"Cannot read git status in {_display_path(str(tree))}, so nothing "
            f"was verified: {(res.stderr or res.stdout).strip()}",
            "whether to put this project under git — `endless task verify` "
            "records every passing run as a commit and has nowhere to write "
            "without one",
        )
    paths: list[str] = []
    entries = iter(res.stdout.split("\0"))
    for entry in entries:
        if len(entry) < 4:
            continue
        if entry[0] in "RC":
            # -z puts a rename's source in the next entry; the destination,
            # already in this one, is the path that matters.
            next(entries, None)
        path = entry[3:]
        if not path.startswith(_ENDLESS_MANAGED):
            paths.append(path)
    return paths


def _require_clean_tree(task_id: str, tree: Path) -> None:
    """Refuse before anything runs when tree has uncommitted work (E-2243).

    A recorded run's filename names the commit it tested. With uncommitted
    changes the suite would test something no commit holds, and the record
    would name the wrong code.
    """
    dirty = _dirty_paths(tree)
    if not dirty:
        return
    listing = "\n".join(f"  {p}" for p in dirty)
    raise agent_help.no_report(
        f"{task_id}'s tree has uncommitted changes, so nothing was verified: a "
        f"recorded run must name exactly the commit it tested.",
        "Commit these changes, then verify again",
        text=(f"{task_id}: uncommitted changes in "
              f"{_display_path(str(tree))}, so nothing was verified — a "
              f"recorded run must name exactly the commit it tested. Commit "
              f"these, then verify again:\n{listing}"),
        detail=listing,
    )


def _head_sha(tree: Path) -> str:
    """The short SHA of tree's HEAD: the code a run in tree tests."""
    res = _git(tree, "rev-parse", "--short", "HEAD")
    if res.returncode != 0:
        raise agent_help.report(
            f"Cannot resolve HEAD in {_display_path(str(tree))}, so nothing "
            f"was verified: {(res.stderr or res.stdout).strip()}",
            "how to give this checkout a commit to verify — a run is recorded "
            "against the commit it tested",
        )
    return res.stdout.strip()


def _record_passing_run(binary: str, task_id: str, task_num: int, sha: str,
                        tree: Path) -> None:
    """Move a passing run's report onto main, commit it, clear the cache.

    MOVED, never copied: once the task passes, nothing of its runs is left in
    the cache. Its earlier failed reports go too — they mattered only until
    now. If the move or the commit fails, the suite's pass is not enough: the
    command fails, naming where the report now sits, and the failed reports are
    left in place.
    """
    report_dir = _report_dir(binary, task_id)
    reports = sorted(report_dir.glob(f"*{_REPORT_SUFFIX}"))
    if not reports:
        raise agent_help.fault(
            f"{task_id}'s suite passed, but the runner left no report in "
            f"{_display_path(str(report_dir))} to record.",
        )
    newest = reports[-1]
    stem = newest.name[: -len(_REPORT_SUFFIX)]

    main = _main_checkout(tree)
    if main is None:
        raise agent_help.report(
            f"{task_id}'s suite passed, but no main checkout was found above "
            f"{_display_path(str(tree))} to record it on. The report is still "
            f"at {_display_path(str(newest))}.",
            "where this project's main checkout is — a passing run is recorded "
            "there",
        )
    rel = f".endless/tasks/e-{task_num}/verify-{stem}-{sha}{_REPORT_SUFFIX}"
    dest = main / rel

    try:
        dest.parent.mkdir(parents=True, exist_ok=True)
        shutil.move(str(newest), str(dest))
    except OSError as e:
        raise agent_help.report(
            f"{task_id}'s suite passed, but its report could not be moved onto "
            f"main: {e}. The report is still at {_display_path(str(newest))}.",
            "how to make the main checkout writable so the passing run can be "
            "recorded",
        )

    res = subprocess.run(
        [binary, "event", "commit-verify-report", "--project-root", str(main),
         "--path", rel, "--task", task_id],
        capture_output=True, text=True,
    )
    if res.returncode != 0:
        raise agent_help.report(
            f"{task_id}'s suite passed, but its report could not be committed "
            f"on main, so the run is not recorded. The report is at "
            f"{_display_path(str(dest))}, uncommitted.",
            "how to clear the git failure on the main checkout so the passing "
            "run can be committed",
            detail=(res.stderr or res.stdout).strip(),
        )

    for leftover in [*report_dir.glob(f"*{_REPORT_SUFFIX}"),
                     report_dir / _LEGACY_REPORT]:
        leftover.unlink(missing_ok=True)
    print(f"CTRF: {_display_path(str(dest))}")


def _report_dir(binary: str, task_id: str) -> Path:
    """Where the runner writes task_id's reports — asked, not re-derived, so
    the cache location has one definition (internal/verifycmd reportDir)."""
    res = subprocess.run([binary, "verify", "--report-dir", task_id],
                         capture_output=True, text=True)
    if res.returncode != 0:
        raise agent_help.relay(res.stderr or res.stdout,
                               exit_code=res.returncode)
    return Path(res.stdout.strip())


def _resolve_task_id() -> int | None:
    """The task to verify when the caller named none.

    Two sources, in order:

      1. The current session's active task. This is the primary one and the
         documented meaning of a bare `endless task verify`: verify what you
         are working on. It reads the database, so it is best-effort — a
         self-dev worktree with no `--db` refuses that read, and a refusal must
         not become the answer to a question the cwd can also answer.
      2. The task whose worktree cwd is inside. Structural, needs no database,
         and correct by construction: a suite is a proof about a checkout, and
         a task worktree's path names its task.

    Same two sources, same order, as `just land` — minus the tmux hop, because
    Python resolves the session directly rather than shelling out for it.
    """
    task_id = _session_task_id()
    if task_id is not None:
        return task_id
    return _cwd_task_id()


def _session_task_id() -> int | None:
    """The current session's active task, or None if it cannot be read.

    Every failure is None, not an exception: this is one of two ways to answer
    the question, and a database that refuses to open is a reason to try the
    other one rather than to give up.
    """
    try:
        return _current_session_task_id()
    except Exception:
        return None


def _cwd_task_id(cwd: Path | None = None) -> int | None:
    """The task whose worktree contains cwd, from the path alone."""
    match = _WORKTREE_RE.search(str(cwd if cwd is not None else Path.cwd()))
    return int(match.group(1)) if match else None


def _run_dir(task_id: int) -> str | None:
    """Where to run the suite: that task's worktree when it exists, else cwd.

    A suite is a pre-land gate. It proves the CANDIDATE tree, so it has to run
    against the candidate tree — asking for a task from the main checkout would
    otherwise discover main's copy of that suite and run it against code the
    task has not landed yet. Doing this here rather than making the caller `cd`
    first is what makes one command sufficient from anywhere in the project.

    Returns None (meaning "inherit cwd") when the worktree does not exist: a
    task whose worktree was reaped, or a project not using worktrees at all,
    still verifies from wherever the caller is standing.
    """
    root = _main_checkout()
    if root is None:
        return None
    worktree = root / ".endless" / "worktrees" / f"e-{task_id}"
    return str(worktree) if worktree.is_dir() else None


def _main_checkout(cwd: Path | None = None) -> Path | None:
    """The main checkout above cwd, whether cwd is in it or in a worktree.

    Keyed on the path shape rather than git, because the caller may not be
    inside a repository at all and because the worktree layout is Endless's own
    convention: <root>/.endless/worktrees/e-NNN. Falls back to walking up for a
    .endless directory when cwd is not inside a worktree.
    """
    start = (cwd if cwd is not None else Path.cwd()).resolve()
    match = _WORKTREE_RE.search(str(start))
    if match:
        return Path(str(start)[: match.start()])
    for candidate in [start, *start.parents]:
        if (candidate / ".endless").is_dir():
            return candidate
    return None

"""Fixture driver for E-1957's verification suite.

The suite is shell; the code under test is Python. This is the seam between
them. It builds one throwaway git repository per conflict class, drives the
REAL land helpers against it — `_rebase_conflict_message` is the function both
of land's conflict handlers call, `_rehearse_land_rebase` is what `land
--dry-run` calls — and prints `key=value` lines the shell asserts on.

Nothing here is a stub and nothing re-implements the code under test. It builds
git state, calls the shipped function, and reports what came back.

Usage:
    driver.py message <scenario> <workdir>   build, conflict, print the message
    driver.py classify <scenario> <workdir>  ...then print the classification
    driver.py rehearse <scenario> <workdir>  build, rehearse, print the verdict

Scenarios are the five conflict classes, plus `clean` for a branch that rebases
without conflicting.
"""

import shutil
import subprocess
import sys
from pathlib import Path

from endless import land_conflict
from endless.worktree_cmd import _rebase_conflict_message, _rehearse_land_rebase


def run(cmd, cwd, check=True):
    return subprocess.run(
        cmd, cwd=str(cwd), check=check, capture_output=True, text=True
    )


def init(path: Path) -> Path:
    """A repository and a sandbox, both from scratch.

    The sandbox matters as much as the repo: a capture left there by an earlier
    invocation would make "did this write a capture?" answer about the previous
    run. Wiping both is what lets the rehearsal check assert `capture_written`
    is false and mean it.
    """
    from endless import config

    shutil.rmtree(path, ignore_errors=True)
    shutil.rmtree(config.sandbox_root(path.name), ignore_errors=True)
    path.mkdir(parents=True, exist_ok=True)
    run(["git", "init", "-q", "-b", "main"], path)
    run(["git", "config", "user.email", "verify@example.com"], path)
    run(["git", "config", "user.name", "verify"], path)
    run(["git", "config", "commit.gpgsign", "false"], path)
    return path


def commit(repo: Path, msg: str, files: dict) -> str:
    for rel, content in files.items():
        p = repo / rel
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(content)
    run(["git", "add", "-A"], repo)
    run(["git", "commit", "-q", "-m", msg], repo)
    return run(["git", "rev-parse", "HEAD"], repo).stdout.strip()


# Padding keeps a deletion at the top of a file and an edit at the bottom in
# separate hunks, which is the shape the E-1943 incident had.
PAD = "\n".join(f"# filler {i}" for i in range(40))


def build_supersession(repo: Path) -> tuple[str, list[str]]:
    """The incident: main deletes two names the branch's side still calls."""
    commit(repo, "base", {"sync.py": f'''BINARY_SOURCE_PATHS = ["cmd/endless-go"]


def _refuse_if_behind_base(worktree, base):
    raise SystemExit("behind base")


{PAD}


def sync_worktrees(apply):
    return None
'''})
    run(["git", "checkout", "-q", "-b", "task/1943"], repo)
    commit(repo, "E-1943: print each binary source path",
           {"sync.py": f'''BINARY_SOURCE_PATHS = ["cmd/endless-go"]


def _refuse_if_behind_base(worktree, base):
    raise SystemExit("behind base")


{PAD}


def sync_worktrees(apply):
    _refuse_if_behind_base(".", "main")
    for p in BINARY_SOURCE_PATHS:
        print(p)
    return None
'''})
    run(["git", "checkout", "-q", "main"], repo)
    commit(repo, "E-1941: delete the behind-base refusal", {"sync.py": f'''{PAD}


def sync_worktrees(apply):
    return rebuild_binary(None)
'''})
    run(["git", "checkout", "-q", "task/1943"], repo)
    return "task/1943", ["rebase", "main"]


def build_overlap(repo: Path) -> tuple[str, list[str]]:
    """Two intentional edits to one line, using only names both sides keep."""
    commit(repo, "base", {"app.py": "def total(rows):\n    return sum(rows)\n"})
    run(["git", "checkout", "-q", "-b", "task/2000"], repo)
    commit(repo, "E-2000: skip blanks",
           {"app.py": "def total(rows):\n    return sum(r for r in rows if r)\n"})
    run(["git", "checkout", "-q", "main"], repo)
    commit(repo, "E-1999: round the total",
           {"app.py": "def total(rows):\n    return round(sum(rows), 2)\n"})
    run(["git", "checkout", "-q", "task/2000"], repo)
    return "task/2000", ["rebase", "main"]


def build_auto_file(repo: Path) -> tuple[str, list[str]]:
    """A conflict confined to files endless writes and commits itself."""
    rel = ".endless/verbs.jsonl"
    commit(repo, "base", {rel: '{"value":"alpha"}\n'})
    run(["git", "checkout", "-q", "-b", "task/3000"], repo)
    commit(repo, "Endless: bundle worktree verb additions",
           {rel: '{"value":"alpha"}\n{"value":"branch"}\n'})
    run(["git", "checkout", "-q", "main"], repo)
    commit(repo, "Endless: bundle worktree verb additions",
           {rel: '{"value":"alpha"}\n{"value":"main"}\n'})
    run(["git", "checkout", "-q", "task/3000"], repo)
    return "task/3000", ["rebase", "main"]


def build_already_landed(repo: Path) -> tuple[str, list[str]]:
    """A commit whose patch main already carries under a different SHA.

    Reached only through land's Step 3.7 form. A plain `git rebase main`
    compares patch ids and SKIPS such a commit, so it can never stop on one;
    `--onto <base> <sha>` has nothing upstream to compare against and replays
    every commit.
    """
    commit(repo, "base", {"lib.py": "one\ntwo\nthree\n"})
    run(["git", "checkout", "-q", "-b", "task/4000"], repo)
    fork = run(["git", "rev-parse", "HEAD"], repo).stdout.strip()
    commit(repo, "E-4000: rename two", {"lib.py": "one\nTWO\nthree\n"})
    commit(repo, "E-4000: add four", {"lib.py": "one\nTWO\nthree\nfour\n"})
    run(["git", "checkout", "-q", "main"], repo)
    # Different subject so the two are distinct commit OBJECTS: same tree, same
    # parent, same author and same second would hash to one identical commit.
    commit(repo, "E-4000: rename two (landed via another branch)",
           {"lib.py": "one\nTWO\nthree\n"})
    commit(repo, "E-4001: rename it again", {"lib.py": "one\nSECOND\nthree\n"})
    run(["git", "checkout", "-q", "task/4000"], repo)
    return "task/4000", ["rebase", "--onto", "main", fork]


def build_ledger_orphan(repo: Path) -> tuple[str, list[str]]:
    """A branch based on a ledger commit main has since appended to.

    The branch holds a byte-PREFIX of main's segment, never a byte-identical
    copy — which is what "main already has this content" means for an
    append-only JSONL ledger once main has recorded one more event.
    """
    seg = ".endless/db-ledger/db-entries-abcd-000001.jsonl"
    commit(repo, "root", {"README.md": "hello\n"})
    commit(repo, "Endless: record ledger entry", {seg: '{"seq":1}\n'})
    run(["git", "checkout", "-q", "-b", "task/5000"], repo)
    commit(repo, "E-5000: change the greeting", {"README.md": "hello, branch\n"})
    run(["git", "checkout", "-q", "main"], repo)
    (repo / seg).write_text('{"seq":1}\n{"seq":2}\n')
    run(["git", "add", "-A"], repo)
    run(["git", "commit", "-q", "--amend", "-m", "Endless: record ledger entry"], repo)
    commit(repo, "E-4999: change the greeting too", {"README.md": "hello, main\n"})
    run(["git", "checkout", "-q", "task/5000"], repo)
    return "task/5000", ["rebase", "main"]


def build_clean(repo: Path) -> tuple[str, list[str]]:
    """Two branches that touch different files: nothing to conflict over."""
    commit(repo, "base", {"a.txt": "one\n"})
    run(["git", "checkout", "-q", "-b", "task/8000"], repo)
    commit(repo, "E-8000: add b", {"b.txt": "two\n"})
    run(["git", "checkout", "-q", "main"], repo)
    commit(repo, "E-7999: add c", {"c.txt": "three\n"})
    run(["git", "checkout", "-q", "task/8000"], repo)
    return "task/8000", ["rebase", "main"]


SCENARIOS = {
    "supersession": build_supersession,
    "overlap": build_overlap,
    "auto-file": build_auto_file,
    "already-landed": build_already_landed,
    "ledger-orphan": build_ledger_orphan,
    "clean": build_clean,
}


def build(scenario: str, workdir: Path) -> tuple[Path, str, list[str]]:
    repo = init(workdir / scenario)
    branch, rebase_argv = SCENARIOS[scenario](repo)
    return repo, branch, rebase_argv


def emit(**fields):
    for k, v in fields.items():
        print(f"{k}={v}")


def cmd_message(scenario: str, workdir: Path) -> int:
    """Drive land's real conflict handler: conflict, build the message (which
    captures the evidence), abort — in that order, as land does."""
    repo, branch, rebase_argv = build(scenario, workdir)
    res = run(["git", *rebase_argv], repo, check=False)
    if res.returncode == 0:
        emit(conflict="no", repo=repo)
        return 0
    # git's stderr is threaded through exactly as land threads it, so the
    # message under test is the one an operator actually sees — including the
    # quoted `git rebase --continue` hint it now has to answer.
    msg = _rebase_conflict_message(
        repo, "main", phase="rebasing your branch onto main",
        stderr=res.stderr)
    run(["git", "rebase", "--abort"], repo, check=False)
    emit(conflict="yes", repo=repo, branch=branch,
         evidence_file=land_conflict.evidence_path(repo))
    print("---message---")
    print(msg)
    return 0


def cmd_classify(scenario: str, workdir: Path) -> int:
    """...then read the capture back and classify it, as `diagnose` does."""
    if cmd_message(scenario, workdir) != 0:
        return 1
    repo = workdir / scenario
    ev = land_conflict.load_evidence(repo)
    if ev is None:
        emit(loaded="no")
        return 1
    cl = land_conflict.classify(ev, repo)
    emit(loaded="yes",
         rebase_head=ev.rebase_head,
         hunks=len(ev.hunks),
         klass=cl.klass,
         proven=str(cl.proven).lower(),
         prescribes=str(bool(cl.prescription)).lower(),
         symbols=",".join(cl.superseded_symbols))
    print("---rendered---")
    print(land_conflict.render_human(ev, cl))
    return 0


def cmd_rehearse(scenario: str, workdir: Path) -> int:
    """Drive what `land --dry-run` drives, then prove nothing was left behind."""
    repo, branch, _ = build(scenario, workdir)
    before = {
        "branches": run(["git", "branch", "--format=%(refname)"], repo).stdout,
        "head": run(["git", "rev-parse", "HEAD"], repo).stdout.strip(),
        "base": run(["git", "rev-parse", "main"], repo).stdout.strip(),
        "branch_tip": run(["git", "rev-parse", branch], repo).stdout.strip(),
    }
    ev = _rehearse_land_rebase(repo, branch, "main", repo, "E-1957")
    after = {
        "branches": run(["git", "branch", "--format=%(refname)"], repo).stdout,
        "head": run(["git", "rev-parse", "HEAD"], repo).stdout.strip(),
        "base": run(["git", "rev-parse", "main"], repo).stdout.strip(),
        "branch_tip": run(["git", "rev-parse", branch], repo).stdout.strip(),
    }
    checkouts = run(
        ["git", "worktree", "list", "--porcelain"], repo
    ).stdout.count("worktree ")

    emit(repo=repo,
         conflict="yes" if ev is not None else "no",
         rehearsal_flag=str(bool(ev and ev.rehearsal)).lower(),
         klass=land_conflict.classify(ev, repo).klass if ev else "",
         unchanged=str(before == after).lower(),
         checkouts=checkouts,
         throwaway_branches=after["branches"].count("endless/land-rehearsal/"),
         capture_written=str(land_conflict.load_evidence(repo) is not None).lower())
    return 0


def main(argv: list[str]) -> int:
    if len(argv) != 3:
        print(__doc__, file=sys.stderr)
        return 2
    verb, scenario, workdir = argv
    if scenario not in SCENARIOS:
        print(f"unknown scenario {scenario!r}", file=sys.stderr)
        return 2
    handlers = {
        "message": cmd_message, "classify": cmd_classify, "rehearse": cmd_rehearse,
    }
    if verb not in handlers:
        print(f"unknown verb {verb!r}", file=sys.stderr)
        return 2
    return handlers[verb](scenario, Path(workdir))


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))

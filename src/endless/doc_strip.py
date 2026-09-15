"""Strip Endless-authored document-mirror commits off task branches (E-2137).

A mirror — `.endless/tasks/e-NNNN/plan.md` and its siblings — is a projection of
a database column. It used to be committed on the task's BRANCH, where it waited
for a land. Measured over 133 worktrees on 2026-09-14, with the content-based
probe that drives the diamond marker: 56 worktrees held 139 genuinely unlanded
commits, 123 of them were mirrors, and 44 of the 56 were unlanded ONLY because
of them. So 79% of the worktrees that read as holding unlanded work did so
because Endless put its own bookkeeping on their branch.

Endless no longer writes there. This removes what it already wrote.

# Why it is not enough to let them land naturally

Consolidating mirrors under `.endless/tasks/e-NNNN/` renames a directory on
main, and git's directory-rename detection then misplaces a branch's NEW file at
an old path. Tested on a scratch repository, not assumed: with
`.endless/plans/E-0.md` renamed to `.endless/tasks/e-0/plan.md` on main, a branch
adding `.endless/plans/E-2.md` rebased to `.endless/tasks/e-0/E-2.md` — one
task's content inside another task's directory. Git raised a conflict rather
than doing it silently, but that IS the resolution it offers, and
`Endless: add plan for E-N` is the most common commit shape on these branches.
`.endless/plans/` does not rename to one directory; it fans out to hundreds, and
git picks whichever it detected.

Removing the commits removes the input entirely.

# Why it does not skip worktrees that are in use

`worktree sync` skips them, and copying that rule here would be wrong, because
its reason does not transfer. `sync` REBASES ONTO A NEWER MAIN, which rewrites
every file under a running agent — the E-2090 hazard its gate exists for. This
drops specific commits WITHOUT MOVING THE BASE: the only files whose content
changes are the mirrors, which nothing reads and no worktree materializes any
more. Every other file in the working tree is byte-identical before and after.

Skipping in-use worktrees would leave mirror commits on exactly the branches
most likely to be long-lived, for no safety gained.

The real hazard is narrower — a session COMMITTING while the rewrite is in
flight would have its commit stranded on the old tip — and it is solved where
such races are always solved, with a compare-and-swap on the branch ref. The
update fails if the ref moved since it was read, so this can never clobber a
commit it did not see.

One condition still forces a skip: a worktree MID-OPERATION, its own rebase,
merge or cherry-pick in progress. The ref is not ours to move then.
"""

import os
import subprocess
from pathlib import Path

import click

from endless import doc_mirror
from endless.worktree_cmd import doc_mirror_content


# The message a commit built here carries when the original cannot be read back.
# It never appears in practice; it exists so a failure to read `%B` produces a
# commit with a subject rather than an exception halfway through a rewrite.
_FALLBACK_SUBJECT = "Endless: (subject unavailable)"


def _git(args: list[str], cwd: Path, check: bool = True,
         env: dict | None = None) -> subprocess.CompletedProcess:
    """Run git, capturing output. Separate from worktree_cmd._git_run because
    this module needs to pass a per-call environment (commit-tree's author and
    committer identity) and to write through a scratch index.
    """
    full_env = None
    if env is not None:
        full_env = {**os.environ, **env}
    return subprocess.run(
        ["git", *args], cwd=str(cwd), capture_output=True, text=True,
        check=check, env=full_env,
    )


def _out(args: list[str], cwd: Path) -> str:
    return _git(args, cwd).stdout.strip()


# --- what a branch holds ----------------------------------------------------

def _task_branches(root: Path) -> list[str]:
    """Every local task branch, newest ref first is not needed — ordered by name
    so a dry run reads the same way twice.

    `refs/heads/task/*` covers both the constructed `task/<id>` name and any
    surviving `task/<id>-<slug>` from before ED-1587 renamed them.
    """
    out = _out(["for-each-ref", "--format=%(refname:short)", "refs/heads/task"], root)
    return sorted(ln.strip() for ln in out.splitlines() if ln.strip())


def _merge_base(base: str, branch: str, root: Path) -> str | None:
    res = _git(["merge-base", base, branch], root, check=False)
    return res.stdout.strip() if res.returncode == 0 else None


def _mirror_commits(root: Path, since: str, branch: str) -> list[str]:
    """SHAs in `since..branch` that touch any mirror path, oldest first."""
    res = _git(
        ["log", "--reverse", "--format=%H", f"{since}..{branch}",
         "--", *doc_mirror.MIRROR_PATHSPECS],
        root, check=False,
    )
    if res.returncode != 0:
        return []
    return [ln.strip() for ln in res.stdout.splitlines() if ln.strip()]


def _branch_commits(root: Path, since: str, branch: str) -> list[str]:
    """Every SHA in `since..branch`, oldest first."""
    res = _git(["log", "--reverse", "--format=%H", f"{since}..{branch}"],
               root, check=False)
    if res.returncode != 0:
        return []
    return [ln.strip() for ln in res.stdout.splitlines() if ln.strip()]


def _has_merge_commit(root: Path, since: str, branch: str) -> bool:
    res = _git(["log", "--merges", "--format=%H", f"{since}..{branch}"],
               root, check=False)
    return bool(res.stdout.strip())


def _mirror_paths_touched(root: Path, since: str, branch: str) -> list[str]:
    """Every mirror path any commit in `since..branch` touched, deduplicated.

    Asked of the whole range rather than per commit because what the rewrite
    needs is the SET of paths to pin back to their base state; which commit
    touched which is not part of the answer.
    """
    res = _git(
        ["log", "--format=", "--name-only", f"{since}..{branch}",
         "--", *doc_mirror.MIRROR_PATHSPECS],
        root, check=False,
    )
    seen: dict[str, None] = {}
    for ln in res.stdout.splitlines():
        rel = ln.strip()
        if rel and doc_mirror.is_mirror_path(rel):
            seen[rel] = None
    return sorted(seen)


def _branch_file(root: Path, branch: str, rel: str) -> str | None:
    res = _git(["show", f"{branch}:{rel}"], root, check=False)
    return res.stdout if res.returncode == 0 else None


# --- the rewrite ------------------------------------------------------------

def _base_entries(root: Path, base_rev: str, paths: list[str]) -> dict[str, str]:
    """`ls-tree` entries at the merge base, keyed by path.

    The value is the `<mode>,<sha>` pair `update-index --cacheinfo` wants. A
    path absent from the base is absent from the map, which is how the rewrite
    tells "restore this" from "remove this".
    """
    if not paths:
        return {}
    res = _git(["ls-tree", "-z", base_rev, "--", *paths], root, check=False)
    entries: dict[str, str] = {}
    for record in res.stdout.split("\0"):
        if not record.strip():
            continue
        meta, _, path = record.partition("\t")
        fields = meta.split()
        if len(fields) < 3:
            continue
        mode, _kind, sha = fields[0], fields[1], fields[2]
        entries[path] = f"{mode},{sha}"
    return entries


def _rewrite_branch(
    root: Path, branch: str, base_rev: str, paths: list[str], index_path: Path,
) -> str:
    """Replay `base_rev..branch` with every mirror path pinned to its base state.

    Returns the new tip SHA, which is `base_rev` when every commit on the branch
    was mirror-only.

    Works entirely in plumbing against a scratch index, so it touches neither the
    branch's worktree nor the main checkout's own index. That is what lets it run
    against a branch somebody is actively working in — `git rebase` would need a
    clean worktree it has no right to demand.

    A commit whose rewritten tree equals its new parent's is DROPPED. That is
    exactly the mirror-only commit, identified by what it turns out to contain
    rather than by its subject line, so a commit that bundled a mirror with real
    work keeps the real work and loses only the mirror.
    """
    base_entries = _base_entries(root, base_rev, paths)
    parent = base_rev
    parent_tree = _out(["rev-parse", f"{base_rev}^{{tree}}"], root)

    for sha in _branch_commits(root, base_rev, branch):
        env = {"GIT_INDEX_FILE": str(index_path)}
        _git(["read-tree", f"{sha}^{{tree}}"], root, env=env)
        for rel in paths:
            entry = base_entries.get(rel)
            if entry is None:
                _git(["update-index", "--force-remove", "--", rel], root, env=env)
            else:
                # The comma form takes mode, blob and path as ONE argument.
                _git(["update-index", "--add", "--cacheinfo", f"{entry},{rel}"],
                     root, env=env)
        new_tree = _git(["write-tree"], root, env=env).stdout.strip()

        if new_tree == parent_tree:
            continue  # the commit contained nothing but mirrors

        message = _git(["log", "-1", "--format=%B", sha], root, check=False).stdout
        if not message.strip():
            message = _FALLBACK_SUBJECT
        ident = _git(
            ["log", "-1", "--format=%an%x00%ae%x00%aI%x00%cn%x00%ce%x00%cI", sha],
            root, check=False,
        ).stdout.rstrip("\n").split("\0")
        commit_env = {}
        if len(ident) == 6:
            commit_env = {
                "GIT_AUTHOR_NAME": ident[0], "GIT_AUTHOR_EMAIL": ident[1],
                "GIT_AUTHOR_DATE": ident[2], "GIT_COMMITTER_NAME": ident[3],
                "GIT_COMMITTER_EMAIL": ident[4], "GIT_COMMITTER_DATE": ident[5],
            }
        made = subprocess.run(
            ["git", "commit-tree", new_tree, "-p", parent],
            cwd=str(root), input=message, capture_output=True, text=True,
            check=True, env={**os.environ, **commit_env},
        )
        parent = made.stdout.strip()
        parent_tree = new_tree

    return parent


# --- putting a worktree back in step ---------------------------------------

def _worktree_on_branch(root: Path, branch: str) -> Path | None:
    """The checkout that has `branch` checked out, if any.

    `git worktree list --porcelain` is the only authority here: a task's
    directory may have been detached, reused, or never created, and the branch
    is what this command moved.
    """
    res = _git(["worktree", "list", "--porcelain"], root, check=False)
    current: Path | None = None
    for line in res.stdout.splitlines():
        if line.startswith("worktree "):
            current = Path(line[len("worktree "):].strip())
        elif line.startswith("branch ") and current is not None:
            if line[len("branch "):].strip() == f"refs/heads/{branch}":
                return current
    return None


def _state_anomaly(path: Path) -> str:
    """Name a git operation already in flight in this worktree, or ''."""
    from endless.worktree_cmd import _git_state_anomaly
    return _git_state_anomaly(path)


def _resync_worktree(wt: Path, paths: list[str]) -> None:
    """Bring one worktree's index and files back in step for the mirror paths.

    Moving a ref does not touch the checkout that has it checked out, so without
    this the worktree would report every stripped mirror as a staged addition —
    a permanently dirty tree, which is the failure E-1525 removed and which
    trips land's modified-worktree guard.

    Only the mirror paths are touched. Every other file, staged or not, is left
    exactly as the session left it.
    """
    for rel in paths:
        in_head = _git(["cat-file", "-e", f"HEAD:{rel}"], wt, check=False).returncode == 0
        if in_head:
            _git(["checkout", "-f", "HEAD", "--", rel], wt, check=False)
            continue
        _git(["update-index", "--force-remove", "--", rel], wt, check=False)
        target = wt / rel
        if target.exists():
            target.unlink()


# --- the command ------------------------------------------------------------

class _Verdict:
    """One branch's disposition, with the reason shown verbatim."""

    def __init__(self, branch: str, action: str, reason: str,
                 paths: list[str] | None = None, commits: int = 0):
        self.branch = branch
        self.action = action        # "strip" | "skip" | "error"
        self.reason = reason
        self.paths = paths or []
        self.commits = commits


def _classify(root: Path, base: str, branch: str) -> _Verdict:
    """Decide what to do with one branch, without changing anything."""
    base_rev = _merge_base(base, branch, root)
    if base_rev is None:
        return _Verdict(branch, "skip", f"no common ancestor with {base}")

    mirror_commits = _mirror_commits(root, base_rev, branch)
    if not mirror_commits:
        return _Verdict(branch, "skip", "holds no mirror commits")

    if _has_merge_commit(root, base_rev, branch):
        return _Verdict(
            branch, "skip",
            "has a merge commit; rewriting it would flatten the merge")

    wt = _worktree_on_branch(root, branch)
    if wt is not None and wt.is_dir():
        anomaly = _state_anomaly(wt)
        if anomaly:
            return _Verdict(branch, "skip", f"its worktree has {anomaly}")

    paths = _mirror_paths_touched(root, base_rev, branch)
    for rel in paths:
        branch_text = _branch_file(root, branch, rel)
        if branch_text is None:
            # Touched but not present at the tip — the branch deleted it. There
            # is nothing to lose and nothing to compare.
            continue
        db_text = doc_mirror_content(rel)
        if db_text is None:
            return _Verdict(
                branch, "skip",
                f"could not read what {rel} should contain")
        if branch_text.strip() != db_text.strip():
            return _Verdict(
                branch, "skip",
                f"{rel} holds content the database does not have; read it with "
                f"`git show {branch}:{rel}` and adopt it with "
                f"`endless task update` before re-running")

    return _Verdict(branch, "strip", f"{len(mirror_commits)} mirror commit(s)",
                    paths, len(mirror_commits))


def strip_doc_commits(apply: bool) -> None:
    """Remove document-mirror commits from this project's task branches.

    Dry run by default, like `worktree sync`: a sweep that rewrites ninety
    branches shows its work before it does it, not after.
    """
    from endless.worktree_cmd import _default_base_branch, _project_root, _tilde

    root = _project_root()
    base = _default_base_branch(root)
    branches = _task_branches(root)
    if not branches:
        click.echo("No task branches for this project.")
        return

    verdicts = [_classify(root, base, b) for b in branches]
    todo = [v for v in verdicts if v.action == "strip"]
    skipped = [v for v in verdicts if v.action == "skip"]
    noteworthy = [v for v in skipped if not v.reason.startswith("holds no")]

    if not apply:
        for v in todo:
            click.echo(f"  would strip  {v.branch}  ({v.reason})")
        for v in noteworthy:
            click.echo(f"  skip         {v.branch}  ({v.reason})")
        quiet = len(skipped) - len(noteworthy)
        click.echo(
            f"\n{len(todo)} branch(es) would be stripped of "
            f"{sum(v.commits for v in todo)} commit(s); "
            f"{len(noteworthy)} skipped, {quiet} already clean."
        )
        if todo:
            click.echo("Re-run with --apply to strip them.")
        return

    index_path = root / ".endless" / "tmp" / "strip-docs.index"
    index_path.parent.mkdir(parents=True, exist_ok=True)

    # Say what is being left alone BEFORE doing anything, not only in a dry
    # run. A branch skipped because its mirror holds content the database does
    # not have is the whole reason this command refuses to guess; reporting it
    # only in the dry run would mean the person who ran --apply never hears it.
    for v in noteworthy:
        click.echo(f"  skip      {v.branch}  ({v.reason})")

    stripped, moved, failed = 0, [], []
    for v in todo:
        old_tip = _out(["rev-parse", v.branch], root)
        base_rev = _merge_base(base, v.branch, root)
        if base_rev is None:
            failed.append((v.branch, f"no common ancestor with {base}"))
            continue
        # Re-classify immediately before acting. The survey above was built for
        # the whole fleet at once; a session can wake up, commit, or start a
        # rebase between then and now. The compare-and-swap below closes the
        # commit race for certain — this only keeps the report honest.
        fresh = _classify(root, base, v.branch)
        if fresh.action != "strip":
            moved.append((v.branch, fresh.reason))
            click.echo(f"  skip      {v.branch}  (changed while sweeping: {fresh.reason})")
            continue

        try:
            if index_path.exists():
                index_path.unlink()
            new_tip = _rewrite_branch(root, v.branch, base_rev, fresh.paths, index_path)
        except subprocess.CalledProcessError as e:
            failed.append((v.branch, (e.stderr or e.stdout or str(e)).strip()))
            click.echo(f"  FAILED    {v.branch}  (rewrite: {(e.stderr or '').strip()})")
            continue
        finally:
            if index_path.exists():
                index_path.unlink()

        if new_tip == old_tip:
            continue

        cas = _git(["update-ref", f"refs/heads/{v.branch}", new_tip, old_tip],
                   root, check=False)
        if cas.returncode != 0:
            moved.append((v.branch, "its tip moved mid-rewrite; left alone"))
            click.echo(f"  skip      {v.branch}  (its tip moved mid-rewrite; left alone)")
            continue

        wt = _worktree_on_branch(root, v.branch)
        if wt is not None and wt.is_dir():
            _resync_worktree(wt, fresh.paths)

        stripped += 1
        click.echo(f"  stripped  {v.branch}  ({v.commits} commit(s))")

    click.echo(
        f"\n{stripped} branch(es) stripped, {len(skipped) + len(moved)} skipped"
        f"{f', {len(failed)} failed' if failed else ''}."
    )
    for branch, detail in failed:
        click.echo(f"  {branch}: {detail}")
    if stripped:
        click.echo(
            "\nEach branch's pre-strip tip is in its reflog, so the way back "
            "stays available:\n"
            f"  git -C {_tilde(root)} reflog show task/<id>"
        )

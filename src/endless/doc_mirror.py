"""Where a task's or a decision's document mirror lives, and how it gets there.

A mirror is a projection of a database column — `tasks.plan`, `tasks.outcome`,
`tasks.analysis`, a decision's body — written to a committed `.md` file so a
human can read it on github.com without a database. The column is the source of
truth; the file is derived from it and can always be regenerated.

Mirrors the Go definition in `internal/docmirror` — keep the two in step. The
split is the same one `AUTO_COMMIT_GLOBS` already lives with: Go owns the hook
that refuses a hand-edit and the sweep that keeps main current, Python owns the
CLI that writes them.

Every write goes to the project's MAIN checkout (E-2137). It used to go to the
task's worktree branch whenever a worktree existed, where it waited for a land:
measured over 133 worktrees, 123 of the 139 genuinely-unlanded commits were
these mirrors, and 44 of 56 unlanded worktrees were unlanded only because of
them. The ledger entry — the authoritative half of the same write — has always
been enforced onto main. The mirror now follows it there, at the same time.
"""

import re
import shutil
import subprocess
from dataclasses import dataclass
from pathlib import Path

import click


@dataclass(frozen=True)
class Kind:
    """One mirrored task-document kind.

    `column` is the `tasks` column holding the authoritative content, `stem` the
    mirror's filename inside the task's own directory, `label` the noun used in
    commit subjects and CLI output, and `legacy_dir` the directory under
    `.endless/` this kind was mirrored into before consolidation — retained
    because the sweep must still RECOGNIZE a file there in order to relocate it.
    """

    column: str
    stem: str
    label: str
    legacy_dir: str


# Every task-scoped document kind. Adding one here is the whole change: the
# path, the recognizer and the sweep all read from this one list.
TASK_KINDS: tuple[Kind, ...] = (
    Kind("plan", "plan", "plan", "plans"),
    Kind("outcome", "outcome", "outcome", "outcomes"),
    Kind("analysis", "analysis", "analysis", "analyses"),
)

KIND_BY_COLUMN: dict[str, Kind] = {k.column: k for k in TASK_KINDS}

TASKS_ROOT = ".endless/tasks"
DECISIONS_DIR = ".endless/decisions"


def task_dir(task_id: int) -> str:
    """A task's own directory, repo-relative.

    The casing is lowercase `e-NNNN`, matching the directory verification suites
    have always used. Spelled here exactly once so no caller has to remember —
    a hand-written path gets the case wrong silently on a case-insensitive
    filesystem and loudly on everyone else's.
    """
    return f"{TASKS_ROOT}/e-{task_id}"


def task_doc_path(task_id: int, stem: str) -> str:
    """Repo-relative path of one task document mirror."""
    return f"{task_dir(task_id)}/{stem}.md"


def legacy_task_doc_path(kind: Kind, task_id: int) -> str:
    """Where this kind's mirror was written before consolidation.

    Used only to FIND a file to relocate; nothing writes here.
    """
    return f".endless/{kind.legacy_dir}/E-{task_id}.md"


def decision_doc_path(decision_id: int) -> str:
    """Repo-relative path of a decision body mirror.

    Decisions do NOT move into the task tree: `ED-NNNN` has no owning task, so
    it is not task-scoped, and E-1868 is about to renumber every decision id.
    """
    return f"{DECISIONS_DIR}/ED-{decision_id}.md"


def task_doc_subject(action: str, label: str, task_id: int) -> str:
    """Commit subject for one task document mirror.

    The id is in the subject so two different tasks' mirrors never amend over
    each other — `canAmend` requires the subject to match, and that is the only
    thing keeping them apart.
    """
    return f"Endless: {action} {label} for E-{task_id}"


def decision_doc_subject(action: str, decision_id: int) -> str:
    """Commit subject for a decision body mirror."""
    return f"Endless: {action} decision ED-{decision_id}"


def write_to_main(
    main_root: Path, rel_path: str, content: str, subject: str, label: str,
) -> Path | None:
    """Write one mirror into the main checkout and commit it there.

    Returns the written path, or None when there was nowhere to write.

    Best-effort on the COMMIT, never on the write: the database row was updated
    before this was called and is authoritative, so failing the command here
    would report failure for work that already succeeded (the reasoning E-1474
    settled for `land`). A commit that does not happen is repaired by the
    `doc-mirrors` sweep, which rewrites any mirror whose bytes differ from its
    column.
    """
    target = main_root / rel_path
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text(content)
    click.echo(
        click.style("✓", fg="green")
        + f" Wrote {label} to {_display_path(target)}"
    )
    commit_on_main(main_root, rel_path, subject)
    return target


def commit_on_main(project_root: Path, rel_path: str, subject: str) -> None:
    """Commit one mirror on the project's main checkout via endless-go.

    Reuses the Go `event commit-doc` path (→ events.CommitDoc → commitPaths),
    inheriting its main-checkout enforcement, its index.lock retry and its
    GIT_DIR-family env stripping instead of re-implementing them in Python.
    Warns and skips on any failure.
    """
    binary = shutil.which("endless-go")
    if not binary:
        click.echo(
            "  warning: endless-go not found on PATH; "
            f"{rel_path} not committed to main.",
            err=True,
        )
        return
    try:
        result = subprocess.run(
            [binary, "event", "commit-doc", "--project-root", str(project_root),
             "--path", rel_path, "--subject", subject],
            capture_output=True, text=True,
        )
    except OSError as e:
        click.echo(f"  warning: endless-go event commit-doc: {e}", err=True)
        return
    if result.returncode != 0:
        click.echo(
            f"  warning: could not commit {rel_path} to main: "
            f"{(result.stderr or '').strip()}",
            err=True,
        )


def _display_path(p: Path) -> str:
    """Display a Path with $HOME collapsed to ~."""
    s = str(p)
    home = str(Path.home())
    return s.replace(home, "~", 1) if s.startswith(home) else s


# Recognizers ---------------------------------------------------------------
#
# Mirrors of the same expressions in internal/docmirror — keep the two in step.
# Each matches an absolute or repo-relative path.

# The alternation over stems is closed on purpose: `.endless/tasks/e-NNNN/` also
# holds `verify.sh` and `verify.toml`, which are the TASK's files to write. A
# pattern that matched the whole directory would refuse a session's own
# verification suite.
TASK_DOC_RE = re.compile(r"(^|/)\.endless/tasks/e-\d+/(plan|outcome|analysis)\.md$")
LEGACY_TASK_DOC_RE = re.compile(r"(^|/)\.endless/(plans|outcomes|analyses)/E-\d+\.md$")
DECISION_DOC_RE = re.compile(r"(^|/)\.endless/decisions/ED-\d+\.md$")


def is_mirror_path(path: str) -> bool:
    """True for any document mirror Endless writes — consolidated, legacy, or a
    decision. The "this content belongs to the database, not to you" test.
    """
    return bool(
        TASK_DOC_RE.search(path)
        or LEGACY_TASK_DOC_RE.search(path)
        or DECISION_DOC_RE.search(path)
    )


# Git pathspecs covering every place a mirror can sit. Used to ask git which
# commits on a branch touch one — a pathspec list rather than a regex because
# the question is put to `git log -- <paths>`, which does the filtering itself.
MIRROR_PATHSPECS: tuple[str, ...] = (
    ".endless/plans",
    ".endless/outcomes",
    ".endless/analyses",
    ".endless/decisions",
    ".endless/tasks/e-*/plan.md",
    ".endless/tasks/e-*/outcome.md",
    ".endless/tasks/e-*/analysis.md",
)

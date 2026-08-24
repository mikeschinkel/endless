"""`endless lesson write` — record a correction, and commit it, in one step.

The lessons log is `<project>/.endless/LESSONS.md` on the MAIN checkout: a
write-only record of what the user corrected, appended newest-last. Before
E-2055 a session appended to that file by hand and the append then sat
uncommitted on main until some task's `worktree land` swept it up — so
recording a correction meant re-landing a task that was otherwise finished.

This command closes that: it appends the entry and commits that one file on
main immediately, exactly as `endless verb add` does for `.endless/verbs.jsonl`
(E-1208). Both share `main_commit.commit_path`, and both are sanctioned by
ED-1199's global-config exception to no-direct-commits-to-main.

Three things follow from routing the write through a command rather than a raw
file append:

- the entry's shape is written by endless, not retyped per session, so
  `- **Project**:` is derived instead of remembered;
- the log gets created, with a header, on the first lesson a project records —
  a project that has never recorded one has no file to explain;
- there is a single choke point for the lessons TABLE this becomes (E-2056),
  which is why the summary is a separate argument from the detail rather than
  one blob: they are that table's `summary` and `text` columns.

Deliberately still a flat Markdown file. Replacing it is the sibling task's
job, not this one's.
"""

from __future__ import annotations

from datetime import date

import click

from endless import main_commit
from endless.project_path import project_name_for_cwd, project_root

# Repo-relative, on the main checkout. Not a worktree copy — see the module
# docstring and CLAUDE.md's "Memory is OFF here".
LESSONS_REL_PATH = ".endless/LESSONS.md"

# The commit subject is capped, not the summary: the subject is what
# `git log --oneline` shows, and a project's history is the surface the cap
# exists to keep scannable. The summary's own budget is whatever the prefix
# leaves, computed below rather than written down twice.
SUBJECT_PREFIX = "Endless: record lesson ("
SUBJECT_SUFFIX = ")"
SUBJECT_LIMIT = 60
SUMMARY_LIMIT = SUBJECT_LIMIT - len(SUBJECT_PREFIX) - len(SUBJECT_SUFFIX)

# Written once, when a project records its first lesson. Says what the file is
# and how entries get here; it does NOT tell a reader whether to read it back.
# That is a per-project policy (Endless's own copy forbids it; a project that
# feeds lessons back to its agent will not), so it belongs in that project's
# own header text, not in the one endless generates.
DEFAULT_HEADER = """# Lessons Learned

A capture log of corrections, newest last. Entries are appended by
`endless lesson write`, which commits this file on the main checkout as it
writes — so recording a lesson never waits on a task landing, and never
dirties a worktree.

## Format

```
### [YYYY-MM-DD] One-line summary
<the lesson — what went wrong, why, and the rule that replaces it>
- **Project**: <project>
```

---

## Entries
"""


def _subject_for(summary: str) -> str:
    return f"{SUBJECT_PREFIX}{summary}{SUBJECT_SUFFIX}"


def _render_entry(summary: str, text: str, project: str | None, today: str) -> str:
    """The Markdown block appended for one lesson.

    Leading blank line so the entry separates from whatever precedes it,
    whether that is the header or an earlier entry.
    """
    lines = [f"\n### [{today}] {summary}", text.strip()]
    if project:
        lines.append(f"- **Project**: {project}")
    return "\n".join(lines) + "\n"


def write_lesson(summary: str, text: str | None) -> None:
    """Append one lesson to the project's log on main and commit it there.

    Raises ClickException for every condition the caller can act on: an empty
    or over-long summary, missing detail, no registered project, or a failed
    git commit. On a failed commit the append has already happened and is left
    on disk — an append-only record must not lose content to a git error, and
    the message says the file is dirty so the user can commit it by hand.
    """
    summary = (summary or "").strip()
    if not summary:
        raise click.ClickException("A lesson needs a one-line summary.")

    subject = _subject_for(summary)
    if len(subject) > SUBJECT_LIMIT:
        raise click.ClickException(
            f"Summary is too long: the commit subject would be {len(subject)} "
            f"characters, over the {SUBJECT_LIMIT} limit.\n"
            f"  Subject: {subject}\n"
            f"  Budget:  {SUMMARY_LIMIT} characters for the summary; yours is "
            f"{len(summary)}.\n"
            f"  The summary is the scannable one-liner; put the explanation in "
            f"--text."
        )

    if not text or not text.strip():
        raise click.ClickException(
            "A lesson needs its detail: pass --text (or --text-file).\n"
            f"  Example: endless lesson write {summary!r} \\\n"
            f"             --text \"- **What went wrong**: ...\\n"
            f"- **Why**: ...\\n- **Rule**: ...\"\n"
            "  The summary alone is a label, not a lesson — the rule that "
            "replaces the mistake is the part worth keeping."
        )

    # strict: an unresolvable project is fatal here — there is no machine-layer
    # fallback for a lesson — and the resolver's own message ("not in a
    # registered project", "--db unset in a worktree") is the actionable one.
    root = project_root(strict=True)
    if root is None:
        raise click.ClickException(
            "The project resolved but has no path on record; nowhere to write "
            f"{LESSONS_REL_PATH}.\n"
            "  Repair the registry row: endless project scan"
        )

    log_path = root / LESSONS_REL_PATH
    created = not log_path.exists()
    if created:
        log_path.parent.mkdir(parents=True, exist_ok=True)
        log_path.write_text(DEFAULT_HEADER)

    existing = log_path.read_text()
    if existing and not existing.endswith("\n"):
        existing += "\n"
    # Named from the project we RESOLVED, not from cwd: cwd may be a
    # subdirectory or a worktree, and `project_name_for_cwd` only reads a
    # `.endless/config.json` sitting directly in the directory it is given.
    entry = _render_entry(
        summary, text, project_name_for_cwd(root), date.today().isoformat(),
    )
    log_path.write_text(existing + entry)

    try:
        main_commit.commit_path(root, LESSONS_REL_PATH, subject, text)
    except RuntimeError as e:
        raise click.ClickException(
            f"{e}\n"
            f"  The lesson IS written to {log_path} — only the commit failed. "
            f"Commit that one path by hand, or re-run once git is happy "
            f"(a re-run appends a second copy)."
        )

    click.echo(
        click.style("•", fg="cyan")
        + f" Recorded lesson: {log_path}"
        + ("  (created)" if created else "")
    )
    click.echo(f"  Committed on {root.name}'s main checkout: {subject}")

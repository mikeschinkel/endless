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

# The summary and the commit subject are capped SEPARATELY, and the subject is
# derived from the summary rather than equal to it.
#
# The summary is the lesson's one-line rule — what E-2056's `summary` column
# holds and what a rendered memory index would show. 384 characters is about
# three lines of prose: enough for a rule with its condition, short enough that
# it cannot quietly absorb the narrative that belongs in --text.
SUMMARY_LIMIT = 384

# The subject is `git log --oneline`'s surface, and history is the thing the cap
# exists to keep scannable. Shaped per Conventional Commits v1.0.0 —
# `<type>[optional scope]: <description>` with the detail in the body.
#
# The type is a bare `lesson`, with no `Endless` marker in front of it. Nothing
# in the codebase matches on such a prefix: the only programmatic subject test
# is an exact compare against `Endless: record ledger entry`
# (worktree_cmd.AMENDABLE_COMMIT_SUBJECTS / events.LedgerCommitSubject), and a
# vendor prefix would spend 11 of the 60 characters buying a scannability the
# Conventional-Commits type already provides. Every character not spent here is
# a character of the actual lesson.
SUBJECT_PREFIX = "lesson: "
SUBJECT_LIMIT = 60
SUBJECT_ROOM = SUBJECT_LIMIT - len(SUBJECT_PREFIX)

# An ellipsis this close to the end of a word is worth backing up to the word
# boundary for; further back and the truncation loses more than it tidies.
_WORD_BOUNDARY_FLOOR = SUBJECT_ROOM * 3 // 4

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
    """The commit subject for a lesson: prefix + as much summary as fits.

    A summary within the remaining room is used whole — the common case, and
    the one where history reads exactly as authored. A longer one is cut to fit
    and marked with an ellipsis, backing up to a word boundary when that costs
    little. Only the subject is ever truncated; the file and the commit body
    keep the summary verbatim.
    """
    if len(summary) <= SUBJECT_ROOM:
        return SUBJECT_PREFIX + summary
    cut = summary[:SUBJECT_ROOM - 1]
    space = cut.rfind(" ")
    if space >= _WORD_BOUNDARY_FLOOR:
        cut = cut[:space]
    return SUBJECT_PREFIX + cut.rstrip() + "\u2026"


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

    if len(summary) > SUMMARY_LIMIT:
        raise click.ClickException(
            f"Summary is too long: {len(summary)} characters, over the "
            f"{SUMMARY_LIMIT} limit.\n"
            f"  The summary is the lesson's one-line rule. At this length it is "
            f"the lesson — move the explanation into --text, which has no cap."
        )
    subject = _subject_for(summary)

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

"""Resolve a name to a project, handling duplicates with --path."""

from endless import agent_help, db, provenance
from endless.project_path import stored


def resolve_project(name: str, path_hint: str | None = None) -> dict:
    """Look up a project by name. If duplicates exist, require --path.

    Args:
        name: The project name to look up
        path_hint: Optional path substring to disambiguate

    Returns:
        A sqlite3.Row dict for the matched project

    Raises:
        a classified refusal (agent_help) if not found or ambiguous

    None of the refusals below name a command: this resolver is shared by
    `project set`, `rename`, `decision`, `worktree`, `task` and the session
    verbs, so the verb that produced the failure is the one thing it cannot
    know. The factories default `command=` to the running command's path, which
    is exactly right here and better than any string this file could thread.
    """
    rows = db.query(
        "SELECT id, name, label, path, group_name, description, "
        "status, language, created_at, updated_at "
        "FROM projects WHERE name = ?",
        (name,),
    )

    if not rows:
        raise agent_help.no_report(
            f"No registered project is named '{name}'. Nothing was changed.",
            "Find the registered name with `endless project list` and retry",
            text=f"No project found with name '{name}'",
        )

    if len(rows) == 1:
    # E-1668: record what this invocation resolved, so the provenance trace can
    # say so when it is not the project enclosing cwd. Recorded at the RESOLVERS
    # rather than per command because "which project answered" is their answer,
    # and a command that resolves none has nothing to declare.
        provenance.record_project(rows[0]["name"])
        return dict(rows[0])

    # Multiple matches — need disambiguation
    if not path_hint:
        paths = [stored(r["path"]) for r in rows]
        path_list = "\n  ".join(paths)
        # Which of the two the caller meant is settled by the conversation, not
        # by anything the database holds — both rows are equally real and this
        # resolver sees only a name. Both branches are named (ED, 2026-09-18).
        raise agent_help.report_if(
            f"{len(rows)} registered projects are named '{name}', so the name "
            "alone selects nothing. Nothing was changed.",
            "the name came from the user and nothing in context says which "
            "checkout they meant",
            f"retry with --path={_suggest_segment(paths)} (or another "
            "distinguishing segment)",
            "which of two identically-named projects to act on is theirs to say",
            text=(f"Multiple projects with name '{name}':\n"
                  f"  {path_list}\n"
                  f"Use --path=<segment> to disambiguate "
                  f"(e.g., --path={_suggest_segment(paths)})"),
        )

    # Filter by path hint
    matches = [
        r for r in rows
        if path_hint in r["path"]
    ]

    if len(matches) == 0:
        raise agent_help.no_report(
            f"None of the {len(rows)} projects named '{name}' has "
            f"'{path_hint}' in its path. Nothing was changed.",
            "Re-check the paths with `endless project list` and retry with a "
            "--path segment that appears in one of them",
            text=(f"No project with name '{name}' "
                  f"matching path '{path_hint}'"),
        )
    if len(matches) > 1:
        paths = [stored(r["path"]) for r in matches]
        path_list = "\n  ".join(paths)
        raise agent_help.no_report(
            f"--path='{path_hint}' still matches {len(matches)} projects named "
            f"'{name}'. Nothing was changed.",
            "Retry with a longer --path segment that appears in only one of "
            "the listed paths",
            text=(f"Path hint '{path_hint}' still matches "
                  f"multiple projects:\n  {path_list}\n"
                  f"Use a more specific --path segment"),
        )

    return dict(matches[0])


def _suggest_segment(paths: list[str]) -> str:
    """Suggest a disambiguating path segment from a list of paths."""
    # Find the first differing path component
    parts_list = [p.split("/") for p in paths]
    for i in range(min(len(p) for p in parts_list)):
        segments = set(p[i] for p in parts_list)
        if len(segments) > 1:
            return sorted(segments)[0]
    return paths[0].split("/")[-2]

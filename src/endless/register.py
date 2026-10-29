"""Register command logic."""

import re
import subprocess
from datetime import datetime, timezone
from pathlib import Path

import click

from endless import agent_help, db, config, main_commit
from endless.project_identity import (
    CONFIG_KEY, NoIdentity, identity_for, stored_identity,
)
from endless.project_path import resolved, stored

NAME_PATTERN = re.compile(r"^[a-z0-9][a-z0-9_-]*$")

LANGUAGE_EXTENSIONS = {
    ".go": "go",
    ".ts": "typescript",
    ".tsx": "typescript",
    ".js": "javascript",
    ".jsx": "javascript",
    ".py": "python",
    ".rs": "rust",
    ".rb": "ruby",
    ".sh": "bash",
    ".bash": "bash",
}


# Canonical endless entries every registered project's .gitignore should carry.
# Only paths endless writes that must never be committed and whose currency is
# settled. `.endless/tmp/` is the sanctioned project-local scratch dir — agents
# author throwaway content there (co-located with the work, survives reboot,
# recoverable before a worktree drops) instead of system /tmp. `worktree.json`
# (write-once identity) and `worktree.lock` (its per-session lock holder) are per-
# worktree state that must never be committed — kept per ED-1530. NOT
# `.endless/sessions/`: that companion-file path was pruned, so new projects must
# never scaffold it. `.endless/logs/` holds the session-notice log the hook
# appends to (internal/monitor AppendNoticeLog), per-machine and never shared.
# worktree land reads this same list to tell Endless's runtime files from user
# work (E-2349), so a path added here is excluded there too.
# tests/test_register.py classifies every `.endless/` path the source names, so
# a new runtime path cannot be written without landing here.
GITIGNORE_ENTRIES = [
    ".endless/worktrees/",
    ".endless/tmp/",
    ".endless/logs/",
    ".endless/worktree.json",
    ".endless/worktree.lock",
]
GITIGNORE_BLOCK_HEADER = "# endless (managed by `endless project init`)"


def scaffold_gitignore(project_path: Path) -> list[str]:
    """Idempotently ensure the project's .gitignore ignores the canonical
    endless paths. Appends only the missing entries under a marked block and
    creates .gitignore if absent; entries already present anywhere in the file
    (in any form) are left as-is. A file that already has the marked block gets
    the missing entries at the end of that block, not a second header. Returns
    the entries newly added — empty when the file already covered them all, so
    re-running never duplicates a line."""
    gitignore = project_path / ".gitignore"
    existing = gitignore.read_text() if gitignore.exists() else ""
    present = {line.strip() for line in existing.splitlines()}
    missing = [entry for entry in GITIGNORE_ENTRIES if entry not in present]
    if not missing:
        return []
    lines = existing.splitlines()
    if GITIGNORE_BLOCK_HEADER in lines:
        # The block runs from its header to the first blank line or EOF.
        end = lines.index(GITIGNORE_BLOCK_HEADER) + 1
        while end < len(lines) and lines[end].strip():
            end += 1
        lines[end:end] = missing
        gitignore.write_text("\n".join(lines) + "\n")
        return missing
    block = GITIGNORE_BLOCK_HEADER + "\n" + "\n".join(missing) + "\n"
    if existing and not existing.endswith("\n"):
        existing += "\n"
    separator = "\n" if existing else ""
    gitignore.write_text(existing + separator + block)
    return missing


def validate_name(name: str) -> bool:
    return bool(NAME_PATTERN.match(name))


def detect_language(project_path: Path) -> str:
    counts: dict[str, int] = {}
    for f in project_path.rglob("*"):
        if f.is_file() and len(f.relative_to(project_path).parts) <= 2:
            lang = LANGUAGE_EXTENSIONS.get(f.suffix)
            if lang:
                counts[lang] = counts.get(lang, 0) + 1
    if not counts:
        return ""
    return max(counts, key=counts.get)


def register_project(
    project_path: Path,
    name: str | None = None,
    label: str | None = None,
    description: str | None = None,
    language: str | None = None,
    status: str | None = None,
    infer: bool = False,
) -> str:
    """Register or update a project. Returns the name."""

    # The RESOLVED form is what everything below touches on disk; the STORED
    # form is what the projects column holds (E-2011).
    project_path = resolved(project_path)
    if not project_path.is_dir():
        raise agent_help.no_report(
            f"{project_path} is not a directory, so there is nothing to "
            "register. Nothing was written.",
            "Correct the path and retry",
            text=f"Directory not found: {project_path}",
        )
    stored_path = stored(project_path)

    # A marker on disk is the user saying "leave this directory alone" (E-2251).
    # Explicit registration is allowed UNDER an ignored directory — that is how
    # a project inside an ignored tree gets registered — but not of a directory
    # that carries the marker itself; the marker is the thing to remove.
    marker = project_path / config.IGNORE_MARKER
    if marker.is_file():
        raise agent_help.no_report(
            f"{project_path} carries {config.IGNORE_MARKER}, which marks it as "
            "not a project. Nothing was registered.",
            f"Delete {marker} if this directory should be a project, then retry",
            text=f"Ignored by marker: {marker}",
        )

    # A task worktree is a checkout of a project, never a project of its own
    # (E-2349). Refused before anything is read or written, and without
    # relying on the identity check below, which a row with no stored identity
    # cannot make.
    _refuse_worktree(project_path)

    # Snapshot the files this run may commit, before it touches any of them.
    before = _commit_candidates_status(project_path)

    # Check if already registered
    existing = db.query(
        "SELECT id, name, label, description, language, status "
        "FROM projects WHERE path = ?",
        (stored_path,),
    )
    is_update = len(existing) > 0

    # The identity decides whether this checkout is a project already
    # registered somewhere else, so it is settled before anything is prompted
    # for or written (E-2217).
    identity = _identity_or_refuse(project_path)
    moved_from = None
    holder = _identity_holder(identity, existing[0]["id"] if is_update else None)
    if holder is not None:
        if is_update or resolved(holder["path"]).exists():
            _refuse_duplicate_identity(holder, project_path)
        # The checkout that holds this identity is gone, so this one is where
        # the project lives now: adopt its row, tasks and all, rather than
        # registering a second project or refusing a move.
        moved_from = resolved(holder["path"])
        existing = db.query(
            "SELECT id, name, label, description, language, status "
            "FROM projects WHERE id = ?",
            (holder["id"],),
        )
        is_update = True

    if moved_from is not None:
        click.echo(
            click.style("•", fg="cyan")
            + f" {click.style(existing[0]['name'], bold=True)} moved from "
            + click.style(str(moved_from), dim=True)
            + " — updating its path"
        )
    elif is_update:
        row = existing[0]
        click.echo(
            click.style("•", fg="cyan")
            + f" Project already registered at "
            + click.style(str(project_path), bold=True)
            + " — updating"
        )

    dir_name = project_path.name
    detected_lang = detect_language(project_path)

    # Determine defaults from existing or inference
    if is_update:
        row = existing[0]
        default_name = row["name"]
        default_label = row["label"] or ""
        default_desc = row["description"] or ""
        default_lang = row["language"] or detected_lang
        default_status = row["status"]
        if default_status == config.IGNORED_STATUS:
            # Registering an ignored directory re-activates it (E-2251). A row
            # that never was a project is named by its path; offer the
            # directory's name instead.
            default_status = "active"
            if default_name == stored_path:
                default_name = dir_name
    else:
        default_name = dir_name
        default_label = ""
        default_desc = ""
        default_lang = detected_lang
        default_status = "active"

    # Resolve values: explicit flag > interactive prompt > default
    if infer:
        name = name or default_name
        label = label if label is not None else default_label
        description = description if description is not None else default_desc
        language = language or default_lang
        status = status or default_status
    else:
        if name is None:
            name = click.prompt(
                click.style("Name (identifier)", bold=True),
                default=default_name,
            )
        if label is None:
            label = click.prompt(
                click.style("Label (display)", bold=True),
                default=default_label or "",
            )
        if description is None:
            description = click.prompt(
                click.style("Description", bold=True),
                default=default_desc or "",
            )
        if language is None:
            language = click.prompt(
                click.style("Language", bold=True),
                default=default_lang or "",
            )
        if status is None:
            status = click.prompt(
                click.style("Status", bold=True),
                default=default_status,
                type=click.Choice(
                    ["active", "paused", "archived", "idea"]
                ),
            )

    # Validate
    if not validate_name(name):
        # With --infer the default is the directory's own name, so a directory
        # like "My App" lands here until --name supplies an identifier.
        raise agent_help.no_report(
            f"'{name}' is not a valid project name. Nothing was registered.",
            "Retry with --name set to a lowercase alphanumeric identifier "
            "(hyphens and underscores allowed)",
            text=(f"Invalid name: '{name}' "
                  "(must be lowercase alphanumeric, hyphens, or underscores)"),
        )
    _refuse_name_clash(name, existing[0]["id"] if is_update else None)
    if status not in ("active", "paused", "archived", "idea"):
        # Effectively unreachable from the CLI: --status is a click.Choice over
        # this same set, and a stored status can only be one of them. It stays
        # as the guard for a direct caller of this function.
        raise agent_help.no_report(
            f"'{status}' is not a project status. Nothing was registered.",
            "Retry with --status active, paused, archived or idea",
            text=f"Invalid status: {status}",
        )

    # Write .endless/config.json — merge over any existing config so re-running
    # never clobbers keys endless doesn't manage here (e.g. self_dev, matchers)
    # and preserves an existing project's dependencies/documents.
    cfg = config.project_config_read(project_path) or {}
    cfg.update({
        "name": name,
        "label": label,
        "description": description,
        "language": language,
        "status": status,
    })
    # Never overwrites a stored identity: the stored copy is what survives a
    # history rewrite (E-2217).
    cfg.setdefault(CONFIG_KEY, identity)
    cfg.setdefault("dependencies", [])
    cfg.setdefault("documents", {"rules": []})
    config.project_config_write(project_path, cfg)

    # Scaffold .gitignore + the project-local scratch dir so agents author
    # throwaway content under .endless/tmp/ instead of system /tmp, and never
    # hit a missing-directory trap when they do.
    added = scaffold_gitignore(project_path)
    (project_path / ".endless" / "tmp").mkdir(parents=True, exist_ok=True)

    # Detect group from parent directory
    group_name = None
    parent = project_path.parent
    roots = config.get_roots()
    if parent not in roots:
        group_name = parent.name

    # Upsert into database
    now = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%S")
    if is_update:
        db.execute(
            "UPDATE projects SET name=?, label=?, group_name=?, "
            "description=?, status=?, language=?, updated_at=?, "
            "identity=?, slug=COALESCE(slug, ?), path=? "
            "WHERE id=?",
            (name, label, group_name, description, status,
             language, now, identity, name, stored_path, existing[0]["id"]),
        )
        click.echo(
            click.style("•", fg="cyan")
            + f" Updated {click.style(name, bold=True)}"
        )
    else:
        db.execute(
            "INSERT INTO projects "
            "(name, label, path, group_name, description, "
            "status, language, created_at, updated_at, identity, slug) "
            "VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
            (name, label, stored_path, group_name,
             description, status, language, now, now, identity, name),
        )
        click.echo(
            click.style("•", fg="cyan")
            + f" Registered {click.style(name, bold=True)}"
            + f" at {project_path}"
        )

    click.echo(
        click.style("•", fg="cyan")
        + " Config written to "
        + click.style(
            str(project_path / ".endless" / "config.json"),
            dim=True,
        )
    )
    if added:
        click.echo(
            click.style("•", fg="cyan")
            + " .gitignore updated with "
            + click.style(", ".join(added), dim=True)
        )
    _place_suite_rules(name, project_path)

    pointer_file = scaffold_guide_pointer(project_path)
    if pointer_file:
        click.echo(
            click.style("•", fg="cyan")
            + " Pointer to `endless guide` written to "
            + click.style(pointer_file, dim=True)
        )

    _scaffold_output_style(project_path)

    _commit_bring_up(project_path, before)

    return name


# --- Bringing a project up to date (E-2349) ---------------------------------

SUITE_RULES_KIND = "tasks-rules"


def _place_suite_rules(name: str, project_path: Path) -> None:
    """Place `.endless/tasks/CLAUDE.md`, the verification-suite rules.

    Placed before the project has any suites, so the reasoning is already there
    the first time someone writes or runs one. A re-run brings an unedited copy
    up to the rules this Endless ships, and leaves an edited one alone (E-2421):
    the text, every shipped version and the decision live in Go
    (internal/shipped). Runs after the project row exists, because the record
    of what was written is an event in this project's ledger.

    Off the default branch nothing is recorded: the ledger entry would commit
    on that branch and be lost if the branch is thrown away. With no record, a
    later run still updates the copy, because it is exactly a shipped version.
    """
    from endless import event_bridge

    unrecorded = _not_committable_reason(project_path)
    placed = event_bridge.place_shipped_file(
        SUITE_RULES_KIND, name, project_path, record=unrecorded is None)
    path = click.style(placed["path"], dim=True)
    action = placed["action"]
    note = f" (not recorded: {unrecorded})" if unrecorded else ""
    if action == "created":
        click.echo(click.style("•", fg="cyan")
                   + " Verification-suite rules written to " + path + note)
    elif action == "updated":
        click.echo(click.style("•", fg="cyan")
                   + " Verification-suite rules updated in " + path + note)
    elif action == "edited":
        click.echo(click.style("•", fg="yellow") + " " + path
                   + " was edited locally and not updated")


GUIDE_POINTER_MARKER = "<!-- endless (managed by `endless project init`) -->"
GUIDE_POINTER_BLOCK = (
    GUIDE_POINTER_MARKER + "\n"
    "## Endless\n"
    "\n"
    "This project's work is tracked with Endless. Run `endless guide` before\n"
    "starting work.\n"
)
INSTRUCTION_FILES = ("CLAUDE.md", "AGENTS.md")


def scaffold_guide_pointer(project_path: Path) -> str | None:
    """Idempotently point the project's agent instructions at `endless guide`.

    The SessionStart hook injects the same pointer, but only into sessions that
    run Endless's hooks; the file reaches every agent. With neither CLAUDE.md
    nor AGENTS.md, CLAUDE.md is created holding the block. With either, and
    neither mentioning `endless guide`, the block is appended under its marker
    to CLAUDE.md if present, else AGENTS.md. A file that already mentions it is
    left alone. Returns the file written, or None.
    """
    present = [n for n in INSTRUCTION_FILES if (project_path / n).is_file()]
    if any("endless guide" in (project_path / n).read_text() for n in present):
        return None
    target = project_path / (present[0] if present else INSTRUCTION_FILES[0])
    existing = target.read_text() if target.is_file() else ""
    if existing and not existing.endswith("\n"):
        existing += "\n"
    separator = "\n" if existing else ""
    target.write_text(existing + separator + GUIDE_POINTER_BLOCK)
    return target.name


# Files a bring-up may commit. Endless's own are committed whatever their state;
# the user's only when the run changed them and they had no change of their own
# beforehand, so an Endless commit never carries the user's work (EQ-83).
# The output style's path mirrors internal/outputstylecmd (StyleName "Endless"):
# init writes it there, unactivated, and it is Endless's file like the rest.
ENDLESS_OWNED = (
    ".endless/config.json",
    ".endless/tasks/CLAUDE.md",
    ".claude/output-styles/Endless.md",
)
USER_OWNED = (".gitignore",) + INSTRUCTION_FILES
BRING_UP_COMMIT_SUBJECT = "Endless: bring project up to Endless usage"


def _git(project_path: Path, *args: str) -> subprocess.CompletedProcess:
    return subprocess.run(
        ["git", "-C", str(project_path), *args],
        capture_output=True, text=True, env=main_commit.sanitized_git_env(),
    )


def _refuse_worktree(project_path: Path) -> None:
    """Refuse a git linked worktree, or a directory under a `.endless/worktrees/`.

    The owner is the checkout the worktree belongs to: the directory above
    `.endless/worktrees/`, else the repository whose common git dir this one
    shares. It is named by its registered project when there is one.
    """
    owner = None
    parts = project_path.parts
    for i in range(len(parts) - 1):
        if parts[i] == ".endless" and parts[i + 1] == "worktrees":
            owner = Path(*parts[:i])
            break
    if owner is None:
        res = _git(project_path, "rev-parse", "--path-format=absolute",
                   "--git-dir", "--git-common-dir")
        lines = res.stdout.split()
        if res.returncode != 0 or len(lines) != 2 or lines[0] == lines[1]:
            return
        common = Path(lines[1])
        owner = common.parent if common.name == ".git" else common
    rows = db.query("SELECT name FROM projects WHERE path = ?", (stored(owner),))
    named = f"project '{rows[0]['name']}' at {owner}" if rows else str(owner)
    raise agent_help.no_report(
        f"{project_path} is a worktree of {named}, not a project of its own. "
        "Nothing was registered.",
        f"Run endless project init in {owner} instead",
        text=(f"Worktree, not a project: {project_path}\n"
              f"It is a worktree of {named}. Run `endless project init` "
              "there instead."),
    )


def _status(project_path: Path, paths) -> dict[str, str]:
    """`git status --porcelain` codes for `paths` that have any change."""
    res = _git(project_path, "status", "--porcelain", "-z",
               "--untracked-files=all", "--", *paths)
    if res.returncode != 0:
        return {}
    codes: dict[str, str] = {}
    entries = res.stdout.split("\0")
    i = 0
    while i < len(entries):
        entry = entries[i]
        i += 1
        if len(entry) < 4:
            continue
        codes[entry[3:]] = entry[:2]
        if entry[0] in "RC":
            i += 1  # a rename carries its source path as the next entry
    return codes


def _commit_candidates_status(project_path: Path) -> dict:
    """What the candidate files hold, and what git says of them, before a run."""
    candidates = ENDLESS_OWNED + USER_OWNED
    return {
        "status": _status(project_path, candidates),
        "content": {
            p: (project_path / p).read_bytes() if (project_path / p).is_file()
            else None
            for p in candidates
        },
    }


def _commit_bring_up(project_path: Path, before: dict) -> None:
    """Commit what this run changed, by the rule in ENDLESS_OWNED's comment.

    Only on the project's default branch. Every changed file left out is
    listed with why, so the user knows what is theirs to commit.
    """
    after = _status(project_path, ENDLESS_OWNED + USER_OWNED)
    if not after:
        return
    top = _git(project_path, "rev-parse", "--show-toplevel")
    if top.returncode != 0 or resolved(Path(top.stdout.strip())) != project_path:
        # git reports paths from the repository's root, so a project in a
        # subdirectory would match none of them; say so rather than guess.
        click.echo(
            click.style("•", fg="yellow")
            + " Not committed: the project is not at its repository's root; "
            "commit Endless's files yourself"
        )
        return
    commit: list[str] = []
    left: list[tuple[str, str]] = []
    for path in after:
        if path in ENDLESS_OWNED:
            commit.append(path)
        elif path in before["status"]:
            left.append((path, "it had uncommitted changes of yours"))
        elif before["content"].get(path) != (
            (project_path / path).read_bytes()
            if (project_path / path).is_file() else None
        ):
            commit.append(path)
    if commit:
        reason = _not_committable_reason(project_path)
        if reason:
            left.extend((p, reason) for p in commit)
            commit = []
    if commit:
        try:
            main_commit.commit_paths(project_path, commit,
                                     BRING_UP_COMMIT_SUBJECT)
            click.echo(
                click.style("•", fg="cyan")
                + " Committed "
                + click.style(", ".join(commit), dim=True)
            )
        except RuntimeError as e:
            left.extend((p, f"the commit failed: {e}") for p in commit)
    for path, why in left:
        click.echo(
            click.style("•", fg="yellow")
            + f" Not committed: {path} ("
            + why + "); commit it yourself"
        )


def _not_committable_reason(project_path: Path) -> str | None:
    """Why this checkout takes no Endless commit now, or None when it does."""
    from endless.worktree_cmd import _default_base_branch

    head = _git(project_path, "symbolic-ref", "--short", "-q", "HEAD")
    if head.returncode != 0:
        return "the checkout is not on a branch"
    try:
        default = _default_base_branch(project_path)
    except click.ClickException:
        return "the project's default branch could not be resolved"
    current = head.stdout.strip()
    if current != default:
        return f"the checkout is on {current}, not the default branch {default}"
    return None


def _identity_or_refuse(project_path: Path) -> str:
    """The project's identity, or a refusal naming why there is none.

    Endless identifies a project by its repo's root commit, so a directory that
    is not a repo, or a repo with no commit yet, has nothing to register under.
    Making either is the user's call: it creates history in their project.
    """
    try:
        return identity_for(project_path)
    except NoIdentity as e:
        if e.reason == "not_a_repo":
            raise agent_help.report(
                f"{project_path} is not a git repository, and Endless "
                "identifies a project by its repo's root commit. Nothing was "
                "registered.",
                "whether to make this directory a git repository with a first "
                "commit",
                text=(f"Not a git repository: {project_path}\n"
                      "Endless identifies a project by its repo's root commit. "
                      "Run `git init`, make a first commit, then register."),
            ) from e
        raise agent_help.report(
            f"{project_path} has no commits yet, and Endless identifies a "
            "project by its repo's root commit. Nothing was registered.",
            "whether to make the repository's first commit now",
            text=(f"No commits yet: {project_path}\n"
                  "Endless identifies a project by its repo's root commit. "
                  "Make a first commit, then register."),
        ) from e


def _identity_holder(identity: str, own_id: int | None):
    """The other row holding `identity`, or None.

    Every row counts, an unregistered (ignored) one included: E-2251 keeps an
    unregistered project's row, with its tasks, so it is still that project.
    """
    rows = db.query(
        "SELECT id, name, path, status FROM projects WHERE identity = ?",
        (identity,),
    )
    return next((r for r in rows if r["id"] != own_id), None)


def _refuse_duplicate_identity(holder, project_path: Path) -> None:
    """Refuse a checkout whose identity another project's existing checkout holds.

    One registered checkout per project per machine (E-2217). A second clone
    and a fork are indistinguishable here, since both carry the upstream's root
    commit, so the message names both: parallel work belongs in a worktree of
    the registered checkout, and forks are not yet supported. A holder that was
    unregistered is still that project, so the way back is to register its
    checkout again, or to move it away so this one takes the project over.
    """
    other_path = resolved(holder["path"])
    name = holder["name"]
    if holder["status"] == config.IGNORED_STATUS:
        raise agent_help.report(
            f"{project_path} is a checkout of '{name}', which was unregistered "
            f"at {other_path} and still holds that project's tasks. Nothing "
            "was registered.",
            f"whether to register {other_path} again, or retire it so this "
            "checkout takes the project over",
            text=(f"Unregistered project: '{name}' at {other_path}\n"
                  f"{project_path} is another checkout (or a fork) of it. "
                  f"Register {other_path} again to bring it back, or move or "
                  "delete that checkout and register this one: a project "
                  "whose checkout is gone moves to the new one, tasks and "
                  "all."),
        )
    raise agent_help.report(
        f"{project_path} is a checkout of '{name}', which is already "
        f"registered at {other_path}. Endless registers one checkout per "
        "project per machine; for parallel work use a worktree of the "
        "registered checkout (endless task claim creates one). Forks are not "
        "yet supported. Nothing was registered.",
        "whether to work in the registered checkout instead, or retire it so "
        "this checkout takes the project over",
        text=(f"Already registered: '{name}' at {other_path}\n"
              f"{project_path} is another checkout (or a fork) of the same "
              "project. Endless registers one checkout per project per "
              "machine: for parallel work, use a worktree of the registered "
              "checkout (endless task claim creates one). Forks are not yet "
              "supported."),
    )


def _refuse_name_clash(name: str, own_id: int | None) -> None:
    """Refuse a name another registered project holds as its name or slug.

    One machine database cannot hold two projects of one name, because every
    resolver keys on it; a slug is checked too because it keeps a renamed
    project's old name (E-2217). Choosing a different slug at registration
    arrives with per-project databases (E-2215).
    """
    rows = db.query(
        "SELECT id, name, path, status FROM projects WHERE name = ? OR slug = ?",
        (name, name),
    )
    other = next((r for r in rows if r["id"] != own_id), None)
    if other is None:
        return
    other_path = resolved(other["path"])
    # An unregistered project keeps its row and its name (E-2251).
    held_by = ("the unregistered project at"
               if other["status"] == config.IGNORED_STATUS
               else "the project registered at")
    # Who picked the name decides the class, and this function cannot see
    # that: the same call arrives whether the user dictated it or --infer
    # took the directory's own. The rename refusal names both branches too.
    raise agent_help.report_if(
        f"'{name}' is already the name of {held_by} "
        f"{other_path}, and one machine cannot register two projects under one "
        "name yet. Nothing was registered.",
        "the user chose this name",
        "retry with --name set to an unused name",
        "another name for this project, which is written to its committed "
        "config, is theirs to pick",
        text=(f"Name '{name}' is already the name of {held_by} "
              f"{other_path}\nChoose another with --name."),
    )


CONFIG_REL = ".endless/config.json"


def backfill_identities(dry_run: bool = False) -> list[tuple[str, str]]:
    """Give every registered project its identity (E-2217).

    For each row: derive the identity (the stored copy, else the root commit),
    record it in the project's `.endless/config.json` and commit that one file
    in the project's own repo, then set the column. Returns (name, outcome)
    per project, and echoes each as it goes.

    The config is committed only when it is tracked and was clean before the
    write: committing it otherwise would sweep the user's own uncommitted edits,
    or a file they never chose to commit, into an Endless commit. Such a config
    is written and left for them to commit. A project whose identity another
    row already holds is a second checkout registered before this check
    existed; it is reported and left alone, since which copy stays is theirs
    to say.
    """
    outcomes: list[tuple[str, str]] = []

    def note(name: str, outcome: str, ok: bool = True) -> None:
        outcomes.append((name, outcome))
        click.echo(
            click.style("•", fg="cyan" if ok else "yellow")
            + f" {click.style(name, bold=True)}: {outcome}"
        )

    rows = db.query(
        "SELECT id, name, path, identity FROM projects "
        "WHERE status != 'forgotten' ORDER BY id"
    )
    for row in rows:
        name = row["name"]
        path = resolved(row["path"])
        if not (path / CONFIG_REL).is_file():
            note(name, f"skipped: no {CONFIG_REL} at {path}", ok=False)
            continue
        cfg = config.project_config_read(path) or {}
        try:
            identity = identity_for(path, cfg)
        except NoIdentity as e:
            note(name, f"skipped: {e}", ok=False)
            continue
        holder = db.query(
            "SELECT name, path FROM projects WHERE identity = ? AND id != ?",
            (identity, row["id"]),
        )
        if holder:
            note(name, f"skipped: another checkout of '{holder[0]['name']}' "
                       f"(registered at {resolved(holder[0]['path'])})",
                 ok=False)
            continue
        if stored_identity(cfg) is None:
            if dry_run:
                note(name, f"would record {identity}")
                continue
            committable = _config_committable(path)
            cfg[CONFIG_KEY] = identity
            config.project_config_write(path, cfg)
            if committable:
                try:
                    main_commit.commit_path(
                        path, CONFIG_REL, "Endless: record project identity",
                    )
                    outcome = f"recorded {identity} and committed {CONFIG_REL}"
                except RuntimeError as e:
                    outcome = f"recorded {identity}; commit failed: {e}"
            else:
                outcome = (f"recorded {identity}; {CONFIG_REL} left "
                           "uncommitted (it had changes of its own, or was "
                           "never committed)")
        elif row["identity"] == identity:
            note(name, "already recorded")
            continue
        else:
            outcome = f"set {identity} from {CONFIG_REL}"
            if dry_run:
                note(name, f"would {outcome}")
                continue
        db.execute(
            "UPDATE projects SET identity = ? WHERE id = ?",
            (identity, row["id"]),
        )
        note(name, outcome)
    return outcomes


def _config_committable(project_path: Path) -> bool:
    """True when the project's config is tracked and has no changes of its own."""
    env = main_commit.sanitized_git_env()
    tracked = subprocess.run(
        ["git", "-C", str(project_path), "ls-files", "--error-unmatch", "--",
         CONFIG_REL],
        capture_output=True, text=True, env=env,
    )
    if tracked.returncode != 0:
        return False
    clean = subprocess.run(
        ["git", "-C", str(project_path), "diff", "--quiet", "HEAD", "--",
         CONFIG_REL],
        capture_output=True, text=True, env=env,
    )
    return clean.returncode == 0


def _scaffold_output_style(project_path: Path) -> None:
    """Place (never activate) the Endless output style during registration.

    Registration scaffolds the file so a newly-registered project has it on
    disk; it deliberately does NOT select it. Activation changes how every
    session in the project answers, which is too large a behavioral change to
    fall out of `project init` — the user opts in with
    `endless setup output-style --activate` or `/config output-style=Endless`.

    Best-effort: registration must not fail because the style could not be
    written (E-1919). A failure is reported, not raised.
    """
    from endless.setup import setup_output_style

    try:
        setup_output_style(cwd=project_path)
    except click.ClickException as e:
        click.echo(
            click.style("•", fg="yellow")
            + " Output style not scaffolded: "
            + click.style(e.format_message(), dim=True)
        )

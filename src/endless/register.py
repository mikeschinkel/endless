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
from endless.suite_rules import scaffold_suite_rules

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
# never scaffold it.
GITIGNORE_ENTRIES = [
    ".endless/worktrees/",
    ".endless/tmp/",
    ".endless/worktree.json",
    ".endless/worktree.lock",
]
GITIGNORE_BLOCK_HEADER = "# endless (managed by `endless project init`)"


def scaffold_gitignore(project_path: Path) -> list[str]:
    """Idempotently ensure the project's .gitignore ignores the canonical
    endless paths. Appends only the missing entries under a marked block and
    creates .gitignore if absent; entries already present anywhere in the file
    (in any form) are left as-is. Returns the entries newly added — empty when
    the file already covered them all, so re-running never duplicates a line."""
    gitignore = project_path / ".gitignore"
    existing = gitignore.read_text() if gitignore.exists() else ""
    present = {line.strip() for line in existing.splitlines()}
    missing = [entry for entry in GITIGNORE_ENTRIES if entry not in present]
    if not missing:
        return []
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
    _refuse_duplicate_identity(
        identity, project_path, existing[0]["id"] if is_update else None,
    )

    if is_update:
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

    # Place the verification-suite rules before the project has any suites, so
    # the reasoning is already there the first time someone writes or runs one.
    # Never overwrites an existing file — a customized copy survives re-running
    # registration.
    suite_rules_written = scaffold_suite_rules(project_path)

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
            "identity=?, slug=COALESCE(slug, ?) "
            "WHERE path=?",
            (name, label, group_name, description, status,
             language, now, identity, name, stored_path),
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
    if suite_rules_written:
        click.echo(
            click.style("•", fg="cyan")
            + " Verification-suite rules written to "
            + click.style(".endless/tasks/CLAUDE.md", dim=True)
        )

    _scaffold_output_style(project_path)

    return name


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


def _refuse_duplicate_identity(
    identity: str, project_path: Path, own_id: int | None,
) -> None:
    """Refuse a checkout whose identity another registered project holds.

    One registered checkout per project per machine (E-2217). A second clone
    and a fork are indistinguishable here, since both carry the upstream's root
    commit, so the message names both: parallel work belongs in a worktree of
    the registered checkout, and forks are not yet supported.
    """
    rows = db.query(
        "SELECT id, name, path FROM projects WHERE identity = ?", (identity,),
    )
    other = next((r for r in rows if r["id"] != own_id), None)
    if other is None:
        return
    other_path = resolved(other["path"])
    moved = (
        f" That path no longer exists: if the checkout moved, unregister the "
        f"old entry (endless project unregister {other['name']}) and retry."
        if not other_path.exists() else ""
    )
    raise agent_help.report(
        f"{project_path} is a checkout of '{other['name']}', which is already "
        f"registered at {other_path}. Endless registers one checkout per "
        "project per machine; for parallel work use a worktree of the "
        "registered checkout (endless task claim creates one). Forks are not "
        f"yet supported. Nothing was registered.{moved}",
        "whether to work in the registered checkout instead, or unregister it "
        "and register this one",
        text=(f"Already registered: '{other['name']}' at {other_path}\n"
              f"{project_path} is another checkout (or a fork) of the same "
              "project. Endless registers one checkout per project per "
              "machine: for parallel work, use a worktree of the registered "
              "checkout (endless task claim creates one). Forks are not yet "
              f"supported.{moved}"),
    )


def _refuse_name_clash(name: str, own_id: int | None) -> None:
    """Refuse a name another registered project holds as its name or slug.

    One machine database cannot hold two projects of one name, because every
    resolver keys on it; a slug is checked too because it keeps a renamed
    project's old name (E-2217). Choosing a different slug at registration
    arrives with per-project databases (E-2215).
    """
    rows = db.query(
        "SELECT id, name, path FROM projects WHERE name = ? OR slug = ?",
        (name, name),
    )
    other = next((r for r in rows if r["id"] != own_id), None)
    if other is None:
        return
    other_path = resolved(other["path"])
    # Who picked the name decides the class, and this function cannot see
    # that: the same call arrives whether the user dictated it or --infer
    # took the directory's own. The rename refusal names both branches too.
    raise agent_help.report_if(
        f"'{name}' is already the name of the project registered at "
        f"{other_path}, and one machine cannot register two projects under one "
        "name yet. Nothing was registered.",
        "the user chose this name",
        "retry with --name set to an unused name",
        "another name for this project, which is written to its committed "
        "config, is theirs to pick",
        text=(f"Name '{name}' is already registered at {other_path}\n"
              "Choose another with --name."),
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

    rows = db.query("SELECT id, name, path, identity FROM projects ORDER BY id")
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

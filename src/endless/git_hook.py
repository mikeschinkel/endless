"""The Endless-Task trailer on task-branch commits (E-2490, ED-1641).

A commit made on a `task/<N>` branch carries `Endless-Task: E-<N>` in its
trailer block, so `git log --format='%(trailers:key=Endless-Task,valueonly)'`
maps commits to tasks without parsing subjects. Git hooks add it, so it reaches
every commit from any tool that runs them, not only an agent's.

Two hooks, because the message does not exist yet when an editor opens:

- `prepare-commit-msg` adds it when the message was given (-m, -F, --amend, a
  merge). It leaves an editor commit (source empty, or `template`) alone: a
  trailer written before the editor opens survives an aborted edit, and git
  then commits a message that is only the trailer instead of aborting.
- `commit-msg` adds it after the editor closes. `--no-verify` skips it, so an
  interactive `--no-verify` commit goes without.

Both add nothing to an empty message, for the same reason, and both always exit
zero: a hook that fails aborts the commit, and a missing trailer is never worth
that.

Every setup calls `endless git-hook <event>`, so a fix here reaches every repo
without reinstalling anything. How a repo gets set up is ED-1641's rule:
through lefthook, or into a hook file nothing else claims; never by
overwriting, renaming or removing a hook file or `core.hooksPath` Endless did
not write. For any other manager, the user gets the line to add.
"""

from __future__ import annotations

import json
import re
import shutil
import stat
import subprocess
from dataclasses import dataclass, field
from pathlib import Path

import click

from endless import agent_help

TRAILER_KEY = "Endless-Task"

# The two events Endless hooks, in the order git runs them.
EVENTS = ("prepare-commit-msg", "commit-msg")

# A task branch, as worktree_cmd.task_branch writes it (ED-1587).
_TASK_BRANCH = re.compile(r"^task/(\d+)$")

# Recognizes a hook file Endless wrote, so re-running init may rewrite it and
# nothing else.
HOOK_FILE_MARKER = "# endless git-hook (written by `endless project init`)"

# The one line a user adds to a manager Endless does not configure. `|| true`
# because the last command's status is the script's, and a missing `endless`
# must not abort the commit.
def manager_line(event: str) -> str:
    return (f'command -v endless >/dev/null && endless git-hook {event} "$@" '
            "|| true")


# lefthook's config files, in its own lookup order, and their local overrides.
LEFTHOOK_CONFIGS = (
    "lefthook.yml", ".lefthook.yml", "lefthook.yaml", ".lefthook.yaml",
    ".config/lefthook.yml", ".config/lefthook.yaml",
)
LEFTHOOK_LOCALS = (
    "lefthook-local.yml", ".lefthook-local.yml", "lefthook-local.yaml",
    ".lefthook-local.yaml", ".config/lefthook-local.yml",
    ".config/lefthook-local.yaml",
)
LEFTHOOK_COMMAND = "endless-task-trailer"

# Hook managers Endless does not configure, by the file that gives each away.
OTHER_MANAGERS = (
    (".pre-commit-config.yaml", "pre-commit"),
    (".overcommit.yml", "overcommit"),
)


# --- The hook --------------------------------------------------------------

def _git(cwd: Path | None, *args: str) -> subprocess.CompletedProcess:
    """Run git. The hook inherits git's own environment (GIT_DIR and the rest),
    which is what makes it answer about the repo committing."""
    return subprocess.run(["git", *args], cwd=cwd, capture_output=True,
                          text=True)


def task_id_for_branch(cwd: Path | None = None) -> int | None:
    """The task a `task/<N>` branch belongs to, or None on any other branch or a
    detached HEAD."""
    res = _git(cwd, "symbolic-ref", "--short", "-q", "HEAD")
    m = _TASK_BRANCH.match(res.stdout.strip()) if res.returncode == 0 else None
    return int(m.group(1)) if m else None


def _comment_char(cwd: Path | None) -> str:
    value = _git(cwd, "config", "core.commentChar").stdout.strip()
    # "auto" picks a character per message; '#' is its usual answer.
    return value if value and value != "auto" else "#"


def _has_content(text: str, comment: str) -> bool:
    """Whether a message holds anything git would keep: a non-blank line that is
    not a comment, above any scissors line."""
    for line in text.splitlines():
        if line.startswith(comment) and ">8" in line and "-" * 8 in line:
            break
        if line.strip() and not line.startswith(comment):
            return True
    return False


def add_trailer(message_file: Path, cwd: Path | None = None) -> bool:
    """Add the trailer to message_file when on a task branch and the message has
    content and no Endless-Task trailer yet. Returns whether it was added."""
    task_id = task_id_for_branch(cwd)
    if task_id is None:
        return False
    text = message_file.read_text()
    if not _has_content(text, _comment_char(cwd)):
        return False
    res = _git(cwd, "interpret-trailers", "--in-place",
               "--if-exists", "doNothing",
               "--trailer", f"{TRAILER_KEY}: E-{task_id}", str(message_file))
    return res.returncode == 0 and message_file.read_text() != text


def run_hook(event: str, message_file: str, source: str = "") -> None:
    """The body of `endless git-hook <event>`. Never raises: a hook that fails
    aborts the commit."""
    try:
        if event == "prepare-commit-msg" and source in ("", "template"):
            return  # the editor has yet to run; commit-msg adds it after
        add_trailer(Path(message_file))
    except Exception as e:  # noqa: BLE001 — see the docstring
        # A notice, not a refusal: the commit goes ahead regardless.
        agent_help.info(f"endless git-hook {event}: trailer not added: {e}",
                        err=True)


# --- What a repo has -------------------------------------------------------

@dataclass
class Repo:
    """A repo's hook arrangement, as `project init` and `git-hook status` see
    it from one checkout (the main one, or a task worktree)."""
    root: Path
    hooks_dir: Path
    hooks_path: str  # core.hooksPath, "" when unset
    manager: str  # "lefthook", "husky", "other", "hooks" or "none"
    other: str = ""  # what manages the hooks, when manager is "other"
    existing: list[str] = field(default_factory=list)  # foreign hook files

    def hook_file(self, event: str) -> Path:
        return self.hooks_dir / event

    @property
    def label(self) -> str:
        """What adds the trailer here, as a person would name it."""
        return {"none": "Endless's own Git hooks", "hooks": "Git hooks",
                "other": self.other}.get(self.manager, self.manager)


def _read(path: Path) -> str:
    try:
        return path.read_text(errors="replace")
    except OSError:
        return ""


def _is_hook(path: Path) -> bool:
    return path.is_file() and not path.name.endswith(".sample")


def is_endless_hook(path: Path) -> bool:
    return HOOK_FILE_MARKER in _read(path)


def _is_lefthook_hook(path: Path) -> bool:
    return "lefthook" in _read(path)


def inspect(root: Path) -> Repo | None:
    """Classify the repo at root's hooks, or None when root is not in a Git
    work tree."""
    top = _git(root, "rev-parse", "--show-toplevel")
    hooks = _git(root, "rev-parse", "--path-format=absolute",
                 "--git-path", "hooks")
    if top.returncode != 0 or hooks.returncode != 0:
        return None
    root = Path(top.stdout.strip())
    hooks_dir = Path(hooks.stdout.strip())
    hooks_path = _git(root, "config", "core.hooksPath").stdout.strip()
    files = sorted(p for p in hooks_dir.iterdir() if _is_hook(p)) \
        if hooks_dir.is_dir() else []
    repo = Repo(root=root, hooks_dir=hooks_dir, hooks_path=hooks_path,
                manager="none")

    if (any((root / n).is_file() for n in LEFTHOOK_CONFIGS + LEFTHOOK_LOCALS)
            or any(_is_lefthook_hook(p) for p in files)):
        repo.manager = "lefthook"
    elif (root / ".husky").is_dir() or ".husky" in hooks_path:
        repo.manager = "husky"
    elif hooks_path:
        repo.manager, repo.other = "other", f"core.hooksPath ({hooks_path})"
    else:
        for name, manager in OTHER_MANAGERS:
            if (root / name).is_file():
                repo.manager, repo.other = "other", manager
                break
    repo.existing = [p.name for p in files
                     if not is_endless_hook(p) and not _is_lefthook_hook(p)]
    if repo.manager == "none" and repo.existing:
        repo.manager = "hooks"
    return repo


# --- Is it set up? ---------------------------------------------------------

def _invokes(text: str, event: str) -> bool:
    return f"endless git-hook {event}" in text


def _lefthook_main(root: Path) -> Path | None:
    return next((root / n for n in LEFTHOOK_CONFIGS if (root / n).is_file()),
                None)


def problem(repo: Repo) -> str | None:
    """Why this checkout's task-branch commits will not get the trailer, or
    None when they will. A future `endless doctor` check (E-2285) reports it."""
    missing: list[str] = []
    if repo.manager == "lefthook":
        if _lefthook_main(repo.root) is None:
            return (f"lefthook has no lefthook.yml in {repo.root}, so it reads "
                    "no config there, lefthook-local.yml included")
        configs = "".join(_read(repo.root / n)
                          for n in LEFTHOOK_CONFIGS + LEFTHOOK_LOCALS)
        for event in EVENTS:
            hook = repo.hook_file(event)
            if _invokes(_read(hook), event):
                continue  # the line added to a hook file lefthook leaves alone
            if event in repo.existing:
                missing.append(f"{hook} is not lefthook's and does not run "
                               f"`endless git-hook {event}`")
            elif not _invokes(configs, event):
                missing.append(f"no lefthook config here runs `endless "
                               f"git-hook {event}`")
            elif not _is_lefthook_hook(hook):
                missing.append(f"lefthook is not installed for {event} "
                               "(run `lefthook install`)")
    elif repo.manager == "husky":
        for event in EVENTS:
            if not _invokes(_read(repo.root / ".husky" / event), event):
                missing.append(f".husky/{event} does not run `endless "
                               f"git-hook {event}`")
    elif repo.manager == "other":
        return (f"{repo.other} manages this repo's Git hooks, and Endless "
                "cannot confirm it runs `endless git-hook`")
    else:
        for event in EVENTS:
            if not _invokes(_read(repo.hook_file(event)), event):
                missing.append(f"{repo.hook_file(event)} does not run "
                               f"`endless git-hook {event}`")
    return "; ".join(missing) or None


# --- Setting a repo up -----------------------------------------------------

def _lefthook_run(event: str, committed: bool) -> str:
    """lefthook's run line for event. `{0}` is every argument git passed, each
    quoted; `{2}` and `{3}` would reach the command literally when git passes
    fewer."""
    cmd = f"endless git-hook {event} {{0}}"
    # A committed file reaches clones without Endless installed.
    return f"command -v endless >/dev/null || exit 0; {cmd}" if committed \
        else cmd


def _yaml_block(entries: dict[str, list[tuple[str, str]]]) -> str:
    """lefthook YAML for {event: [(command name, run line)]}. Each run line is
    a JSON string, which YAML reads as a double-quoted scalar."""
    out = []
    for event, commands in entries.items():
        out.append(f"{event}:\n  commands:")
        for name, run in commands:
            out.append(f"    {name}:\n      run: {json.dumps(run)}")
    return "\n".join(out) + "\n"


_LEFTHOOK_HEADER = ("# endless: adds the Endless-Task trailer to task-branch "
                    "commits (`endless git-hook`)")


def _has_key(text: str, event: str) -> bool:
    return re.search(rf"^{re.escape(event)}\s*:", text, re.M) is not None


def _add_to_lefthook_file(path: Path, entries) -> list[str]:
    """Append Endless's commands to a lefthook file without disturbing the rest
    of it. An event the file already lists is not appended to (a second
    top-level key is invalid YAML); its snippet is returned for the user."""
    text = _read(path)
    todo, manual = {}, []
    for event, commands in entries.items():
        if all(run in text or (name == LEFTHOOK_COMMAND and _invokes(text, event))
               for name, run in commands):
            continue
        if _has_key(text, event):
            manual.append(_yaml_block({event: commands}))
        else:
            todo[event] = commands
    if todo:
        path.parent.mkdir(parents=True, exist_ok=True)
        sep = "" if not text or text.endswith("\n\n") else (
            "\n" if text.endswith("\n") else "\n\n")
        path.write_text(text + sep + _LEFTHOOK_HEADER + "\n"
                        + _yaml_block(todo))
    return manual


def exclude_local(repo: Repo, name: str) -> None:
    """List a local lefthook file in the repo's shared info/exclude, so no
    checkout of it — the main one or a task worktree — reports it untracked."""
    if _git(repo.root, "check-ignore", "-q", name).returncode == 0:
        return
    res = _git(repo.root, "rev-parse", "--path-format=absolute",
               "--git-common-dir")
    exclude = Path(res.stdout.strip()) / "info" / "exclude"
    exclude.parent.mkdir(parents=True, exist_ok=True)
    text = _read(exclude)
    sep = "" if not text or text.endswith("\n") else "\n"
    exclude.write_text(text + sep + f"/{name}\n")


def copy_lefthook_local(main_root: Path, worktree: Path) -> list[str]:
    """Copy the main checkout's untracked lefthook-local files into a new task
    worktree: lefthook reads them from the worktree's own top level, and an
    untracked file is not checked out there."""
    copied = []
    for name in LEFTHOOK_LOCALS:
        src, dst = main_root / name, worktree / name
        if not src.is_file() or dst.exists():
            continue
        if _git(main_root, "ls-files", "--error-unmatch", name).returncode == 0:
            continue  # tracked: git checked it out already
        dst.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(src, dst)
        copied.append(name)
    return copied


def _write_hook_file(path: Path, event: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(
        "#!/bin/sh\n"
        f"{HOOK_FILE_MARKER}\n"
        "command -v endless >/dev/null || exit 0\n"
        f'endless git-hook {event} "$@" || true\n'
    )
    path.chmod(path.stat().st_mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)


@dataclass
class Outcome:
    """What setup did, and what the user should know. `lines` are printed in
    order; `set_up` says whether task-branch commits will get the trailer."""
    set_up: bool = False
    lines: list[tuple[str, str]] = field(default_factory=list)  # (glyph, text)

    def ok(self, text):
        self.lines.append(("•", text))

    def warn(self, text):
        self.lines.append(("⚠", text))


LEFTHOOK_INSTALL = ("Install lefthook (https://lefthook.dev/installation/, "
                    "e.g. `brew install lefthook` or `go install "
                    "github.com/evilmartians/lefthook@latest`)")


def _lefthook_install(repo: Repo, out: Outcome) -> bool:
    if shutil.which("lefthook") is None:
        out.warn(f"lefthook is not installed. {LEFTHOOK_INSTALL}, then run "
                 f"`lefthook install` in {repo.root}")
        return False
    res = subprocess.run(["lefthook", "install"], cwd=repo.root,
                         capture_output=True, text=True)
    if res.returncode != 0:
        out.warn("`lefthook install` failed: "
                 + (res.stderr or res.stdout).strip())
        return False
    out.ok("Ran `lefthook install`")
    return True


def _rel(repo: Repo, path: Path) -> str:
    return path.relative_to(repo.root).as_posix()


def _would_rename(repo: Repo, migrate) -> list[str]:
    """The foreign hook files `lefthook install` would rename: those of the
    events lefthook's config here names, other than the ones being migrated."""
    configs = "".join(_read(repo.root / n)
                      for n in LEFTHOOK_CONFIGS + LEFTHOOK_LOCALS)
    return [e for e in repo.existing
            if e not in migrate and _has_key(configs, e)]


def _endless_entries(committed: bool, events) -> dict[str, list[tuple[str, str]]]:
    return {e: [(LEFTHOOK_COMMAND, _lefthook_run(e, committed))]
            for e in events}


def _setup_lefthook(repo: Repo, lefthook_file: str, out: Outcome,
                    migrate: list[str] = ()) -> None:
    """Add Endless's commands to lefthook's config, in lefthook-local.yml or the
    committed lefthook.yml as the setting chooses, migrating `migrate`'s hook
    files (renamed <event>.old by `lefthook install`) into lefthook-local.yml:
    their paths are this clone's."""
    # lefthook renames a foreign hook file for an event its config names, on
    # `lefthook install` and on its own next sync, and that file then stops
    # running; ED-1641 rules that out, so such an event is left to that file.
    claimed = [e for e in EVENTS if e in repo.existing and e not in migrate]
    unclaimed = [e for e in EVENTS if e not in claimed]
    todo = [e for e in claimed
            if not _invokes(_read(repo.hook_file(e)), e)]
    if todo:
        out.warn(
            ", ".join(str(repo.hook_file(e)) for e in todo)
            + " is not lefthook's, and naming the event in lefthook's config "
            "would have lefthook rename it. For the Endless-Task trailer, add "
            "to it yourself:\n"
            + "\n".join(f"  {manager_line(e)}" for e in todo))
    committed = lefthook_file == "committed"
    main = _lefthook_main(repo.root)
    created_main = main is None
    if created_main:
        main = repo.root / "lefthook.yml"
        main.write_text(
            "# lefthook configuration (https://lefthook.dev/configuration/).\n"
            "# Created by `endless project init`: lefthook reads "
            "lefthook-local.yml only beside this file.\n")
    local = next((repo.root / n for n in LEFTHOOK_LOCALS
                  if (repo.root / n).is_file()), repo.root / LEFTHOOK_LOCALS[0])

    local_entries: dict[str, list[tuple[str, str]]] = {}
    for event in migrate:
        old = repo.hooks_dir / f"{event}.old"
        local_entries.setdefault(event, []).append(
            ("existing-hook", f"'{old}' {{0}}"))
    endless = _endless_entries(committed, unclaimed)
    target = main if committed else local
    if target == local:
        for event, commands in endless.items():
            local_entries.setdefault(event, []).extend(commands)
        manual = _add_to_lefthook_file(local, local_entries)
    else:
        manual = _add_to_lefthook_file(main, endless)
        if local_entries:
            manual += _add_to_lefthook_file(local, local_entries)
    if local.is_file():
        exclude_local(repo, _rel(repo, local))
    if created_main:
        out.ok(f"Created {_rel(repo, main)}: lefthook reads "
               f"{_rel(repo, local)} only beside it")
    out.ok(f"lefthook runs `endless git-hook` from {_rel(repo, target)}")
    for snippet in manual:
        out.warn(f"Not added: the file already configures that event. Add "
                 f"these commands to it yourself:\n{snippet}")
    if manual:
        return
    # The same rule, for the events the user's own config already names.
    claimed = _would_rename(repo, migrate)
    if claimed:
        out.warn("Not running `lefthook install`: it would rename "
                 + ", ".join(str(repo.hook_file(e)) for e in claimed)
                 + ", which Endless did not write. Add `endless git-hook` to "
                 "them yourself, or move them into lefthook and run "
                 "`lefthook install`")
    elif all(_is_lefthook_hook(repo.hook_file(e)) for e in unclaimed):
        out.set_up = not todo  # lefthook's dispatcher is already there
    else:
        out.set_up = _lefthook_install(repo, out) and not todo


def setup(root: Path, choice: str | None, interactive: bool,
          lefthook_file: str) -> Outcome:
    """Set the repo at root up to add the trailer, as `project init` does.

    choice is --git-hooks ("lefthook" or "none"), or None to ask (interactive)
    or take the default: a plain hook file where nothing claims one, and no
    lefthook adoption where hooks exist (that renames them, so it needs a yes).
    """
    out = Outcome()
    repo = inspect(root)
    if repo is None:
        out.warn("Not a Git repository: task-branch commits will not get "
                 "the Endless-Task trailer")
        return out

    if repo.manager == "lefthook":
        _setup_lefthook(repo, lefthook_file, out)
    elif repo.manager in ("husky", "other"):
        where = (lambda e: f".husky/{e}") if repo.manager == "husky" else (
            lambda e: f"{repo.other}'s {e} hook")
        out.warn(
            f"{'husky' if repo.manager == 'husky' else repo.other} manages "
            "this repo's Git hooks, which Endless does not configure. For the "
            "Endless-Task trailer on task-branch commits, add:\n"
            + "\n".join(f"  to {where(e)}:\n    {manager_line(e)}"
                        for e in EVENTS))
    elif repo.manager == "hooks":
        agree = choice == "lefthook"
        if choice is None and interactive:
            preview = _yaml_block({
                e: [("existing-hook", f"'{repo.hooks_dir / e}.old' {{0}}")]
                for e in repo.existing})
            click.echo(
                f"This repo has Git hooks ({', '.join(repo.existing)}) and no "
                "hook manager. To add the Endless-Task trailer, Endless can "
                "move them into lefthook: `lefthook install` renames each to "
                "<name>.old, and lefthook-local.yml runs it from there:\n\n"
                + preview)
            agree = click.confirm("Move these hooks into lefthook?",
                                  default=False)
        if agree:
            _setup_lefthook(repo, lefthook_file, out, migrate=repo.existing)
        else:
            out.warn(
                "Task-branch commits will not get the Endless-Task trailer: "
                f"this repo's Git hooks ({', '.join(repo.existing)}) were not "
                "moved into lefthook, and in a repo with hooks of its own "
                "Endless adds the trailer only through a hook manager. Re-run "
                "`endless project init --git-hooks=lefthook` to move them")
    else:  # none
        if choice is None and interactive:
            choice = click.prompt(
                "Add the Endless-Task trailer through lefthook, or a plain "
                "Git hook (none)?",
                type=click.Choice(["lefthook", "none"]), default="none")
        if choice == "lefthook":
            _setup_lefthook(repo, lefthook_file, out)
        else:
            for event in EVENTS:
                _write_hook_file(repo.hook_file(event), event)
            out.ok(f"Wrote {', '.join(EVENTS)} in {repo.hooks_dir}")
            out.set_up = True
    return out


def echo_outcome(out: Outcome) -> None:
    for glyph, text in out.lines:
        color = "cyan" if glyph == "•" else "yellow"
        click.echo(click.style(glyph, fg=color) + " " + text)

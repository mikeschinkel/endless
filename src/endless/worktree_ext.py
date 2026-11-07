"""The worktree extension point (E-2563).

Endless's core runs a fixed list of *worktree extensions* at two moments in a
task worktree's life:

- right after `git worktree add`, in `_bootstrap_task_worktree`, before the
  project's post-worktree-create hook; and
- before a session starts in a worktree that already exists — on `task
  spawn`, `task claim`, `task prime`, and `session resume` / `session goto
  --resume`.

An extension is a callable that takes the worktree path. It decides for
itself whether it applies, by looking at the worktree, and it must be
idempotent, because it runs at every start of a session in that worktree.
The core knows nothing about any language or stack: what it holds is the
list of built-in extension modules, and each of those registers itself with
`register` when it is imported. A failing extension prints a warning naming
itself and the worktree, and the spawn, claim or resume goes on — the
worktree is usable without whatever the extension would have added, and the
extension runs again at the next start.

Why not the project's post-worktree-create hook: that hook is the project's,
written per repository, and it runs once at creation. An extension is
Endless's own, applies to every project it recognises, and runs at every
start, so a worktree created before the extension existed — or one whose
file was deleted later — is brought up to date the next time a session
enters it.
"""

from __future__ import annotations

import importlib
from pathlib import Path
from typing import Callable, NamedTuple

import click

from endless import agent_help, config


class Extension(NamedTuple):
    name: str
    run: Callable[[Path], object]


# The built-in extension modules. Each registers itself on import; the core
# names the module, never what it does. A module that later moves out of
# Endless (the Go one is slated for Gomion) leaves this tuple, nothing else.
_BUILTIN_MODULES: tuple[str, ...] = ("endless.go_support",)

_extensions: list[Extension] = []
_builtins_loaded = False


def register(name: str, run: Callable[[Path], object]) -> None:
    """Register `run` as the worktree extension called `name`.

    `name` is what a failure is reported under, so it should read as a thing
    (`go-support`), not a sentence. Registering a name twice replaces the
    earlier registration rather than running it twice: a module re-imported
    under test, or an extension that moves between modules, must not double
    up.
    """
    for i, ext in enumerate(_extensions):
        if ext.name == name:
            _extensions[i] = Extension(name, run)
            return
    _extensions.append(Extension(name, run))


def registered() -> list[Extension]:
    """The extensions in the order they run, built-ins loaded."""
    _load_builtins()
    return list(_extensions)


def run_extensions(worktree: Path) -> None:
    """Run every registered extension against `worktree`, in order.

    Never raises on an extension's behalf: a failure is printed, naming the
    extension and the worktree, and the next extension still runs. The
    caller — a spawn, a claim or a resume — is not stopped by it.
    """
    _load_builtins()
    for ext in list(_extensions):
        try:
            ext.run(worktree)
        except Exception as e:  # noqa: BLE001 — the contract is "never stop the caller"
            _report_failure(ext.name, worktree, e)


def _load_builtins() -> None:
    global _builtins_loaded
    if _builtins_loaded:
        return
    _builtins_loaded = True
    for module in _BUILTIN_MODULES:
        importlib.import_module(module)


def _report_failure(name: str, worktree: Path, error: Exception) -> None:
    where = config.tilde(worktree)
    detail = str(error).strip() or error.__class__.__name__
    # NO-REPORT: nothing was decided and nothing is withheld. The worktree
    # exists and the session starts in it; what the extension would have
    # added is missing, the cause is in `detail`, and the extension runs
    # again by itself at the next claim, spawn or resume, so the recovery is
    # to fix the cause and let it.
    agent_help.warn.no_report(
        f"worktree extension {name} failed in {where}: {detail}. The "
        f"worktree is usable without it.",
        "Fix what it names; the extension runs again at the next claim, "
        "spawn or resume of this worktree",
        text=(
            click.style(f"⚠ worktree extension {name} failed", fg="yellow")
            + f"\n    worktree: {where}\n"
            f"    {detail}\n"
            f"    The worktree is usable without it; the extension runs again at\n"
            f"    the next claim, spawn or resume."
        ),
    )

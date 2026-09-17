"""Endless's own post-worktree-create hook runs recipes from MAIN's justfile.

Endless runs the hook that lives in the main checkout, with cwd set to the
worktree. A bare `just <recipe>` there resolves the WORKTREE's justfile — the
same file for a worktree created a moment ago, but an older branch's justfile
for anything else, which is how a sweep over existing worktrees failed with
"Justfile does not contain recipe `go-work-init`" and a recipe calling a binary
that no longer exists. These tests pin the three facts that keep the hook
version-consistent with itself.
"""

import re
import shutil
import subprocess
from pathlib import Path

import pytest

HOOK = Path(".endless/hooks/post-worktree-create.sh")
JUSTFILE = Path("justfile")


def _hook_text() -> str:
    return HOOK.read_text()


def _called_recipes() -> list[str]:
    return re.findall(r"^\s*recipe\s+([\w-]+)", _hook_text(), re.M)


def test_no_recipe_is_run_against_the_worktrees_own_justfile():
    """The only `just` invocation is the wrapper, and the wrapper names main's
    justfile and the worktree as the working directory."""
    lines = [
        ln for ln in _hook_text().splitlines()
        if re.match(r"\s*just\s", ln)
    ]
    assert len(lines) == 1, f"expected only the wrapper to call just, got: {lines}"
    wrapper = lines[0]
    assert '--justfile "${main_checkout}/justfile"' in wrapper
    assert '--working-directory "${worktree}"' in wrapper


def test_main_checkout_is_known_before_the_first_recipe():
    text = _hook_text()
    first_recipe = re.search(r"^\s*recipe\s+[\w-]+", text, re.M)
    assigned = re.search(r"^main_checkout=", text, re.M)
    assert first_recipe and assigned
    assert assigned.start() < first_recipe.start()


def test_the_hook_calls_recipes_that_exist():
    called = _called_recipes()
    assert called, "the hook calls no recipes; the test is reading the wrong file"
    if shutil.which("just") is None:
        pytest.skip("just is not installed")
    summary = subprocess.run(
        ["just", "--justfile", str(JUSTFILE), "--summary"],
        capture_output=True, text=True, check=True,
    ).stdout.split()
    missing = [r for r in called if r not in summary]
    assert not missing, f"recipes the hook calls but the justfile lacks: {missing}"


def test_called_recipes_do_not_locate_themselves_by_the_justfile():
    """What makes the wrapper safe: each recipe finds everything from its working
    directory. One that used justfile_directory() would, under the wrapper, act
    on the MAIN checkout instead of the worktree."""
    if shutil.which("just") is None:
        pytest.skip("just is not installed")
    offenders = []
    for name in _called_recipes():
        body = subprocess.run(
            ["just", "--justfile", str(JUSTFILE), "--show", name],
            capture_output=True, text=True, check=True,
        ).stdout
        if re.search(r"justfile_directory|source_directory|invocation_directory", body):
            offenders.append(name)
    assert not offenders, f"recipes that locate themselves by the justfile: {offenders}"

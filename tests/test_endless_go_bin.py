"""Every test runs the endless-go this session built, and nothing else (E-1921).

conftest's pytest_sessionstart builds `./cmd/endless-go` once and routes every
lookup to it. These tests pin the routing, so a later edit that reintroduces a
route to `<checkout>/bin/endless-go` or a PATH-found global fails here instead
of silently testing Python against a stale binary — the E-1917 incident.
"""

import os
import re
import shutil
from pathlib import Path

from endless import config, statuses

TESTS_DIR = Path(__file__).resolve().parent
CHECKOUT_BIN = (TESTS_DIR.parent / "bin" / "endless-go").resolve()


def test_path_finds_the_session_build_first(endless_go_bin):
    assert Path(shutil.which("endless-go")).resolve() == endless_go_bin.resolve()


def test_the_session_build_is_not_the_checkout_bin(endless_go_bin):
    assert endless_go_bin.resolve() != CHECKOUT_BIN
    assert os.access(endless_go_bin, os.X_OK)


def test_the_worktree_lookup_never_answers_the_checkout_bin():
    """The one lookup that bypasses PATH. Inside a self-dev worktree it would
    name `<checkout>/bin/endless-go` by absolute path; from the main checkout it
    answers None. Either way it must not hand a test the checkout's binary."""
    found = config.worktree_endless_go()
    assert found is None or found.resolve() != CHECKOUT_BIN


def test_the_status_vocabulary_was_read_from_the_session_build(endless_go_bin):
    """Read at import, so this proves the build and routing ran before
    collection — the timing a fixture alone cannot provide."""
    assert Path(statuses._binary()).resolve() == endless_go_bin.resolve()


def test_no_test_resolves_the_binary_itself():
    """A test that runs endless-go takes the `endless_go_bin` fixture. One that
    reaches `<checkout>/bin/endless-go` or builds its own is the stale-binary
    route this task removed, so neither may come back."""
    checkout_bin = re.compile(r"(__file__|REPO_ROOT|parents?\b).*[\"']bin[\"']")
    offenders = []
    for path in sorted(TESTS_DIR.glob("test_*.py")):
        if path.name == Path(__file__).name:
            continue
        for n, line in enumerate(path.read_text().splitlines(), 1):
            if checkout_bin.search(line) or "./cmd/endless-go" in line:
                offenders.append(f"{path.name}:{n}: {line.strip()}")
    assert offenders == []

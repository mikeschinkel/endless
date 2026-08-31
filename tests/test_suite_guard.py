"""The in-file guard on per-task verification suites (E-2090).

A verify suite is a land-time proof of one task, and running someone else's —
or running your own outside the runner's isolation — has twice taken down
session tracking in the user's real database. The guard that refuses those two
things lives INSIDE the suite, so it travels with the file.

These tests are here rather than only in E-2090's own suite because of what the
suites' own rules say: a behaviour checked only in a verify suite is unprotected
the moment that task lands. The guard is the thing protecting every other
suite — it is the last thing that should rot.

The behavioural tests drive the real `_guard.sh` from a synthetic checkout, so
they assert what a suite actually does rather than what the file appears to say.
"""

import os
import shutil
import subprocess
from pathlib import Path

import pytest

REPO = Path(__file__).resolve().parents[1]
TASKS = REPO / ".endless" / "tasks"
GUARD = TASKS / "_guard.sh"
HARNESS = TASKS / "_harness.sh"
SUITES = sorted(TASKS.glob("e-*/verify.sh"))

BANNER = "# ── DO NOT EDIT"


def _first_code_line(path: Path) -> str:
    for line in path.read_text().splitlines():
        stripped = line.strip()
        if stripped and not stripped.startswith("#"):
            return stripped
    return ""


def test_there_are_suites_to_guard():
    """A glob that silently matched nothing would make every test below pass."""
    assert len(SUITES) > 100, f"only {len(SUITES)} suites found under {TASKS}"


HARNESS_LINE = 'source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"'


def _sources(path: Path, target: str) -> list[str]:
    """Executable lines that SOURCE target — mentions in prose do not count.

    A suite is allowed to talk about the harness, and E-2023's names it in a
    comment, copies it as a fixture, and writes it into a heredoc. Only a line
    that actually sources it changes what running the suite does.
    """
    out = []
    for line in path.read_text().splitlines():
        stripped = line.strip()
        if stripped.startswith("#"):
            continue
        if (stripped.startswith("source ") or stripped.startswith(". ")) and target in stripped:
            out.append(stripped)
    return out


@pytest.mark.parametrize("suite", SUITES, ids=lambda p: p.parent.name)
def test_every_suite_reaches_the_guard_on_its_first_executable_line(suite):
    """The guard must have had its say before any of the suite's own code.

    First line specifically, not merely present: several suites `cd` early, and
    the guard locates itself and its task from the running file's path.
    """
    assert _first_code_line(suite) == HARNESS_LINE


@pytest.mark.parametrize("suite", SUITES, ids=lambda p: p.parent.name)
def test_no_suite_sources_the_guard_directly_as_well(suite):
    """One path in, or the refusal prints twice.

    The harness sources the guard, so a suite that also sources it directly is
    doubly guarded — which is the one shape the plan for this ruled out.
    """
    assert _sources(suite, "_guard.sh") == []


@pytest.mark.parametrize("suite", SUITES, ids=lambda p: p.parent.name)
def test_every_suite_names_its_own_owner_in_a_do_not_edit_banner(suite):
    """The banner is the E-1916 Arm 1 interlock: the file says who may edit it."""
    task = "E-" + suite.parent.name.removeprefix("e-")
    lines = suite.read_text().splitlines()
    assert sum(1 for ln in lines if ln.startswith(BANNER)) == 1, "banner count != 1"
    assert lines[0].startswith("#!"), "the shebang must stay on line 1"
    assert lines[1].startswith(BANNER), "the banner belongs directly below the shebang"
    assert f"This suite belongs to {task} " in "\n".join(lines), (
        f"the banner does not name {task} — it names another task's id"
    )


def test_the_harness_reaches_the_guard_before_defining_anything():
    """The guard runs before the harness's own vocabulary exists.

    Not literally the first line any more — the harness first locates the guard
    and refuses if it is absent — but before PASS_COUNT, before any helper, and
    so before anything a suite could call.
    """
    code = [ln.strip() for ln in HARNESS.read_text().splitlines()
            if ln.strip() and not ln.strip().startswith("#")]
    assert "_guard.sh" in code[0], "the harness does not go looking for the guard first"
    sourced = code.index('source "${_endless_guard}"')
    defined = next(i for i, ln in enumerate(code) if ln.startswith("PASS_COUNT="))
    assert sourced < defined, "the harness defines its vocabulary before the guard runs"


def _marker_lines(path: Path, comment: str) -> tuple[list[str], list[str]]:
    """Lines mentioning the removed marker, split into (code, prose)."""
    code, prose = [], []
    for line in path.read_text().splitlines():
        if "ENDLESS_VERIFY_RUN" not in line:
            continue
        (prose if line.strip().startswith(comment) else code).append(line.strip())
    return code, prose


def test_no_marker_variable_grants_permission_anywhere():
    """ENDLESS_VERIFY_RUN was a claim a caller could make about itself.

    Nothing read its value; one `export` satisfied it. It is gone, and this test
    is what stops it — or a replacement spelled differently — coming back into
    the runner or the harness by way of a convenient bypass.

    Two files may still NAME it, in prose only: the guard that replaced it and
    the runner that used to export it. A hundred-odd worktrees predate this
    change and still contain the old marker, so somebody will grep for it; a
    name that appears nowhere at all reads as a file that was never there.
    """
    absent = [
        (HARNESS, "#"),
        (REPO / "internal" / "verifycmd" / "verify.go", "//"),
        (REPO / "src" / "endless" / "suite_rules.py", "#"),
    ]
    for path, comment in absent:
        code, prose = _marker_lines(path, comment)
        assert not code and not prose, (
            f"{path.relative_to(REPO)} still refers to the removed run marker"
        )

    prose_only = [(GUARD, "#"), (REPO / "internal" / "verifycmd" / "script.go", "//")]
    for path, comment in prose_only:
        code, prose = _marker_lines(path, comment)
        assert not code, (
            f"{path.relative_to(REPO)} USES the removed marker, not just names it: {code}"
        )
        assert prose, (
            f"{path.relative_to(REPO)} no longer explains where the marker went"
        )


# --- behaviour --------------------------------------------------------------

def _checkout(tmp_path: Path, worktree_task: str | None, suite_task: str) -> Path:
    """Build a synthetic checkout and return the suite path inside it.

    worktree_task=None puts the suite in a plain checkout rather than a task
    worktree — the reaped-worktree and no-worktrees-project shape.
    """
    root = tmp_path / "proj"
    if worktree_task is not None:
        root = root / ".endless" / "worktrees" / f"e-{worktree_task}"
    tasks = root / ".endless" / "tasks"
    (tasks / f"e-{suite_task}").mkdir(parents=True)
    shutil.copy(GUARD, tasks / "_guard.sh")
    suite = tasks / f"e-{suite_task}" / "verify.sh"
    suite.write_text(
        '#!/usr/bin/env bash\n'
        'source "$(dirname "${BASH_SOURCE[0]}")/../_guard.sh"\n'
        'echo RAN\n'
    )
    suite.chmod(0o755)
    return suite


def _run(suite: Path, home: Path) -> subprocess.CompletedProcess:
    """Run the suite with an isolated HOME/XDG, the way the runner does."""
    env = dict(os.environ)
    env["HOME"] = str(home)
    env["XDG_CONFIG_HOME"] = str(home / ".config")
    return subprocess.run(["bash", str(suite)], capture_output=True, text=True, env=env)


def test_refuses_another_tasks_suite_from_your_worktree(tmp_path):
    suite = _checkout(tmp_path, worktree_task="2090", suite_task="1001")
    res = _run(suite, tmp_path / "home")
    assert res.returncode == 2
    assert "RAN" not in res.stdout, "the suite's own code ran before the refusal"
    assert "E-1001" in res.stderr and "E-2090" in res.stderr, (
        "the refusal must name both the suite's owner and where the caller is"
    )


def test_no_environment_variable_can_grant_permission(tmp_path):
    """The refusal above must not be bypassable by claiming to be the runner."""
    suite = _checkout(tmp_path, worktree_task="2090", suite_task="1001")
    env = dict(os.environ)
    env.update({
        "HOME": str(tmp_path / "home"),
        "XDG_CONFIG_HOME": str(tmp_path / "home" / ".config"),
        "ENDLESS_VERIFY_RUN": "/tmp/anything",
        "ENDLESS_VERIFY_TASK": "E-1001",
    })
    res = subprocess.run(["bash", str(suite)], capture_output=True, text=True, env=env)
    assert res.returncode == 2 and "RAN" not in res.stdout


def test_allows_a_suite_in_its_own_worktree(tmp_path):
    suite = _checkout(tmp_path, worktree_task="1001", suite_task="1001")
    res = _run(suite, tmp_path / "home")
    assert res.returncode == 0 and "RAN" in res.stdout, res.stderr


def test_abstains_outside_any_worktree(tmp_path):
    """A reaped worktree, and a project that uses none, run from the checkout.

    The runner already falls back to cwd for exactly those two cases, so a
    refusal here would make a real task unverifiable rather than prevent a
    mistake.
    """
    suite = _checkout(tmp_path, worktree_task=None, suite_task="1001")
    res = _run(suite, tmp_path / "home")
    assert res.returncode == 0 and "RAN" in res.stdout, res.stderr


def test_a_missing_guard_is_fatal_rather_than_a_warning(tmp_path):
    """The harness must refuse when _guard.sh is not beside it.

    `source` on an absent file prints an error and carries on, so the suite
    would run with no ownership and no isolation check — and still report a
    pass. That is worse than no guard at all: it looks like the check happened.
    A fixture that copies the harness without the guard is the shape that found
    this, and it is the shape a hand-assembled project tree takes too.
    """
    root = tmp_path / "proj" / ".endless" / "worktrees" / "e-102"
    tasks = root / ".endless" / "tasks"
    (tasks / "e-101").mkdir(parents=True)
    shutil.copy(HARNESS, tasks / "_harness.sh")  # deliberately WITHOUT _guard.sh
    suite = tasks / "e-101" / "verify.sh"
    suite.write_text(
        '#!/usr/bin/env bash\n'
        'source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"\n'
        'report_pass "ran unguarded"\n'
        'summary\n'
    )
    suite.chmod(0o755)

    res = _run(suite, tmp_path / "home")
    assert res.returncode == 2, "a tree missing the guard must refuse, not proceed"
    assert "ran unguarded" not in res.stdout, "the suite ran with no guard at all"
    assert "guard is missing" in res.stderr

    # And with the guard restored it is the ownership refusal that fires, not
    # this one — so the check above is not quietly masking the real guard.
    shutil.copy(GUARD, tasks / "_guard.sh")
    res = _run(suite, tmp_path / "home")
    assert res.returncode == 2
    assert "E-101" in res.stderr and "E-102" in res.stderr


def test_refuses_when_a_real_config_is_reachable(tmp_path):
    """Ownership alone still lets the owner run it by hand against a live config.

    That is how a suite drove the hook binary into the main database. The check
    is on the environment's actual shape, so the only way to satisfy it is to
    genuinely be isolated.
    """
    suite = _checkout(tmp_path, worktree_task="1001", suite_task="1001")
    home = tmp_path / "home"
    (home / ".config" / "endless").mkdir(parents=True)
    res = _run(suite, home)
    assert res.returncode == 2
    assert "RAN" not in res.stdout
    assert "endless task verify E-1001" in res.stderr, (
        "the refusal must name the sanctioned command for THIS suite"
    )

"""E-1940: the Go and Python default-branch resolvers must agree, case for case.

There are two implementations because there are two runtimes: `monitor.DefaultBranch`
backs the ◆ probe and the worktree reaper, `worktree_cmd._default_base_branch`
backs `worktree land` and worktree creation. They resolve the SAME question, and
a project that lands into `master` while the marker measures against `main` is
the bug E-1940 exists to remove — so the agreement is asserted here rather than
described in a comment on either side.

The Go side is reached through `session-query worktree-unsettled`, whose JSON
carries the resolved `base`. That is a real product surface rather than a
test-only hook, so this cannot pass by testing something no user reaches.

Absorbs E-1166, whose finding — `origin/HEAD` is unset until `git remote
set-head` runs, so a fresh clone falls through step 2 — is why the order has
four steps at all.
"""

import json
import subprocess
from pathlib import Path

import pytest

from endless import worktree_cmd

REPO_ROOT = Path(__file__).resolve().parent.parent


@pytest.fixture(scope="session")
def endless_go(tmp_path_factory) -> Path:
    """Build endless-go from THIS checkout.

    Built rather than found on PATH on purpose: a globally installed binary
    belongs to whatever landed last, and a parity test that silently compared
    today's Python against last month's Go would assert nothing.
    """
    out = tmp_path_factory.mktemp("go-bin") / "endless-go"
    subprocess.run(
        ["go", "build", "-o", str(out), "./cmd/endless-go"],
        cwd=str(REPO_ROOT), check=True, capture_output=True, text=True,
    )
    return out


def _git(repo: Path, *args: str) -> None:
    subprocess.run(["git", "-C", str(repo), *args], check=True, capture_output=True)


def _make_repo(path: Path, branch: str) -> Path:
    """A git repo with one commit on `branch`, isolated from the developer's
    own git config so a machine-wide init.defaultBranch cannot leak in."""
    path.mkdir(parents=True, exist_ok=True)
    subprocess.run(
        ["git", "init", f"--initial-branch={branch}", str(path)],
        check=True, capture_output=True,
        env={"PATH": "/usr/bin:/bin:/usr/local/bin", "GIT_CONFIG_NOSYSTEM": "1",
             "HOME": str(path)},
    )
    _git(path, "config", "user.name", "t")
    _git(path, "config", "user.email", "t@example.com")
    (path / "f.txt").write_text("x\n")
    _git(path, "add", "f.txt")
    _git(path, "commit", "-m", "initial")
    return path


def _write_project_config(repo: Path, default_branch: str) -> None:
    cfg = repo / ".endless"
    cfg.mkdir(exist_ok=True)
    (cfg / "config.json").write_text(
        json.dumps({"name": "fixture", "default_branch": default_branch}))


def _go_resolution(endless_go: Path, repo: Path) -> tuple[str, str]:
    """Return (base, base_error) as the Go resolver reports them."""
    res = subprocess.run(
        [str(endless_go), "session-query", "worktree-unsettled", str(repo)],
        capture_output=True, text=True, check=True,
    )
    # E-1668: session-query wraps array payloads so an empty result can still
    # name the database it came from.
    probe = json.loads(res.stdout)["rows"][0]
    return probe.get("base", ""), probe.get("base_error", "")


def _python_resolution(repo: Path) -> tuple[str, str]:
    """Return (base, error) as the Python resolver reports them."""
    try:
        return worktree_cmd._default_base_branch(repo), ""
    except worktree_cmd.DefaultBranchUnresolved as exc:
        return "", str(exc)


# Each case: (name, initial branch, init.defaultBranch or None,
#             .endless/config.json default_branch or None, expected branch or None)
# expected None means "neither implementation may name a branch".
CASES = [
    # Step 4: neither config nor detection has anything to say; `master` exists.
    # This is the case that used to exit 128 forever against a hardcoded `main`.
    ("master fallback", "master", None, None, "master"),
    # Step 3 must verify its answer: init.defaultBranch is a preference of the
    # MACHINE, and `main` there on a `master` repo is the common real setup.
    ("init.defaultBranch names no branch here", "master", "main", None, "master"),
    # ...but it is honoured when it does name a branch.
    ("init.defaultBranch is right", "trunk", "trunk", None, "trunk"),
    # Step 1 beats detection.
    ("project config wins", "release", "release", "release", "release"),
    # Step 1's one asymmetry: a typo does not fall through to detection.
    ("project config typo", "main", None, "mian", None),
    # Nothing can name it: both must refuse rather than substitute `main`.
    ("unresolvable", "develop", None, None, None),
]


@pytest.mark.parametrize(
    "name,branch,init_default,project_default,expected",
    CASES, ids=[c[0] for c in CASES],
)
def test_go_and_python_resolvers_agree(
    endless_go, tmp_path, name, branch, init_default, project_default, expected
):
    repo = _make_repo(tmp_path / "repo", branch)
    if init_default:
        _git(repo, "config", "init.defaultBranch", init_default)
    if project_default:
        _write_project_config(repo, project_default)

    go_base, go_err = _go_resolution(endless_go, repo)
    py_base, py_err = _python_resolution(repo)

    assert go_base == py_base, (
        f"{name}: Go resolved {go_base!r}, Python resolved {py_base!r}"
    )
    assert bool(go_err) == bool(py_err), (
        f"{name}: Go error={go_err!r}, Python error={py_err!r}"
    )

    if expected is None:
        assert go_base == "", f"{name}: expected no answer, Go gave {go_base!r}"
        assert go_err and py_err, f"{name}: expected both to refuse"
        assert "main" not in (go_base, py_base), (
            f"{name}: a resolver substituted `main` — the failure E-1940 removes"
        )
    else:
        assert go_base == expected, f"{name}: Go resolved {go_base!r}"
        assert py_base == expected, f"{name}: Python resolved {py_base!r}"


def test_python_resolver_never_falls_back_to_main(tmp_path):
    """The E-1166 limitation, stated as a prohibition.

    `_default_base_branch` used to `return "main"` on any failure. That is what
    made the bug permanent rather than rare: on a repo whose default branch
    differs, every probe built on the result exits 128 forever.
    """
    repo = _make_repo(tmp_path / "repo", "develop")
    with pytest.raises(worktree_cmd.DefaultBranchUnresolved):
        worktree_cmd._default_base_branch(repo)

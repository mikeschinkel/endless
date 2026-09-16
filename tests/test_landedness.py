"""`task show` STATES landedness, and `task unlanded` reports it (E-2095).

The `Landed:` line used to render only when a `task_landings` row existed, so
"landed before landings were recorded" and "the branch still holds the work"
both rendered as nothing — absence doing work it cannot do. E-1115 sat `assumed`
in the second of those states and the same bug was fixed a second time fourteen
days later as E-1395.

The split with the Go tests is deliberate. Every claim about what git can see —
which commits a rebase leaves behind, which paths a commit touched — is proved
against real repositories in internal/monitor/task_landedness_test.go. What is
proved here is the half Python owns: which population gets asked, and how the
four verdicts render. So these stub the probe rather than building a repo, and a
stub is honest here precisely because the verdict's derivation is somebody
else's tested contract.
"""

import json

import pytest
from click.testing import CliRunner

from endless import cli, db, task_cmd

_TASK = 1


def _add_task(title: str, status: str, type_id: int = _TASK) -> int:
    cur = db.execute(
        "INSERT INTO tasks (project_id, title, status, type_id, phase, created_at) "
        "VALUES (1, ?, ?, ?, 'now', datetime('now'))",
        (title, status, type_id),
    )
    return cur.lastrowid


def _record_landing(task_id: int, sha: str = "abc1234def") -> None:
    db.execute(
        "INSERT INTO task_landings (task_id, merge_commit_sha, landed_at) "
        "VALUES (?, ?, '2026-08-30T22:16:00')",
        (task_id, sha),
    )


def _verdict(**over) -> dict:
    base = {
        "branch": "task/1",
        "branch_exists": True,
        "base": "main",
        "unlanded_count": 0,
        "unlanded_log": [],
        "undetermined": False,
        "interrupted": False,
    }
    base.update(over)
    return base


@pytest.fixture
def stub_probe(monkeypatch):
    """Answer every branch with one canned verdict, and record what was asked.

    Returns a setter; the list it exposes is how the tests below assert that the
    probe was NOT run for a population that should not pay for it.
    """
    asked: list[list[str]] = []
    state = {"verdict": _verdict()}

    def fake(root, branches):
        asked.append(list(branches))
        return [dict(state["verdict"], branch=b) for b in branches]

    monkeypatch.setattr(task_cmd, "_landedness_probe", fake)

    def set_verdict(**over):
        state["verdict"] = _verdict(**over)

    set_verdict.asked = asked
    return set_verdict


@pytest.fixture(autouse=True)
def _project(seeded_project_at_cwd):
    """Every test needs a registered project at cwd: `task show` resolves the
    repository root from the project row to hand the probe a directory."""


def _show(task_id: int, *args) -> str:
    result = CliRunner().invoke(cli.main, ["task", "show", f"E-{task_id}", *args])
    assert result.exit_code == 0, result.output
    return result.output


def _landed_line(output: str) -> str | None:
    for line in output.split("\n"):
        if line.startswith("Landed:"):
            return line.split(":", 1)[1].strip()
    return None


# ─── which tasks are asked the question ─────────────────────────────────────


@pytest.mark.parametrize("status", ["confirmed", "assumed", "completed"])
def test_finished_work_always_states_landedness(stub_probe, status):
    """The whole point. No landing row and a clean branch used to render as
    nothing at all, which reads as "not applicable" rather than "never"."""
    task_id = _add_task("Fix the thing", status)
    assert _landed_line(_show(task_id)) == "never"


@pytest.mark.parametrize("status", ["underway", "unverified", "unreviewed",
                                    "ready", "declined", "obsolete"])
def test_unfinished_and_abandoned_work_is_not_asked(stub_probe, status):
    """Two exclusions, for opposite reasons. `unverified`/`unreviewed` work is
    SUPPOSED to be sitting on a branch — reporting it would flag the normal
    state of every task awaiting its user. `declined`/`obsolete` never shipped,
    so "never" carries no information."""
    task_id = _add_task("Fix the thing", status)
    assert _landed_line(_show(task_id)) is None
    assert stub_probe.asked == [], "the probe ran for a task it should not have"


def test_a_recorded_landing_skips_the_probe(stub_probe):
    """A recorded landing is a fact; consulting git to restate it would spend a
    git call per `task show` on the common case."""
    task_id = _add_task("Fix the thing", "assumed")
    _record_landing(task_id)
    assert _landed_line(_show(task_id)).startswith("2026-08-30")
    assert stub_probe.asked == []


def test_the_branch_asked_about_is_derived_from_the_id(stub_probe):
    """ED-1587: the name is a pure function of the task id, never a lookup.
    E-2108 retired `task_landings.branch` on exactly that basis."""
    task_id = _add_task("Fix the thing", "confirmed")
    _show(task_id)
    assert stub_probe.asked == [[f"task/{task_id}"]]


# ─── the four verdicts ──────────────────────────────────────────────────────


def test_unlanded_commits_are_named_in_the_line(stub_probe):
    stub_probe(unlanded_count=3, unlanded_log=["aaa1111 E-1: fix"])
    task_id = _add_task("Fix the thing", "assumed")
    assert _landed_line(_show(task_id)) == "never — 3 unlanded commits"


def test_one_unlanded_commit_is_singular(stub_probe):
    stub_probe(unlanded_count=1)
    task_id = _add_task("Fix the thing", "assumed")
    assert _landed_line(_show(task_id)) == "never — 1 unlanded commit"


def test_an_unresolved_base_is_unknown_not_never(stub_probe):
    """`unknown` is a third value and never collapses into `never`: a probe that
    could not run has not established that the work did not land. Rendering it
    as `never` is the failure E-1940 removed."""
    stub_probe(undetermined=True, base="",
               base_error="no default branch could be resolved")
    task_id = _add_task("Fix the thing", "assumed")
    assert _landed_line(_show(task_id)) == "unknown — base branch unresolved"


def test_a_failed_probe_names_the_git_command(stub_probe):
    stub_probe(undetermined=True,
               probe_error="git range-diff: fatal: need two commit ranges")
    task_id = _add_task("Fix the thing", "assumed")
    assert _landed_line(_show(task_id)) == "unknown — git range-diff failed"


def test_an_interrupted_probe_does_not_say_failed(stub_probe):
    """Quitting a view sends SIGINT to the whole process group, so an in-flight
    git probe dies with its parent. The verdict is unchanged — the probe
    established nothing — but "git range-diff failed" is a false statement about
    a healthy repository somebody just pressed Ctrl-C in."""
    stub_probe(undetermined=True, interrupted=True,
               probe_error="git range-diff: signal: interrupt")
    task_id = _add_task("Fix the thing", "assumed")
    assert _landed_line(_show(task_id)) == "unknown — git range-diff interrupted"


def test_an_interrupted_base_resolution_does_not_say_unresolved(stub_probe):
    stub_probe(undetermined=True, interrupted=True, base="",
               base_error="cannot resolve the repository's default branch")
    task_id = _add_task("Fix the thing", "assumed")
    assert _landed_line(_show(task_id)) == "unknown — base resolution interrupted"


def test_a_missing_branch_reads_never_not_clean(stub_probe):
    """Reaped, or never created. There is nothing outstanding AND nothing on
    record, and the field's value is the second of those."""
    stub_probe(branch_exists=False)
    task_id = _add_task("Fix the thing", "completed")
    assert _landed_line(_show(task_id)) == "never"


@pytest.mark.parametrize("over", [
    {},
    {"unlanded_count": 320},
    {"undetermined": True, "base_error": "no default branch could be resolved"},
    {"undetermined": True, "probe_error": "git range-diff: fatal: bad revision"},
    {"undetermined": True, "interrupted": True,
     "probe_error": "git range-diff: signal: interrupt"},
    {"undetermined": True, "interrupted": True, "base_error": "unresolved"},
])
def test_every_form_fits_the_line_budget(stub_probe, over):
    """HARD 60 characters including the label. These are read in split panes,
    and a status field that wraps looks broken in a way a long TITLE does not —
    a title is user content and obviously continues, a field value is not."""
    stub_probe(**over)
    task_id = _add_task("Fix the thing", "assumed")
    line = next(l for l in _show(task_id).split("\n") if l.startswith("Landed:"))
    assert len(line) <= task_cmd.LANDING_LINE_BUDGET, line


# ─── the other two output modes ─────────────────────────────────────────────


def test_agent_mode_carries_the_verdict_and_the_commits(stub_probe):
    stub_probe(unlanded_count=2,
               unlanded_log=["aaa1111 E-1: fix", "bbb2222 E-1: more"])
    task_id = _add_task("Fix the thing", "assumed")
    out = _show(task_id, "--agent")
    assert "landed=never — 2 unlanded commits" in out
    assert "unlanded aaa1111 E-1: fix" in out


def test_json_carries_the_probe_as_a_key(stub_probe):
    stub_probe(unlanded_count=1, unlanded_log=["aaa1111 E-1: fix"])
    task_id = _add_task("Fix the thing", "assumed")
    out = json.loads(_show(task_id, "--json"))
    assert out["landed"] is None
    assert out["landedness"]["summary"] == "never — 1 unlanded commit"
    assert out["landedness"]["unlanded_count"] == 1


def test_json_landedness_is_null_when_the_question_does_not_apply(stub_probe):
    task_id = _add_task("Fix the thing", "underway")
    assert json.loads(_show(task_id, "--json"))["landedness"] is None


# ─── task unlanded ──────────────────────────────────────────────────────────


def _unlanded(*args) -> str:
    result = CliRunner().invoke(cli.main, ["task", "unlanded", *args])
    assert result.exit_code == 0, result.output
    return result.output


def test_the_two_sections_are_split_and_labelled(stub_probe, monkeypatch):
    """Collapsing them would report every finished task in the project as
    outstanding when a handful are actionable — measured on this repository,
    122 versus 0."""
    outstanding = _add_task("Fix the outstanding thing", "assumed")
    unrecorded = _add_task("Fix the unrecorded thing", "completed")

    def fake(root, branches):
        return [
            _verdict(branch=b, unlanded_count=2 if b == f"task/{outstanding}" else 0)
            for b in branches
        ]

    monkeypatch.setattr(task_cmd, "_landedness_probe", fake)
    out = json.loads(_unlanded("--json"))["rows"][0]
    assert [r["id"] for r in out["outstanding"]] == [f"E-{outstanding}"]
    assert [r["id"] for r in out["unrecorded"]] == [f"E-{unrecorded}"]


def test_an_undetermined_row_is_outstanding_not_unrecorded(stub_probe):
    """It must never read as "nothing to land". Nobody knows whether there is."""
    stub_probe(undetermined=True, probe_error="git merge-base: fatal: bad object")
    _add_task("Fix the thing", "assumed")
    out = json.loads(_unlanded("--json"))["rows"][0]
    assert len(out["outstanding"]) == 1
    assert out["outstanding"][0]["reason"] == "undetermined (git merge-base failed)"
    assert out["unrecorded"] == []


def test_an_interrupted_row_is_outstanding_and_says_interrupted(stub_probe):
    stub_probe(undetermined=True, interrupted=True,
               probe_error="git merge-base: signal: interrupt")
    _add_task("Fix the thing", "assumed")
    out = json.loads(_unlanded("--json"))["rows"][0]
    assert out["outstanding"][0]["reason"] == "undetermined (git merge-base interrupted)"


def test_a_landed_task_with_a_clean_branch_is_in_neither_section(stub_probe):
    task_id = _add_task("Fix the thing", "assumed")
    _record_landing(task_id)
    out = json.loads(_unlanded("--json"))["rows"][0]
    assert out["outstanding"] == []
    assert out["unrecorded"] == []


def test_a_landed_task_whose_branch_moved_on_is_outstanding(stub_probe):
    """A landing row is not the end of the story: `worktree land` is
    append-only, and a branch can gain work after one."""
    task_id = _add_task("Fix the thing", "assumed")
    _record_landing(task_id)
    stub_probe(unlanded_count=1)
    out = json.loads(_unlanded("--json"))["rows"][0]
    assert [r["id"] for r in out["outstanding"]] == [f"E-{task_id}"]


def test_the_base_branch_is_named_once_per_report(stub_probe):
    """Never per row — `task unsettled` set the precedent. It is what keeps
    "not on <base>" falsifiable without paying for the name on every line."""
    stub_probe(base="trunk", unlanded_count=1)
    _add_task("Fix the thing", "assumed")
    report = json.loads(_unlanded("--json"))["rows"][0]
    assert report["base"] == "trunk"

    human = _unlanded()
    assert "are not on trunk:" in human
    assert human.count("trunk") == 1, human


def test_no_surface_writes_the_word_main(stub_probe):
    """E-1940: substituting `main` is the bug the resolver exists to remove, and
    it fails in the one direction that matters — a false all-clear on a repo
    that named its default branch anything else."""
    stub_probe(base="trunk", unlanded_count=1)
    _add_task("Fix the thing", "assumed")
    assert "main" not in _unlanded()


def test_nothing_outstanding_says_so_rather_than_saying_nothing(stub_probe):
    _add_task("Fix the thing", "assumed")
    _record_landing(1)
    assert "has landed on main" in _unlanded()

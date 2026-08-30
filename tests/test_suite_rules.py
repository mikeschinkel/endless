"""The verification-suite rules ship with every registered project (E-2023).

A file that existed only in Endless's own checkout would teach nobody, and the
mistakes it exists to prevent are not Endless-specific. So registration places
it, the same way it places `.gitignore` entries and `.endless/tmp/` — and, like
those, re-running registration must neither duplicate it nor revert a project's
own edits.
"""

from pathlib import Path

from endless.register import register_project
from endless.suite_rules import SUITE_RULES, scaffold_suite_rules


def _rules_path(project: Path) -> Path:
    return project / ".endless" / "tasks" / "CLAUDE.md"


def test_registration_places_the_rules(isolated_env):
    project_dir = isolated_env["projects_root"] / "fresh-proj"
    project_dir.mkdir()

    register_project(project_dir, infer=True)

    rules = _rules_path(project_dir)
    assert rules.is_file(), "a freshly registered project has no .endless/tasks/CLAUDE.md"
    assert rules.read_text() == SUITE_RULES


def test_reregistration_does_not_overwrite_a_customized_copy(isolated_env):
    project_dir = isolated_env["projects_root"] / "customized-proj"
    project_dir.mkdir()

    register_project(project_dir, infer=True)
    rules = _rules_path(project_dir)
    rules.write_text("# Our own rules\n")

    register_project(project_dir, infer=True)

    assert rules.read_text() == "# Our own rules\n"


def test_scaffold_reports_whether_it_wrote(tmp_path):
    assert scaffold_suite_rules(tmp_path) is True
    assert scaffold_suite_rules(tmp_path) is False


def test_the_rules_name_the_things_the_refusals_point_at():
    """The runner's refusal and the harness both send a reader here by path.

    If the file stops covering what those messages promise it covers, the
    refusal becomes a dead link — which is worse than no reference at all,
    because it reads as though the reasoning exists somewhere.
    """
    for phrase in (
        "endless task verify",
        "_harness.sh",
        "land-time gate",
        "Do not run another task's suite",
        "Do not edit a landed task's suite",
    ):
        assert phrase in SUITE_RULES, f"the suite rules no longer mention {phrase!r}"


def test_endless_ships_the_same_file_it_scaffolds():
    """Endless's own copy is the scaffolded text, not a hand-maintained twin.

    Two copies of a rules file drift, and the one that drifts is always the one
    nobody is reading.
    """
    repo_copy = Path(__file__).resolve().parents[1] / ".endless" / "tasks" / "CLAUDE.md"
    assert repo_copy.is_file()
    assert repo_copy.read_text() == SUITE_RULES

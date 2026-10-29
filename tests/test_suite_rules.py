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

from conftest import init_git_repo


def _rules_path(project: Path) -> Path:
    return project / ".endless" / "tasks" / "CLAUDE.md"


def test_registration_places_the_rules(isolated_env):
    project_dir = isolated_env["projects_root"] / "fresh-proj"
    init_git_repo(project_dir)

    register_project(project_dir, infer=True)

    rules = _rules_path(project_dir)
    assert rules.is_file(), "a freshly registered project has no .endless/tasks/CLAUDE.md"
    assert rules.read_text() == SUITE_RULES


def test_reregistration_does_not_overwrite_a_customized_copy(isolated_env):
    project_dir = isolated_env["projects_root"] / "customized-proj"
    init_git_repo(project_dir)

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


def _section(heading: str) -> str:
    """The text of one `## ` section of SUITE_RULES, heading included."""
    start = SUITE_RULES.index(f"## {heading}\n")
    end = SUITE_RULES.find("\n## ", start + 1)
    return SUITE_RULES[start:] if end < 0 else SUITE_RULES[start:end]


def test_the_rules_teach_the_manifest_form_only():
    """E-2419: SUITE_RULES ships to every project, and only the endless repo has
    `_harness.sh` and `_guard.sh`. So the opening lines, the file table and
    "Writing one" name no harness, guard or script template — a project that
    was told to source a harness it does not have could not write a suite.
    """
    opening = SUITE_RULES[: SUITE_RULES.index("\n## ")]
    file_table = _section("Three kinds of file, and only one of them is yours")
    writing = _section("Writing one")
    script_only = ("_guard.sh", "#!/usr/bin/env bash", "source \"$(dirname", "assert_eq",
                   "as_agent", "with_tmux")

    for name, text in (("opening lines", opening), ("file table", file_table)):
        for word in ("_harness.sh", "verify.sh", *script_only):
            assert word not in text, f"the {name} name {word!r}"

    for word in script_only:
        assert word not in writing, f"'Writing one' names {word!r}"
    for want in ("verify.toml", "[[check]]", "sh ", "contains", "not_contains",
                 "equals", "name", "Native tests first"):
        assert want in writing, f"'Writing one' does not teach {want!r}"


def test_one_line_sends_a_script_suite_reader_to_the_harness_header():
    lines = [line for line in SUITE_RULES.splitlines() if "_harness.sh" in line]
    assert len(lines) == 1, f"_harness.sh is named on {len(lines)} lines: {lines}"
    paragraph = _section("Writing one").split("\n\n")
    sends = [p for p in paragraph if "_harness.sh" in p]
    assert sends and "header" in sends[0] and "verify.sh" in sends[0], sends

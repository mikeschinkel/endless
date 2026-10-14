"""The decisions guide carries the tests for what counts as a decision (E-2317).

The guide used to say "Always document decisions where applicable", with no
filter but preference vs prohibition. A brainstorm handoff that asked for
decisions unconditionally then filed ED-1606, a one-to-one description of one
task's implementation. These tests pin the section that replaced it, and that
every guide page telling an agent to `decision add` points back to it.
"""

from pathlib import Path

GUIDE_DIR = Path(__file__).resolve().parent.parent / "docs" / "guide"
DECISIONS = (GUIDE_DIR / "decisions.md").read_text()
TESTS = "real alternative, outlives the task, the user made it"


def _section(text: str, heading: str) -> str:
    start = text.index(heading)
    end = text.find("\n## ", start + len(heading))
    return text[start:end if end != -1 else len(text)]


def test_unconditional_filing_instruction_is_gone():
    assert "Always document decisions" not in DECISIONS


def test_is_it_a_decision_states_the_three_tests():
    s = _section(DECISIONS, "## Is it a decision?")
    for test in ("**Real alternative.**", "**Outlives the task.**",
                 "**The user made it.**"):
        assert test in s
    assert "All three must pass" in s


def test_is_it_a_decision_lists_the_qualifying_kinds():
    s = _section(DECISIONS, "## Is it a decision?")
    assert "at least one" in s
    for kind in ("project's structure", "cuts across components",
                 "scope boundary or a deferral",
                 "convention the project's users see",
                 "reverses or modifies an earlier decision"):
        assert kind in s


def test_is_it_a_decision_says_zero_is_normal_and_routes_failures_to_a_lesson():
    s = _section(DECISIONS, "## Is it a decision?")
    assert "Zero decisions is the normal outcome" in s
    assert "one-to-one onto a single task's implementation" in s
    assert "endless lesson write" in s
    assert "When it is unclear which, it is a lesson." in s


def test_strong_guidance_points_to_the_tests():
    s = _section(DECISIONS, "## STRONG guidance")
    assert "(#is-it-a-decision)" in s


# Each guide paragraph that tells an agent to file a decision, found by a
# phrase of its own. Mentions of `decision add` as mechanism (what it commits,
# what relation it creates) are not filing prompts and are not listed.
FILING_PROMPTS = [
    ("index.md", "# Record a decision discovered during work"),
    ("tasks.md", "To record a decision prompted by a task"),
    ("tasks.md", "spawn the follow-up tasks/decisions it produced"),
    ("tasks.md", "A brainstorm is typically a *precursor*"),
]


def test_every_filing_prompt_points_to_the_tests():
    for name, phrase in FILING_PROMPTS:
        text = (GUIDE_DIR / name).read_text()
        paras = [p for p in text.split("\n\n") if phrase in p]
        assert len(paras) == 1, (name, phrase)
        assert "endless guide decisions" in paras[0], (name, phrase)
        assert "three tests" in paras[0], (name, phrase)

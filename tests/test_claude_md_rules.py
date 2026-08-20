"""CLAUDE.md stays a short list of rules, and every fact in it stays true.

ED-1564: CLAUDE.md is loaded into every session's context unconditionally, so
its length is a permanent per-session token tax on this repo alone. It carries
only what is true HERE AND NOWHERE ELSE, states WHAT and never WHY, and holds no
rationale, no history, no mechanism, no task ids. Anything an agent on any
Endless project needs lives in `endless guide`, which is pulled on demand.

The checks below are the mechanical half of that. They cannot judge whether a
sentence is rationale — that is Mike's review — but they can hold the size
budget, keep the guide's canonical blocks from being duplicated back in, and
catch the failure that motivated the audit: rules naming commands that do not
exist. E-1817 found three of those live in the file at once (`just install`
from a worktree "never", `/usr/local/bin/endless-hook`, `endless-sandbox
destroy`), each steering an agent away from the supported path.
"""

import re
from pathlib import Path

_REPO_ROOT = Path(__file__).resolve().parent.parent
_CLAUDE_MD = _REPO_ROOT / "CLAUDE.md"
_JUSTFILE = _REPO_ROOT / "justfile"

# 59 lines today. The budget is headroom for a rule Mike adds, not room to grow
# back toward the 440 lines E-2014 replaced.
_MAX_LINES = 80

_NUMBER_WORDS = {
    "one": 1, "two": 2, "three": 3, "four": 4, "five": 5, "six": 6,
    "seven": 7, "eight": 8, "nine": 9, "ten": 10,
}


def _text() -> str:
    return _CLAUDE_MD.read_text()


def test_stays_within_the_size_budget():
    lines = _text().splitlines()
    assert len(lines) <= _MAX_LINES, (
        f"CLAUDE.md is {len(lines)} lines, over the {_MAX_LINES}-line budget. "
        f"Every session in this repo pays for it. Workflow belongs in "
        f"`endless guide`; rationale belongs in an accepted decision."
    )


def test_cites_no_task_or_decision_ids():
    """No history, no pointers — a rule that needs a citation is rationale."""
    ids = sorted(set(re.findall(r"\b(?:ED?-\d{3,})\b", _text())))
    assert not ids, (
        f"CLAUDE.md cites {', '.join(ids)}. An agent cannot act on a task id, "
        f"and resolving one costs a lookup every session. State the rule; put "
        f"the reasoning in an accepted decision."
    )


def test_embeds_no_canonical_block_from_the_guide():
    """A section duplicated from the guide is a defect, not redundancy.

    The guide is canonical; a copy here goes stale silently. The status
    lifecycle lived in CLAUDE.md this way until E-1817 deleted it.
    """
    assert "<!-- BEGIN canonical:" not in _text(), (
        "CLAUDE.md embeds a canonical block copied from elsewhere in the repo. "
        "Point at `endless guide` instead."
    )


def _justfile_recipes() -> set[str]:
    return {
        m.group(1)
        for m in re.finditer(r"^([A-Za-z_][A-Za-z0-9_-]*)(?:\s|:)", _JUSTFILE.read_text(), re.M)
    }


def test_every_just_recipe_it_names_exists():
    named = set(re.findall(r"`just ([a-z][a-z0-9-]*)", _text()))
    assert named, "CLAUDE.md names no `just` recipe — did the Build section move?"
    missing = sorted(named - _justfile_recipes())
    assert not missing, (
        f"CLAUDE.md names `just {'`, `just '.join(missing)}`, which the justfile "
        f"does not define. A rule pointing at a command that does not exist "
        f"steers an agent off the supported path."
    )


def _python_sqlite_files() -> list[str]:
    src = _REPO_ROOT / "src" / "endless"
    return sorted(p.name for p in src.rglob("*.py") if "sqlite3" in p.read_text())


def test_the_direct_sql_file_count_is_current():
    """The count is a countdown to zero — it has to be the real one.

    Python reading SQLite directly is temporary; all database access is moving
    to Go. A stale number reads as "still six to go" after the work is done.
    """
    m = re.search(r"directly in (\w+) files", _text())
    assert m, "CLAUDE.md no longer states how many Python files read SQLite directly."
    stated = _NUMBER_WORDS.get(m.group(1).lower())
    assert stated is not None, f"unparseable count in CLAUDE.md: {m.group(1)!r}"
    actual = _python_sqlite_files()
    assert stated == len(actual), (
        f"CLAUDE.md says {m.group(1)} Python files read SQLite directly; there "
        f"are {len(actual)}: {', '.join(actual)}. Update the sentence — or "
        f"delete it, if the answer is now zero."
    )

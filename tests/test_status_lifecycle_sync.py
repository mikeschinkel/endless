"""The canonical status-lifecycle mermaid stays byte-identical everywhere.

`docs/status-lifecycle.mmd` is the single source of truth for the task status
diagram; README.md and docs/guide/index.md embed it verbatim between
BEGIN/END markers. Drift means an agent reading one of the copies gets a
different lifecycle than the one the CLI implements.

CLAUDE.md was a third copy until E-1817 removed it: the project CLAUDE.md
states WHAT, not WHY, and the lifecycle it duplicated is already canonical in
`endless guide`.

This is a permanent invariant, not point-in-time acceptance. It had been
re-asserted by hand in three separate per-task verify scripts (e-1648, e-1832,
e-1845); E-1889 promoted it here so the copies stop multiplying — a per-task
script is retired once its task lands, and this invariant outlives all of them.
"""

from pathlib import Path

import pytest

_REPO_ROOT = Path(__file__).resolve().parent.parent
_CANON = _REPO_ROOT / "docs" / "status-lifecycle.mmd"
_COPIES = ("README.md", "docs/guide/index.md")

_BEGIN = "<!-- BEGIN canonical:docs/status-lifecycle.mmd"
_END = "<!-- END canonical:docs/status-lifecycle.mmd"


def _embedded_block(path: Path) -> str:
    """The mermaid body a doc embeds between the canonical markers."""
    text = path.read_text()
    start = text.index(_BEGIN)
    end = text.index(_END, start)
    block = text[start:end]
    fence_open = block.index("```mermaid\n") + len("```mermaid\n")
    fence_close = block.index("\n```", fence_open) + 1
    return block[fence_open:fence_close]


def test_canonical_file_exists():
    assert _CANON.is_file(), f"{_CANON} is the source of truth and must exist"


@pytest.mark.parametrize("doc", _COPIES)
def test_copy_is_byte_identical_to_the_canonical_file(doc):
    path = _REPO_ROOT / doc
    assert path.is_file(), f"{doc} is missing"
    assert _embedded_block(path) == _CANON.read_text(), (
        f"{doc}'s embedded mermaid has drifted from docs/status-lifecycle.mmd. "
        f"Edit the canonical file, then re-sync the copies."
    )


def test_canonical_diagram_admits_reopening_landed_work():
    """E-1889: the three end states have an edge back to `revisit`.

    The CLI has always accepted confirmed/assumed/completed → revisit; the
    diagram did not draw it, which is what made reopening your own landed work
    read as unsupported. `declined`/`obsolete` deliberately gain no edge —
    reversing a decision is an explicit act, not a lifecycle transition.
    """
    canon = _CANON.read_text()
    for status in ("confirmed", "assumed", "completed"):
        assert f"{status} --> revisit" in canon
    for status in ("declined", "obsolete"):
        assert f"{status} --> revisit" not in canon

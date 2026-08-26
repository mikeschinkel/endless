"""The committed status-lifecycle artifacts are current.

`internal/taskstatus/transitions.go` is the source of truth for the task status
lifecycle. `docs/status-lifecycle.mmd` is rendered from it, and README.md and
docs/guide/index.md embed that file verbatim between BEGIN/END markers.

This test's job NARROWED in E-2018. It used to assert that three hand-maintained
copies matched EACH OTHER — which said nothing about whether any of them matched
the CLI, and they didn't: `blocked` had no node in the diagram at all, and
`completed` had no inbound edge, so a guard derived from the picture would have
refused the very transition that closed E-1817. Now the diagram is generated, so
what is worth asserting is the `gofmt -l` shape: is the committed artifact what
the generator produces today? A table edited without running `just
lifecycle-index` fails here.

The byte-identity of the two copies is still asserted, because it is still what
makes an agent reading README get the same lifecycle as one reading the guide.

This is a permanent invariant, not point-in-time acceptance. It had been
re-asserted by hand in three separate per-task verify scripts (e-1648, e-1832,
e-1845); E-1889 promoted it here so the copies stop multiplying — a per-task
script is retired once its task lands, and this invariant outlives all of them.
"""

import pytest

from endless import lifecycle_map


def test_canonical_file_exists():
    assert lifecycle_map.CANONICAL.is_file(), (
        f"{lifecycle_map.CANONICAL} is the committed artifact and must exist"
    )


def test_committed_artifacts_match_the_go_transition_table():
    """The `gofmt -l` shape: regenerating must be a no-op."""
    stale = lifecycle_map.drift()
    assert stale == [], (
        "these lifecycle artifacts no longer match "
        f"internal/taskstatus/transitions.go: {', '.join(stale)}. "
        "Run `just lifecycle-index` and commit the result."
    )


@pytest.mark.parametrize("doc", lifecycle_map.COPIES)
def test_copy_is_byte_identical_to_the_canonical_file(doc):
    path = lifecycle_map._REPO_ROOT / doc
    assert path.is_file(), f"{doc} is missing"
    assert lifecycle_map._embedded_block(path) == lifecycle_map.CANONICAL.read_text(), (
        f"{doc}'s embedded mermaid has drifted from docs/status-lifecycle.mmd. "
        "Run `just lifecycle-index`."
    )


def test_canonical_diagram_admits_reopening_landed_work():
    """E-1889: the three end states have an edge back to `revisit`.

    The CLI has always accepted confirmed/assumed/completed → revisit; the
    diagram did not draw it, which is what made reopening your own landed work
    read as unsupported. `declined`/`obsolete` deliberately gain no edge to
    `revisit` — reversing an abandonment decision re-enters at `untriaged`
    instead (E-2018), so a reconsidered task is re-triaged like any other.
    """
    canon = lifecycle_map.CANONICAL.read_text()
    for status in ("confirmed", "assumed", "completed"):
        assert f"{status} --> revisit" in canon
    for status in ("declined", "obsolete"):
        assert f"{status} --> revisit" not in canon


def test_the_preamble_survives_regeneration():
    """Only the marked block is generated; the editorial prose is hand-written.

    Same split `just guide-index` uses. Without it the rationale that belongs
    with the picture — why blocking is not a state, why epic status is not
    drawn — would have to live in a Go string literal.
    """
    text = lifecycle_map.CANONICAL.read_text()
    preamble = text[: text.index(lifecycle_map.BEGIN_MARKER)]
    assert "blocked_by" in preamble, (
        "the hand-written preamble was lost; it explains what the diagram "
        "deliberately does not draw"
    )
    assert preamble in lifecycle_map.assemble_canonical()

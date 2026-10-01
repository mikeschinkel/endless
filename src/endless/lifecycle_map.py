"""Render docs/status-lifecycle.mmd from the Go transition table, and re-sync
its two embedded copies.

The lifecycle diagram used to be hand-authored and byte-copied into README.md
and docs/guide/index.md, with a test asserting the three matched each other.
Three copies agreeing said nothing about whether any of them matched the CLI —
and they didn't: `blocked` had no node at all, and `completed` had no inbound
edge, so a guard derived from the picture would have refused the very transition
that closed E-1817.

E-2018 inverted it. `internal/taskstatus/transitions.go` is the source; this
module renders it. Drift between the rule and the picture stops being something
a test detects and becomes something that cannot happen.

Deliberately the same shape as `endless.guide_map`, which already runs this
pattern for the guide's command cross-reference: `index` rewrites the generated
block, `check` exits non-zero on drift, and both are wrapped by a just recipe
(`just lifecycle-index` / `just lifecycle-check`). The split of labour matches
the rest of the codebase too — Go owns the table and renders the mermaid, Python
owns the file surgery.

Dev-side, like guide_map: reached through the just recipes and the pytest
invariant, never through the `endless` CLI.
"""

from pathlib import Path

from endless import agent_help, statuses

_REPO_ROOT = Path(__file__).resolve().parent.parent.parent

CANONICAL = _REPO_ROOT / "docs" / "status-lifecycle.mmd"

# The two docs that embed the canonical file verbatim. Both carry the
# BEGIN/END canonical markers around a ```mermaid fence.
COPIES = ("README.md", "docs/guide/index.md")

# Markers inside the .mmd itself, delimiting the part Go renders. The preamble
# above BEGIN is hand-written editorial and survives regeneration.
BEGIN_MARKER = "%% BEGIN generated: rendered from internal/taskstatus/transitions.go"
END_MARKER = "%% END generated"

# Markers in the embedding docs, delimiting the whole canonical file.
COPY_BEGIN = "<!-- BEGIN canonical:docs/status-lifecycle.mmd"
COPY_END = "<!-- END canonical:docs/status-lifecycle.mmd"


def rendered_block() -> str:
    """The generated mermaid body, straight from the Go table.

    Shelling out rather than reimplementing: the table has an actor column and a
    per-type column that a second renderer would have to interpret identically
    forever. One renderer, in the language that owns the data.
    """
    return statuses.lifecycle()


def assemble_canonical() -> str:
    """The full .mmd: the file's own hand-written preamble, then the rendering.

    Requires the markers to already be present — they are placed once by hand,
    exactly as guide_map requires of index.md.
    """
    # Nothing normally reads either of these: this module is reached through
    # `just lifecycle-index` / `just lifecycle-check` and the pytest invariant
    # inside Endless's own checkout, never through the `endless` CLI. They are
    # classified for the reader they do reach, and the two differ:
    if not CANONICAL.is_file():
        # A checked-in artifact that is simply not there is the checkout being
        # wrong about itself, with no remedy this can name — `fault`.
        raise agent_help.fault(
            f"{CANONICAL} is missing, so the lifecycle diagram cannot be "
            "assembled. Nothing was written.",
            text=f"{CANONICAL} is missing; it is the canonical artifact.",
        )
    text = CANONICAL.read_text()
    if BEGIN_MARKER not in text or END_MARKER not in text:
        # The file IS there and the fix is an exactly-specified edit to it —
        # small, safe, and no judgement about anyone's data — so NO-REPORT.
        raise agent_help.no_report(
            f"{CANONICAL} has no generated-block markers, so the rendered "
            "diagram has nowhere to go. Nothing was written.",
            f"Add a '{BEGIN_MARKER}' / '{END_MARKER}' pair around the diagram, "
            "then re-run",
            text=(f"{CANONICAL} has no generated-block markers. Add a "
                  f"'{BEGIN_MARKER}' / '{END_MARKER}' pair around the diagram."),
        )
    preamble = text[: text.index(BEGIN_MARKER)]
    return f"{preamble}{BEGIN_MARKER}\n{rendered_block()}{END_MARKER}\n"


def _embedded_block(path: Path) -> str:
    """The mermaid body a doc embeds between the canonical markers."""
    text = path.read_text()
    start = text.index(COPY_BEGIN)
    end = text.index(COPY_END, start)
    block = text[start:end]
    fence_open = block.index("```mermaid\n") + len("```mermaid\n")
    fence_close = block.index("\n```", fence_open) + 1
    return block[fence_open:fence_close]


def _replace_embedded_block(path: Path, canonical: str) -> bool:
    """Rewrite one doc's embedded copy. Returns True if the file changed."""
    text = path.read_text()
    current = _embedded_block(path)
    if current == canonical:
        return False
    path.write_text(text.replace(current, canonical, 1))
    return True


def update() -> list[str]:
    """Regenerate the canonical file and both copies. Returns what changed."""
    changed: list[str] = []

    canonical = assemble_canonical()
    if CANONICAL.read_text() != canonical:
        CANONICAL.write_text(canonical)
        changed.append(str(CANONICAL.relative_to(_REPO_ROOT)))

    for doc in COPIES:
        path = _REPO_ROOT / doc
        if not path.is_file():
            raise agent_help.fault(
                f"{doc} is missing, so the copy it must embed cannot be "
                f"rewritten. {len(changed)} file(s) were already updated.",
                text=f"{doc} is missing; it must embed the canonical file.",
            )
        if _replace_embedded_block(path, canonical):
            changed.append(doc)

    return changed


def drift() -> list[str]:
    """The files whose committed content is not what the table renders."""
    stale: list[str] = []

    canonical = assemble_canonical()
    if CANONICAL.read_text() != canonical:
        stale.append(str(CANONICAL.relative_to(_REPO_ROOT)))

    for doc in COPIES:
        path = _REPO_ROOT / doc
        if not path.is_file() or _embedded_block(path) != canonical:
            stale.append(doc)

    return stale


def run_cli(argv: list[str]) -> int:
    cmd = argv[0] if argv else ""
    if cmd == "index":
        changed = update()
        print("status lifecycle: " + (
            "regenerated " + ", ".join(changed) if changed else "already in sync."
        ))
        return 0
    if cmd == "check":
        stale = drift()
        if not stale:
            print("status lifecycle: in sync with internal/taskstatus.")
            return 0
        print("status lifecycle: STALE — " + ", ".join(stale))
        print("  The transition table changed and the artifacts did not.")
        print("  Run `just lifecycle-index` and commit the result.")
        return 1
    print("usage: python -m endless.lifecycle_map {index|check}")
    return 2


if __name__ == "__main__":
    import sys

    # Click's handler is not installed on this path — this module is run as a
    # script by the just recipes — so the refusal is rendered and its status
    # passed on by hand, exactly as guide_map does.
    try:
        _code = run_cli(sys.argv[1:])
    except agent_help.Refusal as _refusal:
        # `info(..., err=True)` rather than `refusal.show()`: show() is Click's
        # renderer and would prepend "Error: ", which a bare SystemExit(str)
        # never did. The bytes a person reads here stay what they were.
        agent_help.info(_refusal.format_message(), err=True)
        _code = _refusal.exit_code
    agent_help.passthrough_exit(_code)

"""Every anchor in the refusal inventory still resolves against the tree.

`docs/research-2026-09-17-refusal-inventory.tsv` records each refusal and warning
Endless can emit, anchored to the file and symbol that raises it (E-2155). It is
read row by row by the conversion work in E-2159, so a row naming a file or a
symbol that no longer exists is not cosmetic — it is a row nobody can act on.

WHY THIS TEST EXISTS. The inventory is a snapshot of the code, and code moves.
E-2155 anchored it to file+symbol rather than line numbers precisely so ordinary
edits would not invalidate it, and marked the rows that were already stale. But
nothing then CHECKED the anchors, so the next landing to move a symbol broke
them again silently — E-2157 did exactly that, moving four flag sentinels out of
internal/monitor into internal/dbcontext and leaving the rows behind. The gap is
not that someone forgot; it is that forgetting was free.

WHAT IT ASSERTS, and deliberately no more: that each row's FILE exists and its
SYMBOL is declared there. It does not check that the message text still matches.
It cannot: 212 of the 553 live Go rows carry a paraphrase rather than the literal
(`(passes the stderr through)`, `usage + subcommand list`), and a check that
fails on those would be turned off rather than fixed.

RETIRED / RELOCATED ROWS ARE EXEMPT, by their marker in the notes column. A row
whose surface is gone is not a broken anchor, it is a documented one, and E-2155
established that vocabulary. A row may be marked only when the surface really is
gone or moved — the marker is an answer, not a way to silence this test.
"""

import re
from pathlib import Path

import pytest

TSV = Path(__file__).resolve().parents[1] / "docs" / "research-2026-09-17-refusal-inventory.tsv"
ROOT = TSV.parents[1]

# The notes-column vocabulary for a surface that is gone or has moved.
MARKED = re.compile(r"\b(?:RETIRED|RELOCATED|RELOCATE):", re.I)

# A symbol the inventory uses when a message has no enclosing declaration.
PACKAGE_LEVEL = "(package level)"

_DECL = re.compile(
    r"^(?:func (?:\([^)]*\) )?(?P<f>\w+)"
    r"|(?:var|const|type) (?P<v>\w+)"
    r"|\t(?P<g>\w+)\s*(?:=|\w))",
    re.M,
)


def _rows():
    text = TSV.read_text()
    header, *body = text.split("\n")
    for n, line in enumerate(body, start=2):
        if not line.strip():
            continue
        fields = line.split("\t")
        assert len(fields) == 10, f"line {n}: {len(fields)} fields, want 10"
        yield n, fields


def _declared(path: Path) -> set[str]:
    src = path.read_text()
    return {m.group("f") or m.group("v") or m.group("g") for m in _DECL.finditer(src)} - {None}


def _live_go_rows():
    for n, f in _rows():
        if f[2] != "go" or not f[0].endswith(".go") or MARKED.search(f[9]):
            continue
        yield n, f[0], f[1]


def test_the_inventory_is_well_formed():
    assert list(_rows()), "the inventory is empty"


def test_every_anchored_file_exists():
    missing = [
        f"line {n}: {path} ({sym})"
        for n, path, sym in _live_go_rows()
        if not (ROOT / path).is_file()
    ]
    assert not missing, (
        "inventory rows name a file that no longer exists. Either re-anchor the "
        "row to where the refusal moved, or mark the surface retired in the notes "
        "column (see .endless/../docs — E-2155's vocabulary: 'RETIRED: ... No "
        "conversion needed'):\n  " + "\n  ".join(missing)
    )


def test_every_anchored_symbol_is_declared_in_its_file():
    wrong = []
    for n, path, sym in _live_go_rows():
        p = ROOT / path
        if not p.is_file() or sym == PACKAGE_LEVEL:
            continue
        if sym not in _declared(p):
            wrong.append(f"line {n}: {path} declares no {sym!r}")
    assert not wrong, (
        "inventory rows name a symbol their file does not declare. A landing that "
        "renames or moves a function must bring its inventory row along:\n  "
        + "\n  ".join(wrong)
    )


def test_no_row_is_left_saying_find_it_yourself():
    """`RELOCATE:` (imperative) was E-2155's to-do marker, not a resolution.

    A row carrying it is a row someone still has to chase. E-2157 resolved the
    thirteen that existed; this keeps a new one from being parked instead of
    answered. A surface that has genuinely moved says RELOCATED and names where.
    """
    parked = [
        f"line {n}: {f[0]} ({f[1]})"
        for n, f in _rows()
        if re.search(r"\bRELOCATE:", f[9])
    ]
    assert not parked, (
        "rows are marked RELOCATE: (find it yourself) rather than resolved. "
        "Trace the message and either re-anchor it or mark it RETIRED:\n  "
        + "\n  ".join(parked)
    )

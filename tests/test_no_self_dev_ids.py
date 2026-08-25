"""No Endless-project task/session/decision id may reach a user.

Endless is run against someone else's project. Their task numbering has nothing
to do with the numbering of the ledger Endless itself is developed against, so
an `E-`/`ES-`/`ED-` id in help text, the guide or a runtime message is at best
noise and at worst a wrong cross-reference into a ledger the reader cannot open.

Source-code comments are exempt: they carry self-dev provenance and no user
ever sees them.

Documentation examples still need ids to look like ids, so ids **1-199** are
reserved as the example band. Write examples with those (`E-101`, `ES-102`,
`ED-103`, or the `E-NNNN` placeholder) and this guard stays quiet; cite a real
id and it fails. If a sentence needs a real id to make sense, it is describing
Endless's own development rather than the tool's behaviour, and belongs in the
ledger instead.
"""

import ast
import re
from pathlib import Path

import click
import pytest

from endless.cli import main

#: Ids at or below this are the reserved documentation-example band.
EXAMPLE_ID_MAX = 199

_ID = re.compile(r"\b(?:E|ES|ED)-(\d+)")

#: Appended to every failure. The moment this guard fires is the only moment a
#: session is guaranteed to be thinking about the rule, so the rule is stated
#: here rather than left in a docstring nobody opens.
_RULE = f"""

  Ids 1-{EXAMPLE_ID_MAX} are the reserved DOCUMENTATION-EXAMPLE band. An id above
  that reads as a citation into the ledger Endless itself is developed against
  — which a user running Endless on their own project cannot open.

  Fix one of three ways:
    - Trailing citation ("Set the order (E-1683).") — delete the parenthetical.
    - The id standing in for a fact ("Per ED-1540, unsettled means ...",
      "enforced since E-1950") — state the fact plainly instead. If the
      sentence needs the id to make sense, it is describing Endless's own
      development, not the tool's behaviour, and belongs in the ledger.
    - A hypothetical EXAMPLE — renumber it into the band (E-101, ES-102,
      ED-103) or use the E-NNNN placeholder.

  Source-code comments are exempt: they carry self-dev provenance and no user
  sees them. This guard reads help text, guide pages and runtime strings only.
"""

_REPO_ROOT = Path(__file__).resolve().parent.parent
_GUIDE_DIR = _REPO_ROOT / "docs" / "guide"
_SRC_DIR = _REPO_ROOT / "src" / "endless"


def _offenders(text):
    """Every id in `text` that is outside the reserved example band."""
    return [m.group(0) for m in _ID.finditer(text)
            if int(m.group(1)) > EXAMPLE_ID_MAX]


def _report(where, text):
    """Render offending ids with the line each sits on."""
    lines = []
    for line in text.splitlines():
        for bad in _offenders(line):
            lines.append(f"  {where}: {bad} :: {line.strip()}")
    return lines


def _walk(cmd, path, parent_ctx=None):
    """Every (invocation, --help output) pair in the Click tree, hidden included."""
    ctx = click.Context(cmd, info_name=path[-1], parent=parent_ctx)
    yield " ".join(path), cmd.get_help(ctx)
    if isinstance(cmd, click.Group):
        for name in sorted(cmd.list_commands(ctx)):
            sub = cmd.get_command(ctx, name)
            if sub is not None:
                yield from _walk(sub, path + [name], ctx)


def _non_docstring_constants(tree):
    """String literals that are not a module/class/function docstring.

    A Click command's docstring IS its help text, so it is covered by the help
    walk above. Every other docstring documents implementation for developers
    and is exempt for the same reason comments are.
    """
    docstrings = set()
    for node in ast.walk(tree):
        if isinstance(node, (ast.Module, ast.ClassDef,
                             ast.FunctionDef, ast.AsyncFunctionDef)):
            body = getattr(node, "body", None)
            if (body and isinstance(body[0], ast.Expr)
                    and isinstance(body[0].value, ast.Constant)
                    and isinstance(body[0].value.value, str)):
                docstrings.add(id(body[0].value))
    for node in ast.walk(tree):
        if (isinstance(node, ast.Constant) and isinstance(node.value, str)
                and id(node) not in docstrings):
            yield node


def test_help_tree_carries_no_self_dev_ids():
    """Every `--help` in the tree, including hidden commands."""
    bad = []
    walked = 0
    for invocation, help_text in _walk(main, ["endless"]):
        walked += 1
        bad += _report(invocation, help_text)
    assert walked > 100, f"the walk only reached {walked} commands — did it stop early?"
    assert not bad, "task ids in --help output:\n" + "\n".join(bad) + _RULE


def test_guide_carries_no_self_dev_ids():
    """`endless guide` renders these files verbatim."""
    pages = sorted(_GUIDE_DIR.rglob("*.md"))
    assert pages, f"no guide pages found under {_GUIDE_DIR}"
    bad = []
    for page in pages:
        bad += _report(page.relative_to(_REPO_ROOT), page.read_text())
    assert not bad, "task ids in the guide:\n" + "\n".join(bad) + _RULE


def test_runtime_strings_carry_no_self_dev_ids():
    """Messages the CLI prints or raises — `click.echo`, exceptions, banners."""
    bad = []
    for path in sorted(_SRC_DIR.rglob("*.py")):
        tree = ast.parse(path.read_text())
        rel = path.relative_to(_REPO_ROOT)
        for node in _non_docstring_constants(tree):
            for offender in _offenders(node.value):
                bad.append(f"  {rel}:{node.lineno}: {offender} :: "
                           f"{node.value.strip()[:100]}")
    assert not bad, "task ids in runtime strings:\n" + "\n".join(bad) + _RULE


@pytest.mark.parametrize("text,expected", [
    ("Set the order (E-1683).", ["E-1683"]),
    ("endless task list --parent E-101", []),
    ("Sessions render as ES-NNNN", []),
    ("write-once (ED-1560)", ["ED-1560"]),
    ("`endless session goto ES-101`", []),
    ("see ES-963 and ED-42", ["ES-963"]),
])
def test_offenders_bands_correctly(text, expected):
    """The band is what separates a citation from an example."""
    assert _offenders(text) == expected

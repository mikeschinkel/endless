"""Every Go subcommand must be reachable from the Python CLI (E-1950).

`endless errors raise` shipped in the Go binary, was written up in the guide and
in docs/errors.md, and had verify-suite coverage — all of which exercised
`endless-go errors raise` directly. Nobody wired the Python side, so the command
users actually type did not exist, and every check passed anyway.

The Python CLI is the surface a human types; the Go binary is an implementation
detail behind it. A verb reachable only through the binary is unshipped, however
thoroughly it is documented and tested.

These tests read the Go dispatch switch from source rather than shelling out to
a built binary, so they hold on a clean checkout with no bin/ present.
"""

import re
from pathlib import Path

import pytest

from endless.cli import main

ROOT = Path(__file__).resolve().parents[1]

# Aliases and help spellings are dispatch conveniences, not commands a user is
# owed a Python equivalent of.
NOT_COMMANDS = {"-h", "--help", "help"}

# Go source file -> Python command group name.
SURFACES = [
    ("internal/errorscmd/errors.go", "errors"),
    ("internal/jobscmd/jobs.go", "jobs"),
]


def _go_subcommands(rel_path: str) -> set[str]:
    """Canonical verbs from the leading dispatch switch in a Go Run function.

    Stops at the first `}` that closes the switch, so unrelated later switches
    in the same file (e.g. errors.go's severity switch) are not scooped up.

    Only the FIRST label of a multi-label case counts. `case "show", "list":`
    declares one verb with a shorthand, and Python is not owed an equivalent of
    every Go-side alias — only of every verb.
    """
    src = (ROOT / rel_path).read_text()
    start = src.index("func Run(")
    body = src[start:]
    switch = body.index("switch ")
    end = body.index("\n\t}", switch)
    names: set[str] = set()
    for line in body[switch:end].splitlines():
        stripped = line.strip()
        if not stripped.startswith("case "):
            continue
        labels = re.findall(r'"([^"]+)"', stripped)
        if labels:
            names.add(labels[0])
    return names - NOT_COMMANDS


@pytest.mark.parametrize("rel_path,group_name", SURFACES)
def test_every_go_subcommand_is_exposed_in_python(rel_path, group_name):
    go_names = _go_subcommands(rel_path)
    assert go_names, f"parsed no subcommands out of {rel_path} — the parser drifted"

    group = main.commands[group_name]
    py_names = set(group.commands)

    missing = sorted(go_names - py_names)
    assert not missing, (
        f"`endless-go {group_name}` dispatches {missing} but "
        f"`endless {group_name}` does not expose them — "
        f"a verb only the binary can reach is unshipped"
    )


def test_errors_raise_is_reachable_from_the_python_cli():
    """The specific regression: raise existed in Go only."""
    assert "raise" in main.commands["errors"].commands


@pytest.mark.parametrize("rel_path,group_name", SURFACES)
def test_parser_sees_the_known_verbs(rel_path, group_name):
    """Guard the guard: a parser that silently matches nothing would make the
    parity test vacuously green."""
    expected = {
        "errors": {"show", "clear", "codes", "raise"},
        "jobs": {"list", "run", "retry"},
    }[group_name]
    assert expected <= _go_subcommands(rel_path)

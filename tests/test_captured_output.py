"""Output another program takes in never carries the provenance line (E-1668).

E-1668 made every command that reads a database say which one answered, as a
trailing `# db: main` line. It kept that line out of `--json` and `--tsv`, and
missed the commands that print a payload with no flag at all: a value meant for
`$(...)`, or shell code meant for `eval`. Appended to those, the line became part
of the value.

  - shell-init captures `session cd --target worktree` into `$wt` and tests
    `[ -d "$wt" ]`. A two-line path fails that test, and `_endless_run` falls
    back to the GLOBAL endless instead of the worktree's — silently.
  - `cd "$(endless worktree for-task <id>)"`, which the guide teaches, fails.
  - `esu` evals `session use`.

The fix is declared per command (`stdout_is_captured`), because nothing at run
time can tell output captured to be read from output captured to be run. A
declaration can be forgotten, so the first test here does not trust a list: it
finds every command the docs and shell-init actually capture, resolves each
against the real command tree, and fails on any that is not marked.
"""

import re
from pathlib import Path

import click
import pytest
from click.testing import CliRunner

from endless import cli, provenance

REPO = Path(__file__).resolve().parent.parent

# `$(endless …)` or `$(_endless_run …)` — shell-init's own wrapper. The lookahead
# keeps `$(endless-go …)` out: that is a different binary with its own contract.
_CAPTURE = re.compile(r"\$\(\s*(?:endless|_endless_run)(?=[\s)])([^)]*)\)")

# The flags that already make output machine-readable. A capture that passes one
# is covered by that flag, not by this declaration.
_MACHINE_FLAGS = {"--json", "--tsv"}


def _sources() -> list[tuple[str, str]]:
    """(where, text) for everything that tells a shell to capture endless."""
    out = [("endless shell-init", cli._SHELL_INIT_SNIPPET)]
    candidates = [REPO / "README.md", REPO / "AGENTS.md"]
    candidates += sorted((REPO / "docs").rglob("*.md"))
    candidates += sorted((REPO / "src" / "endless").rglob("*.py"))
    for path in candidates:
        if path.is_file():
            out.append((str(path.relative_to(REPO)), path.read_text()))
    return out


def _resolve(tokens: list[str]):
    """Walk the real Click tree along `tokens`; return (command, path) or None.

    Resolving against the tree, rather than assuming how many words a command
    has, is the point. A regex that expected two words after `endless` is how
    `$(endless shell-init)` — the most-captured command in the product, run by
    every user's shell rc — was missed the first time.
    """
    cur, path, i = cli.main, [], 0
    while i < len(tokens):
        tok = tokens[i]
        if tok == "--db":
            i += 2
            continue
        if tok.startswith("-"):
            i += 1
            continue
        commands = getattr(cur, "commands", None)
        if commands is None or tok not in commands:
            break
        cur = commands[tok]
        path.append(tok)
        i += 1
    if not path or isinstance(cur, click.Group):
        return None
    return cur, " ".join(path)


def _captured_commands() -> dict[str, tuple[click.Command, set[str]]]:
    found: dict[str, tuple[click.Command, set[str]]] = {}
    for where, text in _sources():
        for m in _CAPTURE.finditer(text):
            tokens = m.group(1).split()
            if _MACHINE_FLAGS & set(tokens):
                continue
            hit = _resolve(tokens)
            if hit is None:
                continue
            command, name = hit
            found.setdefault(name, (command, set()))[1].add(where)
    return found


def test_the_scan_finds_the_captures_we_know_exist():
    """Proves the scanner before trusting what it did NOT find. A pattern that
    quietly matches nothing would make the next test pass vacuously."""
    found = _captured_commands()
    for known in ("shell-init", "session use", "session cd", "worktree for-task"):
        assert known in found, f"the scan no longer finds {known!r}: {sorted(found)}"


def test_every_captured_command_is_marked():
    unmarked = {
        name: sorted(where)
        for name, (command, where) in _captured_commands().items()
        if not getattr(command, "stdout_is_captured", False)
    }
    assert not unmarked, (
        "These commands are captured by $(...) or eval, so the provenance line "
        "would become part of the value. Mark each with "
        "`<function>.stdout_is_captured = True` in cli.py:\n"
        + "\n".join(f"  endless {n}  (captured in {', '.join(w)})"
                    for n, w in sorted(unmarked.items()))
    )


# --- the mechanism -----------------------------------------------------------


# The line is spelled `db: …` for a person and `# db: …` for an agent. Every
# check here runs as both: E-1668's verify suite first matched only the agent's
# spelling, passed when an agent ran it, and failed when the owner did.
AUDIENCES = [
    pytest.param((False, "db: main"), id="person"),
    pytest.param((True, "# db: main"), id="agent"),
]


@pytest.fixture(params=AUDIENCES)
def announcing(monkeypatch, request):
    """A tiny CLI built from the real classes, with a database to name, rendered
    for one audience at a time. Yields (cli, the line that audience sees)."""
    agent, expected_line = request.param
    monkeypatch.setattr(provenance, "_describe_db", lambda: ("main", "/cfg"))
    monkeypatch.setattr(provenance, "_describe_project", lambda: None)
    monkeypatch.setattr(provenance, "_agent_mode", lambda: agent)

    @click.group(cls=cli.AgentAwareGroup)
    def root():
        pass

    @root.result_callback()
    def _tail(result, **kwargs):
        provenance.echo_tail()

    @root.command("value")
    def value():
        provenance.mark_touched()
        click.echo("/the/path")

    value.stdout_is_captured = True

    @root.command("report")
    def report():
        provenance.mark_touched()
        click.echo("a report")

    return root, expected_line


def test_a_captured_command_prints_only_its_value(announcing):
    root, _ = announcing
    result = CliRunner().invoke(root, ["value"])

    assert result.exit_code == 0, result.output
    assert result.output == "/the/path\n"


def test_an_ordinary_command_still_says_which_database_answered(announcing):
    """The fix must not switch the feature off: output that is READ keeps it,
    exactly once, in the spelling its audience gets."""
    root, expected_line = announcing
    result = CliRunner().invoke(root, ["report"])

    assert result.exit_code == 0, result.output
    lines = result.output.splitlines()
    assert "a report" in lines
    assert lines.count(expected_line) == 1, result.output

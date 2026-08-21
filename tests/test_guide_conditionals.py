"""The guide is conditional on the reading project's config (E-2030).

`reportChannelOn` gates the Stop hook and the SessionStart rule on
`.endless/config.json`'s `report_gate`, and its own comment states the rule it
enforces: a session must never be told to use a channel that will not gate it,
nor gated without having been told. The guide used to be `cat`ed to stdout, so
every word of it was unconditional — including the instruction to run
`endless task report`. On a gate-off project (Endless's own checkout, among
others) that instruction was the forbidden half of that invariant.

These tests pin the render path, the registry that makes an unknown condition
loud, and the fact that the two branches actually differ.
"""

import json
import re
import shutil
import subprocess
from pathlib import Path

import pytest

from endless import cli

GUIDE_DIR = Path(__file__).resolve().parent.parent / "docs" / "guide"
GUIDE_FILES = sorted(GUIDE_DIR.glob("*.md"))

# Every `{{...}}` action the guide is allowed to contain. Conditions are checked
# against the live registry; this covers the non-condition halves of a branch.
_ACTION_RE = re.compile(r"\{\{-?\s*(.*?)\s*-?\}\}")
_CONDITION_RE = re.compile(r"^(?:if|else if)\s+\.(\w+)$")


def _go_bin() -> str:
    """The endless-go the worktree built, or the one on PATH."""
    local = Path(__file__).resolve().parent.parent / "bin" / "endless-go"
    if local.is_file():
        return str(local)
    found = shutil.which("endless-go")
    if not found:
        pytest.skip("endless-go not built and not on PATH")
    return found


def _render(path: Path, **conditions: bool) -> str:
    result = subprocess.run(
        [_go_bin(), "template", "render", "--file", str(path)],
        input=json.dumps(conditions),
        capture_output=True, text=True, check=False,
    )
    assert result.returncode == 0, f"{path.name}: {result.stderr}"
    return result.stdout


def test_guide_files_exist():
    """A glob that silently matched nothing would make every test below vacuous."""
    assert GUIDE_FILES, f"no guide files found under {GUIDE_DIR}"


@pytest.mark.parametrize("path", GUIDE_FILES, ids=lambda p: p.name)
def test_every_condition_is_in_the_registry(path):
    """A misspelled condition renders the else-arm rather than failing.

    That is the failure mode worth a test: `{{if .report_gat}}` parses, executes,
    and quietly hands every reader the gate-off guide. Nothing downstream can
    tell that apart from a project that genuinely turned the channel off.
    """
    known = set(cli.guide_conditions())
    for action in _ACTION_RE.findall(path.read_text()):
        m = _CONDITION_RE.match(action)
        if not m:
            assert action in ("else", "end"), \
                f"{path.name}: unsupported guide action {{{{{action}}}}}"
            continue
        assert m.group(1) in known, (
            f"{path.name}: branches on {m.group(1)!r}, which "
            f"cli.guide_conditions() does not answer (known: {sorted(known)})"
        )


@pytest.mark.parametrize("path", GUIDE_FILES, ids=lambda p: p.name)
@pytest.mark.parametrize("gate", [True, False], ids=["gate-on", "gate-off"])
def test_no_markers_survive_rendering(path, gate):
    """Whatever the conditions, the reader gets markdown, not template source."""
    assert "{{" not in _render(path, report_gate=gate)


def test_gate_off_never_instructs_the_reader_to_run_task_report():
    """The defect E-2030 exists to close, stated as an assertion.

    Scoped to the INVOCATION, not the name. The gate-off guide still says
    `endless task report` — to say there is no such step here, which a reader
    cannot act on without the command's name. What must not survive is anything
    runnable: a `--draft-file` invocation, or an imperative pointing at one.
    """
    for path in GUIDE_FILES:
        rendered = _render(path, report_gate=False)
        assert "--draft-file" not in rendered, \
            f"{path.name}: gate-off guide still hands the reader an invocation"
        for phrase in ("run `endless task report", "then:\n\n```bash\nendless task report"):
            assert phrase not in rendered, \
                f"{path.name}: gate-off guide still tells the reader to run the command"


def test_the_two_branches_actually_differ():
    """Guards against a gate that resolves but changes nothing."""
    changed = [
        p.name for p in GUIDE_FILES
        if _render(p, report_gate=True) != _render(p, report_gate=False)
    ]
    assert "index.md" in changed
    assert "tasks.md" in changed
    assert "orchestration.md" in changed


@pytest.mark.parametrize("gate", [True, False], ids=["gate-on", "gate-off"])
def test_topic_table_stays_contiguous(gate):
    """A blank line inside a markdown table ends the table early.

    The two minimizer topic rows are conditional, and the obvious spelling —
    `{{if .x}}row{{end}}` on its own line — leaves the row's newline outside both
    actions, so a false condition renders a blank line mid-table.
    """
    lines = _render(GUIDE_DIR / "index.md", report_gate=gate).splitlines()
    start = lines.index("| Topic | Section | Covers |")
    body = lines[start + 2:]
    end = next(i for i, line in enumerate(body) if not line.startswith("|"))
    assert all(line.startswith("| ") for line in body[:end])
    assert body[end].startswith("<!-- END generated")


def test_retired_pre_summarize_criteria_stay_gone():
    """E-2030 retired "do not pre-summarize" from every surface it lived on.

    It told the agent to hand over bloat it had already recognized as bloat, on
    the reasoning that the minimizer should be the one to cut it. Which agent
    does the cutting does not matter; the outcome the user receives does. The
    criteria were never requested — they were added by a session writing its own
    standard into the guide — so a test keeps them from being reasoned back in.
    """
    root = Path(__file__).resolve().parent.parent
    surfaces = [
        *GUIDE_FILES,
        root / "internal" / "hookcmd" / "claude.go",
        root / "internal" / "templatecmd" / "templates" / "handoff" / "_close.tmpl",
        root / "internal" / "templatecmd" / "templates" / "handoff" / "respawn.md.tmpl",
        root / "src" / "endless" / "cli.py",
        root / "src" / "endless" / "task_cmd.py",
        root / "src" / "endless" / "report_cmd.py",
    ]
    for path in surfaces:
        text = path.read_text()
        for phrase in ("Do NOT pre-summarize", "Do not pre-summarize",
                       "no pre-summarizing"):
            assert phrase not in text, f"{path.name} grew back {phrase!r}"

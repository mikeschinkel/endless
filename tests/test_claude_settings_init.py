"""Tests for the claude-settings-init recipe's embedded settings builder.

The `claude-settings-init` Justfile recipe writes a worktree-local
`.claude/settings.local.json`. The assembly logic lives in an inline
`python3 - ... <<'PY' ... PY` heredoc inside the recipe, and these tests extract
that embedded Python verbatim from the Justfile and run it with synthetic argv
rather than provisioning a real git worktree, a fake $HOME and the `just`
binary. That pins the tests to the actual source: if the embedded script
changes, the test runs the new code.

Its inputs shrank twice. E-1457 moved the output from the tracked
`.claude/settings.json` to the git-ignored `.claude/settings.local.json`, which
dropped the committed-file and working-copy arguments — Claude Code merges the
committed file natively, so copying it in was never needed. E-2166 dropped the
LAST of the three original inputs, the user's own settings, because the recipe
no longer reproduces their hooks. Two arguments remain: where to write, and
whatever the local file already held.

What the script must get right, and what each test here pins:

  - `worktree.bgIsolation: "none"` is always present (E-1569), and always
    replaced rather than merged.
  - No `hooks` key is ever produced, and a `hooks` key already in the file is
    REMOVED (E-2166). That removal is the entire mechanism behind
    `just claude-settings-sweep`: the sweep is this recipe re-run across every
    worktree, so this behaviour is what un-pins the existing population.
  - Hand-written keys survive, so regenerating is not destructive.
  - Re-running is byte-identical, so nothing churns in a file git ignores but
    humans still read.
"""

import json
import subprocess
import sys
import textwrap
from pathlib import Path

import pytest

JUSTFILE = Path(__file__).resolve().parent.parent / "Justfile"


def _extract_embedded_script() -> str:
    """Pull the `python3 - ... <<'PY' ... PY` body out of claude-settings-init.

    Returns the dedented Python source (the recipe indents it 4 spaces).
    """
    lines = JUSTFILE.read_text().splitlines()
    start = end = None
    for i, line in enumerate(lines):
        if "<<'PY'" in line:
            start = i + 1
        elif start is not None and line.strip() == "PY":
            end = i
            break
    assert start is not None and end is not None, "PY heredoc not found in Justfile"
    body = "\n".join(lines[start:end])
    return textwrap.dedent(body)


def _run(script: str, *, out_path, local):
    """Run the extracted script with the same argv the recipe passes.

    The recipe's two arguments are the output path and the raw text of whatever
    `.claude/settings.local.json` already held ('{}' when it did not exist).
    """
    argv = [
        sys.executable, "-",
        str(out_path),
        local if isinstance(local, str) else json.dumps(local),
    ]
    proc = subprocess.run(argv, input=script, text=True, capture_output=True)
    assert proc.returncode == 0, f"script failed: {proc.stderr}"
    return proc


@pytest.fixture
def script():
    return _extract_embedded_script()


def _pinned_hooks(worktree_root):
    """A hooks block shaped exactly like the one this recipe used to write."""
    cmd = f"{worktree_root}/bin/endless-go hook claude"
    return {
        event: [{"hooks": [{"command": cmd, "type": "command", "async": False}]}]
        for event in ("PreToolUse", "PostToolUse", "SessionStart")
    }


def test_bg_isolation_written_fresh(script, tmp_path):
    """A fresh run (no prior local file) writes bgIsolation: none."""
    out_path = tmp_path / "settings.local.json"
    _run(script, out_path=out_path, local={})
    out = json.loads(out_path.read_text())
    assert out["worktree"] == {"bgIsolation": "none"}


def test_bg_isolation_overwrites_existing_worktree_key(script, tmp_path):
    """A stale worktree key is replaced, not merged."""
    out_path = tmp_path / "settings.local.json"
    _run(script, out_path=out_path,
         local={"worktree": {"bgIsolation": "tree", "other": 1}})
    out = json.loads(out_path.read_text())
    assert out["worktree"] == {"bgIsolation": "none"}


def test_no_hooks_key_is_ever_written(script, tmp_path):
    """E-2166: the recipe does not reproduce the user's hooks.

    Hooks reach a worktree from the user scope, which Claude Code applies in
    every directory. A copy here would be a duplicate whose only distinguishing
    feature was a substituted binary path — the pin ED-1596 removed.
    """
    out_path = tmp_path / "settings.local.json"
    _run(script, out_path=out_path, local={})
    assert "hooks" not in json.loads(out_path.read_text())


def test_an_existing_pin_is_removed(script, tmp_path):
    """E-2166: a hooks block a previous run left behind is stripped.

    This is what `just claude-settings-sweep` relies on. The sweep has no logic
    of its own — it re-runs this recipe over every worktree — so if the script
    merely stopped ADDING hooks while leaving an existing block alone, the 96
    already-pinned worktrees would stay pinned to a possibly-stale binary.
    """
    out_path = tmp_path / "settings.local.json"
    _run(script, out_path=out_path, local={"hooks": _pinned_hooks(tmp_path)})
    out = json.loads(out_path.read_text())
    assert "hooks" not in out
    assert out["worktree"] == {"bgIsolation": "none"}


def test_removal_is_announced(script, tmp_path):
    """Stripping a pin says so; a worktree that had none stays quiet.

    The sweep reads this line to count what it changed, so the two cases have
    to be distinguishable on stdout.
    """
    out_path = tmp_path / "settings.local.json"
    pinned = _run(script, out_path=out_path,
                  local={"hooks": _pinned_hooks(tmp_path)})
    assert "removed the stale worktree pin" in pinned.stdout

    clean = _run(script, out_path=out_path, local={})
    assert "removed the stale worktree pin" not in clean.stdout


def test_hand_written_keys_survive(script, tmp_path):
    """Regenerating preserves anything a human put in the file."""
    out_path = tmp_path / "settings.local.json"
    _run(script, out_path=out_path, local={
        "permissions": {"allow": ["Bash(just test)"]},
        "env": {"SOME_VAR": "1"},
    })
    out = json.loads(out_path.read_text())
    assert out["permissions"] == {"allow": ["Bash(just test)"]}
    assert out["env"] == {"SOME_VAR": "1"}
    assert out["worktree"] == {"bgIsolation": "none"}


def test_idempotent(script, tmp_path):
    """Re-running produces byte-identical output (no churn)."""
    out_path = tmp_path / "settings.local.json"
    local = {"permissions": {"allow": ["Bash(just test)"]}}
    _run(script, out_path=out_path, local=local)
    first = out_path.read_text()
    # Second pass feeds the freshly-written file back in, mirroring how the
    # recipe reads settings.local.json on a re-run.
    _run(script, out_path=out_path, local=json.loads(first))
    second = out_path.read_text()
    assert first == second
    assert json.loads(second)["worktree"] == {"bgIsolation": "none"}


def test_a_pin_does_not_survive_a_second_pass(script, tmp_path):
    """The removal is not a one-shot: re-running never resurrects the block."""
    out_path = tmp_path / "settings.local.json"
    _run(script, out_path=out_path, local={"hooks": _pinned_hooks(tmp_path)})
    first = out_path.read_text()
    _run(script, out_path=out_path, local=json.loads(first))
    assert first == out_path.read_text()
    assert "hooks" not in json.loads(out_path.read_text())

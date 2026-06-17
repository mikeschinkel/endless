"""Tests for the claude-settings-init recipe's embedded settings builder.

The `claude-settings-init` Justfile recipe assembles a worktree-local
.claude/settings.json from three inputs: the user's ~/.claude/settings.json
(hook source), the committed .claude/settings.json (enabledPlugins etc.), and
the working-tree copy (sandbox-bind env block). The assembly logic lives in an
inline `python3 - ... <<'PY' ... PY` heredoc inside the recipe.

Rather than provision a real git worktree + fake $HOME + the `just` binary to
exercise the whole recipe, these tests extract that embedded Python verbatim
from the Justfile and run it with synthetic argv. That pins the tests to the
actual source: if the embedded script changes, the test runs the new code.

E-1569 added `out["worktree"] = {"bgIsolation": "none"}` to the script.
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


def _run(script: str, *, user, worktree_root, out_path, committed, working):
    """Run the extracted script with the same argv the recipe passes."""
    argv = [
        sys.executable, "-",
        json.dumps(user) if isinstance(user, dict) else user,
        str(worktree_root),
        str(out_path),
        json.dumps(committed),
        json.dumps(working),
    ]
    # The script reads user settings from a file path (argv[1]); the other two
    # JSON blobs are passed as raw strings. Write the user file out.
    user_path = Path(worktree_root) / "user_settings.json"
    user_path.write_text(json.dumps(user))
    argv[2] = str(user_path)
    proc = subprocess.run(
        argv, input=script, text=True, capture_output=True,
    )
    assert proc.returncode == 0, f"script failed: {proc.stderr}"
    return proc


@pytest.fixture
def script():
    return _extract_embedded_script()


def test_bg_isolation_written_fresh(script, tmp_path):
    """A fresh run (no prior worktree key) writes bgIsolation: none."""
    out_path = tmp_path / "settings.json"
    user = {
        "hooks": {
            "PostToolUse": [
                {"hooks": [{"command": "endless-go hook post", "type": "command"}]}
            ]
        }
    }
    _run(
        script, user=user, worktree_root=tmp_path, out_path=out_path,
        committed={"enabledPlugins": ["x"]}, working={},
    )
    out = json.loads(out_path.read_text())
    assert out["worktree"] == {"bgIsolation": "none"}
    # Other keys are still assembled as before.
    assert out["enabledPlugins"] == ["x"]
    assert "PostToolUse" in out["hooks"]


def test_bg_isolation_overwrites_existing_worktree_key(script, tmp_path):
    """A stale worktree key from working/committed is replaced, not merged."""
    out_path = tmp_path / "settings.json"
    _run(
        script, user={"hooks": {}}, worktree_root=tmp_path, out_path=out_path,
        committed={}, working={"worktree": {"bgIsolation": "tree", "other": 1}},
    )
    out = json.loads(out_path.read_text())
    assert out["worktree"] == {"bgIsolation": "none"}


def test_bg_isolation_idempotent(script, tmp_path):
    """Re-running produces byte-identical output (no churn)."""
    out_path = tmp_path / "settings.json"
    user = {
        "hooks": {
            "PreToolUse": [
                {"hooks": [{"command": "endless-go hook pre", "type": "command"}]}
            ]
        }
    }
    committed = {"enabledPlugins": ["a", "b"]}
    working = {"env": {"XDG_CONFIG_HOME": "/tmp/sb"}}
    _run(script, user=user, worktree_root=tmp_path, out_path=out_path,
         committed=committed, working=working)
    first = out_path.read_text()
    # Second pass feeds the freshly-written file back as the working copy,
    # mirroring how the recipe reads .claude/settings.json on a re-run.
    working2 = json.loads(first)
    _run(script, user=user, worktree_root=tmp_path, out_path=out_path,
         committed=committed, working=working2)
    second = out_path.read_text()
    assert first == second
    assert json.loads(second)["worktree"] == {"bgIsolation": "none"}

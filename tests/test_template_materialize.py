"""End-to-end materialize-on-demand tests for `endless-go template render`.

Drives the Go binary via subprocess against a temp project root so the
materialize → render → persist → restore loop is exercised at the same
process boundary the spawn flow uses (E-1565).
"""

import json
import subprocess
from pathlib import Path

import pytest


@pytest.fixture
def project_root(tmp_path):
    """A temp dir that looks like an endless project root."""
    (tmp_path / ".endless").mkdir()
    return tmp_path


def _bin() -> str:
    return str(Path(__file__).resolve().parent.parent / "bin" / "endless-go")


def _run_render(cwd: Path, name: str = "handoff.md", vars_payload: dict | None = None) -> subprocess.CompletedProcess:
    payload = vars_payload if vars_payload is not None else {
        "spawned_id": 4242,
        "title": "Materialize test",
        "spawner_task": 1565,
        "return_anchor": "%9",
        "worktree_path": "/tmp/wt/e-4242",
        "branch": "task/4242-materialize",
    }
    return subprocess.run(
        [_bin(), "template", "render", name],
        cwd=cwd,
        input=json.dumps(payload),
        capture_output=True, text=True, check=False,
    )


def test_first_render_materializes_template_file(project_root):
    dst = project_root / ".endless" / "templates" / "handoff.md.tmpl"
    assert not dst.exists()

    result = _run_render(project_root)
    assert result.returncode == 0, result.stderr
    assert dst.exists(), "materialized template file did not appear"
    assert dst.read_text().startswith("You're a worktree-bound")


def test_gitignore_untouched_by_render(project_root):
    gi = project_root / ".gitignore"
    gi.write_text("# preserved\n")

    result = _run_render(project_root)
    assert result.returncode == 0, result.stderr
    assert gi.read_text() == "# preserved\n"


def test_user_edits_persist_across_renders(project_root):
    tmpl_dir = project_root / ".endless" / "templates"
    tmpl_dir.mkdir(parents=True)
    custom = "USER OVERRIDE {{.spawned_id}}\n"
    (tmpl_dir / "handoff.md.tmpl").write_text(custom)

    result = _run_render(project_root)
    assert result.returncode == 0, result.stderr
    assert "USER OVERRIDE 4242" in result.stdout
    # File untouched.
    assert (tmpl_dir / "handoff.md.tmpl").read_text() == custom


def test_delete_to_restore(project_root):
    # First render to materialize.
    r1 = _run_render(project_root)
    assert r1.returncode == 0, r1.stderr
    dst = project_root / ".endless" / "templates" / "handoff.md.tmpl"
    embedded = dst.read_text()

    # Modify, delete, re-render → embedded restored.
    dst.write_text("MODIFIED\n")
    dst.unlink()
    r2 = _run_render(project_root)
    assert r2.returncode == 0, r2.stderr
    assert dst.exists()
    assert dst.read_text() == embedded


def test_local_tmpl_overrides_committed(project_root):
    tmpl_dir = project_root / ".endless" / "templates"
    tmpl_dir.mkdir(parents=True)
    (tmpl_dir / "handoff.md.tmpl").write_text("COMMITTED\n")
    (tmpl_dir / "handoff.md.local.tmpl").write_text("LOCAL_WINS\n")

    result = _run_render(project_root)
    assert result.returncode == 0, result.stderr
    assert "LOCAL_WINS" in result.stdout
    assert "COMMITTED" not in result.stdout

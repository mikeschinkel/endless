"""Tests for endless.project_path — the one project-path normalization rule.

The Go half is pinned in internal/monitor/projects_test.go; the two agreeing
end to end is what tests/tasks/e-2002-verify.sh drives through the real hook.
"""

from pathlib import Path

from endless import db
from endless.project_path import (
    match_project_path,
    normalize,
    project_name_for_cwd,
)
from endless.register import register_project


def _seed(path, name="acme", pid=None):
    cols = "name, path" if pid is None else "id, name, path"
    args = (name, str(path)) if pid is None else (pid, name, str(path))
    ph = "?, ?" if pid is None else "?, ?, ?"
    db.execute(f"INSERT INTO projects ({cols}) VALUES ({ph})", args)


# ─── normalize ──────────────────────────────────────────────────────────────

def test_normalize_resolves_symlink_components(tmp_path):
    real = (tmp_path / "real").resolve()
    real.mkdir()
    link = tmp_path / "link"
    link.symlink_to(real)

    assert normalize(link) == real
    assert normalize(link / "src") == real / "src"


def test_normalize_is_non_strict(tmp_path):
    """A directory that does not exist yet still gets its existing prefix
    resolved — the property the Go half reproduces by hand, because
    filepath.EvalSymlinks fails outright on a missing leaf."""
    real = (tmp_path / "real").resolve()
    real.mkdir()
    link = tmp_path / "link"
    link.symlink_to(real)

    assert normalize(link / "not" / "created" / "yet") == \
        real / "not" / "created" / "yet"


def test_normalize_expands_tilde():
    """Input tolerance, not part of the canonical stored form — nothing writes
    a tilde into projects.path. Pinned because bare `Path.resolve()` treats a
    literal `~` as an ordinary directory name and returns `<cwd>/~/...`."""
    assert normalize("~/some-project") == \
        Path.home().resolve() / "some-project"
    assert normalize("~/some-project") != Path("~/some-project").resolve()


def test_normalize_is_idempotent(tmp_path):
    once = normalize(tmp_path)
    assert normalize(once) == once


# ─── match_project_path ─────────────────────────────────────────────────────

def test_match_finds_row_through_a_symlinked_path(isolated_env, tmp_path):
    real = (tmp_path / "acme").resolve()
    real.mkdir()
    _seed(real)
    link = tmp_path / "link-to-acme"
    link.symlink_to(real)

    assert match_project_path(link) == str(real)


def test_match_tolerates_a_row_stored_unresolved(isolated_env, tmp_path):
    """The legacy-ledger case, and the reason E-2002 needs no data migration:
    normalization at the comparison boundary makes the stored spelling
    irrelevant."""
    real = (tmp_path / "acme").resolve()
    real.mkdir()
    link = tmp_path / "link-to-acme"
    link.symlink_to(real)
    _seed(link)  # written before the rule existed

    assert match_project_path(real) == str(link)


def test_match_prefers_the_older_of_two_rows_for_one_directory(
    isolated_env, tmp_path,
):
    """A ledger that already caught the bug holds the real registration AND the
    auto-registered duplicate. The lower id — the row with the tasks — wins."""
    real = (tmp_path / "acme").resolve()
    real.mkdir()
    link = tmp_path / "link-to-acme"
    link.symlink_to(real)
    _seed(real, name="acme", pid=1)
    _seed(link, name="acme-2", pid=2)

    assert match_project_path(link) == str(real)


def test_match_returns_none_for_an_unregistered_directory(
    isolated_env, tmp_path,
):
    assert match_project_path(tmp_path / "nowhere") is None


# ─── project_name_for_cwd ───────────────────────────────────────────────────

def test_name_for_cwd_reads_the_on_disk_config_first(isolated_env, tmp_path):
    proj = tmp_path / "proj"
    (proj / ".endless").mkdir(parents=True)
    (proj / ".endless" / "config.json").write_text('{"name": "from-config"}')
    _seed(proj, name="from-db")

    assert project_name_for_cwd(proj) == "from-config"


def test_name_for_cwd_falls_back_to_the_registered_row(isolated_env, tmp_path):
    real = (tmp_path / "acme").resolve()
    real.mkdir()
    _seed(real, name="acme")
    link = tmp_path / "link-to-acme"
    link.symlink_to(real)

    assert project_name_for_cwd(link) == "acme"


def test_name_for_cwd_is_none_outside_any_project(isolated_env, tmp_path):
    assert project_name_for_cwd(tmp_path) is None


# ─── the write side ─────────────────────────────────────────────────────────

def test_register_stores_the_resolved_path(isolated_env, tmp_path):
    """Registering through a symlink must store the canonical path, so the Go
    hook — which normalizes the cwd Claude Code hands it — matches on the
    indexed fast path rather than the legacy scan."""
    real = (tmp_path / "acme").resolve()
    real.mkdir()
    link = tmp_path / "link-to-acme"
    link.symlink_to(real)

    register_project(link, name="acme", infer=True)

    rows = db.query("SELECT path FROM projects WHERE name = ?", ("acme",))
    assert len(rows) == 1
    assert rows[0]["path"] == str(real)

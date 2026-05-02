"""CLI smoke tests using click's CliRunner."""

from click.testing import CliRunner

from endless.cli import main


def test_help():
    runner = CliRunner()
    result = runner.invoke(main, ["--help"])
    assert result.exit_code == 0
    assert "Project awareness system" in result.output


def test_version():
    runner = CliRunner()
    result = runner.invoke(main, ["--version"])
    assert result.exit_code == 0
    assert "0.1.0" in result.output


def test_list_empty(isolated_env):
    runner = CliRunner()
    result = runner.invoke(main, ["list"])
    assert result.exit_code == 0
    assert "No projects registered" in result.output


def test_register_and_list(isolated_env):
    project_dir = isolated_env["projects_root"] / "cli-test"
    project_dir.mkdir()

    runner = CliRunner()

    # Register
    result = runner.invoke(
        main, ["register", str(project_dir), "--infer"]
    )
    assert result.exit_code == 0
    assert "Registered" in result.output or "Updated" in result.output

    # List
    result = runner.invoke(main, ["list"])
    assert result.exit_code == 0
    assert "cli-test" in result.output


def test_status_by_name(isolated_env):
    project_dir = isolated_env["projects_root"] / "status-test"
    project_dir.mkdir()

    runner = CliRunner()
    runner.invoke(
        main, ["register", str(project_dir), "--infer"]
    )

    result = runner.invoke(main, ["status", "status-test"])
    assert result.exit_code == 0
    assert "status-test" in result.output
    assert "Status:" in result.output


def test_set_field(isolated_env):
    project_dir = isolated_env["projects_root"] / "set-test"
    project_dir.mkdir()

    runner = CliRunner()
    runner.invoke(
        main, ["register", str(project_dir), "--infer"]
    )

    result = runner.invoke(
        main, ["set", "set-test.label=New Label"]
    )
    assert result.exit_code == 0
    assert "New Label" in result.output

    # Verify it stuck
    result = runner.invoke(main, ["status", "set-test"])
    assert "New Label" in result.output


def test_rename(isolated_env):
    project_dir = isolated_env["projects_root"] / "old-name"
    project_dir.mkdir()

    runner = CliRunner()
    runner.invoke(
        main, ["register", str(project_dir), "--infer"]
    )

    result = runner.invoke(
        main, ["rename", "old-name", "new-name"]
    )
    assert result.exit_code == 0
    assert "new-name" in result.output

    # Old name should be gone
    result = runner.invoke(main, ["status", "old-name"])
    assert result.exit_code != 0

    # New name should work
    result = runner.invoke(main, ["status", "new-name"])
    assert result.exit_code == 0


def test_unregister(isolated_env):
    project_dir = isolated_env["projects_root"] / "doomed"
    project_dir.mkdir()

    runner = CliRunner()
    runner.invoke(
        main, ["register", str(project_dir), "--infer"]
    )

    result = runner.invoke(main, ["unregister", "doomed"])
    assert result.exit_code == 0
    assert "Unregistered" in result.output

    # Should be gone from list
    result = runner.invoke(main, ["list"])
    assert "doomed" not in result.output

    # Config should still exist with status=unregistered
    import json
    cfg_path = project_dir / ".endless" / "config.json"
    assert cfg_path.exists()
    with open(cfg_path) as f:
        cfg = json.load(f)
    assert cfg["status"] == "unregistered"

    # Reconcile should NOT re-register it
    result = runner.invoke(main, ["list"])
    assert "doomed" not in result.output


def test_scan(isolated_env):
    project_dir = isolated_env["projects_root"] / "scan-test"
    project_dir.mkdir()
    (project_dir / "README.md").write_text("# Hello\n")

    runner = CliRunner()
    runner.invoke(
        main, ["register", str(project_dir), "--infer"]
    )

    result = runner.invoke(main, ["scan"])
    assert result.exit_code == 0
    assert "1 project(s)" in result.output


def test_task_add_rejects_invalid_phase_at_parse_time(isolated_env):
    """E-1121: --phase on task add must reject unknown values via click.Choice
    so bad input is caught at the CLI boundary, not deep in the events layer."""
    runner = CliRunner()
    result = runner.invoke(
        main, ["task", "add", "Add a thing", "--phase", "foo"]
    )
    assert result.exit_code != 0
    assert "'foo' is not one of" in result.output
    for valid in ("now", "next", "later", "maybe"):
        assert valid in result.output

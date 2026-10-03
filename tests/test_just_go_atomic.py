"""E-2205: `just go` replaces bin/endless-go atomically.

It builds to bin/endless-go.next and renames that over bin/endless-go, so a
reader polling the binary never sees it missing or half-written, a failed build
leaves the old binary in place, and no .next file is left behind either way.

Runs the project's real justfile against a throwaway Go module, so the recipe
under test is the one a developer and the land run, not a copy of it.
"""

import os
import shutil
import subprocess
from pathlib import Path

import pytest

JUSTFILE = Path(__file__).resolve().parents[1] / "justfile"

pytestmark = pytest.mark.skipif(
    shutil.which("just") is None or shutil.which("go") is None,
    reason="needs just and go on PATH",
)


def _module(root: Path, body: str) -> None:
    (root / "go.mod").write_text("module example.com/atomic\n\ngo 1.21\n")
    main = root / "cmd" / "endless-go"
    main.mkdir(parents=True, exist_ok=True)
    (main / "main.go").write_text(body)
    (root / "bin").mkdir(exist_ok=True)


def _just(root: Path, recipe: str) -> subprocess.CompletedProcess:
    return subprocess.run(
        ["just", "--justfile", str(JUSTFILE), "--working-directory", str(root),
         recipe],
        capture_output=True, text=True,
        # A module outside any workspace: the developer's go.work must not apply.
        env={**os.environ, "GOWORK": "off", "GOFLAGS": ""},
    )


def test_a_successful_build_replaces_the_binary_and_leaves_no_next(tmp_path):
    _module(tmp_path, 'package main\n\nfunc main() { println("new") }\n')
    (tmp_path / "bin" / "endless-go").write_text("old binary\n")

    result = _just(tmp_path, "go")

    assert result.returncode == 0, result.stderr
    out = subprocess.run([str(tmp_path / "bin" / "endless-go")],
                         capture_output=True, text=True)
    assert out.stderr.strip() == "new"
    assert not (tmp_path / "bin" / "endless-go.next").exists()


def test_a_failed_build_keeps_the_old_binary_and_leaves_no_next(tmp_path):
    _module(tmp_path, "package main\n\nfunc main() { this does not compile }\n")
    (tmp_path / "bin" / "endless-go").write_text("old binary\n")

    result = _just(tmp_path, "go")

    assert result.returncode != 0
    assert (tmp_path / "bin" / "endless-go").read_text() == "old binary\n"
    assert not (tmp_path / "bin" / "endless-go.next").exists()


def test_go_build_next_leaves_the_installed_binary_alone(tmp_path):
    _module(tmp_path, 'package main\n\nfunc main() { println("new") }\n')
    (tmp_path / "bin" / "endless-go").write_text("old binary\n")

    result = _just(tmp_path, "go-build-next")

    assert result.returncode == 0, result.stderr
    assert (tmp_path / "bin" / "endless-go").read_text() == "old binary\n"
    assert (tmp_path / "bin" / "endless-go.next").exists()

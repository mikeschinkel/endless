"""The settings registry, as `endless-go settings list --json` exports it (E-2284).

Go owns the registry: which config keys exist, which file each may live in,
what values each accepts. Python reads that here and never parses a registered
key's value itself.
"""

import json
import re
import shutil
import subprocess
from pathlib import Path

from endless import agent_help, config

# A Go duration as time.ParseDuration accepts it, e.g. "5m", "1h30m", "250ms".
_DURATION = re.compile(r"^(\d+(\.\d+)?(ns|us|µs|ms|s|m|h))+$")

# The path, name and command kinds' shapes, as internal/settings/shapes.go
# checks them: a path is absolute or under ~; a name is non-empty with no /,
# whitespace, : or .; a command contains {cmd}, or is empty.
_NAME = re.compile(r"^[^/:.\s]+$")


def _is_path(value: str) -> bool:
    return value == "~" or value.startswith("~/") or value.startswith("/")


def _candidates() -> list[str]:
    """The worktree's endless-go first — the coherent partner of the Python
    running here, and the only one that knows a key a branch adds — then the
    PATH-resolved global."""
    out = []
    worktree_bin = config.worktree_endless_go()
    if worktree_bin is not None and worktree_bin.is_file():
        out.append(str(worktree_bin))
    found = shutil.which("endless-go")
    if found is not None:
        out.append(found)
    return out


def export(project_path: Path) -> dict:
    """The whole export for the project at project_path: `machine_file`,
    `project_file`, and `settings`, each with its effective value there."""
    return _list(["--project", str(project_path)])


def metadata() -> dict:
    """The registry alone, every value its default. Reads no config file, so
    deciding whether a key is registered never depends on some file parsing."""
    return _list(["--metadata"])


def _list(args: list[str]) -> dict:
    last = None
    for binary in _candidates():
        result = subprocess.run(
            [binary, "settings", "list", "--json", *args],
            capture_output=True, text=True,
        )
        if result.returncode == 0:
            return json.loads(result.stdout)
        last = result
        # A worktree binary branched before `settings` existed cannot answer;
        # the global one ships with the Python asking. Anything else — a config
        # file the binary refused — is the answer, and is relayed.
        if "unknown subcommand" not in result.stderr:
            break
    if last is not None:
        raise agent_help.relay(last.stderr, exit_code=last.returncode or 1)
    raise agent_help.report(
        "endless-go is not on PATH, and it owns the settings registry, so no "
        "setting could be read. Nothing was changed.",
        "installing endless-go alongside endless on their own machine — the "
        "two ship together and Endless cannot supply the missing half",
        text="endless-go binary not found on PATH.",
    )


def by_id(doc: dict) -> dict[str, dict]:
    """An export's settings, keyed by ID."""
    return {s["id"]: s for s in doc["settings"]}


def accepted(setting: dict) -> str:
    """The accepted values of a setting, as a refusal names them."""
    kind = setting["kind"]
    if kind == "optin":
        return '"on", "off" or "unset"'
    if kind == "choice":
        return ", ".join(f'"{c}"' for c in setting["choices"])
    if kind == "int":
        return "an integer"
    if kind == "duration":
        return 'a Go duration such as "5m"'
    if kind == "path":
        return "an absolute path, or one under ~"
    if kind == "name":
        return "a non-empty name with no /, whitespace, : or ."
    if kind == "command":
        return "a command containing {cmd}, or empty to detect the terminal"
    return "a value"


def parse(setting: dict, value: str):
    """The JSON value to write for `value`, or None when the setting does not
    accept it. An opt-in's "unset" parses to the string "unset"; the caller
    removes the key for it, since a missing key is unset."""
    kind = setting["kind"]
    if kind == "optin":
        return value if value in ("on", "off", "unset") else None
    if kind == "choice":
        return value if value in setting["choices"] else None
    if kind == "int":
        return int(value) if re.fullmatch(r"-?\d+", value) else None
    if kind == "duration":
        return value if _DURATION.match(value) else None
    if kind == "path":
        return value if _is_path(value) else None
    if kind == "name":
        return value if _NAME.match(value) else None
    if kind == "command":
        return value if value == "" or "{cmd}" in value else None
    return None

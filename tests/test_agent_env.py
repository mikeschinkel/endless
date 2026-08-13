"""Tests for agent harness detection (E-1962).

Mirrors internal/agentenv/agentenv_test.go. The two harness cases are
transcriptions of real `env` dumps taken 2026-08-13; the rest are the design.
Nothing here should be "fixed" by loosening a match — if a real environment stops
being identified, take a fresh dump and add a detector.
"""

import pytest
from click.testing import CliRunner

from endless import agent_env


def env(**pairs):
    """A Lookup over a fixed map. Absent keys read as "", exactly as os.environ
    .get(k, "") reports an unset variable — which matters, because the Desktop
    detector's signal is partly a set of ABSENT variables."""
    return lambda key: pairs.get(key, "")


TERMINAL = dict(
    CLAUDE_CODE_ENTRYPOINT="cli",
    CLAUDECODE="1",
    CLAUDE_CODE_SESSION_ID="6f53f9d3-c73e-4f9e-b04f-fcd3742290f1",
    AI_AGENT="claude-code_2-1-222_agent",
    __CFBundleIdentifier="com.apple.Terminal",
)

# Read from the Desktop HARNESS process (`ps eww` on the bundled claude
# binary), not from Desktop's Bash tool. An earlier version of this file encoded
# a Bash-tool sample in which the entrypoint was absent, and the detector was
# written to match that fiction.
DESKTOP = dict(
    CLAUDE_CODE_ENTRYPOINT="claude-desktop",
    __CFBundleIdentifier="com.anthropic.claudefordesktop",
    CLAUDE_AGENT_SDK_VERSION="0.3.227",
    CLAUDE_CODE_HOST_SESSION_ID="local_e44c9f73-0054-4197-ac66-6c6638fc3f95",
)


@pytest.mark.parametrize("name,vars_,expected", [
    ("claude code in a terminal (observed)", TERMINAL, agent_env.CLAUDE_CLI),
    ("claude code desktop (observed, harness process)", DESKTOP,
     agent_env.CLAUDE_DESKTOP),
    ("desktop entrypoint without a bundle id",
     dict(CLAUDE_CODE_ENTRYPOINT="claude-desktop"), agent_env.CLAUDE_DESKTOP),
    ("desktop bundle id without an entrypoint",
     dict(__CFBundleIdentifier="com.anthropic.claudefordesktop"),
     agent_env.CLAUDE_DESKTOP),

    # An Agent SDK version by itself is NOT Desktop. The retired branch treated
    # it as one, which was a guess: "an SDK hosts this" is not "this is Desktop".
    ("agent sdk version alone is not desktop",
     dict(CLAUDE_AGENT_SDK_VERSION="0.3.227"), agent_env.UNKNOWN),

    # Ordering, now that Desktop has a positive entrypoint of its own.
    ("cli entrypoint beats a desktop bundle id",
     dict(CLAUDE_CODE_ENTRYPOINT="cli",
          __CFBundleIdentifier="com.anthropic.claudefordesktop"),
     agent_env.CLAUDE_CLI),

    # Ordering: the CLI's positive match gets first refusal. A terminal session
    # carrying an SDK version — a background or SDK-driven session launched FROM
    # the terminal — is still the CLI and must not be demoted to Desktop.
    ("terminal carrying an sdk version is still the cli",
     dict(CLAUDE_CODE_ENTRYPOINT="cli", CLAUDE_AGENT_SDK_VERSION="0.3.222"),
     agent_env.CLAUDE_CLI),

    ("empty environment", {}, agent_env.UNKNOWN),
    ("ide extension", dict(CLAUDE_CODE_ENTRYPOINT="vscode"), agent_env.UNKNOWN),
    ("python sdk", dict(CLAUDE_CODE_ENTRYPOINT="sdk-py"), agent_env.UNKNOWN),
    ("harness nobody has seen",
     dict(CLAUDE_CODE_ENTRYPOINT="holodeck"), agent_env.UNKNOWN),

    # Exact match: the value comes from Claude Code, not a human, so a near-miss
    # is a different surface or a corrupted environment.
    ("wrong case", dict(CLAUDE_CODE_ENTRYPOINT="CLI"), agent_env.UNKNOWN),
    ("padded", dict(CLAUDE_CODE_ENTRYPOINT=" cli "), agent_env.UNKNOWN),

    # CLAUDECODE alone says "some Claude Code", not which surface, and which is
    # the entire job.
    ("claudecode without an entrypoint",
     dict(CLAUDECODE="1"), agent_env.UNKNOWN),
])
def test_detect(name, vars_, expected):
    assert agent_env.detect(env(**vars_)) == expected, name


def test_supported_is_an_allow_list():
    assert agent_env.supported(env(**TERMINAL))
    # Detected and named, but not supported — E-1505 is the task that flips this
    # without touching detection.
    assert not agent_env.supported(env(**DESKTOP))
    assert not agent_env.supported(env())


def test_label_never_returns_a_bare_slug():
    for harness in (agent_env.CLAUDE_CLI, agent_env.CLAUDE_DESKTOP,
                    agent_env.UNKNOWN, "codex_cli"):
        rendered = agent_env.label(harness)
        assert rendered
        assert rendered != harness


def test_go_and_python_tables_agree():
    """The Go side is the one that enforces; this copy exists for `endless
    guide`. They are transcriptions of the same observed environments, so they
    should only ever diverge by mistake."""
    go_src = _read_go_source()
    for needle in ('"cli"', '"claude-desktop"', "com.anthropic.claudefordesktop",
                   "claude_cli", "claude_desktop"):
        assert needle in go_src, f"Go detector lost {needle!r}"


def _read_go_source() -> str:
    from pathlib import Path
    root = Path(__file__).resolve().parent.parent
    return (root / "internal" / "agentenv" / "agentenv.go").read_text()


# ─── the CLI refusal ────────────────────────────────────────────────────────


def _run(monkeypatch, args, **vars_):
    """Invoke the CLI with a synthetic harness environment."""
    from endless import cli
    for key in ("CLAUDE_CODE_ENTRYPOINT", "CLAUDE_AGENT_SDK_VERSION",
                "__CFBundleIdentifier", "CLAUDECODE"):
        monkeypatch.delenv(key, raising=False)
    for key, value in vars_.items():
        monkeypatch.setenv(key, value)
    return CliRunner().invoke(cli.main, args)


@pytest.mark.parametrize("args", [
    ["guide"],
    ["task", "list"],
    ["session", "status"],
    ["project", "list"],
])
def test_every_command_refuses_on_an_unsupported_harness(monkeypatch, args):
    """The refusal lives in the group callback, so it covers the whole surface.

    Per-command gating would still leave an agent walking the surface to learn
    what one banner can say up front — and the failures it hits on the way are
    the confusing kind (session-gated commands failing for want of a tmux pane),
    not the informative kind.
    """
    result = _run(monkeypatch, args, **DESKTOP)
    # Exit ZERO. The refusal is terminal, not recoverable, so a non-zero status
    # would contradict the "do not treat this as a failure to diagnose" line
    # sitting right next to it — and the exit code is the channel an agent reads
    # mechanically. "This command did not run" carries that meaning instead.
    assert result.exit_code == 0, f"{args} exited non-zero"
    assert "does not support Claude Code Desktop" in result.output
    assert "This command did not run" in result.output


def test_refusal_names_the_claude_md_instruction(monkeypatch):
    """The reason this matters at all: CLAUDE.md files say "Run `endless
    guide`". An agent that reads that and lands here needs to be told, in the
    same breath, that the instruction does not apply."""
    result = _run(monkeypatch, ["guide"], **DESKTOP)
    assert "CLAUDE.md" in result.output
    assert "does not apply here" in result.output


def test_refusal_cites_no_endless_task_id(monkeypatch):
    """"Do not use Endless here" plus "see E-NNNN" is a contradiction: resolving
    the second requires the first."""
    result = _run(monkeypatch, ["guide"], **DESKTOP)
    import re
    assert not re.search(r"\bE-\d+", result.output), result.output


def test_refusal_withholds_the_output(monkeypatch):
    """The banner and the guide are contradictory instructions; only one ships."""
    result = _run(monkeypatch, ["guide"], **DESKTOP)
    assert "The happy path" not in result.output


def test_supported_harness_runs_normally(monkeypatch):
    result = _run(monkeypatch, ["guide"], CLAUDE_CODE_ENTRYPOINT="cli")
    assert result.exit_code == 0
    assert "does not support" not in result.output
    assert "Using Endless in a Claude Code Session" in result.output


def test_refusal_fails_open_for_a_human_at_a_shell(monkeypatch):
    """UNKNOWN is overwhelmingly a person at a prompt. This fails OPEN where the
    hooks fail closed — locking a human out of their own tool to defend against
    a harness that may not exist is the worse trade."""
    result = _run(monkeypatch, ["guide"])
    assert result.exit_code == 0
    assert "does not support" not in result.output

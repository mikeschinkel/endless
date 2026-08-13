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

DESKTOP = dict(
    __CFBundleIdentifier="com.anthropic.claudefordesktop",
    CLAUDE_AGENT_SDK_VERSION="0.3.222",
)


@pytest.mark.parametrize("name,vars_,expected", [
    ("claude code in a terminal (observed)", TERMINAL, agent_env.CLAUDE_CLI),
    ("claude code desktop (observed)", DESKTOP, agent_env.CLAUDE_DESKTOP),
    ("desktop without bundle ids",
     dict(CLAUDE_AGENT_SDK_VERSION="0.3.222"), agent_env.CLAUDE_DESKTOP),

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
    for needle in ('"cli"', "com.anthropic.claudefordesktop",
                   "CLAUDE_AGENT_SDK_VERSION", "claude_cli", "claude_desktop"):
        assert needle in go_src, f"Go detector lost {needle!r}"


def _read_go_source() -> str:
    from pathlib import Path
    root = Path(__file__).resolve().parent.parent
    return (root / "internal" / "agentenv" / "agentenv.go").read_text()


# ─── the `endless guide` banner ─────────────────────────────────────────────


def _guide(monkeypatch, **vars_):
    """Run `endless guide` with a synthetic environment."""
    from endless import cli
    for key in ("CLAUDE_CODE_ENTRYPOINT", "CLAUDE_AGENT_SDK_VERSION",
                "__CFBundleIdentifier", "CLAUDECODE"):
        monkeypatch.delenv(key, raising=False)
    for key, value in vars_.items():
        monkeypatch.setenv(key, value)
    return CliRunner().invoke(cli.main, ["guide"])


def test_guide_refuses_on_an_unsupported_harness(monkeypatch):
    result = _guide(monkeypatch, **DESKTOP)
    assert result.exit_code == 0
    assert "does not support Claude Code Desktop" in result.output
    assert "Ignore Endless for this session" in result.output
    assert "E-1505" in result.output
    # The banner and the guide are contradictory instructions; only one ships.
    assert "## The happy path" not in result.output


def test_guide_prints_normally_on_the_supported_harness(monkeypatch):
    result = _guide(monkeypatch, CLAUDE_CODE_ENTRYPOINT="cli")
    assert result.exit_code == 0
    assert "does not support" not in result.output
    assert "Endless" in result.output


def test_guide_fails_open_for_a_human_at_a_shell(monkeypatch):
    """UNKNOWN is overwhelmingly a person at a prompt — the docs tell you to run
    `endless guide`. This banner fails OPEN where the hooks fail closed, because
    refusing a human breaks a real workflow to defend against a harness that may
    not exist."""
    result = _guide(monkeypatch)
    assert result.exit_code == 0
    assert "does not support" not in result.output

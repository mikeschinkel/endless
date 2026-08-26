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


# ─── present(): agent-vs-person, which is not supported-vs-not (E-2006) ─────


@pytest.mark.parametrize("name,vars_,expected", [
    ("claude code in a terminal", TERMINAL, True),
    # The row that carries the meaning: Desktop is an AGENT and is NOT
    # supported. Answering "did an agent do this?" with supported() would call
    # a Desktop session's own edit a person's.
    ("claude desktop, detected but unsupported",
     dict(CLAUDE_CODE_ENTRYPOINT="claude-desktop"), True),
    ("bare shell", {}, False),
    ("an unrecognized harness", dict(CLAUDE_CODE_ENTRYPOINT="holodeck"), False),
    ("claudecode without an entrypoint", dict(CLAUDECODE="1"), False),
])
def test_present(name, vars_, expected):
    assert agent_env.present(env(**vars_)) is expected, name


def test_present_is_not_supported():
    """Pin that the two predicates differ, so neither collapses into the other."""
    desktop = env(CLAUDE_CODE_ENTRYPOINT="claude-desktop")
    assert agent_env.present(desktop) is True
    assert agent_env.supported(desktop) is False


# ─── the consumers ask the detector, not the environment (E-1966) ───────────
#
# Two helpers predate this module and each carried its own CLAUDECODE=1 test:
# task_cmd._running_under_agent() and agent_help.is_claude_code_agent(). Three
# spellings of one question, of which two answered "some Claude Code" rather
# than which, and missed every non-Claude harness. Both now delegate here; the
# second is gone entirely, folded into its only caller.


def _pin_env(monkeypatch, **vars_):
    """Put the real os.environ into a known harness state."""
    for key in ("CLAUDE_CODE_ENTRYPOINT", "CLAUDE_AGENT_SDK_VERSION",
                "__CFBundleIdentifier", "CLAUDECODE"):
        monkeypatch.delenv(key, raising=False)
    for key, value in vars_.items():
        monkeypatch.setenv(key, value)


@pytest.mark.parametrize("name,vars_,expected", [
    ("claude code in a terminal", TERMINAL, True),
    ("bare shell", {}, False),
    # The case that proves the delegation: the retired body returned True here.
    ("claudecode without an entrypoint", dict(CLAUDECODE="1"), False),
])
def test_running_under_agent_follows_the_detector(monkeypatch, name, vars_, expected):
    from endless import task_cmd
    _pin_env(monkeypatch, **vars_)
    assert task_cmd._running_under_agent() is expected, name


@pytest.mark.parametrize("name,vars_,expected", [
    ("claude code in a terminal", TERMINAL, True),
    ("bare shell", {}, False),
    ("claudecode without an entrypoint", dict(CLAUDECODE="1"), False),
])
def test_help_augmentation_follows_the_detector(monkeypatch, name, vars_, expected):
    from endless import agent_help
    _pin_env(monkeypatch, **vars_)
    monkeypatch.setattr(agent_help, "_AGENT_VIEW", False)
    assert agent_help._should_augment() is expected, name
    # --agent-view is a human's deliberate preview, not harness detection, so it
    # stays an independent term rather than being folded in.
    monkeypatch.setattr(agent_help, "_AGENT_VIEW", True)
    assert agent_help._should_augment() is True


def test_the_third_spelling_is_gone():
    """agent_help no longer answers the harness question itself.

    Its `is_claude_code_agent()` was folded into `_should_augment()`, its only
    caller. Re-adding a module-level harness predicate here is how the codebase
    grows a second answer to a question that has one.

    Scoped to harness IDENTITY. task_cmd still reads CLAUDECODE where it asks
    something else: `_current_endless_session_id()` pairs it with
    CLAUDE_CODE_SESSION_ID to resolve WHICH session this is (E-1455), which is
    not a question about which harness is running. (A second such reader, the
    task-attach verb, used it to refuse an exec that would kill the caller's own
    Claude process; it went with background agents in E-2074.)
    """
    from endless import agent_help
    assert not hasattr(agent_help, "is_claude_code_agent")


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


def test_refusal_covers_help_so_the_agent_block_never_lands(monkeypatch):
    """`--help` is refused too, which is the seam E-1966 opened.

    Folding `is_claude_code_agent()` onto the detector widened
    `_should_augment()` from "Claude Code CLI" to "any recognized harness", so
    Desktop now answers True and the directive block IS computed for it. It
    never reaches anyone: Click runs the root group callback before rendering a
    subcommand's help, so the banner replaces the whole output. Were that
    ordering to change, an unsupported harness would start being handed
    `endless guide <section>` — a pointer to a command that refuses.
    """
    assert agent_env.detect(env(**DESKTOP)) != agent_env.UNKNOWN  # block computed
    result = _run(monkeypatch, ["task", "spawn", "--help"], **DESKTOP)
    assert "does not support Claude Code Desktop" in result.output
    assert "AGENT — read this" not in result.output
    assert "Usage:" not in result.output

    # The supported harness still gets it — this pins the ordering, not the
    # augmentation.
    result = _run(monkeypatch, ["task", "spawn", "--help"], **TERMINAL)
    assert "AGENT — read this" in result.output


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


# ─── the refusal must not leak into other commands' output (E-1997) ──────────


def test_shell_init_prints_only_its_snippet_on_an_unsupported_harness(monkeypatch):
    """The reported bug, at its narrowest.

    `endless setup shell-helpers` appends 'eval "$(endless shell-init)"' to the
    user's rc, so this command runs on EVERY shell the harness launches —
    including the shell behind every Bash tool call. The banner goes to stderr,
    which `$( )` does not capture, so it surfaced ahead of the output of an
    unrelated `gh repo deploy-key add` the user had approved, and again on every
    terminal opened inside Claude Code Desktop.

    Exact equality, not a substring check: any byte on either stream is a byte
    that lands in somebody else's tool output.
    """
    from endless import cli
    result = _run(monkeypatch, ["shell-init"], **DESKTOP)
    assert result.exit_code == 0
    assert result.output == cli._SHELL_INIT_SNIPPET


def test_shell_init_output_is_identical_across_harnesses(monkeypatch):
    """Whatever the harness, the eval'd snippet is the same static text — so the
    helpers stay defined and no rc grows a harness-conditional branch."""
    from endless import cli
    for vars_ in (DESKTOP, TERMINAL, {}):
        result = _run(monkeypatch, ["shell-init"], **vars_)
        assert result.output == cli._SHELL_INIT_SNIPPET, vars_


def test_the_rc_line_endless_installs_invokes_an_exempt_subcommand():
    """Pins the exemption to what the installer actually writes.

    The defect was not "shell-init is special", it was "Endless put an `endless`
    call into the user's shell startup and then made that call talk". If the rc
    line is ever repointed at another subcommand, that subcommand needs the same
    exemption — and this fails until it gets one.
    """
    from endless import cli, setup
    assert setup.SHELL_HELPERS_EVAL == 'eval "$(endless shell-init)"'
    assert "shell-init" in cli.HARNESS_EXEMPT_SUBCOMMANDS


def test_exempt_subcommands_have_nothing_to_withhold():
    """An exemption is only defensible for a command with no side effect the
    refusal could be withholding — otherwise "This command did not run" would be
    replaced by silently running it. SANDBOX_SAFE_SUBCOMMANDS already carries
    exactly that property (pure stdout, no project/global I/O), so exemption is
    a subset of it, not a parallel judgment call."""
    from endless import cli
    assert cli.HARNESS_EXEMPT_SUBCOMMANDS <= cli.SANDBOX_SAFE_SUBCOMMANDS


@pytest.mark.parametrize("args", [
    ["session", "use"],
    ["session", "cd", "--target", "project"],
    ["session", "forget"],
])
def test_the_helpers_themselves_still_refuse(monkeypatch, args):
    """The exemption buys silence for defining the helpers, not for using them.

    esu/esp/esf each shell out to `endless`, so an unsupported harness still
    meets the banner the moment someone deliberately invokes one — which is the
    invocation the banner was written for.
    """
    result = _run(monkeypatch, args, **DESKTOP)
    assert result.exit_code == 0
    assert "does not support Claude Code Desktop" in result.output

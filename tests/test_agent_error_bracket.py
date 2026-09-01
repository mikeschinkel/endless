"""The bracketed refusal: one verdict line at BOTH ends, for agents (E-2097).

A refusal's verdict used to be line 2 of 17. An agent piping `2>&1 | tail -3`
kept the tail of the guidance and never saw the one line naming the constraint
— measured across five refusals in one session, one of which shaved 34
characters off a title without ever reading the `107>100` the error had stated
twice, and one of which wrote a junk value to a live task to probe the
validator.

So a refusal rendered for an agent repeats a dense, one-line verdict as its
FIRST and LAST line. The two are byte-identical on purpose: split them and
`head -N` yields the problem without the fix while `tail -N` yields the fix
without the problem.

The end-to-end assertions run through the real CLI rather than the message
string, because the property is about RENDERED output. Click prints a
ClickException as `Error: <message>`, so the first line arrives prefixed —
whether the two ends match is a fact about what lands on stderr, and only a
rendered comparison can see it.
"""

import pytest
from click.testing import CliRunner

from endless import agent_env, agent_help, db, task_cmd
from endless.cli import main


LONG_TITLE = "Add " + "x" * (task_cmd.TITLE_MAX_LENGTH - len("Add ") + 7)
LONG_DESCRIPTION = "d" * (task_cmd.DESCRIPTION_MAX_LENGTH + 8)


@pytest.fixture(autouse=True)
def _pin_agent_view(monkeypatch):
    """`--agent-view` is module state set by the CLI's argv pre-scan.

    An invocation that passes the flag would otherwise leak it into every test
    that ran afterwards in the same process.
    """
    monkeypatch.setattr(agent_help, "_AGENT_VIEW", False)


@pytest.fixture
def as_agent(monkeypatch):
    """Run as a Claude Code agent. conftest strips the signal; this restores it."""
    monkeypatch.setenv(agent_env.ENTRYPOINT_VAR, agent_env.CLI_ENTRYPOINT)


@pytest.fixture
def project_row(isolated_env):
    db.execute(
        "INSERT INTO projects (name, path, status, created_at, updated_at) "
        "VALUES ('sample', '/tmp/sample', 'active', datetime('now'), datetime('now'))"
    )


def _stderr_lines(result):
    """The refusal as it reached the terminal, one line per element.

    `result.output` is stdout and stderr interleaved, which is what `2>&1`
    produces — the shape the agent that motivated this task was reading.
    """
    return result.output.rstrip("\n").split("\n")


# ─── the helper, in isolation ───────────────────────────────────────────────


def test_human_form_is_the_guidance_unchanged():
    """Byte for byte. A repeated long line is noise to a reader who was never
    going to truncate it, so the duplication is agent-only by design."""
    guidance = "Title is 107 characters; max is 100.\n\n  Some teaching.\n"
    assert agent_help.agent_error("verdict", guidance) == guidance


def test_agent_form_brackets_the_guidance(as_agent):
    msg = agent_help.agent_error("title 107>100 chars.", "the guidance", command="task add")
    lines = msg.split("\n")
    assert lines[0] == "ENDLESS-ERROR task add: title 107>100 chars."
    assert lines[-1] == "Error: ENDLESS-ERROR task add: title 107>100 chars."
    assert "the guidance" in msg


def test_agent_form_without_a_command_still_carries_the_sentinel(as_agent):
    """Called outside a running command there is no verb to name, and a bare
    error line still has to be identifiable as one of ours."""
    msg = agent_help.agent_error("something is wrong.", "guidance", command=None)
    assert msg.split("\n")[0] == "ENDLESS-ERROR: something is wrong."


# ─── the property, through the real CLI ─────────────────────────────────────


def test_refusal_first_and_last_lines_are_identical(project_row, as_agent):
    result = CliRunner().invoke(main, ["task", "add", LONG_TITLE])
    assert result.exit_code != 0
    lines = _stderr_lines(result)
    assert lines[0] == lines[-1], "\n".join(lines)
    assert lines[0].startswith("Error: ENDLESS-ERROR task add:")


def test_head_and_tail_each_keep_the_verdict(project_row, as_agent):
    """The two ends of the property, measured the way the failure was: a small
    window over the refusal, from either side."""
    result = CliRunner().invoke(main, ["task", "add", LONG_TITLE])
    lines = _stderr_lines(result)
    head, tail = lines[:3], lines[-3:]
    assert any(agent_help.ERROR_SENTINEL in line for line in head), head
    assert any(agent_help.ERROR_SENTINEL in line for line in tail), tail


def test_the_verdict_carries_what_would_have_stopped_the_failure(project_row, as_agent):
    result = CliRunner().invoke(main, ["task", "add", LONG_TITLE])
    verdict = _stderr_lines(result)[0]
    # The measured numbers, not an adjective — the number is what makes the
    # correction computable.
    assert f"title {len(LONG_TITLE)}>{task_cmd.TITLE_MAX_LENGTH} chars" in verdict
    # The command that produced it, so the line survives out of context.
    assert "task add" in verdict
    # Where the overflow goes, not merely "shorten".
    assert "--analysis" in verdict and "--text" in verdict
    # Whether anything changed.
    assert task_cmd.NOTHING_CREATED in verdict


def test_the_verdict_is_one_line(project_row, as_agent):
    """Length is unconstrained — head/tail are line-based, so a 200-character
    line survives whole. Being ONE line is the requirement; a paragraph invites
    the same skimming that caused the failure."""
    result = CliRunner().invoke(main, ["task", "add", LONG_TITLE])
    lines = _stderr_lines(result)
    assert sum(1 for line in lines if agent_help.ERROR_SENTINEL in line) == 2


def test_update_names_its_own_verb_and_says_nothing_changed(project_row, as_agent):
    db.execute(
        "INSERT INTO tasks (project_id, title, description, status, type_id, phase, created_at) "
        "VALUES (1, 'Add a thing', 'short', 'unplanned', 1, 'now', datetime('now'))"
    )
    result = CliRunner().invoke(main, ["task", "update", "1", "--title", LONG_TITLE])
    verdict = _stderr_lines(result)[0]
    assert "task update" in verdict
    assert task_cmd.NOTHING_CHANGED in verdict


# ─── humans see today's message ─────────────────────────────────────────────


def test_human_refusal_is_not_bracketed(project_row, monkeypatch):
    monkeypatch.delenv(agent_env.ENTRYPOINT_VAR, raising=False)
    result = CliRunner().invoke(main, ["task", "add", LONG_TITLE])
    assert result.exit_code != 0
    out = result.output
    assert agent_help.ERROR_SENTINEL not in out
    # Today's shape, unchanged: a blank line, then Click's one `Error:` line,
    # then the guidance — stated once.
    lines = out.rstrip("\n").split("\n")
    assert lines[0] == ""
    assert lines[1] == f"Error: Title is {len(LONG_TITLE)} characters; max is {task_cmd.TITLE_MAX_LENGTH}."
    assert out.count("Consider using this template:") == 1


def test_agent_view_renders_the_bracket_for_a_human(project_row, monkeypatch):
    """`--agent-view` is a human's override for previewing what an agent sees.

    A version that fired on detection alone would make agent-facing output the
    one thing --agent-view cannot show.
    """
    monkeypatch.delenv(agent_env.ENTRYPOINT_VAR, raising=False)
    result = CliRunner().invoke(main, ["task", "add", LONG_TITLE, "--agent-view"])
    lines = _stderr_lines(result)
    assert lines[0] == lines[-1]
    assert agent_help.ERROR_SENTINEL in lines[0]


# ─── every problem at once ──────────────────────────────────────────────────


def test_title_and_description_are_named_in_one_verdict(project_row, as_agent):
    """Both violations, one refusal. They used to be sequential raises, so the
    description problem was only discovered after the title was fixed."""
    result = CliRunner().invoke(
        main, ["task", "add", LONG_TITLE, "--description", LONG_DESCRIPTION]
    )
    lines = _stderr_lines(result)
    verdict = lines[0]
    assert f"title {len(LONG_TITLE)}>{task_cmd.TITLE_MAX_LENGTH} chars" in verdict
    assert (f"description {len(LONG_DESCRIPTION)}>"
            f"{task_cmd.DESCRIPTION_MAX_LENGTH} chars") in verdict
    assert lines[0] == lines[-1]
    # One refusal, not two: both guidance blocks, one verdict at each end.
    assert "Consider using this template:" in result.output
    assert "not a dissertation" in result.output


def test_a_multiline_and_oversized_description_reports_both(project_row):
    """The same accumulation inside one field."""
    problems = task_cmd._description_problems("x" * 2000 + "\nsecond line")
    assert len(problems) == 2
    assert "chars" in problems[0].verdict
    assert "newline" in problems[1].verdict


def test_a_refused_call_mutated_nothing(project_row, as_agent):
    result = CliRunner().invoke(
        main, ["task", "add", LONG_TITLE, "--description", LONG_DESCRIPTION]
    )
    assert result.exit_code != 0
    rows = list(db.query("SELECT count(*) AS c FROM tasks"))
    assert rows[0]["c"] == 0


# ─── the shared predicate ───────────────────────────────────────────────────


def test_the_refusal_and_the_help_share_one_predicate():
    """`agent_error` must not grow a fourth spelling of "is an agent reading
    this?". agent_help has consolidated that question twice already (E-1966,
    E-2006) and retired task_cmd's wrapper in E-2097."""
    import inspect
    source = inspect.getsource(agent_help.agent_error)
    assert "agent_facing()" in source
    assert "agent_env" not in source
    assert "_AGENT_VIEW" not in source

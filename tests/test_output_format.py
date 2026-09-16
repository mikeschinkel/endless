"""The output-format flag surface: `--agent`, `--json` and `--format` (E-1504).

Three spellings of one setting — which rendering a command emits. The rename of
`--llm` to `--agent` is here too, and so is the drift guard: a sweep over the
whole command tree asserting that the three spellings arrive together, because
they drifted apart once already (`--llm` on 15 commands, `--json` on 28, and no
command able to say which renderings it actually had).
"""

import json

import click
import pytest
from click.testing import CliRunner

from endless import db
from endless.cli import main


# ─── walking the command tree ────────────────────────────────────────────────

def _walk(cmd, path=("endless",)):
    yield " ".join(path), cmd
    for name, sub in getattr(cmd, "commands", {}).items():
        yield from _walk(sub, path + (name,))


def _opts(cmd):
    """Every option spelling the command declares, hidden ones included."""
    return {o for p in cmd.params for o in getattr(p, "opts", ())}


def _param(cmd, spelling):
    for p in cmd.params:
        if spelling in getattr(p, "opts", ()):
            return p
    return None


ALL = list(_walk(main))

# `--json` here names how the command reads its INPUT, not how it writes its
# output: `session order --json` parses SPEC as a JSON array-of-groups, and
# `task import --json` names a file to read. Neither is a rendering, so neither
# takes `--format`. Listed rather than inferred, so that adding a third one is a
# deliberate act with a test to change.
INPUT_JSON = {"endless session order", "endless task import"}


# ─── the drift guard ─────────────────────────────────────────────────────────

def test_every_output_json_command_also_carries_format():
    missing = [
        path for path, cmd in ALL
        if "--json" in _opts(cmd) and path not in INPUT_JSON
        and "--format" not in _opts(cmd)
    ]
    assert not missing, f"--json without --format: {missing}"


def test_format_never_appears_without_json():
    """`--format` is the long form of the short flags, so it never outlives them."""
    stray = [path for path, cmd in ALL
             if "--format" in _opts(cmd) and "--json" not in _opts(cmd)]
    assert not stray


def test_input_json_commands_do_not_advertise_a_rendering():
    for path in INPUT_JSON:
        cmd = dict(ALL)[path]
        assert "--format" not in _opts(cmd)
        assert "--agent" not in _opts(cmd)


def test_format_advertises_exactly_what_the_command_accepts():
    """The metavar names `agent` if and only if the command has that rendering."""
    for path, cmd in ALL:
        p = _param(cmd, "--format")
        if p is None:
            continue
        metavar = p.type.get_metavar(p, None)
        assert ("agent" in metavar) == ("--agent" in _opts(cmd)), path


def test_every_agent_command_retires_llm():
    """The rename is complete: nothing carries `--agent` without the pointer
    from its old name, and nothing still carries a working `--llm`."""
    for path, cmd in ALL:
        if "--agent" not in _opts(cmd):
            continue
        assert "--llm" in _opts(cmd), f"{path} lost the --llm pointer"
        assert _param(cmd, "--llm").hidden, f"{path} still advertises --llm"


# ─── the three spellings agree ───────────────────────────────────────────────

@pytest.fixture
def linked_task(seeded_project_at_cwd):
    cur = db.execute(
        "INSERT INTO tasks (project_id, title, status, type_id, phase, created_at) "
        "VALUES (1, 'subject', 'ready', 1, 'now', datetime('now'))")
    subject = cur.lastrowid
    cur = db.execute(
        "INSERT INTO tasks (project_id, title, status, type_id, phase, created_at) "
        "VALUES (1, 'blocker', 'underway', 1, 'now', datetime('now'))")
    blocker = cur.lastrowid
    db.execute(
        "INSERT INTO task_deps (source_type, source_id, target_type, target_id, dep_type) "
        "VALUES ('task', ?, 'task', ?, 'blocked_by')", (subject, blocker))
    return subject, blocker


def _run(*argv):
    return CliRunner().invoke(main, list(argv))


@pytest.mark.parametrize("short,long", [
    ([], ["--format", "text"]),
    (["--json"], ["--format", "json"]),
    (["--agent"], ["--format", "agent"]),
])
def test_format_is_an_exact_alias_of_the_short_flag(linked_task, short, long):
    subject, _ = linked_task
    a = _run("task", "relations", f"E-{subject}", *short)
    b = _run("task", "relations", f"E-{subject}", *long)
    assert a.exit_code == 0 and b.exit_code == 0
    assert a.output == b.output


def test_same_rendering_named_twice_is_not_a_contradiction(linked_task):
    subject, _ = linked_task
    res = _run("task", "relations", f"E-{subject}", "--json", "--format", "json")
    assert res.exit_code == 0
    assert json.loads(res.output)["id"] == f"E-{subject}"


# ─── refusals ────────────────────────────────────────────────────────────────

def test_two_different_renderings_are_refused_not_resolved(linked_task):
    """Before E-1504 `--llm --json` silently yielded JSON. A contradiction now
    costs tokens instead of returning the rendering the caller did not ask for."""
    subject, _ = linked_task
    res = _run("task", "relations", f"E-{subject}", "--json", "--agent")
    assert res.exit_code == 2
    assert "--agent and --json name two different output renderings" in res.output
    assert "--format agent or --format json" in res.output


def test_agent_is_refused_by_name_where_there_is_no_agent_rendering():
    res = _run("session", "status", "--format", "agent")
    assert res.exit_code == 2
    assert "has no agent rendering yet" in res.output
    # ED-1584: the remedy that works leads, rationale last.
    remedy = res.output.index("--format json")
    assert remedy < res.output.index("command by command")


def test_unknown_format_names_the_accepted_set():
    res = _run("task", "list", "--format", "toon")
    assert res.exit_code == 2
    assert "'toon' is not an output format. Use text, json or agent." in res.output


def test_llm_refuses_with_a_pointer_rather_than_no_such_option():
    res = _run("task", "list", "--llm")
    assert res.exit_code == 2
    assert "--llm was renamed to --agent" in res.output
    assert "No such option" not in res.output


def test_llm_is_not_advertised_anywhere():
    for _, cmd in ALL:
        assert "--llm" not in (cmd.get_help(click.Context(cmd)))


# ─── the new JSON rendering on relations/deps (E-909's gap) ──────────────────

def test_relations_json_carries_the_subject_and_its_links(linked_task):
    subject, blocker = linked_task
    payload = json.loads(_run("task", "relations", f"E-{subject}", "--json").output)
    assert payload["id"] == f"E-{subject}"
    assert payload["links"] == [
        {"id": f"E-{blocker}", "rel": "blocked by", "status": "underway"}
    ]


def test_relations_json_always_carries_a_links_key(seeded_project_at_cwd):
    cur = db.execute(
        "INSERT INTO tasks (project_id, title, status, type_id, phase, created_at) "
        "VALUES (1, 'lonely', 'ready', 1, 'now', datetime('now'))")
    payload = json.loads(_run("task", "relations", f"E-{cur.lastrowid}", "--json").output)
    assert payload["links"] == []


def test_deps_renders_identically_to_relations(linked_task):
    """They are one command under two names; the new rendering keeps them so."""
    subject, _ = linked_task
    for fmt in (["--json"], ["--agent"]):
        assert (_run("task", "deps", f"E-{subject}", *fmt).output
                == _run("task", "relations", f"E-{subject}", *fmt).output)


def test_relations_json_uses_the_same_rel_token_as_the_agent_line(linked_task):
    """E-1185: one fact wears one spelling across the renderings."""
    subject, blocker = linked_task
    payload = json.loads(_run("task", "relations", f"E-{subject}", "--json").output)
    agent = _run("task", "relations", f"E-{subject}", "--agent").output
    assert f"({payload['links'][0]['rel']})" in agent

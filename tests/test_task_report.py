"""Tests for `endless task report` (E-1771) — payload parsing, the per-entry
Haiku gate (mocked), and the steering-prompt render. The computed facts (Go
`session-query task-report`) are covered by Go unit tests + the verify script;
here `_compute_facts`/`_compute_anomalies` are stubbed so these stay fast and
hermetic."""

import subprocess

import click
import pytest

from endless import report_cmd, report_prompts


def _completed(stdout: str, code: int = 0) -> subprocess.CompletedProcess:
    return subprocess.CompletedProcess(args=["claude"], returncode=code, stdout=stdout, stderr="")


def _mock_haiku(monkeypatch, reply: str, code: int = 0):
    monkeypatch.setattr(
        report_cmd.internal_claude, "run_internal_claude",
        lambda prompt, **kw: _completed(reply, code),
    )


# --- payload parsing --------------------------------------------------------

def test_empty_payload_is_normal_path():
    assert report_cmd._parse_payload(None) == ([], [], None)
    assert report_cmd._parse_payload("") == ([], [], None)
    assert report_cmd._parse_payload("   ") == ([], [], None)


def test_parse_valid_notes_and_questions():
    payload = (
        '{"notes":[{"kind":"anomaly","text":"base is behind main"}],'
        '"questions":[{"text":"go-pkgs worktree or repo?","type":"choice","style":"single"}]}'
    )
    notes, questions, verify = report_cmd._parse_payload(payload)
    assert notes == [{"kind": "anomaly", "text": "base is behind main"}]
    assert questions == [{"text": "go-pkgs worktree or repo?", "type": "choice", "style": "single"}]
    assert verify is None


def test_question_type_defaults_to_text():
    _, questions, _ = report_cmd._parse_payload('{"questions":[{"text":"proceed?"}]}')
    assert questions == [{"text": "proceed?", "type": "text"}]


# --- E-1901: the verify field -----------------------------------------------

def test_parse_verify_command():
    payload = '{"verify":"esu && ./tests/tasks/e-1901-verify.sh"}'
    notes, questions, verify = report_cmd._parse_payload(payload)
    assert (notes, questions) == ([], [])
    assert verify == "esu && ./tests/tasks/e-1901-verify.sh"


def test_verify_is_stripped():
    _, _, verify = report_cmd._parse_payload('{"verify":"  just test  "}')
    assert verify == "just test"


def test_empty_verify_rejected():
    with pytest.raises(click.ClickException, match="non-empty string"):
        report_cmd._parse_payload('{"verify":"   "}')


def test_non_string_verify_rejected():
    with pytest.raises(click.ClickException, match="non-empty string"):
        report_cmd._parse_payload('{"verify":["a","b"]}')


def test_multiline_verify_rejected():
    """The handoff contract is ONE command. A multi-line value is a checklist
    wearing a field's clothes — exactly the ceremony this replaces."""
    with pytest.raises(click.ClickException, match="ONE command"):
        report_cmd._parse_payload('{"verify":"just build\\njust test"}')


def test_verify_is_never_haiku_gated(monkeypatch):
    """A command is not prose. Sending it to the ceremony classifier would
    invent a failure mode rather than catch one."""
    def boom(prompt, **kw):
        raise AssertionError("verify must not be classified")
    monkeypatch.setattr(report_cmd.internal_claude, "run_internal_claude", boom)
    notes, questions, verify = report_cmd._parse_payload('{"verify":"just test"}')
    report_cmd._gate(notes, questions, report_prompts.DEFAULTS)
    assert verify == "just test"


def test_malformed_json_rejected():
    with pytest.raises(click.ClickException, match="not valid JSON"):
        report_cmd._parse_payload("{not json")


def test_non_object_payload_rejected():
    with pytest.raises(click.ClickException, match="must be a JSON object"):
        report_cmd._parse_payload('["a","b"]')


def test_unknown_top_level_field_rejected():
    with pytest.raises(click.ClickException, match="unknown report field"):
        report_cmd._parse_payload('{"notes":[],"summary":"hi"}')


def test_bad_note_kind_rejected():
    with pytest.raises(click.ClickException, match="kind must be one of"):
        report_cmd._parse_payload('{"notes":[{"kind":"chatter","text":"x"}]}')


def test_unknown_note_field_rejected():
    with pytest.raises(click.ClickException, match="unknown field"):
        report_cmd._parse_payload('{"notes":[{"kind":"anomaly","text":"x","extra":1}]}')


def test_empty_note_text_rejected():
    with pytest.raises(click.ClickException, match="non-empty 'text'"):
        report_cmd._parse_payload('{"notes":[{"kind":"anomaly","text":"  "}]}')


def test_bad_question_type_rejected():
    with pytest.raises(click.ClickException, match="type must be one of"):
        report_cmd._parse_payload('{"questions":[{"text":"x","type":"date"}]}')


# --- per-entry Haiku gate ---------------------------------------------------

def test_classify_keep(monkeypatch):
    _mock_haiku(monkeypatch, "KEEP")
    keep, reason = report_cmd._classify("check {text}", "a real anomaly")
    assert keep is True and reason is None


def test_classify_drop_with_reason(monkeypatch):
    _mock_haiku(monkeypatch, "DROP: this just confirms the tree is clean")
    keep, reason = report_cmd._classify("check {text}", "working tree clean")
    assert keep is False
    assert "confirms the tree is clean" in reason


def test_classify_fail_open_on_nonzero(monkeypatch):
    _mock_haiku(monkeypatch, "", code=1)
    keep, reason = report_cmd._classify("check {text}", "anything")
    assert keep is True and reason is None


def test_classify_fail_open_on_unparseable(monkeypatch):
    _mock_haiku(monkeypatch, "I think maybe?")
    keep, _ = report_cmd._classify("check {text}", "anything")
    assert keep is True


def test_classify_fail_open_on_missing_binary(monkeypatch):
    def boom(prompt, **kw):
        raise FileNotFoundError("claude")
    monkeypatch.setattr(report_cmd.internal_claude, "run_internal_claude", boom)
    keep, _ = report_cmd._classify("check {text}", "anything")
    assert keep is True


def test_gate_bounces_ceremonial_note(monkeypatch):
    _mock_haiku(monkeypatch, "DROP: ceremony")
    prompts = report_prompts.DEFAULTS
    with pytest.raises(click.ClickException, match="ceremony"):
        report_cmd._gate([{"kind": "anomaly", "text": "tree clean"}], [], prompts)


def test_gate_passes_genuine_note(monkeypatch):
    _mock_haiku(monkeypatch, "KEEP")
    prompts = report_prompts.DEFAULTS
    report_cmd._gate([{"kind": "discovery", "text": "E-1648 landed mid-session"}], [], prompts)


def test_gate_bounce_agent_phrasing(monkeypatch):
    monkeypatch.setenv("CLAUDECODE", "1")
    _mock_haiku(monkeypatch, "DROP: settled")
    prompts = report_prompts.DEFAULTS
    with pytest.raises(click.ClickException) as exc:
        report_cmd._gate([], [{"text": "should I proceed?", "type": "text"}], prompts)
    assert "Do not invent a decision" in exc.value.message


def test_gate_no_entries_never_calls_haiku(monkeypatch):
    def boom(prompt, **kw):
        raise AssertionError("Haiku must not be called on the empty path")
    monkeypatch.setattr(report_cmd.internal_claude, "run_internal_claude", boom)
    report_cmd._gate([], [], report_prompts.DEFAULTS)


# --- render -----------------------------------------------------------------

def test_render_clean_task_is_empty():
    """A clean handoff has nothing the user could not already compute (E-1880):
    no Task/Status/Landed recap, no empty-category ceremony — an empty block."""
    facts = {"status": "unverified", "type": "todo", "landed": False,
             "successors": []}
    block = report_cmd._render_sanctioned(facts, [], [], None)
    assert block.strip() == ""


def test_verify_leads_the_sanctioned_block():
    """It is the one line the user acts on, so it comes first."""
    facts = {"status": "unverified", "type": "todo", "landed": False,
             "successors": [{"id": 1906, "status": "untriaged", "relation": "relates_to"}],
             "children": []}
    block = report_cmd._render_sanctioned(facts, [], [], "esu && ./x.sh")
    assert block.splitlines()[0] == "Verify: `esu && ./x.sh`"


def test_verify_alone_makes_the_block_non_empty():
    """The keystone: an otherwise-clean session still has a sanctioned block,
    because the deliverable pointer is a field now instead of freehand prose."""
    facts = {"status": "unverified", "type": "todo", "landed": False,
             "successors": [], "children": []}
    block = report_cmd._render_sanctioned(facts, [], [], "just test")
    assert block.strip() == "Verify: `just test`"


def test_anomalies_are_not_in_the_sanctioned_block():
    """Anomalies are conditional ('surface only if unexpected') and the
    sanctioned block is unconditional — including them would force the relay of
    noise the agent was told to judge."""
    facts = {"status": "unverified", "type": "todo", "landed": False,
             "successors": [], "children": []}
    block = report_cmd._render_sanctioned(facts, [], [], None)
    assert "uncommitted" not in block
    addendum = report_cmd._render_agent_notes(["uncommitted: scratch.go"])
    assert "uncommitted: scratch.go" in addendum
    assert "NOT part of the report" in addendum
    assert "--json anomaly note" in addendum  # names the way back in


def test_no_anomalies_renders_no_addendum():
    assert report_cmd._render_agent_notes([]) == ""


def test_render_omits_computable_lines():
    """Status/Landed/Task are computable from `task show` + `session status`, and
    the handoff forbids recapping them — so the command must not emit them even
    when it has the values."""
    facts = {"status": "unverified", "type": "todo", "landed": True,
             "successors": [{"id": 1772, "status": "unverified", "relation": "blocks"}],
             "children": []}
    block = report_cmd._render_sanctioned(facts, [], [], None)
    assert "Status:" not in block
    assert "Landed" not in block
    assert "Task: E-" not in block
    assert "Follow-ups you filed: E-1772 [unverified]" in block


def test_render_includes_nonempty_sections():
    facts = {
        "status": "unverified", "type": "epic", "landed": True,
        "successors": [{"id": 1772, "status": "unverified", "relation": "blocks"}],
    }
    notes = [{"kind": "anomaly", "text": "base is behind main"}]
    questions = [{"text": "worktree or repo?", "type": "choice", "style": "single"}]
    block = report_cmd._render_sanctioned(facts, notes, questions, "just test")
    assert "Verify: `just test`" in block
    assert "Follow-ups you filed: E-1772 [unverified]" in block
    assert "[anomaly] base is behind main" in block
    assert "worktree or repo?  (choice/single)" in block


# --- E-1911: children are not the report's job at all -----------------------

def test_render_never_lists_children_even_for_an_epic():
    """The Children line was epic-only (E-1880) because the epic handoff asked
    the session to lead with their state — and that directive existed because
    the report computed them. Both are gone: `session status` renders a task's
    children, and the report duplicating it is what listed E-1907 and E-1908 as
    children of E-1785 when neither was one."""
    facts = {
        "status": "unverified", "type": "epic", "landed": False,
        "successors": [{"id": 1872, "status": "submitted", "relation": "cleaned_up_by"}],
        # Present in the dict on purpose: a renderer that reads it again fails.
        "children": [{"id": 1899, "status": "ready"},
                     {"id": 1900, "status": "confirmed"}],
    }
    block = report_cmd._render_sanctioned(facts, [], [], None)
    assert "Children" not in block
    assert "E-1899" not in block
    assert "E-1900" not in block
    assert block.strip() == "Follow-ups you filed: E-1872 [submitted]"


def test_render_epic_with_only_children_is_empty():
    """An epic whose only related tasks are children now reports nothing rather
    than a recap — and `report_item` turns that into `Nothing to report.`"""
    facts = {"status": "unverified", "type": "epic", "landed": False,
             "successors": [], "children": [{"id": 1899, "status": "ready"}]}
    assert report_cmd._render_sanctioned(facts, [], [], None).strip() == ""


def test_render_follow_up_appears_exactly_once():
    """`--parent E-N --cleans-up E-N` — the pattern every handoff prescribes —
    lands one task in both relations. With children gone the dedupe that used to
    be needed is structural: the id can only render under follow-ups."""
    refs = [{"id": 1872, "status": "submitted", "relation": "cleaned_up_by"},
            {"id": 1873, "status": "submitted", "relation": "cleaned_up_by"}]
    facts = {"status": "unverified", "type": "epic", "landed": False,
             "successors": refs,
             "children": [{"id": r["id"], "status": r["status"]} for r in refs]}
    block = report_cmd._render_sanctioned(facts, [], [], None)
    assert block.count("E-1872") == 1
    assert block.count("E-1873") == 1


# --- E-1953 increment 1: the render path is OFF -----------------------------

def test_report_item_refuses_while_disabled(monkeypatch):
    """The disable is asserted through the PUBLIC entry point, not the flag.

    A test that only read `REPORT_DISABLED` would pass against a build whose
    `report_item` had stopped consulting it — which is exactly the regression
    that matters, because the render path below it is still fully intact for
    increment 2 to rebuild in place.
    """
    called = []
    monkeypatch.setattr(report_cmd, "_compute_facts", lambda i: called.append("facts"))
    monkeypatch.setattr(report_cmd, "_classify", lambda p, t: called.append("haiku"))
    monkeypatch.setattr(report_cmd, "_record_checkpoint", lambda t: called.append("gate"))

    with pytest.raises(click.ClickException) as e:
        report_cmd.report_item(1953, None)
    assert "disabled" in str(e.value)

    # Nothing on the way to the refusal: no model call, no DB read, no gate arm.
    # A disabled command that still costs a Haiku round-trip is not disabled.
    assert called == []


def test_disabled_refusal_forbids_hand_writing_a_block(monkeypatch):
    """The refusal must not leave the agent believing it should improvise one.

    An agent told only "the command is off" reproduces the block from memory —
    it has seen hundreds of them. Naming the separator and the `Nothing to
    report.` line as things NOT to write is the whole point of the message.
    """
    with pytest.raises(click.ClickException) as e:
        report_cmd.report_item(1953, None)
    msg = str(e.value)
    assert "Do NOT hand-write" in msg
    assert "Nothing to report." in msg


def test_disabled_refusal_precedes_payload_validation():
    """Refusal beats rejection. A malformed payload must produce the DISABLED
    message, not a parse error — otherwise an agent debugging its JSON never
    learns the command is off and keeps retrying a surface that is gone."""
    with pytest.raises(click.ClickException) as e:
        report_cmd.report_item(1953, "{not json")
    assert "disabled" in str(e.value)


# --- tunable config surface -------------------------------------------------

def test_prompts_default_to_embedded(isolated_env):
    prompts = report_prompts.load_prompts()
    assert prompts == report_prompts.DEFAULTS


def test_machine_layer_overrides_embedded(isolated_env):
    import json
    from endless import config
    (config.CONFIG_DIR / "report-prompts.jsonl").write_text(
        json.dumps({"name": "steer", "text": "MACHINE STEER {facts}"}) + "\n"
    )
    prompts = report_prompts.load_prompts()
    assert prompts[report_prompts.STEER] == "MACHINE STEER {facts}"
    # untouched names keep the embedded default
    assert prompts[report_prompts.NOTE_CHECK] == report_prompts.DEFAULTS[report_prompts.NOTE_CHECK]


def test_unknown_name_ignored(isolated_env):
    import json
    from endless import config
    (config.CONFIG_DIR / "report-prompts.jsonl").write_text(
        json.dumps({"name": "bogus", "text": "nope"}) + "\n"
    )
    prompts = report_prompts.load_prompts()
    assert "bogus" not in prompts
    assert prompts == report_prompts.DEFAULTS

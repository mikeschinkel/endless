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
    assert report_cmd._parse_payload(None) == ([], [])
    assert report_cmd._parse_payload("") == ([], [])
    assert report_cmd._parse_payload("   ") == ([], [])


def test_parse_valid_notes_and_questions():
    payload = (
        '{"notes":[{"kind":"anomaly","text":"base is behind main"}],'
        '"questions":[{"text":"go-pkgs worktree or repo?","type":"choice","style":"single"}]}'
    )
    notes, questions = report_cmd._parse_payload(payload)
    assert notes == [{"kind": "anomaly", "text": "base is behind main"}]
    assert questions == [{"text": "go-pkgs worktree or repo?", "type": "choice", "style": "single"}]


def test_question_type_defaults_to_text():
    notes, questions = report_cmd._parse_payload('{"questions":[{"text":"proceed?"}]}')
    assert questions == [{"text": "proceed?", "type": "text"}]


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
             "successors": [], "children": None}
    block = report_cmd._render_facts(facts, [], [], [])
    assert block.strip() == ""


def test_render_omits_computable_lines():
    """Status/Landed/Task are computable from `task show` + `session status`, and
    the handoff forbids recapping them — so the command must not emit them even
    when it has the values."""
    facts = {"status": "unverified", "type": "todo", "landed": True,
             "successors": [{"id": 1772, "status": "unverified", "relation": "blocks"}],
             "children": []}
    block = report_cmd._render_facts(facts, [], [], [])
    assert "Status:" not in block
    assert "Landed" not in block
    assert "Task: E-" not in block
    assert "Follow-ups you filed: E-1772 [unverified]" in block


def test_render_includes_nonempty_sections():
    facts = {
        "status": "unverified", "type": "epic", "landed": True,
        "successors": [{"id": 1772, "status": "unverified", "relation": "blocks"}],
        "children": [{"id": 1800, "status": "assumed"}],
    }
    notes = [{"kind": "anomaly", "text": "base is behind main"}]
    questions = [{"text": "worktree or repo?", "type": "choice", "style": "single"}]
    block = report_cmd._render_facts(facts, ["uncommitted: scratch.go"], notes, questions)
    assert "Follow-ups you filed: E-1772 [unverified]" in block
    assert "Children: E-1800 [assumed]" in block
    assert "[anomaly] base is behind main" in block
    assert "worktree or repo?  (choice/single)" in block
    assert "uncommitted: scratch.go" in block
    assert "unexpected" in block  # anomaly guidance to the agent


# --- E-1880: children are epic-only, and never duplicate a follow-up ---------

def test_render_hides_children_for_non_epic():
    """`--parent E-N --cleans-up E-N` — the pattern every handoff prescribes —
    lands a follow-up in BOTH lists. For a non-epic the Children line is a pure
    recap, so it is not rendered and the ids appear exactly once."""
    refs = [{"id": 1872, "status": "submitted", "relation": "cleaned_up_by"},
            {"id": 1873, "status": "submitted", "relation": "cleaned_up_by"}]
    facts = {"status": "unverified", "type": "todo", "landed": False,
             "successors": refs,
             "children": [{"id": r["id"], "status": r["status"]} for r in refs]}
    block = report_cmd._render_facts(facts, [], [], [])
    assert "Children" not in block
    assert block.count("E-1872") == 1
    assert block.count("E-1873") == 1


def test_render_epic_keeps_children_but_dedupes():
    """An epic handoff is explicitly asked to lead with the state of its
    children — so the line stays — but an id already listed as a follow-up is
    never repeated under it."""
    facts = {
        "status": "unverified", "type": "epic", "landed": False,
        "successors": [{"id": 1872, "status": "submitted", "relation": "cleaned_up_by"}],
        "children": [{"id": 1872, "status": "submitted"},
                     {"id": 1899, "status": "ready"}],
    }
    block = report_cmd._render_facts(facts, [], [], [])
    assert "Children: E-1899 [ready]" in block
    assert block.count("E-1872") == 1


def test_render_epic_all_children_filed_omits_the_line():
    """Dedupe leaving nothing behind must omit the label, not print an empty one."""
    facts = {
        "status": "unverified", "type": "epic", "landed": False,
        "successors": [{"id": 1872, "status": "submitted", "relation": "cleaned_up_by"}],
        "children": [{"id": 1872, "status": "submitted"}],
    }
    block = report_cmd._render_facts(facts, [], [], [])
    assert "Children" not in block


def test_render_missing_type_is_not_an_epic():
    """A task with no type_id reports type "" — treat it as a non-epic rather
    than crashing or leaking a Children recap."""
    facts = {"status": "unverified", "type": "", "landed": False,
             "successors": [], "children": [{"id": 1899, "status": "ready"}]}
    assert report_cmd._render_facts(facts, [], [], []).strip() == ""


# --- steer selection --------------------------------------------------------

def test_report_item_wraps_facts_in_steer(monkeypatch, capsys):
    monkeypatch.setattr(report_cmd, "_compute_facts",
                        lambda i: {"status": "unverified", "type": "todo", "landed": False,
                                   "successors": [{"id": 1772, "status": "ready",
                                                   "relation": "cleaned_up_by"}],
                                   "children": []})
    monkeypatch.setattr(report_cmd, "_compute_anomalies", lambda: [])
    report_cmd.report_item(1771, None)
    out = capsys.readouterr().out
    assert "Report the following to the user" in out  # steer header
    assert "Follow-ups you filed: E-1772 [ready]" in out
    assert "Status:" not in out


def test_report_item_empty_block_uses_steer_empty(monkeypatch, capsys):
    """Nothing to report → the steer-empty prompt, and never the `steer` header
    (which would introduce a fact block that isn't there)."""
    monkeypatch.setattr(report_cmd, "_compute_facts",
                        lambda i: {"status": "unverified", "type": "todo", "landed": False,
                                   "successors": [], "children": []})
    monkeypatch.setattr(report_cmd, "_compute_anomalies", lambda: [])
    report_cmd.report_item(1771, None)
    out = capsys.readouterr().out
    assert "Report the following to the user" not in out
    assert out.strip() == report_prompts.DEFAULTS[report_prompts.STEER_EMPTY]


def test_steer_empty_is_overridable(isolated_env, monkeypatch, capsys):
    """steer-empty is a registered prompt name, so it stays user-editable like
    the other three (ED-1531 Req 5)."""
    import json
    from endless import config
    (config.CONFIG_DIR / "report-prompts.jsonl").write_text(
        json.dumps({"name": "steer-empty", "text": "EMPTY-MARKER"}) + "\n"
    )
    monkeypatch.setattr(report_cmd, "_compute_facts",
                        lambda i: {"status": "unverified", "type": "todo", "landed": False,
                                   "successors": [], "children": []})
    monkeypatch.setattr(report_cmd, "_compute_anomalies", lambda: [])
    report_cmd.report_item(1771, None)
    assert capsys.readouterr().out.strip() == "EMPTY-MARKER"


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

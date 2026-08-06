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
             "successors": [], "children": None}
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
        "children": [{"id": 1800, "status": "assumed"}],
    }
    notes = [{"kind": "anomaly", "text": "base is behind main"}]
    questions = [{"text": "worktree or repo?", "type": "choice", "style": "single"}]
    block = report_cmd._render_sanctioned(facts, notes, questions, "just test")
    assert "Verify: `just test`" in block
    assert "Follow-ups you filed: E-1772 [unverified]" in block
    assert "Children: E-1800 [assumed]" in block
    assert "[anomaly] base is behind main" in block
    assert "worktree or repo?  (choice/single)" in block


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
    block = report_cmd._render_sanctioned(facts, [], [], None)
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
    block = report_cmd._render_sanctioned(facts, [], [], None)
    assert "Children: E-1899 [ready]" in block
    assert block.count("E-1872") == 1


def test_render_epic_all_children_filed_omits_the_line():
    """Dedupe leaving nothing behind must omit the label, not print an empty one."""
    facts = {
        "status": "unverified", "type": "epic", "landed": False,
        "successors": [{"id": 1872, "status": "submitted", "relation": "cleaned_up_by"}],
        "children": [{"id": 1872, "status": "submitted"}],
    }
    block = report_cmd._render_sanctioned(facts, [], [], None)
    assert "Children" not in block


def test_render_missing_type_is_not_an_epic():
    """A task with no type_id reports type "" — treat it as a non-epic rather
    than crashing or leaking a Children recap."""
    facts = {"status": "unverified", "type": "", "landed": False,
             "successors": [], "children": [{"id": 1899, "status": "ready"}]}
    assert report_cmd._render_sanctioned(facts, [], [], None).strip() == ""


# --- steer selection --------------------------------------------------------

@pytest.fixture(autouse=True)
def _no_checkpoint(monkeypatch):
    """Arming the Stop gate needs a live session + endless-go; these tests cover
    rendering, so the recording side-effect is stubbed out. `test_report_item_*`
    below asserts it is called with the right text."""
    monkeypatch.setattr(report_cmd, "_record_checkpoint", lambda text: None)


def _stub_facts(monkeypatch, successors=None, children=None, type_="todo"):
    monkeypatch.setattr(report_cmd, "_compute_facts",
                        lambda i: {"status": "unverified", "type": type_, "landed": False,
                                   "successors": successors or [], "children": children or []})
    monkeypatch.setattr(report_cmd, "_compute_anomalies", lambda: [])


def test_report_item_wraps_facts_in_steer(monkeypatch, capsys):
    _stub_facts(monkeypatch, successors=[{"id": 1772, "status": "ready",
                                          "relation": "cleaned_up_by"}])
    report_cmd.report_item(1771, None)
    out = capsys.readouterr().out
    assert "Relay the block between the markers" in out  # steer header
    assert report_prompts.BEGIN_MARKER in out
    assert report_prompts.END_MARKER in out
    assert "Follow-ups you filed: E-1772 [ready]" in out
    assert "Status:" not in out


def test_report_item_empty_block_uses_nothing_to_report(monkeypatch, capsys):
    """The empty case is no longer a separate steer telling the agent to compose
    a one-liner — it is a sanctioned string like any other, so the same markers
    and the same gate apply (E-1901)."""
    _stub_facts(monkeypatch)
    report_cmd.report_item(1771, None)
    out = capsys.readouterr().out
    assert "Relay the block between the markers" in out
    assert "Nothing to report." in out
    # And it is inside the block, so the equality gate has something to match.
    body = out.split(report_prompts.BEGIN_MARKER)[1].split(report_prompts.END_MARKER)[0]
    assert body.strip() == "Nothing to report."


def test_nothing_to_report_is_overridable(isolated_env, monkeypatch, capsys):
    """It stays a registered prompt name, so the wording is user-editable like
    the other three (ED-1531 Req 5)."""
    import json
    from endless import config
    (config.CONFIG_DIR / "report-prompts.jsonl").write_text(
        json.dumps({"name": "nothing-to-report", "text": "EMPTY-MARKER"}) + "\n"
    )
    _stub_facts(monkeypatch)
    report_cmd.report_item(1771, None)
    out = capsys.readouterr().out
    body = out.split(report_prompts.BEGIN_MARKER)[1].split(report_prompts.END_MARKER)[0]
    assert body.strip() == "EMPTY-MARKER"


def test_report_item_arms_the_gate_with_the_block_only(monkeypatch, capsys):
    """The recorded text must be the sanctioned block ALONE — not the steer that
    frames it, and not the agent-facing anomaly addendum. Recording either would
    gate against a string the user was never meant to receive, and every relay
    would bounce."""
    recorded = []
    monkeypatch.setattr(report_cmd, "_record_checkpoint", recorded.append)
    _stub_facts(monkeypatch, successors=[{"id": 1906, "status": "untriaged",
                                          "relation": "relates_to"}])
    monkeypatch.setattr(report_cmd, "_compute_anomalies", lambda: ["uncommitted: scratch.go"])
    report_cmd.report_item(1901, '{"verify":"esu && ./tests/tasks/e-1901-verify.sh"}')

    assert len(recorded) == 1
    assert recorded[0] == (
        "Verify: `esu && ./tests/tasks/e-1901-verify.sh`\n"
        "Follow-ups you filed: E-1906 [untriaged]"
    )
    out = capsys.readouterr().out
    assert "uncommitted: scratch.go" in out          # printed for the agent…
    assert "uncommitted" not in recorded[0]          # …but never sanctioned
    assert "Relay the block" not in recorded[0]      # steer is not the message


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

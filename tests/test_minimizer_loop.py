"""The minimizer's autoresearch loop (E-1975).

Hermetic: every model call is stubbed, and the DB is the suite's isolated one.
What that leaves provable is the loop's MECHANICS — the variant store's identity
and lineage, the mechanical vetoes, the paired-replay decision rule, the fetch
record's round trip, the bypass, and the shape of a paired presentation.

What it deliberately does NOT cover is judgment: whether the rewritten prompt
actually produces better replies, and whether the judge's fidelity score means
anything. No stub can fake either. That lives in `tests/tasks/e-1975-verify.sh`,
which runs the real model.
"""

import json

import pytest

from endless import (
    db,
    minimizer_fetch,
    minimizer_invariants,
    minimizer_judge,
    minimizer_optimizer,
    minimizer_store,
    report_cmd,
    report_prompts,
)


# --- the variant store -------------------------------------------------------

def test_hash_covers_all_three_axes():
    """Two bundles differing only in bypass threshold are different experiments.

    Hashing the prompt alone would collapse them, and an eval would then
    reference a bundle that never ran as recorded.
    """
    policy = {"fetches": []}
    base = minimizer_store.content_hash("p", policy, 256)
    assert base != minimizer_store.content_hash("p", policy, 512)
    assert base != minimizer_store.content_hash("p", {"fetches": [{"source": "x"}]}, 256)
    assert base != minimizer_store.content_hash("q", policy, 256)
    # Stable across calls, or lineage would fork on every write.
    assert base == minimizer_store.content_hash("p", policy, 256)


def test_saving_the_same_bundle_twice_is_one_row():
    """A generator that rediscovers the champion has produced no challenger, and
    the loop needs to SEE that rather than record a copy and replay a prompt
    against itself."""
    policy = {"fetches": []}
    a = minimizer_store.save_variant(
        task_type="", prompt_text="p", fetch_policy=policy, bypass_threshold=256)
    b = minimizer_store.save_variant(
        task_type="", prompt_text="p", fetch_policy=policy, bypass_threshold=256)
    assert a == b
    assert len(minimizer_store.list_variants()) == 1


def test_champion_seeds_itself_on_first_use():
    """A fresh install has no champion, and must not therefore have no prompt."""
    champ = minimizer_store.champion("todo")
    assert champ["prompt_text"] == report_prompts.DEFAULTS[report_prompts.MINIMIZE]
    assert champ["bypass_threshold"] == report_prompts.DEFAULT_BYPASS_THRESHOLD
    assert minimizer_store.champion_row("todo")["promoted_by"] == "seed"


def test_a_new_task_type_inherits_the_untyped_champion():
    """Splitting by task type must not mean every new type restarts from zero.

    That would trade a real cost today (every type relearning) for the
    speculative gain the split was for, which is backwards.
    """
    h = minimizer_store.save_variant(
        task_type="", prompt_text="tuned", fetch_policy={"fetches": []},
        bypass_threshold=300)
    minimizer_store.promote("", h, by="replay")

    champ = minimizer_store.champion("bugfix")
    assert champ["hash"] == h
    assert champ["prompt_text"] == "tuned"


def test_promotion_and_rollback_are_pointer_moves():
    """The property that makes auto-promotion safe: undoing one is one UPDATE,
    not a reconstruction from a diff."""
    seed = minimizer_store.champion("")
    child = minimizer_store.save_variant(
        task_type="", prompt_text="challenger", fetch_policy=seed["fetch_policy"],
        bypass_threshold=seed["bypass_threshold"], parent_hash=seed["hash"])
    minimizer_store.promote("", child, by="replay")
    assert minimizer_store.champion("")["hash"] == child

    ok, message = minimizer_optimizer.rollback("")
    assert ok, message
    assert minimizer_store.champion("")["hash"] == seed["hash"]
    # Both variants survive the rollback — nothing was rewritten.
    assert {v["hash"] for v in minimizer_store.list_variants()} == {seed["hash"], child}


def test_rollback_refuses_at_the_seed():
    """There is nowhere further back, and saying so beats silently no-opping."""
    minimizer_store.champion("")
    ok, message = minimizer_optimizer.rollback("")
    assert not ok
    assert "seed" in message


def test_untried_challengers_excludes_the_already_replayed():
    """A challenger that lost is answered. Re-running it spends a whole corpus
    slice on a settled question."""
    champ = minimizer_store.champion("")
    tried = minimizer_store.save_variant(
        task_type="", prompt_text="tried", fetch_policy=champ["fetch_policy"],
        bypass_threshold=256, parent_hash=champ["hash"])
    fresh = minimizer_store.save_variant(
        task_type="", prompt_text="fresh", fetch_policy=champ["fetch_policy"],
        bypass_threshold=256, parent_hash=champ["hash"])
    minimizer_store.record_eval(
        task_type="", challenger_hash=tried, champion_hash=champ["hash"],
        corpus_ids=[1], wins=0, losses=3, ties=0, vetoes=0, promoted=False,
        verdict="held", detail="")

    pending = minimizer_store.untried_challengers("", champ["hash"])
    assert [v["hash"] for v in pending] == [fresh]


# --- the mechanical vetoes ---------------------------------------------------

_TABLE = "| a | b |\n|---|---|\n| 1 | 2 |"
_CODE = "```go\nfunc main() {\n\tprintln(1)\n}\n```"


def test_intact_protected_content_passes():
    raw = f"Intro.\n\n{_TABLE}\n\nMiddle.\n\n{_CODE}\n\nRun `just test` to check."
    minimized = f"{_TABLE}\n\n{_CODE}\n\nRun `just test`."
    ok, detail, _ = minimizer_invariants.check(raw, minimized)
    assert ok, detail


def test_deleting_protected_content_whole_is_allowed():
    """Survive byte for byte, OR be absent entirely. Deleting a table the user
    did not need is a legitimate edit — the veto is against mangling."""
    raw = f"Intro.\n\n{_TABLE}\n\n{_CODE}"
    ok, detail, _ = minimizer_invariants.check(raw, "Intro.")
    assert ok, detail


def test_a_partially_reproduced_table_is_vetoed():
    raw = f"Intro.\n\n{_TABLE}"
    ok, _, violations = minimizer_invariants.check(raw, "Intro.\n\n| a | b |")
    assert not ok
    assert any("table" in v for v in violations)


def test_a_reformatted_code_block_is_vetoed():
    raw = f"Intro.\n\n{_CODE}"
    mangled = "```go\nfunc main() {\n    println(1)\n}\n```"
    ok, _, violations = minimizer_invariants.check(raw, f"Intro.\n\n{mangled}")
    assert not ok
    assert any("code block" in v for v in violations)


def test_an_altered_command_is_vetoed():
    """The dangerous case is not a dropped command but a plausible-looking one:
    the user sees an invocation and runs it, and it is not what they were given."""
    raw = "Verify with `./tests/tasks/e-1975-verify.sh --strict`."
    ok, _, violations = minimizer_invariants.check(
        raw, "Verify with `./tests/tasks/e-1975-verify.sh`.")
    assert not ok
    assert any("command" in v for v in violations)


def test_a_dropped_command_is_not_a_mechanical_violation():
    """Whether a command SHOULD have survived is judgment and belongs to the
    judge. Whether a surviving one is still the same command is arithmetic and
    belongs here."""
    raw = "Verify with `just test`. Also, the parser is fine."
    ok, detail, _ = minimizer_invariants.check(raw, "The parser is fine.")
    assert ok, detail


def test_an_empty_output_is_vetoed():
    ok, _, violations = minimizer_invariants.check("something", "   \n")
    assert not ok
    assert any("empty" in v for v in violations)


def test_a_rewrite_is_not_a_violation():
    """The check E-1953 could make and E-1975 cannot: `minimized ⊆ raw` as a
    subsequence died with deletion-only, and would now fire on every correct
    edit."""
    raw = "It is worth noting that the parser, fundamentally, handles nested quotes."
    ok, detail, _ = minimizer_invariants.check(raw, "The parser handles nested quotes.")
    assert ok, detail


def test_compression_saturates():
    """Past a point, cutting more stops being a benefit and starts being a risk
    to fidelity. A score that kept rewarding cuts would optimise toward the empty
    reply."""
    raw = "x" * 1000
    assert minimizer_invariants.compression(raw, "x" * 1000) == 0.0
    half = minimizer_invariants.compression(raw, "x" * 500)
    assert 0 < half < 1
    assert minimizer_invariants.compression(raw, "x" * 10) == 1.0
    assert minimizer_invariants.compression(raw, "x" * 1) == 1.0


# --- the fetch record --------------------------------------------------------

def test_context_round_trips_from_the_record():
    """Replay MUST rebuild context from the record. Re-fetching would give the
    challenger a different world than the champion saw, and a paired comparison
    across two worlds is not a comparison."""
    record = json.dumps([
        {"source": "task_plan", "ok": True, "chars": 4, "text": "plan"},
        {"source": "session_status", "ok": False, "error": "boom"},
        {"source": "recent_replies", "skipped": "no task claimed"},
    ])
    rebuilt = minimizer_fetch.context_from_record(record)
    assert "### task_plan\nplan" in rebuilt
    assert "session_status" not in rebuilt
    assert minimizer_fetch.context_from_record(None) == ""
    assert minimizer_fetch.context_from_record("not json") == ""


def test_an_unknown_source_is_recorded_and_skipped():
    """The optimizer writes policies, so a hallucinated source name must cost one
    weak variant — not every reply that turn."""
    context, record = minimizer_fetch.run_policy(
        {"fetches": [{"source": "no_such_thing", "when": "always"}]},
        task_id=None, session_id=None)
    assert context == ""
    assert json.loads(record)[0]["skipped"] == "unknown source"


def test_a_task_scoped_source_is_skipped_without_a_task():
    context, record = minimizer_fetch.run_policy(
        {"fetches": [{"source": "task_plan", "when": "task"}]},
        task_id=None, session_id=None)
    assert context == ""
    assert json.loads(record)[0]["skipped"] == "no task claimed"


def test_the_policy_budget_is_enforced():
    """A policy that pulls 40k characters buys a better dedup at a latency the
    user feels on every turn, so the ceiling is part of the tunable policy."""
    db.execute(
        "INSERT INTO projects (id, name, path) VALUES (1, 'p', '/tmp/p')")
    db.execute(
        "INSERT INTO sessions (id, session_id, project_id, state, summary) "
        "VALUES (7, 'uuid-7', 1, 'active', ?)", ("x" * 5000,))

    _, record = minimizer_fetch.run_policy(
        {"fetches": [{"source": "session_status", "when": "always"}], "max_chars": 50},
        task_id=None, session_id=7)
    entry = json.loads(record)[0]
    assert entry["ok"] and entry["chars"] <= 50


# --- the paired presentation and the bypass ----------------------------------

def _stub_model(monkeypatch, replies):
    """Return successive replies from the minimizer's model call."""
    seq = list(replies)

    def fake(prompt, **kw):
        import subprocess
        return subprocess.CompletedProcess(
            args=["claude"], returncode=0, stdout=seq.pop(0), stderr="")

    monkeypatch.setattr(report_cmd.internal_claude, "run_internal_claude", fake)


def test_a_short_draft_bypasses_the_minimizer(monkeypatch, tmp_path):
    """The row is still corpus. "We let this one through untouched" is exactly
    the evidence the threshold axis is tuned on — and at design time nothing had
    ever been under a threshold, so there is no evidence yet at all."""
    def boom(*a, **k):
        raise AssertionError("the model must not be called under the threshold")

    monkeypatch.setattr(report_cmd.internal_claude, "run_internal_claude", boom)
    monkeypatch.setattr(report_cmd, "_plan", lambda *a, **k: (None, None, "", ""))

    emitted, checkpoint = report_cmd._produce("short answer", "q?", None, None)
    assert emitted == "short answer"
    assert checkpoint["variants"][0]["bypassed"] is True


def test_a_long_draft_does_not_bypass(monkeypatch):
    monkeypatch.setattr(report_cmd, "_plan", lambda *a, **k: (None, None, "", ""))
    _stub_model(monkeypatch, ["minimized"])
    draft = "y" * (report_prompts.DEFAULT_BYPASS_THRESHOLD + 1)
    emitted, checkpoint = report_cmd._produce(draft, "q?", None, None)
    assert emitted == "minimized"
    assert checkpoint["variants"][0]["bypassed"] is False


def test_a_paired_turn_emits_both_options(monkeypatch):
    """One sanctioned output carrying both, because the Stop hook compares the
    FINAL message and a tool call happens before it — an agent cannot send an A/B
    block and then ask which won, since the reply ends the turn."""
    champ = {"hash": "aaa", "prompt_text": "champ {draft}", "bypass_threshold": 10,
             "fetch_policy": {"fetches": []}}
    chal = {"hash": "bbb", "prompt_text": "chal {draft}"}
    monkeypatch.setattr(report_cmd, "_plan", lambda *a, **k: (champ, chal, "ctx", "[]"))
    _stub_model(monkeypatch, ["reply A", "reply B"])

    emitted, checkpoint = report_cmd._produce("a long draft " * 30, "q?", None, None)

    assert "[Option A of B]" in emitted
    assert "[Option B of B]" in emitted
    assert "reply A" in emitted and "reply B" in emitted
    assert "$A" in emitted and "$B" in emitted
    assert "$MORE" in emitted and "$LESS" in emitted

    slots = [(v["slot"], v["sanctioned"], v["variant_hash"]) for v in checkpoint["variants"]]
    assert slots == [("A", "reply A", "aaa"), ("B", "reply B", "bbb")]
    # The emitted block is recorded separately from either variant: it is what
    # the agent owes, while the variants are what the judge scores.
    assert checkpoint["emitted"] == emitted


def test_a_long_option_is_previewed_not_duplicated_in_full():
    """A/B doubles what the user reads, and the minimizer exists to cut what they
    read. Previewing is the resolution, with `session turn` for the rest."""
    long_reply = "\n".join(f"line {i}" for i in range(40))
    block = report_cmd._pair_block(long_reply, "short", "")
    assert "line 0" in block
    assert "line 39" not in block
    assert "endless session turn A -p" in block
    # A short option is shown whole — truncating it would buy nothing.
    assert "short" in block


def test_the_calibration_notice_rides_on_the_pair():
    """ED-1556: low agreement raises the sample rate and is announced IN BAND.
    In band means in the reply the user is already reading."""
    block = report_cmd._pair_block("a", "b", "agreeing with you only 40% of the time")
    assert "40%" in block


# --- the replay decision rule ------------------------------------------------

def test_a_veto_beats_every_margin():
    """A challenger that violated an invariant is not one that scored slightly
    lower. An average is precisely the instrument that would let compression buy
    off a fabricated sentence."""
    promoted, why = minimizer_optimizer.decide(
        {"wins": 8, "losses": 0, "ties": 0, "vetoes": 1, "regressions": 0,
         "skipped": 0, "notes": []})
    assert not promoted
    assert "vetoed" in why


def test_a_fidelity_regression_beats_every_margin():
    promoted, why = minimizer_optimizer.decide(
        {"wins": 8, "losses": 0, "ties": 0, "vetoes": 0, "regressions": 1,
         "skipped": 0, "notes": []})
    assert not promoted
    assert "fidelity regressed" in why


def test_a_thin_margin_does_not_promote():
    """One net win out of eight is noise wearing a result's clothes."""
    promoted, why = minimizer_optimizer.decide(
        {"wins": 4, "losses": 3, "ties": 1, "vetoes": 0, "regressions": 0,
         "skipped": 0, "notes": []})
    assert not promoted
    assert "not clear of the champion" in why


def test_a_clear_win_promotes():
    promoted, why = minimizer_optimizer.decide(
        {"wins": 5, "losses": 1, "ties": 2, "vetoes": 0, "regressions": 0,
         "skipped": 0, "notes": []})
    assert promoted
    assert "promoted" in why


def test_no_comparable_items_is_not_a_promotion():
    promoted, why = minimizer_optimizer.decide(
        {"wins": 0, "losses": 0, "ties": 0, "vetoes": 0, "regressions": 0,
         "skipped": 8, "notes": []})
    assert not promoted


def _replay_with(monkeypatch, scores):
    """Drive replay() over one corpus row with hand-written scores.

    `scores` is (champion, challenger). Both model calls are stubbed, so what is
    under test is the comparison rule and nothing else.
    """
    champ_score, chal_score = scores
    monkeypatch.setattr(
        minimizer_optimizer, "_minimize_with", lambda variant, row, ctx: "output")

    seq = [champ_score, chal_score]
    monkeypatch.setattr(
        minimizer_optimizer, "_score", lambda row, ctx, text: seq.pop(0))

    champ = minimizer_store.champion("")
    row = {"id": 1, "raw_draft": "draft", "user_prompt": "q?",
           "sanctioned_text": "", "fetched_context": None, "variant_hash": "other"}
    return minimizer_optimizer.replay("", champ, [row])


def _score(*, invariants_ok=True, invented=False, fidelity=90, compression=0.5):
    return {"invariants_ok": invariants_ok, "invariant_detail": "mangled a table",
            "invented": invented, "fidelity": fidelity, "compression": compression}


def test_a_mechanical_violation_vetoes_absolutely(monkeypatch):
    """A reflowed table is a reflowed table whoever produced it. The champion
    doing the same elsewhere is no defense."""
    result = _replay_with(monkeypatch, (
        _score(invariants_ok=False), _score(invariants_ok=False)))
    assert result["vetoes"] == 1


def test_a_fabrication_the_champion_also_commits_is_not_a_veto(monkeypatch):
    """`invented` is a model's OPINION, and models over-report it on drafts that
    are themselves confused.

    Vetoing the challenger for a fabrication the champion committed on the same
    draft would judge the two by different rules on the axis where the judge is
    least reliable — and in practice makes promotion impossible, which turns the
    loop into an expensive no-op. Pairing is the instrument for exactly this: if
    both invent on the same item, the item is the explanation.
    """
    result = _replay_with(monkeypatch, (
        _score(invented=True, compression=0.4),
        _score(invented=True, compression=0.6)))
    assert result["vetoes"] == 0
    assert result["wins"] == 1


def test_a_fabrication_the_champion_avoided_is_a_veto(monkeypatch):
    """The direction that matters. Rewriting is what made fabrication possible,
    so a challenger that invents where the champion did not is a regression on
    the one axis deletion-only never had."""
    result = _replay_with(monkeypatch, (
        _score(invented=False), _score(invented=True, compression=0.9)))
    assert result["vetoes"] == 1
    assert result["wins"] == 0


def test_compression_only_counts_after_fidelity_holds(monkeypatch):
    """Maximize compression SUBJECT TO no fidelity regression — never instead
    of it."""
    result = _replay_with(monkeypatch, (
        _score(fidelity=90, compression=0.2),
        _score(fidelity=50, compression=0.99)))
    assert result["regressions"] == 1
    assert result["losses"] == 1
    assert result["wins"] == 0


def test_a_round_refuses_on_a_corpus_too_small_to_decide():
    """The honest answer for a fresh install, and it must not read as a failure."""
    result = minimizer_optimizer.run_round("")
    assert not result["acted"]
    assert "corpus too small" in result["reason"]


def test_a_generated_prompt_missing_a_placeholder_is_rejected(monkeypatch):
    """A prompt missing {draft} still reads like a fine instruction and would run
    — against no draft — producing confident output about nothing."""
    minimizer_store.champion("")
    reply = json.dumps({"note": "n", "prompt_text": "cut it {prompt} {context} {denylist}"})

    def fake(prompt, **kw):
        import subprocess
        return subprocess.CompletedProcess(
            args=["claude"], returncode=0, stdout=reply, stderr="")

    monkeypatch.setattr(minimizer_optimizer.internal_claude, "run_internal_claude", fake)
    assert minimizer_optimizer.generate("") is None


def test_a_generated_prompt_is_stored_with_its_lineage(monkeypatch):
    champ = minimizer_store.champion("")
    text = "edit {prompt} against {context} for {draft}, avoiding {denylist}"

    def fake(prompt, **kw):
        import subprocess
        return subprocess.CompletedProcess(
            args=["claude"], returncode=0,
            stdout=json.dumps({"note": "tighter", "prompt_text": text}), stderr="")

    monkeypatch.setattr(minimizer_optimizer.internal_claude, "run_internal_claude", fake)
    h = minimizer_optimizer.generate("")
    assert h is not None
    stored = minimizer_store.get_variant(h)
    assert stored["parent_hash"] == champ["hash"]
    assert stored["origin"] == "generated"
    assert stored["note"] == "tighter"


def test_the_seed_rotates_rather_than_randomising():
    """So the loop can say "each of these has been tried once" without a
    statistics argument. A random seed makes an unlucky run indistinguishable
    from an exhausted search."""
    seen = [minimizer_optimizer.next_seed()
            for _ in range(len(report_prompts.GRAMMAR_SEEDS))]
    assert seen == list(report_prompts.GRAMMAR_SEEDS)
    assert minimizer_optimizer.next_seed() == report_prompts.GRAMMAR_SEEDS[0]


# --- the judge ---------------------------------------------------------------

def test_a_verdict_needs_a_fidelity_score():
    """A reply that will not parse is no reply. Inventing defaults for the
    missing fields would put a fabricated score in the corpus under the guise of
    a measurement."""
    assert minimizer_judge.parse_verdict('{"fidelity": 80}') == {"fidelity": 80}
    assert minimizer_judge.parse_verdict('{"note": "hi"}') is None
    assert minimizer_judge.parse_verdict("no json here") is None
    assert minimizer_judge.parse_verdict(None) is None
    # A fence or a stray sentence around it is tolerated: a model asked for JSON
    # alone still occasionally frames it.
    assert minimizer_judge.parse_verdict(
        'Here you go:\n```json\n{"fidelity": 10}\n```') == {"fidelity": 10}


def _corpus_row(gate_id: int, **overrides) -> None:
    db.execute(
        "INSERT OR IGNORE INTO sessions (id, session_id, state) "
        "VALUES (1, 'u', 'active')")
    cols = {
        "id": gate_id, "session_id": 1, "kind_id": 2,
        "raw_draft": "a draft", "sanctioned_text": "a reply",
    }
    cols.update(overrides)
    keys = ", ".join(cols)
    marks = ", ".join("?" for _ in cols)
    db.execute(
        f"INSERT INTO session_gates ({keys}) VALUES ({marks})", tuple(cols.values()))


def test_a_judgment_made_after_the_user_reacted_is_not_blind(monkeypatch):
    """Hindsight wearing a prediction's clothes. Recording it as blind would let
    the calibration number flatter the judge indefinitely."""
    _corpus_row(1)
    db.execute(
        "INSERT INTO report_labels (gate_id, session_id, token) VALUES (1, 1, 'BLOAT')")

    monkeypatch.setattr(
        minimizer_judge, "call_judge",
        lambda prompt: '{"fidelity": 70, "will_complain": true}')
    assert minimizer_judge.judge_row(minimizer_store.corpus_row(1))

    row = db.query("SELECT blind FROM report_judgments WHERE gate_id = 1")[0]
    assert row["blind"] == 0


def test_agreement_counts_only_blind_scored_predictions(monkeypatch):
    _corpus_row(2)
    monkeypatch.setattr(
        minimizer_judge, "call_judge",
        lambda prompt: '{"fidelity": 90, "will_complain": false}')
    assert minimizer_judge.judge_row(minimizer_store.corpus_row(2))

    # No reaction yet: nothing to agree or disagree with.
    assert minimizer_store.agreement() == (0, 0)

    # The user complains, which the judge said they would not.
    db.execute(
        "INSERT INTO report_labels (gate_id, session_id, token) VALUES (2, 1, 'BLOAT')")
    assert minimizer_judge.observe_reactions() == 1
    assert minimizer_store.agreement() == (0, 1)


def test_calibration_says_not_yet_rather_than_a_number():
    """"Not calibrated" and "doing badly" are different statements and must not
    be confused: acting on three samples is how a loop talks itself into raising
    its own sample rate forever."""
    cal = minimizer_judge.calibration()
    assert cal["calibrated"] is False
    assert cal["rate"] is None
    assert cal["trusted"] is True


def test_approval_is_not_a_complaint(monkeypatch):
    """A corpus made only of complaints trains the minimizer toward verbosity,
    so approval has to register as an outcome rather than as silence."""
    _corpus_row(3)
    monkeypatch.setattr(
        minimizer_judge, "call_judge",
        lambda prompt: '{"fidelity": 95, "will_complain": false}')
    minimizer_judge.judge_row(minimizer_store.corpus_row(3))
    db.execute(
        "INSERT INTO report_labels (gate_id, session_id, token) VALUES (3, 1, 'GOOD')")
    minimizer_judge.observe_reactions()
    assert minimizer_store.agreement() == (1, 1)


# --- the config switches -----------------------------------------------------

@pytest.mark.parametrize("body,enabled,optimizer", [
    ({}, True, True),
    ({"minimizer": {"enabled": False}}, False, True),
    ({"minimizer": {"optimizer": False}}, True, False),
    ({"minimizer": False}, False, False),
    ({"report_gate": False}, False, True),
    ({"report_gate": False, "minimizer": {"enabled": True}}, True, True),
])
def test_the_switches_and_the_legacy_key(tmp_path, body, enabled, optimizer):
    """The old scalar keeps being read because a rename that silently re-enables
    a gate a project switched off is the one migration failure the user cannot
    see."""
    from endless import config
    proj = tmp_path / "proj"
    (proj / ".endless").mkdir(parents=True)
    (proj / ".endless" / "config.json").write_text(json.dumps(body))

    cfg = config.project_minimizer_config(proj)
    assert cfg["enabled"] is enabled
    assert cfg["optimizer"] is optimizer

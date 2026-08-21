"""The judge: scores every turn, and is itself scored against the user (E-1975).

The judge exists because human labels are sparse and always will be. The user is
a SENSOR in the flow of work, not an annotator (ED-1556) — they label when
something annoyed them and otherwise say nothing, which is the right trade for
their attention and a hopeless basis for evaluating a prompt over a corpus. So
the judge scores every turn, labelled or not, from what the minimizer itself saw:
the user's request, the material they already had, the draft, and the reply.

**Human labels do not score prompts. They calibrate this judge.** The judge
commits to a PREDICTION before the user reacts — will they complain, and if the
turn was paired, which will they pick — and rolling prediction-vs-outcome
agreement is the loop's honesty check on itself. That inversion is what makes a
sparse signal sufficient: twenty labels cannot rank two prompts over a corpus,
but they can say whether the instrument ranking them is any good.

Per ED-1556, sagging agreement RAISES the A/B sample rate and says so in band. It
never halts promotion silently. A loop that quietly stopped improving because it
had lost confidence in its own judge would be indistinguishable, from outside,
from a loop that had converged.

Blindness is recorded, not assumed. A sweep that judges an old row whose labels
already landed is hindsight wearing a prediction's clothes, so those rows are
written with blind=0 and excluded from the calibration number.
"""

from __future__ import annotations

import json
import re
import subprocess

from endless import (
    db,
    internal_claude,
    minimizer_fetch,
    minimizer_invariants,
    minimizer_store,
    report_prompts,
)

# Same model and effort as the minimizer itself, deliberately. A judge weaker
# than the thing it judges cannot see the failures that matter — spotting an
# invented sentence inside a fluent rewrite is the hardest read in the loop, and
# it is exactly what a smaller model returns a confident "fine" on.
_MODEL = "sonnet"
_EFFORT = "medium"
_TIMEOUT = 180

# Below this rolling agreement the judge is not trusted to stand in for the user,
# so the loop asks the user directly more often.
#
# 0.6 rather than 0.5. At 0.5 a binary predictor is a coin, and a loop that waits
# for its judge to become literally worthless before reacting has already spent
# many rounds promoting on noise.
AGREEMENT_FLOOR = 0.6

# Agreement is meaningless on three samples. Below this the loop reports "not yet
# calibrated" rather than a number, which is a different statement from "doing
# badly" and must not be confused with it.
AGREEMENT_MIN_SAMPLES = 8


def call_judge(prompt: str) -> str | None:
    """Run the judge's model call, or None on any failure.

    Fails OPEN, unlike the minimizer. The minimizer fails closed because a green
    light that means nothing would silently void the enforcement; the judge fails
    open because an unreachable judge should leave a row unjudged for the next
    sweep, not block the loop or write a fabricated score. A missing judgment is
    visible in `minimizer status`; a guessed one is not.
    """
    try:
        result = internal_claude.run_internal_claude(
            prompt, model=_MODEL, effort=_EFFORT, timeout=_TIMEOUT
        )
    except (subprocess.TimeoutExpired, FileNotFoundError, OSError):
        return None
    if result.returncode != 0:
        return None
    return result.stdout


_JSON_RE = re.compile(r"\{.*\}", re.DOTALL)


def parse_json_object(text: str | None) -> dict | None:
    """Pull one JSON object out of a model reply.

    Tolerant of a code fence or a stray sentence around it, because a model asked
    for JSON alone still occasionally frames it. Not tolerant of anything else: a
    reply that will not parse is no reply, and inventing defaults for the missing
    fields would put a fabricated score in the corpus under the guise of a
    measurement.
    """
    if not text:
        return None
    m = _JSON_RE.search(text)
    if not m:
        return None
    try:
        obj = json.loads(m.group(0))
    except (TypeError, ValueError):
        return None
    return obj if isinstance(obj, dict) else None


def parse_verdict(text: str | None) -> dict | None:
    """A parsed judge verdict, or None. `fidelity` is what makes it a verdict."""
    obj = parse_json_object(text)
    if obj is None or "fidelity" not in obj:
        return None
    return obj


def _reaction(row: dict) -> tuple[str | None, bool, bool]:
    """(actual_pick, complained, reacted) for a corpus row.

    A pick counts as a reaction on its own: choosing B IS the user telling us
    something about A, which is the entire reason paired turns exist.
    """
    labels = minimizer_store.labels_for(row["id"])
    pick = None
    if row.get("pair_id"):
        picked = [r for r in minimizer_store.pair_rows(row["pair_id"]) if r["picked"]]
        if picked:
            pick = picked[0].get("pair_slot")
    complained = any(l["token"] not in ("GOOD", "A", "B") for l in labels)
    return pick, complained, bool(labels) or pick is not None


def _paired_half(row: dict) -> tuple[str, str]:
    """(the other option's text, this row's slot) for a paired turn, else ("", "").

    A pick prediction is unanswerable from one side. Asking the judge which
    option the user will choose while showing it only one is not a hard question,
    it is an incoherent one — and its answer would still be scored against the
    real pick, quietly filling the calibration window with noise.
    """
    pair_id = row.get("pair_id")
    if not pair_id:
        return "", ""
    others = [r for r in minimizer_store.pair_rows(pair_id) if r["id"] != row["id"]]
    if not others:
        return "", ""
    other = others[0]
    label = (other.get("pair_slot") or "?").upper()
    return f"(option {label})\n{other.get('sanctioned_text') or ''}", \
        (row.get("pair_slot") or "?").upper()


def judge_row(row: dict) -> bool:
    """Score one corpus row and persist the judgment. True when one was written."""
    raw = row.get("raw_draft") or ""
    minimized = row.get("sanctioned_text") or ""
    if not raw or not minimized:
        return False

    context = minimizer_fetch.context_from_record(row.get("fetched_context"))
    ok, detail, _ = minimizer_invariants.check(raw, minimized)
    ratio = minimizer_invariants.compression(raw, minimized)

    # Blindness is decided BEFORE the model call, from the state of the world at
    # the moment the prediction is made.
    _, _, reacted = _reaction(row)
    blind = not reacted

    alternative, slot = _paired_half(row)

    prompts = report_prompts.load_prompts()
    verdict = parse_verdict(call_judge(report_prompts.build_judge_prompt(
        prompts, row.get("user_prompt") or "", raw, minimized, context,
        alternative=alternative, slot=slot,
    )))
    if verdict is None:
        return False

    lost = verdict.get("lost") or []
    invented = verdict.get("invented") or []
    redundant = verdict.get("redundant") or []
    fidelity_detail = json.dumps(
        {"lost": lost, "invented": invented, "redundant": redundant},
        ensure_ascii=False,
    )

    minimizer_store.record_judgment(
        gate_id=row["id"],
        variant_hash=row.get("variant_hash"),
        invariants_ok=ok,
        invariant_detail=detail,
        invented=bool(invented),
        fidelity=clamp_score(verdict.get("fidelity")),
        fidelity_detail=fidelity_detail,
        compression=ratio,
        # The judge is only asked to predict a pick where a pick is possible.
        # Asking it to guess one on an unpaired turn would fill the calibration
        # window with predictions about an event that cannot occur.
        predicted_pick=as_slot(verdict.get("predicted_pick")) if row.get("pair_id") else None,
        predicted_flag=bool(verdict.get("will_complain")),
        blind=blind,
    )
    return True


def clamp_score(value) -> int | None:
    try:
        return max(0, min(100, int(value)))
    except (TypeError, ValueError):
        return None


def as_slot(value) -> str | None:
    if isinstance(value, str) and value.strip().upper() in ("A", "B"):
        return value.strip().upper()
    return None


def observe_reactions(limit: int = 100) -> int:
    """Close the loop on predictions whose user reaction has since landed.

    Separate from judging because the two happen at different times by design:
    the prediction is made when the reply is sent, the outcome arrives whenever
    the user next speaks — or never, which is itself the common case and is not a
    failure.
    """
    rows = db.query(
        "SELECT j.gate_id FROM report_judgments j "
        "WHERE j.agreed IS NULL AND j.blind = 1 "
        "ORDER BY j.id DESC LIMIT ?",
        (limit,),
    )
    closed = 0
    for r in rows:
        row = minimizer_store.corpus_row(r["gate_id"])
        if row is None:
            continue
        pick, complained, reacted = _reaction(row)
        if not reacted:
            continue
        minimizer_store.record_reaction(row["id"], actual_pick=pick, actual_flag=complained)
        closed += 1
    return closed


def calibration() -> dict:
    """The judge's rolling agreement with the user, and what follows from it."""
    agreed, total = minimizer_store.agreement()
    if total < AGREEMENT_MIN_SAMPLES:
        return {
            "agreed": agreed,
            "total": total,
            "rate": None,
            "calibrated": False,
            "trusted": True,
        }
    rate = agreed / total
    return {
        "agreed": agreed,
        "total": total,
        "rate": rate,
        "calibrated": True,
        "trusted": rate >= AGREEMENT_FLOOR,
    }


def sweep(limit: int = 8) -> dict:
    """One judge pass: score what is unjudged, then observe what has landed."""
    judged = 0
    for row in minimizer_store.unjudged_rows(limit):
        if judge_row(row):
            judged += 1
    closed = observe_reactions()
    cal = calibration()
    _apply_calibration(cal)
    return {"judged": judged, "reactions": closed, "calibration": cal}


def _apply_calibration(cal: dict) -> None:
    """Raise the A/B sample rate when the judge has stopped tracking the user.

    Raising the rate is the correct response and a nudge in the user's direction
    is not: the loop does not need the user to fix the judge, it needs more
    counterfactuals to fix it with, and pairing is what produces those. The
    announcement rides on the pair itself (the pair block itself), so the
    user is told why the pairs got more frequent in the same breath as seeing
    one — rather than in a log they would have to go looking for.
    """
    if not cal["calibrated"] or cal["trusted"]:
        minimizer_store.state_set("calibration_notice", "")
        return
    rate = minimizer_store.ab_rate()
    # A rate of exactly zero is the user having said `$LESS` until the pairs
    # stopped. That is an instruction, not a setting the loop may overrule
    # because it would like more data — so the raise skips it, and only the
    # announcement survives. The alternative silently reverses something the user
    # asked for, in the one mechanism whose whole premise is that they are the
    # ground truth.
    #
    # Not a ladder step, deliberately. `$MORE` and `$LESS` move rungs because the
    # user is answering a question; this is the loop asking for more
    # counterfactuals, so it doubles and caps rather than pretending to be an
    # answer somebody gave.
    if 0 < rate < 0.4:
        minimizer_store.state_set(
            minimizer_store.STATE_AB_RATE, str(min(0.4, max(0.1, rate * 2))))
    minimizer_store.state_set(
        "calibration_notice",
        f"agreeing with you only {cal['rate']:.0%} of the time, so these are "
        "appearing more often until it catches up",
    )

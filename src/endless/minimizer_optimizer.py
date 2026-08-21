"""The optimizer: generate variants, replay them paired, promote by pointer.

Three things this file is careful about, each of which has an obvious wrong
version that a reasonable person would write first.

**Variants are GENERATED, not hand-written.** An agent writing challengers writes
the ones it can already imagine, which is the same distribution the champion came
from. Seeding from published controlled-English grammars (report_prompts.
GRAMMAR_SEEDS) puts decades of somebody else's evidence into the search instead,
and the generator is told to diverge from its seed rather than comply with it.

**Deciding is paired replay over a FROZEN corpus; monitoring is not deciding.**
Rolling metrics over live traffic answer "are we drifting?" — an alarm, and one
confounded by corpus drift, because a rolling mean improves when the work gets
easier. Only a paired replay may gate a promotion: the same drafts, the same
fetched context, both prompts, so item difficulty cancels. Replay serves context
from the corpus record and never re-fetches, or the two prompts would be
answering different questions.

**Scoring is vetoes, not a weighted average.** A challenger that violates one
invariant, or invents one sentence, is not a challenger that scored slightly
lower — it is disqualified, at any compression. An average is precisely the
instrument that would let compression buy off a fabricated sentence, and the
whole reason a self-scored compression target is a LEGITIMATE variant to try is
that invariants are enforced somewhere the prompt cannot reach.
"""

from __future__ import annotations

import json
import subprocess

from endless import (
    db,
    internal_claude,
    minimizer_fetch,
    minimizer_invariants,
    minimizer_judge,
    minimizer_store,
    report_prompts,
)

_MODEL = "sonnet"
_EFFORT = "medium"
_TIMEOUT = 240

# How many real turns one replay is decided on.
#
# Small, and unapologetically so. Every item costs up to four model calls, and
# the alternative to a small paired sample is not a large one — it is no
# evaluation at all, because a round nobody can afford never runs. Pairing is
# what makes eight items informative: both prompts face the same eight drafts, so
# the comparison does not have to average away item difficulty.
CORPUS_LIMIT = 8

# A challenger must clear the champion by this margin in paired wins. One net win
# out of eight is noise wearing a result's clothes.
WIN_MARGIN = 2

# Fidelity points a challenger may drop on an item before it counts as a
# regression. The judge is a model scoring 0-100; treating a 3-point difference
# as meaningful would make the veto fire on rounding.
FIDELITY_TOLERANCE = 5

# How long between optimize rounds. The tick that fires this runs every ten
# minutes and judges every time; replaying is the expensive half and wants to be
# rare.
ROUND_INTERVAL_HOURS = 6


# --- generation --------------------------------------------------------------

def evidence(task_type: str, limit: int = 20) -> str:
    """What the champion is getting wrong lately, as plain text for the generator.

    Drawn from the judge's own structured complaints (`lost` / `invented` /
    `redundant`) and from the words the USER used, unaltered. The user's tokens go
    in verbatim rather than summarised: `$JARGON` is a more useful instruction to
    a prompt writer than "the user expressed dissatisfaction with terminology",
    and the whole point of a free vocabulary is that the word they chose carries
    information.
    """
    rows = db.query(
        "SELECT j.fidelity, j.compression, j.fidelity_detail "
        "FROM report_judgments j "
        "JOIN session_gates g ON g.id = j.gate_id "
        "WHERE COALESCE(g.task_type,'') = ? OR ? = '' "
        "ORDER BY j.id DESC LIMIT ?",
        (task_type, task_type, limit),
    )
    if not rows:
        return "No judgments recorded yet — this is the first round."

    fid = [r["fidelity"] for r in rows if r["fidelity"] is not None]
    comp = [r["compression"] for r in rows if r["compression"] is not None]
    lost: list[str] = []
    invented: list[str] = []
    redundant: list[str] = []
    for r in rows:
        try:
            detail = json.loads(r["fidelity_detail"] or "{}")
        except (TypeError, ValueError):
            continue
        lost += [str(x) for x in (detail.get("lost") or [])]
        invented += [str(x) for x in (detail.get("invented") or [])]
        redundant += [str(x) for x in (detail.get("redundant") or [])]

    labels = db.query(
        "SELECT token, COALESCE(span, note, '') AS said FROM report_labels "
        "ORDER BY id DESC LIMIT 20"
    )

    parts = [f"Judged turns: {len(rows)}"]
    if fid:
        parts.append(f"Mean fidelity: {sum(fid) / len(fid):.0f}/100")
    if comp:
        parts.append(f"Mean compression score: {sum(comp) / len(comp):.2f}")
    if lost:
        parts.append("Things the user needed and lost:\n  - " + "\n  - ".join(lost[:8]))
    if invented:
        parts.append("Claims fabricated by the editor (worst failure):\n  - "
                     + "\n  - ".join(invented[:8]))
    if redundant:
        parts.append("Material left in that the user already had:\n  - "
                     + "\n  - ".join(redundant[:8]))
    if labels:
        parts.append("What the user said, in their words:\n  - " + "\n  - ".join(
            f"${r['token']} {r['said']}".strip() for r in labels[:10]
        ))
    return "\n\n".join(parts)


def next_seed() -> str:
    """The next controlled-English seed, in rotation.

    Rotation rather than random choice, so the loop can say "each of these has
    been tried once" without a statistics argument. A random seed makes an
    unlucky run indistinguishable from an exhausted search.
    """
    seeds = report_prompts.GRAMMAR_SEEDS
    try:
        idx = int(minimizer_store.state_get(minimizer_store.STATE_SEED_INDEX, "0") or "0")
    except ValueError:
        idx = 0
    minimizer_store.state_set(minimizer_store.STATE_SEED_INDEX, str((idx + 1) % len(seeds)))
    return seeds[idx % len(seeds)]


_REQUIRED_PLACEHOLDERS = ("{prompt}", "{context}", "{draft}", "{denylist}")


def generate(task_type: str) -> str | None:
    """Write one challenger to the current champion. Returns its hash, or None.

    The placeholder check is not a formality. A prompt missing `{draft}` still
    reads like a fine instruction and would run — against no draft — producing
    confident output about nothing. Rejecting it here costs one wasted generation;
    letting it through would cost a replay round and could promote it.
    """
    champ = minimizer_store.champion(task_type)
    prompts = report_prompts.load_prompts()
    prompt = report_prompts.build_generate_prompt(
        prompts, champ["prompt_text"], evidence(task_type), next_seed()
    )
    try:
        result = internal_claude.run_internal_claude(
            prompt, model=_MODEL, effort=_EFFORT, timeout=_TIMEOUT
        )
    except (subprocess.TimeoutExpired, FileNotFoundError, OSError):
        return None
    if result.returncode != 0:
        return None

    verdict = minimizer_judge.parse_json_object(result.stdout)
    if verdict is None:
        return None
    text = verdict.get("prompt_text")
    if not isinstance(text, str) or not text.strip():
        return None
    if any(p not in text for p in _REQUIRED_PLACEHOLDERS):
        return None

    h = minimizer_store.save_variant(
        task_type=task_type,
        prompt_text=text,
        fetch_policy=champ["fetch_policy"],
        bypass_threshold=champ["bypass_threshold"],
        parent_hash=champ["hash"],
        origin="generated",
        note=str(verdict.get("note") or "")[:400],
    )
    return h if h != champ["hash"] else None


# --- paired replay -----------------------------------------------------------

def _minimize_with(variant: dict, row: dict, context: str) -> str | None:
    """Run one variant's prompt over one corpus row. None on any model failure."""
    prompts = report_prompts.load_prompts()
    prompt = report_prompts.build_minimize_prompt(
        prompts,
        row.get("user_prompt") or "",
        row.get("raw_draft") or "",
        context,
        minimize_text=variant["prompt_text"],
    )
    try:
        result = internal_claude.run_internal_claude(
            prompt, model=_MODEL, effort=_EFFORT, timeout=_TIMEOUT
        )
    except (subprocess.TimeoutExpired, FileNotFoundError, OSError):
        return None
    if result.returncode != 0:
        return None
    out = result.stdout.strip()
    return out or None


def _score(row: dict, context: str, minimized: str) -> dict | None:
    """Invariant veto + judged fidelity + compression for one output."""
    ok, detail, _ = minimizer_invariants.check(row.get("raw_draft") or "", minimized)
    verdict = minimizer_judge.parse_verdict(minimizer_judge.call_judge(
        report_prompts.build_judge_prompt(
            report_prompts.load_prompts(),
            row.get("user_prompt") or "",
            row.get("raw_draft") or "",
            minimized,
            context,
        )
    ))
    if verdict is None:
        return None
    return {
        "invariants_ok": ok,
        "invariant_detail": detail,
        "invented": bool(verdict.get("invented")),
        "fidelity": minimizer_judge.clamp_score(verdict.get("fidelity")) or 0,
        "compression": minimizer_invariants.compression(row.get("raw_draft") or "", minimized),
    }


def replay(task_type: str, challenger: dict, corpus: list[dict]) -> dict:
    """Run champion and challenger over the same frozen rows and compare.

    The champion's output is REUSED where the row was produced by the champion
    itself: that text is what the user actually received, so re-minimizing it
    would replace the real datum with a re-roll of the same non-deterministic
    call. Half the model calls, and the more honest half at that.
    """
    champ = minimizer_store.champion(task_type)
    wins = losses = ties = vetoes = regressions = 0
    skipped = 0
    notes: list[str] = []

    for row in corpus:
        context = minimizer_fetch.context_from_record(row.get("fetched_context"))

        if row.get("variant_hash") == champ["hash"] and row.get("sanctioned_text"):
            champ_text = row["sanctioned_text"]
        else:
            champ_text = _minimize_with(champ, row, context)
        chal_text = _minimize_with(challenger, row, context)
        if not champ_text or not chal_text:
            skipped += 1
            continue

        champ_score = _score(row, context, champ_text)
        chal_score = _score(row, context, chal_text)
        if champ_score is None or chal_score is None:
            skipped += 1
            continue

        # Two vetoes, applied differently, and the difference is the whole
        # reason replay is PAIRED.
        #
        # The mechanical invariants are arithmetic: a reflowed table is a
        # reflowed table whoever produced it, so a challenger that mangles one is
        # out absolutely, and the champion doing the same on another item is no
        # defense.
        #
        # `invented` is a MODEL'S OPINION, and models over-report it on drafts
        # that are themselves confused. Vetoing the challenger for a fabrication
        # the champion committed on the same draft would judge the two by
        # different rules on the one axis where the judge is least reliable — and
        # in practice it makes promotion impossible, which turns the loop into an
        # expensive no-op. Pairing is precisely the instrument for this: if both
        # invent on the same item, the item is the explanation.
        if not chal_score["invariants_ok"]:
            vetoes += 1
            notes.append(f"row {row['id']}: {chal_score['invariant_detail']}")
            continue
        if chal_score["invented"] and not champ_score["invented"]:
            vetoes += 1
            notes.append(f"row {row['id']}: fabricated a claim the champion did not")
            continue

        if chal_score["fidelity"] < champ_score["fidelity"] - FIDELITY_TOLERANCE:
            regressions += 1
            losses += 1
            notes.append(
                f"row {row['id']}: fidelity {chal_score['fidelity']} vs "
                f"{champ_score['fidelity']}"
            )
            continue

        if chal_score["compression"] > champ_score["compression"] + 0.02:
            wins += 1
        elif chal_score["compression"] < champ_score["compression"] - 0.02:
            losses += 1
        else:
            ties += 1

    return {
        "wins": wins,
        "losses": losses,
        "ties": ties,
        "vetoes": vetoes,
        "regressions": regressions,
        "skipped": skipped,
        "notes": notes,
    }


def decide(result: dict) -> tuple[bool, str]:
    """Promote or not, and say why in one line.

    Order is the design's order and it is not interchangeable: veto on
    invariants, veto on fidelity regression, THEN maximize compression. Reading
    the compression margin first and treating a veto as a penalty would be the
    weighted average this whole design refuses.
    """
    if result["vetoes"]:
        return False, f"vetoed: {result['vetoes']} invariant/fabrication violation(s)"
    if result["regressions"]:
        return False, f"vetoed: fidelity regressed on {result['regressions']} item(s)"
    scored = result["wins"] + result["losses"] + result["ties"]
    if scored == 0:
        return False, "no comparable items"
    margin = result["wins"] - result["losses"]
    if margin < WIN_MARGIN:
        return False, (
            f"not clear of the champion: {result['wins']}W-{result['losses']}L-"
            f"{result['ties']}T (needs +{WIN_MARGIN})"
        )
    return True, (
        f"promoted: {result['wins']}W-{result['losses']}L-{result['ties']}T, "
        "no vetoes, no fidelity regression"
    )


def run_round(task_type: str = minimizer_store.NO_TASK_TYPE) -> dict:
    """One optimize round: ensure a challenger exists, replay it, decide.

    Returns a dict the CLI renders, and it always says what it did — including
    "nothing, because there is no corpus yet", which is the honest answer for a
    fresh install and must not be mistaken for a failure.
    """
    champ = minimizer_store.champion(task_type)
    corpus = minimizer_store.frozen_corpus(task_type, CORPUS_LIMIT)
    if len(corpus) < 3:
        return {
            "task_type": task_type,
            "acted": False,
            "reason": f"corpus too small to decide anything ({len(corpus)} replayable rows)",
        }

    pending = minimizer_store.untried_challengers(task_type, champ["hash"], limit=1)
    if not pending:
        h = generate(task_type)
        if h is None:
            return {"task_type": task_type, "acted": False,
                    "reason": "no usable challenger was generated"}
        pending = [minimizer_store.get_variant(h)]

    challenger = pending[0]
    result = replay(task_type, challenger, corpus)
    promoted, verdict = decide(result)

    minimizer_store.record_eval(
        task_type=task_type,
        challenger_hash=challenger["hash"],
        champion_hash=champ["hash"],
        corpus_ids=[r["id"] for r in corpus],
        wins=result["wins"],
        losses=result["losses"],
        ties=result["ties"],
        vetoes=result["vetoes"],
        promoted=promoted,
        verdict=verdict,
        detail="\n".join(result["notes"])[:4000],
    )
    if promoted:
        minimizer_store.promote(
            task_type, challenger["hash"], by="replay", note=verdict
        )
    return {
        "task_type": task_type,
        "acted": True,
        "challenger": challenger["hash"],
        "champion": champ["hash"],
        "promoted": promoted,
        "verdict": verdict,
        **result,
    }


def rollback(task_type: str) -> tuple[bool, str]:
    """Point the champion back at its parent.

    This is the whole of rollback, and it is why removing the user's approval
    step is safe rather than reckless: an auto-promotion that turns out badly is
    undone by moving one pointer, not by reconstructing a prompt from a diff or
    reverting a commit that has other things in it.
    """
    champ = minimizer_store.champion(task_type)
    parent = champ.get("parent_hash")
    if not parent:
        return False, f"the champion for {task_type or '(untyped)'} is the seed; nothing to roll back to"
    if minimizer_store.get_variant(parent) is None:
        return False, f"parent {parent} is missing from the variant store"
    minimizer_store.promote(task_type, parent, by="rollback",
                            note=f"rolled back from {champ['hash']}")
    return True, f"champion for {task_type or '(untyped)'} rolled back to {parent}"


def reseed(task_type: str) -> tuple[bool, str]:
    """Promote a variant built from the CURRENT shipped defaults.

    Without this a defect fix in the shipped prompt is undeliverable. The
    champion is a pointer into a content-addressed store; editing
    report_prompts.py changes what a FRESH install starts from and nothing else,
    so any machine where the loop has already promoted once keeps running the
    old text forever. That is correct while the seed is merely being improved on
    — the loop's whole job is to beat it — and wrong the moment the seed is
    found to be WRONG, because every descendant was generated from the flawed
    text and inherits the flaw.

    Deliberately manual. Auto-adopting a new seed would silently discard
    everything the loop had learned, on nothing more than someone editing a
    string; `minimizer status` reports the divergence instead, and a human
    decides. Rollback still works afterwards: the reseeded variant records no
    parent, so it is a floor rather than a link in the old lineage.
    """
    current = minimizer_store.champion(task_type)
    fresh = minimizer_store.seed_variant(task_type)
    if fresh["hash"] == current["hash"]:
        return False, f"already running the shipped default ({fresh['hash']})"
    minimizer_store.promote(
        task_type, fresh["hash"], by="reseed",
        note=f"adopted the shipped default, replacing {current['hash']}")
    return True, f"{current['hash']} -> {fresh['hash']} (shipped default adopted)"


def champion_diverges(task_type: str) -> str | None:
    """The shipped default's hash when the champion is not it, else None.

    Reported by `minimizer status` so a machine running a tuned prompt says so,
    and so a shipped fix that has not been adopted is visible rather than silent.
    """
    shipped = minimizer_store.content_hash(
        report_prompts.load_prompts()[report_prompts.MINIMIZE],
        report_prompts.DEFAULT_FETCH_POLICY,
        report_prompts.DEFAULT_BYPASS_THRESHOLD,
    )
    return None if minimizer_store.champion(task_type)["hash"] == shipped else shipped

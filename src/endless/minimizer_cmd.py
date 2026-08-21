"""`endless minimizer` — read and drive the autoresearch loop (E-1975).

The loop runs itself. Nothing here has to be typed for it to work: the job runner
fires `minimizer run` on a tick, the judge scores what arrived, and a round
promotes or does not. These verbs exist for the two things automation cannot do —
letting a human SEE what the loop believes, and letting them undo a promotion
they disagree with.

`status` is the one to read. It reports the earliest observable that the loop is
learning anything: keep-ratio by draft size. At design time the 2k+ bucket sat at
0.86 while 512-2k drafts were near 0.62-0.67 — a deleting editor keeps MORE of
the worst drafts, because a long draft is long by repetition and repetition is
not deletable span by span. The 2k+ figure drifting down toward the others is the
cheapest early sign ED-1557's rewriting objective has taken hold.

Watch it; do not steer by it. It is a rolling metric over live traffic, which
makes it an alarm and never a verdict — only paired replay over a frozen corpus
may gate a promotion, because a rolling mean improves whenever the work gets
easier.
"""

from __future__ import annotations

import datetime as dt
import json

import click

from endless import db, minimizer_judge, minimizer_optimizer, minimizer_store

_BUCKETS = (
    ("<256", 0, 256),
    ("256-512", 256, 512),
    ("512-1k", 512, 1024),
    ("1k-2k", 1024, 2048),
    ("2k+", 2048, 10**9),
)


def _keep_ratios() -> list[tuple[str, int, float | None]]:
    """(bucket, n, mean keep-ratio) over every corpus row that has both halves.

    A/B turns are handled carefully, because the obvious arithmetic is wrong in
    two different ways. The doubled total is NEVER formed: a pair is two
    minimizations of ONE draft, and charging the minimizer for their sum would
    make the experiment look like the prompt getting worse. And the pair is
    averaged into ONE sample rather than contributing two, so a draft does not
    get double weight in the mean for having been experimented on.

    The doubling is the cost of the experiment. It is fair to spend because it is
    opt-out: a user who tunes the minimizer to their satisfaction turns the
    optimizer off and never sees a pair again.
    """
    rows = db.query(
        "SELECT COALESCE(pair_id, id) AS turn_id, "
        "       length(raw_draft) AS raw_len, length(sanctioned_text) AS min_len "
        "FROM session_gates "
        "WHERE kind_id = ? AND raw_draft IS NOT NULL AND sanctioned_text IS NOT NULL "
        "  AND length(raw_draft) > 0",
        (minimizer_store.RELAY_KIND,),
    )
    turns: dict[int, list[tuple[int, float]]] = {}
    for r in rows:
        turns.setdefault(r["turn_id"], []).append(
            (r["raw_len"], r["min_len"] / r["raw_len"]))

    samples = [(v[0][0], sum(x[1] for x in v) / len(v)) for v in turns.values()]

    out = []
    for label, lo, hi in _BUCKETS:
        vals = [ratio for raw_len, ratio in samples if lo <= raw_len < hi]
        out.append((label, len(vals), sum(vals) / len(vals) if vals else None))
    return out


def status() -> None:
    """Print what the loop currently believes."""
    click.echo()
    click.echo(click.style("Minimizer loop", bold=True))
    click.echo("─" * 60)

    champs = db.query(
        "SELECT c.task_type, c.hash, c.promoted_at, c.promoted_by, v.origin "
        "FROM minimizer_champions c "
        "LEFT JOIN minimizer_variants v ON v.hash = c.hash "
        "ORDER BY c.task_type"
    )
    click.echo(click.style("  Champions", bold=True))
    if not champs:
        click.echo("    (none promoted yet — the shipped default is in force)")
    for c in champs:
        bucket = c["task_type"] or "(untyped)"
        click.echo(
            f"    {bucket:<12} {c['hash']}  via {c['promoted_by'] or '?'}"
            f"  {c['promoted_at']}  [{c['origin'] or '?'}]"
        )

    for c in champs:
        shipped = minimizer_optimizer.champion_diverges(c["task_type"])
        if shipped:
            click.echo(
                f"    {c['task_type'] or '(untyped)':<12} differs from the shipped "
                f"default ({shipped}) — `endless minimizer reseed` adopts it"
            )

    click.echo()
    click.echo(click.style("  Sampling", bold=True))
    click.echo(f"    A/B rate: {minimizer_store.ab_rate():.0%}  (the user moves this with $MORE / $LESS)")
    notice = minimizer_store.state_get("calibration_notice", "") or ""
    if notice:
        click.echo(f"    raised because the judge is {notice}")

    cal = minimizer_judge.calibration()
    click.echo()
    click.echo(click.style("  Judge calibration", bold=True))
    if not cal["calibrated"]:
        click.echo(
            f"    not yet calibrated ({cal['total']} scored prediction(s); "
            f"needs {minimizer_judge.AGREEMENT_MIN_SAMPLES})"
        )
    else:
        verdict = "trusted" if cal["trusted"] else "BELOW FLOOR"
        click.echo(
            f"    agrees with the user {cal['rate']:.0%} of the time "
            f"({cal['agreed']}/{cal['total']}) — {verdict}"
        )

    click.echo()
    click.echo(click.style("  Keep-ratio by draft size", bold=True))
    click.echo("    (rolling, over live traffic — an alarm, never a verdict)")
    for label, n, mean in _keep_ratios():
        if n == 0:
            click.echo(f"    {label:<10} —      (0 samples)")
        else:
            click.echo(f"    {label:<10} {mean:.2f}   ({n} samples)")

    unjudged = db.query(
        "SELECT count(*) AS n FROM session_gates g "
        "WHERE g.kind_id = ? AND g.raw_draft IS NOT NULL "
        "  AND NOT EXISTS (SELECT 1 FROM report_judgments j WHERE j.gate_id = g.id)",
        (minimizer_store.RELAY_KIND,),
    )
    click.echo()
    click.echo(click.style("  Queue", bold=True))
    click.echo(f"    unjudged corpus rows: {unjudged[0]['n']}")

    evals = minimizer_store.recent_evals(5)
    click.echo()
    click.echo(click.style("  Recent replays", bold=True))
    if not evals:
        click.echo("    (none yet)")
    for e in evals:
        mark = "PROMOTED" if e["promoted"] else "held"
        click.echo(
            f"    {e['created_at']}  {e['task_type'] or '(untyped)':<10} "
            f"{e['challenger_hash']} vs {e['champion_hash']}  "
            f"{e['wins']}W-{e['losses']}L-{e['ties']}T  {mark}"
        )
        click.echo(f"        {e['verdict']}")
    click.echo()


def judge(limit: int) -> None:
    """Score unjudged turns, then close out predictions the user has answered."""
    result = minimizer_judge.sweep(limit)
    cal = result["calibration"]
    click.echo(
        f"judged {result['judged']} turn(s), closed {result['reactions']} prediction(s)"
    )
    if cal["calibrated"]:
        click.echo(f"judge agreement: {cal['rate']:.0%} ({cal['agreed']}/{cal['total']})")
    else:
        click.echo(f"judge agreement: not yet calibrated ({cal['total']} scored)")


def optimize(task_type: str | None) -> None:
    """Run one paired-replay round now, for one task type or for the untyped bucket."""
    bucket = task_type if task_type is not None else minimizer_store.NO_TASK_TYPE
    result = minimizer_optimizer.run_round(bucket)
    if not result["acted"]:
        click.echo(f"no round run: {result['reason']}")
        return
    click.echo(
        f"{result['challenger']} vs {result['champion']} over "
        f"{result['wins'] + result['losses'] + result['ties']} paired item(s): "
        f"{result['wins']}W-{result['losses']}L-{result['ties']}T, "
        f"{result['vetoes']} veto(es), {result['skipped']} skipped"
    )
    click.echo(result["verdict"])


def _round_is_due() -> bool:
    """Whether enough time has passed since the last optimize round.

    The claim is the timestamp itself, written BEFORE the round runs. Two ticks
    racing (the lease can lapse under a slow round) would otherwise both see the
    same stale value and both spend a full replay on the same question.
    """
    last = minimizer_store.state_get(minimizer_store.STATE_LAST_OPTIMIZE)
    now = dt.datetime.now(dt.timezone.utc)
    if last:
        try:
            when = dt.datetime.fromisoformat(last)
            if when.tzinfo is None:
                when = when.replace(tzinfo=dt.timezone.utc)
            hours = (now - when).total_seconds() / 3600
            if hours < minimizer_optimizer.ROUND_INTERVAL_HOURS:
                return False
        except ValueError:
            pass
    minimizer_store.state_set(
        minimizer_store.STATE_LAST_OPTIMIZE, now.replace(microsecond=0).isoformat())
    return True


def run(limit: int) -> None:
    """One loop tick — what the background job fires.

    Judging every tick and replaying rarely is the whole cadence decision, and it
    lives here rather than in two job registrations because only this side knows
    what a round costs.
    """
    judge(limit)
    if not _round_is_due():
        click.echo("optimize round: not due")
        return
    optimize(None)


def variants(task_type: str | None, limit: int) -> None:
    """List variants with their lineage, newest first."""
    rows = minimizer_store.list_variants(task_type, limit)
    if not rows:
        click.echo("no variants recorded yet")
        return
    champions = {
        r["task_type"]: r["hash"]
        for r in db.query("SELECT task_type, hash FROM minimizer_champions")
    }
    click.echo()
    for v in rows:
        mark = "*" if champions.get(v["task_type"]) == v["hash"] else " "
        parent = v["parent_hash"] or "—"
        click.echo(
            f" {mark} {v['hash']}  {v['task_type'] or '(untyped)':<10} "
            f"parent={parent:<16} bypass={v['bypass_threshold']:<5} "
            f"{v['origin'] or '?':<10} {v['created_at']}"
        )
        if v["note"]:
            click.echo(f"     {v['note']}")
    click.echo()
    click.echo("  * = champion.  `endless minimizer show <hash>` prints the full prompt.")
    click.echo()


def show(variant_hash: str) -> None:
    """Print one variant in full — all three axes, not just the prompt."""
    v = minimizer_store.get_variant(variant_hash)
    if v is None:
        raise click.ClickException(f"No such variant: {variant_hash}")
    click.echo()
    click.echo(click.style(f"Variant {v['hash']}", bold=True))
    click.echo(f"  task type:        {v['task_type'] or '(untyped)'}")
    click.echo(f"  parent:           {v['parent_hash'] or '—'}")
    click.echo(f"  origin:           {v['origin'] or '?'}")
    click.echo(f"  created:          {v['created_at']}")
    click.echo(f"  bypass threshold: {v['bypass_threshold']} chars")
    if v["note"]:
        click.echo(f"  note:             {v['note']}")
    click.echo()
    click.echo(click.style("  Fetch policy", bold=True))
    click.echo(json.dumps(v["fetch_policy"], indent=2))
    click.echo()
    click.echo(click.style("  Prompt", bold=True))
    click.echo(v["prompt_text"])
    click.echo()


def rollback(task_type: str | None) -> None:
    """Point a champion back at its parent."""
    bucket = task_type if task_type is not None else minimizer_store.NO_TASK_TYPE
    ok, message = minimizer_optimizer.rollback(bucket)
    click.echo(message)
    if not ok:
        raise SystemExit(1)


def reseed(task_type: str | None) -> None:
    """Adopt the current shipped default as champion."""
    bucket = task_type if task_type is not None else minimizer_store.NO_TASK_TYPE
    changed, message = minimizer_optimizer.reseed(bucket)
    click.echo(message)
    if not changed:
        raise SystemExit(1)

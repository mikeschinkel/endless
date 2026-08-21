"""The autoresearch loop's storage layer (E-1975).

Every read and write the loop makes against the database lives here, so the judge,
the optimizer and the report command share one definition of "the corpus", "the
champion" and "a variant" rather than three that drift.

Three things worth knowing before reading further.

**A variant is the whole bundle, not the prompt.** Prompt text, fetch policy and
bypass threshold are tuned JOINTLY because they interact: a prompt told to
deduplicate against the task plan is only as good as a policy that fetches the
task plan, and a threshold that skips the minimizer entirely makes both moot for
that turn. Tuning them separately credits one axis for another's win.

**Variants are content-addressed, and there is no git repo.** Every eval run has
to reference its variant inside this store regardless, so adding git would create
two identities for one object plus a sync problem. `parent_hash` supplies the one
thing git was wanted for — lineage — and diffs are computed at read time.

**Promotion is a pointer move.** `minimizer_champions` holds one row per task
type and nothing else changes when a challenger wins. That is what makes removing
the user's approval step safe (ED-1556): a bad auto-promotion is undone by
pointing the row back at `parent_hash`, not by reconstructing a prompt from a
diff.
"""

import hashlib
import json
from typing import Any

from endless import db, report_prompts

# The gate kind for a report row. Mirrors gatekind.GateKindRelay on the Go side;
# the name stayed 'relay' because renaming it would churn the schema, the enum
# and the integrity check for no behavior change.
RELAY_KIND = 2

# The task-type bucket a turn with no claimed task falls into. Empty string
# rather than NULL so every query can join on equality — a nullable bucket key
# turns "the champion for this turn" into a three-branch expression at eight call
# sites.
NO_TASK_TYPE = ""

# Loop-state keys, shared with the Go side (internal/monitor/minimizer.go).
STATE_AB_RATE = "ab_rate"
STATE_LAST_OPTIMIZE = "last_optimize_at"
STATE_SEED_INDEX = "seed_index"

DEFAULT_AB_RATE = 0.15


# --- variants ----------------------------------------------------------------

def content_hash(prompt_text: str, fetch_policy: dict, bypass_threshold: int) -> str:
    """The variant's identity: sha256 over all three axes.

    All three, not just the prompt. Two bundles differing only in bypass
    threshold are different experiments with different results, and collapsing
    them onto one hash would make an eval reference a bundle that no longer
    exists as it was run.
    """
    payload = json.dumps(
        {
            "prompt_text": prompt_text,
            "fetch_policy": fetch_policy,
            "bypass_threshold": int(bypass_threshold),
        },
        sort_keys=True,
        separators=(",", ":"),
    )
    return hashlib.sha256(payload.encode("utf-8")).hexdigest()[:16]


def save_variant(
    *,
    task_type: str,
    prompt_text: str,
    fetch_policy: dict,
    bypass_threshold: int,
    parent_hash: str | None = None,
    origin: str = "generated",
    note: str = "",
) -> str:
    """Insert a variant if it is new and return its hash.

    Idempotent by construction: content addressing means a regenerated identical
    bundle is the same row. That matters more than it looks — a generator that
    rediscovers the champion has produced no challenger, and the loop needs to be
    able to SEE that rather than record a second copy under a new id and replay
    a prompt against itself.
    """
    h = content_hash(prompt_text, fetch_policy, bypass_threshold)
    db.execute(
        "INSERT OR IGNORE INTO minimizer_variants "
        "(hash, task_type, prompt_text, fetch_policy, bypass_threshold, "
        " parent_hash, origin, note) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
        (
            h,
            task_type,
            prompt_text,
            json.dumps(fetch_policy, sort_keys=True),
            int(bypass_threshold),
            parent_hash,
            origin,
            note or None,
        ),
    )
    return h


def get_variant(variant_hash: str) -> dict | None:
    """One variant by hash, with its fetch policy already parsed."""
    rows = db.query(
        "SELECT hash, task_type, prompt_text, fetch_policy, bypass_threshold, "
        "       parent_hash, origin, note, created_at "
        "FROM minimizer_variants WHERE hash = ?",
        (variant_hash,),
    )
    if not rows:
        return None
    return _variant_row(rows[0])


def _variant_row(row) -> dict:
    out = dict(row)
    try:
        out["fetch_policy"] = json.loads(out["fetch_policy"])
    except (TypeError, ValueError):
        out["fetch_policy"] = dict(report_prompts.DEFAULT_FETCH_POLICY)
    return out


def seed_variant(task_type: str = NO_TASK_TYPE) -> dict:
    """The variant built from the shipped defaults, created on first use.

    This is the floor the loop can always fall back to and the point every
    lineage traces to. It carries origin='seed' so `minimizer variants` can say
    which prompt was written by a person and which by the loop — a distinction
    that stops mattering for correctness and never stops mattering for reading
    the history.
    """
    prompts = report_prompts.load_prompts()
    h = save_variant(
        task_type=task_type,
        prompt_text=prompts[report_prompts.MINIMIZE],
        fetch_policy=report_prompts.DEFAULT_FETCH_POLICY,
        bypass_threshold=report_prompts.DEFAULT_BYPASS_THRESHOLD,
        origin="seed",
        note="shipped default",
    )
    return get_variant(h)


def champion(task_type: str = NO_TASK_TYPE) -> dict:
    """The variant in force for this task type.

    Resolution is three-step and the middle step is the one that matters:

      1. This task type's own champion, if it has one.
      2. The untyped bucket's champion — so splitting by task type does not mean
         every new type starts from the shipped seed with no history.
      3. The seed, created on demand.

    Splitting by task type NOW rather than later is deliberate (the column is
    cheap; coordinating a follow-up task to add it later is not), but a split
    that made each type start from zero would trade a real cost today for a
    speculative gain, which is the opposite of what was wanted.
    """
    for bucket in (task_type, NO_TASK_TYPE):
        rows = db.query(
            "SELECT hash FROM minimizer_champions WHERE task_type = ?", (bucket,)
        )
        if rows:
            variant = get_variant(rows[0]["hash"])
            if variant is not None:
                return variant
    variant = seed_variant(task_type)
    promote(task_type, variant["hash"], by="seed", note="first use")
    return variant


def promote(task_type: str, variant_hash: str, *, by: str, note: str = "") -> None:
    """Point a task type's champion at a variant. The whole of promotion."""
    db.execute(
        "INSERT INTO minimizer_champions (task_type, hash, promoted_by, note) "
        "VALUES (?, ?, ?, ?) "
        "ON CONFLICT(task_type) DO UPDATE SET hash=excluded.hash, "
        "  promoted_at=strftime('%Y-%m-%dT%H:%M:%S','now'), "
        "  promoted_by=excluded.promoted_by, note=excluded.note",
        (task_type, variant_hash, by, note or None),
    )


def champion_row(task_type: str) -> dict | None:
    """The raw champion pointer, or None when the type has never been promoted."""
    rows = db.query(
        "SELECT task_type, hash, promoted_at, promoted_by, note "
        "FROM minimizer_champions WHERE task_type = ?",
        (task_type,),
    )
    return dict(rows[0]) if rows else None


def list_variants(task_type: str | None = None, limit: int = 50) -> list[dict]:
    """Variants newest first, optionally scoped to one task type."""
    if task_type is None:
        rows = db.query(
            "SELECT hash, task_type, prompt_text, fetch_policy, bypass_threshold, "
            "       parent_hash, origin, note, created_at "
            "FROM minimizer_variants ORDER BY created_at DESC, hash LIMIT ?",
            (limit,),
        )
    else:
        rows = db.query(
            "SELECT hash, task_type, prompt_text, fetch_policy, bypass_threshold, "
            "       parent_hash, origin, note, created_at "
            "FROM minimizer_variants WHERE task_type = ? "
            "ORDER BY created_at DESC, hash LIMIT ?",
            (task_type, limit),
        )
    return [_variant_row(r) for r in rows]


def untried_challengers(task_type: str, champion_hash: str, limit: int = 4) -> list[dict]:
    """Challengers for this task type that have never been replayed, newest first.

    "Never been replayed" and not "never won": a challenger that lost is answered,
    and re-running it spends the corpus on a question already settled. A
    challenger that was generated and never evaluated is the only kind worth a
    round.
    """
    rows = db.query(
        "SELECT v.hash, v.task_type, v.prompt_text, v.fetch_policy, "
        "       v.bypass_threshold, v.parent_hash, v.origin, v.note, v.created_at "
        "FROM minimizer_variants v "
        "WHERE v.task_type = ? AND v.hash <> ? "
        "  AND NOT EXISTS (SELECT 1 FROM minimizer_evals e "
        "                  WHERE e.challenger_hash = v.hash) "
        "ORDER BY v.created_at DESC, v.hash LIMIT ?",
        (task_type, champion_hash, limit),
    )
    return [_variant_row(r) for r in rows]


# --- loop state --------------------------------------------------------------

def state_get(key: str, default: str | None = None) -> str | None:
    rows = db.query("SELECT value FROM minimizer_state WHERE key = ?", (key,))
    return rows[0]["value"] if rows else default


def state_set(key: str, value: str) -> None:
    db.execute(
        "INSERT INTO minimizer_state (key, value) VALUES (?, ?) "
        "ON CONFLICT(key) DO UPDATE SET value=excluded.value, "
        "  updated_at=strftime('%Y-%m-%dT%H:%M:%S','now')",
        (key, str(value)),
    )


def ab_rate() -> float:
    """The fraction of turns that get a paired minimization.

    Not a config key. The only seat that can judge the annoyance-versus-value
    trade is the one being annoyed, and it makes that judgment by answering the
    question each pair ends with — so the value lives in loop state that `$MORE`
    and `$LESS` move.
    """
    raw = state_get(STATE_AB_RATE)
    if raw is None:
        return DEFAULT_AB_RATE
    try:
        return max(0.0, min(1.0, float(raw)))
    except ValueError:
        return DEFAULT_AB_RATE


# --- the corpus --------------------------------------------------------------

_CORPUS_COLUMNS = (
    "g.id, g.session_id, g.task_id, g.task_type, g.raw_draft, g.user_prompt, "
    "g.sanctioned_text, g.fetched_context, g.variant_hash, g.bypassed, "
    "g.pair_id, g.pair_slot, g.picked, g.triggered_at"
)


def unjudged_rows(limit: int = 8) -> list[dict]:
    """Corpus rows with no judgment yet, OLDEST first.

    Oldest first is not a stylistic choice. A judgment is only a PREDICTION while
    the user has not reacted; sweeping newest-first would leave the oldest rows
    permanently unjudged under a backlog, and those are exactly the rows whose
    reactions have already landed — so the queue would silently fill with samples
    that can never be blind.
    """
    rows = db.query(
        f"SELECT {_CORPUS_COLUMNS} FROM session_gates g "
        "WHERE g.kind_id = ? AND g.raw_draft IS NOT NULL "
        "  AND g.sanctioned_text IS NOT NULL "
        "  AND NOT EXISTS (SELECT 1 FROM report_judgments j WHERE j.gate_id = g.id) "
        "ORDER BY g.id LIMIT ?",
        (RELAY_KIND, limit),
    )
    return [dict(r) for r in rows]


def corpus_row(gate_id: int) -> dict | None:
    rows = db.query(
        f"SELECT {_CORPUS_COLUMNS} FROM session_gates g WHERE g.id = ?", (gate_id,)
    )
    return dict(rows[0]) if rows else None


def frozen_corpus(task_type: str, limit: int = 12, require_context: bool = True) -> list[dict]:
    """The replay slice: real turns, newest first, with the context they ran on.

    FROZEN means the caller records exactly which ids it used
    (`minimizer_evals.corpus_ids`) and never re-fetches their context. Replay
    that re-fetches is not a paired comparison — the champion and the challenger
    would be answering different questions — and the whole reason paired replay
    may gate a promotion while rolling metrics may not is that pairing cancels
    item difficulty.
    """
    where = [
        "g.kind_id = ?",
        "g.raw_draft IS NOT NULL",
        "g.sanctioned_text IS NOT NULL",
        "g.bypassed = 0",
    ]
    params: list[Any] = [RELAY_KIND]
    if require_context:
        where.append("g.fetched_context IS NOT NULL")
    if task_type:
        where.append("COALESCE(g.task_type,'') = ?")
        params.append(task_type)
    params.append(limit)
    rows = db.query(
        f"SELECT {_CORPUS_COLUMNS} FROM session_gates g "
        f"WHERE {' AND '.join(where)} ORDER BY g.id DESC LIMIT ?",
        tuple(params),
    )
    return [dict(r) for r in rows]


def labels_for(gate_id: int) -> list[dict]:
    rows = db.query(
        "SELECT token, span, note, created_at FROM report_labels "
        "WHERE gate_id = ? ORDER BY id",
        (gate_id,),
    )
    return [dict(r) for r in rows]


def pair_rows(pair_id: int) -> list[dict]:
    rows = db.query(
        f"SELECT {_CORPUS_COLUMNS} FROM session_gates g "
        "WHERE g.pair_id = ? ORDER BY g.id",
        (pair_id,),
    )
    return [dict(r) for r in rows]


# --- judgments ---------------------------------------------------------------

def record_judgment(
    *,
    gate_id: int,
    variant_hash: str | None,
    invariants_ok: bool,
    invariant_detail: str,
    invented: bool,
    fidelity: int | None,
    fidelity_detail: str,
    compression: float | None,
    predicted_pick: str | None,
    predicted_flag: bool | None,
    blind: bool,
) -> None:
    """Persist one judgment. One per corpus row, enforced by a UNIQUE constraint.

    `INSERT OR IGNORE` rather than an upsert: a re-claimed job tick must not
    overwrite a prediction with a second one made after the user had already
    reacted. The first judgment is the honest one, and later is not better here.
    """
    db.execute(
        "INSERT OR IGNORE INTO report_judgments "
        "(gate_id, variant_hash, invariants_ok, invariant_detail, invented, "
        " fidelity, fidelity_detail, compression, predicted_pick, predicted_flag, blind) "
        "VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
        (
            gate_id,
            variant_hash,
            1 if invariants_ok else 0,
            invariant_detail or None,
            1 if invented else 0,
            fidelity,
            fidelity_detail or None,
            compression,
            predicted_pick,
            None if predicted_flag is None else (1 if predicted_flag else 0),
            1 if blind else 0,
        ),
    )


def record_reaction(gate_id: int, *, actual_pick: str | None, actual_flag: bool) -> None:
    """Close the loop on one prediction once the user has reacted.

    `agreed` is computed here rather than at read time so the calibration number
    is a count over a stored column instead of a re-derivation that can silently
    change meaning when the comparison is edited.
    """
    rows = db.query(
        "SELECT predicted_pick, predicted_flag, blind FROM report_judgments "
        "WHERE gate_id = ?",
        (gate_id,),
    )
    if not rows:
        return
    j = rows[0]
    agreed: int | None = None
    if j["blind"]:
        if actual_pick is not None and j["predicted_pick"] is not None:
            agreed = 1 if j["predicted_pick"] == actual_pick else 0
        elif j["predicted_flag"] is not None:
            agreed = 1 if bool(j["predicted_flag"]) == actual_flag else 0
    db.execute(
        "UPDATE report_judgments SET actual_pick = ?, actual_flag = ?, agreed = ? "
        "WHERE gate_id = ?",
        (actual_pick, 1 if actual_flag else 0, agreed, gate_id),
    )


def agreement(window: int = 50) -> tuple[int, int]:
    """Rolling (agreed, total) over the most recent scored predictions.

    This is the loop's honesty check on its own judge, and it is the number that
    raises the A/B sample rate when it sags. It counts only BLIND judgments with
    an observed reaction — a judgment made after the user already complained is
    hindsight wearing a prediction's clothes.
    """
    rows = db.query(
        "SELECT agreed FROM report_judgments WHERE agreed IS NOT NULL "
        "ORDER BY id DESC LIMIT ?",
        (window,),
    )
    total = len(rows)
    return sum(1 for r in rows if r["agreed"]), total


def record_eval(
    *,
    task_type: str,
    challenger_hash: str,
    champion_hash: str,
    corpus_ids: list[int],
    wins: int,
    losses: int,
    ties: int,
    vetoes: int,
    promoted: bool,
    verdict: str,
    detail: str,
) -> None:
    db.execute(
        "INSERT INTO minimizer_evals "
        "(task_type, challenger_hash, champion_hash, corpus_ids, wins, losses, "
        " ties, vetoes, promoted, verdict, detail) "
        "VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
        (
            task_type,
            challenger_hash,
            champion_hash,
            json.dumps(corpus_ids),
            wins,
            losses,
            ties,
            vetoes,
            1 if promoted else 0,
            verdict,
            detail or None,
        ),
    )


def recent_evals(limit: int = 10) -> list[dict]:
    rows = db.query(
        "SELECT id, task_type, challenger_hash, champion_hash, wins, losses, "
        "       ties, vetoes, promoted, verdict, created_at "
        "FROM minimizer_evals ORDER BY id DESC LIMIT ?",
        (limit,),
    )
    return [dict(r) for r in rows]

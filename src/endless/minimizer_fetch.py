"""The minimizer's fetch policy: what it may look at, and what it actually did.

ED-1557 makes deduplication half the minimizer's job — "remove things the user
can ALREADY see in `session status`, things ALREADY written in the task plan or
analysis". That objective cannot be met, or scored, from the (prompt, draft,
minimized) triple alone. Something has to put the user's existing material in
front of the editor.

Two proposals in the design turned out to be one mechanism:

  * the minimizer FETCHES what it wants, and
  * the corpus row RECORDS what was requested and what came back.

They are the same mechanism, because a tool-using minimizer is nondeterministic
in its INPUTS. An A/B over a frozen corpus is meaningless if each run re-fetches
live state that has since changed — the champion and the challenger would be
answering different questions and the comparison would measure drift. So replay
must serve context from the record, never re-fetch, and that requirement is what
forces recording. Fetching and recording are not two features.

**What it may fetch is declared here; what it does fetch is a JSON policy the
optimizer maintains.** That split is the third tunable axis. "Give it everything
every turn" fails on cost and latency, not on principle — which is exactly what
makes the policy worth optimizing rather than fixing.

Every source is a plain DB read, deliberately. Giving the minimizer real tool use
would make each turn a multi-step agent loop with a latency the user feels and an
input set nothing can replay; a declared source list with a policy over it buys
the same dedup at one round trip and stays reproducible.
"""

from __future__ import annotations

import json

from endless import db

# Per-source output cap, applied before the policy's global budget. One runaway
# analysis field must not crowd out every other source — the point of a policy
# with three entries is that the editor sees three kinds of thing.
_PER_SOURCE_CHARS = 3000


def _truncate(text: str, limit: int = _PER_SOURCE_CHARS) -> str:
    text = (text or "").strip()
    if len(text) <= limit:
        return text
    return text[:limit] + "\n… (truncated)"


# --- the declared sources ----------------------------------------------------
#
# Each takes (task_id, session_id, spec) and returns the fetched text. Adding a
# source is a function plus a row in SOURCES; the policy may then name it. A
# policy naming an unknown source is skipped rather than fatal — the optimizer
# writes policies, and a hallucinated source name must cost one weak variant, not
# every reply that turn.

def _fetch_task_plan(task_id: int | None, session_id: int | None, spec: dict) -> str:
    if not task_id:
        return ""
    rows = db.query(
        "SELECT id, title, status, phase, description, text, analysis "
        "FROM live_tasks WHERE id = ?",
        (task_id,),
    )
    if not rows:
        return ""
    t = rows[0]
    parts = [f"E-{t['id']} [{t['status']}/{t['phase']}] {t['title']}"]
    for label, key in (("Description", "description"), ("Plan", "text"),
                       ("Analysis", "analysis")):
        value = (t[key] or "").strip()
        if value:
            parts.append(f"--- {label} ---\n{value}")
    return _truncate("\n\n".join(parts))


def _fetch_task_analysis(task_id: int | None, session_id: int | None, spec: dict) -> str:
    if not task_id:
        return ""
    rows = db.query("SELECT analysis FROM live_tasks WHERE id = ?", (task_id,))
    if not rows:
        return ""
    return _truncate(rows[0]["analysis"] or "")


def _fetch_sibling_tasks(task_id: int | None, session_id: int | None, spec: dict) -> str:
    """The task's children and siblings with their statuses.

    This is the single most-restated thing in a long reply: an agent that just
    filed three follow-ups narrates all three, and every one of them is already a
    row the user can list.
    """
    if not task_id:
        return ""
    rows = db.query(
        "SELECT id, title, status FROM live_tasks "
        "WHERE parent_id = ? OR (parent_id IS NOT NULL AND parent_id = "
        "      (SELECT parent_id FROM live_tasks WHERE id = ?)) "
        "ORDER BY id LIMIT 30",
        (task_id, task_id),
    )
    if not rows:
        return ""
    return _truncate("\n".join(f"E-{r['id']} [{r['status']}] {r['title']}" for r in rows))


def _fetch_session_status(task_id: int | None, session_id: int | None, spec: dict) -> str:
    """What `session status` would already be showing this user.

    Rendered from the same rows rather than by shelling the command: the point is
    the FACTS the user can see, and a subprocess would add latency to every turn
    to reproduce them with box-drawing characters the editor cannot use.
    """
    if not session_id:
        return ""
    rows = db.query(
        "SELECT s.id, s.state, s.task_id, s.summary, "
        "       COALESCE(p.name,'') AS project "
        "FROM sessions s LEFT JOIN projects p ON p.id = s.project_id "
        "WHERE s.id = ?",
        (session_id,),
    )
    if not rows:
        return ""
    s = rows[0]
    parts = [f"session ES-{s['id']} [{s['state']}] project={s['project']}"]
    if s["task_id"]:
        t = db.query(
            "SELECT id, title, status, phase FROM live_tasks WHERE id = ?",
            (s["task_id"],),
        )
        if t:
            parts.append(
                f"active task E-{t[0]['id']} [{t[0]['status']}/{t[0]['phase']}] {t[0]['title']}"
            )
    if (s["summary"] or "").strip():
        parts.append("summary: " + " ".join(s["summary"].split()))
    return _truncate("\n".join(parts))


# There is deliberately NO source for the session's own earlier replies.
#
# One existed, and it was the defect. Deduplication targets material the user
# will have to review ANYWAY — session status, a task plan, a decision — because
# repeating that here costs them a second reading of something they are going to
# open regardless. An earlier chat message is not that: chat is ephemeral, they
# are reading the current reply, and nothing sends them back through the
# transcript to reassemble it.
#
# Feeding prior replies in as duplication evidence deleted the table, the code
# block and the verify command together in 2 of 8 live runs, leaving a single
# sentence. Re-adding this source re-opens that, so it is absent rather than
# merely left out of the default policy — the optimizer writes policies, and it
# can only choose from what is declared here.
SOURCES = {
    "task_plan": _fetch_task_plan,
    "task_analysis": _fetch_task_analysis,
    "sibling_tasks": _fetch_sibling_tasks,
    "session_status": _fetch_session_status,
}


# --- executing a policy ------------------------------------------------------

def run_policy(
    policy: dict,
    *,
    task_id: int | None,
    session_id: int | None,
) -> tuple[str, str]:
    """Execute a fetch policy and return (context_text, record_json).

    context_text goes to the minimizer. record_json goes on the corpus row and is
    what a later replay reads INSTEAD of running this function again.

    Failures are recorded, not raised. A source that errors leaves an `ok: false`
    entry and the turn proceeds with less context — the minimizer degrades to
    judging the draft alone, which is worse but not wrong, whereas failing the
    report would cost the user their turn over a nice-to-have.
    """
    fetches = policy.get("fetches") or []
    budget = int(policy.get("max_chars", 6000) or 6000)

    record: list[dict] = []
    blocks: list[str] = []
    used = 0

    for spec in fetches:
        if not isinstance(spec, dict):
            continue
        name = spec.get("source", "")
        when = spec.get("when", "always")
        if when == "task" and not task_id:
            record.append({"source": name, "skipped": "no task claimed"})
            continue
        fn = SOURCES.get(name)
        if fn is None:
            record.append({"source": name, "skipped": "unknown source"})
            continue
        try:
            text = fn(task_id, session_id, spec)
            ok, err = True, ""
        except Exception as exc:  # noqa: BLE001 — see docstring
            text, ok, err = "", False, str(exc)
        if ok and not text:
            record.append({"source": name, "ok": True, "chars": 0})
            continue
        remaining = budget - used
        if remaining <= 0:
            record.append({"source": name, "skipped": "budget exhausted"})
            continue
        if len(text) > remaining:
            # The marker counts against the budget too. Appending it after
            # truncating to `remaining` would put every truncated fetch over the
            # ceiling by a constant — a budget that is exceeded by construction
            # is not a budget.
            marker = "\n… (budget)"
            text = text[:max(0, remaining - len(marker))] + marker
        used += len(text)
        record.append({
            "source": name,
            "ok": ok,
            "chars": len(text),
            "text": text,
            **({"error": err} if err else {}),
        })
        if ok and text:
            blocks.append(f"### {name}\n{text}")

    return "\n\n".join(blocks), json.dumps(record, ensure_ascii=False)


def context_from_record(record_json: str | None) -> str:
    """Rebuild the context text a corpus row was minimized with.

    This is the whole reason the record exists. Replay MUST come through here and
    never through run_policy: re-fetching would give the challenger a different
    world than the champion saw, and a paired comparison across two different
    worlds is not a comparison.
    """
    if not record_json:
        return ""
    try:
        record = json.loads(record_json)
    except (TypeError, ValueError):
        return ""
    blocks = []
    for entry in record:
        if not isinstance(entry, dict) or not entry.get("ok"):
            continue
        text = entry.get("text") or ""
        if text:
            blocks.append(f"### {entry.get('source', '?')}\n{text}")
    return "\n\n".join(blocks)

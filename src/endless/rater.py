"""The rater: proposes complexity and risk for unrated `submitted` tasks (E-2203).

Ratings are the agent's to know and the user's to ratify (ED-1538). An agent
that attaches a plan is refused the promotion to `submitted` until it rates the
task, so the tasks left `submitted` and unrated are, in practice, ones a person
filed with a plan. Asking that person to originate the ratings is what the
design rules out; this module originates them instead, and the person ratifies
them at `task approve` as usual.

It is the retired description triage (E-1859, removed by E-1993) reused rather
than re-derived: Go reads → JSON → Go template render → prompt → model → parse
→ `emit_event`. Four properties carry over and are load-bearing.

**Zero Python SQLite.** Every read goes through `endless-go session-query`
(`unrated-tasks`, `rater-context`, `rater-claim`, `rater-release`); the write
routes through `emit_event` → the Go executor. This module must never import
a SQLite driver or open a database.

**Persisted artifacts only.** The model sees the task's description, context,
plan, parent, sibling titles and linked decisions — never a transcript — so the
call is reproducible and judges what an implementer will work from.

**Fail-open, always.** Timeout, missing `claude`, non-zero exit, a reply with no
usable rating, an unreadable context → write nothing, leave the task unrated,
record a WARN-0029 fault, exit zero. The next sweep retries it, and a person can
always rate by hand with `task update --complexity/--risk`.

**A rating anyone gave always wins.** The write re-reads the task immediately
before emitting and writes only an axis still unset, and only while the task is
still `submitted`, so a rating given in the seconds the model call took is never
overwritten. The same guard makes a re-claimed sweep idempotent.
"""

import functools
import json
import os
import socket
import subprocess
from pathlib import Path

import click

from endless import config
from endless import provenance
from endless import ratings

# How long one rating call may take. Generous: the prompt carries a whole plan
# plus a parent, siblings and decisions, and a slow call is not a wrong call.
CALL_TIMEOUT_SECONDS = 120

# Default tasks per sweep. Every selected task becomes a model call, so this is
# a spend cap as much as a batch size. internal/raterjob passes it explicitly.
DEFAULT_BATCH_LIMIT = 10

# The template rendered into the prompt. Three-layer lookup (E-1565/E-1822):
# <project>/.endless/templates/rater/ratings.md.local.tmpl (per-developer)
# → .tmpl (committed) → embedded. Wording is a user-tunable lever.
TEMPLATE_NAME = "rater/ratings"

# The model purpose, resolved by config.internal_model.
MODEL_PURPOSE = "rater"

# The only status the rater writes to. A task that left it in the interval —
# approved, claimed, sent back — is no longer awaiting a proposal.
_RATEABLE_STATUS = "submitted"

# The catalog code for "the rater could not propose ratings" (docs/errors.md).
RATE_FAILED_CODE = "WARN-0029"


class RaterError(Exception):
    """A read or render failed. Callers fail open — see the module docstring."""


# --- the Go bridge ----------------------------------------------------------


def _endless_go(args: list[str], stdin: str | None = None) -> str:
    """Run `endless-go <args>` with the resolved DB context and return stdout.

    Binary and DB-context resolution are both borrowed from event_bridge
    rather than re-derived, so the rater reads open exactly the database it
    writes to (E-1429/E-1510).
    """
    from endless.event_bridge import _resolve_endless_go

    config.require_db_context()
    cmd = [_resolve_endless_go(), *config.go_db_context_args(), *args]
    try:
        result = subprocess.run(
            cmd, capture_output=True, text=True, input=stdin, timeout=60,
        )
    except (OSError, subprocess.TimeoutExpired) as exc:
        raise RaterError(f"{' '.join(args[:2])}: {exc}") from exc
    if result.returncode != 0:
        raise RaterError(
            f"{' '.join(args[:2])}: exit {result.returncode}: "
            f"{result.stderr.strip()}"
        )
    return result.stdout


def select_unrated(
    limit: int = DEFAULT_BATCH_LIMIT,
    project: str | None = None,
) -> list[dict]:
    """The rater queue: unrated `submitted` tasks, oldest first, capped.

    `project` None means every project — the database-wide sweep the job runs,
    which has a database but no cwd to resolve a project from.
    """
    args = ["session-query", "unrated-tasks", "--limit", str(limit)]
    if project:
        args += ["--project", project]
    out = _endless_go(args).strip()
    if not out:
        return []
    try:
        return provenance.rows_of(json.loads(out))
    except json.JSONDecodeError as exc:
        raise RaterError(f"unrated-tasks returned non-JSON: {exc}") from exc


def build_context(task_id: int) -> dict:
    """Assemble one task's rater context from persisted artifacts only."""
    out = _endless_go(
        ["session-query", "rater-context", "--id", str(task_id)]
    ).strip()
    if not out:
        raise RaterError(f"rater-context returned nothing for E-{task_id}")
    try:
        return json.loads(out)
    except json.JSONDecodeError as exc:
        raise RaterError(f"rater-context returned non-JSON: {exc}") from exc


# CLAIM_TTL_SECONDS bounds a claim. It must EXCEED the worst-case model call,
# or a slow-but-healthy claimant gets its task re-claimed underneath it and the
# duplicate spend this claim exists to prevent happens anyway. Headroom covers
# the Go reads and the render that bracket the call.
CLAIM_TTL_SECONDS = CALL_TIMEOUT_SECONDS + 60


@functools.lru_cache(maxsize=1)
def _claim_owner() -> str:
    """This PYTHON process's claim identity, stable for its lifetime.

    Passed explicitly to both claim and release: they are two separate
    `endless-go` invocations, so letting the Go helper default the owner to its
    own process would make every release a no-op against a different owner.
    """
    return f"{socket.gethostname()}:{os.getpid()}"


def claim(task_id: int) -> bool:
    """Take the per-task rater claim; True when this process won it.

    Taken BEFORE the model call. The job lease keeps two sweeps apart, but a
    person's `endless rater run` never enters the job runner — without this, it
    and a sweep could both pay for the same task. False is an ordinary outcome
    (someone else is mid-call), and so is a failure to reach the claim table:
    refusing to rate is the safe direction, and the sweep retries.
    """
    try:
        out = _endless_go([
            "session-query", "rater-claim",
            "--id", str(task_id),
            "--ttl-seconds", str(CLAIM_TTL_SECONDS),
            "--owner", _claim_owner(),
        ])
    except RaterError:
        return False
    return out.strip() == "1"


def release(task_id: int) -> None:
    """Drop this process's claim. Best-effort: a claim left behind lapses."""
    try:
        _endless_go([
            "session-query", "rater-release",
            "--id", str(task_id),
            "--owner", _claim_owner(),
        ])
    except RaterError:
        pass


def render_prompt(context: dict) -> str:
    """Render the rating template with `context` as its variables."""
    args = ["template", "render"]
    project = context.get("project")
    if project:
        args += ["--project", project]
    args.append(TEMPLATE_NAME)
    prompt = _endless_go(args, stdin=json.dumps(context))
    if not prompt.strip():
        raise RaterError(f"{TEMPLATE_NAME} rendered empty")
    return prompt


# --- the model call ---------------------------------------------------------


def _model(context: dict) -> str:
    """The model for this task's project, per config.internal_model."""
    root = context.get("project_root")
    return config.internal_model(
        MODEL_PURPOSE, project_root=Path(root) if root else None,
    )


def evaluate(prompt: str, model: str) -> tuple[dict, str]:
    """Ask the model for ratings; return (ratings, failure detail).

    The ratings dict is whatever parse_ratings found, possibly empty; the
    detail is "" on a reply and says what went wrong otherwise. Never raises.
    """
    from endless import internal_claude

    try:
        result = internal_claude.run_internal_claude(
            prompt, model=model, timeout=CALL_TIMEOUT_SECONDS,
        )
    except subprocess.TimeoutExpired:
        return {}, f"the model call timed out after {CALL_TIMEOUT_SECONDS}s"
    except FileNotFoundError:
        return {}, "`claude` was not found on PATH"
    except OSError as exc:
        return {}, f"the model call could not start: {exc}"
    if result.returncode != 0:
        return {}, (
            f"`claude` exited {result.returncode}: "
            f"{(result.stderr or '').strip()[:200]}"
        )
    return parse_ratings(result.stdout), ""


def parse_ratings(reply: str) -> dict:
    """The `COMPLEXITY:` / `RISK:` lines of a reply, as {axis: slug}.

    Lenient by design: any line, any case, surrounding markdown emphasis or a
    trailing period tolerated, first valid value per axis wins, and an absent
    or unrecognized value simply leaves that axis out. The ratings are a
    proposal a person ratifies, so a dropped one costs a keystroke at approve,
    while a strict parser would cost the whole call.
    """
    found: dict = {}
    for raw in (reply or "").splitlines():
        key, sep, value = raw.partition(":")
        axis = key.strip().strip("*_`-# \t").lower()
        if not sep or axis not in ratings.AXES or axis in found:
            continue
        slug = value.strip().strip("*_`.").strip().lower()
        if slug in ratings.LEVELS:
            found[axis] = slug
    return found


# --- the write --------------------------------------------------------------


def unset_axes(context: dict) -> list[str]:
    """The axes this task does not carry yet, in axis order."""
    return ratings.missing(context.get("complexity"), context.get("risk"))


def apply(task_id: int, proposed: dict, model: str) -> dict:
    """Write the proposed ratings for each axis the task still lacks.

    Re-reads the row first — the model call it follows takes seconds, and in
    that window a person may have rated, approved or claimed the task. They
    win: nothing is written unless the task is still `submitted`, and only an
    axis still unset is written. Returns the ratings actually written.
    """
    from endless.event_bridge import emit_event

    current = build_context(task_id)
    if current.get("status") != _RATEABLE_STATUS:
        return {}
    to_write = {
        axis: proposed[axis] for axis in unset_axes(current) if axis in proposed
    }
    if not to_write:
        return {}
    emit_event(
        kind="task.fields_updated",
        project=current["project"],
        entity_type="task",
        entity_id=str(task_id),
        payload={
            "fields": to_write,
            # Provenance, not attribution: WHO decided is actor.kind=triager
            # (the class of job); WHICH job and model live here, so a person
            # who disagrees with a rating can see where it came from.
            "rater": {"job": "rater", "model": model, "template": TEMPLATE_NAME},
        },
        actor_kind="triager",
        # Passed explicitly. Left to default, emit_event would look the path up
        # through Python's `db.query` — SQLite on a path this module keeps off.
        project_root=current.get("project_root"),
    )
    return to_write


# --- failure reporting ------------------------------------------------------


def report_failure(task_id: int, detail: str) -> None:
    """Record a WARN-0029 fault (task_id 0: the queue itself was unreadable) so a failed rating reaches a surface a user
    watches, instead of dying in a background job's captured stdout.

    Best-effort and silent on failure: a diagnostic must never be the reason a
    fail-open path starts failing closed.
    """
    try:
        _endless_go([
            "errors", "record",
            "--code", RATE_FAILED_CODE,
            "--source", "job:rater",
            "--summary", (
                f"rater left E-{task_id} unrated: {detail}" if task_id
                else f"rater could not read its queue: {detail}"
            )[:200],
            "--detail", detail,
            # Group by CAUSE, not by task: "claude is missing" is one incident
            # however many tasks hit it.
            "--fingerprint", f"rate-failed:{detail[:80]}",
        ])
    except RaterError:
        pass


# --- entry points -----------------------------------------------------------


def rate_one(task_id: int, dry_run: bool = False) -> dict:
    """Rate a single task. Never raises; the result dict says what happened.

    `outcome` is one of: `rated` (every unset axis written), `partial` (some
    written, some not), `dry-run`, `skipped` (nothing to do, or another run
    holds it), or `failed` (fail-open — nothing written). `failed` and
    `partial` are also recorded as WARN-0029 faults.
    """
    result = _rate_one(task_id, dry_run=dry_run)
    if result["outcome"] in ("failed", "partial"):
        report_failure(task_id, result.get("detail") or "no detail recorded")
    return result


def _rate_one(task_id: int, dry_run: bool = False) -> dict:
    """The rating itself. See rate_one for the reporting wrapper."""
    result: dict = {"task_id": task_id, "outcome": "failed", "detail": ""}
    try:
        context = build_context(task_id)
    except RaterError as exc:
        result["detail"] = str(exc)
        return result

    if context.get("status") != _RATEABLE_STATUS:
        result["outcome"] = "skipped"
        result["detail"] = f"status is {context.get('status')!r}, not submitted"
        return result
    needed = unset_axes(context)
    if not needed:
        result["outcome"] = "skipped"
        result["detail"] = "already rated"
        return result

    # The claim is taken BEFORE the model call and covers everything through
    # the write. --dry-run claims too: it makes the same call, so it costs the
    # same.
    if not claim(task_id):
        result["outcome"] = "skipped"
        result["detail"] = "another rater run holds the claim on this task"
        return result

    try:
        try:
            prompt = render_prompt(context)
        except RaterError as exc:
            result["detail"] = str(exc)
            return result

        model = _model(context)
        proposed, failure = evaluate(prompt, model)
        if failure:
            result["detail"] = failure
            return result
        usable = {a: proposed[a] for a in needed if a in proposed}
        if not usable:
            result["detail"] = (
                "the reply had no usable "
                + " or ".join(f"{a.upper()}:" for a in needed) + " line"
            )
            return result
        result["ratings"] = usable

        if dry_run:
            result["outcome"] = "dry-run"
            return result

        try:
            written = apply(task_id, usable, model)
        except (RaterError, click.ClickException) as exc:
            result["detail"] = str(exc)
            return result

        if not written:
            result["outcome"] = "skipped"
            result["detail"] = "the task was rated or moved before the write"
            return result
        result["ratings"] = written
        still_missing = [a for a in needed if a not in written]
        if still_missing:
            result["outcome"] = "partial"
            result["detail"] = (
                "the reply had no usable "
                + " or ".join(f"{a.upper()}:" for a in still_missing) + " line"
            )
        else:
            result["outcome"] = "rated"
        return result
    finally:
        release(task_id)


def rate_batch(
    limit: int = DEFAULT_BATCH_LIMIT,
    project: str | None = None,
    dry_run: bool = False,
) -> list[dict]:
    """Rate up to `limit` unrated submitted tasks. Never raises."""
    try:
        queue = select_unrated(limit=limit, project=project)
    except RaterError as exc:
        report_failure(0, str(exc))
        return [{"task_id": 0, "outcome": "failed", "detail": str(exc)}]
    return [rate_one(int(t["id"]), dry_run=dry_run) for t in queue]


# --- rendering --------------------------------------------------------------


def _ratings_text(r: dict) -> str:
    return ", ".join(f"{a} {v}" for a, v in r.get("ratings", {}).items())


def render_results(results: list[dict], dry_run: bool) -> None:
    """Print one line per task, then a one-line tally.

    The tally is last on purpose: internal/raterjob records the final line as
    the run's note, which `endless jobs list` shows.
    """
    for r in results:
        outcome = r["outcome"]
        task = f"E-{r['task_id']}" if r["task_id"] else "queue"
        if outcome in ("rated", "dry-run", "partial"):
            verb = "would rate" if dry_run else "rated"
            line = f"{click.style('•', fg='cyan')} {task} {verb} {_ratings_text(r)}"
            if outcome == "partial":
                line += f" ({r['detail']})"
            click.echo(line)
        elif outcome == "skipped":
            click.echo(f"  {task} skipped ({r['detail']})")
        else:
            click.echo(f"  {task} left unrated ({r['detail']})", err=True)

    rated = sum(1 for r in results if r["outcome"] in ("rated", "partial", "dry-run"))
    failed = sum(1 for r in results if r["outcome"] == "failed")
    if not results:
        click.echo("Nothing to rate.")
    else:
        verb = "would rate" if dry_run else "rated"
        click.echo(
            f"{verb} {rated} of {len(results)}"
            + (f", {failed} left unrated (WARN-0029)" if failed else "")
        )


def run(
    task_id: int | None = None,
    limit: int = DEFAULT_BATCH_LIMIT,
    project: str | None = None,
    all_projects: bool = False,
    dry_run: bool = False,
) -> None:
    """Back `endless rater run`. Always exits zero — see fail-open."""
    if task_id is not None:
        results = [rate_one(task_id, dry_run=dry_run)]
    else:
        if not all_projects and project is None:
            project = _cwd_project_name()
        results = rate_batch(limit=limit, project=project, dry_run=dry_run)
    render_results(results, dry_run)


def _cwd_project_name() -> str | None:
    """The registered project name for cwd, or None when cwd is in no project.

    Read from `<root>/.endless/config.json` — a file read, not a DB read. None
    falls through to a database-wide sweep.
    """
    root = config.enclosing_project_root()
    if root is None:
        return None
    cfg = config.project_config_read(Path(root)) or {}
    name = cfg.get("name")
    return name if isinstance(name, str) and name else None

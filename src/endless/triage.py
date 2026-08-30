"""The description-sufficiency triager (E-1859).

Every new task is filed `untriaged` — nobody has looked at it. Triage routes it
one hop: `submitted` when the description is already a sufficient spec, or
`unplanned` when design work has to happen first. This module is the thing that
does the routing.

It is glue and nothing else: Go reads → JSON → Go template render → prompt →
model → parse → `emit_event`. Four properties are load-bearing.

**Zero Python SQLite.** Every read goes through `endless-go session-query`
(`untriaged-tasks`, `triage-context`); the write already routes through
`emit_event` → the Go executor. This module must never import `sqlite3` or open
a database — that is the E-1486 boundary this feature was built on the right
side of, and `.endless/tasks/e-1859/verify.sh` asserts it.

**Persisted artifacts only.** The context handed to the model is the task's
description, its parent, its sibling titles, and its linked decisions. NOT the
filing session's transcript. That exclusion is the point: triage must judge what
is written down, so the call is reproducible and matches what a future
implementer will actually have to work from.

**Fail-open, always.** Timeout, missing `claude`, non-zero exit, unparseable
reply, unreadable context → leave the task `untriaged`, transition nothing, exit
zero. This does triple duty: it makes the job idempotent for free (a re-claim
after a lapsed lease re-selects only still-`untriaged` rows), it keeps a model
outage from corrupting the ledger, and it makes the worst failure mode the
status quo — a human routing by hand with `task submit`, which stays the
permanent override.

**The human always wins.** `apply` re-reads the task's status immediately before
emitting and refuses to move a row that is no longer `untriaged`, so a person
who routed the task by hand in the interval is never overwritten.
"""

import functools
import json
import os
import socket
import subprocess
import sys
from pathlib import Path

import click

from endless import config

# How long one sufficiency call may take. Generous: the prompt carries a
# parent, siblings and decisions, and a slow call is not a wrong call.
CALL_TIMEOUT_SECONDS = 120

# Default tasks per sweep. Every selected task becomes a model call, so this is
# a spend cap as much as a batch size — one sweep can never run away.
DEFAULT_BATCH_LIMIT = 10

# The template rendered into the prompt. Three-layer lookup (E-1565/E-1822):
# <project>/.endless/templates/triage/sufficiency.md.local.tmpl (per-developer)
# → .tmpl (committed) → embedded. Wording is therefore a user-tunable lever,
# not product source.
TEMPLATE_NAME = "triage/sufficiency"

# The two routes triage can take, keyed by the token the model must lead with.
_DECISIONS = ("SUBMITTED", "UNPLANNED")

# Rationale is provenance shown to a human reading the ledger, not data anything
# parses. One line, bounded, so a model that ignores "one sentence" cannot write
# an essay into every event payload.
_RATIONALE_MAX = 400


class TriageError(Exception):
    """A read or render failed. Callers fail open — see the module docstring."""


# --- the Go bridge ----------------------------------------------------------


def _endless_go(args: list[str], stdin: str | None = None) -> str:
    """Run `endless-go <args>` with the resolved DB context and return stdout.

    Binary and `--config-dir` resolution are both borrowed from event_bridge
    rather than re-derived, so triage reads open exactly the database triage
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
        raise TriageError(f"{' '.join(args[:2])}: {exc}") from exc
    if result.returncode != 0:
        raise TriageError(
            f"{' '.join(args[:2])}: exit {result.returncode}: "
            f"{result.stderr.strip()}"
        )
    return result.stdout


def select_untriaged(
    limit: int = DEFAULT_BATCH_LIMIT,
    project: str | None = None,
) -> list[dict]:
    """The triage queue: `untriaged` tasks, oldest first, capped at `limit`.

    `project` None means every project — the database-wide sweep the background
    job runs, which has a database but no cwd to resolve a project from.
    """
    args = ["session-query", "untriaged-tasks", "--limit", str(limit)]
    if project:
        args += ["--project", project]
    out = _endless_go(args).strip()
    if not out:
        return []
    try:
        return json.loads(out) or []
    except json.JSONDecodeError as exc:
        raise TriageError(f"untriaged-tasks returned non-JSON: {exc}") from exc


def build_context(task_id: int) -> dict:
    """Assemble one task's triage context from persisted artifacts only."""
    out = _endless_go(
        ["session-query", "triage-context", "--id", str(task_id)]
    ).strip()
    if not out:
        raise TriageError(f"triage-context returned nothing for E-{task_id}")
    try:
        return json.loads(out)
    except json.JSONDecodeError as exc:
        raise TriageError(f"triage-context returned non-JSON: {exc}") from exc


# CLAIM_TTL_SECONDS bounds a claim. It must EXCEED the worst-case model call,
# or a slow-but-healthy claimant gets its task re-claimed underneath it and the
# duplicate spend this claim exists to prevent happens anyway. Headroom covers
# the two Go reads and the render that bracket the call.
CLAIM_TTL_SECONDS = CALL_TIMEOUT_SECONDS + 60


@functools.lru_cache(maxsize=1)
def _claim_owner() -> str:
    """This PYTHON process's claim identity, stable for its lifetime.

    It must be passed explicitly to both claim and release. The Go helper
    defaults the owner to its OWN process identity, and claim and release are
    two separate `endless-go` invocations — so letting it default would make
    every release a no-op against a different owner, stranding the claim until
    its TTL lapsed and blocking the task for CLAIM_TTL_SECONDS.
    """
    return f"{socket.gethostname()}:{os.getpid()}"


def claim(task_id: int) -> bool:
    """Take the per-task triage claim; True when this process won it.

    Both triage paths call this BEFORE the model call. The inline file-time
    child never enters the E-698 job runner, so the runner's job lease cannot
    arbitrate between it and a sweep — without this, a sweep firing mid-call
    re-selects the same still-`untriaged` row and pays for a second model call.
    The post-call status re-read in `apply` keeps the ledger correct either way;
    only the claim protects the spend.

    False is an ordinary outcome (someone else is mid-call on this task), not an
    error. A failure to reach the claim table is also False: refusing to triage
    is the safe direction, and the sweep retries.
    """
    try:
        out = _endless_go([
            "session-query", "triage-claim",
            "--id", str(task_id),
            "--ttl-seconds", str(CLAIM_TTL_SECONDS),
            "--owner", _claim_owner(),
        ])
    except TriageError:
        return False
    return out.strip() == "1"


def release(task_id: int) -> None:
    """Drop this process's claim. Best-effort: a claim left behind simply
    lapses, which is the whole point of time-boxing it rather than locking."""
    try:
        _endless_go([
            "session-query", "triage-release",
            "--id", str(task_id),
            "--owner", _claim_owner(),
        ])
    except TriageError:
        pass


def render_prompt(context: dict) -> str:
    """Render the sufficiency template with `context` as its variables."""
    args = ["template", "render"]
    project = context.get("project")
    if project:
        args += ["--project", project]
    args.append(TEMPLATE_NAME)
    prompt = _endless_go(args, stdin=json.dumps(context))
    if not prompt.strip():
        raise TriageError(f"{TEMPLATE_NAME} rendered empty")
    return prompt


# --- the model call ---------------------------------------------------------


def evaluate(prompt: str) -> tuple[str, str] | None:
    """Ask the model to route one task; return (decision, rationale) or None.

    None is every failure: timeout, missing binary, non-zero exit, a reply that
    does not lead with one of the two tokens. The caller leaves the task
    `untriaged` for the next sweep.
    """
    from endless import internal_claude

    try:
        result = internal_claude.run_internal_claude(
            prompt,
            model=config.internal_model("triage"),
            timeout=CALL_TIMEOUT_SECONDS,
        )
    except (subprocess.TimeoutExpired, FileNotFoundError, OSError):
        return None
    if result.returncode != 0:
        return None
    return parse_reply(result.stdout)


def parse_reply(reply: str) -> tuple[str, str] | None:
    """Parse a constrained reply into (decision, rationale), or None.

    Accepts the first non-blank line, so a model that emits a leading newline
    still parses. A bare `SUBMITTED` with no rationale is accepted — the
    decision is the load-bearing half and the rationale is provenance; refusing
    it would re-run a working call at cost forever.
    """
    for raw in (reply or "").splitlines():
        line = raw.strip()
        if not line:
            continue
        for decision in _DECISIONS:
            if line.upper().startswith(decision):
                rest = line[len(decision):].lstrip(": \t").strip()
                return decision.lower(), rest[:_RATIONALE_MAX]
        return None  # first real line was not a verdict — malformed
    return None


# --- the write --------------------------------------------------------------


def apply(task_id: int, decision: str, rationale: str) -> bool:
    """Emit the transition, guarded on the task still being `untriaged`.

    Returns True when the status moved. Re-reads the row first — the model call
    it follows takes seconds, and in that window a human may have routed the
    task by hand. They win. The same guard is what makes a re-claimed sweep
    transition each task at most once.
    """
    from endless.event_bridge import emit_event

    current = build_context(task_id)
    if current.get("status") != "untriaged":
        return False

    emit_event(
        kind="task.status_changed",
        project=current["project"],
        entity_type="task",
        entity_id=str(task_id),
        payload={
            "old_status": "untriaged",
            "new_status": decision,
            "cascade": False,
            # Provenance, not attribution: WHO decided is actor.kind=triager;
            # WHICH model and WHY live here, so a person who disagrees with a
            # call can see what made it.
            "triage": {
                "model": config.internal_model("triage"),
                "rationale": rationale,
                "template": TEMPLATE_NAME,
            },
        },
        actor_kind="triager",
        # Passed explicitly. Left to default, emit_event would look the path up
        # through Python's `db.query` — putting SQLite back on the one code path
        # this feature exists to keep off it.
        project_root=current.get("project_root"),
    )
    return True


# --- failure reporting ------------------------------------------------------

# The catalog code for "triage could not reach a verdict" (docs/errors.md).
TRIAGE_FAILED_CODE = "ERR-0009"

# Set by spawn_detached in the child's environment so a recorded fault can say
# WHICH path failed. Read only for labelling; it gates nothing.
INLINE_ENV = "ENDLESS_TRIAGE_INLINE"


def _failure_source() -> str:
    return "triage:inline" if os.environ.get(INLINE_ENV) else "triage:sweep"


def report_failure(task_id: int, detail: str, source: str) -> None:
    """Record a fault so a failed triage reaches a surface a user watches.

    `triage.run` is fail-open by design, and the inline path runs DETACHED —
    so without this, a crashed child and a considered no-verdict are
    indistinguishable and neither is written anywhere. The fault store puts it
    on the `session status` / `session monitor` badge and writes the detail to
    its own log; repeats collapse into one incident with an occurrence count,
    so a machine with no `claude` raises one warning, not one per filing.

    Best-effort and silent on failure: a diagnostic must never be the reason a
    fail-open path starts failing closed.
    """
    try:
        _endless_go([
            "errors", "record",
            "--code", TRIAGE_FAILED_CODE,
            "--source", source,
            "--summary", f"triage left E-{task_id} untriaged: {detail}"[:200],
            "--detail", detail,
            # Group by CAUSE, not by task: "claude is missing" is one incident
            # however many tasks hit it.
            "--fingerprint", f"triage-failed:{detail[:80]}",
        ])
    except TriageError:
        pass


# --- entry points -----------------------------------------------------------


def triage_one(task_id: int, dry_run: bool = False) -> dict:
    """Triage a single task. Never raises; the result dict says what happened.

    `outcome` is one of: `routed` (status moved), `dry-run`, `skipped` (no
    longer untriaged), or `failed` (fail-open — the task stays untriaged).

    Every `failed` outcome is ALSO recorded as a fault, so it reaches the
    session-status badge instead of dying in a detached child's closed stdout.
    Fail-open stays fail-open: the task keeps its status and the caller still
    exits zero. Silence was the bug, not the tolerance.
    """
    result = _triage_one(task_id, dry_run=dry_run)
    if result["outcome"] == "failed":
        report_failure(
            task_id,
            result.get("detail") or "no detail recorded",
            _failure_source(),
        )
    return result


def _triage_one(task_id: int, dry_run: bool = False) -> dict:
    """The decision itself. See triage_one for the reporting wrapper."""
    result: dict = {"task_id": task_id, "outcome": "failed", "detail": ""}
    try:
        context = build_context(task_id)
    except TriageError as exc:
        result["detail"] = str(exc)
        return result

    if context.get("status") != "untriaged":
        result["outcome"] = "skipped"
        result["detail"] = f"status is {context.get('status')!r}, not untriaged"
        return result

    # The claim is taken BEFORE the model call and covers everything through
    # the write, so a sweep and an inline child cannot both pay for this task.
    # --dry-run claims too: it makes the same call, so it costs the same.
    if not claim(task_id):
        result["outcome"] = "skipped"
        result["detail"] = "another triage run holds the claim on this task"
        return result

    try:
        try:
            prompt = render_prompt(context)
        except TriageError as exc:
            result["detail"] = str(exc)
            return result

        verdict = evaluate(prompt)
        if verdict is None:
            result["detail"] = "no usable verdict from the model"
            return result

        decision, rationale = verdict
        result["decision"] = decision
        result["rationale"] = rationale

        if dry_run:
            result["outcome"] = "dry-run"
            return result

        try:
            moved = apply(task_id, decision, rationale)
        except (TriageError, click.ClickException) as exc:
            result["detail"] = str(exc)
            return result

        result["outcome"] = "routed" if moved else "skipped"
        if not moved:
            result["detail"] = "left untriaged: the row moved before the write"
        return result
    finally:
        release(task_id)


def triage_batch(
    limit: int = DEFAULT_BATCH_LIMIT,
    project: str | None = None,
    dry_run: bool = False,
) -> list[dict]:
    """Triage up to `limit` untriaged tasks. Never raises."""
    try:
        queue = select_untriaged(limit=limit, project=project)
    except TriageError as exc:
        return [{"task_id": 0, "outcome": "failed", "detail": str(exc)}]
    return [triage_one(int(t["id"]), dry_run=dry_run) for t in queue]


# --- the fire-and-forget inline path ----------------------------------------


# NO_TRIAGE_ENV force-disables the automatic file-time triage for one process.
# The escape hatch a test suite, a bulk import, or a scripted filing run needs:
# without it, every `task add` would fan out a model call nobody asked for.
# It governs only the AUTOMATIC path — an explicit `endless triage run` is a
# deliberate act and still runs.
NO_TRIAGE_ENV = "ENDLESS_NO_TRIAGE"


def _enclosing_worktree() -> Path | None:
    """The self-dev worktree cwd sits inside, or None when cwd is outside one."""
    dir_name = config.worktree_dir_name()
    root = config.gated_worktree_root()
    if dir_name is None or root is None:
        return None
    return (Path(root) / ".endless" / "worktrees" / dir_name).resolve()


def inline_suppressed() -> str:
    """Why automatic file-time triage must not fire here, or "" when it may.

    The hazard being guarded is E-698's, and it is a PAIR of conditions, not
    one: CANDIDATE code writing the MAIN database.

        binary     DB        verdict
        ---------  --------  ------------------------------
        candidate  sandbox   fine — this IS the test
        candidate  main      forbidden — the E-698 hazard
        landed     main      fine — this is triage

    An earlier version suppressed on "self-dev worktree + --db main" alone,
    copying `internal/jobs.suppressedWithReason` without re-deriving it. That
    was wrong here and was the primary reason automatic triage never fired in
    endless's own repo: `--db main` from a worktree is exactly how every agent
    session is told to file, and agents file nearly every task, so the inline
    path was suppressed on the normal path and everything waited on the sweep.

    The suppression is correct THERE because the Claude hooks invoke
    `<worktree>/bin/endless-go`, which is genuinely candidate. It is not true
    here: under `--db main` the child spawned is `sys.argv[0]` — the `endless`
    shim, which `just install` points at the MAIN checkout's editable source —
    and that child writes through `event_bridge._resolve_endless_go()`, which
    prefers the worktree binary only under `--db sandbox`. Both halves are
    landed code.

    So gate on where the CLI that would actually be spawned LIVES, not on
    whether the project is self-dev. Path-gating rather than deleting the clause
    keeps it correct if anyone later runs `uv run endless` from a worktree,
    which today nobody does.
    """
    if os.environ.get(NO_TRIAGE_ENV):
        return f"{NO_TRIAGE_ENV} is set"

    # Anything but the main database is a sandbox or a test DB: candidate code is
    # supposed to write those.
    if config.RESOLVED_CONFIG_DIR != config.main_config_dir():
        return ""

    worktree = _enclosing_worktree()
    if worktree is None:
        return ""

    cli = _self_cli_path()
    if cli is None:
        # Nothing resolvable to spawn; spawn_detached reports that separately.
        return ""

    try:
        Path(cli).resolve().relative_to(worktree)
    except ValueError:
        # Landed CLI + main database — this is ordinary triage, the whole point.
        return ""

    return (
        f"candidate CLI inside the worktree ({cli}) is pinned to the main database"
    )


def spawn_detached(task_id: int) -> bool:
    """Spawn `endless triage run --task E-N` detached; return whether it launched.

    Called from `task add` so an interactive filing is routed within seconds
    without the filing itself waiting on a model call. Deliberately
    fire-and-forget: if the child dies, or never starts, the task simply stays
    `untriaged` and the background sweep picks it up. The fallback is the normal
    path, not an error path — so every failure here is silent and returns False.
    """
    if inline_suppressed():
        return False

    cli = _self_cli_path()
    if cli is None:
        return False

    env = dict(os.environ)
    # Lets the child label its own faults `triage:inline` rather than
    # `triage:sweep` — the two fail for different reasons and a user reading
    # the badge needs to know which path is broken.
    env[INLINE_ENV] = "1"
    if config.RESOLVED_CONFIG_DIR is not None:
        # The config dir is always <XDG_CONFIG_HOME>/endless, so handing the
        # child the parent reproduces this process's DB routing.
        env["XDG_CONFIG_HOME"] = str(config.RESOLVED_CONFIG_DIR.parent)

    argv = [cli, *_child_db_args(), "triage", "run", "--task", str(task_id)]
    # stdout/stderr go to a machine-local log, NOT to DEVNULL. The child records
    # its own fault for a failure it can observe (see report_failure); this log
    # is the floor beneath that — it catches the failures the child cannot
    # report because it died before it could, an import error or a signal.
    # Never inherited: `task add` may be printing to a terminal a user is
    # reading, and a detached child writing over it is worse than silence.
    try:
        log = _child_log_handle()
    except OSError:
        log = subprocess.DEVNULL
    try:
        subprocess.Popen(
            argv,
            stdin=subprocess.DEVNULL,
            stdout=log,
            stderr=subprocess.STDOUT,
            start_new_session=True,
            env=env,
        )
    except OSError:
        return False
    finally:
        if log is not subprocess.DEVNULL:
            log.close()
    return True


def child_log_path() -> Path:
    """Where a detached triage child's output lands."""
    return config.CONFIG_DIR / "logs" / "triage-inline.log"


def _child_log_handle():
    """Append-mode handle for the child log, creating the directory."""
    path = child_log_path()
    path.parent.mkdir(parents=True, exist_ok=True)
    return open(path, "a", buffering=1)


def _self_cli_path() -> str | None:
    """This CLI's own executable, so the child runs the same code this does."""
    argv0 = sys.argv[0] if sys.argv else ""
    if argv0 and os.path.isfile(argv0) and os.access(argv0, os.X_OK):
        return argv0
    import shutil

    return shutil.which("endless")


def _child_db_args() -> list[str]:
    """`--db main|sandbox` for the child, when this process's choice is one.

    Inside a self-dev worktree the child would otherwise be refused by the
    E-1429 gate, since XDG_CONFIG_HOME alone does not satisfy it. Outside a
    self-dev project there is no choice to pass and this is empty.
    """
    resolved = config.RESOLVED_CONFIG_DIR
    if resolved is None:
        return []
    if resolved == config.main_config_dir():
        return ["--db", "main"]
    dir_name = config.worktree_dir_name()
    if dir_name and resolved == config.sandbox_config_dir(dir_name):
        return ["--db", "sandbox"]
    return []


# --- rendering --------------------------------------------------------------


def render_results(results: list[dict], dry_run: bool) -> None:
    """Print one line per task. Terse: this runs unattended far more often
    than a human reads it."""
    if not results:
        click.echo("Nothing untriaged.")
        return
    for r in results:
        outcome = r["outcome"]
        task = f"E-{r['task_id']}" if r["task_id"] else "queue"
        if outcome in ("routed", "dry-run"):
            arrow = "would route to" if dry_run else "→"
            click.echo(
                f"{click.style('•', fg='cyan')} {task} {arrow} "
                f"{click.style(r['decision'], bold=True)}"
                + (f": {r['rationale']}" if r.get("rationale") else "")
            )
        elif outcome == "skipped":
            click.echo(f"  {task} skipped ({r['detail']})")
        else:
            click.echo(
                f"  {task} left untriaged ({r['detail']})", err=True
            )


def run(
    task_id: int | None = None,
    limit: int = DEFAULT_BATCH_LIMIT,
    project: str | None = None,
    all_projects: bool = False,
    dry_run: bool = False,
) -> None:
    """Back `endless triage run`. Always exits zero — see fail-open."""
    if task_id is not None:
        results = [triage_one(task_id, dry_run=dry_run)]
    else:
        if not all_projects and project is None:
            project = _cwd_project_name()
        results = triage_batch(limit=limit, project=project, dry_run=dry_run)
    render_results(results, dry_run)


def _cwd_project_name() -> str | None:
    """The registered project name for cwd, or None when cwd is in no project.

    Read from `<root>/.endless/config.json` — a file read, not a DB read, so
    the module's zero-SQLite property holds. None falls through to a
    database-wide sweep, which is the right default for a cwd that belongs to no
    project at all.
    """
    root = config.enclosing_project_root()
    if root is None:
        return None
    cfg = config.project_config_read(Path(root)) or {}
    name = cfg.get("name")
    return name if isinstance(name, str) and name else None

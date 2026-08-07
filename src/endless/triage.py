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
side of, and `tests/tasks/e-1859-verify.sh` asserts it.

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

import json
import os
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

    `project` None means every project — the ledger-wide sweep the background
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


# --- entry points -----------------------------------------------------------


def triage_one(task_id: int, dry_run: bool = False) -> dict:
    """Triage a single task. Never raises; the result dict says what happened.

    `outcome` is one of: `routed` (status moved), `dry-run`, `skipped` (no
    longer untriaged), or `failed` (fail-open — the task stays untriaged).
    """
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


def inline_suppressed() -> str:
    """Why automatic file-time triage must not fire here, or "" when it may.

    Two suppressions, mirroring `internal/jobs.Suppressed` (E-698) — the same
    hazards apply, because this spawns the same candidate code:

    - `ENDLESS_NO_TRIAGE` set: the explicit opt-out.
    - a self-dev worktree pinned to the real ledger: candidate, unreviewed
      triage code pointed at the developer's actual tasks is exactly the
      pollution the per-worktree sandbox (E-1281) exists to prevent.
    """
    if os.environ.get(NO_TRIAGE_ENV):
        return f"{NO_TRIAGE_ENV} is set"
    if (
        config.gated_worktree_root() is not None
        and config.RESOLVED_CONFIG_DIR == config.main_config_dir()
    ):
        return "self-dev worktree pinned to the real ledger"
    return ""


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
    if config.RESOLVED_CONFIG_DIR is not None:
        # The config dir is always <XDG_CONFIG_HOME>/endless, so handing the
        # child the parent reproduces this process's DB routing.
        env["XDG_CONFIG_HOME"] = str(config.RESOLVED_CONFIG_DIR.parent)

    argv = [cli, *_child_db_args(), "triage", "run", "--task", str(task_id)]
    try:
        subprocess.Popen(
            argv,
            stdin=subprocess.DEVNULL,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            start_new_session=True,
            env=env,
        )
    except OSError:
        return False
    return True


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
    ledger-wide sweep, which is the right default for a cwd that belongs to no
    project at all.
    """
    root = config.enclosing_project_root()
    if root is None:
        return None
    cfg = config.project_config_read(Path(root)) or {}
    name = cfg.get("name")
    return name if isinstance(name, str) and name else None

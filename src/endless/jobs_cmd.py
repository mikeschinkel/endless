"""Thin pass-throughs to the Go job runner and fault record (E-698).

`endless jobs ...` and `endless errors ...` are user-facing verbs, but the
runner and the fault store both live in Go (internal/jobs, internal/faults) —
the session monitor that triggers the runner is Go, and the badge that surfaces
faults is rendered by the Go view. Reimplementing either read path in Python
would be a second source of truth for the same tables.

So these delegate to `endless-go jobs|errors`, threading the resolved --db
context the same way session_cmd.session_status_resolve does, and inheriting
stdout/stderr so the Go side detects the real terminal.
"""

import subprocess


def _run_go(subcommand: str, args: list[str]) -> None:
    """Exec `endless-go <subcommand> <args...>` and propagate its exit status.

    Two resolutions matter here, and both reuse existing machinery rather than
    re-deriving it:

    - WHICH BINARY: event_bridge._resolve_endless_go prefers
      <worktree>/bin/endless-go under `--db sandbox` in a self-dev worktree
      (E-1510). That is load-bearing for these verbs, not a nicety — the
      `jobs`/`errors` subcommands and the tables they read exist only in the
      candidate build, so the PATH-resolved global would refuse with "unknown
      subcommand" until this branch lands.

    - WHICH DATABASE: --config-dir threads the resolved DB context (E-1429), so
      the subprocess opens the same database this CLI resolved instead of being
      refused by the Go-side self-dev worktree gate.

    require_db_context() MUST precede go_db_context_args() here — that is the
    contract go_db_context_args documents, and omitting it silently defeated the
    E-1429 gate for every verb routed through this helper (E-1950).

    Without it, `endless errors clear` or `jobs retry` inside a self-dev worktree
    with no --db threaded no --config-dir at all, and the Go binary fell through
    to E-1368 cwd self-detection: the command ran, reported success, and
    mutated whichever database that guessed. The gate exists precisely so a
    human or an agent cannot hit the wrong DB by omission — a silent guess is
    the failure mode it was built to prevent, so these verbs must refuse rather
    than choose.
    """
    from endless import config
    from endless.event_bridge import _resolve_endless_go

    config.require_db_context()

    result = subprocess.run(
        [_resolve_endless_go(), *config.go_db_context_args(), subcommand, *args],
    )
    if result.returncode != 0:
        raise SystemExit(result.returncode)


def jobs_list() -> None:
    """Show registered jobs and their scheduling state."""
    _run_go("jobs", ["list"])


def jobs_run(job: str | None) -> None:
    """Fire the runner once."""
    args = ["run"]
    if job:
        args += ["--job", job]
    _run_go("jobs", args)


def jobs_retry(name: str) -> None:
    """Clear a job's backoff and make it due now."""
    _run_go("jobs", ["retry", name])


def errors_show(show_all: bool, detail: bool, error_id: int | None,
                project: str = "", all_projects: bool = False) -> None:
    """List recorded errors.

    Project scope is resolved on the Go side from this process's cwd, which the
    subprocess inherits — the same walk `project status` uses, so standing in a
    worktree scopes to the checkout that owns it.
    """
    args = ["show"]
    if show_all:
        args.append("--all")
    if detail:
        args.append("--detail")
    if error_id:
        args += ["--id", str(error_id)]
    args += _project_scope_args(project, all_projects)
    _run_go("errors", args)


def errors_clear(ids: tuple[int, ...], project: str = "",
                 all_projects: bool = False) -> None:
    """Mark errors cleared (never deletes)."""
    # Flags before positionals: Go's flag package stops parsing at the first
    # non-flag argument, so an id ahead of --all-projects would leave the flag
    # unparsed and silently narrow the clear back to the ambient project.
    _run_go("errors", ["clear", *_project_scope_args(project, all_projects),
                       *[str(i) for i in ids]])


def _project_scope_args(project: str, all_projects: bool) -> list[str]:
    """Render the shared --project/--all-projects pair for the Go subcommand.

    Neither flag is passed when neither was given: absence is what tells the Go
    side to resolve the ambient project, and an empty --project would instead
    read as "the project literally named ''".
    """
    if all_projects:
        return ["--all-projects"]
    if project:
        return ["--project", project]
    return []


def errors_record(code: str, summary: str, source: str, detail: str,
                  fingerprint: str) -> None:
    """Record a real catalog fault (E-1859).

    The bridge `endless triage run` needs: it executes detached, where a failure
    has nowhere to go, and the fault store is the surface a user actually
    watches. Distinct from `raise`, which only emits the synthetic test codes.
    """
    args = ["record", "--code", code, "--summary", summary]
    if source:
        args += ["--source", source]
    if detail:
        args += ["--detail", detail]
    if fingerprint:
        args += ["--fingerprint", fingerprint]
    _run_go("errors", args)


def errors_codes() -> None:
    """Print the documented error catalog."""
    _run_go("errors", ["codes"])


def errors_raise(severity: str, summary: str | None, source: str | None,
                 repeat: int) -> None:
    """Record a synthetic fault so the error surface can be exercised."""
    args = ["raise", "--severity", severity]
    if summary:
        args += ["--summary", summary]
    if source:
        args += ["--source", source]
    if repeat != 1:
        args += ["--repeat", str(repeat)]
    _run_go("errors", args)

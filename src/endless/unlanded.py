"""The `unlanded` status and the land that settles it (E-2262).

`unlanded` means the user's own `endless task verify` passed at a recorded
commit and the work is waiting only for `endless worktree land`. The lifecycle:

    unverified --(user's passing verify)--> unlanded --(land)--> assumed
                 <--(the branch moved past the passed commit)--

Three callers, one module:

  - `task verify` calls `record_pass` after a passing run. An agent's run never
    counts (agent_env.present), so only a verify the user ran unlocks a land.
  - `worktree land` calls `require_landable` before anything moves, and
    `settle` after the land is recorded.
  - `task show`, `task list`, `task next`, `task verify` and `worktree land`
    call `reconcile`, which sends a stale `unlanded` back to `unverified`.

Every read goes through `endless-go worktree verify-state`: the answer needs the
database and git, and the database is Go's. The decisions — record, refuse,
settle, reset — are made here, and every write is an ordinary
`task.status_changed` event.
"""

import json
import subprocess
from pathlib import Path

import click

from endless import agent_env, agent_help, config, provenance
from endless.event_bridge import _resolve_endless_go, emit_event

UNLANDED = "unlanded"
UNVERIFIED = "unverified"
ASSUMED = "assumed"
LANDED = "landed"

# The two files a task's verify suite can be (internal/verify ManifestFile and
# ScriptFile). A suite lives in the task's own worktree, at
# .endless/tasks/e-NNNN/.
_SUITE_FILES = ("verify.toml", "verify.sh")


def verify_states(task_num: int | None = None,
                  endless_go_bin: str | None = None) -> list[dict]:
    """The Go side's answer for one task, or for every `unlanded` task.

    Raises the relayed refusal when endless-go could not answer: a caller that
    gates on it (land) must fail closed, and a caller that only tidies (the
    reads) catches it.
    """
    binary = _resolve_endless_go(override=endless_go_bin)
    cmd = [binary, *config.go_db_context_args(), "worktree", "verify-state"]
    if task_num is not None:
        cmd += ["--task", str(task_num)]
    res = subprocess.run(cmd, capture_output=True, text=True)
    if res.returncode != 0:
        raise agent_help.relay(res.stderr or res.stdout,
                               exit_code=res.returncode)
    return provenance.rows_of(json.loads(res.stdout or "[]"))


def _emit_status(state: dict, new_status: str, actor_kind: str = "cli",
                 verified_sha: str | None = None,
                 endless_go_bin: str | None = None) -> None:
    payload = {"old_status": state["status"], "new_status": new_status}
    if verified_sha:
        payload["verified_sha"] = verified_sha
    emit_event(
        kind="task.status_changed",
        project=state["project"],
        entity_type="task",
        entity_id=str(state["task_id"]),
        payload=payload,
        actor_kind=actor_kind,
        project_root=state["root"],
        endless_go_bin=endless_go_bin,
    )


def reconcile(task_num: int | None = None,
              endless_go_bin: str | None = None) -> list[dict]:
    """Send every stale `unlanded` task back to `unverified`.

    Returns the states that were reset, each still carrying its stale_reason.
    The actor is `system`: nobody chose this, the branch moving did.
    """
    reset = []
    for state in verify_states(task_num, endless_go_bin):
        if state["status"] != UNLANDED or not state["stale"]:
            continue
        _emit_status(state, UNVERIFIED, actor_kind="system",
                     endless_go_bin=endless_go_bin)
        reset.append(state)
    return reset


def reconcile_quietly(task_num: int | None = None) -> None:
    """`reconcile` for a read command: never fails the read.

    A reset is announced on stderr, so the read's own output is unchanged. A
    failure to reconcile is announced too, rather than swallowed: the read is
    still right about everything except a stale `unlanded`.

    Stands down where the read itself is about to refuse — a self-dev worktree
    that named no database — so the reader gets that one refusal, not two.
    """
    if config.RESOLVED_CONFIG_DIR is None and config.gated_worktree_root():
        return
    try:
        reset = reconcile(task_num)
    except (click.ClickException, OSError, ValueError) as e:
        detail = e.message if isinstance(e, click.ClickException) else str(e)
        first = (detail.strip().splitlines() or [""])[0]
        agent_help.warn.report(
            f"Could not check unlanded tasks for a moved branch: {first}. The "
            f"rest of this output is unaffected.",
            "whether the installed endless-go needs attention — it could not "
            "answer `worktree verify-state`",
        )
        return
    for state in reset:
        agent_help.warn.no_report(
            f"E-{state['task_id']} is unverified again: "
            f"{state['stale_reason']}.",
            f"Hand the user `endless task verify E-{state['task_id']}` again "
            f"once the branch is ready to land",
        )


def _pinned_db_name() -> str:
    """The database this process is pinned to, as a reader would name it.

    Asked of the resolved directory, not of the flag that was typed, so a
    default pin and an explicit --db read alike.
    """
    resolved = config.RESOLVED_CONFIG_DIR
    if resolved is None or resolved == config.main_config_dir():
        return f"the main database ({config.tilde(config.main_config_dir())})"
    worktree = config.worktree_path()
    if worktree is not None and resolved == config.sandbox_config_dir(worktree):
        return (f"the sandbox database of {worktree.name} "
                f"({config.tilde(resolved)})")
    return f"the database at {config.tilde(resolved)}"


def require_known(task_num: int) -> None:
    """Before a verify runs its suite: refuse when the pinned database holds no
    row for the task (E-2282).

    A pass is recorded in the pinned database, and `worktree land` reads the
    main one. A database that does not know the task could only lose the pass,
    so this says so while the invocation can still be corrected, rather than
    after a full suite run.

    Refuses only on a read that ANSWERS and finds nothing. A read that cannot
    answer lets the verify run: a suite is a proof about a checkout and keeps
    its value when the recording cannot be filed.
    """
    try:
        states = verify_states(task_num)
    except (click.ClickException, OSError, ValueError):
        return
    if states:
        return
    task_id = f"E-{task_num}"
    pinned = _pinned_db_name()
    if config.RESOLVED_CONFIG_DIR in (None, config.main_config_dir()):
        raise agent_help.no_report(
            f"{pinned} has no task {task_id}, so its suite was not run.",
            "Check the task id and retry",
        )
    raise agent_help.no_report(
        f"{pinned} has no task {task_id}, so a pass could not be recorded "
        f"where `worktree land` reads it; its suite was not run.",
        f"Retry as `endless task verify {task_id}`, which names no database "
        f"and records the pass in the main one",
    )


def record_pass(task_num: int, sha: str) -> None:
    """After a passing `task verify`: move the task to `unlanded` at sha.

    Only a run that is not an agent's counts. Re-verifying an `unlanded` task
    records the newer commit. Anything else — a task not yet `unverified`, a
    type with no `unlanded` in its lifecycle — is left alone and said so.

    A failure to read or write leaves the task where it was and warns; land will
    say so too. The one exit that fails the verify is a read that answers and
    finds no such task: `require_known` refuses that before the suite runs, so
    reaching it means something upstream is wrong. The report is already on disk
    by then (verify_cmd._record_passing_run runs first), so the only thing a
    failure costs is the exit code. The caller pins the database
    (verify_cmd.run_verify).
    """
    task_id = f"E-{task_num}"
    if agent_env.present():
        click.echo(
            f"{task_id} stays as it is: this run is an agent's, and only a "
            f"verify you run yourself moves a task to `unlanded`."
        )
        return
    try:
        states = verify_states(task_num)
        if states:
            state = states[0]
            if state["status"] not in (UNVERIFIED, UNLANDED) \
                    or not state["unlanded_lane"]:
                click.echo(
                    f"{task_id} is {state['status']}, so the pass does not "
                    f"move it to `unlanded` — only an `unverified` "
                    f"{state['type']} task waits on a verify."
                    if state["unlanded_lane"] else
                    f"{task_id} is a {state['type']} task, which has no "
                    f"`unlanded` status; the pass is recorded and nothing else "
                    f"changes."
                )
                return
            _emit_status(state, UNLANDED, verified_sha=sha)
    except (click.ClickException, OSError, ValueError) as e:
        detail = e.message if isinstance(e, click.ClickException) else str(e)
        agent_help.warn.report(
            f"{task_id}'s suite passed and its report is recorded, but it "
            f"could not be marked `unlanded`, so it cannot land yet: "
            f"{detail.strip()}",
            "how to clear that failure, then verify again so the task can land",
        )
        return
    if not states:
        raise agent_help.report(
            f"{task_id}'s suite passed and its report is recorded, but the pass "
            f"was not recorded: {_pinned_db_name()} has no task {task_id}, so "
            f"it was not marked `unlanded` and cannot land yet.",
            f"why this verify was pinned to a database without {task_id} — "
            f"`endless task verify {task_id}` records in the main one",
        )
    click.echo(
        f"{task_id} is unlanded at {sha[:10]}: "
        f"`endless worktree land {task_id}` will land and settle it."
    )


def has_suite(worktree: Path, task_num: int) -> bool:
    """Whether the task's worktree carries a verify suite of either form."""
    suite = worktree / ".endless" / "tasks" / f"e-{task_num}"
    return any((suite / name).is_file() for name in _SUITE_FILES)


def require_landable(task_num: int, worktree: Path) -> dict:
    """Refuse a land the task's type and status do not allow; return its state.

    Fails closed: if the state cannot be read, nothing lands. Runs before the
    land moves anything, and before its rebase — afterwards the branch differs
    from the passed commit by everything the base branch gained.
    """
    task_id = f"E-{task_num}"
    reset = reconcile(task_num)
    states = verify_states(task_num)
    if not states:
        raise agent_help.no_report(
            f"No task {task_id} was found, so nothing was landed.",
            "Check the task id and retry",
        )
    state = states[0]

    if not state["lands"]:
        raise agent_help.report(
            f"{task_id} is a {state['type']} task: its deliverable is the "
            f"outcome text, never files, so there is nothing to land. Nothing "
            f"was landed.",
            f"whether to accept {task_id}'s outcome — `endless task complete "
            f"{task_id}` — rather than land it",
        )

    gated = state["requires_verify_suite"] or (
        state["unlanded_lane"] and has_suite(worktree, task_num))
    if not gated or state["status"] == UNLANDED:
        return state

    why = (f" It was `unlanded` until just now: {reset[0]['stale_reason']}."
           if reset else "")
    if state["requires_verify_suite"] and not has_suite(worktree, task_num):
        need = (f"a {state['type']} task lands only after its verify suite "
                f"passes, and {task_id} has none")
        remedy = (f"whether to have a suite written at "
                  f".endless/tasks/e-{task_num}/ and then run `endless task "
                  f"verify {task_id}` yourself")
    else:
        need = ("a land needs a passing `endless task verify` that you ran "
                "yourself, at the branch's current code")
        remedy = (f"running `endless task verify {task_id}` yourself, then "
                  f"landing again")
    raise agent_help.report(
        f"{task_id} is {state['status']}, not unlanded: {need}.{why} Nothing "
        f"was landed.",
        remedy,
    )


def open_post_land(task_num: int,
                   endless_go_bin: str | None = None) -> list[dict]:
    """The task's open post-land items (E-2444), asked of the same endless-go
    the land runs, which knows the schema the land just migrated to."""
    binary = _resolve_endless_go(override=endless_go_bin)
    res = subprocess.run(
        [binary, *config.go_db_context_args(), "session-query",
         "post-land-state", "--id", str(task_num)],
        capture_output=True, text=True)
    if res.returncode != 0:
        raise agent_help.relay(res.stderr or res.stdout, exit_code=res.returncode)
    return json.loads(res.stdout or "{}").get("open") or []


def settle(state: dict, keep_status: bool,
           endless_go_bin: str | None = None) -> None:
    """After a recorded land: set `assumed` when the type settles on land — or
    `landed` when post-land items are still open (E-2444), listing them.

    The land has happened, so a failure here never unwinds it; it is said
    loudly with the one command that finishes the job.
    """
    task_id = f"E-{state['task_id']}"
    if keep_status or not state["settles_on_land"]:
        return
    try:
        still_open = open_post_land(state["task_id"], endless_go_bin)
    except (click.ClickException, OSError, ValueError) as e:
        detail = e.message if isinstance(e, click.ClickException) else str(e)
        agent_help.warn.report(
            f"{task_id} landed, but its post-land items could not be read, so "
            f"it was left {state['status']}: {detail.strip()}",
            f"whether {task_id} has post-land items still to do; if not, "
            f"`endless task update {task_id} --status assumed` settles it",
        )
        return
    if still_open:
        try:
            _emit_status(state, LANDED, endless_go_bin=endless_go_bin)
        except (click.ClickException, OSError) as e:
            detail = e.message if isinstance(e, click.ClickException) else str(e)
            agent_help.warn.no_report(
                f"{task_id} landed with post-land items open, but could not be "
                f"set to landed: {detail.strip()}",
                f"Run `endless task update {task_id} --status landed`",
            )
            return
        click.echo(click.style("•", fg="yellow")
                   + f" {task_id} is now landed: post-land items still open —")
        for it in still_open:
            click.echo(f"    {it['label']} {it['statement']}")
        click.echo(f"  Do them on the landed, installed result and record them "
                   f"with `endless task reconcile {task_id}`; the last one "
                   f"settles it as assumed (`endless guide tasks/post-land`).")
        return
    try:
        _emit_status(state, ASSUMED, endless_go_bin=endless_go_bin)
    except (click.ClickException, OSError) as e:
        detail = e.message if isinstance(e, click.ClickException) else str(e)
        agent_help.warn.no_report(
            f"{task_id} landed, but could not be set to assumed: "
            f"{detail.strip()}",
            f"Run `endless task assume {task_id}`",
        )
        return
    click.echo(click.style("•", fg="green") + f" {task_id} is now assumed")

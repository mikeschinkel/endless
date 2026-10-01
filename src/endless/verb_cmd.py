"""CLI implementations for `endless verb` (E-1117).

Verbs are a top-level domain concept with their own command surface. They
live in <project>/.endless/config.json and ~/.config/endless/config.json
under a `verbs` array of {value, definition, ...} objects.
"""

import json

import click

from endless import agent_help
from endless import matchers
from endless import provenance
from endless import rowcap


def add_verb(
    value: str,
    definition: str | None,
    category: tuple[str, ...] = (),
    machine_only: bool = False,
) -> None:
    if not definition or not definition.strip():
        raise agent_help.no_report(
            f"`verb add {value!r}` was given no --definition. "
            "No verb was registered.",
            f"Retry with --definition \"to ...\" naming the action {value!r} "
            "means, or drop the word if no such definition exists",
            text=(
                f"Adding a verb requires --definition. Define what action '{value}' names.\n"
                f"  Example: endless verb add '{value}' --definition \"to deliberate over\"\n"
                f"  If you cannot write a 'to ___' definition, the word is probably not a verb."
            ),
        )
    if not value or not value.strip():
        raise agent_help.no_report(
            "Verb value is required. No verb was registered.",
            "Retry with the verb as the argument",
            text="Verb value is required.",
        )

    try:
        wrote_project, wrote_machine = matchers.add_verb(
            value=value, definition=definition,
            category=list(category), machine_only=machine_only,
        )
    except ValueError as e:
        # Unreachable in practice — both of matchers.add_verb's ValueErrors are
        # the two checks just above — but classified anyway, because "nothing
        # normally reads this" is a statement about today's call sites and the
        # class has to survive a new one.
        raise agent_help.no_report(
            f"{e}. No verb was registered.",
            "Retry with both a verb value and --definition",
            text=str(e),
        )
    except RuntimeError as e:
        # CONDITIONAL in the inventory, and it stays one: the verb IS on disk
        # (matchers commits only after the file write succeeded), so what is
        # left is a git failure on main whose nature this command cannot see
        # from one attempt. A lock that has since cleared and a conflict the
        # user is mid-way through look identical from here.
        #
        # The re-run trap is why the summary says the write landed: a plain
        # `verb add` of the same value now prints "Already present" and never
        # reaches the commit, so an agent that retries blind concludes it
        # succeeded while verbs.jsonl is still dirty on main.
        raise agent_help.report_if(
            "The verb is written to .endless/verbs.jsonl but git could not "
            f"commit it on main: {e}",
            "git is failing for a reason on main only the user can clear — a "
            "conflict, a hook, or a lock they are holding",
            "remove and re-add the verb, which forces the commit to be retried",
            "the verb is registered on disk and left uncommitted on main",
            text=str(e),
        )

    where = []
    if wrote_project:
        where.append("project")
    if wrote_machine:
        where.append("machine")
    if not where:
        click.echo(
            click.style("•", fg="yellow")
            + f" Already present (no change): verb={value!r}"
        )
        return
    click.echo(
        click.style("•", fg="cyan")
        + f" Added to {' + '.join(where)}: verb={value!r}"
    )


def list_verbs(as_json: bool, limit: int | None = None,
               no_limit: bool = False) -> None:
    cap = rowcap.resolve_cap(limit, no_limit, machine=as_json)
    verbs = matchers.load_all_verbs()
    if as_json:
        click.echo(json.dumps(provenance.attach(verbs), indent=2))
        return
    if not verbs:
        click.echo("No verbs registered.")
        return
    verbs, hidden = rowcap.cap_rows(verbs, cap)
    width = max((len(v.get("value", "")) for v in verbs), default=10)

    def _cat(v: dict) -> str:
        raw = v.get("category")
        if isinstance(raw, str):
            raw = [raw]
        if not isinstance(raw, (list, tuple)):
            raw = ["action"]  # absent ⇒ action (E-1658 read-time default)
        cats = [str(c).strip().lower() for c in raw if isinstance(c, str)] or ["action"]
        return "+".join(sorted(set(cats)))

    catw = max((len(_cat(v)) for v in verbs), default=len("Category"))
    catw = max(catw, len("Category"))
    click.echo(f"{'Verb':<{width}}  {'Category':<{catw}}  Definition")
    click.echo("-" * width + "  " + "-" * catw + "  " + "-" * 40)
    for v in verbs:
        value = v.get("value", "")
        defn = v.get("definition", "")
        click.echo(f"{value:<{width}}  {_cat(v):<{catw}}  {defn}")

    rowcap.echo_footer(hidden)


def update_verb(
    value: str,
    definition: str | None,
    category: tuple[str, ...] = (),
    machine_only: bool = False,
) -> None:
    if not value or not value.strip():
        raise agent_help.no_report(
            "Verb value is required. Nothing was updated.",
            "Retry with the verb as the argument",
            text="Verb value is required.",
        )
    if definition is None and not category:
        raise agent_help.no_report(
            f"`verb update {value!r}` named no field to change. "
            "Nothing was updated.",
            "Retry with --definition and/or --category",
            text=(
                "Nothing to update. Pass --definition and/or --category.\n"
                f"  Example: endless verb update '{value}' --category investigation\n"
                "  What you omit is left exactly as it is — that is the point of update."
            ),
        )

    try:
        result = matchers.update_verb(
            value=value, definition=definition,
            category=list(category) if category else None,
            machine_only=machine_only,
        )
    except matchers.UnknownVerbError:
        raise agent_help.no_report(
            f"No verb matched value={value!r}, so there was nothing to update. "
            "Nothing was changed.",
            "Run `endless verb list` and retry with the registered spelling, "
            "or register the verb first with `endless verb add`",
            text=(
                f"No verb matched: value={value!r}\n"
                f"  Register it first: endless verb add '{value}' --definition \"...\"\n"
                f"  Or run 'endless verb list' to see what is registered."
            ),
        )
    except ValueError as e:
        raise agent_help.no_report(
            f"{e}. Nothing was updated.",
            "Correct the --definition or --category value and retry",
            text=str(e),
        )
    except RuntimeError as e:
        # Same shape as add_verb's, with the re-run trap one step worse: a
        # plain re-run of an update that already landed on disk prints "Already
        # set (no change)" and never commits, so the remedy has to be a change
        # that forces a rewrite rather than the same command again.
        raise agent_help.report_if(
            "The update is written to .endless/verbs.jsonl but git could not "
            f"commit it on main: {e}",
            "git is failing for a reason on main only the user can clear — a "
            "conflict, a hook, or a lock they are holding",
            "re-apply a change that forces a rewrite, which retries the commit",
            "the updated verb is on disk and left uncommitted on main",
            text=str(e),
        )

    fields = ", ".join(result.fields)
    if not result.layers:
        click.echo(
            click.style("•", fg="yellow")
            + f" Already set (no change): verb={result.value!r} ({fields})"
        )
        return
    click.echo(
        click.style("•", fg="cyan")
        + f" Updated in {' + '.join(result.layers)}: verb={result.value!r} ({fields})"
    )
    if result.materialized:
        # The new line holds only what was passed, so it reads as a truncated
        # entry unless the reader knows the rest still resolves from below.
        click.echo(
            f"  New override for {result.value!r} in the {result.layers[0]} layer"
            f" — the fields you did not pass still resolve from below."
        )


def remove_verb(value: str, machine_only: bool) -> None:
    pr, mr = matchers.remove_verb(value=value, machine_only=machine_only)
    if pr == 0 and mr == 0:
        raise agent_help.no_report(
            f"No verb matched value={value!r}. Nothing was removed — if the "
            "goal was that the verb not be registered, it already is not.",
            "Run `endless verb list` and retry with the registered spelling, "
            "or treat the goal as already met",
            text=f"No verb matched: value={value!r}",
        )
    where = []
    if pr:
        where.append(f"project ({pr})")
    if mr:
        where.append(f"machine ({mr})")
    click.echo(
        click.style("•", fg="cyan")
        + f" Removed from {', '.join(where)}: verb={value!r}"
    )

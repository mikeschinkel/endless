"""CLI implementations for `endless verb` (E-1117).

Verbs are a top-level domain concept with their own command surface. They
live in <project>/.endless/config.json and ~/.config/endless/config.json
under a `verbs` array of {value, definition, ...} objects.
"""

import json

import click

from endless import matchers


def add_verb(value: str, definition: str | None, machine_only: bool) -> None:
    if not definition or not definition.strip():
        raise click.ClickException(
            f"Adding a verb requires --definition. Define what action '{value}' names.\n"
            f"  Example: endless verb add '{value}' --definition \"to deliberate over\"\n"
            f"  If you cannot write a 'to ___' definition, the word is probably not a verb."
        )
    if not value or not value.strip():
        raise click.ClickException("Verb value is required.")

    try:
        wrote_project, wrote_machine = matchers.add_verb(
            value=value, definition=definition, machine_only=machine_only,
        )
    except ValueError as e:
        raise click.ClickException(str(e))

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


def list_verbs(as_json: bool) -> None:
    verbs = matchers.load_all_verbs()
    if as_json:
        click.echo(json.dumps(verbs, indent=2))
        return
    if not verbs:
        click.echo("No verbs registered.")
        return
    width = max((len(v.get("value", "")) for v in verbs), default=10)
    click.echo(f"{'Verb':<{width}}  Definition")
    click.echo("-" * width + "  " + "-" * 50)
    for v in verbs:
        value = v.get("value", "")
        defn = v.get("definition", "")
        click.echo(f"{value:<{width}}  {defn}")


def remove_verb(value: str, machine_only: bool) -> None:
    pr, mr = matchers.remove_verb(value=value, machine_only=machine_only)
    if pr == 0 and mr == 0:
        raise click.ClickException(f"No verb matched: value={value!r}")
    where = []
    if pr:
        where.append(f"project ({pr})")
    if mr:
        where.append(f"machine ({mr})")
    click.echo(
        click.style("•", fg="cyan")
        + f" Removed from {', '.join(where)}: verb={value!r}"
    )

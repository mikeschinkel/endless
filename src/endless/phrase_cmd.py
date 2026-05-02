"""CLI implementations for 'endless phrase'.

Mutations write to <project>/.endless/config.json AND ~/.config/endless/config.json
by default (project-first, machine mirror). The --machine-only flag skips the
project write. Lookups merge both layers additively.
"""

import json
import re

import click

from endless import matchers


VALID_METHODS = ("exact", "substring", "regex")
DEFAULT_METHOD_FOR_TYPE = {
    "pivot": "substring",
}


def _default_method(type_: str) -> str:
    return DEFAULT_METHOD_FOR_TYPE.get(type_, "regex")


def _validate_type(type_: str) -> None:
    if not type_ or not re.fullmatch(r"[a-z][a-z0-9-]*", type_):
        raise click.ClickException(
            f"Invalid type {type_!r}. Must be lowercase kebab-case "
            f"(e.g., verb, pivot, start, complete, beacon)."
        )


def _validate_scope(scope: str | None) -> None:
    if scope is None:
        return
    if not re.fullmatch(r"[a-z][a-z0-9-]*", scope):
        raise click.ClickException(
            f"Invalid scope {scope!r}. Must be lowercase kebab-case."
        )


def _validate_regex(pattern: str) -> None:
    try:
        re.compile(pattern)
    except re.error as e:
        raise click.ClickException(f"Invalid regex: {e}")


def add_phrase(
    type_: str,
    value: str,
    scope: str | None,
    method: str | None,
    case_sensitive: bool,
    machine_only: bool,
) -> None:
    _validate_type(type_)
    _validate_scope(scope)
    if type_ == "verb":
        raise click.ClickException(
            "verbs are managed via 'endless verb add' (E-1117), not 'phrase add verb'"
        )
    if method is None:
        method = _default_method(type_)
    if method not in VALID_METHODS:
        raise click.ClickException(
            f"Invalid method {method!r}. Valid: {', '.join(VALID_METHODS)}"
        )
    if method == "regex":
        _validate_regex(value)

    wrote_project, wrote_machine = matchers.add_match_value(
        type_=type_,
        value=value,
        scope=scope,
        method=method,
        case_sensitive=case_sensitive,
        machine_only=machine_only,
    )

    where = []
    if wrote_project:
        where.append("project")
    if wrote_machine:
        where.append("machine")
    if not where:
        click.echo(
            click.style("•", fg="yellow")
            + f" Already present (no change): "
            + _describe(type_, value, scope, method, case_sensitive)
        )
        return

    click.echo(
        click.style("•", fg="cyan")
        + f" Added to {' + '.join(where)}: "
        + _describe(type_, value, scope, method, case_sensitive)
    )


def list_phrases(
    type_filter: str | None,
    scope_filter: str | None,
    show_disabled: bool,
    as_json: bool,
) -> None:
    all_matchers = matchers.load_all_matchers()

    rows = []
    for m in all_matchers:
        if type_filter and m.get("type") != type_filter:
            continue
        if scope_filter and m.get("scope") != scope_filter:
            continue
        if not show_disabled and m.get("enabled", True) is False:
            continue
        rows.append(m)

    if as_json:
        click.echo(json.dumps(rows, indent=2))
        return

    if not rows:
        click.echo("No matchers match.")
        return

    click.echo(
        f"{'Type':<10}  {'Scope':<8}  {'Method':<10}  {'CS':<3}  {'On':<3}  Match"
    )
    click.echo("-" * 10 + "  " + "-" * 8 + "  " + "-" * 10 + "  "
               + "-" * 3 + "  " + "-" * 3 + "  " + "-" * 40)
    for m in rows:
        cs = "y" if m.get("case_sensitive") else "n"
        on = "y" if m.get("enabled", True) else "n"
        match = m.get("match")
        if isinstance(match, list):
            preview = ", ".join(match[:5])
            if len(match) > 5:
                preview += f", ... (+{len(match) - 5})"
        else:
            preview = str(match)
        if len(preview) > 50:
            preview = preview[:49] + "…"
        click.echo(
            f"{m.get('type', ''):<10}  {(m.get('scope') or '-'):<8}  "
            f"{m.get('method', ''):<10}  {cs:<3}  {on:<3}  {preview}"
        )


def _describe(type_, value, scope, method, case_sensitive):
    parts = [f"type={type_}"]
    if scope:
        parts.append(f"scope={scope}")
    parts.append(f"method={method}")
    if case_sensitive:
        parts.append("case-sensitive")
    parts.append(f"value={value!r}")
    return " ".join(parts)


def disable_phrase(type_: str, value: str, scope: str | None, machine_only: bool) -> None:
    _validate_type(type_)
    _validate_scope(scope)
    pr, mr = matchers.set_enabled(type_=type_, value=value, scope=scope, enabled=False)
    if pr == 0 and mr == 0:
        raise click.ClickException(
            f"No enabled matcher matched: {_describe(type_, value, scope, '?', False)}"
        )
    where = []
    if pr:
        where.append(f"project ({pr})")
    if mr:
        where.append(f"machine ({mr})")
    click.echo(click.style("•", fg="cyan") + f" Disabled in {', '.join(where)}: type={type_} value={value!r}")


def enable_phrase(type_: str, value: str, scope: str | None) -> None:
    _validate_type(type_)
    _validate_scope(scope)
    pr, mr = matchers.set_enabled(type_=type_, value=value, scope=scope, enabled=True)
    if pr == 0 and mr == 0:
        raise click.ClickException(
            f"No disabled matcher matched: {_describe(type_, value, scope, '?', False)}"
        )
    where = []
    if pr:
        where.append(f"project ({pr})")
    if mr:
        where.append(f"machine ({mr})")
    click.echo(click.style("•", fg="cyan") + f" Enabled in {', '.join(where)}: type={type_} value={value!r}")


def remove_phrase(type_: str, value: str, scope: str | None, machine_only: bool) -> None:
    _validate_type(type_)
    _validate_scope(scope)
    pr, mr = matchers.remove_match_value(
        type_=type_, value=value, scope=scope, machine_only=machine_only,
    )
    if pr == 0 and mr == 0:
        raise click.ClickException(
            f"No matcher matched: {_describe(type_, value, scope, '?', False)}"
        )
    where = []
    if pr:
        where.append(f"project ({pr})")
    if mr:
        where.append(f"machine ({mr})")
    click.echo(click.style("•", fg="cyan") + f" Removed from {', '.join(where)}: type={type_} value={value!r}")



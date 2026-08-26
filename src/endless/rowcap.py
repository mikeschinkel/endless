"""One row cap for every listing surface, and it announces itself (E-2071).

A list that gets cut off says nothing about it. Before this module, `endless
task list` rendered all 1360 live rows and `endless task search` rendered a
silent 20 under a "20 match(es)" line indistinguishable from exactly twenty
matches — so a caller that could not predict the result size bounded it
downstream with `head`, and `head` is silent. That silence turned two searches
in one session into false "no existing task" conclusions.

The rule this module enforces, borrowed from the session-status view (E-1914):
**nothing is ever omitted without a trace.** Every capped render prints a footer
naming both how many rows it dropped and the flag that shows them.

Two escapes, and only two:

- `--limit N` asks for a different cap.
- `--no-limit` removes it.

MACHINE-READABLE renders (`--json`, `--tsv`) are UNCAPPED by default. Capping
them would be strictly worse than the original problem: a consumer parsing 20 of
1360 rows has no footer to read and no way to notice. `--llm` is capped, because
it is prose an agent reads and the footer lands in it like any other line. An
explicit `--limit N` still caps a machine render — explicit is explicit.
"""

import click

# Every listing surface caps at the same number, so the cap is one fact to know
# rather than one per command. Twenty fills a screen and leaves the footer
# visible above the prompt.
DEFAULT_ROW_CAP = 20

# The flag the footer names. Spelled once here so the help text, the error, and
# every footer cannot drift apart.
NO_LIMIT_FLAG = "--no-limit"


def resolve_cap(
    limit: int | None,
    no_limit: bool,
    *,
    machine: bool = False,
) -> int | None:
    """Resolve the two flags to a row cap, or None for uncapped.

    `limit` is the raw `--limit` value: None when the flag was not passed, which
    is why every caller declares it `default=None` rather than `default=20`.
    Without that, an explicit `--limit 20` on a `--json` render would be
    indistinguishable from the default and silently ignored.

    `machine` marks a payload a program parses (`--json`, `--tsv`). It defaults
    to uncapped; an explicit `--limit` still applies.
    """
    if no_limit:
        if limit is not None:
            raise click.UsageError(
                f"--limit and {NO_LIMIT_FLAG} are mutually exclusive: "
                f"--limit sets a cap, {NO_LIMIT_FLAG} removes it."
            )
        return None
    if limit is not None:
        if limit < 1:
            raise click.UsageError(
                f"--limit must be at least 1. To render every row, "
                f"pass {NO_LIMIT_FLAG}."
            )
        return limit
    if machine:
        return None
    return DEFAULT_ROW_CAP


def cap_rows(rows, cap: int | None):
    """Split `rows` at `cap`, returning (shown, hidden_count).

    `cap` of None is uncapped. The caller holds the FULL result set — the cap is
    applied here, in the renderer, not pushed into SQL — because the footer has
    to name an exact remainder, and a query that stopped at the cap cannot say
    how many rows it did not fetch. These are local SQLite reads over a table
    measured in thousands of rows, so fetching the tail costs less than the
    second COUNT query the alternative would need.
    """
    if cap is None or len(rows) <= cap:
        return rows, 0
    return rows[:cap], len(rows) - cap


def footer(hidden: int, *, llm: bool = False) -> str:
    """The omission trace, in the session-status idiom ('… N hidden (--flag)').

    Names the count and the flag, so a truncated listing documents its own way
    out. Never called with hidden == 0; `echo_footer` is the guard.
    """
    noun = "row" if hidden == 1 else "rows"
    if llm:
        return f"# {hidden} more {noun} ({NO_LIMIT_FLAG})"
    return f"… {hidden} more {noun} ({NO_LIMIT_FLAG})"


def echo_footer(hidden: int, *, llm: bool = False, err: bool = False) -> None:
    """Print the omission trace when anything was omitted, and nothing when not.

    The footer is REQUIRED whenever the count is non-zero — that is the whole
    property this module exists to guarantee — so every capped render ends with
    this call rather than deciding for itself.

    `err` sends it to stderr, which is how a MACHINE format keeps the guarantee
    without corrupting itself. A `--json` or `--tsv` payload only ever truncates
    under an explicit `--limit`, but "the caller asked for it" is not a reason to
    drop the trace — it is a reason to put the trace somewhere `| jq` will not
    choke on.
    """
    if hidden <= 0:
        return
    line = footer(hidden, llm=llm)
    click.echo(line if (llm or err) else click.style(line, dim=True), err=err)


def limit_options(f):
    """Attach `--limit` / `--no-limit` to a listing command.

    One decorator so the two flags cannot appear on one command and not another,
    and so their help text is written once.
    """
    f = click.option(
        NO_LIMIT_FLAG, "no_limit", is_flag=True,
        help="Render every row, however many there are.",
    )(f)
    f = click.option(
        "--limit", default=None, type=int,
        help=f"Max rows to render (default: {DEFAULT_ROW_CAP}; "
             f"{NO_LIMIT_FLAG} for all). Machine formats (--json/--tsv) are "
             f"uncapped unless you pass this.",
    )(f)
    return f

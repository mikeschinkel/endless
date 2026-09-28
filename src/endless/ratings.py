"""The two task rating axes, complexity and risk (E-1813, ED-1538/ED-1539).

Complexity is how much human-AI interaction is needed to nail down the
specifics; risk is the blast radius if the work is wrong. They are independent,
which is why they are two axes rather than one scalar.

The Go enum `internal/rating` is the source of truth, and the executor rejects
an unknown slug before it reaches the database. The vocabulary here is the
CLI's copy of it, used to validate a flag early and to build the SQL that reads
the slugs back. It holds no database access of its own.

The agent proposes both ratings at submit; the user ratifies them at approve.
A rating never moves status.
"""

import click

# In rank order. The ids are rating.Level's: 2 and 4 are deliberately unseeded
# so medium-low and medium-high can be added without renumbering.
LEVELS = ("low", "medium", "high")

# The flag value that clears a rating (unrated). Also the filter value that
# selects unrated tasks.
NONE = "none"

AXES = ("complexity", "risk")

_COLUMN = {"complexity": "complexity_id", "risk": "risk_id"}
_TABLE = {"complexity": "complexity_levels", "risk": "risk_levels"}

CHOICES = click.Choice([*LEVELS, NONE], case_sensitive=False)


def normalize(value: str | None) -> str | None:
    """Lower-case a flag value; None stays None (the flag was not given)."""
    return value.strip().lower() if value is not None else None


def select_sql(alias: str) -> str:
    """SELECT-list fragment reading both ratings as slugs, aliased by axis."""
    return ", ".join(
        f"(SELECT slug FROM {_TABLE[a]} WHERE id = {alias}.{_COLUMN[a]}) AS {a}"
        for a in AXES
    )


def filter_sql(alias: str, axis: str, value: str) -> tuple[str, list]:
    """WHERE fragment (leading ' AND') for a --complexity/--risk filter.

    `none` selects unrated tasks; a level selects that level.
    """
    col = f"{alias}.{_COLUMN[axis]}"
    if value == NONE:
        return f" AND {col} IS NULL", []
    return (
        f" AND {col} = (SELECT id FROM {_TABLE[axis]} WHERE slug = ?)",
        [value],
    )


def sort_sql(alias: str, axis: str) -> str:
    """ORDER BY expression: low first, unrated last."""
    return f"COALESCE({alias}.{_COLUMN[axis]}, 99)"


def missing(complexity: str | None, risk: str | None) -> list[str]:
    """The axes still unrated, in axis order."""
    return [a for a, v in zip(AXES, (complexity, risk)) if not v]


def display(value: str | None) -> str:
    """One rating for a human: its slug, or 'unrated'."""
    return value or "unrated"


def pair(complexity: str | None, risk: str | None) -> str:
    """Both ratings compactly, for a table cell: 'low/high', '-' when unrated."""
    if not complexity and not risk:
        return "-"
    return f"{complexity or '-'}/{risk or '-'}"

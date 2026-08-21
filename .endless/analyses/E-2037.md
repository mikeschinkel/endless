# "Ledger" already means something else

The db-ledger (`.endless/db-ledger/*.jsonl`) is the permanent record of task
state; the SQLite database is a rebuildable projection of it. CLAUDE.md says so
in its own words. Using "ledger" for the database collapses the distinction that
architecture rests on, and it does it in the two places most likely to mislead:
agent-facing comments, and the user-facing `--db` refusal.

## Scope

31 sites in live code, docs and templates. Two are user-facing:

    src/endless/config.py:328   "  --db main     the real ledger — managing the project"
    src/endless/cli.py:302      "--db main (the real ledger) or --db sandbox (this worktree's test DB). "

The rest are comments, docstrings and two guide pages. `internal/monitor/db.go`
carries the most, and is where the term does the most damage — it is the file
that decides which database a process opens.

## Explicitly OUT of scope: `tests/tasks/*-verify.sh`

There are 80 more occurrences there. Leave every one of them.

A verify script is a pre-land gate, valid only immediately before its own land,
in its own worktree. A landed one is spent — nothing should ever run it again —
so its wording cannot mislead anyone and editing it is churn against an artifact
with no future. E-1975 already filed and declined a task (E-2033) that misread
this rule; do not re-derive it.

## Wording

`--db main` and `--db sandbox` already establish the vocabulary. Say "the main
database" and "the sandbox database" (or "this worktree's sandbox"). Avoid "real
ledger", "the real DB" and "the ledger" wherever the SQLite database is meant.

The user-facing strings deserve Mike's eye rather than a mechanical
substitution: the refusal text is the first thing an agent hits when it gets the
flag wrong, and it is currently the clearest statement in the product of what
the two databases are for.

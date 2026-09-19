# Scrub Endless's own IDs from user-visible help, guide, and output

## Principle

A user of Endless runs it against their own project. Their task numbering has
nothing to do with Endless's. Any `E-`, `ES-`, or `ED-` id that reaches their
screen is at best noise and at worst a wrong cross-reference into a ledger they
cannot read. No help text, guide page, or runtime message may cite one.

The boundary: **user-visible text is scrubbed; source-code comments are not.**
Comments carry self-dev provenance, are invisible to users, and stay.

## Measured scope

**Help output — 28 commands, 50 occurrences.** Worst offenders:

    13  endless session order --help
     3  endless session --help
     2  endless worktree land --help, endless worktree --help,
        endless task list --help, endless session use --help,
        endless session task add/remove --help,
        endless session snapshot/snapshot add --help
     1  endless {triage,shell-init,jobs} --help
     1  endless task {update,unsettled,search,replace,recent,next,continue,active} --help
     1  endless worktree {reap,check} --help
     1  endless errors --help, endless errors raise --help
     1  endless epic list --help, endless db path --help,
        endless session task --help

Most come from Click command docstrings, whose first line becomes the parent
group's short help — that is why three ids appear in `endless session --help`.

**Guide — 80 occurrences across 5 files:**

    docs/guide/tasks.md           42
    docs/guide/sessions.md        18
    docs/guide/orchestration.md   12
    docs/guide/reference.md        5
    docs/guide/decisions.md        3

`endless guide` renders these files, so fixing the markdown fixes both.

**Runtime strings:** a scan of `click.echo` / `ClickException` lines found zero.
Re-run the scan after the help pass rather than assuming it stays zero.

## How to rewrite

Two cases, and the difference matters:

1. **Trailing provenance citation** — `Set this session's task implementation
   order (E-1683).` The id adds nothing for a user. Delete the parenthetical.

2. **The id standing in for a fact** — `enforced for these verbs since E-1950`,
   `A removed task keeps its row (E-1929)`. Deleting alone leaves a dangling or
   weakened sentence. State the fact plainly instead: what the behavior is, not
   which ticket introduced it. Never replace one id with another reference the
   user also cannot resolve.

If a sentence needs the id to make sense, the sentence is describing Endless's
development history rather than its behavior, and belongs in the ledger.

## Guard

Add a test that fails on regression, since this class of text is written fresh
with every new command:

- Walk the full Click command tree, capture each `--help`, and assert no
  `\b(E|ES|ED)-\d+` match.
- Assert the same over `docs/guide/*.md`.

A recursive help walker already exists in scratch form and is worth
reimplementing properly in the test: enumerate subcommands by parsing the
`Commands:` block of each group's help, and recurse.

Run `just test` and `just build`.

## Verification

`tests/tasks/e-NNNN-verify.sh` asserting:

1. `endless session --help` contains no id (the reported symptom).
2. `endless session order --help` contains no id (the worst single offender).
3. The full recursive help walk yields zero matches.
4. `docs/guide/*.md` yields zero matches.
5. Spot-check that rewritten sentences still read correctly where an id was
   load-bearing rather than a trailing citation.

# Delivered

`endless verb update <value>` with `--definition`, `--category` (repeatable) and
`--machine-only`. Writes only the fields passed; every other field — including
ones no flag covers — is left exactly as found.

- `matchers.update_verb` / `_update_verb_in_file` / `_canonical_verb_value`,
  `UnknownVerbError`, `VerbUpdate`; `_normalize_categories` factored out of
  `add_verb` and shared.
- `verb_cmd.update_verb` and the Click command in `cli.py`.
- `tests/test_verb_update.py` (23 cases); `.endless/tasks/e-2114/verify.sh`
  (28 assertions).
- `docs/guide/tasks.md` verb section.

## Two design calls, both put to Mike before implementing

1. **Built-ins are corrected by materializing an override**, not refused.
   Chosen over "refuse and point at `verb add`".
2. **`--machine-only` is a layer scope**, exactly as on `add`/`remove` — not a
   field that relocates the verb between layers.

Implementation note discovered after those answers: targeting had to be
**scope-first, not presence-first**. `_ensure_default_seeds` writes every
`DEFAULT_VERBS` entry into the machine `verbs.jsonl` on first run, so a built-in
IS present in a file; following presence would have sent a project-scoped
correction machine-wide, contradicting the behaviour chosen in (1). The
addressed layer is written whether or not it holds the verb; the other layer is
kept in step but never created.

# Scope grown beyond the description

**A stray self-dev id in `docs/guide/orchestration.md`.** Found by this task's
own project-wide regression: `tests/test_no_self_dev_ids.py` was red on main
because the worktree-reaper paragraph ended with "(E-2111 is where the two are
reconciled)". Unrelated to `verb update`.

First filed as E-2119, which was the wrong call — a single sentence, in a tree
already open, unbreaking a suite this task had just run, with E-2114 still live
in the session that landed it (ED-1550's exemption to the no-reopen rule).
E-2119 is now `obsolete`; the fix is commit `bc03b7fd` on this task's branch,
landed with the second land.

That misfiling is itself filed as **E-2120** — the handoff's "could it
reasonably be done now" test reads as a topical purity test rather than a cost
test, which is what routed this to `task add`.

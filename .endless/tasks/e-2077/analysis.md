## Why relocation, not just a stronger rule

The rule already exists and has been violated repeatedly. What makes it fragile
is that the scripts are shelved with a real test suite: `just test` runs
tests/, the scripts sit at tests/tasks/, and "check for regressions" reads as
"run what is under tests/". Nothing about the path says "these execute against
live state, one at a time, by the session that owns the task".

## What is actually unsafe

Relocation stops accidental sweeps. It does not stop a deliberate wrong run,
and it does not make the scripts safe. The underlying hazard is that some of
them reach the live environment at all:

- e-1202-verify pipes a synthetic payload into the real hook binary with no
  --config-dir and no XDG_CONFIG_HOME override, so the hook lazy-creates a
  session row in the main database and binds it to $TMUX_PANE.
- Others shell out to git, tmux and endless-go; how many are hermetic is
  unknown and worth measuring rather than assuming.

Newer scripts (e-2067, e-2071) build a throwaway config dir under the cache
sandbox root and run the CLI from outside the repo, so the pattern for a
hermetic fixture already exists in-tree and can be the model.

## Scope to settle before moving anything

- Where they land. `.verify/` keeps them near .endless/ and out of tests/;
  a sibling of tests/ that is not tests/ is the only hard requirement.
- Every reference that resolves a script by task id: the justfile, docs/guide,
  `endless verify`, the scripts' own "Run from inside the worktree" headers,
  and any spawn/handoff prose that names the path.
- Whether a guard belongs in the harness — refusing a bulk invocation outright
  beats relying on the reader to notice the directory changed.
- An audit of which scripts touch live state, and whether those should be made
  hermetic on the same pass or tracked separately.

# Plan — `just verify` self_dev recipe

A dev-only Justfile recipe to run a task's verification while developing Endless itself —
the self_dev counterpart to `just test`.

## Behavior

`just verify [task-id]` — invokes the PRODUCT verb `endless verify` against Endless's own
`.endless/tasks/` suites. With no id, defaults to the worktree's bound task (same resolution
other endless commands use). Runs with the worktree-built binary + sandbox routing so the
candidate code is exercised, not the global install.

## Constraint (just-is-dev-only)

The recipe contains NO verification logic — it only wraps `endless verify` (and sets up the
self_dev context). All verification behavior lives in the product runner (E-1603). If the recipe
starts wanting logic, that logic belongs in `endless verify`, not the Justfile.

## Deps

- Blocked by E-1603 (the product `endless verify` runner it wraps).
- Pairs with E-1605 (first dogfood suite) — `just verify` is how that suite gets run in dev.

## Verify

- `just verify E-NNNN` runs that task's checks via the worktree binary against the sandbox and
  reports the merged result.
- `just verify` with no arg resolves the worktree's task and does the same.
- The recipe is a thin wrapper (grep confirms no assertion/normalization logic in the Justfile).

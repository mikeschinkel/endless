# Let projects configure their own allowed paths for the path gate — PLAN

Phase: later. Relates to the inline-content path gate (E-1744).

## Context
The path gate (E-1744) blocks absolute paths in inline flag content. Its built-in
**allowed paths** — a data-driven *table* of endless's own resolved dirs
(`<config_dir>/endless/`, `<cache_dir>/endless/`, XDG-resolved, NOT hardcoded) —
exempt the well-known endless locations that legitimately appear in plans from
outside the project root, so a user need not pass `--allow-path` for them.

But endless is used as a PRODUCT by ~40 other projects, each with its OWN
well-known outside-root paths that legitimately appear in that project's plans
(build dirs, shared toolchains, sibling repos, etc.). Those projects need a way to
declare their allowed paths once, rather than passing `--allow-path` on every
command.

## Approach
- Add an `allowed_paths` array (list of absolute-path regexes) to project config
  `.endless/config.json`.
- The gate composes its effective allowed set from ONE table/merge point:
  built-in endless dirs (E-1744's table) + project-config `allowed_paths` +
  per-invocation `--allow-path` flags. Reuse the exact table mechanism E-1744
  establishes — extend the table from config, don't fork the logic.
- Terminology: **"allowed paths"** everywhere (config key, docs, help) — NOT
  "whitelist".

## Pull-forward note
If E-1744's built-in table is structured so that merging a config-provided
`allowed_paths` array is trivial, this can be folded into E-1744 now rather than
waiting. Filed `later` because it's the downstream-product surface, not needed for
endless's own self-dev use; promote if the config merge turns out to be a few lines.

## Deliverable — verify script
Generate `tests/tasks/e-<id>-verify.sh` (shape per `tests/tasks/e-1577-verify.sh`):
an absolute path matching a project-config `allowed_paths` entry is accepted; a
non-matching absolute path still blocks; built-in endless dirs and `--allow-path`
still work. On completion set `unverified` and hand off exactly:
`esu && ./tests/tasks/e-<id>-verify.sh`.

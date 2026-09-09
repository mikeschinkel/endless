# Plan as shipped (2026-09-08)

The reopened task's remedy, per the analysis: stop writing the generated
per-worktree override into the tracked `.claude/settings.json`, and stop hiding
it with `git update-index --skip-worktree`. Write it to Claude Code's Local-tier
`.claude/settings.local.json`, which this repo already gitignores. Nothing
tracked is modified, so no rebase can ever be refused over it.

## The merge semantics were measured, not assumed

The analysis required this before implementing. A throwaway project with both
files and a hook in each, run under Claude Code 2.1.236:

    scope=local    MERGE_A=from-project MERGE_B=from-local MERGE_SHARED=from-local
    scope=project  MERGE_A=from-project MERGE_B=from-local MERGE_SHARED=from-local

`hooks` CONCATENATE across the two scopes (both fired). `env` merges per-key
with the local file winning. The committed `settings.json` ships no hooks, so
relocating the override leaves the effective hook set unchanged, and
`enabledPlugins` / `autoMemoryEnabled` keep coming from the committed file —
read natively rather than copied into the local one.

## What changed

- `internal/sandboxcmd/bind.go` — the `XDG_CONFIG_HOME` env block goes to
  `settings.local.json`; `markSettingsSkipWorktree` is gone.
- `justfile` `claude-settings-init` — the hook override and
  `worktree.bgIsolation` go to `settings.local.json`, merged over whatever that
  file already holds (the env block, hand-written `permissions`); no
  `update-index --skip-worktree`; no copying of committed keys.
- `internal/sandboxcmd/claudesettings.go` (new) — `repairWorktreeClaudeSettings`
  and `endless-go sandbox claude-settings-repair [--all] [<worktree> ...]`.
- `internal/hookcmd/claude.go` — `worktreeOverrideRegistered` reads
  `settings.local.json` first and still recognises the legacy `settings.json`
  location, so a not-yet-repaired worktree keeps self-skipping correctly.
- `.gitignore`, `internal/monitor/worktree_anomalies.go`,
  `.endless/hooks/post-worktree-create.sh`, `src/endless/task_cmd.py` — comments
  that asserted the old arrangement.

## Migration: Mike's call, 2026-09-08

Changing the writers only helps worktrees created afterwards; 135 already on
disk carry the bit. Asked, and the answer was a one-shot repair command run on
E-1347's own land — so `.endless/hooks/post-land/e-1347.sh` invokes
`sandbox claude-settings-repair --all` once, right after the merge advances main.

The repair lives in Go rather than the Python CLI because of version skew: at
post-land time the globally installed `endless` is still the PRE-land build, so
a Python subcommand added by this branch would not exist yet. E-1347's own
worktree binary is rebuilt by `just land` before the land, so the script probes
`sandbox --help` and picks the first build that actually has the subcommand.

Per worktree the repair: salvages every top-level key whose working value
differs from HEAD into `settings.local.json` (existing local values win — the
salvage fills gaps, it never overwrites), clears the bit, then restores the
tracked file. The skip-worktree bit is the ONLY trigger: without it a modified
`.claude/settings.json` is visible user work, and reverting that would be data
loss. That guard is what makes the fleet sweep and repeat runs safe.

## PRODUCT

`sandbox bind` now warns once, and changes nothing, when a project does not
gitignore `.claude/settings.local.json` — a downstream project would otherwise
see it as untracked work and as a worktree anomaly at handoff. Endless's own
`.gitignore` already covers it. Endless does not edit the user's ignore rules.

## Dogfooded

This worktree was itself repaired with the shipped command: bit cleared, tracked
file back to its committed content, `git status` clean, and all three generated
keys (`env`, six hook events, `worktree.bgIsolation`) intact in the local file —
the effective merged config is byte-identical to what it was before.

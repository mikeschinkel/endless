# E-1662 — Fully provision self-dev worktrees on spawn/claim (PRODUCT-compliant)

## Problem
claim/spawn auto-provision the sandbox DB (via `endless-go sandbox init/bind`,
worktree_cmd.py `_maybe_auto_sandbox_bind`:1214) but never BUILD the worktree's
binaries (no `bin/`) and never install the hook override
(`.claude/settings.json`). So the PostToolUse hook and sandbox CLI run the
GLOBAL/main binary, never the candidate code the sandbox exists to exercise
(E-1281/E-998). Observed in e-1643: `bin-sandbox/` present, `bin/` absent,
settings.json has XDG but no hook block / no PATH-prepend.

## PRODUCT constraint (Mike)
Shipped endless code MUST NOT shell out to `just` recipes. Provisioning runs
through a PROJECT-SPECIFIC SCRIPT the project declares (e.g. a key in
`.endless/config.json`), invoked generically by `endless-go` -- the SAME shape
as the existing `endless-go sandbox init/bind`. The endless project supplies its
own provision script (which MAY wrap `just build` / `just claude-settings-init`
INSIDE that project script -- that's the dev wrapper). endless-the-product only
invokes the declared script; `just` never appears in shipped endless source.

## Coordinate with E-1368
E-1368 (next/needs_plan) replaces the `bin-sandbox/` wrappers with binary
self-detection. Do NOT entrench `bin-sandbox/` here. If E-1368 lands first, the
binary self-routes and the PATH-prepend concern disappears, leaving this task's
job as purely BUILD + hook-override. Prefer sequencing E-1368 first; at minimum
relate, don't duplicate the routing fix.

## Approach
1. Add a project-declared "worktree provision" script (config.json key), invoked
   by `endless-go` during self_dev worktree creation -- extend the existing
   `_maybe_auto_sandbox_bind` / `endless-go sandbox init` path. For the endless
   project, that script builds the worktree binaries and installs the hook
   override.
2. The hook override must point at the worktree's freshly built
   `bin/endless-hook` so candidate code runs (E-998).
3. Provision failure surfaces loudly (and, once E-1661 lands, blocks).

## Verify (tests/tasks/e-1662-verify.sh)
- Spawn a throwaway self_dev task; assert the worktree's binaries are built and
  the hook + CLI resolve to the WORKTREE's binary, not the global one.
- Grep guard: no shipped endless Python/Go source invokes `just` in the
  provisioning path.

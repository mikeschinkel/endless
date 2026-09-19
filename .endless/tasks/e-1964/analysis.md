# Implementation notes: universal per-worktree sandboxes

Design settled in the E-1958 brainstorm; read that task's outcome for the
reasoning. This file carries only what an implementing session needs that the
outcome does not spell out.

## Order of work

1. Move the resolver. One function returns the sandbox path; every caller goes
   through it. Go's `monitor.SelfDetectWorktreeSandbox` stops mapping a worktree
   to `~/.cache/endless/sandboxes/<name>` and appends a fixed relative segment
   instead. Python's `config.sandbox_config_dir()` does the same. `_cache_root()`
   drops out of sandbox resolution entirely.
2. Ungate provisioning. `worktree_cmd._maybe_auto_sandbox_bind` stops consulting
   `config.project_is_self_dev`. Creating the dir, writing its self-ignoring
   `.gitignore`, and running the project's `post-worktree-create` hook happen for
   every project.
3. Migrate. Rename each `~/.cache/endless/sandboxes/e-NNN` whose worktree still
   exists into that worktree. Same filesystem, so a rename, not a copy. Add
   provision-on-miss to the resolver as the backstop for pre-change worktrees and
   for sandboxes already gone.
4. Delete the XDG injection and `sandbox bind` with it — AFTER the check below.
5. Documentation. CLAUDE.md's "Self-dev DB sandbox" section describes the cache
   location, the `XDG_CONFIG_HOME` injection and a manual `endless-sandbox
   destroy` that will all be gone.

## The one thing to verify before deleting the injection

`bind`'s own comment gives two justifications. The first — "the Python CLI
resolves its default config dir from XDG_CONFIG_HOME" — appears already
unreachable: `config.apply_db_choice("sandbox")` computes the dir directly and
reads `XDG_CACHE_HOME`, never `XDG_CONFIG_HOME`, and E-1429's gate refuses to run
in a self-dev worktree without an explicit `--db`, so the XDG-derived default is
never consulted there.

The second justification is the live one: it "keeps Go config.json/log reads on
the sandbox even when a binary is invoked with a cwd outside the worktree."
`--config-dir` threading covers deliberate callers. Find out whether anything
relies on ambient inheritance instead, and cover those explicitly before the
injection goes. If something does, that is a finding worth reporting, not a
reason to keep the injection by default.

## Scale, as of 2026-08-13

137 worktrees under `.endless/worktrees/`; 126 sandboxes under
`~/.cache/endless/sandboxes/` totalling 102 MB. Roughly two dozen of those
sandboxes have no surviving worktree — orphans from worktrees already reaped,
including one `worktree-e-1449` from a superseded naming scheme. In-worktree
placement makes that class of orphan structurally impossible, which is a
secondary win worth stating in the commit.

## Interaction with E-1963

E-1963 removes the sandbox tooling rot. Its item 3 (CLAUDE.md documenting
`endless-sandbox destroy`) is subsumed by this task's step 5 if this lands first.
Its decision about deleting the ephemeral `run`/`enter`/`prune` path is
independent and should not be folded in here.



---

## Evidence folded in from a session that hit the stale-sandbox failure (2026-08-16)

Recorded here rather than as its own task, per ED-1550: this relocation is what
removes the condition, so it is evidence for this design, not separate work.

A session working in `.endless/worktrees/e-1904` filed a task and got:

```
Event write failed: endless-go event: error: events: stamp task actor:
SQL logic error: no such column: changed_by_session (1)
```

The sandbox at `~/.cache/endless/sandboxes/e-1904` had been created weeks
earlier; its schema predated a column the current binary writes. Three
observations worth carrying into the implementation:

1. **The relocation fixes the root cause.** Sandboxes that "live and die with
   their worktree" cannot drift for weeks the way a `~/.cache` sandbox
   outliving its landed worktree did. This failure is the concrete argument for
   that property.

2. **A residual remains.** A long-lived worktree still accumulates drift as the
   binary advances. There is no schema-version mechanism today — no
   `user_version` PRAGMA, no `schema_version` table — so drift cannot be
   detected at all, and it surfaces as a raw driver error naming a column
   instead of naming the database. If drift still bites after this lands, the
   fix is a version stamp checked at `monitor.DB()`'s single entry point,
   beside the existing E-1429 `guardWorktreeDBContext` gate, so every command
   inherits it without an allowlist.

3. **`XDG_CONFIG_HOME` beat cwd, silently.** The session had `cd`'d to the main
   checkout, but the env var injected at claim time still selected the sandbox.
   `guardWorktreeDBContext` keys on cwd and never consults the env var, so the
   two disagreed and only one was checked. Deleting the injection (already in
   this task's scope) closes that hole — worth stating explicitly as a benefit,
   since it is not obvious from the relocation framing alone.



---

## A consumer of XDG_CONFIG_HOME that postdates the check above (2026-09-01, ES-1159)

Step 4 says to find what relies on the injection before deleting it. One
consumer landed after this analysis was written, so it is not on that list:
`.endless/tasks/_guard.sh`, from E-2090 (landed 2026-09-01).

Its second arm decides whether a verify suite is running isolated by asking
whether a real config is REACHABLE, and it checks two spellings:

    for cfg in "${XDG_CONFIG_HOME:-${HOME:-}/.config}/endless" "${HOME:-}/.config/endless"

It does not rely on ambient inheritance — the verify runner sets both variables
explicitly for the suite subprocess (internal/verifycmd/env.go), which is a
different mechanism from the claim-time injection this task deletes, and is not
in scope here.

The risk is a grep-shaped one. Someone deleting "the XDG_CONFIG_HOME injection"
may find the runner's assignment and the guard's read and take them for the same
deprecation. If the runner stops setting it, the guard's first spelling resolves
against the developer's real config and arm 2 silently stops firing on a
hand-run — the exact failure it exists to prevent, and invisible because arm 2
never fires under the runner anyway.

So: deleting the claim-time injection is safe for the guard. Deleting the
runner's per-run assignment is not, and they are not the same thing. If
XDG_CONFIG_HOME is later deprecated as the config locator outright, the guard's
reachability test needs rewriting against whatever replaces it — which ED-1583
(proposed) states as the standing rule.

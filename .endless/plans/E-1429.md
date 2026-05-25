# Require explicit `--db main|worktree` for endless CLI inside self-dev worktrees

## Context — why this change

Endless is developed using endless. When a developer or an AI agent works inside a self-dev worktree, two distinct activities get conflated:

1. **Using endless to manage the project** — filing tasks, claiming, status updates, ledger entries. Belongs in the real DB (`~/.config/endless/endless.db`).
2. **Testing endless's own CLI behavior** — running commands to verify a code change. Must NOT pollute the real ledger; belongs in a throwaway per-worktree sandbox DB.

Today the choice is implicit: it depends on whether `XDG_CONFIG_HOME` was inherited from `.claude/settings.json` injection (→ sandbox) or not (→ real DB). Silent and mistake-prone — whoever isn't actively thinking about it writes to the wrong DB.

Concrete incident (2026-05-19): `endless task add "Testing bar"` run from a bare shell `cd`'d into `.endless/worktrees/e-1424/` landed in the **real ledger** as E-1425 — the developer believed it was going to the sandbox. Earlier incidents in the same vein: the E-1307 stale-session-id bug, the side-pane companion-discovery failure.

E-1281 established the original intent (isolate worktree DB writes from the real ledger). This task replaces implicit DB selection with an explicit, bypass-proof gate.

## The decision (locked with Mike, 2026-05-17/18/20)

- Manual `endless` CLI invoked with `cwd` inside `.endless/worktrees/e-*` of a project whose `.endless/config.json` has `worktree_sandbox: true` MUST pass `--db main|worktree`.
- If `--db` is absent in that context, refuse with an actionable error.
- **No env-var equivalent.** An env var can be exported once and silently defeat the gate for every later command — the exact silent-wrong-DB failure mode this task exists to kill. A per-invocation flag cannot be bypassed that way.
- `--db main` → operate on `~/.config/endless/endless.db`. `--db worktree` → operate on this worktree's per-worktree sandbox DB.
- Gate enforced at TWO layers:
  - **Python CLI layer** — parse `--db` on the root command; produce the human-facing refusal error.
  - **Go `DB()` layer** (`internal/monitor/db.go`) — refuse to open a connection when running in a self-dev worktree without an explicit context argument. Bypass-proof backstop: catches direct invocations of `endless-event` and any future Go binary that didn't get the flag wired through.
- Outside a self-dev worktree (main checkout, other projects, anywhere else) — no flag required, no gate, no behavior change.
- **`endless-hook` is exempt and hardcoded to the main DB.** It fires automatically on every Claude prompt; there is no human to type a flag. Hooks track real project state (sessions, activity, task bindings), not test artifacts — every Claude session is a real session regardless of which directory it works in. **Mechanism (locked 2026-05-20): the hook always passes `--db main` explicitly at every call site.** The Go `DB()` layer is not actor-aware — it sees the hook as a regular caller that happens to always pass `main`. Rationale: keeps `DB()` simple (one rule for all callers); makes the hook's main-DB decision locally visible in hook code rather than buried in a DB-layer special case; couples nothing across layers.
- No `is_dev_session` flag, no view-level filtering, no command-text introspection. Sessions are sessions.

## No allowlist — gate at DB access

An allowlist of subcommands exempt from the flag was considered and rejected (lists rot; new subcommands get forgotten). Instead the gate lives at `DB()`. Commands that never open the DB (`--help`, `--version`, `shell-init`, `endless session id` in pure-resolution mode) never hit the gate and work without `--db`. Coverage is automatic.

## Refusal message (locked 2026-05-20)

```
Error: running inside self-dev worktree requires an explicit --db value:

  --db main      the real ledger — managing the project
  --db worktree  this worktree's sandbox — testing endless itself

Need paths? Run `endless db path --db=main|worktree`.
```

Choices made and rationale (don't relitigate during planning):

- **No resolved DB paths in the error itself.** Use the `endless db path` companion command (see Sibling deliverable below).
- **No suggested default.** Newcomers should ask Claude which to use; defaulting risks pushing them toward the wrong choice.
- **No one-letter aliases yet** (`-m` / `-w`). Add only if typing becomes tedious in practice.
- **Don't echo the user's command in the error.** Click's usage line already covers it.

## Sibling deliverable: `endless db path` command

Add a small purpose-built print verb companion to the gate:

```
endless db path --db=main         # prints e.g. /Users/mike/.config/endless/endless.db
endless db path --db=worktree     # prints e.g. /Users/mike/.cache/endless/sandboxes/worktree-e-NNN/endless.db
```

`--db=worktree` resolves the sandbox dir from current worktree's cwd, the same way the gate resolves it. For users / agents who actually need the absolute path (SQL client debug, scripting). Referenced from the gate's refusal message so users have a one-command escape hatch.

**Must land WITH (or before) the gate**, so the error message's reference is functional immediately, not aspirational.

## Post-E-1426 model

E-1426 (urgent, assumed) retires companion JSON files, replacing them with idempotent `sessions.process` UPSERTs on every hook event. Write this task against that post-E-1426 world:
- Do NOT reference companion files as a DB-choice or discovery mechanism — they will not exist.
- Session identity lives in `sessions.process` in whichever DB the hook wrote to — which, given the hook is hardcoded to main, is always main.
- Sequencing: E-1426 is expected to land first. If it has not, coordinate so the hook's main-DB hardcoding does not fight E-1426's UPSERT path.

## Implementation shape

Three committable slices (any order across slices 1–2; slice 3 must land WITH or BEFORE the gate becomes user-facing):

1. **Go `DB()` gate** — detect self-dev-worktree cwd plus `worktree_sandbox: true` in the project config; require an explicit context argument; refuse otherwise. Tests.
2. **Python CLI `--db` flag** — root-level option; map `main` → real config dir, `worktree` → resolve the current worktree's sandbox dir; pass the resolved context to every Go subprocess as an explicit CLI argument (NOT an env var, to preserve the no-global-bypass property); emit the refusal error when missing. Tests.
3. **`endless db path` command** — see Sibling deliverable above. Small, isolated, lands with or before the gate.

## Sandbox-path resolution (locked 2026-05-21)

`--db worktree` resolves the sandbox dir from current worktree's cwd:

1. Walk up cwd to find `<project>/.endless/worktrees/<wt-dirname>/`.
2. Extract task_id from `<wt-dirname>` — reuse `monitor.TaskIDFromWorktreePath` on the Go side (already exists) and the equivalent on the Python side.
3. Build sandbox dir per current convention: `~/.cache/endless/sandboxes/worktree-e-{task_id}/`.

The current sandbox-naming convention is under review:
- **E-1446** — include the worktree slug in the sandbox dir name for 1-to-1 worktree↔sandbox mapping.
- **E-1447** — drop the redundant `worktree-` prefix.

If either lands before E-1429 implementation, update this task's resolution rule and verification step to match. If neither has landed, lock against the current convention.

## Coordination with E-1362 (snapshot file location move)

E-1362 moves `snapshotPlanFile`'s filesystem target from `<main-checkout>/.endless/plans/snapshots/` to `<main-checkout>/.git/info/endless/snapshots/`. The two tasks answer different questions:

- E-1429 decides **which checkout** the hook resolves to (always main, via the hook-uses-main rule).
- E-1362 decides **where within that checkout** the snapshot file lands.

They don't conflict, and either can land first. Either way, the hook's `ProjectPath()` lookup in `snapshotPlanFile` resolves to main checkout (because E-1429 makes hooks always use main, and E-1362's target is inside main anyway). The implementer of E-1429 must **not** touch `snapshotPlanFile`'s filesystem path — that change belongs to E-1362.

## Related tasks (NOT deliverables of this task)

- **E-1281** — this task `cleans_up` it; completes the original intent.
- **E-1330** — implements baseline `--mode seed` for sandbox init; complementary, not affected by this task.
- **E-1335** — adds user-configurable seed policy on top of E-1330; complementary, not affected by this task.
- **E-1414** — revised: precmd hook keeps PATH and `ENDLESS_SESSION_ID` duties, drops the `XDG_CONFIG_HOME` injection (superseded by `--db`).
- **E-1332** — revised: delayed sandbox reap instead of immediate destroy on worktree drop/land.
- **E-1426** — relates; see Post-E-1426 section.
- **E-1446** — sandbox naming change: include worktree slug. May alter the sandbox-path resolution rule above.
- **E-1447** — sandbox naming change: drop `worktree-` prefix. May alter the sandbox-path resolution rule above.
- **E-1337, E-1391** — substantively unaffected; their mechanics remain valid under the new gate.

## Verification

1. From main checkout: `endless task add "x"` works with no `--db` flag (no behavior change).
2. From inside `.endless/worktrees/e-NNN/`: `endless task add "x"` refuses with the exact refusal message above.
3. From inside the worktree: `endless --db main task add "x"` writes to the real ledger; `endless --db worktree task add "x"` writes to the sandbox DB. Verify each lands in the expected DB and NOT the other.
4. Direct Go binary invocation (`endless-event ...`) from inside a worktree with no context argument → Go `DB()` refuses.
5. `endless-hook` fired from inside a worktree → reads/writes the main DB, no refusal.
6. `endless --help`, `endless --version`, `endless shell-init` from inside a worktree → work without `--db` (never open the DB).
7. `endless db path --db=main` prints the real-DB path; `endless db path --db=worktree` from inside a worktree prints that worktree's sandbox-DB path.


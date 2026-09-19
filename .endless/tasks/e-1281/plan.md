# E-1281 — Fix worktree sessions writing to global production DB

## Context

When Mike develops endless itself inside a per-task worktree (`/path/to/endless/.endless/worktrees/e-NNN/`), every `endless` command run from that worktree writes to the real production DB at `~/.config/endless/endless.db` — polluting Mike's actual task ledger with throwaway dev-time noise.

Two roles must be distinguished automatically:
- **System-under-test role.** Inside an endless worktree → use a sandbox DB. (The case this task fixes.)
- **Tool role.** Inside an acme-cli worktree → use the real DB. Real audit data, not pollution.

### Locked design choices (from planning conversation with Mike)

- **Endless never shells out to `just`.** Endless source provides generic sandbox primitives (commands). The Justfile recipe is a thin convenience for users; it's never called from endless code.
- **Discrimination via per-project config flag, not auto-detect.** A project's `.endless/config.json` sets `"worktree_sandbox": true` to opt in. Endless's own `.endless/config.json` sets the flag; other projects don't. No "is this the endless project?" sniffing baked into endless source — the project itself declares the policy.
- **Storage.** Sandbox lives at `~/.cache/endless/sandboxes/worktree-e-NNN/`, reusing E-995's directory convention. Stable name = `worktree-` + task ID.
- **Initial state via `--mode` flag.** `empty` (default), `seed` (current task + parents — may be deferred), `clone` (deferred to E-1087). Different tasks have different needs.
- **Injection.** Wrapper scripts in `<worktree>/bin-sandbox/` export `XDG_CONFIG_HOME` and exec the worktree-built binaries. PATH-prepended via `<worktree>/.claude/settings.json` env block so Claude sessions inherit them.
- **Auto-wiring.** `endless task claim` and `endless task spawn` call `endless sandbox init` and `endless sandbox bind` automatically when the project's `.endless/config.json` has `worktree_sandbox: true`. User doesn't think about it.
- **Manual opt-in still available.** `just dev-sandbox-init` is a 2-line recipe calling the same endless commands, for users who created a worktree by hand or want to re-run the setup.
- **Migration.** Start fresh. Existing prod-DB pollution stays put; Mike cleans manually as he notices it.

---

## Current state (verified by exploration)

| Concern | Site | Status |
|---|---|---|
| Python config root | `src/endless/config.py:8-17` `_config_root()` | Already honors `XDG_CONFIG_HOME` |
| Go config dir | `internal/monitor/db.go:20-43` `ConfigDir()` | Already honors `XDG_CONFIG_HOME` |
| Subprocess env inheritance | `src/endless/event_bridge.py:101` | `subprocess.run(...)` inherits env automatically |
| Existing sandbox subcommands | `cmd/endless-sandbox/` | `run`, `enter`, `list`, `prune`, `destroy` exist; **missing `init` and `bind`** |
| Worktree-discriminator idiom | `Justfile:claude-settings-init`, `cmd/endless-hook/main.go:isInMainCheckout` | `git rev-parse --git-dir == --git-common-dir` |
| Project config file | `src/endless/config.py:87-108` `project_config_*` | Reads/writes `<project>/.endless/config.json` |
| Worktree creation | `src/endless/worktree_cmd.py:create_task_worktree` | Already handles companion file write |
| `endless task claim` | `src/endless/task_cmd.py` (location confirmed during impl) | Calls `create_task_worktree` |

---

## Implementation

### Phase 1 — `endless sandbox init` subcommand

**New file: `cmd/endless-sandbox/init.go`**

```
endless sandbox init <name> [--mode {empty|seed|clone}] [--force]
```

- Provisions `~/.cache/endless/sandboxes/<name>/` with `.sandbox-meta.json` (mode=`persistent`, creator_pid, name, created_at).
- `--mode empty` (default): empty schema, auto-migrated on first DB access.
- `--mode seed`: copies current-task row + parent chain + `projects` row (with `path` pointing at sandbox-scoped location). **May ship in a follow-up; default empty is sufficient for E-1281.**
- `--mode clone`: deferred to E-1087. Returns clear error if specified.
- Idempotent: re-running with the same name exits 0 with a notice. `--force` recreates.
- Prints sandbox path on stdout for capture by callers.

Wire into `cmd/endless-sandbox/main.go` subcommand dispatch. Add matching Click subcommand in `src/endless/sandbox_cmd.py` (if the Python group exists by then; if not, callers invoke the Go binary directly).

### Phase 2 — `endless sandbox bind` subcommand

**New file: `cmd/endless-sandbox/bind.go`**

```
endless sandbox bind <worktree-path> [<sandbox-name>]
```

- If `<sandbox-name>` omitted, default to `worktree-<task-id-from-worktree-basename>`.
- Resolves the sandbox path via `~/.cache/endless/sandboxes/<name>/`. Errors if the sandbox doesn't exist (suggest running `init` first).
- Generates wrapper scripts in `<worktree-path>/bin-sandbox/` for each endless binary (`endless`, `endless-event`, `endless-hook`, `endless-channel`, `endless-tmux`, `endless-serve`, `endless-sandbox`). Each wrapper:
  ```sh
  #!/usr/bin/env bash
  export XDG_CONFIG_HOME="<sandbox-path>"
  export ENDLESS_SANDBOX="<sandbox-path>"
  exec "<worktree-path>/bin/<binary>" "$@"   # falls back to global if worktree binary missing
  ```
- Writes/updates `<worktree-path>/.claude/settings.json` env block: PATH-prepends `<worktree>/bin-sandbox/`. Preserves other env entries already there.
- Idempotent: re-running produces byte-identical wrappers and the same settings.json modification.

**Note on `.claude/settings.json` ownership:** Today E-998's `claude-settings-init` Justfile recipe writes this file. Refactor that recipe to delegate to `endless sandbox bind` for the env block, while keeping the hook-command rewriting it already does. Two writers to one file is fine if they touch disjoint keys (env vs hooks). Implementation phase will confirm by reading the recipe and structuring the merges.

### Phase 3 — Project config flag

**Modify: `src/endless/config.py` and downstream readers**

Add `worktree_sandbox: bool` field to `.endless/config.json` schema. Default false. Read via existing `project_config_read()` API.

**Commit endless's own flag.** Add to the endless project's `.endless/config.json`:
```json
{
  "worktree_sandbox": true
}
```

This is committed to main as a global-config artifact (per Mike's `feedback_global_config_exception.md`).

### Phase 4 — Auto-wire claim and spawn

**Modify: `src/endless/task_cmd.py` (and `src/endless/worktree_cmd.py` for spawn)**

After `create_task_worktree(...)` returns the worktree path:
1. Look up the project's config via `project_config_read(project_path)`.
2. If `worktree_sandbox` is true:
   - `subprocess.run(["endless-sandbox", "init", f"worktree-e-{task_id}", "--mode", "empty"], check=True)`.
   - `subprocess.run(["endless-sandbox", "bind", str(worktree_path), f"worktree-e-{task_id}"], check=True)`.
3. If false/missing: skip silently — tool-role works as today.

Failures here should not abort `task claim` — the worktree is already created. Log the error, print a one-line warning, continue. (Mike can rerun `endless sandbox init/bind` or `just dev-sandbox-init` manually.)

### Phase 5 — `just dev-sandbox-init` recipe

**Modify: `Justfile`**

Two-line recipe for users who provisioned a worktree by hand or want to re-run setup:

```just
dev-sandbox-init:
    #!/usr/bin/env bash
    set -euo pipefail
    git_dir="$(cd "$(git rev-parse --git-dir)" && pwd)"
    git_common_dir="$(cd "$(git rev-parse --git-common-dir)" && pwd)"
    if [ "$git_dir" = "$git_common_dir" ]; then
        echo "dev-sandbox-init: must run from a worktree, not main." >&2
        exit 1
    fi
    task_id="$(basename "$(pwd)" | sed -n 's/^e-\([0-9][0-9]*\).*/\1/p')"
    test -n "$task_id" || { echo "cannot parse task ID from $(pwd)"; exit 1; }
    name="worktree-e-${task_id}"
    endless sandbox init "$name" --mode empty
    endless sandbox bind "$(pwd)" "$name"
```

### Phase 6 — Update CLAUDE.md

**Modify: `CLAUDE.md`**

In the worktree-setup section, note that after `endless task claim` / `endless task spawn` auto-wires the sandbox when the project has `worktree_sandbox: true`. For manual worktree creation, run `just dev-sandbox-init`. Briefly explain the storage location (`~/.cache/endless/sandboxes/worktree-e-NNN/`) so users can find their data when needed.

---

## Critical files

**New:**
- `cmd/endless-sandbox/init.go`
- `cmd/endless-sandbox/bind.go`

**Modified:**
- `cmd/endless-sandbox/main.go` — register `init` and `bind`.
- `src/endless/sandbox_cmd.py` — Click subcommands for `endless sandbox init`/`bind` (if present; else direct binary invocation).
- `src/endless/task_cmd.py` — claim hook to call sandbox init/bind.
- `src/endless/worktree_cmd.py` — spawn hook for the same.
- `src/endless/config.py` — `worktree_sandbox` field plumbing (read-side only; flag is set by writing the project config file directly).
- `Justfile` — `dev-sandbox-init` recipe; refactor `claude-settings-init` to delegate the env-block write to `endless sandbox bind`.
- `.endless/config.json` (endless's own) — `"worktree_sandbox": true`.
- `CLAUDE.md` — worktree-setup docs.

---

## Follow-up tasks to file (`--cleans-up E-1281`)

After plan-approval:
1. **Auto-cleanup sandbox on `worktree drop`/`worktree land`.** `endless worktree land <id>` should `endless sandbox destroy worktree-e-<id>` after successful merge. Same for `drop`.
2. **Implement `endless sandbox init --mode seed`.** If deferred from Phase 1.
3. **Update E-1080 (sandbox docs)** or file new docs task: document the auto-wired worktree-sandbox flow.

All to be confirmed with Mike before claiming and implementing.

---

## Verification (end-to-end, runnable from this worktree)

After implementation lands and a fresh `endless task claim E-test` (or rerun `just dev-sandbox-init`):

1. **Provisioning runs.** `ls -la ~/.cache/endless/sandboxes/worktree-e-1281/` shows `.sandbox-meta.json` and (after first DB access) `endless/endless.db`. `ls bin-sandbox/` shows seven wrapper scripts, all `chmod +x`.

2. **Wrapper redirects.** `bin-sandbox/endless task list` runs against the empty sandbox DB (shows zero tasks). `bin-sandbox/endless task add "test sandbox"` succeeds; the row appears in the sandbox DB. Plain `endless task list` (no `bin-sandbox/` prefix) still shows real production tasks.

3. **No prod-DB writes from a Claude session.** Capture before-state:
   ```
   sha256sum ~/.config/endless/endless.db
   ```
   Spawn a Claude session inside the worktree (`endless task spawn <test-task>`), perform several `endless task add`/`update` ops inside the session, exit. Re-check `sha256sum ~/.config/endless/endless.db` — **unchanged**. The added rows are in `~/.cache/endless/sandboxes/worktree-e-1281/endless/endless.db`.

4. **Tool-role still works (regression check).** From a non-endless worktree (e.g., `~/Projects/some-other-project/.endless/worktrees/e-XXX/` where the project's `.endless/config.json` has no `worktree_sandbox` flag): `endless task add ...` lands in the real production DB. This is the happy path for non-endless projects; confirm we haven't broken it.

5. **PATH inside Claude.** Inside a Claude session spawned from the worktree, run `which endless` → prints `<worktree>/bin-sandbox/endless`. `printenv XDG_CONFIG_HOME` → prints sandbox path.

6. **Hook routes to sandbox.** Trigger a SessionStart event via spawn. Verify session-row writes land in the sandbox DB. Check via `bin-sandbox/endless session status` from inside the worktree.

7. **Idempotence.** Re-run `just dev-sandbox-init` (or `endless sandbox init worktree-e-1281`). Exit 0, no changes, prints the existing path.

8. **Migration sanity.** No automatic moves of pre-existing rows. `sqlite3 ~/.config/endless/endless.db "SELECT COUNT(*) FROM tasks"` before and after rollout: identical.

---

## Out of scope

- Auto-cleanup of orphan sandboxes on `worktree drop`/`worktree land` (follow-up task).
- `--mode seed` and `--mode clone` (seed may slip to follow-up; clone is E-1087).
- Bare-shell-from-fresh-terminal isolation (user can add `bin-sandbox/` to PATH or `cd` via direnv if they want it).
- E-1309 (ledger-route-to-main). Independent; this work supersedes it when sandbox is active.
- Cleaning up existing prod-DB pollution from pre-fix worktree sessions.

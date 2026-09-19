# E-998 — Per-worktree `.claude/settings.json` so spawned sessions test the worktree's hook

## Problem

Exercising new endless-hook code in a real Claude session today requires repointing `/usr/local/bin/endless-hook` (a symlink) at the worktree's `bin/endless-hook`. That swap is global — every other Claude session on the machine starts running the unverified build for the duration of the test. Sibling sessions get noisy DB writes, surprising errors from in-progress hook code, or both. The verifier ends up babysitting other tabs to make sure they don't fire while the swap is active.

Claude Code's settings precedence (Managed → CLI → Local → Project → User) makes a project-level `.claude/settings.json` win over the user-level `~/.claude/settings.json` for matching keys. We can therefore drop a `.claude/settings.json` inside each worktree that re-registers the same hook events with the path swapped, and only Claude sessions whose cwd is inside that worktree pick up the override. Other sessions stay on the global symlink. No more "tap the symlink and pray nothing else fires."

## Discovery

- `~/.claude/settings.json` registers `/usr/local/bin/endless-hook claude` for five events: `SessionStart`, `UserPromptSubmit`, `PostToolUse` (all sync — `async: false`), `Stop`, `SessionEnd` (both async). It also has unrelated entries for `claude-log-hook` and `claude-tmux-alert` that are not endless-related.
- `/usr/local/bin/endless-hook` is a symlink to `<main>/bin/endless-hook`, written by the `just install` recipe.
- The committed main-checkout `.claude/settings.json` contains only `enabledPlugins` (one plugin: `frontend-design@claude-plugins-official`). It does not currently define `hooks`, so writing the override there is purely additive at the project level.
- `.claude/settings.local.json` exists at the main checkout and is gitignored globally (`~/.config/git/ignore`). It currently holds only `permissions`.
- `src/endless/setup.py` has the canonical structure for endless's hook entry (`_make_hook_entry`, `CLAUDE_HOOK_EVENTS`, `SYNC_EVENTS`) used to install hooks into `~/.claude/settings.json`. We mirror its output, not its code path — the worktree generator pulls live values from the user's actual settings rather than the constants, so any local divergence (events the user added/removed by hand) is preserved.

## Design

### Where the override file lives

`<worktree-root>/.claude/settings.json` — Project tier in Claude Code's precedence chain. Higher than User tier (which is the global symlink). Sessions whose cwd is inside the worktree see this file; sessions in main or in another worktree do not.

We considered `.claude/settings.local.json` (Local tier, even higher precedence, already gitignored). Two reasons we chose `settings.json` instead:

1. **Single owner per file.** `settings.local.json` is hand-edited by the user for `permissions`. If the recipe also wrote there, every regeneration would have to read-merge-write, fighting any concurrent edits. Keeping `settings.json` as the auto-generated file and `settings.local.json` as the user-managed file gives each one a clean owner.
2. **Aligned with the user's explicit instruction.** The task brief specified `settings.json`. The handling for the tracked-file complication (skip-worktree, below) is a small one-time cost.

### What goes in the override

The recipe reads `~/.claude/settings.json`, finds every hook entry whose `command` contains `endless-hook` (any event, any async flag, any args after the binary), and rewrites the binary path to `<worktree-root>/bin/endless-hook` while preserving the rest of the command (e.g. the trailing ` claude` arg) and the entry's other fields (`type`, `async`).

Non-endless hooks (`claude-log-hook`, `claude-tmux-alert`) are intentionally not mirrored. Claude Code's `hooks`-key merge behavior across settings layers is documented as project-overrides-user, so the worktree's `hooks` block is the only one that fires for sessions in the worktree. Losing the user's two unrelated tooling hooks for the duration of a worktree session is an acceptable trade — the alternative (mirror everything from user settings into project settings) would couple the recipe to per-developer setup it shouldn't know about.

`enabledPlugins` from the committed main-branch `HEAD:.claude/settings.json` is preserved in the generated file so plugin enablement isn't lost in the worktree.

### Refusing to run from main

The recipe checks `git rev-parse --git-dir` against `git rev-parse --git-common-dir`. They are equal in a main checkout (both point at `<main>/.git`) and different in a worktree (`<main>/.git/worktrees/<name>` vs `<main>/.git`). If equal, the recipe exits non-zero before writing — it would otherwise overwrite the committed `.claude/settings.json` and that's a footgun.

### Idempotence

`json.dump` with `sort_keys=True` and a fixed indent gives byte-stable output. Two consecutive runs produce identical files (verified: same MD5). `git update-index --skip-worktree` is also idempotent — running it on an already-skipped path is a no-op.

### Tracked-file handling — `.gitignore` plus `skip-worktree`

`.claude/settings.json` is committed at the main checkout (the source of `enabledPlugins`). Because git's `.gitignore` does not affect tracked files, adding `.claude/settings.json` to `.gitignore` is documentary rather than functional — it covers the future case where someone untracks the file, and it makes the intent visible to readers of the gitignore.

The actual modification-masking is `git update-index --skip-worktree .claude/settings.json`, which the recipe applies after writing. This makes the regenerated content invisible to `git status` in the worktree only — main and other worktrees retain their own per-worktree skip-worktree state and are unaffected.

If the committed file ever changes upstream (e.g. someone adds another plugin), the worktree's skipped copy will not pick up the change automatically. Mitigation: re-run `just claude-settings-init` after pulling, or `git update-index --no-skip-worktree .claude/settings.json && git checkout -- .claude/settings.json && just claude-settings-init` to fully resync.

We rejected three alternatives:

- **Untrack `.claude/settings.json` entirely.** Cleanest but un-shares the committed `enabledPlugins`. Out of scope per the brief's "worktree-only" constraint.
- **Use `.claude/settings.local.json`.** Already gitignored, so no skip-worktree dance, but conflicts with the user's manual `permissions` edits as discussed above.
- **Per-worktree `.git/info/exclude`.** Same problem as `.gitignore` — does not affect tracked files.

### Build dependency

The recipe writes the absolute path regardless of whether `bin/endless-hook` exists yet. The binary must exist by the time Claude actually fires a hook — i.e. `just go` (or `just build`) must have run before the first Claude session in the worktree. The CLAUDE.md update sequences `claude-settings-init` and `just build` in the documented worktree-bootstrap commands.

## Verification

End-to-end (manual, by Mike): from inside `.endless/worktrees/e-998/`, run `endless task spawn <task>`. The spawned Claude session's hook events should invoke `<worktree>/bin/endless-hook`, not `/usr/local/bin/endless-hook`. Confirm by tailing whatever the worktree binary writes (e.g. compare DB writes from the worktree's session against main checkout's binary signature).

Structural verification done in this session:

- **File at expected path:** `<worktree>/.claude/settings.json` exists, JSON-parses, has five hook events.
- **Paths point at worktree binary:** all five `command` strings start with `<worktree>/bin/endless-hook` (no fall-through to `/usr/local/bin/endless-hook`).
- **Binary exists and runs:** `bin/endless-hook` was built (`just go`), differs by MD5 from the global symlink target, and exits 0 when invoked with a sample claude-event payload.
- **Idempotence:** two consecutive `just claude-settings-init` runs produce byte-identical files (same MD5).
- **Skip-worktree is set:** `git ls-files -v .claude/` shows `S .claude/settings.json` in the worktree; `git status` no longer reports the modification.
- **Refuses main checkout:** the guard uses `git-dir` vs `git-common-dir` equality (verified by reading the recipe — running from main is impossible to test directly without modifying main's Justfile, which is what the guard exists to prevent).

## Out of scope (filed if not yet)

- Automatic `claude-settings-init` invocation from a future `endless worktree create` command — folded into E-1086 (worktree-creation extension hook).
- Cleanup on worktree drop — not needed; removing the worktree directory removes the file.
- Modifying `~/.claude/settings.json` — explicitly excluded per the brief.

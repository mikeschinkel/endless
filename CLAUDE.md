# Endless Project Rules

## Build

Use `just build` to build everything (templ generate, tailwind CSS, Go binaries). All Go binaries are output to `./bin/`. Use `just install` to build and symlink to `/usr/local/bin/`.

**NEVER build Go binaries to the project root or `/usr/local/bin/` directly.**

## Install / refresh

Use `just install` to refresh the local toolchain after pulling main or landing a branch. It (a) builds Go binaries into `./bin/`, (b) symlinks them to `/usr/local/bin/`, and (c) installs the Python CLI in **editable mode** via `uv tool install -e . --force` — so subsequent Python source changes go live without reinstalling.

**Do NOT run `uv tool install --reinstall .` directly.** That installs a non-editable *copy* of the source into the uv tool's site-packages, which then goes stale on every merge until you reinstall again. `just install` is the single source of truth.

Run `just install` from the **main checkout**, never from a worktree — `uv tool install -e .` from a worktree would point the global tool at the worktree's source, which gets removed when the worktree is dropped.

## Worktree setup (Go builds)

`go.mod` has `replace ../go-pkgs/X` directives that only resolve correctly from the main checkout — worktrees see them at the wrong relative depth and Go builds break. After creating a worktree:

```sh
git worktree add .endless/worktrees/e-NNN main
cd .endless/worktrees/e-NNN
just go-work-init
just claude-settings-init   # see below — only when you'll run Claude inside this worktree
just build                  # builds bin/endless-hook for the override above to call
```

`just go-work-init` generates a `go.work` file with absolute paths to the user's local `go-pkgs/` modules. `go.work` is gitignored (per-developer). When present, it overrides go.mod's `replace` directives.

Run `just go-work-init` from any checkout (main or worktree) — it walks up to the main checkout, finds `../go-pkgs/`, and writes the workspace file in cwd.

The broader strategy for handling co-developed third-party deps across worktrees is open as **E-1085**.

## Worktree setup (Claude hook override) — E-998

`just claude-settings-init` generates `<worktree>/.claude/settings.json` so a Claude session whose cwd is inside this worktree invokes `<worktree>/bin/endless-hook` instead of the global `/usr/local/bin/endless-hook` symlink. Sessions outside the worktree are unaffected.

This replaces the old workflow of repointing the global symlink at the worktree's binary, which forced every other live Claude session on the machine onto the unverified build.

Run AFTER `git worktree add` and BEFORE spawning Claude inside the worktree. `just build` (or `just go`) must follow so `bin/endless-hook` actually exists; the recipe writes the absolute path even if the binary is missing.

The recipe refuses to run from the main checkout (would clobber the committed `.claude/settings.json`). Inside a worktree it (a) mirrors the endless-hook entries from `~/.claude/settings.json` with the path swapped, (b) preserves `enabledPlugins` from the main-branch HEAD `.claude/settings.json`, and (c) sets `git update-index --skip-worktree .claude/settings.json` so the regenerated content stays out of `git status` for this worktree only.

Idempotent: re-running produces the same file. Removing the worktree takes the file with it (no cleanup needed).

## Tests

Use `just test` to run Python tests.

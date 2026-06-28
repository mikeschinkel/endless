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
just build                  # builds bin/* binaries the wrappers will exec
just dev-sandbox-init       # see "Self-dev DB sandbox" below
just claude-settings-init   # see "Claude hook override" below
```

`endless task claim` and `endless task spawn` invoke `dev-sandbox-init` automatically; the manual `just dev-sandbox-init` is for worktrees created by hand or to re-wire the sandbox after rebuilding binaries.

`just go-work-init` generates a `go.work` file with absolute paths to the user's local `go-pkgs/` modules. `go.work` is gitignored (per-developer). When present, it overrides go.mod's `replace` directives.

Run `just go-work-init` from any checkout (main or worktree) — it walks up to the main checkout, finds `../go-pkgs/`, and writes the workspace file in cwd.

`endless task claim` and `endless task spawn` run the full worktree bootstrap automatically via endless's own `.endless/hooks/post-worktree-create.sh` (the generic post-worktree-create hook): `go-work-init`, then `build` (so `bin/*` exists), then `claude-settings-init` (the per-worktree hook override). So the entire manual block above is only needed for worktrees created by hand with `git worktree add`, which doesn't fire the hook. Without the build + override step a self-dev worktree silently falls back to the global/main binary instead of its own candidate build (E-1662). The hook is non-fatal + loud per E-986's contract: on failure the worktree is kept and you re-run the hook (it's idempotent) to finish bootstrap.

The broader strategy for handling co-developed third-party deps across worktrees is open as **E-1085**.

## Worktree setup (Claude hook override) — E-998

`just claude-settings-init` generates `<worktree>/.claude/settings.json` so a Claude session whose cwd is inside this worktree invokes `<worktree>/bin/endless-hook` instead of the global `/usr/local/bin/endless-hook` symlink. Sessions outside the worktree are unaffected.

This replaces the old workflow of repointing the global symlink at the worktree's binary, which forced every other live Claude session on the machine onto the unverified build.

Run AFTER `git worktree add` and BEFORE spawning Claude inside the worktree. `just build` (or `just go`) must follow so `bin/endless-hook` actually exists; the recipe writes the absolute path even if the binary is missing.

The recipe refuses to run from the main checkout (would clobber the committed `.claude/settings.json`). Inside a worktree it (a) mirrors the endless-hook entries from `~/.claude/settings.json` with the path swapped, (b) preserves `enabledPlugins` from the main-branch HEAD `.claude/settings.json`, and (c) sets `git update-index --skip-worktree .claude/settings.json` so the regenerated content stays out of `git status` for this worktree only.

Idempotent: re-running produces the same file. Removing the worktree takes the file with it (no cleanup needed).

## Self-dev DB sandbox — E-1281

Endless self-dev worktrees route DB writes to a per-worktree sandbox so dev-time CLI usage and tests don't pollute the user's real task ledger at `~/.config/endless/endless.db`. Sandbox lives at `~/.cache/endless/sandboxes/e-NNN[-slug]/` — its basename matches the worktree dir's basename, so each worktree maps 1-to-1 to its own sandbox.

Inside a Claude session spawned from the worktree, routing is transparent: `endless task ...` reads/writes the sandbox via the `XDG_CONFIG_HOME` injected into `<worktree>/.claude/settings.json`. From a bare shell inside the worktree, run the worktree-built Go binary directly — `./bin/endless-go ...` self-detects the sandbox from cwd (E-1368), no wrapper or manual export needed; for the Python CLI, pass `endless --db sandbox ...` (or export `XDG_CONFIG_HOME` manually). From the main checkout or any non-endless project, endless reads/writes the real DB.

Opt-in is per project. Endless's own `.endless/config.json` sets `"self_dev": true`. Downstream projects that *use* endless as a tool leave the flag unset so their worktree tasks land in the real DB (real audit data, not pollution).

`endless task claim` and `endless task spawn` auto-invoke the setup when the flag is true; no extra step from the user. For worktrees created via `git worktree add` directly, run `just dev-sandbox-init` from the worktree.

The setup writes an `XDG_CONFIG_HOME` value into `<worktree>/.claude/settings.json`'s `env` block so Claude-spawned subprocesses (including the endless-hook fired on every event) inherit the sandbox routing. There are no longer any wrapper scripts: the `endless-go` binary self-detects the sandbox from cwd (E-1368, mirroring the Python CLI's `--db sandbox` self-routing from E-1513), so candidate code built into `<worktree>/bin/` is exercised by invoking it directly. `XDG_CONFIG_HOME` is retained because the Python CLI resolves its default config dir from it.

Sandbox cleanup on worktree drop/land is not yet automatic; manually `endless-sandbox destroy e-NNN[-slug]` if the cache needs reclaiming.

## Tests

Use `just test` to run Python tests.

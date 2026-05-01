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
```

`just go-work-init` generates a `go.work` file with absolute paths to the user's local `go-pkgs/` modules. `go.work` is gitignored (per-developer). When present, it overrides go.mod's `replace` directives.

Run `just go-work-init` from any checkout (main or worktree) — it walks up to the main checkout, finds `../go-pkgs/`, and writes the workspace file in cwd.

The broader strategy for handling co-developed third-party deps across worktrees is open as **E-1085**.

## Tests

Use `just test` to run Python tests.

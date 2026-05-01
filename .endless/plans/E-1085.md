# E-1085 — Strategy for co-developed third-party dependencies across worktrees

## Context

The `replace github.com/mikeschinkel/X => ../go-pkgs/X` pattern in endless's own `go.mod` is a specific instance of a general problem: how does an endless-managed project handle dependencies that are being co-developed alongside it, across worktrees, in a way that is language-agnostic and not per-developer fragile?

E-996 is the narrow Go-specific fix (`go.work`). It works today but doesn't generalize. Other languages have analogous mechanisms with the same pain points.

## Examples this needs to cover

- **Go**: `replace` directives, `go.work` overrides.
- **Python**: `pip install -e ../my-package`, `uv pip install -e`.
- **JS / TS**: `npm link`, `workspaces:` with `file:` deps, pnpm workspaces.
- **Rust**: `[patch.crates-io]`, `path` deps in `Cargo.toml`.

Today the workaround is a manual symlink at `.endless/worktrees/go-pkgs -> ~/Projects/go-pkgs` re-created per machine.

## Mike's design ideas (2026-05-01)

### Option 1 — Restructure repo root

Move the project into a subdir (`repo/`, `root/`, or similar) with a sibling `vendor/` (general-purpose, not Go-vendor) at the same level. Worktrees of the project would inherit the same shape; vendor is shared across worktrees.

- **Pro**: clean separation, single vendor location.
- **Con**: highly disruptive — every existing checkout moves, every path changes.

### Option 2 — Per-worktree gitignored deps dir

Place each dep at `<worktree>/.endless/deps/<dep>/`, materialized as a clone, symlink, or git worktree. Per-worktree, language-agnostic, gitignored so it doesn't enter commits.

(Mike's original phrasing was `.git/info/endless/worktrees/<dep>` for the same intent. That doesn't quite work as a literal path — `.git` is a file in worktrees, not a directory; `info/` resolves through git tooling. The corrected version uses a real filesystem path inside the worktree.)

- **Pro**: real filesystem path; language-agnostic; per-worktree isolation; auto-cleaned when worktree drops.
- **Con**: each worktree needs its own materialization step; storage multiplied.

### Option 3 — Per-worktree git worktrees of the deps

Combine (2) with `git worktree add` on the dep itself. Each parent worktree gets a corresponding dep worktree, allowing parallel dev on the dep too.

- **Pro**: parallel-dev capability on deps.
- **Con**: heaviest; most moving parts; cleanup correctness matters.

### Option 4 — Stay with per-machine symlink

Keep the current pattern; document carefully.

- **Pro**: zero design work.
- **Con**: per-machine fragility; new contributors hit it; doesn't generalize.

## Cross-cutting concerns

- **Registration**: endless needs to know which deps the project tracks. Probably extends `.endless/config.json` with a `deps` section.
- **Materialization mode**: per-dep choice of clone / symlink / git-worktree. User picks.
- **Cleanup**: dep dirs go with their parent worktree (gitignored); dep removal prunes everywhere.
- **Language-specific glue**: each dep needs "how to wire into the language's build system" — `go.work` entry, `npm link`, `pip install -e`, etc. This should be pluggable, like `.endless/extensions/use.sh` (E-1014).
- **Discovery / CLI surface**: `endless dep list`, `endless dep add <repo> --as <name>`, `endless dep materialize`, etc.

## Connections

- **E-996** is the narrow Go-only stopgap. Whichever strategy lands here either subsumes or coexists with it.
- **E-971** (worktrees + behavioral gate): worktree creation is the natural moment to materialize per-worktree deps. This task informs E-971's worktree-create flow.
- **E-1083** (memory-to-repo strategy): both are about endless-as-a-product needing to communicate conventions to users. Dep-handling IS one of those conventions.

## Out of scope

- Implementing the chosen approach. File a follow-up once design lands.
- Migrating endless's own `go-pkgs` setup beyond E-996.

## Origin

2026-05-01 conversation. Mike: *"I think that you have identified something we should standardize — how we handle 3rd party code that we are developing alongside what is in the current worktree."*

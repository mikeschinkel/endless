# Endless — Manage 50+ Claude Code tasks without going insane

Endless lets one developer run many Claude Code sessions at once — each tracked by
an Endless "task" and with its own Git worktree and its own config/DB sandbox, so
nothing collides. You have Claude file a task, collaborate on a plan, then spawn the
plan into a new Claude session.

Then, when Claude has finished implementation it hands back a runnable verification
script for you to check and then merge the branch back into the main Git branch. A
live session monitor view shows every session at a glance.

This is all built on plain Git and an append-only ledger. You collaborate with
others on any project simply by pushing and pulling commits and allowing Endless to
orchestrate the rest.

Endless is in active development — expect rough edges, expect change. Honest
feedback on friction is welcome: when something is wrong or surprising, say so.

## Prerequisites

Endless drives a toolchain rather than replacing it. Install these and have them on
your `PATH` before building:

- **[git](https://git-scm.com/)** and **[tmux](https://github.com/tmux/tmux)** — tmux is
  required at runtime, not just for the layout below: `spawn`, session navigation, and
  inter-session messaging all refuse to run without it.
- **[just](https://github.com/casey/just)** — the command runner used for build/install.
- **[Go](https://go.dev/) 1.26+** — builds the Go binaries.
- **[uv](https://github.com/astral-sh/uv)** with **[Python](https://www.python.org/) 3.12+** — runs and installs the Python CLI.
- **[templ](https://templ.guide/)** and **[tailwindcss](https://tailwindcss.com/)** — invoked by the build.
- **[sqlite3](https://www.sqlite.org/)** and **[jq](https://jqlang.org/)** — used by tooling and the verification scripts.

Endless does not yet install these for you; wiring up prerequisite setup that
respects your existing package manager (Homebrew, asdf/mise, system packages, …) is
planned.

## Install

```bash
just install
```

This builds the Go binaries to `./bin/`, symlinks them to `/usr/local/bin/`, and
installs the Python CLI via `uv tool`.

To build without installing, or to run the tests:

```bash
just build    # templ generate, tailwind CSS, Go binaries
just test     # run Python tests
```

Endless depends on a [fork of `modelcontextprotocol/go-sdk`](https://github.com/mikeschinkel/go-mcp-sdk/tree/send-notification) via a `replace` directive in `go.mod`, pending upstream merge of [PR #844](https://github.com/modelcontextprotocol/go-sdk/pull/844). If `go mod tidy` fails with a `sum.golang.org` 404, set `GOPRIVATE` once to bypass the public checksum database for the fork:

```bash
go env -w GOPRIVATE=github.com/mikeschinkel/*
```

## Getting started

Point Endless at a project, give it a task, and work that task in its own session.

```bash
# 1. Register the current directory as a project (auto-detect metadata).
cd ~/Projects/myapp
endless project register --infer

# 2. Add a task.
endless task add "Build the login flow" --description "Email + password auth"

# 3. See your tasks — the full list, or just the most recently touched.
endless task list
endless task recent

# 4. Read one task in detail.
endless task show E-123

# 5. Spawn a task into its own Claude session — its own tmux window, git
#    worktree, and sandbox, with a generated handoff as the opening prompt.
endless task spawn E-123
```

### Watching your sessions

```bash
endless session status     # one-shot snapshot of the current session's focal task
endless session monitor    # the same view kept live, like `top`; Ctrl-C to exit
```

### Working layout

Endless is meant to be driven from tmux. The layout we use is a single window split
into three panes:

- **Left** — your Claude Code session, doing the work.
- **Top right** — `endless session monitor`, redrawing as your sessions change state
  so you can watch every session at a glance.
- **Bottom right** — a free shell for ad-hoc `endless` commands.

`endless task spawn` opens each task's session in its own tmux window and builds
this three-pane layout for you — you land in the Claude pane with the monitor
already running beside it. The monitor keeps its pane exactly as tall as the rows
it has to show, so the shell below it gets everything left over. Shipping the tmux
configuration Endless needs alongside the repo is still in progress.

### Exploring the CLI

Every command and subcommand self-documents with `--help`:

```bash
endless --help
endless task --help
endless task spawn --help
```

## Task lifecycle

Every task moves through a small set of statuses. New tasks start `untriaged`; triage
routes each one to `unplanned` (it still needs a plan) or straight to `submitted` (its
description is already a sufficient spec). That routing is automatic — filing a task
triages it in the background, and a periodic sweep drains anything missed — and it can
always be overridden by hand. An agent also reaches `submitted` by attaching a plan.
From there a human runs `endless task approve` to reach `ready` — so `ready` provably
means *approved to implement*, not merely *planned*.

<!-- BEGIN canonical:docs/status-lifecycle.mmd — edit the canonical file, then re-sync; do not hand-edit here -->
```mermaid
%% Canonical task status lifecycle — single source of truth.
%% Embedded (byte-identical) in README.md, CLAUDE.md, and docs/guide/index.md
%% between <!-- BEGIN canonical:docs/status-lifecycle.mmd --> / <!-- END ... -->
%% markers. Edit HERE, then re-sync the copies (tests/tasks/e-1648-verify.sh
%% asserts they match). Blocking is a relation (blocked_by), not a state, so it
%% is intentionally absent.
stateDiagram-v2
    [*] --> untriaged

    untriaged --> unplanned: triage routes (needs a plan)
    untriaged --> submitted: triage routes (description sufficient)
    unplanned --> submitted: agent submits (plan attached OR description sufficient)
    submitted --> ready: user approves
    ready --> underway: session claims
    underway --> unverified: implementation done
    unverified --> confirmed: user verifies
    unverified --> assumed: believed done, verify on use

    confirmed --> [*]
    assumed --> [*]

    unplanned --> revisit: needs re-evaluation
    underway --> revisit
    revisit --> submitted: re-submit

    submitted --> declined
    ready --> declined
    unplanned --> obsolete
    declined --> [*]
    obsolete --> [*]
    completed --> [*]
```
<!-- END canonical:docs/status-lifecycle.mmd -->

## Roadmap & vision

- [`ROADMAP.md`](ROADMAP.md) — what exists today versus what's planned, for anyone
  who wants to use, suggest improvements for, or contribute to Endless.
- [`VISION.md`](VISION.md) — the envisioned end state: what we imagine Endless
  becoming.

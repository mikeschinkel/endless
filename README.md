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

- **git** and **tmux** — tmux is required at runtime, not just for the layout below:
  `spawn`, session navigation, and inter-session messaging all refuse to run without it.
- **[just](https://github.com/casey/just)** — the command runner used for build/install.
- **Go 1.26+** — builds the Go binaries.
- **[uv](https://github.com/astral-sh/uv)** with **Python 3.12+** — runs and installs the Python CLI.
- **[templ](https://templ.guide/)** and **[tailwindcss](https://tailwindcss.com/)** — invoked by the build.
- **sqlite3** and **jq** — used by tooling and the verification scripts.

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

# 3. See what's on your plate, across every registered project.
endless task show --all

# 4. Spawn a task into its own Claude session — its own tmux window,
#    git worktree, and sandbox, with a generated handoff as the opening prompt.
endless task spawn E-123
```

### Working layout

Endless is meant to be driven from tmux. The layout we use is a single window split
into three panes:

- **Left** — your Claude Code session, doing the work.
- **Top right** — `endless session monitor`, a live top-like view that redraws as
  your sessions change state, so you can watch every session at a glance.
- **Bottom right** — a free shell for ad-hoc `endless` commands.

`endless task spawn` opens each task's session in its own tmux window. Automating
this three-pane layout, and shipping the tmux configuration Endless needs alongside
the repo, are both in progress — until then you arrange the panes yourself.

## Task lifecycle

Every task moves through a small set of statuses. New tasks start `unevaluated`; an
evaluator routes each one to `unplanned` (it still needs a plan) or straight to
`submitted` (its description is already a sufficient spec). An agent also reaches
`submitted` by attaching a plan. From there a human runs `endless task approve` to
reach `ready` — so `ready` provably means *approved to implement*, not merely
*planned*.

<!-- BEGIN canonical:docs/status-lifecycle.mmd — edit the canonical file, then re-sync; do not hand-edit here -->
```mermaid
%% Canonical task status lifecycle — single source of truth.
%% Embedded (byte-identical) in README.md, CLAUDE.md, and docs/guide/index.md
%% between <!-- BEGIN canonical:docs/status-lifecycle.mmd --> / <!-- END ... -->
%% markers. Edit HERE, then re-sync the copies (tests/tasks/e-1648-verify.sh
%% asserts they match). Blocking is a relation (blocked_by), not a state, so it
%% is intentionally absent.
stateDiagram-v2
    [*] --> unevaluated

    unevaluated --> unplanned: evaluator routes (needs a plan)
    unevaluated --> submitted: evaluator routes (description sufficient)
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

## Digging deeper

The full reference — project and task management, sessions and spawning, the data
model underneath — lives in the guide: [`docs/guide/index.md`](docs/guide/index.md).

The guide is written for a Claude Code session driving Endless, but it's the most
complete and up-to-date reference for humans too. Once Endless is installed you can
also read it from the terminal:

```bash
endless guide              # the main guide
endless guide --list       # list every topic
endless guide tasks        # a specific topic
```

## Roadmap & vision

- [`ROADMAP.md`](ROADMAP.md) — what exists today versus what's planned, for anyone
  who wants to use, suggest improvements for, or contribute to Endless.
- [`VISION.md`](VISION.md) — the envisioned end state: what we imagine Endless
  becoming.

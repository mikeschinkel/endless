# Sandbox tooling rot — inventory

Found while brainstorming E-1958. Mike believed the ephemeral sandbox feature had
been removed from Endless entirely ("we realized YAGNI so we eliminated them from
the Endless feature set") and that `sandbox run`/`enter`/`prune` did not exist.
They do — just not where he looked.

## 1. Error strings name a binary that does not exist

Every error and usage string in `internal/sandboxcmd/` is prefixed
`endless-sandbox <cmd>:` — init.go, bind.go, run.go, prune.go, destroy.go,
list.go. There is no such binary:

```
$ which endless-sandbox
endless-sandbox not found
$ ls cmd/
endless-go
```

The real surface is `endless-go sandbox <cmd>`. `endless sandbox` (the Python
CLI) also does not exist, which is what made the whole feature look absent.
So a user hitting any sandbox error is handed a command they cannot run.

## 2. The ephemeral, non-worktree sandbox path is still live code

`endless-go sandbox --help` lists:

```
  run     [--clone] [--name N] [--keep] -- <cmd> [args]
  enter   [--clone] <name>
  init    [--mode empty|worktree|seed|clone] [--force] <name>
  bind    <worktree-path> [<sandbox-name>]
  list
  prune   [--older-than DURATION]
  destroy [--force] [--if-exists] <name>
```

`run` provisions `modeEphemeral` with a random hex name and auto-destroys on
exit; `enter` forks a subshell; `prune` exists solely to reap orphaned
ephemerals (minimum `--older-than 24h`). None of this is reachable from the
worktree lifecycle, which is 1-to-1 with a task.

Falls out if run/enter/prune go: `modeEphemeral`/`modeKeep`, orphan.go,
supervisor.go, livewriters.go, and the `--clone` flags that only warn about
unimplemented E-1087.

Related: `init --mode seed` and `--mode clone` both hard-exit "not yet
implemented". Dead flags on a live command.

## 3. CLAUDE.md documents the nonexistent binary

The "Self-dev DB sandbox — E-1281" section ends with: "manually
`endless-sandbox destroy e-NNN` if the cache needs reclaiming." Cannot be run.

This item may be moot depending on E-1958: if sandboxes move inside the
worktree they die with it, so manual destroy stops being a thing to document
at all.

## 4. `ENDLESS_SANDBOX` semantics are worth a look while in here

`Sandbox.Env()`  injects `ENDLESS_SANDBOX=<dir>` alongside
`XDG_CONFIG_HOME`, but only on the run/enter paths — never via `bind`. Its
only consumer is the E-1162 refusal gate in the Python CLI: when set,
project-bound endless subcommands refuse. If run/enter are deleted, nothing
sets the variable and the gate becomes unreachable. Decide whether the gate
goes too, or gets re-keyed onto something that still exists.

## Decision needed

Whether to delete the ephemeral path or keep it deliberately. If deleted, the
`endless-sandbox` → `endless-go sandbox` string fix shrinks to the files that
survive.

# Plan: remove the sandbox tooling rot

Decision taken 2026-08-13 (Mike): **delete the ephemeral-sandbox commands.**
Not "decide whether to" — the analysis framed it as an open question and it is
not one. Nothing in code, tests, the justfile or docs invokes `run`, `enter` or
`prune`; every reference found is in historical plan documents. Mike believed the
feature had already been removed. E-1114's plan records it actively biting: a
leaked `enter` subshell survived for hours writing into an already-destroyed
sandbox, leaving him in a destroy/enter loop until he killed the Go parent by
hand.

A note on the word, since it caused confusion: the *sandboxes* `run` creates are
ephemeral (auto-destroyed on exit). The code producing them is not. Everything
below is about deleting that code.

## 1. Delete the ephemeral-sandbox commands

Remove the `run`, `enter` and `prune` cases from the dispatcher and their usage
lines, then the files behind them: `run.go`, `enter.go`, `enter_inject.go`,
`prune.go`, `orphan.go`, `supervisor.go`, `livewriters.go`, with their tests
(`enter_inject_test.go`, `livewriters_test.go`, and the run/enter/prune portions
of `sandbox_test.go`).

Falling out of `sandbox.go`: `modeEphemeral` and `modeKeep` become unreferenced
once `list.go` no longer distinguishes them. So does `Sandbox.Env()` — its only
callers are `run.go` and `enter.go`. Delete it; see item 2 for what that implies.

`list.go` keeps `stateOrphaned` only if orphan detection still means something
after `prune` is gone. It should not: every surviving sandbox is persistent and
bound to a worktree, so the state collapses. Simplify `list` accordingly rather
than leaving a state nothing can produce.

Also remove `init`'s `--mode seed` and `--mode clone`. Both hard-exit "not yet
implemented"; a flag that only ever errors is not an affordance. Note in the
commit that this closes out the E-1087 deep-clone idea as unbuilt.

## 2. Delete the E-1162 refusal gate

`Sandbox.Env()` is the only thing that sets `ENDLESS_SANDBOX`, and `bind` never
writes it. Its sole consumer is the E-1162 refusal gate in the Python CLI, which
makes project-bound subcommands refuse when the variable is set. Delete `Env()`
and nothing can ever set it, so the gate becomes unreachable code guarding a
situation that can no longer arise — you can no longer be inside an ephemeral
sandbox subshell.

**Decided by Mike, 2026-08-13: delete the gate and `test_sandbox_gate.py` with
it.** Do not re-key it onto something that still exists; the hazard does not
survive the deletion, so there is nothing left to guard.

If implementation turns up another consumer of `ENDLESS_SANDBOX`, that is a
finding to report before proceeding — not grounds to keep the gate on your own
judgment.

## 3. Fix the binary name in everything that survives

Every error and usage string in the package is prefixed `endless-sandbox <cmd>:`.
No such binary exists; the surface is `endless-go sandbox <cmd>`. Fix in the
survivors — `init.go`, `bind.go`, `list.go`, `destroy.go`, `sandbox.go` — after
the deletions, so the edit is not spent on files about to be removed.

## 4. Documentation

CLAUDE.md's "Self-dev DB sandbox" section instructs `endless-sandbox destroy
e-NNN`, which cannot be run.

Check E-1964 first. It rewrites that whole section (sandboxes move into the
worktree, die with it, and manual destroy stops existing). If E-1964 has landed,
this item is already handled — do not write a correction that E-1964 deletes. If
it has not, fix the command name only and leave the surrounding prose alone.

## Sequencing against E-1964

Both touch `internal/sandboxcmd`. E-1964 deletes `bind` outright and rewrites
path resolution. This task is the cheaper and less contentious of the two, so
landing it first shrinks E-1964's surface. If E-1964 lands first instead, skip
`bind.go` in item 3 — it will not exist.

## Verification

`just build` and `just test` both clean. `endless-go sandbox --help` lists only
the surviving commands. A deliberate error from each survivor names `endless-go
sandbox`, not `endless-sandbox`. Grep the repo for `endless-sandbox` and for
`ENDLESS_SANDBOX` — outside `.endless/` historical documents, both should be
gone.

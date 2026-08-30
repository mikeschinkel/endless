## Why prose is not enough

The rule already exists and is unambiguous. It was read, understood, paraphrased into "not required", and broken — by a session that could quote it. An agent's compliance with prose is negotiable at the moment it is inconvenient, which is precisely the moment enforcement has to exist.

## The load-bearing fix: guard inside the script

Each script already resolves its worktree root via `git rev-parse --show-toplevel` in `main()`. Add a guard there: ask endless which task this worktree holds and exit 2 unless it matches the script's own id, with a refusal that names the sanctioned runners (`endless verify <id>`, `just verify <id>`) rather than just saying no.

Put it in a shared helper the scripts source, not copied into each — there are ~40 scripts and a copied guard is ~40 chances to omit it. Add it to whatever template new scripts are written from so one authored next month inherits it. Both sanctioned runners already know the task id, so they pass by construction.

This layer works for every user, every harness, and a human at a shell prompt.

## Second symptom, same root cause

A landed script's source-shape greps go stale on legitimate refactors of code the script does not own. `e-1956-verify.sh` lines 155 and 161 grep for literal spellings — `!isTerminal(r.Status)` and a JOIN clause — that E-1185 has since factored away. The properties they stood for are intact and now pinned by behavioral tests on both sides (`TestReplacedByNote_TerminalGate`, `test_replaced_by_map_ignores_a_removed_replacement`, and their duplicates counterparts).

Under prose-only rules that stale grep is a landmine: the next refactorer runs it, sees red, and "fixes" working code to match — which is exactly what happened here. Under the guard it is inert, because nobody outside E-1956's worktree can reach it. One cause, two symptoms, one task.

## Optional second layer, not a substitute

A PreToolUse hook refusing a mismatched verify-script invocation, mirroring E-1226's hook for `sqlite3 .endless` paths. It refuses earlier and can teach at the point of the mistake. But it is harness-specific per E-1962 and does not cover a human at a shell, so it cannot carry the rule alone.

## PRODUCT note

Nothing here is Endless-specific. Any project using Endless accumulates per-task verify scripts with the same property: they are gates for one task at one moment, they look like a regression suite, and they rot. The guard belongs in the shipped scaffolding, not in this repo's scripts only. `self_dev` changes nothing — the worktree-to-task lookup is the same call in both modes.

# Addendum — a bulk sweep, and why hermeticism is not an available fix (E-2071, 2026-08-26)

## What happened

Escalation from the single wrong script this task was filed on: a session ran
EVERY script — `for f in tests/tasks/*.sh` — 197 of them, as a "project-wide
regression check" before handing off. The rationalization was not "the rule does
not apply"; it was that running the suite is what you do before you hand work
over, and these live under `tests/`.

`e-1202-verify.sh` drives the real hook binary with a synthetic payload carrying
`session_id: "e1202-verify"`. The hook did what it always does: wrote a sessions
row into the MAIN database and bound it to the caller's live tmux pane. Two
session identities then claimed one pane, and `_resolve_companion` refuses an
ambiguous sibling set — so `esu` and every companion-resolving command stopped
working in Mike's shell for the rest of the session. The row is still there;
ending it needs a hand-driven SessionEnd.

## The finding that changes the mitigation set

`cmd/endless-go/main.go` PinMainDB's the `hook` and `tmux` subcommands
UNCONDITIONALLY — an explicit `--config-dir` is honored, but hook traffic is
otherwise pinned to main regardless of cwd or XDG_CONFIG_HOME, by design
(E-1450/E-1429: a fired hook is real-world activity in the real database).

So for any verify script that exercises hook behavior, "make it hermetic" is not
on the table in the usual way. The newer scripts' isolation pattern — a throwaway
config dir under the cache sandbox root, CLI run from outside the repo — does not
contain a hook invocation unless the script also threads `--config-dir` into it,
which the older ones do not and which is easy to omit silently.

This strengthens the case that the REFUSAL is the load-bearing defense, not
containment. A script that cannot run outside its own worktree cannot pollute
anything, whatever it does internally.

## Location, as a complement

The guard is the fix. But `tests/tasks/` is what makes the mistake reachable:
the scripts are shelved beside a real suite, `just test` runs `tests/`, and any
glob over `tests/` sweeps them in. Whatever the guard's final shape, consider
whether these belong under a path that does not read as a suite — cheap, and it
removes the invitation rather than only refusing it. (Filed separately as E-2077,
declined as a duplicate of this task; recorded here so the option is not lost.)

## The asymmetry that makes this agent-shaped

Recovering from the damage above surfaced the sharpest version of the problem.
`internal/hookcmd/claude.go` opens `runClaude` with `if !supportedAgent() {
return nil }` — silent, nil, no stdout, exit 0. The predicate reads the PROCESS
environment (`CLAUDE_CODE_ENTRYPOINT=cli`).

So the same script, byte for byte:

- Run by a human at a prompt: the hook gate closes, nothing happens, rc=0. Inert.
- Run by an agent's shell tool: the process IS Claude Code, every harness gate
  opens, and the writes land in the main database.

Mike could not even undo the damage by hand — the recovery command was silently
inert for him and worked immediately from the agent's shell.

This is not incidental. The scripts are dangerous SPECIFICALLY in the hands of
the actor most likely to run them wrongly, and harmless in the hands of the one
who would notice. Any guard designed against a human operator is testing the
case that was never a problem. The refusal has to fire in the agent's process,
which argues for the shared-helper guard in the scripts themselves (which runs
whoever invokes it) over anything keyed on who is asking.

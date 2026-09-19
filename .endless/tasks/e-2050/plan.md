# E-2050 — keep self-dev work out of the real database, ledger, and checkouts

Scope and vocabulary set by E-2048's review (2026-08-24). Read E-2048's
outcome for the evidence behind every claim here.

## The one rule

**Every write follows the resolved isolation context.** Real context → the real
project root, git-committed. Disposable context → that context's own root, never
committed. One resolver, consumed by every writer — not a decision each writer
makes for itself.

This is what survives from ED-1525. What does not survive is ED-1525's *location*
premise ("the sandbox sits outside any repo, so it is structurally unable to ride
a land rebase into main"): ED-1554 moves the sandbox to
`<worktree>/.endless/sandbox/`. See Q1.

## Vocabulary — required in titles, and "route" is banned as a verb

One word meant three mechanisms, which is why titles collided and why the same
defect was filed twice. Use these instead.

| Term | Means | Chosen by | Resolver today |
|---|---|---|---|
| **DB target** | which `endless.db` a command opens | `--db main\|sandbox` | `config.apply_db_choice` (Py), `monitor.ConsumeDBContextFlag` (Go) |
| **artifact root** | which tree a command writes non-DB files into, and whether it commits them | nothing — each writer decides | `eventcmd.ledgerRoot()` for the ledger **only** |
| **binary selection** | which `endless-go`/`endless` build runs | cwd / hook config | E-1972 — **not this epic; coordinate only** |

A title in this area must name which of the three it changes.

## The root cause

There is no fourth term for the thing all three consult, because it is not a
value. "Am I in a disposable context?" is a **predicate re-derived from a path
shape** at each site that cares — `monitor.IsSandboxActive()` asks whether
`ConfigDir()` sits under `CacheDir()/sandboxes` (ED-1528).

That is why this area keeps reopening:

- A writer that never calls the predicate is unisolated by default, silently.
  That is E-2035: the ledger got `ledgerRoot()`, the doc mirrors got nothing.
- The predicate is invisible to the agent, so the only affordance is remembering
  `--db main`. That was E-1839's complaint.
- Any change to where the sandbox lives silently invalidates every consumer.
  ED-1554 is that change.

The fix is to make the isolation context an explicit resolved value that every
writer consults, rather than a path shape each writer may or may not re-derive.

## The set

- **E-2035** — the artifact-root resolver. The load-bearing task.
- **E-1730** — excise the leaked lines already in the real ledger.
- **E-1665** — a failed mutation must not leave a committed ledger line.

Nothing else belongs here. Adjacent bodies, deliberately not absorbed:
binary selection (E-1972), ledger-rebuild trustworthiness (E-1935 and its
children), test-run isolation (E-1897 / E-1045 / E-995), land and commit
machinery (E-1272 / E-1971).

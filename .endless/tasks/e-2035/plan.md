# E-2035 — the DB target must govern file writes, not just the DB

## Problem

`--db sandbox` isolates the database and, since E-1729, the event ledger. It
governs nothing else. Every other file a command writes still resolves a REAL
project root and git-commits there.

Concrete: a sandbox-routed `endless --db sandbox decision add` wrote decision
mirrors into a worktree and auto-committed two junk commits ("Endless: add
decision ED-1", "ED-2") onto a task branch. They had to be reset by hand; a
session that did not check `git log` would have landed sandbox fixtures into
main. Sandbox ids start at 1, so the files also collide with the real ledger's
ED-1 / ED-2 namespace.

E-1729 already fixed this exact mechanism for the ledger and established the
shape — sandbox-local directory under the sandbox config dir, and NO git commit,
because a sandbox is disposable and is not a repo. That fix was applied at one
call site instead of made a rule, so E-1747's doc mirrors reintroduced it.

## Approach

Make "where do artifacts go" a function of the resolved DB target, in one place,
rather than a decision each writer makes independently.

**Scope, restated by Mike 2026-08-27.** The rule is not limited to files a
`--db sandbox` command writes. **The sandbox is where ALL files that need to
change but must NOT be committed are written.** An earlier reading of this plan
took the doc-mirror list below as the boundary and concluded that files written
at worktree-provisioning time (`.claude/settings.json`) were out of scope. They
are not. If a file has to differ per worktree and must never ride a land into
main, its home is the sandbox — whatever wrote it, and whenever.

1. **One resolver.** A single accessor answering "artifact root for the current
   target" — the project root under `--db main`, the sandbox config dir under
   `--db sandbox` — plus "may I commit?" (false in sandbox, always). Python
   side has `config.RESOLVED_CONFIG_DIR` already pinned by `apply_db_choice`,
   so the target is known; nothing consults it for file paths today.

2. **Route every writer through it.** Known writers:
   - `decision_cmd._mirror_decision_body` / `_commit_doc_on_main`
   - `task_cmd._mirror_doc_to_worktree` (plans, outcomes, analysis) and
     `worktree_cmd._commit_doc_in_worktree`
   - `worktree_cmd._materialize_task_docs`
   Audit for others rather than trusting this list.

3. **Write them into the sandbox. Do not suppress them.** An earlier draft of
   this plan proposed skipping the write entirely, on the grounds that a sandbox
   decision has no history worth version-controlling. That is wrong for three
   reasons, all the user's:

   - Suppressing means the write path never executes under `--db sandbox`, so a
     bug in it cannot be caught by the mode that exists to catch bugs.
   - Files under the sandbox are reaped with the sandbox, so persistence costs
     nothing and cleans itself up.
   - The rule has to be generic. Companion files for decisions are one of
     several kinds; answering only for them guarantees the next kind gets it
     wrong, which is exactly how E-1729's ledger fix failed to prevent this.

   So: same relative layout, rooted at the sandbox instead of the project, and
   never a git commit — a sandbox is not a repo.

4. **Make the rule enforceable.** A test that runs the mirror-writing commands
   under `--db sandbox` and asserts the git status of both the worktree and the
   main checkout is unchanged. Without it the next artifact-writing feature
   reintroduces this a third time, which is the actual failure mode here.

## Verification

- `--db sandbox decision add` writes its companion file UNDER THE SANDBOX, at
  the same relative path it would use in a project, and creates no commit; no
  file appears under any git checkout.
- Same for `task update --text` / `--outcome` / `--analysis` under sandbox.
- Reaping the sandbox removes them.
- Under `--db main` every mirror is written and committed exactly as today.
- The regression test above fails if a writer is added that bypasses the
  resolver.

## Also in scope: `.claude/settings.json` (folded in from E-1732, 2026-08-27)

This is the same defect at a different call site, and it is the one that costs
a live session roughly every third time.

**What it is.** `.claude/settings.json` is a tracked file that legitimately
needs a different body in every worktree — hooks pointing at that worktree's
`bin/endless-go`, plus the `XDG_CONFIG_HOME` env block. `just
claude-settings-init` generates that body and hides it with `git update-index
--skip-worktree`. `.gitignore` says the omission of a rule for this path is
deliberate and names skip-worktree as the mechanism.

**Why it breaks.** skip-worktree means "pretend this file never changes." Any
operation that must rewrite it in the working tree — checkout, rebase, merge,
pull — hits a contradiction and refuses with "your local changes would be
overwritten." Every rebase spanning a change to the committed copy stops dead
and has to be hand-unwound.

**A second, silent bug in the same recipe.** The generator reads
`git show HEAD:.claude/settings.json` — the **worktree branch's** HEAD, not
main's. On a fresh worktree those agree; on a stale one they diverge without a
word. Measured 2026-08-27 in the E-1732 worktree: main had added
`"autoMemoryEnabled": false` after that branch forked, so the generated file
omitted it and **auto-memory was live in a worktree the project requires it be
off in**. Any key added to the committed copy after a worktree forks is
silently dropped from that worktree. `CLAUDE.md` describes the recipe as
preserving keys "from the main-branch HEAD", which is not what it does.

**Direction (Mike, 2026-08-27): a symlink into the sandbox.** The per-worktree
body lives in the sandbox; the worktree path points at it. Two things to settle
while implementing, neither resolved here:

- **A symlink at a tracked path does not escape git.** Git records the mode
  change (100644 → 120000) and the link target as a modification, so a tracked
  `.claude/settings.json` symlink needs skip-worktree exactly as much as the
  file does. The symlink only helps once that path is not in the index for the
  worktree.
- **`.claude/settings.local.json` is already the gitignored slot** and is
  already in use on the main checkout. Layering the per-worktree hooks and env
  there, and leaving the tracked `.claude/settings.json` alone, would end both
  bugs with no symlink and no new git mechanism: shared settings flow through
  rebases normally, and the stale-HEAD merge disappears because there is
  nothing left to merge. The cost to weigh is that a worktree's
  `settings.local.json` does not inherit the main checkout's personal one,
  which is plausibly why the current recipe merges into the tracked file
  instead.

Whichever shape wins, the acceptance test is the same: **`git rebase main`
inside a worktree must succeed with no manual step**, and a key added to the
committed `.claude/settings.json` after the worktree forked must reach that
worktree.

**Interaction with E-1964.** E-1964 moves the sandbox to
`<worktree>/.endless/sandbox/`, self-ignored by a `.gitignore` containing `*`.
That is the directory a symlink would target, so this item and E-1964 share a
destination and should agree on ordering rather than each assuming the other.

### Why `settings.local.json` was passed over in E-998, and why that no longer holds

Recorded so this is not re-derived a third time. E-998's plan text considered
`.claude/settings.local.json` and rejected it for two stated reasons:

1. **Single owner per file.** `settings.local.json` is hand-edited by Mike for
   `permissions`. A generator writing there "would have to read-merge-write,
   fighting any concurrent edits", so `settings.json` would be the generated
   file and `settings.local.json` the human-managed one.
2. **"Aligned with the user's explicit instruction. The task brief specified
   `settings.json`."**

**Reason 2 does not survive contact with the record.** That brief was written
by the Claude session working E-998, not by Mike; so was the plan text quoting
it back as an instruction, and so was the judgment that skip-worktree is "a
small one-time cost." Mike has since said plainly that he neither wrote nor
reviewed it. It is a Claude-authored design choice wearing a citation, and it
should carry no weight as prior authority here.

**Reason 1 has since stopped being true.** `just claude-settings-init` today
merges three sources — the committed keys, the working copy's `env` block, and
the freshly-rewritten hooks. It performs exactly the read-merge-write it chose
`settings.json` to avoid, and it merges against the wrong source (the worktree
branch's HEAD rather than main's). Whatever "clean owner" bought in E-998, the
recipe has already spent.

**E-998 also predicted this failure and priced it wrong.** Its plan says: "If
the committed file ever changes upstream (e.g. someone adds another plugin),
the worktree's skipped copy will not pick up the change automatically", with a
mitigation of re-running the recipe by hand. Two errors in that: nothing
triggers the mitigation, so the cost recurs per worktree per upstream change
rather than being one-time; and the consequence was estimated as "misses a
plugin", when what it actually did was drop `"autoMemoryEnabled": false` and
leave memory switched on in a project whose rules forbid it.

E-998's two remaining rejected alternatives, for completeness: untracking
`.claude/settings.json` (rejected as un-sharing `enabledPlugins`) and
per-worktree `.git/info/exclude` (rejected — like `.gitignore`, it has no
effect on tracked files, which is still correct).

## Note on scope

This is deliberately the rule, not the decision-mirror bug. Filing the single
call site is what let it recur after E-1729.

## Also in scope: delete `monitor.ForceRealDB` (folded in from E-1732)

`monitor.ForceRealDB()` (`internal/monitor/db.go:117`) has **no production
caller**. Its only reference outside `db.go` is
`internal/monitor/sandbox_test.go`. The hook is pinned by `monitor.PinMainDB()`
at `cmd/endless-go/main.go:152`, which is a strict superset of what
`ForceRealDB` did for it: unconditional rather than gated on
`IsSandboxActive()`, and DB-path-only in both cases.

Eleven comments in `db.go` still describe the hook as calling it — db.go:29,
:43, :85, :107-116, :165-179, :220, :227, :256, :318, :669, :720, :826. They
are the primary written account of how DB pinning works, and they are wrong.

Do: delete the function and its test, and correct those comments. No behavior
change. It belongs here rather than in its own task because this task is the
one rewriting how the isolation context is resolved and consumed, and the
comments it would leave behind describe the mechanism this task replaces.

Not to be confused with a defect: nothing is broken by the function existing.
The cost is that the next reader trying to understand sandbox-vs-real routing
reads eleven accurate-sounding comments about a code path that does not run.

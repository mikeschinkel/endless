## What the two parts mean, in plain terms

**1. Land silently commits endless's own files.** Before rebasing, land looks at whatever is uncommitted in the main checkout. Anything it recognizes as a file endless itself writes -- the ledger, `verbs.jsonl`, snapshots, `config.json` -- it stages and commits as "Endless: auto-record session activity" without telling anyone. The argument for removing this: those files are supposed to be committed the moment they are written, so land should never find them uncommitted. When it does, that is a bug being papered over. Land should stop and name the files instead of sweeping them up.

**2. Its advice after a failed rebase could destroy work.** When the rebase conflicted, the error message told the user to run `git checkout main -- <endless's file patterns>`, which discards the branch's version of those files without anyone looking at them. If the conflict was real, that silently loses work. The message should name what conflicted and show how to inspect it.

## Where this stands (verified 2026-08-25)

- **Part 2 is done**, shipped separately under E-1417 (commit b1efcc2). `_rebase_conflict_message()` names the commit that failed to replay and the conflicting files, and offers the `git checkout` recovery only when *every* conflicting path is an endless-managed file; otherwise it presents recoveries as candidates to judge between, flagged as possibly wrong.
- **Part 1's premise does not hold.** The auto-commit step still fires: 53 "Endless: auto-record session activity" commits on main, four of them on 2026-08-24. Endless-managed files still arrive at land uncommitted despite E-1206 and E-1208 committing at write time, so removing the step today would turn routine lands into refusals. Why they still arrive dirty is the open question underneath part 1.

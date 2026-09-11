## What is left behind

E-2113 classified an interrupted git child at the source — `monitor.runGit`
wraps a child that died on SIGINT with `ErrGitInterrupted` — and put the guard
inside the three functions that record a git-derived fault, so a probe added
later inherits it rather than having to remember it. Two of those three,
`recordDefaultBranchFault` and `recordReapDefaultBranchFault`, are correct but
currently unreachable.

`resolveDefaultBranch`'s fall-through helpers — `originHeadBranch`,
`gitConfigValue`, `branchIfExists` — each swallow their `runGit` error and
return `""`. That is deliberate: a step that does not resolve is supposed to
fall through to the next, and E-1166's finding (origin/HEAD is unset on a fresh
clone) is why there are four steps rather than one. But it means the
classification is discarded there, and every caller sees only
`ErrDefaultBranchUnresolved`.

So a Ctrl-C landing during resolution still files an incident — ERR-0011 rather
than ERR-0010, about the same innocent worktree, for the same reason E-2113
removed.

## The memoized failure makes it outlive the signal

`defaultBranchCache` stores errors as well as answers. An interrupted resolve is
cached, so every later probe of that repoDir in the same process reads the
cached failure — a single interrupt poisons the rest of the process rather than
costing one tick. E-2113's plan noted this under "Not in scope" and called it
harmless *there*, because the process that took the SIGINT is exiting. It is not
harmless here: `session monitor` is not the only caller, and the reaper runs on
PreToolUse/PostToolUse in a process that keeps going.

## Fix direction

Let an interrupt SHORT-CIRCUIT the fall-through rather than be absorbed by it:
the three helpers return their error alongside the name, and
`resolveDefaultBranch` bails on `errors.Is(err, ErrGitInterrupted)` instead of
trying the next candidate. An interrupted probe has said nothing about which
branch is the default, and trying the next step is asking a question whose
answer was already lost. Everything else about the four-step order is unchanged.

Worth deciding alongside: whether `DefaultBranch` should decline to memoize an
interrupted resolve at all. The cached failure is not a fact about the
repository, and it outlives the signal that produced it.

## Why E-2113 did not fold this in

Two things make it more than a one-line guard.

The step order is mirrored by `endless.worktree_cmd._default_base_branch` on the
Python side, and `tests/test_default_branch_parity.py` asserts the two agree
case for case rather than trusting the comment. Changing what each step returns
has a cross-language contract to keep.

And E-2111 is live in the same files.

## Scale

This is the tail of E-2113's defect, not a second instance of it. `DefaultBranch`
is memoized and resolves once per repoDir; the probe E-2113 fixed re-runs on
every row every two seconds, which is why a Ctrl-C hit it with near-certainty.
The window here is much narrower — but it is the same misclassification, and the
memoization is the part that makes it worth closing rather than tolerating.

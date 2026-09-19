## Observed

Landing E-1997:

```
Error: rebase conflict while rebasing your branch onto main.

Conflicting files:
  (none reported)
```

The actual git failure was:

```
error: Your local changes to the following files would be overwritten by checkout:
	.claude/settings.json
Aborting
error: could not detach HEAD
```

That is a refusal to start the rebase, not a conflict during one. No commit was
applied, no merge was attempted, and there was nothing to resolve.

## Why the advice was actively harmful

Both offered recoveries were wrong for this failure:

1. `git rebase main` / `git add` / `git rebase --continue` — there is no rebase
   in progress to continue, and re-running the same command reproduces the same
   refusal.
2. `git diff main...HEAD > patch; git reset --hard main; git apply` — a
   `reset --hard` proposed for a situation where nothing had gone wrong with the
   commits. The header text even warns "the wrong recovery can duplicate or lose
   work", which is exactly what following it here could do.

## The rule this suggests

"(none reported)" should not be a value the error prints — it means the code
reached "this is a conflict" without any conflicting path to name, which is the
one case where the diagnosis is provably unsupported. When the conflicting-file
list is empty, the honest output is git's own stderr and no recovery
prescription.

## Scope

Distinct from E-1998. That one is about the skip-worktree collision that
produced this particular git failure. This one is about the classifier: any
non-conflict rebase failure — a dirty tree, an unborn base, a hook refusal —
would be mislabeled the same way, with the same two inapplicable recoveries.
Fixing E-1998 removes one trigger, not the misclassification.

## Modes

Not self_dev-specific. The classifier is in the shared land path, so a
downstream project hitting any non-conflict rebase failure gets the same
misdiagnosis and the same `reset --hard` suggestion.

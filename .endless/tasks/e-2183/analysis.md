A worktree-scoped `just install` runs its OWN branch's recipes for
machine-level Claude config, so a branch cut before a config fix landed
re-applies the old behaviour. E-2166 removed the per-worktree hook pin from
`claude-settings-init` and swept 99 of 122 worktrees, but the `install`
recipe's worktree-scoped path calls `just go-work-init`, `just build`,
`just dev-sandbox-init` and `just claude-settings-init` with a BARE `just`,
which resolves the worktree's justfile — so running `just install` in any of
those ~99 pre-land branches re-pins its hooks at its own bin/endless-go. That
is the same class of bug `.endless/hooks/post-worktree-create.sh` already
avoids by passing `--justfile <main>/justfile` explicitly, with the reason
written in its header: a worktree on an older branch carries an older
justfile.

Two pieces. (1) The worktree-scoped install resolves the CONFIG recipes
(`claude-settings-init`, `dev-sandbox-init`, `go-work-init`) from main's
justfile, as post-worktree-create.sh does — but NOT `build`, which must stay
the branch's, because a worktree exists to build candidate code and a branch
that changed how it builds must build its own way. That split is the durable
statement: machine-level config comes from main, candidate behaviour comes
from the branch. (2) DELETE the `claude-settings-sweep` recipe E-2166 added.
It was a one-time remediation with a generic name for a problem no PRODUCT
user has, and it is maintenance debt the moment its job is done. Removal is
safe once `endless worktree sync --apply` has delivered the fixed recipe to
live branches; sync is the existing product mechanism for exactly this
("a fix to a shared file can reach main and reach no working checkout at
all") and is what replaces re-running a sweep.

One residue survives and should be stated, not fixed here: `just
claude-settings-init` invoked DIRECTLY in an unsynced worktree still gets
that branch's recipe. Nothing in the justfile can close that; only rebasing
the branch can, which is what makes "keep task worktrees synced" the answer
rather than a recipe that re-fixes the symptom.

Self_dev only (ED-1571): no other project has a per-worktree install or a
land, so none of this mechanism exists there.

## Open questions

**Q1. What is the trailer's key?**

| Option | Pros | Cons |
|---|---|---|
| A. Fixed: `endless-main: <sha>` | One key to search for in every repo. | Reads wrong in a repo whose default branch is not main. |
| B. Named for the default branch: `endless-<primary_branch>: <sha>` | Says which branch the SHA is on. | The key differs per repo, so a reader must know the branch name to search; renaming the branch changes the key for new commits only. |
| C. Fixed key, branch in the value: `endless-base: main@<sha>` (or two trailers, `endless-branch` and `endless-commit`) | One key everywhere, and it still names the branch. | none |

Recommendation: C.

**Q2. What is the branch called?**

| Option | Pros | Cons |
|---|---|---|
| A. `endless-meta` | Matches the separate-repo naming convention. | none |
| B. `endless/meta` (a namespace) | Leaves room for other Endless branches under `endless/`. | A branch named `endless` can then never exist, since git cannot hold both. |

Recommendation: A.

**Q3. How does Endless write to the branch while main is checked out?**

| Option | Pros | Cons |
|---|---|---|
| A. Git's low-level commands (`hash-object`, `mktree`, `commit-tree`, `update-ref`) with no checkout | No extra directory; no working tree to get dirty. | Reading a record means `git show` against the branch instead of opening a file. |
| B. A dedicated worktree of the branch (e.g. under `.endless/worktrees/`) | Records stay ordinary files that tools and people can open. | One more working tree that can be left dirty or deleted by hand. |

Recommendation: A for writes, with reads going through Endless's commands.

**Q4. When does a clone fetch the branch?**

| Option | Pros | Cons |
|---|---|---|
| A. Before every read of records | Always current. | A network call on each read; offline reads need a fallback to the local copy. |
| B. On a schedule, alongside main-sync's fetch | Reads stay local and fast. | Records can be minutes stale. |

Recommendation: B, with A available as an explicit refresh.

**Q5. What happens to records already on main?**

| Option | Pros | Cons |
|---|---|---|
| A. Copy them to the new branch, then delete them from main's tree in one commit | Main's tree is clean from then on; history keeps the old copies. | One large deletion commit on main. |
| B. Leave existing records on main; only new records go to the branch | No large commit. | Records split across two places forever. |

Recommendation: A.

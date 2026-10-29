## Open questions

**Q1. What is the trailer's key?** The value names Git's default branch and the commit, e.g. `main@<sha>`. A trailer key must be letters, digits and hyphens: `git interpret-trailers --parse` ignores a line whose key has other characters.

| Option | Pros | Cons |
|---|---|---|
| A. `endless-default: main@<sha>` | Names Endless. | Reads as a default of Endless's, which it is not. |
| B. `git-default: main@<sha>` | Uses Git's own term. | Does not say Endless wrote it, so it could collide with another tool's trailer. |
| C. `Endless[git-default]: main@<sha>` | Names Endless and gives a pattern for later annotations. | Git does not recognize it as a trailer, so `git interpret-trailers` and `git log --format=%(trailers)` never see it. |
| D. `endless-git-default: main@<sha>` | Names Endless and uses Git's term; the `endless-<name>` prefix is a pattern for later annotations, and git recognizes it. | none |
| E. `endless-base: main@<sha>` | Names Endless; short. | "base" is not Git's term for the default branch. |

Recommendation: D. (Options A to C were Mike's, 2026-10-29.)

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

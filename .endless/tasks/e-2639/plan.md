# Open questions

Terms used below:
- **rewrite**: any change to a project's git history that gives existing
  commits new SHAs, here specifically removing a file from every commit.
- **commit map**: a list pairing each old commit SHA with its new one.
  `git filter-repo` writes one; other tools do not.
- **stored SHA**: a commit SHA Endless keeps in its database or ledger (see
  the analysis for the list).

## Q1. How does Endless come through a rewrite?

| Option | Pros | Cons |
|---|---|---|
| A. An Endless command that performs the rewrite (wrapping `git filter-repo`) and then remaps every stored SHA using filter-repo's commit map | The map is exact; one step for the user; Endless knows a rewrite happened | Makes `git filter-repo` a dependency; does nothing for a rewrite done with another tool (BFG, interactive rebase) |
| B. A repair command the user runs after a rewrite done with any tool, which builds a commit map itself (from filter-repo's map when present, otherwise by matching commits by author, date and message) | Works whatever tool did the rewrite | Matching without filter-repo's map can pair commits wrongly when two commits share author, date and message |
| C. Stop storing SHAs that a rewrite breaks: replace each stored SHA with something that survives a rewrite (a patch-id, or the task branch name) | Nothing to run after a rewrite | Resume needs a commit to check out, and a patch-id cannot be checked out; `task landed` could no longer show a SHA you can `git show` |
| D. Build nothing; document fixing each stored SHA with `endless correct` | none | One correction per row: Endless's own ledger holds 917 landing SHAs; post-land `head` cannot be corrected at all |

Recommendation: A, with B's matching as a fallback only when no filter-repo
map exists. A covers the case that prompted this (removing a secret or a large
file), and the map makes it exact.

## Q2. How are stored SHAs in past ledger entries updated?

The ledger is append-only today.

| Option | Pros | Cons |
|---|---|---|
| A. Append one event that records the commit map; replay and every reader translate old SHAs through it | Keeps the ledger append-only; the rewrite is recorded and auditable | Every reader of a stored SHA must translate it; one that does not shows a dead SHA |
| B. Rewrite the SHAs inside the ledger `.jsonl` files during the same rewrite | Ledger reads correctly with no translation | Breaks the append-only rule; tamper detection (E-2554) would have to allow it |
| C. Append one ordinary correction per stored SHA | Uses the existing correction mechanism | Hundreds of correction events per rewrite in an active project; post-land `head` needs a new correction target |

Recommendation: A. It keeps the append-only rule and costs one event.

## Q3. Does this cover a file inside `.endless/`?

A secret pasted into a task field ends up in the ledger, the doc mirrors and
`LESSONS.md`, which E-2553 explores separately.

| Option | Pros | Cons |
|---|---|---|
| A. Only files outside `.endless/`; secrets inside `.endless/` stay with E-2553 | E-2553 can settle ledger redaction with tamper detection (E-2554) first | A secret in a task field needs a second, separate procedure |
| B. Any file, including ledger segments and mirrors | One command for any secret | Has to decide how a ledger entry is redacted before E-2553 and E-2554 have |

Recommendation: A, with the command refusing a path under `.endless/` and
naming E-2553's eventual command once it exists.

## Q4. What happens to task branches and worktrees that exist at rewrite time?

A branch forked before the rewrite and not rewritten with it can bring the
removed file back when it lands.

| Option | Pros | Cons |
|---|---|---|
| A. Refuse the rewrite while any task branch is unlanded | The file cannot come back through a branch | A leaked secret waits until every task is landed or abandoned |
| B. Rewrite every task branch along with main, and reset each worktree onto its rewritten branch | No waiting | A worktree with uncommitted changes cannot be reset without risking them |
| C. Rewrite every task branch along with main; leave each worktree for its session, which is told to reset | No waiting; uncommitted work is untouched | A worktree whose session never acts keeps the old commits and can still bring the file back |

Recommendation: B, refusing only while a worktree has uncommitted changes and
naming those worktrees.

## Q5. Who updates the remote?

| Option | Pros | Cons |
|---|---|---|
| A. The command force-pushes all branches and tags after the rewrite | The remote stops serving the file without a step the user can forget | Force-pushing overwrites anything others pushed since; it is irreversible once done |
| B. The command prints the push commands; the user runs them | The user checks the result before anything leaves the machine | The remote keeps serving the file until the user pushes |

Either way GitHub keeps old objects reachable through cached views, forks and
pull-request refs until GitHub support purges them; the command should say so.

Recommendation: B. The push is outward-facing and irreversible.

## Q6. What about other clones of the project?

Another machine's clone still holds the old history and its own ledger
segments.

| Option | Pros | Cons |
|---|---|---|
| A. The rewrite event (Q2 A) tells every clone's Endless that main was rewritten; it refuses to push until that clone is reset or re-cloned | Another clone cannot push the file back | Depends on Q2 A; a clone that has not pulled the event still can |
| B. Document re-cloning on every other machine | none | Relies on the user remembering every clone |

Recommendation: A.

# After this is settled

Once the command exists, use it to remove `cmd/gomion/gomion` from gomion's
history, on its remote as well (23 MB, in ten `checkpoint` commits).

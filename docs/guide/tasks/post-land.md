# Post-land items

Some plan items can only be done after the land: the work needs the landed, migrated, locally installed result. Writing a row into a table the task's own migration creates is the usual case — the main database gets the migration only when the work lands and is installed. A **post-land item** is such an item that **produces no code**. It stays in the task's plan rather than becoming a follow-up task, because it needs none of a task's lifecycle: no worktree, no verify, no land.

**Code means a follow-up task.** If doing the item would change code, it is not a post-land item: file a follow-up with `endless task add "<title>" --cleans-up <id> --description "..."`. Code needs its own verify and land.

---

## Marking an item

The mark goes on a `deliverable` item only — an exclusion is kept or not, never done later.

- **Planned:** `post_land = true` in an `--items-file` `[[item]]` table, or `--post-land` on `endless task plan add` and `endless task plan edit` (`--no-post-land` clears it). The mark is part of what the user approves: changing it makes the item differ from its approval until the user approves it again.
- **Unforeseen:** you reconciled an item `blocked` because it cannot be done before the land, and the reconcile filed a `blocked` question. If the user agrees it is post-land work, record their answer with `endless question answer EQ-<n> "<answer>" --by user --post-land`. That marks the item and counts as the user's approval of the mark — one act by the user, no separate approve.

## Before the land: `deferred`

Reconcile a post-land item as **`deferred`**. Only a post-land item may carry it. It is not a discrepancy: it files no question, and the gate before `unverified` passes it. The reconcile output says that the land will set `landed` and names the post-land reconcile to run then.

An item that cannot be done before the land and is not marked post-land is reconciled `blocked`, as before.

## The land: `landed`

`endless worktree land` — `--record-only` included — sets the task to **`landed`** instead of `assumed` when it has an open post-land item, and lists the open items. `--keep-status` leaves the status alone, as always.

An **open** post-land item is an active post-land item whose current verdict is missing or `deferred`, or is a discrepancy whose question is still open.

`landed` (⇣; the session-status action ⊡ post-land, ranked above ⏚ shipped):

- **still blocks its dependents** — the work is not finished;
- is **frozen** apart from its post-land items: no edits, no new items, and the reconciliation in force at the land never changes;
- is listed by `task active`, and `task show`, `task list` and the per-prompt hook context name its open post-land items;
- moves to `assumed` **by itself**, never by hand: `task assume`, `task confirm` and `task update --status assumed` are refused, and there is no route to `confirmed`;
- may be reopened (`revisit`), declined, obsoleted or superseded by the user.

## After the land: the post-land reconcile

Once the work is landed and installed, do the post-land items, then run `endless task reconcile <id>` on the `landed` task. It shows the post-land items with their current verdicts and a file to fill in, and `--file` records it.

- It takes entries for **post-land items only**, and refuses any other. Name any subset of them, at least one; an item you leave out keeps the verdict it has.
- Each entry is written as its own **post-land row**, keyed to the reconciliation in force at the land. Earlier reconciliations are untouched and no other item's verdict is copied. An item's current verdict — what `task plan list`, the reconcile display, the hook and the gate read — is its newest post-land row, else its entry in the reconciliation.
- `deferred` again means the work still waits.
- A discrepancy files a question, exactly as before the land, and `answer_holds` / `reask` compare against the item's previous **post-land** row. The task stays `landed` until the item settles: answer the question, then reconcile it again.
- **Refused when the branch has code since the land**: if the branch has changes outside `.endless/` (its fingerprint is not `empty`), nothing is recorded. That change is code, so it belongs to a follow-up task (`--cleans-up <id>`); move it there and reconcile again.

The reconcile that leaves **no open post-land item** — every one has a non-`deferred` verdict and every post-land discrepancy's question is settled — moves the task to `assumed`.

## Withdrawing a post-land item

The user, and no agent, may withdraw a post-land item of a `landed` task: `endless task plan withdraw <id> P<n>`. Its open post-land questions are closed as moot, and the task moves to `assumed` if that leaves no post-land item open. An agent that thinks an item should go asks the user.

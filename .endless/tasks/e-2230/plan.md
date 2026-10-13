Open questions for this brainstorm. Answer each in place on its **Answer:** line. The options, pros/cons and recommendations are there to answer from, not decisions already made.

Terms used below:
- **Findings lane:** the task statuses whose deliverable is outcome text (`underway → unreviewed → completed`), used today by research and brainstorm tasks. Defined in the canonical status lifecycle.
- **Settled:** a worktree with a clean working tree and every commit's content on the base branch (`endless task unsettled`). Unsettled is shown as ◆ in `session status`.
- **Sandbox database:** the per-worktree database that `endless` commands use in a self-dev worktree unless given `--db main`. It exists only in projects with self-dev enabled.
- **Spike:** a task whose agent writes code to answer a question. The code is not meant to land, and the deliverable is the outcome text.

## Q1. How is a spike represented?

| Option | Pros | Cons |
|---|---|---|
| **A. A new task type, `spike`** | Type is what selects a lifecycle lane today, so its behavior (findings lane, gets a worktree, `worktree land` refuses) keys off one value; visible in every task list's type column | Every place that enumerates types (transition table, filters, validators, guide) gains a value |
| **B. A flag on research tasks** (for example `--throwaway`, set by `task add` / `task update`) | No new type | The research justification gate applies to spikes; a spike is invisible in a list unless the flag is displayed |
| **C. No distinct kind:** a spike is a research task whose agent is told not to land | Nothing to build | Nothing stops an accidental land; its worktree stays unsettled forever, so ◆ is permanently on for it |

**Recommendation: A.** Type already selects behavior, so a spike's one distinctive rule (never land) lives where the rest of the lane rules live.

**Answer:**

## Q2. Does claiming a research or brainstorm task create a worktree and a sandbox database?

(A spike always needs a worktree, since it writes code; this question is about research and brainstorm.)

| Option | Pros | Cons |
|---|---|---|
| **A. Keep creating both, as today** | One claim path for every type; an agent that decides mid-task to prototype already has a place to do it | A directory to create and reap per task, contributing nothing; in self-dev, every `endless` command that should reach the real project needs `--db main` |
| **B. Neither:** the session runs in the main checkout against the main database | No worktree to create or reap; no `--db main` friction | The agent's cwd is the main checkout, so a stray file write lands there unless enforcement blocks Write/Edit for these types; prototyping means reclassifying the task (to a spike) |
| **C. Create the worktree lazily,** only when the agent first needs to write a file | Zero cost for pure discussion; prototyping still possible without reclassifying | The session has to move into the new worktree mid-task (`/cd`), and tasks of one type end up sometimes with a worktree and sometimes without |
| **D. Worktree but no sandbox database** | Commands reach the real database without `--db main` | Experimenting with `endless` commands inside the task writes to the real database (self-dev only) |

**Recommendation: B**, with enforcement refusing file writes for research and brainstorm, because Q1-A gives prototyping its own type.

**Answer:**

## Q3. How does the monitor show where a findings-lane task stands?

The states that matter: the agent is working; the agent is waiting for the user's reply; the agent has delivered (`unreviewed`); the user has accepted it (`completed`).

| Option | Pros | Cons |
|---|---|---|
| **A. Drop ◆ for findings-lane tasks and show status, with `unreviewed` marked as "ready to read"** | Uses state that already exists; no new signal | During the discussion, "agent working" and "agent waiting for you" look the same unless the session's own state is shown |
| **B. A plus a "your turn" marker:** set when the task is `unreviewed` or its session is waiting for input | One cue for "act now", whatever the reason | In a brainstorm, waiting for the user is the normal state, so the marker is on for most of the discussion; it's only as accurate as session idle detection |
| **C. Replace ◆ with one per-lane "done?" column:** settled plus status for the implementation lane, outcome plus status for the findings lane | One column means "is this finished?" for every type | ◆ also reports worktree hygiene (uncommitted files); for a spike, which does have a worktree, folding it into "done?" hides uncommitted work |

**Recommendation: A.** Add B's session-waiting cue only if A proves insufficient in use.

**Answer:**

## Q4. What happens to a spike's worktree and branch after the spike is completed?

| Option | Pros | Cons |
|---|---|---|
| **A. Both become reapable like any landed task's,** despite the unlanded code | Nothing accumulates | The spike's code is gone once reaped; anyone wanting to revisit it has only the outcome text |
| **B. Reap the worktree directory, keep the branch** | Code stays retrievable from git | Branches accumulate, one per spike, until someone prunes them |
| **C. Reap both, but keep a tag** on the final commit first (for example `spike/<id>`) | Code stays retrievable; `git branch` stays uncluttered | Tags accumulate instead, and are less visible than branches |

**Recommendation: C.** Retrievable without cluttering the branch list.

**Answer:**

Open questions for this brainstorm. Answer each in place on its **Answer:** line. The options, pros/cons and recommendations are there to answer from, not decisions already made.

Terms used below:
- **Findings lane:** the task statuses whose deliverable is outcome text (`underway → unreviewed → completed`), used today by research and brainstorm tasks. Defined in the canonical status lifecycle.
- **Settled:** a worktree with a clean working tree and every commit's content on the base branch (`endless task unsettled`). Unsettled is shown as ◆ in `session status`.
- **Sandbox database:** the per-worktree database that `endless` commands use in a self-dev worktree unless given `--db main`. It exists only in projects with self-dev enabled.
- **Spike:** a task whose agent writes code to answer a question, for example a proof-of-concept Go app. The deliverable is the outcome text. When the user is ready to complete it, they tell the agent to either **keep** the code or **discard** it.
- **Standalone spike:** a spike whose code is self-contained (its own app or module) and changes none of the project's existing files.
- **In-place spike:** a spike that changes the project's existing code, for example trying an approach inside `internal/`.

## Q1. How is a spike represented?

| Option | Pros | Cons |
|---|---|---|
| **A. A new task type, `spike`** | Type is what selects a lifecycle lane today, so a spike's rules (findings lane, gets a worktree, keep-or-discard at completion instead of a normal land) key off one value; visible in every task list's type column | Every place that enumerates types (transition table, filters, validators, guide) gains a value |
| **B. A flag on research tasks** (for example `--spike`, set by `task add` / `task update`) | No new type | The research justification gate applies to spikes; a spike is invisible in a list unless the flag is displayed |
| **C. No distinct kind:** a spike is a research task, and the agent is told what to do with the code | Nothing to build | Nothing stops a normal `worktree land` merging spike code into the project's tree; a discarded spike's worktree stays unsettled forever, so ◆ is permanently on for it |

**Recommendation: A.** Type already selects behavior, and keep-or-discard is a completion rule no other type has.

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

## Q4. Which kinds of spike does Endless support?

| Option | Pros | Cons |
|---|---|---|
| **A. Standalone only** | Keeping is always "put this directory somewhere"; one retention path | Can't try an approach inside the project's own code without reclassifying the work as a todo |
| **B. Both standalone and in-place** | Covers experiments on existing code, which is often what needs answering | An in-place spike's code is a diff to existing files, so it can't be kept as a self-contained directory; it needs a second retention form (branch, tag or patch) |

**Recommendation: B.** Experiments on the project's own code are a common reason to spike. Q6 then gives each kind its own retention form.

**Answer:**

## Q5. Where does a kept standalone spike live?

| Option | Pros | Cons |
|---|---|---|
| **A. `.endless/spikes/<name>/` in the project repo, committed to main** | Versioned and travels with every clone; Go's `./...` pattern skips directories whose names start with `.`, so a kept Go spike never enters the project's builds or tests | Puts user code inside Endless's own state directory, next to the ledger; editors and file pickers often hide dot-directories |
| **B. A project-configurable directory, default a visible top-level one** (for example `spikes/<name>/`) | Visible; the user picks the home | Language tooling may sweep it into the project's builds, tests and linters (Go's `./...` includes it unless it has its own `go.mod`) |
| **C. Outside the repo,** in a per-user Endless data directory | Nothing added to the repo | Not versioned with the project, and not shared with anyone else who clones it |
| **D. Its own new git repository** | The spike can grow into a real project | A repository to create, name and host for every kept spike |

**Recommendation: A**, pending your view on mixing user code into `.endless/`. If that's a problem, B with a default whose name starts with `.` or `_` (for example `_spikes/`), which Go tooling also skips.

**Answer:**

## Q6. How does a spike's code get to its final form when the user says keep or discard?

| Option | Pros | Cons |
|---|---|---|
| **A. A standalone spike is written in its final directory from the start;** keep is a normal land of that directory, discard drops the branch. An in-place spike is kept as a tag on its final commit (for example `spike/<id>`), never landed | Keeping a standalone spike needs no move or rewrite; the in-place diff stays retrievable without touching main | The directory name is fixed at claim, before the spike's shape is known; an in-place spike that's kept is only reachable through git |
| **B. Written anywhere in the worktree; at keep, the agent moves it into the spike directory** | The agent works wherever is natural; the name is chosen when the result is known | A move step at completion that can miss files or break relative paths; doesn't apply to an in-place spike at all |
| **C. Kept as a git tag or branch only, for both kinds** | One mechanism; nothing added to main | A kept standalone app isn't visible in the tree, and is lost if tags or branches aren't pushed |

**Recommendation: A.** Renaming the directory before keeping is a simple step, and a tag is the natural form for a diff.

**Answer:**

## Q7. When is keep or discard chosen?

| Option | Pros | Cons |
|---|---|---|
| **A. At completion, required:** completing a spike refuses without an explicit keep or discard | The user decides with the result in hand; nothing is lost or kept by accident | Completing a spike always needs one more answer from the user |
| **B. At completion, defaulting to discard** | No extra answer for the common throwaway case | A spike the user meant to keep, but forgot to say so, is lost |
| **C. Set when the spike is created, changeable until completion** | The agent knows from the start whether the code is meant to last | The intent is set before the result is known, and is easy to leave stale |

**Recommendation: A.** You described the choice as made when you're ready to complete, and an explicit choice protects the code you meant to keep.

**Answer:**

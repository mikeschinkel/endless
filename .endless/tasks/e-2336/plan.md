## Open questions

Terms used below:
- **Endless project**: a directory registered with Endless (`endless project register`). It has its own `.endless/` and ledger.
- **Anthropic Project**: the Claude Code Projects beta. It is one coordinator conversation that starts worker threads, and one Anthropic Project can span many GitHub repos.
- **Agent view**: `claude agents`, Claude Code's local dashboard for many sessions (research preview).
- **Remote Control**: a Claude Code mode that registers a session running locally with claude.ai, so it can be viewed and steered from the web, desktop or mobile apps.
- **WorktreeCreate hook**: a Claude Code hook event that replaces Claude Code's own `git worktree` creation for `--worktree`, background sessions and `remote-control --spawn worktree`.
- **Auto memory**: Claude Code's local `MEMORY.md` store under `~/.claude/projects/`. It is shared by every worktree of one git repo.

Facts marked (report) come from docs/research-2026-10-14-claude-projects-and-endless.md. That report is Claude Research background, not settled, and has not been verified here.

### Q1. What should Endless call the thing it now calls a "project"?

| Option | Pros | Cons |
|---|---|---|
| A. Keep "project" | No CLI, schema or ledger change. Matches VISION.md, the CLI help ("Project awareness system") and the "manage 10+ projects" talk. | Claude Code users see three meanings of "project": chat Projects, Anthropic Projects, and `~/.claude/projects/` / `CLAUDE_PROJECT_DIR`. An Anthropic Project sits above repos, the opposite granularity, so "register this project" can read as connecting a Claude Project. |
| B. "repo" | Matches what Endless's core relies on: git worktrees, a ledger committed to git, and sharing by push/pull. No collision with Anthropic Projects, and the mapping is one Anthropic Project to many Endless repos. | Registration does not appear to check for git (`register.py`), so a non-git directory registered today would be misnamed. "Repo" is ambiguous between the logical repo and one clone of it, which is the distinction E-2217 (committed identity vs machine-local slug) is drawing. Loses the "many projects" framing in VISION.md and the talk. |
| C. Another term (e.g. "codebase", "workspace") | Avoids both Anthropic's "project" and git's "repo" ambiguity. | "Workspace" already means a multi-root folder set in VS Code/Cursor and one worktree per task in Conductor. "Codebase" makes awkward commands (`endless codebase register`). |

Recommendation: B, "repo". The core features already require git, and the clone-vs-repo ambiguity is one E-2217 has to name anyway.

### Q2. If renamed, how deep does the rename go?

| Option | Pros | Cons |
|---|---|---|
| A. User-facing only: CLI, help, guide, docs, messages | No ledger or schema change, and old and new Endless versions read each other's ledgers. | `endless sql`, ledger lines, Go code and env vars still say "project" while the user and agents see "repo". Agents reading both get two words for one thing. |
| B. Everything: CLI, schema, event fields, env vars, config keys, with old event field names mapped on replay | One term everywhere. | Replay has to map the old field names forever. A collaborator on an older Endless cannot read events written with the new field names in a shared ledger. |

Recommendation: B, done as one cutover. A user-facing-only rename leaves agents reading the ledger with two names for the same thing.

### Q3. If renamed, when?

| Option | Pros | Cons |
|---|---|---|
| A. Now, before E-2215/E-2217 land | Less code using the old name to change. | E-2215 (per-project databases) and E-2217 (project identity) are in progress and rename tables, files and identity. Renaming first means editing the same code twice, or conflicting with those worktrees. |
| B. Together with E-2215/E-2217 | The storage split already rewrites the per-project schema and identity, so the new names land once. | Ties a naming change to two large tasks, so the rename waits on them and makes their review larger. |
| C. Right after E-2215/E-2217 land | Neither task is disturbed or enlarged. | Those tasks add more uses of "project" (database file names, mirror tables, identity fields) that then get renamed. |

Recommendation: C. It keeps two in-flight tasks unchanged, and the extra renames are mechanical.

### Q4. Should Endless have a grouping above its unit (one group, many repos)?

`discover.py` already detects directories that hold two or more git repos as "groups" when it finds projects.

| Option | Pros | Cons |
|---|---|---|
| A. No grouping concept | none | A product spread over several repos (API, web, mobile), which is Anthropic's own Projects example, has no single view in Endless. |
| B. Add a grouping concept later, under a name that is neither "project" nor "repo" | Covers multi-repo products, and its name is reserved now, so the Q1 term doesn't get stretched to cover it. | A cross-ledger view needs the machine database from E-2215 to hold the group, because no single repo's ledger owns it. |
| C. Use "project" for the group, with Q1 renaming the unit | The word lines up with Anthropic's meaning (a project spans repos). | Two different products would use "project" for two different objects, which invites the expectation that an Endless project and an Anthropic Project are the same thing or can sync. They cannot: no project ID is exposed (report). |

Recommendation: B. Pick the name now and build it only when a multi-repo case asks for it.

### Q5. What should Endless lead with when it describes itself?

| Option | Pros | Cons |
|---|---|---|
| A. Human-directed, verification-gated task work that lives in git (enforced task states, plan before claim, human verifies before landing) | These are the features with no Anthropic counterpart. Anthropic's coordinator limits are "instructions Claude keeps to, not enforced settings" (report). | The first thing a newcomer meets is the gates, which the newcomer can read as overhead before seeing what they buy. |
| B. Keep the current framing: awareness across many projects and sessions for a solo developer | Matches VISION.md, the CLI help and the talk. | For github.com code that can run in the cloud, Anthropic Projects and agent view now track many parallel sessions with no setup (report). |
| C. Shared work over plain git (two people on one ledger, no server) | An Anthropic Project belongs to one user and cannot be shared (report). | Contradicts "for solo developers" in the CLI help. Multi-person use has had the least real use of any Endless feature. |
| D. Harness-neutral: one task system across Claude Code, Codex and others | Value does not depend on any one vendor's roadmap. | Not true today. `agentenv` supports only the Claude Code CLI entrypoint, and other harnesses are unsupported. |

Recommendation: A as the lead, with C as a supporting point. D only once a second harness works.

### Q6. How far should Endless build on Anthropic's surfaces?

| Option | Pros | Cons |
|---|---|---|
| A. Only shipped, documented, local mechanisms (Remote Control, the WorktreeCreate hook, auto memory settings) | Works with Endless's local-first model, with nothing hosted. | A user who mainly works inside Anthropic Projects or cloud sessions gets nothing from Endless there. |
| B. A plus an Endless claude.ai connector (remote MCP), so phone, cloud sessions and Project threads can list tasks, read plans and approve them | Reaches every surface, including approving from the phone. | Exposes the local database to the internet through a tunnel. The user must host it and secure its auth. |
| C. Build nothing for Anthropic surfaces until Anthropic exposes a Project ID or API | No work that a later Anthropic change can invalidate. | Leaves phone monitoring and push notifications, which Remote Control already offers, unused. |

Recommendation: A. B's exposure of a local database is a security decision on its own and needs its own task if it is ever wanted.

### Q7. Should Endless-spawned sessions start with Remote Control on?

E-2335 is probing whether hooks fire under Remote Control. This question is only about whether to turn it on.

| Option | Pros | Cons |
|---|---|---|
| A. On by default (`claude --remote-control "<task id>: <title>"`) | Every task session can be watched and steered from the phone, with push notifications, and no setup. | While Remote Control is connected, the transcript is stored on Anthropic's servers. On by default sends every task's transcript there without the user choosing it. |
| B. Opt-in per user (an Endless setting) | The user decides whether transcripts go to Anthropic. | Users who don't know the setting exists never get phone monitoring. |
| C. Off; leave it to Claude Code's own `remoteControlAtStartup` | none | Endless can't name the remote session after the task or record its session ID in the ledger. |

Recommendation: B, with `endless setup` offering it.

### Q8. What should Endless do with worktrees Claude Code creates on its own (agent view, `--worktree`, Desktop background sessions, local Project threads)?

| Option | Pros | Cons |
|---|---|---|
| A. Take them over: a WorktreeCreate hook creates them under `.endless/worktrees/` with a task sandbox | One worktree system per repo. Sessions Claude Code starts become Endless-managed. | The hook has no task to bind the worktree to, so Endless must either invent an untracked task or allow a worktree with no task, which goes against claim-before-write. |
| B. Refuse them in registered repos: the hook fails, with a message pointing to `endless task claim` | Every worktree has a task. | Agent view, `--worktree` and local Project threads stop working in Endless repos. |
| C. Leave them alone (Claude Code keeps `.claude/worktrees/`) | none | Two tools manage worktrees in one repo, and Endless's hooks see sessions in worktrees it doesn't know. |

Recommendation: B for now, and revisit A once Endless has a way to create a task from a session's first prompt.

### Q9. How should Endless treat Claude Code's auto memory in task sessions?

| Option | Pros | Cons |
|---|---|---|
| A. Leave it shared across all worktrees of a repo | Lessons one task learns reach the next task. | One task's notes leak into unrelated tasks, and two parallel sessions write to the same `MEMORY.md`. |
| B. Isolate it per task sandbox (`autoMemoryDirectory`) | No leakage or concurrent writes between tasks. | Whatever one task learns is lost to every other task. |
| C. Turn it off in Endless sessions (`CLAUDE_CODE_DISABLE_AUTO_MEMORY`) and rely on Endless's own lessons | One durable learning channel that Endless controls and that is committed to git. | A user who relies on auto memory outside Endless loses it inside Endless sessions. |

Recommendation: C. This repo already turns memory off in favour of `endless lesson`, and that choice would become product behavior.

## Decided without asking

- E-2335 owns hardening against hookless sessions: cloud guards, `onFailure: "block"`, Desktop support, Remote Control hook probes, and detecting commits on main that skipped landing (which also covers a Project thread's **Merge it** bypassing Endless's verify gate). E-2336 covers naming, positioning and what to build on Anthropic surfaces. Where both touch Remote Control, E-2335 decides whether it is safe and Q7 here decides whether to turn it on.
- Each answered question becomes a recorded decision, plus follow-up tasks where it calls for work. They are linked from this task's outcome.

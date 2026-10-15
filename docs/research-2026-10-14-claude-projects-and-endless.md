# Anthropic's Redesigned "Projects" and What It Means for Endless

Anthropic's redesigned Projects affects Endless less than the shared name suggests. It weakens Endless's general claim to "coordinate many parallel Claude sessions" for work hosted on GitHub that can run in the cloud. It does not touch Endless's core: an enforced task lifecycle, a human-run verification gate before merge, Git-native shared state, and local-first execution. It also validates the category.

## TL;DR

- **What shipped.** On September 17, 2026, Anthropic relaunched "Projects" as a beta inside Claude Code. A project is now one coordinator conversation that starts parallel worker "threads". Each thread is a full Claude Code session, usually in the cloud on its own branch. The project has shared memory, instructions and a file Library. You use it at claude.ai/code, in the desktop app's Code tab and in the mobile app, but **not in the terminal CLI** (shipped).
- **Overlap with Endless.** It substantially overlaps Endless on parallel isolated sessions, context handoff and live monitoring. It barely overlaps on Endless's plan gate, verification scripts and Git-ledger collaboration (a project belongs to one user and can't be shared). It exposes **no project ID** to hooks, environment variables, `claude agents --json`, the SDK or any API. Deep integration therefore isn't possible today; the practical bridge is Remote Control.
- **What to do.** Rename Endless "project" to "repo" now; this report calls Endless's unit a "repo" from here on. Then turn on Remote Control for Endless-spawned sessions so you get phone monitoring and push notifications, and use the `WorktreeCreate` hook so worktrees created by Anthropic's tools land inside Endless's sandbox model. Don't build anything that depends on reading or writing Anthropic Project state; there is no interface for it.

## 1. Executive summary: the impact on Endless's value proposition

**Verdict: Projects partially weakens Endless, mostly validates the category, and leaves its distinctive features untouched.**

- **Where Endless is at real risk.** Take a solo developer whose code is on github.com, who is happy to run work in Anthropic's cloud, and who wants to fire tasks and walk away. For that person, Projects now covers the "I can't keep track of all my sessions" problem that opens Endless's VISION.md.\[1\] It covers it with zero setup and with phone access. Endless should stop leading with "run many sessions without collisions". Anthropic, Conductor, Claude Squad and Claude Code's own agent view all do that now.\[2\]
- **The bigger local threat is agent view, not Projects.** Agent view (`claude agents`) is a research-preview, local, multi-session dashboard that moves each dispatched session into its own worktree.\[3\] That is closer to Endless's tmux-and-worktree mechanics than cloud Projects is.
- **What Endless keeps:**
  - Enforced task states, with a plan required before claim or spawn.\[4\]\[5\]
  - A human-run verification script before landing.\[5\]\[6\]
  - A per-task config/DB sandbox on your own Mac.\[5\]
  - An append-only ledger in Git that lets two people share the work by push and pull.\[1\]\[6\]
  - Independence from github.com and the Claude GitHub App.
  - A design that keeps the human in charge of decomposition rather than handing it to an autonomous coordinator.
- **The product contrast.** Anthropic's coordinator is designed to decide what becomes a thread.\[7\]\[8\] Endless is designed so the human decides and verifies. Those are different products, and VISION.md's "lowering the human's review burden" thesis is now the sharpest point of difference.\[1\]

## 2. What Projects are now

### Timeline and status

| Date | Event | Status |
|---|---|---|
| Long-standing | claude.ai chat Projects: self-contained workspaces with chat history, a knowledge base and instructions. Free users get up to five.\[9\] | Shipped (old feature)\[10\] |
| Sep 17, 2026 | Anthropic article "Projects redesigned: from folder to conversation". Beta for "select Claude Pro and Max subscribers who use cloud sessions in Claude Code and don't have any existing projects on the web or desktop".\[8\] The @ClaudeDevs post the same day says rollout is "on desktop and web". | Shipped (beta)\[11\]\[12\] |
| Sep 17, 2026 | The launch article says threads run in the cloud and that running on your machine "is coming very soon".\[8\] | Announced at the time\[8\] |
| Sep 23, 2026 | @ClaudeDevs: "We've just added local support to Projects in Claude Code, so threads can now run on your own machine." The same day: still working through the waitlist and migrating existing projects. | Shipped (beta)\[12\] |
| Oct 9, 2026 | @ClaudeDevs posted: "We just let in every Pro and Max user from the Claude Code Projects waitlist! If you're new to Claude Code Projects, here's a 4 minute walkthrough to get you started" (text from a search snippet of the X post, since X blocks direct reading; daily.dev and pasqualepillitteri.it reported the same on Oct 10). | Shipped (per official post, read via snippet) |
| Current docs (code.claude.com "Let Claude coordinate ongoing work with Projects") | Public beta on Pro and Max, "rolling out gradually". Not on Team or Enterprise yet. | Shipped (beta)\[13\] |
| Current Help Center ("What are projects?", updated mid-October 2026) | Existing chat projects for some Pro and Max users are being moved to the new version, carrying "instructions, files, full chat history, and scheduled tasks". Chat, Cowork, Team and Enterprise come later. | Partially shipped / announced\[10\] |

There is one conflict to note. On launch day The New Stack ("Anthropic's new Claude Code feature could drain your plan before lunch", Sep 17) wrote that "the beta will expand to more Claude Code users on Pro and Max plans over the coming week, with mobile support coming soon and Team and Enterprise access planned for later." The current Claude Code docs list the Claude mobile app as a place where you create and use projects. I treat mobile as **shipped** per the current docs.

### Old versus new

- **Old (chat Projects):** a folder of chats sharing uploaded knowledge (with RAG on paid plans) and instructions. It can be shared on Team and Enterprise.\[10\] It has no threads, no coordinator and no code execution.
- **New (Claude Code Projects):** "one ongoing conversation where Claude coordinates a stream of related work".
- **How they relate:** the docs describe the old version as "the earlier Projects experience, which groups conversations and reference files without threads or a coordinator".\[14\]\[15\] Old projects keep working until they are migrated.\[8\] Migration is one-way into the new model.

### Data model (shipped, per the Claude Code docs)

- **Coordinator conversation.** One long-running session. It routes each message, starts threads, and sees what threads report rather than every step they take.\[7\]\[14\]
- **Threads.** Separate sessions with their own context windows. A cloud thread "works on its own branch and opens a pull request when the work calls for one",\[8\] then watches the pull request with auto-fix. There is an enforced cap of **200 new threads per day**. "Run at most two at a time" is a preference you can state, not a hard cap.\[14\]\[15\]\[16\]
- **Standing context:**
  - Project memory: files with a `MEMORY.md` index that every cloud thread reads.\[14\]
  - Project instructions: up to 16,000 characters.
  - Repositories, plus uploaded files under `/mnt/project-files`.
  - A cloud environment covering network access, environment variables, secrets and a setup script.\[14\]
  - Plugins, and the claude.ai connectors on your account.\[14\]
- **Overview pane.** Threads are grouped as Ready for review, Waiting on you, Working, Landing, Idle and Resolved. Further tabs show the Library, Pull requests and Routines (scheduled work).\[14\]
- **Repos.** **One project can span many repositories.** Anthropic's own example connects "API, web, and mobile repos",\[2\]\[8\] and a thread can add a repo to itself mid-task. The docs don't say whether one repo can belong to several projects. Nothing in them forbids it, and repos are attached by reference to GitHub, so it is probably allowed (**unverified**).
- **Restrictions:**
  - Code must be on github.com (not GitHub Enterprise Server, GitLab or Bitbucket), with the Claude GitHub App installed.\[14\]
  - "A project belongs to one user. You can't share a project or its threads."\[14\]\[15\]
  - Threads can't move between projects.\[13\]\[14\]\[15\]\[16\]
  - "You can't add a session you started yourself on your machine to a project."\[14\]

### Where state lives, and whether it is the same "project" as in CCC

**Two different concepts share one name.**

- **CCC's "project" is a local, path-keyed directory.** Transcripts and auto memory live under `~/.claude/projects/<project>/`. Auto memory is "stored per git repository", and "all worktrees of the same repository share one auto memory directory".\[17\]\[18\] Settings and hooks come from `.claude/settings.json` scopes, and `${CLAUDE_PROJECT_DIR}` is a folder path.
- **An Anthropic Project is a cloud object.** Its memory is explicitly "separate from the auto memory Claude Code keeps on your machine, even though both use a `MEMORY.md` index".\[14\] Its settings are changed "at claude.ai/code or in the desktop app, not in `settings.json`".\[14\]
- **They overlap in only two places:**
  - Cloud threads read each repo's committed `CLAUDE.md`\[14\] and `.claude/skills|agents|commands`.
  - In a project with a single repo, cloud threads also apply that repo's `.claude/settings.json` permission rules, hooks and `env`. With several repos on Anthropic-hosted environments, those don't apply at all.\[14\]\[19\]

### Programmatic exposure (shipped answer: essentially none)

- **Hook input.** Common fields include `session_id`, `transcript_path`, `cwd`, `permission_mode` and `hook_event_name`.\[20\]\[21\] There is no project field.
- **Environment variables.** There is no `CLAUDE_PROJECT_ID`. The nearest handle is `CLAUDE_CODE_BRIDGE_SESSION_ID`, the Remote Control session ID in `session_…` form. It is set in hook and Bash subprocesses while a session has an active Remote Control connection (v2.1.199+).\[20\]\[22\] Cloud sessions expose `CLAUDE_CODE_REMOTE` and `CLAUDE_CODE_REMOTE_SESSION_ID`.\[22\]\[23\]
- **`claude agents --json`.** Fields are `cwd`, `kind`, `startedAt`, `id`, `state`, `pid`, `status`, `waitingFor`, `sessionId` and `name`.\[3\] There is no project field.
- **CLI, SDK and API.** Projects aren't available in the terminal CLI.\[14\] No Projects API or Agent SDK option is documented.
- **Community requests.** User-filed issue anthropics/claude-code #100714, "[FEATURE] Projects (beta): send messages into a Project by name or ID, with sender identity", proposes `claude project send <project> "<msg>"` and `claude project list`, where "<project> accepts the Project ID (chan_…) or a unique name". It notes that "chan_… IDs are visible in URLs, but no CLI flag or API takes one". Companion issue #100715 asks to attach a running local session to a Project as a "pseudothread". These are **requests, not features**. A separate user report, #100377 (opened Oct 8, 2026; labelled platform:macos), says "Since about 2026-10-07 21:37 UTC, every claude.ai Project thread sent to run on my Mac fails to start ('Couldn't start')", with "CCR v2 worker registration failed ... 400" while "self-started sessions on the same server register fine". Treat that as beta instability.

## 3. Surface-by-surface findings

| | **CCC (terminal)** | **CCD (desktop Code tab)** | **CCM (iOS app)** |
|---|---|---|---|
| **Projects UI** | None. You can't create or use projects. | Yes: Projects sidebar, Overview pane, desktop notifications. | Yes: create and steer projects in the Code tab.\[10\]\[14\]\[24\] |
| **Where a session runs** | Your Mac. | Local sessions on your Mac. Cloud sessions in an Anthropic VM. Project threads are cloud by default, or local via Remote Control. | **Never on the phone.** Cloud sessions and projects run in Anthropic's cloud. Remote Control and Dispatch run on your Mac.\[14\]\[24\] |
| **Local `settings.json` hooks (your Go binaries)** | Fire. | Fire for local sessions: "Claude Code fires the same hook events wherever it runs: … the Desktop app, and cloud sessions." | Fire for Remote Control sessions, because the `claude` process is local (inferred; see experiment E1). Do **not** fire for cloud sessions, which "don't read your local `~/.claude/settings.json`".\[20\] |
| **Local MCP servers** | Available. | Available in local sessions. Cloud threads get MCP only from claude.ai connectors (or a single repo's `.mcp.json`). | Available via Remote Control ("your filesystem, MCP servers, tools, and project configuration all stay available"). Unavailable in cloud sessions.\[25\] |
| **Parallel sessions, worktrees, tracking** | Agent view (research preview), `--worktree`, `--worktree --tmux`, agent teams (experimental), background sessions with `/resume`. | Parallel sessions with an optional worktree each, side chats, scheduled tasks or routines, a tasks pane for subagents. Projects on top. | Monitor and steer only. Push notifications via Remote Control.\[26\]\[27\]\[28\]\[29\] |

### Notes that matter for Endless

- **Hook events are the same everywhere.** The full event list (SessionStart, UserPromptSubmit, PreToolUse, PostToolUse, Stop, SessionEnd, WorktreeCreate and WorktreeRemove, TaskCreated and TaskCompleted, CwdChanged, and others) is documented as uniform across surfaces.\[20\] What differs is **which settings files load**, not which events exist.
- **Cloud sessions can't run your Go hooks as-is (inference).** Those sessions run on Anthropic-managed Linux VMs,\[30\] so macOS (darwin/arm64) Go hook binaries committed to a repo wouldn't execute there. You would need Linux builds plus setup-script installation.
- **CCD reads config from the main checkout in its own worktree sessions.** For worktree sessions started from the desktop app, Claude Code "reads project configuration such as settings, hooks, skills, agents, commands, and `.mcp.json` servers from the main checkout's root rather than from the worktree".\[31\] That bypasses any per-worktree config Endless writes, though only for worktrees CCD itself creates.
- **Cross-surface visibility:**
  - CCD's own session awareness "doesn't see cloud sessions, or sessions you started from the terminal CLI".\[27\]
  - The desktop app can resume a CLI session, and `/desktop` hands a terminal session to CCD.\[32\]\[33\]
  - `claude --teleport` pulls a cloud session into the terminal. The reverse isn't possible from the CLI.\[34\]
- **Will an Endless-spawned tmux session show up in CCD or CCM?** Only if Remote Control is on for it. Use `claude --remote-control "<name>"`, or set `remoteControlAtStartup: true` in user settings; each interactive process then registers one remote session.\[25\] It then appears in the claude.ai/code and CCM session lists, and push notifications work.\[25\] It will **never** appear inside an Anthropic Project, because projects can't adopt sessions you start yourself.\[14\]

## 4. Overlap analysis

| Endless capability | Closest Anthropic equivalent | Overlap | What Endless still does | Where Anthropic is better |
|---|---|---|---|---|
| Task lifecycle with a plan-approval gate | Coordinator honours "propose threads and wait for my go-ahead" and "don't open a PR until I've seen the plan". Overview states. | **Partial** | Explicit, enforced states (unplanned → submitted → ready → underway → unverified/unlanded → confirmed, etc.). Spawn refuses tasks with no plan. Re-plan drops approval.\[5\] Anthropic's limits are "instructions Claude keeps to, not enforced settings". | Zero setup. Natural-language intake from anywhere, including your phone.\[4\] |
| One session per task, isolated worktree, sandboxed config/DB | Cloud thread = own sandbox, clone and branch. Locally: agent view, CCD worktrees, `remote-control --spawn worktree`. | **Substantial** | Per-task config/DB sandbox on your Mac. Isolation for repos not on GitHub or that need local services. Anthropic's local options give worktrees only, and auto memory is shared across all worktrees of a repo. | A cloud VM per thread isolates more strongly than worktrees and keeps running with your laptop closed.\[18\]\[35\] |
| Generated handoff prompts | Coordinator briefs each thread. Instructions and memory reach every new thread. | **Substantial** | Handoffs that are deterministic, inspectable and versioned in Git, generated from the task record. | Shared memory builds up automatically across threads over days.\[8\]\[15\] |
| Verification scripts before merge | Threads run tests, open PRs, auto-fix CI and review comments. "Merge it" button. | **Partial** | A runnable verification artifact the *human* runs. A verify-before-land gate that resets when the branch moves.\[5\] | The CI and PR feedback loop is automated end to end.\[6\]\[7\] |
| Live multi-session monitor | Overview pane, agent view (`claude agents`, `--json`), desktop notifications, mobile push. | **Substantial** | Task-aware view in tmux, beside the shell. | Works from your phone. Push notifications. No tmux required.\[3\]\[24\] |
| Git-native append-only ledger, collaboration by push/pull | None. "A project belongs to one user." | **None** | All of it: no central server, and shared state rebuilds from Git. | —\[6\] |
| Local-first, tmux-driven workflow | Agent view, `claude --worktree --tmux`, Remote Control local threads. Projects are cloud-first and need github.com. | **Partial** | Fully offline-capable. Works with GitLab, Bitbucket or local-only repos, and with your local DBs and VPN. | Nothing for tmux users specifically.\[36\] |

**Assessment.** No Endless capability is fully redundant. Isolation, handoff and monitoring are now **table stakes**, available free from Anthropic. If Endless keeps them as headline features, it competes with Anthropic's own roadmap and loses. The defensible position is a **verification-gated, human-directed task system that lives in Git and drives Claude Code wherever it runs**. That framing turns Projects from a competitor into one more surface Endless can tolerate or bridge.

## 5. Naming recommendation: rename "project" to "repo"

**Recommendation: yes, rename to "repo", and do it now while Endless is pre-1.0.**

- **Confusion risk is high and growing.**
  - "Project" now means three different things in the Claude world: the old chat Project, the new multi-repo cloud Project, and CCC's path-keyed `~/.claude/projects/` plus `CLAUDE_PROJECT_DIR`.\[14\]\[37\]
  - Endless users are exactly the people who will see all three.
  - An Anthropic Project sits *above* repos (one project, many repos), so it is the opposite granularity from an Endless project.\[2\] "Register this project" will read as "connect to my Claude Project".
- **"Repo" fits Endless's mechanics.**
  - Spawn depends on `git worktree`, the ledger is committed to Git, and collaboration is push/pull.\[4\]\[5\]\[6\] A non-Git directory therefore can't use Endless's core features, so "repo" is accurate rather than limiting.
  - A monorepo is still one repo.
  - Multi-repo products are the one real gap. They are better served later by an explicit grouping concept above repos than by stretching "project".
- **Alternatives rejected:**
  - "Workspace" collides badly. Conductor uses "workspace" for one isolated worktree per task, and Cursor uses "multi-root workspace" for a set of folders.\[38\]\[39\]
  - "Checkout" suggests a single working tree, which is wrong once Endless manages many worktrees per repo.
  - "Codebase" is vague and hard to use as a verb noun (`endless codebase register`).
- **What comparable tools call this unit:**
  - Conductor confusingly uses "Project: the Conductor entry for a codebase", with "1 repository contains many workspaces".\[38\]
  - Claude Squad calls its units "instances" and "sessions".\[40\]
  - Cursor 3.2 uses worktrees plus multi-root workspaces.\[41\]
  - Claude Code itself keys memory "per git repository".\[18\]
  - The field is converging on *repo → worktree/workspace → session*.
- **Mapping to Anthropic.** With "repo", Endless maps cleanly: **one Anthropic Project → many Endless repos**. If you later add a grouping concept, give it a name that avoids "project" (for example "product" or "group") so the mapping stays readable.

**Implications of the rename:**

- **CLI.** `endless repo register|list|show`. Keep `endless project …` as a hidden, deprecated alias for one or two releases, with a one-line warning.
- **Environment and config.** Rename any `ENDLESS_PROJECT*` variables and config keys, accepting the old names with a warning.
- **DB schema.** Rename the table to `repos` and `project_id` to `repo_id`. SQLite is a rebuildable projection, so regenerate it rather than migrating it.\[1\]\[6\]
- **Ledger.** Don't rewrite history.\[1\] Add an upcaster that maps old `project*` event fields to `repo*` on replay. ROADMAP.md already lists event upcasting as "in progress", and this is an ideal first real use.\[6\]
- **Docs.** Update README "Getting started", VISION.md "Sharing a Project", ROADMAP.md "Project view", `docs/guide` and the generated Claude-facing guide text. Then grep the Go hooks and MCP tool names and descriptions, since Claude reads those.
- **Cutover.** Make it one commit with a CHANGELOG note, plus a `just lifecycle-check`-style lint that fails on new user-facing uses of "project".

## 6. Leverage opportunities, ranked

| # | Opportunity | Value | Effort | Depends on |
|---|---|---|---|---|
| 1 | **Remote Control for Endless-spawned sessions.** `task spawn` launches `claude --remote-control "E-123: <title>"`. A SessionStart hook records `CLAUDE_CODE_BRIDGE_SESSION_ID` (and its claude.ai/code URL) in the ledger.\[22\] The result is phone monitoring, steering and push notifications ("Push when actions required"). | High | Low | Shipped\[25\] |
| 2 | **`WorktreeCreate` hook in Go.** The hook "replaces default git behavior" for `--worktree`, background sessions and `remote-control --spawn worktree`.\[3\]\[20\] Endless's hook can create the worktree under `.endless/worktrees/` with a task sandbox, so worktrees from agent view, CCD background sessions and local project threads all become Endless-managed. | High | Medium | Shipped (local project threads are beta)\[20\] |
| 3 | **Isolate per-task auto memory.** Set `autoMemoryDirectory`, or `CLAUDE_CODE_DISABLE_AUTO_MEMORY`, per sandbox.\[22\] By default all worktrees of a repo share one memory directory, which leaks context between tasks. | Medium | Low | Shipped\[18\]\[42\] |
| 4 | **Make repo guidance aware of the surface it runs on.** Commit an Endless skill under `.claude/skills/`. Add a SessionStart hook that checks `CLAUDE_CODE_REMOTE`, so cloud threads that clone your repo aren't told to run `endless task claim` (which they can't).\[22\] | Medium | Low | Shipped |
| 5 | **Endless as a claude.ai connector** (remote MCP over a tunnel). Lets CCM chats, cloud sessions and project threads list tasks, read plans and approve them from the phone. | High | High (hosting, auth, security of a local DB exposed to the internet) | Shipped mechanism; your infrastructure |
| 6 | **Linux builds of the Go hooks plus a cloud-environment setup script**, so a single-repo project's cloud threads enforce Endless rules. | Medium | Medium | Shipped (single-repo projects only)\[23\] |
| 7 | **Read or write Anthropic Project instructions, memory or files; group the ledger by Project ID.** | — | Not possible | No API and no ID exposed (unverified future) |

**Risks and conflicts:**

- **Merging that bypasses verification.** Project threads auto-fix CI and offer **Merge it**.\[7\]\[14\] If a user points a project at an Endless repo, work can land without Endless's verify gate. Branch protection on GitHub is the only hard stop.
- **Worktree locations collide.** Claude Code uses `.claude/worktrees/` while Endless uses `.endless/worktrees/`.\[3\]\[4\] Opportunity #2 resolves this. Without it, two tools manage worktrees in one repo.
- **CCD worktree sessions read the main checkout's config**, not the worktree's, so Endless's per-worktree overrides won't apply there.\[31\]
- **Shared auto memory** across worktrees (see #3).\[18\]
- **Anthropic doesn't write `CLAUDE.md`, but Claude does.** The docs don't say Anthropic writes into `CLAUDE.md`. Project memory lives in the cloud, and local auto memory goes to `~/.claude/projects/`. But Claude may still edit a repo's `CLAUDE.md` if asked, and project instructions tell threads to put repo rules there.\[14\] Treat `CLAUDE.md` as shared territory and keep Endless's generated text inside delimited blocks.

## 7. Open questions and experiments to run on the M4 Mac Mini

The documentation is silent on the following. Don't assume answers; run these.

- **E1. Do local hooks fire for sessions started from CCM?**
  1. In an Endless repo, add a Go hook on every event that appends `event`, `session_id`, `cwd` and the full environment (filtered to names starting `CLAUDE`) to `/tmp/endless-hooks.log`.
  2. Run `claude remote-control --spawn worktree`.
  3. From the iPhone Code tab, start a *new* session in that server and send a prompt that edits a file.
  4. Confirm that SessionStart, PreToolUse and PostToolUse fired, that `CLAUDE_CODE_BRIDGE_SESSION_ID` is set, and which worktree path `cwd` reports.
- **E2. What does a project "Work locally" thread look like on disk?**
  1. Connect the same folder to a project via **Settings > Claude Code** in CCD.\[43\]
  2. Ask the coordinator for a local thread with **Worktree** on.
  3. Diff the hook log for any project-identifying field or variable. The docs list none.
  4. Check whether a transcript appears under `~/.claude/projects/<encoded-path>/` and whether `WorktreeCreate` fired.
- **E3. Does an Endless tmux session appear in CCD and CCM?**
  1. Set `remoteControlAtStartup: true` in `~/.claude/settings.json` and run `endless task spawn`.
  2. Check the claude.ai/code list, the CCM list, the CCD sidebar and `claude agents --json`.
  3. Repeat with the setting off.
- **E4. CCD on an Endless worktree.** Open `.endless/worktrees/e-<id>` as a local CCD session (not a CCD-created worktree). Check whether hooks and settings come from the worktree or the main checkout.
- **E5. Cloud-thread behaviour on an Endless repo.**
  1. Create a single-repo project on a throwaway GitHub repo containing Endless's `.claude/settings.json` hooks.
  2. Confirm that the darwin binaries fail in the cloud thread, and see how it reports the failure.
  3. Confirm whether `CLAUDE_CODE_REMOTE=true` is present.
- **E6. MCP reachability from the phone.** In a Remote Control session driven from CCM, send `/mcp`. The docs say this returns a text status summary on mobile.\[25\] Confirm the Endless MCP server is listed and connected.
- **Open questions for Anthropic (file as issues):**
  - A project ID in hook input and environment for local threads.
  - Whether one repo can belong to several projects.
  - Whether local project threads will ever load project memory (they currently start "with the project's instructions, but not with its memory files loaded").\[14\]

## Caveats

- Projects is a **beta** that changes weekly. The local-thread path launched six days after the main launch, and the Help Center article was updated in mid-October 2026.\[10\]\[12\] Re-check the Claude Code docs before building on any detail here.
- Several dated claims come from third-party write-ups quoting @ClaudeDevs, because X blocks automated reading. These are the October 9 waitlist expansion and the GitHub issues. Treat them as unverified.
- I couldn't read the full Claude Code changelog for Projects-specific entries. The page is too large to fetch whole; only versions 2.1.290–2.1.300 were checked, and they contain no Projects entries.\[44\]
- Some statements in this report are my inferences rather than documented behaviour, and are labelled as such: cloud Go-binary incompatibility, hooks firing in phone-initiated Remote Control sessions, and repo membership in multiple projects.

## Sources

1. <https://raw.githubusercontent.com/mikeschinkel/endless/main/VISION.md>
2. [Anthropic's new Claude Code feature could drain your plan before lunch](https://thenewstack.io/claude-code-parallel-projects/)
3. [Manage multiple agents with agent view - Claude Code Docs](https://code.claude.com/docs/en/agent-view)
4. <https://raw.githubusercontent.com/mikeschinkel/endless/main/docs/guide/index.md>
5. <https://raw.githubusercontent.com/mikeschinkel/endless/main/README.md>
6. <https://raw.githubusercontent.com/mikeschinkel/endless/main/ROADMAP.md>
7. [Anthropic Launches Claude Code Projects in Beta: Parallel Cloud Sessions That Keep Running After You Close Your Laptop - MarkTechPost](https://www.marktechpost.com/2026/09/17/anthropic-launches-claude-code-projects-in-beta-parallel-cloud-sessions-that-keep-running-after-you-close-your-laptop/)
8. [Projects redesigned: from folder to conversation](https://claude.com/resources/articles/projects-redesigned)
9. [Claude Features 2026: Projects, Artifacts, Memory, Computer Use, Skills, MCP - Suprmind](https://suprmind.ai/hub/claude/features/)
10. [What are projects? | Claude Help Center](https://support.claude.com/en/articles/9517075-what-are-projects)
11. [Claude Code Projects: One Chat Directs Parallel Threads](https://cellcog.ai/blog/claude-code-projects-redesign/)
12. [ClaudeDevs on X: "We've just added local support to Projects in Claude Code, so threads can now run on your own machine." / X](https://x.com/ClaudeDevs/status/2102893178273874102)
13. [Claude Code Projects: what they are and who can use them — Munder Difflin Blog](https://munderdiffl.in/blog/claude-code-projects/)
14. [Let Claude coordinate ongoing work with Projects - Claude Code Docs](https://code.claude.com/docs/en/claude-projects)
15. [How to Use Claude Code Projects](https://omidsaffari.com/blog/how-to-use-claude-code-projects-beta-2026)
16. [\[Illustrated\] What is Claude Code Projects? How to use the new feature that lets you delegate tasks in parallel within a single conversation, and 7 points to note｜今日から使えるAI実践帳](https://note.com/ai_hack_dx/n/n2b731aefef44?hl=en)
17. [How Claude remembers your project - Claude Code Docs](https://code.claude.com/docs/en/memory)
18. [Glossary - Claude Code Docs](https://code.claude.com/docs/en/glossary)
19. [\[DOCS\] Projects beta: what do local threads (Remote Control) load, and do they use worktrees? · Issue #97565 · anthropics/claude-code · GitHub](https://github.com/anthropics/claude-code/issues/97565)
20. [Hooks reference - Claude Code Docs](https://code.claude.com/docs/en/hooks)
21. [Intercept and control agent behavior with hooks - Claude Code Docs](https://code.claude.com/docs/en/agent-sdk/hooks)
22. <https://code.claude.com/docs/en/env-vars>
23. [Configure cloud environments - Claude Code Docs](https://code.claude.com/docs/en/cloud-environments)
24. [Claude Code on mobile - Claude Code Docs](https://code.claude.com/docs/en/mobile)
25. <https://code.claude.com/docs/en/remote-control>
26. [Run agents in parallel - Claude Code Docs](https://code.claude.com/docs/en/agents)
27. [Desktop application - Claude Code Docs](https://code.claude.com/docs/en/desktop)
28. [Get started with the desktop app - Claude Code Docs](https://code.claude.com/docs/en/desktop-quickstart)
29. [tmux + Claude Code: Sessions, send-keys, and the Ceiling](https://crystl.dev/guides/tmux-for-ai-agents/)
30. [How Claude Code works - Claude Code Docs](https://code.claude.com/docs/en/how-claude-code-works)
31. [Run parallel sessions with worktrees - Claude Code Docs](https://code.claude.com/docs/en/worktrees)
32. [Manage sessions - Claude Code Docs](https://code.claude.com/docs/en/sessions)
33. [Overview - Claude Code Docs](https://code.claude.com/docs/en/overview)
34. [Use Claude Code in the cloud - Claude Code Docs](https://code.claude.com/docs/en/claude-code-on-the-web?amp=&amp=)
35. [Anthropic Rebuilds Claude Code Projects For Parallel Agents](https://www.timesofai.com/news/anthropic-claude-code-projects-multi-agent/)
36. [Claude Code Projects: Who Can Use It and What It Costs](https://arte.itlibra.com/en/articles/claude-code-projects-guide)
37. [Explore the .claude directory - Claude Code Docs](https://code.claude.com/docs/en/claude-directory)
38. [Isolated workspaces](https://www.conductor.build/docs/concepts/workspaces-and-branches)
39. [Cursor Multi-Root Workspaces for Cross-Repo Agent Edits — AgentPatterns.ai](https://agentpatterns.ai/tools/cursor/multi-root-workspaces/)
40. [GitHub - smtg-ai/claude-squad: Manage multiple AI terminal agents like Claude Code, Codex, OpenCode, and Amp. · GitHub](https://github.com/smtg-ai/claude-squad)
41. [Cursor 3.2 (April 2026): /multitask Async Subagents, Better Worktrees, Multi-Root Workspaces](https://authorityaitools.com/blog/cursor-3-2-april-2026-update)
42. [All settings - Claude Code Docs](https://code.claude.com/docs/en/settings-reference)
43. [Claude Code Projects: Parallel Cloud Sessions, Shared Memory, and Project Limits](https://aicatchup.com/news/claude-code-projects-parallel-cloud-sessions)
44. <https://code.claude.com/docs/en/changelog>

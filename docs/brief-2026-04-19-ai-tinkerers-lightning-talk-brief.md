# Claude Code Handoff: AI Tinkerers Atlanta Talk (Tue 2026-04-21)

This document is context for Claude Code to help prepare the lightning talk and companion article. It supersedes prior briefs.

## The ask

Help me prepare two artifacts for an AI Tinkerers Atlanta lightning talk on **Tuesday, April 21, 2026**:

1. A **5-minute live demo** using Endless, showing the working project and exposing enough build internals to satisfy Tinkerers norms.
2. A short companion **GitHub Pages article** at `mikeschinkel.github.io/endless/ai-tinkerers-2026-04/` covering three "pointers" (things I would have shown if I had more time) that audience members can actually pick up and use.

Both artifacts need to be ready by end of day Monday, April 20. Non-negotiable deadline.

## Venue constraints (AI Tinkerers)

The organizers publish explicit demo norms. I have to stay inside them. Direct quotes:

- "These are not pitches. Skip the market overview and jump right to the implementation."
- "Show, don't tell. Prioritize showing your project in action over static slides."
- "Expose the guts of your build."
- "No videos. Show the thing running."
- "Highlighting a particular technical challenge and your solution will make your demo more focused."
- "Half-baked projects and fragile demos are welcome."
- "Focus entirely on answering the implicit question, 'how can I enable a fellow tinkerer with what I'm showing today?'"
- Font sizes: at least 24pt on anything projected.

Audience: senior builders who build with AI as their day job. They already know Beads, Mem0, Letta, and similar memory/planning tools by name and architecture. Do not over-explain the category.

## How the talk is being advertised

> Juggle 10+ projects in parallel without getting overwhelmed. Building a dashboard to monitor and manage multiple parallel AI-driven developer workflows.

This framing is the spine. Do not drift from it. The audience signed up for this; the talk has to deliver on it.

## Talk structure (agreed)

| Time | Segment |
|------|---------|
| 0:00-0:20 | Dashboard on screen. One sentence: "I was drowning in 10+ parallel projects. This is how I'm not anymore." |
| 0:20-0:35 | Thesis sentence: "Everything I'm about to show is about keeping a human actively in the loop with Claude Code, not handing it a plan and walking away." |
| 0:35-1:45 | Dashboard + tmux in action. Real Todos across real projects. Switching between tmux sessions. ~70 seconds. |
| 1:45-3:15 | Demo beat #1. Tmux hooks for Claude Code window alerts. ~90 seconds. |
| 3:15-4:15 | Demo beat #2. Chosen in the moment from two prepared options. ~60 seconds. |
| 4:15-4:30 | URL on screen plus "three things I'd have shown with more time." Audience captures URL. |
| 4:30-5:00 | Three takeaways. This is the close. |

### Three takeaways (closing, verbatim)

1. Treat architecture design as the asset you are building, not the code.
2. Aggressively simplify; resist Claude's urge to over-engineer.
3. Use dogfooding to build your solution.

Deliver flat. No justifying. Trust the audience to fill in the why.

### Demo beat #1 (confirmed): Tmux hooks for Claude Code window alerts

Three hooks. Notification fires when Claude is idle or needs permission, PreToolUse fires when Claude starts working again, UserPromptSubmit fires the instant I hit enter. The `claude-tmux-alert` script reads `$TMUX_PANE`, resolves it to a window ID, and toggles `@window_color` plus a `*` prefix. Replaced a polling `monitor-claude` script that grepped pane content every 2 seconds. That approach was fragile and had false positives.

Show: the hook config, the script, a live firing (window turns red when a background Claude session goes idle), and name what it replaced.

### Demo beats #2 and #3 (prepare both, pick one in the moment)

**Option A: Rabbit-hole capture.** In a Claude Code session, surface a rabbit hole, fire an Endless command to record it as a child Todo, show it appearing in the dashboard, return to main task. Most directly illustrates the thesis. Risk: needs to demo reliably in 60 seconds.

**Option B: Listing recent Todos with narration.** Show the `endless` CLI listing recent Todos by ID (E-711 through E-724 style output), narrate a couple that became real features. Illustrates dogfooding. Lower-risk fallback if A is flaky the day of.

Prepare both. Show whichever feels more reliable the morning of. Plan for A; fall back to B if needed.

## Companion article

**URL:** `mikeschinkel.github.io/endless/ai-tinkerers-2026-04/`

**Format:** Gist-flavored. Not long-lived reference material. Readable in under 5 minutes. Three topics, not more.

**Purpose:** Give audience members who capture the URL something concrete to pick up and use tomorrow. "How-to" content, distinct from the spoken takeaways (which are principles, not techniques).

**Topic selection: Claude Code decides.** Pick the three most useful "how-to" topics for this audience based on actual code state. Don't pick based on what's most novel to me; pick based on what an AI Tinkerers audience member can most plausibly implement in their own setup within a week. Candidates, in no particular order:

- **Claude Channels MCP** for inter-session communication between Claude Code sessions.
- **`~/.claude/logs/hook.log` dogfooding loop.** The Go code here is generic. An audience member could compile it and use it against their own setup.
- **Now/next/later status with deliberately no further classification.** Organizational pattern, not code per se, but a concrete design decision to document.
- **The tmux hooks setup itself**, written up in more depth than the talk allows.
- **The uniform Todo abstraction** (Plans, ad-hoc items, and anything pending are all Todos).
- **Single-record plan storage instead of parsed snippets.** The Stable-IDs dead-end and why abandoning it worked.

Look at the actual state of each before deciding. Some may be more mature than others; some may have cleaner transferable content. Pick the three with the best "10 minutes after the talk, someone can do something with this" payoff.

For each of the three, the article should have:
- A one-sentence description of what it is.
- The specific code, config, or command that matters (large enough to screenshot).
- A note on what it replaced or why it exists.
- Any files or repo paths to look at in the Endless repo.

Keep the whole article under 1500 words. Tinkerer audiences skim; tight beats thorough.

## What I need from Claude Code

1. **Confirm the demo beat #1 script is in demo-ready shape.** Font sizes, script reliability, tmux config currently on the machine. If anything is fragile, flag it now, not Monday night.
2. **Build a reliable demo path for option A (rabbit-hole capture) AND option B (recent Todos list).** Scripted sequences I can run. Known-good starting state, known-good commands, known-good ending state.
3. **Pick the three article topics.** Justify briefly per the criteria above.
4. **Draft the article.** GitHub Pages markdown, targeting the URL above.
5. **Flag anything in the talk structure that the current codebase can't actually support.** I'd rather find out now than at 2 AM Tuesday.

## Context Claude Code should already have

Everything in the Endless repo, including `docs/`, schema, CLI, dashboard, hooks, Channels integration, and hook.log setup. This handoff doesn't duplicate that. If something in this document contradicts what's in the repo, the repo wins. Tell me.

## Machine setup and projection (flagged for Mike's prep, not Claude Code's to-do)

I'll be presenting from a macbook, copying files from the macmini where development happens. Two risks to surface early:

1. **Do a full dry run of the 5 minutes on the macbook itself, not just verify files exist.** Things that can drift between machines and break a demo: tmux config (`.tmux.conf`), shell environment, Go binary paths and compilation, SQLite DB file location, MCP server configuration, Claude Code settings. Monday night is too late to discover that the dashboard is pointed at a DB path that doesn't exist on the new machine. Claude Code: if you notice any machine-specific paths or configs while preparing the demos, call them out so I can verify them on the macbook before Monday.

2. **Projection legibility is its own constraint.** AI Tinkerers requires 24pt minimum. Terminal font, tmux window names, dashboard UI text, and any code shown all need to be readable from the back of the room through an unfamiliar projector or TV. That is not the same bar as "readable on my laptop screen." Claude Code: when preparing demo scripts and any code that will appear on screen, assume aggressively larger fonts than feel normal on the laptop, and flag any UI text in the dashboard that may be too small to read at a distance.

## Working preferences (Mike)

- When a request has ambiguity where the answer materially changes the output, ask rather than assume. Don't silently pick a branch.
- Clear Path style for Go. No em-dashes in prose.
- Don't jump into fixing or building things without confirming. Propose, let me confirm, then do.

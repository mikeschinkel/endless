# Discussion: Agent Feedback Feature vs. Notes System

## Context

While dogfooding Endless across projects, agents naturally discover friction, missing features, and improvement ideas. Currently this requires the user to manually relay feedback between sessions — exactly the kind of cross-session coordination burden Endless exists to eliminate.

This raised a deeper question: what is the notes system actually for, and does a dedicated feedback feature better serve the use case of agents reporting back about Endless?

## Plan Items to Create

### 1. Discussion: Notes vs. Feedback — clarify purpose of each

The notes system exists but its purpose is fuzzy. Feedback from agents has an obvious value proposition. Questions to resolve:

- What are notes FOR? (Document staleness alerts? User reminders? General annotations?)
- Is "agent feedback about a tool" a fundamentally different thing from a "project note"?
- Should notes be repurposed as the feedback mechanism, or are they separate concerns?
- Could notes be eliminated entirely if feedback + plan items cover the use cases?

### 2. Agent feedback feature (local, CLI-driven)

A command like `endless feedback "message"` that any session on any project can run to submit a suggestion/bug/friction report back to Endless. Stored in the Endless DB, surfaced in the dashboard. The agent doesn't need to know the internals — just run the command.

Open questions:
- Where does feedback land? A `feedback` table? Notes on the endless project? Auto-created plan items?
- Should feedback be tied to the session/project it came from?
- How is feedback triaged? (Dashboard widget? CLI command? Auto-categorized?)
- Hook-based capture as a later enhancement, not primary mechanism

### 3. Remote feedback API (separate, future)

For multi-user scenarios: agents on other users' machines could POST feedback to a central API (with user approval). This is architecturally different from local SQLite — it's an API endpoint, likely on a hosted service. Separate plan from the local feedback feature.

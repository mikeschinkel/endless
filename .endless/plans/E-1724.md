Claude Web "Research" prompt for E-1724 — paste as-is; the returned cited report is the task's raw input (goes in the outcome, then mapped to how Endless can make MEMORY.md workable).

---

I need research on how to make **Claude Code's `MEMORY.md` memory feature** workable as a project's memory grows large. Everything you find must apply concretely to *this* feature — not "AI agent memory" in the abstract, and not vector-DB or hosted-service memory products except insofar as a technique can be applied to Claude Code's file-based MEMORY.md.

**How Claude Code's memory feature works (the exact system to reason about):**
- Claude Code keeps a persistent, file-based memory per project. Each memory is a separate Markdown file holding a single fact, with YAML frontmatter: `name` (a kebab-case slug), `description` (a one-line summary used to decide relevance during recall), and `metadata.type` (one of: user, feedback, project, reference). Memories cross-link with `[[name]]` wiki-links.
- A special **`MEMORY.md` index file** lists every memory as a one-line pointer, e.g. `- [Title](file.md) — short hook`. This index is loaded into the model's context at the **start of every session**.
- Because the entire index loads every session, it must stay under a hard read-size limit (observed ~24 KB). Once it exceeds that limit, **only part of it loads** — every memory listed below the cutoff becomes invisible to that session. This is the core problem: a project has accumulated ~140 memories and the index now exceeds the limit and loads only partially.
- The individual memory (topic) files are NOT all loaded up front; they are read on demand, and relevant ones are surfaced mid-session via system-reminder injections chosen from each memory's `description`.
- Memories are append-mostly and long-lived; silently dropping older lessons is unacceptable.

**Research question:** how do we keep Claude Code's MEMORY.md index navigable and under its read-size budget as the number of memories grows into the hundreds, **without losing any lessons**?

Survey the relevant techniques and, for EACH, explain specifically how it would apply to Claude Code's MEMORY.md feature as described above (its Markdown index + per-fact files + description-based recall):
1. **Progressive disclosure** — a compact always-loaded index plus on-demand loading of detail files. How to structure the MEMORY.md index so it stays small while detail stays retrievable.
2. **Hierarchical / tiered memory** (e.g. MemGPT / Letta) — could MEMORY.md be split into hot vs. archival tiers, or grouped/paged, and what would trigger promotion/eviction?
3. **Summarization / compaction** — condensing or merging older or redundant memory entries and their index lines; how to bound information loss.
4. **Retrieval-based recall** — leaning harder on the description-driven system-reminder recall (semantic retrieval) so the always-loaded index can shrink; trade-offs vs. an explicit index.
5. **Archival tiers** — moving rarely-relevant memories out of the loaded index while keeping them recoverable and re-surfaceable.
6. **Index size budgeting and generation** — auto-generating the MEMORY.md index from the memory files under a hard size budget (shorter index lines, grouping, categorization by `type`).

Include any public documentation, guidance, or community practice specific to **Claude Code's memory / MEMORY.md feature** if it exists. Cite sources throughout.

Conclude with a **comparison table** across the approaches and a **recommended approach (or combination)** for making Claude Code's MEMORY.md feature workable at scale — favoring approaches that (a) keep the always-loaded index under the read-size budget, (b) never silently lose older memories, and (c) could be automated by an external tool that generates and maintains the MEMORY.md index and the underlying memory files.

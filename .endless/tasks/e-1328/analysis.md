Suggestions from recording session_statuses.id=11:

CONTENT THAT WENT INTO <notes> BUT FELT TYPABLE:

1. Lessons-learned / recurring-mistakes-corrected (6 bullets in session 11 notes). NOT decisions (which capture design choices) and NOT memory entries (which capture the abstract rule, not the incident). Suggest <lessons><lesson>...</lesson></lessons>: per-session list of corrections received that became memory updates or sharper applications of existing memories. Schema could mirror <decisions> structure. Useful for "what patterns is this session/agent struggling with" queries across sessions.

2. Verification gaps (this session: E-1242 sibling-pane bind not E2E-tested; hook bash-detection did not fire). Distinct from <task status="verify"> which means "work done, awaiting human signoff". Verification gaps are KNOWN limitations of completed work where the agent could not independently confirm an end-to-end path. Suggest <verification-gaps><gap task="E-1242">...</gap></verification-gaps>. Helps the human prioritize what to smoke-test post-land.

3. Activate-actions / operator handoffs (this session: "just install from main required to activate everything globally"). User-action prerequisites for the work to take effect. Not a task (circular — the task is done from the agent side), not a decision, not a memory. Suggest <activate-actions><action>...</action></activate-actions>.

4. Cross-session notes / outstanding bindings (this session: "session 364 still has E-1268 bound"). Coordination state across sessions that the next session reader should be aware of. Could be <cross-session-notes> or merged into activate-actions.

5. Pre-existing-issues observed (this session: test_verb_gate_auto_registers_when_haiku_says_yes failing before this work). Useful to surface so the human knows what was noticed-but-not-fixed, without conflating with deliverables. Could be <observations>.

ARCHITECTURAL OBSERVATIONS ON <summary>:

The <summary><layer> schema is described as "per-layer implementation breakdown" which fit cleanly for genuine architectural layers (CLI verbs, event executor, hook). It also accepted entries like "String hygiene (cross-cutting)" and "Tests" which are not really layers but cross-cutting concerns / supporting work. Two options: (a) broaden the docstring to "work breakdown by aspect"; or (b) add a <work-items> or <changes> typed slot for non-layer breakdowns so <layer> stays purely architectural.

RECORDING-FRIENDLY CHANGES THAT WOULD HELP:

- Allow <task> to carry a <reason> child or "why" attribute (textual content is the task title; nuance about why this session touched it goes elsewhere).
- Allow <memory><entry path="..." op="created|modified|deleted"> — op is inferred from the description today.
- Allow <decision id="..."> for stable cross-row referencing.

WHAT WORKED WELL (do not change):

- XML over markdown — deterministic parsing as the doc promised
- <tasks> with status + filed attribute — disposition + ownership cleanly captured
- Dedup-on-identical-row means re-running same XML is safe; useful for idempotent end-of-turn calls
- Markdown echo back to chat — exactly right ergonomics, no double-writing

PRIORITY: low-to-medium. The current schema does not BLOCK recording — everything fit, just sub-optimally. Worth revisiting after a few more sessions surface the same overloading.
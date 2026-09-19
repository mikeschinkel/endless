# E-1835 — Decide what to publish in ROADMAP.md

Brainstorm outcome. This decides the content and structure of the published
contributor docs and delivers the **ready-to-publish ROADMAP.md text** below.
Publishing is deliberately deferred — Mike reviews this outcome first, then the
file gets committed (remaining work under the E-1834 epic). Nothing is committed
by this task.

---

## What was decided

**1. Split into three contributor-facing docs with distinct jobs.**
The E-1833 draft tried to be one document and ended up reading as a *vision*, not
a roadmap. Separating the jobs:

- **README.md** — tight "what Endless is" intro (2-3 sentences) that links out.
  Do not inline roadmap/vision/tenet content — it bulks the README and makes it
  hard for a human to consume.
- **ROADMAP.md** — spare, keyword-heavy, easy to reason about. An
  **exists-today / planned** table, not prose. Its job is to let a contributor
  see the delta between what's built and what's intended in fifteen seconds.
- **VISION.md** — the narrative: *"what we envision Endless will be."* The "why"
  and the arc live here, not in the roadmap.

*(No decision record was filed — Mike judged this documentation-structure choice
non-architectural, and likely to grow past "three docs" later, so the rationale
lives here in the outcome rather than as an ADR.)*

**2. Audience = contributors.** Written for one developer who wants to use Endless,
suggest improvements, and ideally send PRs — not a marketing/vision page and not a
global public release. "Contributors" (not "Collaboration"/"Collaborators") is the
idiomatic word for that reader. Lack of polish is acceptable; ease of reasoning is
not optional.

**3. Intentions, not commitments.** No dates, no promises. Deliberately vague where
the future is genuinely open (e.g. surfaces beyond tmux, pluggable backends) rather
than locking in unmade decisions.

**4. The roadmap is a two-column table, not a bucketed bullet list.** This was the
key structural move. The earlier now/next/later framing conflated *what already
exists* with *what's aspirational* (and phase now-vs-next isn't reliably
maintained, so it carried no real signal). An Exists-today / Planned table is
honest about the delta and self-evidently easy to scan. Mid-flight items are
tagged **(in progress)** in the Planned column so the exists / in-progress /
planned states all survive without a third column.

**5. Task IDs are stripped entirely.** A published doc must not cite internal
`E-`/`ED-` IDs. (Separately noted, out of scope: published plans in `.endless/plans`
still reference IDs — a known limitation gated on future globally-unique task IDs,
too large to address until then.)

**6. Two items dropped from BOTH docs** — too unclear to commit to:
- *Documents as first-class* — desired, but not enough clarity yet on what the
  implementation looks like.
- *Richer session awareness beyond what exists* — some already in place; what
  "more" looks like is too unclear.

**7. "Lower human review burden" moves to VISION.md with headline billing.** Mike
framed it as a core Endless philosophy that "desires higher billing," so it belongs
prominently in the vision narrative rather than as one row among many in the table.

**8. VISION.md positioning (this became clear while authoring E-1842).** The
brainstorm handed VISION.md a "single-developer → multi-developer substrate" arc.
Authoring made the sharper framing plain: Endless' headline value is amplifying
*one person's* concurrent-task throughput — one person directing tens to hundreds of
concurrent AI-agent sessions, each on its own bespoke task, without becoming
overwhelmed (tagline **"Manage 50+ AI tasks without becoming overwhelmed"**, which
also replaces the working "What we envision Endless will be"). Multiple developers on
one project is *table stakes*, not the pitch, so it survives in VISION.md as a
supporting "Sharing a Project" section rather than as the arc's climax. This
refines, not contradicts, the brainstorm: the brainstorm existed precisely because
the framing wasn't yet clear.

---

## Ready-to-publish ROADMAP.md

The two intro paragraphs are Mike's verbatim wording. The only change from his
text is the spelling fix `commiments` → `commitments`; his phrasing is otherwise
untouched (including "our vision" in the intro, which he confirmed reads fine in
this context). Rendered inline (not fenced) so the table displays as a table.

<br>

# Roadmap for Contributors

*The following outlines our vision for Endless for those who want to use, suggest improvements for, and ideally contribute PRs to advance Endless.*

*Specifically, this roadmap does not provide commitments or dates. It instead simply expresses our current intent for Endless' future direction.*

| Area | Exists today | Planned |
|---|---|---|
| **Ledger** | JSONL write-ahead log as source of record; SQLite as a rebuildable projection | Harden it for real-world multi-developer collaboration |
| **Collaboration** | Single-developer | Many developers on one project via git push/pull, no central server; collision-free task IDs |
| **Runtime** | Python + Go hybrid | Port all Python to Go → one copy-and-run binary; Windows support |
| **Tasks** | Epic / brainstorm / research / bugfix task types; per-task worktrees + sandboxes; `spawn` / `land`; agent work scored for complexity & risk, gated on human sign-off | Background tasks that kick off unattended sessions, promotable to foreground |
| **Verification** | Verify-by-convention scripts run at land time | `verify.toml` spec + tooling *(in progress)*; standardize the spec; adversarial-agent verification |
| **Schema** | — | Event upcasting: replay history into a new schema without rewriting the ledger *(in progress)* |
| **Views / surfaces** | CLI; session view (`session monitor`) | Project view; web console; TUI with task-tree view + terminal multiplexer; then GUI likewise |
| **Multiplexer** | tmux | Pluggable multiplexers beyond tmux (incl. Endless-as-multiplexer) |
| **Integrations** | — | Pluggable task backends: JIRA / Notion / GitHub Issues mirrors |
| **Distribution** | — | Packaging to install & run without a developer toolchain |
| **Roadmap tooling** | — | Generate & maintain this roadmap from the ledger |

---

## Follow-ups spawned

- **E-1842** — Author and publish VISION.md (standalone, not under the E-1834
  epic per Mike). **Landed.** Tagline "Manage 50+ AI tasks without becoming
  overwhelmed"; built from Mike's Key Tenets and the lower-review-burden philosophy
  (headline billing), with the positioning reframed per point 8 above — one person's
  concurrent-task throughput as the pitch, multi-developer collaboration as table
  stakes. Cleans up E-1835.
- **E-1843** — Slim README.md to a tight intro linking to ROADMAP.md and
  VISION.md. Under E-1834; relates to E-1842; cleans up E-1835.

## Remaining under E-1834 (not filed as a separate task by request)

Publishing ROADMAP.md itself — writing the text above to repo-root `ROADMAP.md`
and committing — awaits Mike's review of this outcome, then proceeds as the next
step of the E-1834 epic.

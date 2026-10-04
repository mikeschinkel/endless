# Seed / framing

Mike's proposal (2026-09-15), prompted by an agent coining "attention board" for
what Endless calls `project status` and `project monitor` and spreading it to
roughly twenty sites across three landed tasks before it was caught:

> maybe we want to add a 'glossary' feature considering how often you like to
> coin terms? If we had a glossary we could add guidelines that you need to get
> new terms approved, and an early part of a new project would be coining and
> agreeing on terms rather than what you do is you just start using them and put
> the burden on the user to figure out from context.

Two claims in that, and they are separable. One: a project should have an agreed
vocabulary, recorded. Two: coining a term should require approval, so the burden
of reconciling a new word with the existing ones sits with the agent rather than
the reader.

The failure it targets is not misnaming. It is UNILATERAL naming — an agent
introducing a word, using it confidently in durable content, and leaving the
reader to work out whether it denotes something they already know.

# What to explore

- What a glossary entry is. A term, a definition, and what else — the command or
  symbol that carries it, when it was agreed, what it is NOT to be confused
  with, which terms it supersedes?
- Where it lives. `.endless/` alongside the ledger, the database, a committed
  markdown file like `LESSONS.md`? It is project vocabulary, so it wants to be
  in the project and readable by a human without tooling.
- How an agent meets it. Injected into context like task context, fetched on
  demand like `endless guide`, or consulted only when writing durable content?
  Context is scarce, and a glossary that grows is a glossary that stops being
  injected.
- Whether it is DESCRIPTIVE or PRESCRIPTIVE. Recording agreed terms is cheap and
  useful on its own. Refusing unapproved ones is the part with teeth and the part
  that can go wrong — prose is not code, and a gate over prose has the
  false-positive problem that this project has already hit twice with the
  worktree-removal matcher.
- What "approved" means mechanically. A verb (`endless term add`?), a status, a
  review step? Coining is currently invisible; even making it an explicit ASK
  with no enforcement would change the default.
- The early-project stage Mike describes: agreeing terms up front rather than
  discovering them. Does that become a workflow step, a task type, a prompt at
  `project init`?
- How it interacts with what exists. `endless guide` teaches workflow, LESSONS
  records corrections, decisions record rationale. A glossary is a fourth kind of
  durable knowledge and should not become a place the other three leak into.
- Detection. Is there any cheap signal that a new term has been coined — a noun
  phrase in durable content matching no command, symbol or glossary entry?
  Worth examining, and worth being honest about the false-positive rate before
  proposing it.

# Constraints

Judge as a product, not as this machine (PRODUCT). A glossary must be useful to a
project that is not Endless, on a harness that is not Claude Code, for a user who
never reads the source. Say what it does for a solo user with no agents at all.

This brainstorm should NOT conclude by proposing a gate simply because gates are
what this project reaches for. The lightest thing that changes the default — an
agent asking before coining — may be most of the value.


# Since this was filed (ES-1248, 2026-10-04)

**A second failure mode: overloading, not just coining.** In one session:

- "board" was coined for the session status / session monitor display, and
  nearly reached a plan and a JSON field before being caught.
- "owner"/"ownership" turned out to carry three unrelated meanings at once: the
  claiming session (ED-1560), the session whose display keeps a duplicated task
  (E-2188), and the worktree lock (ED-1530). Nobody coined anything; the word
  drifted.
- "triager" was used both as a class of job and as one job's name.
- "surfaced" (the stored relation) and "filed by" (what a reader would say)
  name the same fact. Mike prefers the latter; that is a code-vs-reader
  vocabulary gap rather than a coined word.

So a glossary has to catch overloading as well as coining. That means an entry
needs to say what a term is NOT, and which other senses it was split from.

**A decision already did glossary work.** ED-1605 (accepted) defines owner, the
owner's current Claude session, steward and worktree lock holder, and says
"Endless uses 'own' for nothing else". E-2225 aligns the code and docs with it.
Whether glossary entries are decisions of a kind, or a separate store that
decisions feed, is now a live question. E-1868 is moving decisions onto tasks,
which changes what "a decision" will be.

**A lesson is standing in for the glossary today.** "Use Endless's existing
terms; never mint new ones in explanations" is in LESSONS. It cannot work
without a list of what the existing terms are.

# Seed entries to test the shape against

owner · the owner's current Claude session · steward · worktree lock holder ·
claim / claiming session · rater (a job) vs triager (the class) · filed by vs
surfaced · project status / project monitor vs session status / session monitor
· "wake" (discussed, deliberately not adopted, so there is nothing to define).

# Open questions (fine to leave some open)

1. Entry shape: term, definition, NOT-to-be-confused-with, superseded terms,
   the code identifier that carries it, the decision that settled it?
2. Store: a committed markdown file, a table, decisions of a glossary kind, or
   a section of the guide?
3. How agents meet it: injected, fetched on demand (`endless guide glossary`?),
   or checked only when writing durable content?
4. Coining: an explicit ASK with no enforcement, a verb (`endless term
   propose`), or a gate? The constraint below still applies.
5. Overloading: is there any cheap signal that one word is being used in two
   senses, or is that a review-time judgment only?
6. Code identifiers vs reader terms (`surfaced` vs "filed by"): does the
   glossary map them, or does the code rename to match?
7. The early-project step Mike described: agreeing terms up front.

# Deliverable

An outcome recommending the lightest design that changes the default, with the
seed entries written in that shape as a worked example, and a list of follow-up
tasks if the recommendation needs any.

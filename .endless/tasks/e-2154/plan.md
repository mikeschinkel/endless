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

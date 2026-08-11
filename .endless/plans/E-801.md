# Documents on disk for external tools

## What is parked here

Only one thing: if a tool Endless has to work alongside requires markdown files
on disk in prescribed locations, Endless needs a way to keep those files current
without a human re-typing content that already lives in the ledger.

That is a generation-and-sync problem — one authoritative source rendered out to
one or more files — and nothing more. It does not need a document entity, event
kinds, section identifiers, Lexorank ordering, a proposal/review workflow, or
conflict surfacing.

## Why the original epic died

The epic was written to solve design-doc drift: ask an AI to revise a design
document and it regenerates the whole document instead of evolving the existing
one, leaving two versions that partly agree and require manual reconciliation.
The proposed fix was documents as event-sourced entities peer to tasks, with
section-level events making whole-document regeneration structurally impossible.

Brainstorm tasks solved that problem by another route. A brainstorm task is a
topic whose thinking evolves in place across sessions, in a field the tooling
already treats as authoritative, under a lifecycle that already has human review
in it. That is what the document entity was for. Once brainstorms existed, the
section-level event machinery had no problem left to solve, and every child of
this epic was obsoleted.

Two children died for reasons of their own as well. The dashboard views die with
the decision to rewrite the web surface from scratch. The ADR workflow lost to
decisions as a capability, whose storage shape is being re-decided under E-1868.

## What would revive this

A concrete external tool that reads markdown from a fixed path, where the
content it needs is already maintained in Endless. When that shows up, the tool
specifies the requirement — file layout, section conventions, refresh trigger —
far better than this placeholder can, so the right move is a fresh task written
against that tool rather than reopening the shape sketched here.

The in-repo precedent for what the mechanism would look like is the canonical
status-lifecycle mermaid block, which is duplicated byte-identically across
several documentation files and held in sync by a verification script rather
than by generation. That pattern generalizes: one source, rendered out, checked
at land time.

## Superseded design

`docs/private/endless-documents-design.md` describes the abandoned approach. It
is retained as a record of the reasoning, not as a spec. Its analysis of the
drift problem still holds; its proposed mechanism does not.

## Note on how this branch was landed

This branch could not be landed as it stood. It had been used for ten days
without ever being reset after its earlier lands, so it carried a large number
of commits whose patches were already upstream on main, plus main had moved
well ahead independently. Replaying that pile conflicted on the append-only
ledger segments — both sides had appended different lines to the same file —
once per ledger commit, so recovering and retrying could not converge.

Comparing trees rather than counting commits showed the branch was genuinely
ahead of main on exactly two files. The branch was therefore reset onto main
and those two changes re-applied, so the landing replays two clean commits
instead of the accumulated history. The discarded commits remain reachable
through the reflog from the pre-reset tip if anything is ever found missing.

The two salvaged changes are this plan itself, and the README tagline — the
newer positioning, naming AI tasks generally rather than Claude Code
specifically, which had been stranded on this branch unlanded. The tagline is
unrelated to this task's subject and rides along only because this branch is
where it was orphaned.

Why the branch went stale in the first place, and what should prevent it, is
E-1943. The narrower failure where a stale branch aborts a land on enum drift
is E-1941.

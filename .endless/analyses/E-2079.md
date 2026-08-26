Observed 2026-08-26 while building tests/tasks/e-2016-verify.sh: a throwaway project registered from scratch, one `task add` titled 'Anchor epic ...', and `audit` stopped being completable.

The shadowed defaults are the completable ones that matter most here: audit, analyze, assess, compare, decide, design, diagnose, document, evaluate, examine, explore, investigate, redesign, research, review, survey.

Consequence chain:
1. `is_completable_verb('audit')` answers False.
2. `_require_completable_verb_for_completed` raises "'completed' isn't a valid final status for this task. Implementation tasks finish as 'confirmed' or 'assumed'."
3. The message points at task TYPE, but the actual cause is verb resolution — so the reader chases the wrong thing.
4. After E-2016 this is worse: a research task routed to `unreviewed` cannot leave it, because the only exit is `completed`.

Why it stayed invisible: Endless's own .endless/verbs.jsonl already carries the completable entries, so the shadow is harmless here. It bites someone else's project on someone else's machine — a fresh tracked repo, first task filed, silently unable to complete findings work.

tests/tasks/e-2016-verify.sh works around it by seeding .endless/verbs.jsonl explicitly in setup_fixture, with a comment saying why. Remove that workaround when this lands.

Two candidate fixes, not yet chosen:
- Layer the sources (defaults < machine < project) so a project file overrides per-verb rather than wholesale. Changes resolution semantics for anyone relying on first-wins.
- Have auto-registration copy the matching default entry's fields (including completable) when the verb it is registering is already one of the defaults. Narrower, but leaves the shadowing semantics in place for hand-edited files.
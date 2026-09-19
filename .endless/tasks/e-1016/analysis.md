Open question, not a committed deprecation. Once session use (E-1014) and shell-init (E-1015) have been in real use long enough for confidence, decide whether session cd remains as a thin lookup verb (just print the path, no env-vars) or gets retired in favor of `eval \$(endless session use)`-style activation.

Arguments for keeping both:
- `session cd` is a single-purpose verb: 'I want the path.' Useful in scripts and one-liners that don't need full activation.
- Removing it churns existing muscle memory and any docs / shell wrappers that referenced it.

Arguments for deprecating:
- Two verbs covering overlapping territory invites 'which one do I want?' friction.
- session use is strictly more capable; cd is a subset.

Decision deferred. File output of session use experience over weeks; revisit when there's signal either way.

Origin: E-1014 landing. Mike pushed back on the 'will deprecate' framing — keep cd until there's evidence it's friction, not before.
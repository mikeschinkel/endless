Today the CLI's content flags are inconsistent three different ways for the same kind of
(multi-line) content:

- `--text` takes a FILE PATH (dest `text_file`) but the name reads like inline content.
- `--analysis` takes inline content OR a path via `@file` magic (added by E-1329).
- `--outcome` / `--description` take inline content only.

This forces the user to remember which flag means which, and it has already produced a latent
bug: the research handoff template instructs `endless task update … --outcome <file>`, but
`--outcome` has no file form, so the literal path is stored as the outcome text.

## Decision (locked with Mike 2026-06-20)

- Every **long-form content flag** gets BOTH forms: bare `--xxx` carries **inline** content,
  `--xxx-file` carries a **file path**. Applies to: **text, analysis, outcome, description**.
- **justification** and **reason** stay **inline-only** (typically short) — a deliberate
  partial-consistency exception, not an oversight.
- **Remove the `@file` magic** on `--analysis` (E-1329, cli.py:1305-1310); `--analysis-file`
  replaces it. One mechanism, no magic. (No auto-detect of path-vs-inline — brittleness
  outweighs convenience.)
- **Hard cutover**, no alias / migration window (pre-1.0, move-fast, no-legacy).

## Consequence to watch

`--text` today IS the file-path flag, so the change INVERTS it: `--text` becomes inline and
`--text-file` becomes the path form. Every existing `--text <path>` call site (code, templates,
docs, tests) must become `--text-file <path>`.

## Coordination (not blockers)

- E-1000 (text→plan rename): whichever name survives, the `--xxx`/`--xxx-file` split applies.
- E-1554 (adds `--notes`): should follow this convention when it lands.
- Independent of the field-separation design (E-999); applies to whichever flag names survive.
- Origin: E-989 finalization.

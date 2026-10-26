Choices made with the user (planning conversation for E-2443):

- Discovery (Q1): each parent section links to all of its subsections at the point where each applies. The index does not list subsections. Reason: subsections exist to avoid information overload for agents, and a full index listing would bring the overload back. By convention every section links every subsection, and a test enforces it.
- Depth (Q2): one level only (`section/sub`).
- Cap (Q3): seven or fewer subsections per section, as at the top level (ED-1637).
- Help directory (Q4): rename `docs/guide/help/` to `docs/guide/_help/` and make a leading `_` mean "special handling, not guide content". It stays inside `docs/guide/` because the user plans to move the whole guide later, and splitting part of it out now would be the wrong architecture. `_` was chosen over `@` and `$`:
  - `$` expands in an unquoted shell word, so `docs/guide/$help` breaks in commands and docs;
  - `@` is read specially by some tools (response-file and scope syntax);
  - `_` already marks `_topics.md` as non-content, so the convention is already in use.
- Scope (Q5): this task builds only the mechanism. Splitting the existing long sections is E-2447, and the first real subsection, `tasks/post-land`, arrives with E-2444.

Decided without asking:
- `--list` shows subsections.
- `guide <section>` does not append its subsections.
- Subsections render through the same template path.
- An unknown subsection is refused with the real ones named.
- Map files may point at subsections.
- The seven-section cap is tested.

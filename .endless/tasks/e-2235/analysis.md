Settled in E-2154 (see its outcome):
- Own table, Go-owned, written through the event pipeline. Not files, not the decisions table.
- Two layers: project (default) and global. Global entries have built_in and enabled; users add their own or disable built-ins. A project entry shadows a global one of the same term; listings say what is shadowed.
- Entry: term, definition, not-to-be-confused-with, split-from, disposition (agreed | rejected), use-instead (rejected), links to the task(s) where it was settled. No proposed/status lifecycle — an entry exists only once the user said yes.
- Commands: `endless glossary ...` for the corpus (list with colour, -p pagination, --terms-only); `endless term ...` for single entries (add, show, reject, disable). `term show` accepts multiple terms in one call.
- Handoff injects the bare agreed terms and the rejected terms (with use-instead), plus the commands to fetch definitions and a pointer to the guide section.
- Guide section: how an agent proposes a term (term, definition, what it is NOT, what it was split from), the user says yes in chat, the agent runs the command. No enforcement and no detection — the user is the detector.
- The LESSONS entry "never mint new terms" becomes "propose terms; use only agreed ones".
Open for planning: whether global terms wait for the global/project DB split or land in the current DB first.

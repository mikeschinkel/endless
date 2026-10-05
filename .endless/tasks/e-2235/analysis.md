Settled in E-2154 (see its outcome):
- Own table, Go-owned, written through the event pipeline. Not files, not the decisions table.
- Two layers: project (default) and global. Global entries have built_in and enabled; users add their own or disable built-ins. A project entry shadows a global one of the same term; listings say what is shadowed.
- Entry: term, definition, not-to-be-confused-with, split-from, disposition (agreed | rejected), use-instead (rejected), links to the task(s) where it was settled. No proposed/status lifecycle — an entry exists only once the user said yes.
- Commands: `endless glossary ...` for the corpus (list with colour, -p pagination, --terms-only); `endless term ...` for single entries (add, show, reject, disable). `term show` accepts multiple terms in one call.
- Handoff injects the bare agreed terms and the rejected terms (with use-instead), plus the commands to fetch definitions and a pointer to the guide section.
- Guide section: how an agent proposes a term (term, definition, what it is NOT, what it was split from), the user says yes in chat, the agent runs the command. No enforcement and no detection — the user is the detector.
- The LESSONS entry "never mint new terms" becomes "propose terms; use only agreed ones".

Planning answers (Mike, 2026-10-04):
1. Build both layers now in today's single database with a scope column (no project = global); the split epic moves the rows when it lands.
2. The non-project layer is called "global". "Global vs machine database" is an early entry: scope vs storage location, two concepts.
3. Implement in Go, dispatched from `endless` the way the Go-side commands (project status, session monitor) are; confirm that wiring during planning.
4. Built-ins ship as seed rows in internal/schema/seeds.sql with built_in = 1; a user's disable is a ledger event. Changing a built-in's definition in a release is a migration.
5. Inject in the handoff AND the SessionStart hook's additional context, the hook skipping sessions that received a handoff — covers sessions started directly and after /clear.
6. Term identity: case-insensitive term plus an optional variants list (owner: own, ownership); add checks uniqueness across terms and variants.
7. Not-to-be-confused-with and split-from are links to entries, not free text. A confuser is recorded first, even if only as rejected.
8. Revising an agreed term is `term update` in place, linking the task where it was revised; the ledger is the history.
9. A project opts out of a global term with a project entry marked rejected that shadows it. `term disable` is global-only.
10. Retiring the old lesson uses a lesson retire command, filed separately and preceding this task.
11. OPEN: guide placement — a new `endless guide glossary` topic (there are six topics today) or a section in an existing one.

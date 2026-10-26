# Plan

Terms: a **section** is `docs/guide/<name>.md` (index excluded). A **subsection** is `docs/guide/<name>/<sub>.md` where `docs/guide/<name>.md` exists, read with `endless guide <name>/<sub>`. A **map file** is a per-command file under the guide's help directory that names the section a command is pointed at.

## Approach

1. Rename `docs/guide/help/` to `docs/guide/_help/` (git mv). Adopt one rule: a `docs/guide/` entry whose name starts with `_` is not guide content, so it is never a section or a subsection. `_topics.md` moves along with the directory. Update `HELP_DIR`/`TOPICS_FILE` in `src/endless/guide_map.py`, the `justfile` guide recipes, `.claude/commands/regenerate-guide.md`, `tests/test_guide_map.py`, and every message that prints the old path.
2. Make one resolver the single source of guide addresses (in `guide_map.py`, which is import-light): it yields section slugs and `section/sub` addresses, honoring the `_` rule, one level deep. `cli.guide`, `guide_sections()` and map-file validation all read from it, instead of the three separate `glob("*.md")` calls they have now.
3. `endless guide <section>/<sub>` renders the subsection through `render_guide_file`. `endless guide <section>` still prints only the parent file. `--list` prints each section followed by its subsection addresses.
4. Refusals: an unknown subsection is refused, naming that section's real subsections. A depth beyond one, a section name starting with `_`, and `index/...` are refused as unknown addresses.
5. Tests enforce the structure rules over the real `docs/guide/`:
   - seven or fewer sections;
   - seven or fewer subsections per section;
   - no subdirectory without a sibling section file (apart from `_` entries);
   - no nesting below one level;
   - every subsection is named in its parent's body as `endless guide <section>/<sub>`.
6. Map files and `_topics.md` may name a subsection in `section:`. Validation accepts any address the resolver yields. The cross-reference index and the AGENT banner print the subsection address as written.
7. `tests/test_guide_conditionals.py` covers subsection files, which its top-level glob misses today.
8. The guide's own text documents the convention in an existing place: the index's Sections paragraph, and the `/regenerate-guide` command for authors.

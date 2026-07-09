# Gate inline content flags against file paths + repair path-corrupted ledger fields — PLAN

## Context

Every inline multiline CLI flag (`--text`, `--description`, `--analysis`,
`--outcome`, `--justification`) stores its argument **verbatim**. Passing a file
*path* to one of these stores the path string and silently discards the intended
content. This is a footgun: the old `--text` accepted a file, but the convention
was changed so that any flag expecting a file is named `--xxxx-file` and the bare
`--xxxx` flag always means literal inline content. Agents and humans still reach
for `--text <path>`, corrupting the ledger.

Confirmed corruption at filing time (real DB):
- `tasks.text` E-1626 = `/tmp/e-1626-plan.md` — referenced file gone, no committed
  mirror `.endless/plans/E-1626.md` → likely **unrecoverable** (check git history
  `--all` before giving up). Task is `obsolete`, but repair it if a mirror turns up.
- (`tasks.outcome` E-1564 was corrupted the same way and has already been repaired
  in a resumed session — no longer in scope; the repair pass re-scans to confirm.)
- E-1202 `tasks.text` was corrupted the same way and already repaired by hand.

Guides, docs, spawn-prompt templates, and CLI help/error strings still say
`--text <path>`, actively teaching the mistake.

Two deliverables: **prevent** (a CLI gate) and **remediate** (repair data + fix
docs/guides/messages).

## Deliverable 1 — CLI gate at `_resolve_content_flag` (prevent)

Single chokepoint: `_resolve_content_flag(inline, file_path, name)` in
`src/endless/cli.py` (~line 1347). Every inline/file flag pair funnels through it
— `task add`, `task update`, `task complete`, `task assume`, `task replace`,
`decision add`, `epic add` — so one guard here covers all fields and all tables.

When `inline is not None`, apply two block rules. **Relative paths are always
allowed** — plans reference project-relative source paths (`internal/hookcmd/
claude.go`, `src/endless/cli.py`, `./tests/...`) constantly; blocking those would
make plans unwritable. Only **absolute** paths are problematic: non-portable, and
ephemeral in the `/tmp` case.

### Rule 1 — the whole value IS a path (mis-passed file)
The entire stripped value is a single whitespace-free path token — absolute OR
relative — that is path-shaped: starts with `/`, `./`, `../`, `~/`, OR contains `/`
/ ends in a file extension (`.md`/`.txt`/`.rst`/…). This is the original corruption
bug (`--text ./plan.md`, `--text /tmp/plan.md`). Block whether or not the file
exists (E-1626/E-1564 were lost precisely because a gone path was accepted). Raise
`click.ClickException` (Python f-string — this gate is Python; no Go template):
`f"--{name} received a file path ({value!r}). --{name} stores its argument "
 f"verbatim as inline content; to load a file's content use --{name}-file."`

### Rule 2 — the content CONTAINS an absolute path (anywhere)
Any token in the content that is — or after `~`-expansion / realpath becomes — an
**absolute** filesystem path (`/…`, `~/…`), appearing ANYWHERE, blocks regardless
of surrounding prose length. Relative tokens mid-content are NOT caught. (This
replaces the earlier short-wrapper / <100-char-remainder heuristic.) Existence is
not required — a gone absolute path still blocks. Message: absolute paths don't
belong in durable ledger content (non-portable; `/tmp` is lost when the worktree
drops) — put real content inline or use `--{name}-file`; for a cross-project
reference prefer a Git file URL over an absolute filesystem path.

### Escape hatch — `--allow-path=<regex>` (repeatable)
A plain, **repeatable** flag (NOT `--allow-path-file` — a path is a reference, not
file *content* like `--text-file`/`--outcome-file`). An absolute path in the
content matching ANY supplied `--allow-path` regex is exempted from both rules. For
the rare legitimate absolute path (e.g. referring to another project — though a Git
file URL is usually better).

Detection notes:
- Extract path candidates conservatively to avoid false positives on prose: treat
  `/…`-leading and `~/…`-leading tokens as candidates; expand `~`; test
  absoluteness lexically (no filesystem check — gone paths still block).
- `--allow-path` values are regexes tested against each candidate absolute path.

### Built-in allowed paths (decided) — endless's own config + cache dirs
Docs and plans legitimately reference stable endless-owned locations outside the
project root (`<config_dir>/endless/…` and `<cache_dir>/endless/…` — e.g.
`~/.config/endless/endless.db`, `~/.cache/endless/sandboxes/…`). These are ALWAYS
exempt from both rules, with NO `--allow-path` needed:
- Build the exempt set as a data-driven **table** of resolved endless dirs — NOT an
  `if`/`switch`. Resolve via endless's own config/cache-dir resolution (honors
  `XDG_CONFIG_HOME` / `XDG_CACHE_HOME`); do NOT hardcode `~/.config/endless`.
- One composition point: effective allowed set = built-in table + per-invocation
  `--allow-path` regexes (+ later, project-config `allowed_paths` — see follow-up
  task). Apply to Rule 1 and Rule 2 alike.
- Terminology: **"allowed paths"**, never "whitelist".

Add unit tests (pytest):
- Rule 1 blocks: `/abs/x.md`, `./x.md`, `~/x.txt`, `/tmp/e-1.md` (gone or present),
  a bare `foo.md` as the whole value.
- Rule 2 blocks: `See /tmp/x.md for detail`; `the plan lives at /Users/x/plan.md`
  inside an otherwise-long paragraph; a multiline value with an absolute path on
  any line.
- Allowed: relative refs mid-content (`edit internal/hookcmd/claude.go then …`,
  `notes in ./foo.md`); multiline content whose only paths are relative; prose with
  no path.
- `--allow-path` exempts: `--text "see /opt/corp/spec.md" --allow-path '^/opt/corp/'`
  → allowed; a second, non-matching absolute path in the same content still blocks.

## Deliverable 2 — Data repair

One-shot repair (a script, not an auto-migration — this is a ledger content fix;
land per the ledger-hygiene-direct-to-main path, not a user worktree). Scan every
inline multiline column for a corrupted value — Rule 1's predicate (the whole
value is a single path token) OR Rule 2's (contains an absolute path):
- `tasks`: `text`, `description`, `analysis`, `outcome` (and `notes` if it can be
  set from an inline flag).
- `decisions`: `description` (+ any other inline multiline columns).

For each hit, recover content in priority order:
1. Committed mirror `.endless/plans/E-NNN.md` — in the worktree, on `main`, or in
   git history (`git log --all -- .endless/plans/E-NNN.md`, then
   `git show <sha>:<path>`).
2. The referenced path itself, if it still exists on disk.
3. Otherwise report the row as **UNRECOVERABLE** for manual attention.

Emit a per-row report (recovered-from / unrecoverable). Do NOT silently drop
unrecoverable rows — surface them. Known unrecoverable at filing: E-1626.text
(re-verify against git `--all` first).

## Deliverable 3 — Docs / guides / error-message sweep (stop teaching it)

`grep` the repo for guidance that passes a path to an inline flag and fix each to
the `--xxxx-file` form (or clarify inline-vs-file):
- Guide markdown and the `endless guide` source, spawn-prompt templates, any
  `CLAUDE.md` snippets, and `*.py` Click help/error strings.
- Patterns: `--text <`, `--outcome <`, `--description <`, `--analysis <` followed
  by a path-looking token.
- Coordinate with the E-1202 gate message (already corrected to `--text-file`) and
  note that the E-1202 task *description* still contains the wrong `--text` form.

## Deliverable 4 — Generate the verify script (do NOT hand-verify)

As part of this task, the implementing session GENERATES
`tests/tasks/e-1744-verify.sh` (self-contained, sandbox DB, `uv run endless` — no
build; shape/output per `tests/tasks/e-1577-verify.sh`, "ALL PASSED" or a failure
list). It must cover, at minimum:
- **Rule 1 blocks** (whole value is a path): a bare path to `--text`, `--outcome`,
  `--description`, `--analysis` (via `task update` AND `task add`) → refused, output
  names the matching `--xxx-file`. Include a nonexistent path (still blocked).
- **Rule 2 blocks** (absolute path anywhere): `See /tmp/x.md for detail`; an
  absolute path buried in a long paragraph (`… the plan is at /Users/x/plan.md …`);
  a multiline value with an absolute path on any line → refused.
- **Allowed (must stay green)**: `--text-file <real file>`; **relative** paths
  mid-content (`edit internal/hookcmd/claude.go then …`, `notes in ./foo.md`);
  multiline content whose only paths are relative; a plain inline note with no path.
- **Escape hatch**: `--text "see /opt/corp/spec.md" --allow-path '^/opt/corp/'` →
  allowed; a second non-matching absolute path in the same content still blocks;
  `--allow-path` repeatable.
- **Built-in allowed paths**: a path under endless's config or cache dir (e.g.
  `--text "the DB is at ~/.config/endless/endless.db"`) is allowed WITHOUT
  `--allow-path`; verify it still passes when `XDG_CONFIG_HOME`/`XDG_CACHE_HOME` are
  set (resolved, not hardcoded).
- **Docs sweep**: no `--<inline-flag> <path>` guidance remains in `docs/` or the
  guide/help source.
- **Data repair**: the one-shot repair is a separate script that emits its own
  recovered/unrecoverable report — reference it in the handoff for a manual eyeball
  rather than re-running it inside the verify script.

Commit the verify script with the implementation.

## Verification (handoff to Mike)

On completion, set `unverified` and tell Mike to run exactly:

```
esu && ./tests/tasks/e-1744-verify.sh
```

Do not hand-verify on Mike's behalf; the script is the verification.

## Notes

- Relates to the E-1202 preToolUse gate (a Claude-hook gate on plan-mirror file
  writes); this task is the complementary **CLI-layer** gate on the flag *value*,
  plus the data/doc cleanup the corruption made necessary. Complemented by the
  multiline-mirror task (durability) and the `/tmp`→`.endless/tmp` task (sanctioned
  scratch dir).
- Both rules hard-block (Mike): Rule 1 = the whole value IS a path (absolute or
  relative — the mis-passed-file case); Rule 2 = the content contains an ABSOLUTE
  path anywhere. Relative paths mid-content are always allowed (plans reference
  project-relative source paths constantly). `--allow-path=<regex>` (repeatable) is
  the escape hatch for a genuine absolute path.
- The verify script is a deliverable of THIS task (Deliverable 4), generated by
  the implementing session — not pre-authored.

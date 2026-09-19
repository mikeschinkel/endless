# Implementation spec — `endless session recap`

Answer "what was this session about?" on demand. The need is concrete: E-1914's
reworked `session list` renders the active task's id and title, so a session that
never claimed a task shows two blank cells, and E-1918 auto-creates a task titled
`Auto-resumed task for session ES-NNN` on resume *because the user cannot name
it*. This is the command that lets them find out.

Explicitly user-invoked and expected to be rare — reached when triaging a
task-less session, not on a schedule.

---

## Command surface

```
endless session recap <session-id|task-id> [--refresh] [--json]
```

- `<ref>` accepts a session (`ES-NNN`, bare integer, UUID prefix) or a task
  (`E-NNN` → that task's most-recent session that has messages).
- Resolve via `_resolve_session` (`src/endless/session_cmd.py:62`), which is
  ES-aware as of E-1914. Do not add a fourth session-ref resolver; E-1918 is
  actively reducing the count.
- `--refresh` — ignore the cache and regenerate.
- `--json` — `{session_id, recap, generated_at, cached, stale}`.

---

## Input: `session_messages`, not the transcript file

E-1905 dropped `sessions.transcript_path`, and the path is known only at hook
time — so there is no stored path to re-read. There does not need to be:
`monitor.ParseTranscript` already writes every message into `session_messages`,
which is what `session history` and `session search` read. Recap summarizes that.

Consequence worth stating: recap works for an ended session whose transcript file
has been deleted, because the content was projected into the DB when it was live.

Selection:

- Roles `user` and `assistant` only. `tool_use` is excluded — it is 61% of the
  whole table (56,236 of 92,545 rows), and tool input (file paths, command text)
  compresses poorly and would crowd out the conversation.
- **Known boilerplate excluded**, from BOTH the input and the staleness count.
  Skill prompts recur verbatim across sessions — one appears 149 times across 133
  real sessions — and a boilerplate `user` row is not a turn in any meaningful
  sense. Left in, running such a skill four times would stale a recap without the
  conversation having moved, and would spend budget on text that says nothing
  about this session. Match the same way `session list` already filters recap
  probes (`content LIKE` prefixes), and keep the patterns in one named constant.
  Excluded from recording? **No** — these are real user turns, and dropping them
  at record time would make `session history` misrepresent what was typed. They
  are excluded from recap only.
- Ordered by `created_at, id`.
- Bounded by `RECAP_INPUT_BUDGET = 60_000` characters, **enforced in SQL, not in
  Python**. Reading 696 KB into memory to then discard 90% of it is backwards;
  the query must return only what will be sent.

### Budgeting in the query

Two levels, both in SQL:

1. **Per-message cap** so one pathological message cannot consume the budget:

   ```sql
   CASE WHEN length(content) <= :msg_cap THEN content
        ELSE substr(content, 1, :msg_cap/2) || ' … ' ||
             substr(content, -(:msg_cap/2))
   END
   ```

2. **Set-level budget** via a running total, so the row count is decided by the
   database:

   ```sql
   SUM(length(content)) OVER (ORDER BY created_at, id)              -- from the head
   SUM(length(content)) OVER (ORDER BY created_at DESC, id DESC)    -- from the tail
   ```

   Take head rows while the ascending running total is under the head share, tail
   rows while the descending running total is under the tail share, `UNION` them,
   and emit `… N messages elided …` between when anything was dropped. SQLite has
   supported window functions since 3.25, and the bundled `modernc.org/sqlite`
   is well past that.

### Head/tail split — sliding, tail-weighted

The longer a session ran, the less its opening predicts its outcome: sessions
drift, and the ending is where the conclusion lives. So the head share shrinks as
the session grows past budget:

```
head_share = clamp(0.5 * RECAP_INPUT_BUDGET / total_chars, 0.15, 0.5)
tail_share = 1 - head_share
```

| total | head / tail |
|---|---|
| 1× budget | 50% / 50% |
| 2× budget | 25% / 75% |
| 10× budget | 15% / 85% (floor) |

The 0.15 floor keeps some opening context at any length — a recap that never says
what the session set out to do is not a recap. `total_chars` comes from a
`SELECT sum(length(content))` over the same filtered set, so the split is computed
before any content is fetched.

---

## The model call

Route through `internal_claude.run_internal_claude` (`src/endless/internal_claude.py`).
**Never hand-roll a `subprocess.run(["claude", ...])`.** That helper sets
`ENDLESS_NO_HOOKS=true`, which is the entire defense against E-1470 — a headless
`claude -p` inheriting the caller's `TMUX_PANE`, registering a fresh session UUID,
and the pane-collision rule then marking the CALLING live session `ended`, which
broke `endless session id` and `task add` until the next Stop hook. Recap was one
of the two original callers that hit it.

```python
internal_claude.run_internal_claude(
    prompt,
    timeout=RECAP_TIMEOUT_SECONDS,      # 120 — input is far larger than triage's
    model=config.internal_model("recap"),
    effort="low",
)
```

Add to `config.INTERNAL_MODEL_DEFAULTS`:

```python
"recap": "haiku",
```

A recap is compression, not judgment, so it sits with `verb_check` (haiku) rather
than `triage` (sonnet). Per-project and per-user override come free from
`internal_model`.

**Failure is loud, not fail-open.** Triage fails open because it is a background
sweep whose worst case is the status quo. This is an explicit command the user is
waiting on, so a missing `claude`, a timeout, or a non-zero exit raises a
`ClickException` naming the cause. Nothing is cached on failure.

### Prompt

Ask for 1–3 sentences of plain prose answering *what was this session working on,
and where did it end up*. No preamble, no markdown headings, no bullet list — the
result has to survive being truncated into a `session list` Title cell.

---

## Storage — ONE field, named `recap`

`sessions.summary` is renamed to `sessions.recap`. It is not joined by a second
column; there is exactly one "what is this session about" field.

E-1906 was supposed to make this rename and did not — it dropped `needs_recap`
and `summary_seq` but left `summary` in place, contrary to the requirement. This
task finishes that. Adding a *new* `recap` column beside `summary` (as the first
draft of this plan proposed) would have entrenched the duplication rather than
removing it: both fields answer the same question about the same session.

The two writers are then two qualities of the same value:

| Written by | When | `recap_at` |
|---|---|---|
| `seedRecapIfEmpty` (transcript parse) | first assistant response, if empty | NULL |
| `session recap` | on demand, from the whole conversation | set |

`recap_at IS NULL` therefore means "cheap seeded placeholder, never generated" and
is the discriminator wherever the distinction matters. A generated recap
overwrites a seeded one; a seeded one never overwrites anything (the writer is
`IfEmpty` and stays that way).

Columns after this task:

| Column | Meaning |
|---|---|
| `recap TEXT` | the text (seeded placeholder or generated) |
| `recap_at TEXT` | when GENERATED; NULL for a seeded placeholder |
| `recap_watermark_id INTEGER` | `MAX(session_messages.id)` at generation time |

Staleness is **derived at read time**, never stored-and-cleared, so there is no
write-path touchpoint in the Go hook at all:

```sql
SELECT count(*) FROM session_messages
 WHERE session_id = ? AND role = 'user' AND id > recap_watermark_id
```

…excluding known boilerplate (below). Stale when that count `>= RECAP_STALE_TURNS`, which is **4** (Mike specified
"3 to 5"; 4 is the midpoint, held in one named constant so it is tunable without
a hunt). A *turn* is a **user** message — the unit a human perceives as a turn,
and the one that does not inflate with tool_use noise.

This is knowingly the mechanism E-1906 removed. Its change file describes
`summary_seq` as *"the recap watermark (the count of user messages at the last
recap, used to decide whether enough new ones had accrued)"*. The watermark was
sound; what E-1906 removed was the **automatic background generator** wrapped
around it, on the premise that one session maps to one task — the premise a
task-less session violates. So: reinstate the watermark, never the generator.

Two deliberate differences from the dropped column: the name is
`recap_watermark_id`, not `summary_seq`, so it cannot be mistaken for the old one;
and it stores a message **id** rather than a count, which stays correct if rows
are ever backfilled or deleted.

### Schema mechanics

Declare the final shape in `internal/schema/schema.sql` **and** ship a change file
(`internal/schema/changes/e-1925-rename-summary-to-recap.sql`):

```sql
ALTER TABLE sessions RENAME COLUMN summary TO recap;
ALTER TABLE sessions ADD COLUMN recap_at TEXT;
ALTER TABLE sessions ADD COLUMN recap_watermark_id INTEGER;
```

schema.sql alone is not sufficient, and this is the trap: `CREATE TABLE IF NOT
EXISTS sessions` no-ops on a populated DB, so an existing ledger would keep
`summary` and never gain the new columns. Same two-part pattern as E-1683's
`do_order` and E-1914's `session_hidden_tasks`.

The rename preserves existing data — every current `summary` value becomes a
seeded placeholder recap with `recap_at` NULL, which is exactly what it is.

`src/endless/db.py`'s legacy `_migrate` adds a `summary` column to very old DBs
(`db.py:382`). Leave that alone: it reproduces a historical shape, and this change
file then renames it. Same rule E-1906 applied to the e-1568 change file.

### Rename every reader

The column is read in more places than its name suggests. All of these move:

| Site | What it does with it |
|---|---|
| `internal/monitor/transcript.go:257` | the writer (`setSummaryIfEmpty` → `seedRecapIfEmpty`) |
| `internal/monitor/live_sessions.go:65` | carried on every live-session record |
| `internal/monitor/session_nav.go:140-141` | labels BOTH endpoints of every `session trail` edge |
| `src/endless/session_cmd.py:761` | the `Not logged in%` exclusion in `session list` |
| `src/endless/session_cmd.py:778, 812` | `session list` query + its `--json` field |
| `src/endless/session_cmd.py:1239` | live-session record passthrough |
| `src/endless/session_cmd.py:1576, 1598, 1617` | `session show` display + `--json` |

`session list --json` and `session show --json` rename their `summary` key to
`recap`. That is an intentional shape change: the field's meaning is being
unified, and leaving a `summary` key over a `recap` column would reintroduce the
confusion this task exists to end.

---

## Fix the byte-slicing truncation while renaming the writer

`setSummaryIfEmpty` (`internal/monitor/transcript.go:257`) currently corrupts
UTF-8, and it is live on every new session — not historical damage:

```go
cutoff := 200
for i := cutoff; i > 100; i-- {
    if summary[i] == '.' || summary[i] == '!' || summary[i] == '?' { cutoff = i + 1; break }
}
summary = summary[:cutoff]           // byte index into a UTF-8 string
```

Indexing a Go string yields a **byte**, so both the scan and the cut operate on
bytes. When no `.`/`!`/`?` falls between bytes 101–200, `cutoff` stays 200 and the
slice lands wherever byte 200 lands — splitting a multi-byte codepoint and writing
invalid UTF-8. (The punctuation hits are safe by accident: a single byte can only
equal `.` on an ASCII char.) One row in the real ledger is already damaged this
way, and every new session whose first assistant response exceeds 200 bytes
without punctuation in that window can produce another.

**The fix itself belongs to E-1985**, not here. That task owns truncation
correctness across all three byte-slicing sites (this one, the 500-char tool_use
cap 55 lines away, and `jobs/run.go`), adds the shared rune-safe helper they all
call, and repairs the rows already damaged. Two tasks carrying the same fix would
conflict on the same function.

What this task owes E-1985 is only the rename: `seedRecapIfEmpty` must call the
shared helper rather than re-deriving a cut. Whichever lands second rebases over
the other — they touch the same function, so expect that and do not try to avoid
it by duplicating the fix.

**Also correct the comment E-1914 landed in `src/endless/db.py`.** It attributes
this to "the recap generator removed in E-1906" and calls it "inert historical
damage no current code can add to". Both halves are wrong: the writer is
`setSummaryIfEmpty`/`seedRecapIfEmpty` in the transcript parser, it is live, and
the damage is 56× wider than the one row that comment implies (E-1985 measured it
across `session_messages`). The lenient `text_factory` stays — it is the right
defense for damage no fix can retroactively repair — but its rationale must name
the real writer. Correcting it here is fine since this task already edits that
function's name; correcting it in E-1985 is equally fine. Do not do both.

---

## Interaction with `session list`

- A **fresh, GENERATED** recap (`recap_at IS NOT NULL`) may fill the Title cell of
  a **task-less** row. A seeded placeholder does NOT qualify: the rule is that the
  listing shows a recap only when `session recap` has actually produced one, and a
  200-character slice of the first assistant response is not that.
- Only under `--all`. E-1914 omits task-less rows by default and that rule is
  unchanged: row inclusion must stay a function of the data, not of cache state.
- **Never generate on read.** `session list` renders up to `--limit` rows; a
  generate-on-read fallback would fan out one `claude -p` per blank row — exactly
  the automatic background behavior E-1906 removed.
- A **stale** cached recap renders as blank, not as text. A listing is a glance
  surface, and a recap that no longer describes the session is worse than an empty
  cell; the user refreshes with `session recap`. This also avoids inventing a
  per-cell staleness marker in a table that has no per-cell styling.
- A task-BEARING row is untouched — the task title wins, always.

---

## Non-goals

- **No automatic generation, ever.** Not on `task add`, not from a job, not on
  read, not from the prompt hook. Every path is user-invoked.
- **Does not remove the seeded placeholder.** `seedRecapIfEmpty` keeps running on
  transcript parse. Its unique load-bearing job is the error/login **auto-hide** —
  it is the only code that spots a first response starting `Not logged in` or
  `Error:` and sets `hidden = 1`, keeping dead login-failure sessions out of
  `session list`. Deleting it would silently retire that.
- No recap surface beyond `session recap`, the `session list --all` fallback, and
  the existing label sites the rename carries over.

---

## Verification

Write `tests/tasks/e-1925-verify.sh`, isolated exactly as `e-1914-verify.sh` is
(throwaway git repo as project root under a temp dir, own `XDG_CONFIG_HOME` and
`XDG_CACHE_HOME`, worktree binary on PATH, no real DB/ledger/cache touched).

**No check may invoke a model.** Seed `recap`/`recap_at`/`recap_watermark_id`
directly via SQL and assert cache-hit behavior; in pytest, stub
`internal_claude.run_internal_claude`. A verify script that calls Claude is
non-hermetic, slow, and bills the user to run its own tests.

Checks:

- The change file renames `summary` → `recap` and adds `recap_at` /
  `recap_watermark_id` on an existing `sessions` table; a re-apply is a recorded
  no-op (`"status":"skipped"`).
- **The rename preserves data**: a pre-existing `summary` value survives as
  `recap` with `recap_at` NULL (a seeded placeholder), and the row's `hidden` flag
  is untouched.
- **No `summary` reference survives** anywhere in `internal/` or `src/endless/`
  outside the change files and the legacy `db.py` migrator — asserted by grep, so
  a missed reader cannot pass silently.
- **The UTF-8 truncation is fixed**: a first assistant response with a multi-byte
  character straddling byte 200 and no `.`/`!`/`?` in bytes 101–200 yields valid
  UTF-8. Go unit test, driven directly against `seedRecapIfEmpty`.
- The error/login **auto-hide still fires**: a first response starting
  `Not logged in` sets `hidden = 1`.
- A seeded placeholder (`recap_at IS NULL`) is NOT rendered by
  `session list --all`; a generated one is.
- `recap` resolves `ES-NNN`, a bare integer, a UUID prefix, and `E-NNN` (task →
  most-recent session with messages).
- A second call inside the watermark is served from cache and makes no model call.
- After `RECAP_STALE_TURNS` new **user** messages the cache reads stale.
- After exactly `RECAP_STALE_TURNS - 1` it does **not** — the boundary, asserted
  explicitly.
- New `tool_use` and `assistant` messages do **not** advance staleness; only
  `user` messages do.
- Input selection excludes `tool_use` and the boilerplate patterns, and over
  `RECAP_INPUT_BUDGET` applies the head+tail elision (assert on the assembled
  prompt, not on model output).
- **The budget is enforced by the query**: seed a session far over budget and
  assert the number of rows the query RETURNS is bounded — not merely that the
  assembled prompt is. A regression that filters in Python would pass a
  prompt-only assertion.
- The sliding split behaves: at ~1× budget the head/tail shares are even; at 10×
  the head is at its 0.15 floor.
- A boilerplate `user` message does NOT advance the staleness count, while a real
  one does.
- `--refresh` regenerates regardless of freshness.
- A missing `claude` produces a clear error and caches nothing.
- A session with no messages errors rather than calling the model.
- `session list --all` renders a fresh cached recap in a task-less row's Title
  cell, renders blank when stale or absent, and never invokes the model.
- The implementation routes through `internal_claude` — assert the source contains
  no direct `subprocess` invocation of `claude`, so a hand-rolled call (and E-1470
  with it) cannot creep back in.

Alongside the script: pytest for ref resolution, input assembly/elision, the
watermark boundary, and the failure paths.

## Docs

Add `session recap` to `docs/guide/appendix-a.md` (it is a human-run interactive
command) and reference it from the **Quieting a noisy status view** neighborhood
in `docs/guide/sessions.md`, where task-less sessions are already discussed.

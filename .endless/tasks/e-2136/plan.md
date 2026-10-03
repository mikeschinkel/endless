# Plan — the folio writer, then the two commands that prove it

Three phases. A is the format with no consumer; B and C are its first two
consumers, one detail-shaped and one list-shaped. B and C are independent of
each other and either may land first.

Every decision this plan rests on is in the analysis. Nothing here is open.

## Phase A — the Agent Folio Format writer, in Go

**New package `internal/folio`.** Writer only on the production path; the reader
is test-only (below).

- `Part{Type, Name, For, Seq, Complete, Purpose string; Body []byte}` and a
  `Write(w io.Writer, parts []Part) error` that emits, per part, the header
  block, a blank line, exactly `len(Body)` bytes, and a blank line before the
  next header.
- **`Length` is computed, never supplied.** The one invariant a caller must not
  be able to get wrong.
- **`@meta` is generated, always first**, carrying a row per part including
  itself: `name, for, seq, type, offset, length`. Offsets are from byte zero and
  point at the start of each part's header block. Generating it means computing
  the whole stream before writing the first byte, since `@meta`'s own length
  changes the offsets after it — write to a buffer, resolve, then emit.
- **Reserved namespace:** names beginning with `@` are the format's. The writer
  rejects an application part named `@…`.
- **Determinism:** byte-identical output for identical input, so golden files
  and diffs work. No map iteration in emission order.
- UTF-8 only. No transfer encodings, no header folding.

**Structured-part helpers.** `internal/folio` stays format-agnostic; two small
encoders sit beside it:

- YAML via `go.yaml.in/yaml/v4` for a flat scalar map and the `viewer` /
  `ownership` mappings.
- TOON via a pinned library for tabular parts.

Both are new Go dependencies. Go already carries ten, including
`BurntSushi/toml`, `goldmark` and `modernc.org/sqlite`, so neither is a posture
change. Pin exact versions.

**Test-only reader.** `internal/folio`'s test helper parses a stream and asserts
the invariants golden files cannot: every `Length` matches its body, every
`@meta` offset lands on a header block, and `@meta`'s row count equals the part
count. Not exported for production use and not reachable from the CLI.

## Phase B — `task show --agent` as a folio

Replaces the current key=value `--agent` rendering, which E-1504 renamed from
`--llm`.

**Parts, in order:**

1. `@meta` — generated.
2. `record`, `application/yaml` — the scalar map: `id`, `project` (name, not
   `project_id`), `type`, `phase`, `status`, `complexity`, `risk`, `parent`,
   `created`, `created_by`, `updated`, `landed`, `settled`.
3. `children`, `text/toon` — present only when the task has children. Columns
   `id,type,phase,status,title`, titles untruncated.
4. `links`, `text/toon` — present only when the task has relations. Columns
   `kind,target`.
5. One content part per non-empty content field, `text/markdown`, named for the
   field: `description`, `context`, `analysis`, `plan`, `outcome`, `notes`.
   Raw bytes, nothing escaped. **All of them, always** — this is the inlining
   the task exists for.

A task with only a description still gets the container: `@meta`, `record`,
`description`.

**Dropped from this view:** `children_count`, `children_by_type`, and the
`*_chars` sizes. With children a part and content inlined, a count is the row
count and a size is visible.

**Where it goes.** Go renders it; Python's `task show` shells `endless-go` for
the agent view and keeps rendering the human view itself. The human view does
not change.

## Phase C — `session status --agent`

**Parts, in order:**

1. `@meta` — generated.
2. `frame`, `application/yaml` — `focal`, and `viewer: {session, focus}`.
3. `rows`, `text/toon` — every row the data carries, including those the table
   omits. Columns: `id`, `project`, `type`, `phase`, `status`, `action`,
   `title`, `relation`, `is_focal`, `is_parent`, `is_from`, `is_session_focus`,
   `hidden`, `in_flight`, `landed`, `settled`, `has_plan`.
4. `ownership`, `text/toon` — rows only for tasks where ownership says
   something: `task,owner,elsewhere,duplicate,also_on`. A separate part rather
   than columns on `rows` because it is sparse; most rows have nothing to say.
5. `edges`, `text/toon` — `from,kind,to`, `kind` one of `blocks`, `precedes`,
   `conflicts`. Replaces per-row `blocked_by_n` / `blocks_n`.

**`--show-hidden` / `--only-hidden` do not apply.** They shape a drawing; the
agent view emits every row with `hidden` as a field. Passing either with
`--agent` is accepted and ignored, not refused — they are display flags whose
meaning is already satisfied.

**`--monitor --agent` is refused**, with a message naming `session status
--agent`. The agent view is a snapshot; a repainting view has no agent meaning.

## The one Go data change

`monitor.AnnotateSessionStatusOwnership` currently sets booleans. It gains the
owning session's id and the ids behind `duplicate_work`, and the internal
`session-status --json` exposes them. That is data in the internal JSON, which
Python and Go both already consume — not an agent renderer, so it does not
conflict with anything.

## Verification

- **Folio invariants**, via the test-only reader: `Length` equals body length
  for every part; every `@meta` offset lands on a header block; `@meta` row
  count equals part count; a part named `@foo` is rejected.
- **Determinism:** the same record rendered twice is byte-identical.
- **No escaping:** a task whose analysis contains a fenced code block, a line
  reading `Type: x`, and CRLF round-trips byte-for-byte out of its part. This is
  the property the format exists for.
- **Inlining:** `task show --agent` on a task with all six content fields emits
  six content parts and no pointer; on a task with one, three parts total.
- **Untruncated:** a title longer than any terminal width appears whole in both
  the `children` and `rows` parts.
- **TOON quoting:** a title containing a comma, and one containing a double
  quote, survive a round trip through the `children` part.
- **Vocabulary:** no agent-view field named `project_id`, `unsettled`,
  `unsettled_known`, `focused`, `owned_elsewhere`, `duplicate_work`,
  `children_count`, `children_by_type`, or `*_chars`.
- **session status:** a row hidden for the viewer still appears, carrying
  `hidden: true`; a task owned by another live session carries that session's id
  in the `ownership` part; `--monitor --agent` exits non-zero naming the
  alternative.
- **Human views unchanged:** golden output for `task show` and `session status`
  without `--agent` is untouched.
- `just test`, `just test-go`.

## What this plan deliberately leaves undone

Caller detection, `task meta`, the other list commands (E-2141), and a
user-facing folio reader. Without caller detection the agent view happens only
when something passes `--agent`, which the brief's own invariant calls wrong —
that gap is named in the analysis and is not closed here.

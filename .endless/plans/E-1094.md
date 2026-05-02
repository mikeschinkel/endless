# Decision: portable snapshot companion-file source_path

## Problem

The current snapshot companion JSON stores `source_path` as a full absolute path:

```json
{ "source_path": "/Users/mikeschinkel/.claude/plans/foo.md" }
```

This is machine-specific, leaks the username, and is meaningless on any other
machine — a hard blocker for committing snapshots cross-developer (E-1091).

## Decision

Replace `source_path` with a portable shape that distinguishes the two cases
`isPlanFile()` matches:

```json
{
  "source_kind": "harness" | "project",
  "source_name": "<basename for harness; project-relative path for project>"
}
```

- For **harness** plans (path under `~/.claude/plans/`), `source_name` is the
  basename (e.g., `e-971-layer-d-hashed-lightning.md`). The `~/.claude/plans/`
  prefix is implied by `source_kind=harness`.
- For **project** plans (path ending in `/plan.md`), `source_name` is the
  path relative to project root (e.g., `docs/plan.md` or `plan.md`).

`source_kind` is explicit even though it could be inferred from whether
`source_name` contains a slash, because explicit is more honest and forward-
compatible (other source kinds may appear).

## Why not just basename + assume `~/.claude/plans/`?

Because `isPlanFile` also matches `*/plan.md` files inside the project. A bare
basename would collapse every project plan to `plan.md`, losing which project
subdir it came from.

## Why not the other extreme — full structured location object?

YAGNI. Two source kinds today; both fit cleanly in `{kind, name}`. If a third
kind appears (e.g., a remote plan), add a third enum value.

## Implementation split

- **E-1095**: change writer (`snapshotPlanFile`) and readers (`plan_cmd.py`)
  to emit and consume the new shape. Keep a tolerant fallback for
  `source_path` during the transition. Rename `existingSnapshot` and any
  internal "sidecar" references in the touched code to "companion"
  terminology.
- **E-1096**: migration script to rewrite every existing companion JSON in
  `.endless/plans/snapshots/` from `source_path` to
  `source_kind`+`source_name`. Run once per project; idempotent.

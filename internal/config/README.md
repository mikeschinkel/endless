# Endless Config Merge Contract

This document describes the layered configuration semantics implemented by
the Go `internal/config` package. It is the canonical specification that
the Python config readers in `src/endless/` must match (see follow-up
task E-954).

## Layers

Endless config loads from two layers:

| Layer       | Path                                  | Created automatically? |
| ----------- | ------------------------------------- | ---------------------- |
| **CLI**     | `~/.config/endless/config.json`       | Yes, on first load     |
| **Project** | `<project-path>/.endless/config.json` | No                     |

Project layer takes precedence over CLI layer for layered fields.

## Field categories

Every field belongs to exactly one of three categories:

- **Global-only**  — only the CLI layer is allowed to set it. The project
  layer should leave it absent.
- **Project-only** — only the project layer is allowed to set it. The CLI
  layer should leave it absent.
- **Layered**      — both layers may set it. A merge rule decides the
  effective value.

For each field below, the row gives JSON name, type, category, and merge
rule. "Receiver" in merge rules means the project layer; "other" means
the CLI layer.

## Field reference

### Global-only

| JSON key        | Type                  | Merge rule (when both layers set it)                       |
| --------------- | --------------------- | ---------------------------------------------------------- |
| `roots`         | `string[]`            | Receiver wins on non-empty; otherwise inherit from other.  |
| `scan_interval` | `int` (seconds)       | Receiver wins on non-zero; otherwise inherit from other.   |
| `ignore`        | `string[]`            | Receiver wins on non-empty; otherwise inherit from other.  |
| `ownership`     | `map[string]string[]` | Receiver wins on non-empty; otherwise inherit from other.  |
| `node_id`       | `string`              | Receiver wins on non-empty; otherwise inherit from other.  |

These are not expected to appear in project files. The merge rules above
exist as a safety net: if a project file mistakenly defines one, the
project value wins, matching the general "receiver wins on non-empty"
pattern.

### Project-only

| JSON key       | Type        | Merge rule (when both layers set it)                       |
| -------------- | ----------- | ---------------------------------------------------------- |
| `name`         | `string`    | Receiver wins on non-empty; otherwise inherit from other.  |
| `label`        | `string`    | Receiver wins on non-empty; otherwise inherit from other.  |
| `description`  | `string`    | Receiver wins on non-empty; otherwise inherit from other.  |
| `language`     | `string`    | Receiver wins on non-empty; otherwise inherit from other.  |
| `status`       | `string`    | Receiver wins on non-empty; otherwise inherit from other.  |
| `dependencies` | `string[]`  | Receiver wins on non-empty; otherwise inherit from other.  |
| `documents`    | `object`    | `documents.rules`: receiver wins on non-empty.             |

These are not expected to appear in CLI files. Same safety-net pattern.

### Layered

| JSON key   | Type              | Merge rule                                                                                |
| ---------- | ----------------- | ----------------------------------------------------------------------------------------- |
| `tracking` | `string`          | Receiver wins when set to a non-empty string; empty string inherits from other.           |
| `checks`   | `map[string]bool` | Per-key merge: for each key, receiver value wins if present; otherwise inherit from other. |

#### `tracking`

Allowed values: `"enforce"`, `"track"`, `"off"`. Empty string means
"inherit". A final empty value after merge is the caller's signal to
apply a default ("enforce" for registered projects, "off" for anonymous).

#### `checks`

Per-key merge with optional per-key custom rules. The default rule for
each key is: project value wins when the key is present in the project
map; otherwise inherit the CLI value; if the key is absent in both
layers, the lookup helper falls back to the hardcoded default for that
check (see "Per-check defaults" below).

A future per-key rule (e.g. "OR logic for some specific check") would be
registered in Go via the `checkMergeRules` map in `merge.go`. As of this
writing no key has a custom rule. Python implementations should mirror
the same dispatch table once any custom rule is added.

## Per-check defaults

When a check key is absent in both layers, these hardcoded defaults
apply:

| Key                   | Default |
| --------------------- | ------- |
| `task_required`       | `true`  |
| `drift_detection`     | `false` |
| `decision_checkpoint` | `false` |
| `session_audit`       | `false` |
| (any other name)      | `true`  |

The `task_required` default is `true` for backwards compatibility with
the original PreToolUse session-required block. Other defined-but-default-
off keys ship disabled so deploying the binary does not change behavior
until a user opts in.

Unknown keys default to `true` so that future checks introduced by client
code (without a corresponding default) fail open rather than silently
disable.

## Empty / missing files

- **CLI file missing**: created automatically as `{}`. Equivalent to all
  fields unset.
- **Project file missing**: silently treated as "no project layer". Only
  the CLI layer applies.
- **CLI file present but malformed JSON**: load fails. Callers MUST
  fall back to safe defaults rather than blocking, matching Go behavior.
- **Project file present but malformed JSON**: load fails. Same.

## Implementation pointers

- Go schema: `internal/config/config.go` (`EndlessConfig` struct).
- Go merge: `internal/config/merge.go` (`(*EndlessConfig).Merge`).
- Go defaults: `internal/config/normalize.go` (`DefaultCheckEnabled`).
- Python readers needing migration (per E-954): `src/endless/config.py`,
  `src/endless/event_bridge.py`, `src/endless/register.py`,
  `src/endless/reconcile.py`.

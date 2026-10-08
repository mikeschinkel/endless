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
| `migrations`   | `object`    | `migrations.dirs`: receiver wins on non-empty.             |

These are not expected to appear in CLI files. Same safety-net pattern.

### Layered

| JSON key   | Type              | Merge rule                                                                                |
| ---------- | ----------------- | ----------------------------------------------------------------------------------------- |
| `tracking` | `string`          | Receiver wins when set to a non-empty string; empty string inherits from other.           |
| `checks`   | `map[string]bool` | Per-key merge: for each key, receiver value wins if present; otherwise inherit from other. |
| `tmux`     | `object`          | Merged PER FIELD, not wholesale — see `tmux` below.                                        |
| `auto_spawn` | `object`        | Split by field — see `auto_spawn` below.                                                   |
| `main_sync` | `object`         | Split by field — see `main_sync` below.                                                    |
| `fault_triage` | `object`      | Split by field — see `fault_triage` below.                                                 |
| `project_status` | `object`    | Per list and per attribute — see `project_status` below.                                   |

#### `tracking`

Allowed values: `"enforce"`, `"track"`, `"off"`. Empty string means
"inherit". A final empty value after merge is the caller's signal to
apply a default ("enforce" for registered projects, "off" for anonymous).

#### `tmux`

Multiplexer preferences.

| Field          | Type     | Meaning |
| -------------- | -------- | ------- |
| `session_name` | `string` | Go `text/template` naming the tmux session `endless project monitor --tmux` opens for a project. Default `{{project}}`. The session lives on the monitor's own tmux server (`tmux -L endless`). |

`{{project}}` is available as a function, so the setting reads the way you would
write it; `{{.Project}}` resolves to the same string. The rendered result is
folded to a legal tmux session name, so a template may hold spaces, dots or
slashes without you having to know tmux's rules.

```json
{ "tmux": { "session_name": "{{project}}-watch" } }
```

A template that fails to parse or renders to nothing falls back to the default
and warns on stderr: this is a preference, and a typo in one must not stop the
monitor from opening.

The session lives on its own tmux server, so a name cannot collide with a
session you keep on your usual one. Each launch adds a `projects` window to it.

Merged per field rather than wholesale, so a project that sets one tmux
preference does not silently blank the others it inherits from the CLI layer.

#### `project_status`

Display preferences for `endless project status` and `endless project monitor`.
`colors` sets each list's row colors as 256-color indexes: `bg` is the
background, `fg` the text. Leave a list or an attribute out to keep its
default; `-1` means none (the terminal's own color), which is how a list goes
without a background. Merged per list and per attribute.

| List     | Default          |
| -------- | ---------------- |
| `urgent` | `bg 1`, `fg 232` |
| `epics`  | `bg 2`, `fg 232` |
| `other`  | `bg 3`, `fg 232` |

Indexes 1–3 are your theme's own red, green and yellow, so the defaults follow
your palette.

```json
{ "project_status": { "colors": { "epics": { "bg": 236, "fg": -1 }, "other": { "bg": -1 } } } }
```

#### `auto_spawn`

The auto-spawn job (E-1814) starts a Claude session, unasked, on a task rated
low complexity and low risk. Its fields are split across the layers by who
decides them, so this object is neither project-only nor global-only as a
whole:

| Field      | Layer        | Type     | Meaning |
| ---------- | ------------ | -------- | ------- |
| `enabled`  | project only | `bool`   | Opts this project in. Default `false`; also the kill switch. |
| `cap`      | project only | `int`    | Most auto-spawned tasks outstanding (underway or unverified) at once. Default `3`. |
| `interval` | CLI only     | `string` | Job cadence, a Go duration. Default `5m`. At most one task is spawned per interval. |
| `target`   | CLI only     | `string` | tmux session the window opens in: `active` (default — the session of the most recently active attached client) or `monitor` (the session the job runs in). |
| `placement` | CLI only    | `string` | Where the window's tab lands in that session: `first`, `last` (default), `left` or `right` — the last two relative to the session's active window. The same positions `task spawn --to-first`/`--to-last`/`--to-left`/`--to-right` take. |

`enabled` and `cap` are **never inherited** from the CLI layer: a user-level
`enabled: true` does not opt any project in. They are read from the project
file alone (`config.LoadProject`), and a project with no file is off.

```json
{ "auto_spawn": { "enabled": true, "cap": 2 } }
```

#### `main_sync`

The main-sync job (E-2233) keeps a project's default branch in step with its
upstream: it fetches, fast-forwards main when only the remote moved, pushes when
only main moved, and records a fault — changing nothing — when both did. It
never runs `git pull`, so your `pull.rebase` and `pull.ff` settings have no
effect on it, and it never force-pushes.

| Field      | Layer        | Type     | Meaning |
| ---------- | ------------ | -------- | ------- |
| `enabled`  | project only | `bool`   | Opts this project in. Default `false`; also the kill switch. |
| `interval` | CLI only     | `string` | Job cadence, a Go duration. Default `5m`. |

`enabled` is **never inherited** from the CLI layer: the job publishes main to
its remote, and that is each project's decision. It is read from the project
file alone (`config.LoadProject`), and a project with no file is off.

```json
{ "main_sync": { "enabled": true } }
```

#### `fault_triage`

The fault-triage job (E-2272) routes each new error to a session that fixes it:
it messages the live session that raised it, resumes an ended one, or files and
spawns a bugfix task. See `docs/errors.md`, "Routing an error to a session that
fixes it".

| Field      | Layer        | Type     | Meaning |
| ---------- | ------------ | -------- | ------- |
| `enabled`  | project only | `bool`   | Opts this project in. Default `false`; also the kill switch. |
| `interval` | CLI only     | `string` | Job cadence, a Go duration. Default `1m`. |

`enabled` is **never inherited** from the CLI layer: the job starts sessions in
the project, and that is each project's decision. Opting in routes only errors
first seen afterwards.

```json
{ "fault_triage": { "enabled": true } }
```

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
| `decision_checkpoint` | `false` |
| `session_audit`       | `false` |
| `bash_git_writes`     | `true`  |
| (any other name)      | `true`  |

The `task_required` default is `true` for backwards compatibility with
the original PreToolUse session-required block. Other defined-but-default-
off keys ship disabled so deploying the binary does not change behavior
until a user opts in.

`bash_git_writes` (E-940) makes the PreToolUse write-target gate treat git
commands that rewrite a working tree (`restore`, `checkout`, `reset --hard`,
`stash pop`, `clean`, `apply`, `pull`, …) as writes to that working tree. It is
on trial; set it `false` to turn that recognition off with no rebuild.

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

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

## Opt-ins and the settings registry (E-2284)

An opt-in is a three-way switch, written as a string: `"on"`, `"off"` or
`"unset"`. A missing key is unset. Unset takes the setting's registered
default; `"off"` is an explicit decline, which `endless doctor` reads as "do not
suggest this again". The opt-ins are `auto_spawn.enabled`, `prime.enabled`,
`main_sync.enabled`, `fault_triage.enabled`, `git_hooks.report` and every key
of `checks`.

A JSON `true` or `false` at an opt-in key is refused when the file loads, by
`Normalize`, with the file, the key and the value to write instead
(`config.OptInError`). `endless-go settings migrate [FILE...]` rewrites those
legacy booleans in place, changing no other byte.

Two layers, after gomion's gomcfg / gompkg split:

- **File layer** — this package. `EndlessConfig` holds opt-ins as `OptInText`,
  the text as written.
- **Runtime layer** — the feature package's own type (`mainsyncjob.MainSync`,
  `autospawnjob.AutoSpawn`, `autospawnjob.Prime`, `triagejob.FaultTriage`,
  `monitor.Checks`), produced by its `Parse...` function from the file layer,
  holding `settings.OptIn`. Readers trust only that.

Each runtime type implements `settings.Provider`: its `Settings()` declares the
feature's keys (one per leaf key — the opt-in and its tuning keys) with the
file(s) each may live in, its kind, default, and for a suggestable opt-in the
rationale doctor shows. The owning package registers them in `init()`;
`endless-go` validates and freezes the registry at startup
(`settings.Initialize`), and a failure there stops every command.

Every leaf key of `EndlessConfig` is either registered or on the allowlist in
`cmd/endless-go/main_test.go`. The allowlist may only shrink (E-2303 registers
the rest): `TestEveryConfigKeyIsRegistered` fails on a new key that is neither,
and on an allowlist longer than its pinned size.

`endless-go settings list --json [--project DIR]` exports the registry with
each key's effective value and source file; Python reads registered keys only
through it.

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
| `checks`   | `map[string]opt-in` | Per-key merge: for each key, receiver value wins if set; otherwise inherit from other. |
| `tmux`     | `object`          | CLI layer only — see `tmux` below.                                                          |
| `auto_spawn` | `object`        | Split by field — see `auto_spawn` below.                                                   |
| `main_sync` | `object`         | Split by field — see `main_sync` below.                                                    |
| `fault_triage` | `object`      | Split by field — see `fault_triage` below.                                                 |
| `project_status` | `object`    | Per list and per attribute — see `project_status` below.                                   |
| `git_hooks` | `object`         | Per field: receiver wins when set (an opt-in's `"unset"` inherits) — see `GitHooks`.      |

#### `tracking`

Allowed values: `"enforce"`, `"track"`, `"off"`. Empty string means
"inherit". A final empty value after merge is the caller's signal to
apply a default ("enforce" for registered projects, "off" for anonymous).

#### `tmux`

Where Endless's own tmux servers live, and how a terminal is opened onto them
(`endless tmux start`, E-2240). **CLI layer only**: a value in a project's
config is ignored, because the socket fields decide which server every Endless
command reaches, and two projects must never reach two different servers.

| Field            | Type     | Meaning |
| ---------------- | -------- | ------- |
| `socket_dir`     | `string` | Directory both servers' sockets live in. Default `/tmp/endless-<uid>`. Created 0700; refused when owned by someone else or writable by others. |
| `console_server` | `string` | The console server's socket file name. Default `endless-console`. |
| `tasks_server`   | `string` | The task-session server's socket file name. Default `endless-tasks`. |
| `tasks_session`  | `string` | The session task windows open in. Default `tasks`. |
| `terminal`       | `string` | Command that opens a terminal window, with `{cmd}` where the attach command goes; run by `sh -c`. Default: detect the terminal, else print the attach commands. |

```json
{ "tmux": { "terminal": "ghostty -e {cmd}" } }
```

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
| `enabled`  | project only | opt-in   | Opts this project in. Default off; also the kill switch. |
| `cap`      | project only | `int`    | Most auto-spawned tasks outstanding (underway or unverified) at once. Default `3`. |
| `interval` | CLI only     | `string` | Job cadence, a Go duration. Default `5m`. At most one task is spawned per interval. |
| `target`   | CLI only     | `string` | tmux session the window opens in: `active` (default — the session of the most recently active attached client) or `monitor` (the session the job runs in). |
| `placement` | CLI only    | `string` | Where the window's tab lands in that session: `first`, `last` (default), `left` or `right` — the last two relative to the session's active window. The same positions `task spawn --to-first`/`--to-last`/`--to-left`/`--to-right` take. |

`enabled` and `cap` are **never inherited** from the CLI layer: a user-level
`enabled: true` does not opt any project in. They are read from the project
file alone (`config.LoadProject`), and a project with no file is off.

```json
{ "auto_spawn": { "enabled": "on", "cap": 2 } }
```

#### `main_sync`

The main-sync job (E-2233) keeps a project's default branch in step with its
upstream: it fetches, fast-forwards main when only the remote moved, pushes when
only main moved, and records a fault — changing nothing — when both did. It
never runs `git pull`, so your `pull.rebase` and `pull.ff` settings have no
effect on it, and it never force-pushes.

| Field      | Layer        | Type     | Meaning |
| ---------- | ------------ | -------- | ------- |
| `enabled`  | project only | opt-in   | Opts this project in. Default off; also the kill switch. |
| `interval` | CLI only     | `string` | Job cadence, a Go duration. Default `5m`. |

`enabled` is **never inherited** from the CLI layer: the job publishes main to
its remote, and that is each project's decision. It is read from the project
file alone (`config.LoadProject`), and a project with no file is off.

```json
{ "main_sync": { "enabled": "on" } }
```

#### `fault_triage`

The fault-triage job (E-2272) routes each new error to a session that fixes it:
it messages the live session that raised it, resumes an ended one, or files and
spawns a bugfix task. See `docs/errors.md`, "Routing an error to a session that
fixes it".

| Field      | Layer        | Type     | Meaning |
| ---------- | ------------ | -------- | ------- |
| `enabled`  | project only | opt-in   | Opts this project in. Default off; also the kill switch. |
| `interval` | CLI only     | `string` | Job cadence, a Go duration. Default `1m`. |

`enabled` is **never inherited** from the CLI layer: the job starts sessions in
the project, and that is each project's decision. Opting in routes only errors
first seen afterwards.

```json
{ "fault_triage": { "enabled": "on" } }
```

#### `checks`

Per-key merge with optional per-key custom rules. The default rule for
each key is: project value wins when the key is present in the project
map; otherwise inherit the CLI value; if the key is absent in both
layers, the reader (`monitor.Checks`) falls back to the registered default for that
check (see "Per-check defaults" below).

A future per-key rule (e.g. "OR logic for some specific check") would be
registered in Go via the `checkMergeRules` map in `merge.go`. As of this
writing no key has a custom rule. Python implementations should mirror
the same dispatch table once any custom rule is added.

## Per-check defaults

When a check key is unset in both layers, its registered default applies
(`monitor.Checks.Settings()`):

| Key                   | Default |
| --------------------- | ------- |
| `task_required`       | `on`    |
| `decision_checkpoint` | `off`   |
| `session_audit`       | `off`   |
| `bash_git_writes`     | `on`    |
| (any other name)      | `on`    |

Only `bash_git_writes` has a reader today. `task_required` defaults on for
backwards compatibility with the original PreToolUse session-required block;
the other defined keys ship off so deploying the binary does not change
behavior until a user opts in.

`bash_git_writes` (E-940) makes the PreToolUse write-target gate treat git
commands that rewrite a working tree (`restore`, `checkout`, `reset --hard`,
`stash pop`, `clean`, `apply`, `pull`, …) as writes to that working tree. It is
on trial; set it `"off"` to turn that recognition off with no rebuild.

Unknown keys default to on so that future checks introduced by client
code (without a registered default) fail open rather than silently
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
- Go defaults: each setting's registration (`internal/settings`; checks in `internal/monitor/checks.go`).
- Python readers needing migration (per E-954): `src/endless/config.py`,
  `src/endless/event_bridge.py`, `src/endless/register.py`,
  `src/endless/project_sync.py`.

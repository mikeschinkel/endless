# E-1367: Consolidate `endless-*` Go binaries into `endless-go` with subcommands

## Context

Endless currently ships **seven** standalone Go binaries from `cmd/endless-*/`:
`endless-event`, `endless-hook`, `endless-channel`, `endless-sandbox`,
`endless-serve`, `endless-tmux`, `endless-session-query`. Every one is its own
build target, install symlink, sandbox-wrapper entry, and subprocess lookup
site — so each binary multiplies the proliferation surface. The task's
description (filed before E-894 added `endless-session-query`) calls out
six; the audit confirms seven.

Consolidate the seven into **one `endless-go` binary** with one verb per
former binary, each preserving its existing internal verbs:

- `endless-go event emit|apply-change|reap-worktrees|backup|validate-db|rebuild-db`
- `endless-go hook prompt|claude|codex|recap`
- `endless-go channel` (MCP server; no verbs)
- `endless-go sandbox run|enter|init|bind|list|prune|destroy`
- `endless-go serve [port]`
- `endless-go tmux apply|status-line|active-id|show-menu`
- `endless-go session-query list-live|task-text`

Outcome: one build target, one install symlink, one bin-sandbox wrapper,
one subprocess lookup. The proliferation surface goes from 7× to 1×.

## Settled decisions (do not re-debate)

1. **Scope = 7 binaries.** Includes `endless-session-query` (added by
   E-894 after E-1367 was filed). The task description's "six" is stale.
2. **Clean break, no shims.** One symlink: `/usr/local/bin/endless-go`.
   No backward-compat aliases for the old names. All external references
   (settings.json files, Justfile recipes, bin-sandbox wrappers, Python
   `subprocess.run` sites) are updated in this task. Endless has one
   user (Mike) and no shipped product — a one-time settings sweep is
   acceptable; no migration logic.
3. **Binary name: `endless-go`** (per the task description).
4. **Test coverage gaps are out of scope.** `internal/monitor` and
   task-text query coverage in `just test-go` is owned by E-1473
   (currently `blocked_by` E-1367; unblocks when this lands).

## Cross-task constraints to preserve

Audit-verified state of the existing mains (one finding differs from the
prompt — flagged below):

- **E-1429 — `monitor.ConsumeDBContextFlag()`** is the gate that lets
  Go binaries open the DB inside an endless self-dev worktree.
  Currently called at the top of `event`, `serve`, and `session-query`
  mains. The dispatcher must invoke it for those three subcommands
  before per-subcommand logic runs. (The Python CLI translates
  `--db main|sandbox` into `--config-dir <dir>`; the flag arrives in
  `os.Args` for the consumer to strip.)
- **E-1450 (revised by audit) — `monitor.PinMainDB()`, not
  `ForceRealDB()`.** The prompt described `ForceRealDB()` at the top of
  `cmd/endless-hook/main.go`. Audit shows it has been **replaced by
  `PinMainDB()`** — see the comment block at
  `cmd/endless-hook/main.go:25-34`: ForceRealDB only redirected when
  XDG pointed at a sandbox, so a hook firing from a worktree cwd
  without sandbox injection hit the E-1429 gate and was refused.
  `PinMainDB()` pins to the real DB *and* satisfies the E-1429 gate
  in one call. Audit also found `endless-channel` and `endless-tmux`
  call `PinMainDB()` at top of main — channel events and tmux status
  reads must also always target the real DB, so this is correct and
  must be preserved. **No current main calls `ForceRealDB`.**
- **E-1470 — `if os.Getenv("ENDLESS_NO_HOOKS") == "true" { return }`**
  must run as the **very first** statement on the `hook` path, before
  `PinMainDB()` and before any DB work. It must NOT fire for any other
  subcommand — the gate is scoped to headless hook calls only.
- **E-1475 already landed in 0072b81 (2026-05-26)** — that commit
  added `*config.go_db_context_args()` threading to both
  `_reap_stale_worktrees` (`endless-event reap-worktrees`) and
  `_materialize_plan_file` (`endless-session-query task-text`). The
  threading stays; this task only renames the binary and prepends the
  subcommand verb (`endless-go event ...`, `endless-go session-query
  ...`). No separate E-1475 follow-up is owed by this work.

## DB-context init: per-subcommand contract

A tiny dispatcher table in `cmd/endless-go/main.go` selects which init
runs before the subcommand body. **Order matters.**

| Subcommand     | Pre-dispatch init                                                                 | Source main                              |
|----------------|------------------------------------------------------------------------------------|------------------------------------------|
| `hook`         | (1) early return if `ENDLESS_NO_HOOKS=true`, then (2) `monitor.PinMainDB()`        | `cmd/endless-hook/main.go:13-35`         |
| `channel`      | `monitor.PinMainDB()`                                                              | `cmd/endless-channel/main.go`            |
| `tmux`         | `monitor.PinMainDB()`                                                              | `cmd/endless-tmux/main.go`               |
| `event`        | `monitor.ConsumeDBContextFlag()`                                                   | `cmd/endless-event/main.go`              |
| `serve`        | `monitor.ConsumeDBContextFlag()`                                                   | `cmd/endless-serve/main.go`              |
| `session-query`| `monitor.ConsumeDBContextFlag()`                                                   | `cmd/endless-session-query/main.go`      |
| `sandbox`      | none                                                                               | `cmd/endless-sandbox/main.go`            |

Dispatch shape (illustrative; hand-rolled to match the existing per-main
style — none of the seven uses cobra/pflag, and adding one balloons the
change):

```go
// cmd/endless-go/main.go
func main() {
    if len(os.Args) < 2 { usage(); os.Exit(1) }
    sub := os.Args[1]
    rest := os.Args[2:]

    // E-1470: ENDLESS_NO_HOOKS short-circuit applies ONLY to `hook`.
    if sub == "hook" && os.Getenv("ENDLESS_NO_HOOKS") == "true" {
        return
    }

    // Per-subcommand DB-context init. Order matters; must precede
    // monitor.DB() inside the subcommand body.
    switch sub {
    case "hook", "channel", "tmux":
        monitor.PinMainDB()
    case "event", "serve", "session-query":
        monitor.ConsumeDBContextFlag()
    case "sandbox":
        // no DB-context init
    }

    // Dispatch.
    switch sub {
    case "event":         eventcmd.Run(rest)
    case "hook":          hookcmd.Run(rest)
    case "channel":       channelcmd.Run(rest)
    case "sandbox":       sandboxcmd.Run(rest)
    case "serve":         servecmd.Run(rest)
    case "tmux":          tmuxcmd.Run(rest)
    case "session-query": sessionquerycmd.Run(rest)
    default:              usage(); os.Exit(1)
    }
}
```

## Subcommand argv namespace

Each current binary already parses an internal verb. The unified
namespace just adds one outer level. What was `os.Args[1]` in each old
main becomes the first arg in the subcommand's `args []string` slice
(i.e. `os.Args[2:]` from the dispatcher's POV); the existing
per-package `switch args[0]` inside each is preserved verbatim.

| Old                                                | New                                                       |
|----------------------------------------------------|-----------------------------------------------------------|
| `endless-event emit|apply-change|reap-worktrees|...` | `endless-go event ...`                                  |
| `endless-hook prompt|claude|codex|recap ...`       | `endless-go hook prompt|claude|codex|recap ...`           |
| `endless-channel` (MCP server, no verbs)           | `endless-go channel`                                      |
| `endless-sandbox run|enter|init|bind|list|prune|destroy ...` | `endless-go sandbox ...`                        |
| `endless-serve [port]`                             | `endless-go serve [port]`                                 |
| `endless-tmux apply|status-line|active-id|show-menu` | `endless-go tmux ...`                                   |
| `endless-session-query list-live|task-text ...`    | `endless-go session-query ...`                            |

## Implementation sequencing

All work happens in `.endless/worktrees/e-1367/`.

### Phase 1 — Port each binary into an `internal/*cmd` package

For each `cmd/endless-X/main.go`, create `internal/<X>cmd/` exposing
`Run(args []string)` (or `Run(args []string) error` where the current
main returns one). Move all per-binary support files with it:

- `cmd/endless-hook/{claude,prompt,recap,codex}.go` → `internal/hookcmd/`
- `cmd/endless-sandbox/{run,enter,init,bind,list,prune,destroy,sandbox}.go` → `internal/sandboxcmd/`
- `cmd/endless-event/*.go` (incl. emit/apply-change/etc. handlers) → `internal/eventcmd/`
- `cmd/endless-channel/main.go` body → `internal/channelcmd/`
- `cmd/endless-serve/main.go` body → `internal/servecmd/`
- `cmd/endless-tmux/main.go` body → `internal/tmuxcmd/`
- `cmd/endless-session-query/main.go` body → `internal/sessionquerycmd/`

Naming: `internal/*cmd` keeps the import direction clean
(`cmd/endless-go → internal/*cmd → internal/<primitive>`). Existing
primitive packages — `internal/monitor`, `internal/events`,
`internal/kairos`, `internal/web` — are untouched; the new `*cmd`
packages are thin shells that delegate to them, exactly like the
current mains do.

**Watch-out: `endless-channel`'s package-level `init()`.** The audit
found only `cmd/endless-channel/main.go` has a top-level `init()`
function (it opens `log/channel.log`). Go runs that `init()` whenever
the package is imported — so if `cmd/endless-go/main.go` imports
`internal/channelcmd`, the log file opens for **every** `endless-go`
invocation regardless of subcommand. Fix during the port: move the
log-file open out of `init()` and into `channelcmd.Run()`. Verify in
Phase 6 that other subcommands don't create `log/channel.log`.

### Phase 2 — Add `cmd/endless-go/main.go` dispatcher

Per the dispatch sketch above. No global flags at the dispatcher level
— each flag stays inside its subcommand body. `--config-dir`
(consumed by `ConsumeDBContextFlag`) arrives in `os.Args` after the
subcommand name; `ConsumeDBContextFlag` scans and strips the flag from
`os.Args`, which still works because the subcommand reads what's left.

`endless-go --help` and `endless-go <subcommand> --help` print usage.

### Phase 3 — Justfile updates

All in `/Users/mikeschinkel/Projects/endless/Justfile`:

- **`build`** (lines 57-66) — collapse seven `go build` lines into one:
  `go build -o bin/endless-go ./cmd/endless-go`.
- **`install`** (lines 74-83) — collapse to one `ln -sfn` for
  `/usr/local/bin/endless-go`. Add an explicit cleanup line:
  `rm -f /usr/local/bin/endless-{serve,hook,channel,event,sandbox,tmux,session-query}`
  (clean-break per Decision 2).
- **`go`** (lines 369-374) — collapse to single build; this recipe was
  also missing two of the seven binaries pre-consolidation, so this
  cleanly fixes that too.
- **`land`** (line 113) — `endless-tmux active-id` →
  `endless-go tmux active-id`.
- **`dev-sandbox-init`** (lines 332-333) — `endless-sandbox init`/`bind`
  → `endless-go sandbox init`/`bind`.
- **`claude-settings-init`** (lines 260, 268-273) — references to
  `bin/endless-hook` → `bin/endless-go`; invocation becomes
  `endless-go hook claude` (matches the global settings change in
  Phase 4).
- **`dev`** (line 50) — templ's `--cmd="go run ./cmd/endless-serve"`
  becomes `--cmd="go run ./cmd/endless-go serve"` (templ re-runs the
  whole command verbatim, so the `serve` arg must be in the string).
- **`kill`** (line 382) — `pkill endless-serve` →
  `pkill -f 'endless-go serve'` (argv match is needed; plain
  `pkill endless-go` would also kill the dispatcher itself).

### Phase 4 — External-caller updates (clean break)

All sites switch atomically with this task — no shims, no aliases.

**Global `~/.claude/settings.json` — 5 entries:**

- PostToolUse (~line 33)
- UserPromptSubmit (~line 69)
- Stop (~line 89)
- SessionStart (~line 100)
- SessionEnd (~line 111)

Rewrite `/usr/local/bin/endless-hook claude` →
`/usr/local/bin/endless-go hook claude` (or `endless-go hook prompt` /
`endless-go hook recap` matching the existing per-entry verb).

**Repo `.claude/settings.json` (committed in main):** Same five
rewrites. Rides into main via `worktree land`.

**`bin-sandbox/` wrapper template** at
`cmd/endless-sandbox/bind.go:17-25` (after rename: `internal/sandboxcmd/bind.go`):

- Collapse `goBinaries = []string{...}` to a single entry `"endless-go"`.
- `writeWrappers()` (lines 102-112) now writes one Go wrapper instead
  of seven. The Python wrapper is unchanged.
- `goWrapperTarget()` (lines 117-120) resolves
  `<worktree>/bin/endless-go`. Wrapper body (`exec <target> "$@"`)
  passes argv through unchanged, so subcommand parsing works inside the
  wrapper.

**Python `subprocess.run` / `Popen` call sites in `src/endless/`:**

| File:line                                    | Old                                                      | New                                          |
|----------------------------------------------|-----------------------------------------------------------|----------------------------------------------|
| `event_bridge.py:126,155,172,180,200,207`    | `endless-event emit\|apply-change\|reap-worktrees ...`    | `endless-go event ...`                       |
| `cli.py:269,281,302`                         | `endless-serve` (Popen for watch mode)                    | `endless-go serve`                           |
| `session_cmd.py:1063`                        | `endless-session-query list-live`                         | `endless-go session-query list-live`         |
| `worktree_cmd.py:738`                        | `endless-event reap-worktrees`                            | `endless-go event reap-worktrees`            |
| `worktree_cmd.py:1192,1205-1207` (`_maybe_auto_sandbox_bind`) | `endless-sandbox init\|bind ...`         | `endless-go sandbox init\|bind ...`          |
| `worktree_cmd.py` `_materialize_plan_file`   | `endless-session-query task-text ...`                     | `endless-go session-query task-text ...`     |
| `tmux_cmd.py:18,34`                          | `endless-tmux apply`                                      | `endless-go tmux apply`                      |
| `setup.py:25,350,382,390,437`                | config checks/repairs referencing `endless-hook`/`endless-channel` | rewrite to `endless-go hook`/`channel` |

`setup.py` notes: it currently validates/repairs the user's
settings.json against the old `endless-hook` shape. Approach: this
task does a **one-shot rewrite** of `setup.py`'s validation rules and
the user's settings (as part of Phase 4); `setup.py` going forward
validates only the new `endless-go hook ...` shape. Mike is the only
user — no need for setup.py to know both shapes.

### Phase 5 — Delete the old `cmd/endless-*/` directories

After Phase 4 (callers all updated, build green, `just install` green):

- Delete `cmd/endless-event/`, `cmd/endless-hook/`, `cmd/endless-channel/`,
  `cmd/endless-sandbox/`, `cmd/endless-serve/`, `cmd/endless-tmux/`,
  `cmd/endless-session-query/`.
- Confirm `cmd/` contains only `cmd/endless-go/`.

### Phase 6 — Rebuild and reinstall

From the **main checkout** (per "never run `just install` from a
worktree"): `just install` (the recipe handles the full chain — build,
symlink, `uv tool install -e .`). Confirms `/usr/local/bin/endless-go`
exists and the seven old symlinks are gone.

## Verification

Run from a fresh shell after Phase 6, in order:

1. **Hook fires from a live Claude pane.** Start a Claude session in
   tmux; trigger a tool use; confirm the hook process command line
   shows `endless-go hook claude` (`ps aux | grep endless-go`), an
   event row appears in the DB, and no errors appear in the project's
   Claude log dir.
2. **`ENDLESS_NO_HOOKS=true` short-circuits.** Pipe `/dev/null` to
   `ENDLESS_NO_HOOKS=true endless-go hook claude`; should return 0
   with zero DB writes (compare event count before/after).
3. **`just land` still works.** Inside a tmux pane on a claimed task,
   `just land` (no arg) derives the task ID via
   `endless-go tmux active-id` and lands successfully.
4. **Sandbox routing still works.** From a self-dev worktree:
   `./bin-sandbox/endless-go event emit ...` writes to the sandbox
   DB. From the main checkout (no wrapper): `endless-go event emit
   ...` writes to main.
5. **`_materialize_plan_file` still works at claim time.** In a fresh
   worktree, `endless task claim <small-open-task-id>` causes the
   plan file to appear in the worktree — the path that calls
   `endless-go session-query task-text`.
6. **`endless serve` Popen path still launches.** The Python CLI watch
   mode opens the dashboard at `http://localhost:8484`.
7. **MCP channel still pairs.** `endless-go channel beacon` in one
   session, `endless-go channel connect` in another, send/receive a
   message.
8. **Tmux status line still updates.** `endless-go tmux apply` followed
   by visual inspection.
9. **`log/channel.log` is NOT created by non-channel subcommands.**
   Delete the file, run `endless-go event emit ...` (or any non-channel
   subcommand), confirm it doesn't reappear. Then run `endless-go
   channel beacon`, confirm it does. Validates the Phase-1 init() move.

Record observed results in the verify-status message per CLAUDE.md
"Verification Before Done" + "How-to-test on verify".

## Out of scope

- **`internal/monitor` and task-text query test coverage.** Owned by
  E-1473 (currently `blocked_by` E-1367).
<!-- E-1475's --db/--config-dir threading fix landed in 0072b81 before
this task started; it is preserved through the rename, not "out of
scope". -->

## Critical files

Source for the port:

- `cmd/endless-event/` (event handlers + main)
- `cmd/endless-hook/main.go` + `claude.go` + `prompt.go` + `recap.go` + `codex.go`
- `cmd/endless-channel/main.go` (incl. the `init()` to relocate)
- `cmd/endless-sandbox/` (incl. `bind.go:17-25, 102-120` — wrapper template)
- `cmd/endless-serve/main.go`
- `cmd/endless-tmux/main.go`
- `cmd/endless-session-query/main.go`

Build / install / dev wiring:

- `/Users/mikeschinkel/Projects/endless/Justfile:46-83, 105-157, 260-273, 332-333, 369-374, 382`

Claude hook configuration:

- `/Users/mikeschinkel/Projects/endless/.claude/settings.json` (repo, committed)
- `/Users/mikeschinkel/.claude/settings.json` (global, 5 entries)

Python subprocess sites:

- `src/endless/event_bridge.py:126,155,172,180,200,207`
- `src/endless/cli.py:269,281,302`
- `src/endless/session_cmd.py:1063`
- `src/endless/worktree_cmd.py:738, 1192, 1205-1207` + `_materialize_plan_file`
- `src/endless/tmux_cmd.py:18,34`
- `src/endless/setup.py:25, 350, 382, 390, 437`

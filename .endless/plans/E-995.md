# E-995: Sandbox/Fixture Pattern for Tests That Mutate State

## Context

Tests and verification work that mutate Endless state must run against an isolated sandbox, never against the live system. Today there is no first-class sandbox primitive. E-787 proved this matters: I bypassed event-sourcing with raw SQL during E-979 cleanup (declined as E-993), then hand-rolled a partial sandbox in `tests/conftest.py` (`XDG_CONFIG_HOME` env, `PATH` prepend, `_seed_project_at_cwd` helper). E-787's Phase 5 manual sanity used yet another bash-only `.test-isolated/` setup. None of these are reusable; each session reinvents.

This plan establishes a first-class `endless sandbox` mechanism with three layered guarantees: env-var isolation as the foundation (cheap, default), opt-in deep clone of live state (for migration / sanity work), and OS-level enforcement via Go 1.24's `os.Root` (paranoid mode, kernel-level write barrier on prod paths). A single Python `Sandbox` class wraps the same machinery as the CLI for use in pytest fixtures.

This plan is general-purpose — sandboxes isolate "whatever the current user's endless install is." It's not specialized for the endless-developing-endless workflow, even though that's the immediate driver.

## Decisions locked during planning conversation

These get filed as Endless decisions on plan exit (via `endless decision add --decides E-995 ...`):

1. **Layered isolation model.** Foundation (#1) is `XDG_CONFIG_HOME`-based env-var redirection — Go honors it natively, Python gained support in E-1020. Opt-in (#2) deep-clone copies the user's DB plus per-project state with paths rewritten. Opt-in (#3) OS-level guard uses Go 1.24's `os.Root` to make writes outside the sandbox dir fail at the syscall level. Each layer composes; none replaces another.

2. **`--clone` is deep-clone.** Copies the DB *and* per-project `.endless/events/`, `.endless/plans/`, etc., and rewrites `projects.path` rows so events written in the sandbox land in sandboxed locations rather than the real project dirs. Cost: ~200-500MB and ~500ms-2s typical; safety justifies it. `--clone` is the only provisioning flag — no `--clone-prod`, no other variants. Without `--clone`, sandbox starts empty.

3. **`endless sandbox` command suite.** `run`, `enter`, `list`, `prune`, `destroy`. No `create` — sandboxes are created implicitly by `run`/`enter`.

4. **Default = ephemeral.** No flags → sandbox lives only for the duration of `run`'s command. Auto-destroyed at exit (via `defer`/`trap`). `--keep` makes it persistent; `--name N` gives it a stable name. Ephemerals get random names internally.

5. **`enter` opens a subshell.** Not env-var modification of the current shell. Cleaner exit semantics; `exit` always works. Speed cost is negligible.

6. **Storage: `~/.cache/endless/sandboxes/<name>/`** (XDG_CACHE_HOME-aware). Out of project dirs (sandbox is endless-config-scoped, not project-scoped).

7. **Crashed sandboxes survive for inspection.** Detected via PID liveness check on `creator_pid` in `.sandbox-meta.json`. `endless sandbox enter <name>` works on orphans for debugging.

8. **`prune` semantics.** Default: orphaned ephemerals only. `--older-than DURATION` filter, minimum 24h enforced (any value below errors with `"minimum --older-than is 24h"`). No `--all` flag — if you want everything gone, `rm -rf ~/.cache/endless/sandboxes/`. Outputs scope being addressed before acting (e.g., `"Pruning 3 orphaned ephemeral sandboxes older than 24h: ..."`).

9. **Always-on noisy startup warning.** When N>0 orphans exist, `endless` prints a one-line warning at startup with explicit disable instructions. Default ON. Disabled via `endless config set sandbox.startup_warning false`. Visibility > temporary annoyance — silent options are invisible options.

10. **pytest fixture API: object + methods.** `sb.run("task add ...")`, `sb.db` (sandboxed connection for assertions), `sb.env` (env vars dict for further subprocesses), `sb.path` (sandbox dir). Wrapped in a context manager; pytest fixture yields it.

11. **Binary-selection helper shared with E-1039.** `_resolve_endless_bin_dir()` (or equivalent) returns the right `bin/` for subprocess calls, detecting worktree-local builds and falling back to PATH. E-995 uses it to set PATH inside `sandbox run`. E-1039 later refactors `event_bridge.emit_event` and other call sites to use the same helper. Shared logic, two consumers — no conflict, clean delegation.

12. **Enforcement layering: `os.Root` + Python app-level guard.** Go binaries open everything via `os.OpenRoot(sandbox_dir)`; out-of-sandbox writes fail at the kernel. Python (which still opens SQLite directly) checks `ENDLESS_SANDBOX` env var and refuses to open paths outside. Once E-894 moves all SQL to Go, the Python guard becomes vestigial.

## Implementation

### Phase 1 — Go core: `endless-sandbox` binary

New binary `cmd/endless-sandbox/main.go`. Subcommands: `create`, `destroy`, `list`, `prune`, `run`, `enter`. (`endless sandbox <subcommand>` in Python dispatches to this.)

Core operations:
- `create` (internal, called by `run`/`enter`): provisions `~/.cache/endless/sandboxes/<name>/`, writes `.sandbox-meta.json` (`created_at`, `mode`, `creator_pid`, `name`).
- `--clone`: deep-copies the user's DB to `<sandbox>/endless/endless.db` and walks the `projects` table. For each project, copies `<project_root>/.endless/` to `<sandbox>/projects/<project_name>/.endless/`; updates the sandbox DB's `projects.path` to point at the sandboxed location.
- Uses `os.OpenRoot(sandbox_dir)` for all file operations. Out-of-root writes return EACCES.

Cleanup:
- `run` registers a `defer` that destroys the sandbox if `--keep` not set.
- Crash-resistant cleanup: trap on EXIT/INT/TERM via signal handler.
- Crashed runs leave the sandbox; orphan detection in `list`/`prune` finds them via dead `creator_pid`.

### Phase 2 — Python CLI integration

New `src/endless/sandbox_cmd.py` with `endless sandbox` Click group. Subcommands shell out to `endless-sandbox` binary.

App-level guard in `src/endless/db.py`:
```python
def _enforce_sandbox_boundary(path: Path):
    sandbox = os.environ.get("ENDLESS_SANDBOX")
    if sandbox and not str(path).startswith(sandbox):
        raise RuntimeError(
            f"sandbox boundary violation: refusing to open {path} outside {sandbox}"
        )
```
Called from `get_db()` and any other path-opening site in Python.

### Phase 3 — Binary-selection helper (delegates with E-1039)

New `src/endless/binary_resolution.py` (or a module under existing structure):
```python
def resolve_endless_bin_dir() -> Path:
    """Return the bin/ containing endless-* binaries to use for subprocess calls.
    Detects worktree-local bin/, else falls back to first PATH match."""
```
Used by `endless sandbox run` to set PATH for the launched command. E-1039 (separate task) will refactor `event_bridge.emit_event` and any other `shutil.which("endless-event")` site to use this same helper.

### Phase 4 — pytest fixture and `Sandbox` class

New `src/endless/testing/sandbox.py`:
```python
class Sandbox:
    def __init__(self, *, clone: bool = False, name: str | None = None): ...
    def __enter__(self) -> "Sandbox": ...   # creates sandbox dir, sets env
    def __exit__(self, *exc): ...            # destroys (unless name set)
    def run(self, cmd: str, *, check: bool = True) -> subprocess.CompletedProcess: ...
    @property
    def db(self) -> sqlite3.Connection: ...  # connection to sandbox DB
    @property
    def env(self) -> dict[str, str]: ...     # env vars to pass to subprocesses
    @property
    def path(self) -> Path: ...              # sandbox dir
```

Update `tests/conftest.py`:
```python
@pytest.fixture
def sandbox(tmp_path):
    with Sandbox() as sb:
        yield sb
```

Migrate existing tests opportunistically (out of scope for this plan; file as separate cleanup task).

### Phase 5 — Startup orphan warning

Hook in `endless` CLI entry point (`src/endless/cli.py:main`). Reads sandbox dir, counts orphans (ephemerals whose `creator_pid` is dead). If >0, prints to stderr:
```
warning: 3 orphaned sandboxes (run 'endless sandbox prune' to clean,
or 'endless config set sandbox.startup_warning false' to silence).
```
Disabled via `endless config set sandbox.startup_warning false`.

### Phase 6 — Documentation

- `docs/sandbox.md`: usage examples (devs, agents, pytest).
- Update `CLAUDE.md` to direct sessions toward `endless sandbox` for any test/sanity work that mutates state.

## Critical files

**New:**
- `cmd/endless-sandbox/main.go` — Go binary (Phase 1)
- `src/endless/sandbox_cmd.py` — Python CLI integration (Phase 2)
- `src/endless/binary_resolution.py` — shared bin-dir helper (Phase 3)
- `src/endless/testing/sandbox.py` — Python `Sandbox` class for tests (Phase 4)
- `docs/sandbox.md` — user-facing docs (Phase 6)

**Modified:**
- `src/endless/db.py` — add `_enforce_sandbox_boundary` guard
- `src/endless/cli.py` — register sandbox group; add startup orphan warning
- `src/endless/event_bridge.py` — (later, via E-1039) use `resolve_endless_bin_dir` helper
- `tests/conftest.py` — add `sandbox` fixture
- `CLAUDE.md` — direct sessions to use `endless sandbox`

## Reusable existing helpers

- `monitor.ConfigDir()` / `monitor.DBPath()` (`internal/monitor/db.go`) — already XDG-aware; sandbox sets XDG_CONFIG_HOME to redirect.
- `config._config_root()` (`src/endless/config.py:14`, from E-1020) — Python equivalent.
- pytest `monkeypatch` fixture — used in conftest to set env without leaking across tests.
- `tests/conftest.py:isolated_env` — current partial isolation; will be deprecated once `sandbox` fixture lands.

## Decisions to record on plan exit

File via `endless decision add --decides E-995 "..."`:
- D1: Layered isolation model (env-var foundation + opt-in deep clone + opt-in OS-level guard).
- D2: Binary-selection helper shared between E-995 and E-1039 (delegation pattern, no duplication).
- D3: Always-on noisy startup warning when orphans exist (visibility > annoyance).
- D4: `prune` semantics — orphans only by default, no `--all`, 24h minimum on `--older-than`.
- D5: pytest fixture API shape — `Sandbox` object with `run`/`db`/`env`/`path` members.
- D6: `--clone` is deep (DB + per-project state + path rewrites); no `--clone-prod`, no other variants.
- D7: `enter` opens a subshell (not current-shell env modification).

## Tasks to file/update on plan exit

**New:**
- "Implement endless-sandbox Go binary (E-995 Phase 1)" — child of E-995
- "Wire endless sandbox CLI group in Python (E-995 Phase 2)" — child of E-995
- "Add binary-selection helper (E-995 Phase 3, shared with E-1039)" — child of E-995
- "Implement pytest Sandbox class and fixture (E-995 Phase 4)" — child of E-995
- "Add startup orphan warning (E-995 Phase 5)" — child of E-995
- "Document endless sandbox usage (E-995 Phase 6)" — child of E-995
- "Migrate existing pytest tests to sandbox fixture" — child of E-995, phase=later

**Update existing:**
- E-1039 (`Add worktree-local binary preference to endless CLI`) — note delegation to E-995 Phase 3's helper; E-1039 becomes "extend the helper's caller set to include event_bridge and other subprocess sites."
- E-1043, E-1044, E-1045, E-1046 — already children of E-995; map them to phases above and either close as covered-by-Phase-N, or keep as discoverability aids and link to the implementing phase task.

## Verification (end-to-end)

1. **Unit isolation:** `endless sandbox run -- task add "Foo"` writes to sandbox DB; assert `~/.config/endless/endless.db` mtime unchanged via stat (still brittle — better via Phase 1's `os.Root` write-failure path).
2. **Deep-clone correctness:** `endless sandbox run --clone -- task list` shows the same tasks as `endless task list` (without sandbox) but all writes during the sandbox session land in sandbox locations.
3. **Os.Root enforcement:** test that any code path attempting to open a prod path during a sandbox run fails with EACCES at syscall level (not just app-level rejection).
4. **Crash inspection:** kill `endless sandbox run` mid-flight; `endless sandbox list` shows orphan; `endless sandbox enter <orphan>` re-attaches; `endless sandbox prune --older-than 0d` errors with `"minimum --older-than is 24h"`; default `prune` after 24h cleans it.
5. **Startup warning:** with N orphans, `endless task list` prints one-line warning; with `sandbox.startup_warning=false`, silent.
6. **pytest:** existing tests migrated to use `sandbox` fixture pass with no functional changes; `seeded_project_at_cwd` workaround can be removed.
7. **No prod leak:** wrap E-787's Phase 5 sanity script in `endless sandbox run --clone` and verify no writes to `~/.config/endless/endless.db` (via os.Root EACCES, not just mtime).

## Out of scope (file separately if needed)

- **Migrating all existing pytest tests** to the new fixture (gradual, opportunistic).
- **Web UI integration** — sandboxes are CLI/test-time concern for v1.
- **Multi-user shared sandboxes** — every sandbox is single-user.
- **In-memory SQLite provisioning mode** — disk-backed sandbox is fast enough; inspection of crashed ones requires disk anyway.
- **Network/process isolation** — only filesystem isolation in scope.
- **E-894** ("Move task display reads from Python to Go") — independent but synergistic; once it lands, the Python app-level guard in Phase 2 becomes vestigial.

## Notes for future planning

- E-1041 (rebuild-db FK failures from pre-E-808 legacy tasks) is independent of E-995 but commonly hit when sandboxes run rebuild-db against `--clone`d state. Worth scheduling near the same time so Phase 5/integration testing isn't blocked by FK noise.
- E-1042 (document `ENDLESS_AUTO_MIGRATE`) overlaps with Phase 6 documentation — fold into the same docs PR.
- E-1040 (block cross-session worktree migration from wiping uncommitted main) is unrelated but came from the same E-787 thread; mention in CLAUDE.md update if scope allows.

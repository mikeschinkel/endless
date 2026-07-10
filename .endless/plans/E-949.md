# Plan: Refactor Endless Config Loading onto go-cfgstore

## Context

While testing E-917 (drift detection), Mike set `"checks": {"drift_detection": true}` in `~/.config/endless/config.json` (the global config). The check defaulted to OFF and the feature appeared dead because my code in `internal/monitor/checks.go` reads only `<project>/.endless/config.json` (per-project). Time was wasted debugging a layering mismatch the codebase should never have allowed.

The deeper problem: I copied the existing precedent set by `monitor.GetTrackingMode` at `internal/monitor/session.go:305`. Both functions reimplement config layering ad hoc with raw `os.ReadFile` + `json.Unmarshal`, ignoring Mike's house package `go-cfgstore` which exists for exactly this purpose, plus `go-doterr` for errors and `go-dt` for paths. So this task replaces *both* bespoke readers, not just the new one.

The proximate win: setting any check or tracking value in either layer works with predictable precedence. The structural win: Endless config moves onto house standards, future config additions follow one pattern, and the merge semantics for every field are explicitly documented instead of accidentally inferred from whichever reader you happen to be looking at.

## Scope

**In scope:**
- New `internal/config/` package using `go-cfgstore`, `go-doterr`, `go-dt`.
- Refactor `monitor.GetTrackingMode` and `monitor.IsCheckEnabled` to use it.
- Field-by-field merge semantics design and documentation (the load-bearing design work).
- End-to-end test that the original `drift_detection` bug is fixed and per-project override still works.

**Out of scope (separate follow-up tasks, not blockers):**
- **Python config readers.** `src/endless/config.py`, `event_bridge.py`, `register.py`, `reconcile.py`, plus several tests, all read the same JSON files directly. Python cannot use `go-cfgstore`. They keep working in this task but will diverge from Go semantics until a follow-up either reimplements matching semantics in Python or moves Python to call a Go shim. Documented as a known gap; file follow-up task.
- **`ConfigDir()` helpers** in `internal/monitor/db.go:20`, `cmd/endless-hook/claude.go:20`, `cmd/endless-channel/main.go:25`. These resolve `~/.config/endless/` for log/db file locations, not for reading user settings. Out of scope here; can migrate to `go-cfgstore`'s `CLIConfigDir()` later.
- **Schema versioning** (`RootConfigV1`) per `go-configuration.md` guidance. Not needed until the schema actually changes.

## Field Inventory and Proposed Merge Semantics

The real design work. Mike flagged that every property needs explicit merge rules. Below is the proposal; the open questions list which calls need confirmation before implementation.

### Global-only fields (`~/.config/endless/config.json`)
No per-project analog; project layer cannot define these.

| Field | Type | Notes |
|---|---|---|
| `roots` | `[]string` | Directories Endless scans for projects. Inherently cross-project. |
| `scan_interval` | `int` | Seconds. Cross-project scheduler setting. |
| `ignore` | `[]string` | Paths/projects to skip during scan. Cross-project. |
| `ownership` | `map[string][]string` | GitHub org/user patterns for ownership detection. Cross-project. |
| `node_id` | `string` | Unique identifier for this machine in event-sourced sync. Per-machine. |

### Per-project-only fields (`<proj>/.endless/config.json`)
No global analog; global layer cannot define these.

| Field | Type | Notes |
|---|---|---|
| `name` | `string` | Project identifier. |
| `label` | `string` | Display name. |
| `description` | `string` | |
| `language` | `string` | Primary language. |
| `status` | `string` | active, paused, archived, idea. |
| `dependencies` | `[]string` | Other Endless projects this one depends on. |
| `documents` | `object` | Sub-keys like `rules: []`. |

### Layered fields (project overrides global where present)

| Field | Type | Merge rule |
|---|---|---|
| `tracking` | `"enforce" \| "track" \| "off"` | **Layered.** Global may set a default; project overrides only if explicitly set. (Resolved 2026-04-28.) |
| `checks` | `map[string]bool` | **Per-key merge with per-key dispatch.** Each key can have its own merge rule. Default rule: project value overrides global value for that key only; unmentioned keys inherit from global. Implementation must allow registering custom per-key merge logic for keys that need it. (Resolved 2026-04-28.) |

## Resolved Design Questions (2026-04-28)

1. **`checks` merge granularity.** Granular per-key merge. Each key can potentially have its own merge requirements, so the merge implementation must dispatch per-key rather than apply one uniform rule across the map. Default rule for keys without a custom rule: project wins if set, else inherit global, else fall back to `defaultCheckEnabled(name)`.
2. **`tracking` layerability.** Layerable. Global may set a default; project overrides only when explicitly set. Empty/unset on the project side means inherit, not "off".
3. **Additional layerable fields.** None planned at this time. New layerable fields will be added by extending the `EndlessConfig` struct and its `Merge()` method when the need arises.

## Recommended Approach

### Phase 1: Bootstrap (no behavior change yet)
- Add `github.com/mikeschinkel/go-cfgstore` (and `go-doterr`, `go-dt` if not already pulled in transitively) to `go.mod`.
- Create `internal/config/` with: `config.go` (the `EndlessConfig` struct + `RootConfig()` marker), `merge.go` (per-field `Merge()`, including per-key dispatch for `checks`), `normalize.go` (`Normalize()` with defaults), `errors.go` (`doterr` sentinels), `load.go` (`Load(projectPath)` entrypoint wrapping `cfgstore.LoadDefaultConfig[EndlessConfig, *EndlessConfig]`).
- Define the `EndlessConfig` struct with EVERY current field from both files, JSON-tagged exactly as today, so existing config files keep loading byte-identical.

### Phase 2: Switch the two existing readers
- Refactor `monitor.GetTrackingMode(projectID)` to call `config.Load(projectPath)` and read `cfg.Tracking`. Delete the bespoke reader. Honor the new layered semantics: empty/unset on the project side inherits global.
- Refactor `monitor.IsCheckEnabled(projectID, name)` to call `config.Load(projectPath)` and read `cfg.Checks[name]`, falling back to the per-check default when neither layer sets the key.
- `just build`. Both call sites compile and behave identically for the unchanged-config case.

### Phase 3: Verify the original bug
- Reproduce the bug against the OLD binary first (capture as confirmation): global-only `drift_detection: true`, attempt out-of-scope edit, edit succeeds when it should block.
- Run the same scenario against the NEW binary: edit blocks. This is the acceptance test for the refactor.
- Add the inverse test: project sets `drift_detection: false`, global sets `true`. Project wins, no block.
- Re-run the end-to-end hook test from the original E-917 verification.

### Phase 4: Document the Python contract
- Add `internal/config/README.md` (short) listing every field, its layer rules, and its merge rule, in plain English. The Python follow-up task uses this as the spec to match.

## Reuse, Do Not Rebuild

- `cfgstore.LoadDefaultConfig[RC, PRC]` — canonical entrypoint for CLI + Project layered loading with project precedence. Sits in `~/Projects/go-pkgs/go-cfgstore/load_config.go`.
- `RootConfig` interface (`RootConfig() / Normalize() / Merge()`) — standard pattern; Mike's pattern docs at `~/.claude/skills/go-config-file-author/references/go-configuration.md`.
- `go-doterr` for ALL error returns from the new package (no `fmt.Errorf`).
- `go-dt` for any path values stored on the struct or passed across boundaries.
- `defaultCheckEnabled(name)` already exists in `internal/monitor/checks.go`; move it to `internal/config/normalize.go` rather than rewriting.

## Critical Files

| Purpose | Path |
|---|---|
| Bespoke reader to delete (tracking) | `internal/monitor/session.go:305` (`GetTrackingMode`) |
| Bespoke reader to delete (checks) | `internal/monitor/checks.go` (entire file) |
| Hook callsites that consume these | `cmd/endless-hook/claude.go:296`, `:319` |
| ConfigDir helper (kept, unrelated) | `internal/monitor/db.go:20` |
| Global config on disk (untouched) | `~/.config/endless/config.json` |
| Per-project config on disk (untouched) | `/Users/mikeschinkel/Projects/endless/.endless/config.json` |
| go-cfgstore source | `~/Projects/go-pkgs/go-cfgstore/` |
| House skill reference | `~/.claude/skills/go-config-file-author/references/go-configuration.md` |
| House skill non-negotiables | `~/.claude/skills/go-config-file-author/references/go-non-negotiables.md` |

## Verification

End-to-end:
1. **Bug fix.** Global-only `drift_detection: true`, no per-project setting. Old binary: edit succeeds incorrectly. New binary: edit blocks. (Capture both runs as evidence.)
2. **Project override.** Global `true`, per-project `false`. Project wins, edit succeeds. Toggle per-project to `true`, edit blocks.
3. **Tracking layering.** Global `tracking: enforce`, no per-project setting. Behavior matches enforce. Add per-project `tracking: off`, behavior matches off. Remove per-project setting, behavior reverts to enforce.
4. **No regression in existing fields.** `endless task list`, `endless task start <id>`, `endless suggestions list` behave identically. `endless scan` (or whatever uses `roots`) still finds projects.
5. **Build green.** `just build`, all four binaries.

## Risks and Their Mitigation

- **Python drift.** Until the Python follow-up lands, Python and Go could disagree about merged values for any layered field. Mitigation: this task's only layered fields are `tracking` and `checks`, and Python doesn't read either of them today (only Go does). So at refactor time the gap is theoretical, not active.
- **Other Go config readers I haven't found.** The exploration found 5 Go callsites; only 2 read content (`GetTrackingMode`, `IsCheckEnabled`). The other 3 are `ConfigDir()` resolvers (out of scope). If a callsite is missed, build will catch any compile breakage; runtime behavior is unchanged for callers that don't go through the new package.
- **`go-cfgstore` is beta** (per its README). The dependency could shift. Acceptable; Mike maintains it and the package's API tier 1 is stable enough for our use.

## Endless Tasks to Create (after ExitPlanMode)

Parent (type=plan):
- "Refactor Endless config loading onto go-cfgstore"

Children (type=task):
1. "Add internal/config package with EndlessConfig schema and per-field merge"
2. "Replace bespoke GetTrackingMode reader with config.Load()"
3. "Replace bespoke IsCheckEnabled reader with config.Load()"
4. "Document Python config merge contract in internal/config/README.md"

Separate follow-up parent (NOT child of this plan):
- "Migrate Python config readers to honor Go config merge semantics"

Implementation order: 1 → 2 → 3 in one wave (they are tightly coupled), then 4 standalone.

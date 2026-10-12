# Plan: Go settings registry with doctor metadata

## Decisions this plan rests on (Mike's answers to the open questions)
- Q1: collect-then-validate pattern (E-2283). `Register` only appends and returns nothing. `Initialize() error` validates everything, joins all errors, builds the index and freezes it. Before that, accessors return `ErrNotInitialized`.
- Q2: `internal/jobs` moves to the same pattern in this task (see also E-2285's notes: whichever task first adds the startup step does it).
- Q3: an `Initialize` failure stops every `endless-go` command, through the refusal path. A Go test catches it during development.
- Q4: every config key belongs in the registry eventually. Setup that lives outside config (Claude hooks, zsh hooks) is deferred to E-2285.
- Q5: each feature's runtime type implements `Settings()`. Top-level scalars become named types (e.g. `type Tracking string`) so they can carry the method too.
- Q6: two layers, following gomion's gomcfg / gompkg / ParseConfig split:
  - The file struct holds opt-in values as strings `"on"`, `"off"` or `"unset"`. A missing key also means unset. Every three-way value uses this one representation.
  - `Parse()` produces runtime types (an `OptIn` type with `On`, `Off`, `Unset`) that the rest of the code trusts.
  - In this task, only registered keys move to the split. E-2303 (later) does the rest.
  - Python gets registered settings' values from the `endless-go settings` export, never by parsing them itself.
- Q7: one entry per leaf key, with a `Feature` field grouping a feature's keys.
- Q8: an entry records which file(s) the key may live in. When a key may live in both the machine file and the project file, a decline defaults to the project file. Letting the user choose is E-2285's job.
- Q9: an entry may name a doctor check ID as its precondition. The reference is optional. It's resolved by the doctor's `Initialize` (E-2285), which runs after `settings.Initialize`.
- Q10: register the opt-ins first. Unregistered keys go on an allowlist that may only shrink.
- Q11: add `endless-go settings list --json`, and `endless set` takes its field list from it.
- Out of scope: the minimizer (a failed experiment). It is not registered and its reader is not touched.

## Steps
1. **`internal/settings` package.**
   - `Setting` holds: ID (JSON path, e.g. `main_sync.enabled`), Feature, Files (machine / project / both), Suggestable, Rationale, Default, optional PreconditionCheck (a doctor check ID).
   - Functions: `Register(...Setting)` (appends only), `Initialize() error`, `All()`, `Lookup(id)`.
   - `Initialize` rejects any entry that has an empty ID, a malformed ID, an unknown Files value, a suggestable entry with no Rationale, or a duplicate ID. It reports every failure (not just the first), wrapped with doterr sentinels.
2. **`OptIn` runtime type** with `Parse(string) (OptIn, error)`. It accepts `"on"`, `"off"`, `"unset"` and a missing key. Anything else is an error naming the file, the key and the accepted values. A legacy JSON `true` or `false` gets a message saying exactly what to write instead.
3. **File/runtime split for the opt-ins:**
   - File struct values change to strings: `main_sync.enabled`, `auto_spawn.enabled`, `prime.enabled`, and the `checks` map (`map[string]string`, so every check key uses the same representation).
   - Add runtime types and a `Parse()` for each: `MainSync`, `AutoSpawn`, `Prime`, `Checks`.
   - Move `DefaultCheckEnabled`'s hard-coded defaults into the registered entries for `checks.*`: `task_required`, `decision_checkpoint`, `session_audit`, `bash_git_writes`. Unknown check keys stay on today's "unknown is on" rule.
   - Update the readers so they consume parsed runtime values: `mainsyncjob`, `autospawnjob` (including prime) and `monitor/checks.go`.
4. **Registration.** Each feature's runtime type implements `Settings()`, and its owning package registers it in `init()`. The feature's tuning keys (`interval`, `cap`, `target`, `placement`) are registered as entries that can't be suggested.
5. **Startup step in `cmd/endless-go/main.go`.** Add one `initialize() error` after `ConsumeDBFlags`. It calls `settings.Initialize()`, then `jobs.Initialize()`, and exits through `refusal.From(err)` on failure.
6. **Convert `internal/jobs`.** `Register` only appends. The empty-name and duplicate-name checks move into `jobs.Initialize()`.
7. **Export.** `endless-go settings list --json` returns each entry with its effective value and source file for the current project. Python's `endless set` takes its field list and allowed values from it, and writes `"on"` or `"off"` for opt-in keys.
8. **Tests:**
   - A Go test that imports what `endless-go` imports and expects `settings.Initialize()` and `jobs.Initialize()` to return nil.
   - A reflection test over the file struct's leaf keys: each key must be registered or on the allowlist, and the allowlist must not grow beyond a pinned count, so any growth shows up in the diff.
   - `Parse` tests for every accepted and rejected form, including legacy bools.
   - Tests for `settings list --json`.
   - Python tests for `endless set` using the exported field list.
9. **Docs.** Update `internal/config/README.md` (string opt-ins, the registry, the allowlist) and the guide's sections on these settings.
10. **Migration happens on land.**
   - When E-2284 lands, the landing session rewrites the bool values for the registered keys to `"on"` or `"off"`, in the same land, before the global install picks up the new binary. Files:
     - the endless project's `.endless/config.json`
     - `~/.config/endless/config.json` (`checks`)
     - any other registered project's `.endless/config.json` that sets these keys.
   - Then it confirms `endless-go settings list --json` succeeds in each of those projects.
   - The edits are not left for a later manual step, because a parse failure stops every command.

## Risks and notes
- Config `checks` (hook gate switches) and doctor checks share a word. The registry uses the JSON path (`checks.task_required`), and doctor check IDs need a different namespace (E-2285).
- Because a parse failure stops every command, the parse-refusal message has to say how to fix the value in one line.

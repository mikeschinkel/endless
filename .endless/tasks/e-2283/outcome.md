# Findings: registration patterns in go-pkgs, and one for the settings and check registries

## Recommendation (short)
Use the **collect-then-validate** pattern from go-cliutil's command registry, in its original form, for both registries, as two separate small packages:

- `Register(x)` called from `init()` only appends to a slice. It returns nothing, panics on nothing, validates nothing and looks nothing up.
- A public `Initialize() error` runs once from `endless-go`'s startup path. It validates every entry and joins all the errors (not just the first one), resolves cross-references, builds the lookup index, and freezes the registry.
- Accessors (`All()`, `Lookup(id)`) read only the frozen index. Before `Initialize` they return `ErrNotInitialized` rather than panicking.

Do not use the initializer-func list (`RegisterInitializerFunc`) for these registries, and do not share a generic registry helper between them.

## What exists in go-pkgs

| # | Pattern | Where | Can init() fail? | Fits the rule? |
|---|---|---|---|---|
| A | Collect in init(), validate and resolve in `Initialize() error` | go-cliutil `RegisterCommand` + `Initialize` (`ValidateCommands`, `BuildCommandTree`); also scout-mcp's copy | Originally no; **now yes** (see A′) | Yes, in its original form |
| A′ | Same, but `RegisterCommand` now returns `error` (FlagName conflict check, added in go-cliutil 442b786) | go-cliutil today; gomion callers do `if err != nil { panic(err) }` inside `init()` | Yes, it panics | **No**. It is a regression of A |
| B | Initializer-func list: `init()` registers closures, `CallInitializerFuncs(args)` runs them and calls `errors.Join` on the results | go-cliutil, go-logutil, scout-mcp langutil | No | Yes, but it is for deferred *setup* (inject the writer or logger), not for a catalog of entries |
| C | Panic on register | go-jsontest `RegisterPipeFunc` (name must end in `()`) | Yes, it panics | No |
| D | Map-keyed, last write wins, no validation, lazy "not registered" error at lookup | go-pathvars `RegisterDataTypeClassifier`, `RegisterConstraint`, `RegisterDataTypeAlias` | No | Half. It cannot fail, but it never validates, so duplicates are silently overwritten |
| E | Stub (`RegisterExtension` appends, nothing reads it) | go-rfc9457 | No | n/a |

Lessons from the inventory:

1. **A is the only pattern that does what the rule asks:** init cannot fail, every failure surfaces in one place reached from `main()`, and all failures are reported together.
2. **A′ shows how the pattern breaks down.** A validation that was convenient to write at registration time (a flag conflict) changed the signature to `error`, and the only thing an `init()` can do with an error is panic. That check is also order-dependent: it only sees global flags already registered. The guard is to keep `Register` with **no return value**, so nobody can add an early validation without changing the signature.
3. **D shows the cost of resolving at registration time.** `RegisterDataTypeAlias` has to look back over the constraints already registered and re-alias them, because it cannot know whether a constraint's `init()` ran before or after it. Go does not promise an `init()` order across files in a package. Resolving cross-references inside `Initialize` (as `BuildCommandTree` does for parent types) removes that whole class of bug.
4. **D also shows why "cannot fail" is not enough on its own.** A map with last write wins and no duplicate check turns a programming error into silent wrong behavior. Validation has to exist. It just belongs in `Initialize`.
5. **B solves a different problem.** It makes sense when a package needs runtime arguments before it can work (a writer, a logger). Routing the settings and check registries through it would add a layer of indirection and gain nothing.
6. `go-jsontest/pipefuncs.Initialize()` "triggers init() by referencing types". That does not work in Go: importing the package is what runs `init()`. Its real value is that callers get a non-blank reason to import the package, plus a hook for returning errors. The registries proposed here get the same benefit, because `Initialize` actually does work.

## Endless already has a registry, and it uses pattern C
`internal/jobs.Register` panics on an empty or duplicate name (`Register` in `internal/jobs/jobs.go`). Jobs register by blank import in the blank imports of `internal/*job` packages in `cmd/endless-go/main.go`. This breaks the rule the doctor design states. If the settings and check registries follow A while jobs stays on C, Endless has two conventions for one problem. See open question 1.

## Recommended shape for each registry (one package per kind)
Sketch only. E-2284 and E-2285 own the details.

```go
var pending []Setting          // appended by init(), never read except by Initialize
var index map[SettingID]Setting // built by Initialize; nil means not initialized

func Register(s Setting) { pending = append(pending, s) }   // no return value, by design

func Initialize() (err error) {
    var errs []error
    // per entry: ID non-empty and well-formed; Scope is machine|project;
    //   Rationale non-empty; Check non-nil; default is tri-state unset
    // across entries: duplicate IDs (report every pair, not only the first)
    // cross-references: resolved here, never at Register time
    // on success: build index; on failure: leave index nil
    return errors.Join(errs...)   // wrapped with a doterr sentinel per house style
}

func All() ([]Setting, error)          // ErrNotInitialized if index == nil; ordered by ID
func Lookup(id SettingID) (Setting, bool, error)
```

The check registry has the same shape. Its per-entry validation: stable check ID, severity, scope (machine, project, worktree, cross-project), fix tier (auto, confirm, advice), and a non-nil run function.

### How this delivers "an agent adding a setting cannot miss the doctor metadata"
- The doctor metadata is fields of the one struct you pass to `Register`. There is no second call to forget.
- `Initialize` refuses an entry with an empty `Rationale`, a nil `Check`, an unknown `Scope`, and so on.
- **Add a Go test that imports what `cmd/endless-go` imports and calls `settings.Initialize()` and `doctor.Initialize()`, expecting `nil`.** This test turns "the CLI exits gracefully" into "`just test-go` goes red before it ships". Without it, the first person to see a missing rationale is a user whose every `endless` command fails, because Python reaches Go through `endless-go`.

### Wiring
- Order: `endless-go` startup calls `settings.Initialize()`, then `doctor.Initialize()`. Checks may reference settings, so settings must be frozen first. On error, exit through the existing refusal path (`refusal.From(err).Command("endless-go").Exit(...)`, as `ConsumeDBFlags` already does in `cmd/endless-go/main.go`), so the message is classified like every other startup failure. `endless-go` has no `Initialize` chain today: `main()` sets the cfgstore logger, consumes the DB flags, then dispatches. One small `initialize() error` step after `ConsumeDBFlags` is enough. It does not need gomion's `RunArgs` and `Initialize` scaffolding.
- Registration stays a blank import in `cmd/endless-go/main.go`, as jobs does. The known weakness: forget the import and the entry silently does not exist, and no validation can detect an absence. In practice this risk is small for settings, because the feature package that owns a setting is already imported for its behavior. It is real for checks that live in their own packages. The cheap mitigation is to keep checks inside the package of the feature they check, not in a separate `checks/` tree.
- No mutex is needed. `init()` runs on one goroutine, and after `Initialize` the index is read-only. (jobs uses a RWMutex. That is harmless but unnecessary.)

### Why no shared generic registry
A `registry.List[T]` helper with validate hooks would save about 30 lines across the two packages. It is also a registry of registries in miniature, which you have said you do not want. Two plain packages also let each validation read in its own domain terms.

## Decisions (Mike, after reading these findings)
- **Pattern A for both registries:** accepted.
- **internal/jobs converts to pattern A:** `init()` registration can never fail, and the empty-name and duplicate checks move into `jobs.Initialize() error`. This ships with E-2284 or E-2285, whichever adds the `endless-go` startup `initialize()` step first. A note is on both tasks.
- **An `Initialize` failure stops every `endless-go` command:** accepted.
- **Settings and checks split, option A:** two independent registries. A setting declares its ID, scope, tri-state default, rationale, and a function that reads its current value. `--suggestions` iterates the settings registry. The check registry holds problem checks only, and may refer to a setting by ID. The other two options were rejected:
  - Settings feeding the check registry merges suggestions and problems, which have different decline and accept rules.
  - Settings carrying their own problem checks splits check registration across two places.
- **Root cause of go-cliutil's A′ regression:** the fixup code added in 442b786 should have done its work in `Initialize()`, not in `RegisterCommand()`. That work is being filed in the go-cliutil project.
- **gomion:** Mike fixed the `Initialize` condition himself (`if err != nil`). It is uncommitted in gomion's main.

## Open questions (answered above) (to settle before E-2284 and E-2285 start)
1. **Convert `internal/jobs` to pattern A as well?** Doing it is small: drop the panics, move the empty and duplicate checks into `jobs.Initialize() error`, and call it in the same startup step. That gives Endless one convention. My recommendation: yes, folded into E-2284 or E-2285 (whichever lands the startup `initialize()` first) rather than filed separately. Your call.
2. **Should `Initialize` failure block every `endless-go` command, or only doctor and settings commands?** Blocking everything is the strict reading of "fail gracefully from main()", and the Go test above makes it a development-time failure only. The alternative (each command initializes only what it uses) adds no real safety and makes startup more complex. My recommendation: block everything.
3. **Does a setting's `Check` belong to the setting, or does doctor derive a "suggest" entry from each setting?** The design reads as: the settings registry carries the metadata, `--suggestions` iterates settings, and the check registry holds problems only. I recommend keeping that split, so neither registry depends on the other's types except through setting IDs.

## Observations outside Endless (not filed)
- **gomion `gommod/run.go` `Initialize`:** after `err = cliutil.Initialize(args.Writer)` it tests `args.Writer == nil` instead of `err != nil`, and `err` is then overwritten by `CallInitializerFuncs`. So `ValidateCommands` and `BuildCommandTree` failures are silently dropped. The fix is one line in gomion.
- **go-cliutil `RegisterCommand` returning `error` (A′):** to bring it back in line with the rule, move the FlagName conflict check into `ValidateCommands` and drop the return value. This touches every caller in gomion, xmlui-cli, go-stash and others.

Neither belongs to an Endless task. Both are noted here so they are not lost.

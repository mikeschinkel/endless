# Retire the verify run-script shell emission

The Go driver is the only path that runs a verify.toml. The shell-script emission
and the bare-clone command derivation it needs are dead code; remove them.

## Remove

- `internal/verify/runscript.go` (`RenderRunScript`) and `runscript_test.go`.
- `Check.ResolvedCommand` and `bareCloneCommand` (`check.go`).
- `pytestCmd` (`driver_pytest.go`), whose only caller is `bareCloneCommand`.
- `goTestCmd`'s `jsonStream` parameter: its only `false` caller is
  `bareCloneCommand`, so it always emits `-json`.
- `LoadManifest` (`discover.go`). `ParseManifest` stays: the manifest validation
  tests exercise the rules through it.
- `TestCheck_ResolvedCommand`, and the `ResolvedCommand` half of
  `TestCheck_ResolvedThroughParse` (its `ResolvedFormat` half stays).

## Rewrite

- The `Manifest` doc comment: drop "the checks a bare clone runs" and the
  pointer to `RenderRunScript`; describe it as what the driver runs.
- `ParseManifest`'s and `goTestCmd`'s doc comments: drop their bare-clone wording.

## E-1596

Add a note to E-1596's analysis: running a suite from a bare clone without
Endless is no longer done by an emitted script, and belongs to E-1793's
standalone binary.

## Out of scope

Running a suite without Endless (E-1793). Landed task suites and their document
mirrors (E-1618, E-1791 and others that name these symbols), and research under
`docs/private/`.

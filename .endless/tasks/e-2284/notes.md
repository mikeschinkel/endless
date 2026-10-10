## Scope added by E-2283 (Mike's decision)

Whichever of E-2284 and E-2285 first adds the `endless-go` startup `initialize()` step also converts `internal/jobs` to the same pattern:

- `jobs.Register` only appends. It does not panic on an empty or duplicate name.
- Those checks move into `jobs.Initialize() error`, which is called in that startup step.
- A failure exits through the refusal path and stops every `endless-go` command.

Pattern, rationale and the settings/check split: E-2283's outcome.

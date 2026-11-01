Fuzz testing has two halves. The search half (`go test -fuzz=...`) is open-ended and pays off by running for hours, so it does not belong in a repo's standard `go test` run. The regression half replays a saved corpus of inputs a past run found and is quick.

Today, in go-cliutil, only the regression half is wired up: `test/corpus_test.go` replays `test/testdata/fuzz/` with a per-entry timeout, and `test/infinite-fuzz.sh` runs the search half by hand, locally, with no schedule and no path for a new crasher to reach the repo. The corpus directory is empty, so the replay proves nothing yet. Other go-pkgs repos have no fuzz setup at all.

Mike raised this while reviewing E-2430 in go-cliutil (2026-11-01): fuzzing is "run it for a long time" work, not "run it fast to catch a regression" work, so Endless should consider running it as a background job rather than in the test suite. The Go-specific pieces may move to Gomion later; for now the whole idea lives in Endless.

Questions to work through:

- Where long fuzz runs live: an Endless background job (the fire-once runner), a scheduled GitHub Actions workflow, or a cron on one of Mike's machines.
- How a run picks its targets: every `Fuzz*` function in a project's modules, or a list the project declares.
- How a run's findings get back: a crasher becomes a corpus entry (committed under `testdata/fuzz/`), a filed bugfix task, or both, and who reviews it.
- Whether the corpus is committed at all, and how its growth is bounded.
- How a run is budgeted: wall-clock per target, per project, per night.
- What Endless owns versus what Gomion will own once the Go-specific parts move there.

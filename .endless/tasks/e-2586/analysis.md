Proposed shape (to settle in the plan):

- Add a top-level `env` table to verify.toml (and probably the project-level verify.toml), whose values expand `$HOME` and `$ENDLESS_VERIFY_DIR` against the run's isolated env. The built-in rows, setup, checks and teardown all get it. E-2440 would set `GOPROXY = "file://$HOME/e-2440/proxy,https://proxy.golang.org"` and `GONOSUMDB = "github.com/mikeschinkel/go-tealeaves"`.
- Run the built-in go.mod rows after `setup`, so a suite can prepare what they read (a file proxy, a module cache).
- Keep the check strict: no list of tolerated unknown revisions. A tolerated-sibling-tag rule was considered and rejected, because it would also hide a mistyped tag.

Prior ordering decisions (searched 2026-11-09):
- E-2569 placed the rows before setup deliberately ("They need nothing from setup"), first in the report and fail-fast, in both the manifest path and the legacy verify.sh path (`runGoModChecks` is called from both). Moving execution after setup reverses that choice; keeping the rows first in the report keeps the part E-2569 cared about, which is where the user reads them.
- No other task has moved them. The verify/land ordering tasks (E-2261, E-2582, E-2583, E-2282) concern which commit a pass covers, not step order. E-2438 fixes setup and teardown at the repo root. E-1603 keeps the runner's own env generic; a suite-declared `env` is the suite's, not the product's, so it doesn't cross that line.

Open:
- When setup fails, today the built-in rows still report; after the move they would not run. Run them anyway (without the suite's env) or report them as not run?
- Does the legacy verify.sh path get `env` too, or only the manifest?
- Should only rows run after setup when the suite declares `env`, leaving E-2569's order unchanged otherwise?

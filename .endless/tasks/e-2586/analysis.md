Proposed shape (to settle in the plan):

- Run the built-in go.mod rows after `setup`, not before, so a suite can prepare what they need (a file proxy, a module cache).
- Add a top-level `env` table to verify.toml (and probably the project-level verify.toml), whose values expand `$HOME` and `$ENDLESS_VERIFY_DIR` against the run's isolated env. The built-in rows, setup, checks and teardown all get it. E-2440 would set `GOPROXY = "file://$HOME/e-2440/proxy,https://proxy.golang.org"` and `GONOSUMDB = "github.com/mikeschinkel/go-tealeaves"`.
- Keep the check strict: no list of tolerated unknown revisions. A tolerated-sibling-tag rule was considered and rejected, because it would also hide a mistyped tag.

Open: whether a failing setup should still report the built-in rows (today they run even when setup would fail).

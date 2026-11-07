## Approach

1. **Home directory at startup.** `cmd/endless-go`'s startup calls `dt.EnsureUserHomeDir()` before any command runs. On failure nothing is converted: paths print in full, and the command proceeds.

2. **Free-text function in `internal/refusal`.** One exported function applies the `$HOME` rewrite described in the analysis to any text. A single path value needs no new function: callers use go-dt's `DirPathToTilde` / `FilepathToTilde` / `EntryPathToTilde` with `OrFullPath`. There is no audience check; person and agent output both show `~`.

3. **Output points.**
   - `refusal.(*Error).Render`: applies the free-text function to what it returns, in both the person and the agent form.
   - internal/errorscmd (list, show, `--detail`, log-only output) and the live-view fault row (internal/faultrow) pass stored summaries, details and field values through the free-text function.
   - Every direct `fmt.Print*` / `fmt.Fprint*` to stdout or stderr that prints a path value shows it in go-dt's `~` form. Find them with the analyzer's first rule (step 6), which must report none when this task is done.

4. **Stored known paths.** Producers that pass a path into `faults.Record` — as a `Fields` value or as a formatted argument of `Summary` / `Detail` — convert it to `~` form where they build the fault (go-dt `*ToTilde`, `OrFullPath`; this is storage, so it does not depend on audience). Find them by searching `faults.Record` / `faults.Fault{` callers for path-typed or path-named values.

5. **Retire hand-rolled helpers.** Remove `tildePath` (internal/hookcmd) and both `displayPath` helpers (internal/monitor, internal/verifycmd); their callers use go-dt's `~` form. The cwd-relative form goes away. The PreToolUse hook refusals (write outside the worktree, working-directory drift, shell writes, unbound worktree) show every path in `~` form, including the `/cd` line and the worktree to re-target under.

6. **Analyzer.** One new package, `internal/pathlint`, holding a `golang.org/x/tools/go/analysis` analyzer with two rules:
   - **printed path:** a `dt.DirPath` / `dt.Filepath` / `dt.EntryPath` (or `Tilde*`) passed to a `fmt` print function writing to stdout/stderr (`Print*`, `Fprint*` to `os.Stdout`/`os.Stderr`) without going through go-dt's `ToTilde`;
   - **untyped path:** a call to a stdlib path function that go-dt replaces — `filepath.Join`, `filepath.Dir`, `filepath.Base`, `filepath.Abs`, `os.Getwd`, `os.UserHomeDir`, `os.MkdirTemp`, `os.TempDir` — with the go-dt equivalent named in the diagnostic.
   A baseline file in the package lists the existing untyped-path sites by package, file and enclosing function (not line). The analyzer skips listed sites; a test fails when the baseline lists a site that no longer exists, so the list only shrinks. A Go test in the package runs the analyzer over `./internal/...` and `./cmd/...`, so `just test-go` enforces it; a `just lint` recipe runs the same check on its own.

7. **Fake-HOME output test.** A Go test builds `endless-go`, sets a temp `HOME`/`XDG_CONFIG_HOME`, and runs one command per output point: a refusal that names a path (`settings list` over a legacy boolean), in both person and agent form, `errors show` and `errors show --detail` of a recorded fault carrying a path, `settings migrate`, and a direct-print command converted in step 3. It fails if `$HOME/` appears in their output.

8. **Python.** `endless.config.tilde()` replaces home only at a path-segment boundary (exactly home, or home followed by the separator).

## Follow-up

Converting the baselined plain-string path code to go-dt types is filed per package as separate tasks; each shrinks the baseline.

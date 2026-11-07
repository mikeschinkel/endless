## Approach

1. **Home directory at startup.** `cmd/endless-go`'s startup calls `dt.EnsureUserHomeDir()` before any command runs. On failure nothing is converted: paths print in full, and the command proceeds.

2. **Display functions in `internal/refusal`.** Two exported functions, next to the audience code:
   - one for a single path value (`dt.DirPath`, `dt.Filepath`, `dt.EntryPath`): for a person, its go-dt `*ToTilde` form with `OrFullPath`; for an agent, the absolute path (expanding a `Tilde*` value);
   - one for free text: for a person, the `$HOME` rewrite described in the analysis; for an agent, the reverse (`~/` back to `$HOME/`), so stored `~` paths reach agents absolute.
   Audience is the error's pin where there is one (`ToAgent` / `ToHuman`), else `refusal.Agent()`.

3. **Output points.**
   - `refusal.(*Error).Render`: the person branch applies the free-text function to `humanText`; the agent branch is unchanged.
   - internal/errorscmd (list, show, `--detail`, log-only output) and the live-view fault row (internal/faultrow) pass stored summaries, details and field values through the free-text function.
   - Every direct `fmt.Print*` / `fmt.Fprint*` to stdout or stderr that prints a path value goes through the path function. Find them with the analyzer's first rule (step 6), which must report none when this task is done.

4. **Stored known paths.** Producers that pass a path into `faults.Record` — as a `Fields` value or as a formatted argument of `Summary` / `Detail` — convert it to `~` form where they build the fault (go-dt `*ToTilde`, `OrFullPath`; this is storage, so it does not depend on audience). Find them by searching `faults.Record` / `faults.Fault{` callers for path-typed or path-named values.

5. **Retire hand-rolled helpers.** Remove `tildePath` (internal/hookcmd) and both `displayPath` helpers (internal/monitor, internal/verifycmd); their callers use the path function from step 2. The cwd-relative form goes away.

6. **Analyzer.** One new package, `internal/pathlint`, holding a `golang.org/x/tools/go/analysis` analyzer with two rules:
   - **printed path:** a `dt.DirPath` / `dt.Filepath` / `dt.EntryPath` (or `Tilde*`) passed to a `fmt` print function writing to stdout/stderr (`Print*`, `Fprint*` to `os.Stdout`/`os.Stderr`) without going through the path function;
   - **untyped path:** a call to a stdlib path function that go-dt replaces — `filepath.Join`, `filepath.Dir`, `filepath.Base`, `filepath.Abs`, `os.Getwd`, `os.UserHomeDir`, `os.MkdirTemp`, `os.TempDir` — with the go-dt equivalent named in the diagnostic.
   A baseline file in the package lists the existing untyped-path sites by package, file and enclosing function (not line). The analyzer skips listed sites; a test fails when the baseline lists a site that no longer exists, so the list only shrinks. A Go test in the package runs the analyzer over `./internal/...` and `./cmd/...`, so `just test-go` enforces it; a `just lint` recipe runs the same check on its own.

7. **Fake-HOME output test.** A Go test builds `endless-go`, sets a temp `HOME`/`XDG_CONFIG_HOME` and the person audience, and runs one command per output point: a refusal that names a path (`settings list` over a legacy boolean), `errors show` and `errors show --detail` of a recorded fault carrying a path, `settings migrate`, and a direct-print command converted in step 3. It fails if `$HOME/` appears in their output. The same commands under the agent audience must show the absolute path.

8. **Python.** `endless.config.tilde()` replaces home only at a path-segment boundary (exactly home, or home followed by the separator).

## Follow-up

Converting the baselined plain-string path code to go-dt types is filed per package as separate tasks; each shrinks the baseline.

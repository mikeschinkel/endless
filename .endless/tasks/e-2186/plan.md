Endless is moving off XDG_CONFIG_HOME as a ROUTING mechanism (the DB moved into <worktree>/.endless/sandbox/ under E-1964). Settled with Mike 2026-09-28 after tracing the history (E-1020 → ED-1066/ED-1072 → E-1281 `sandbox bind` → E-1425/E-1450/E-1628/E-1347 → E-1429/E-1668 → E-1964/ED-1554): the pain was Endless INJECTING a second value that every process inherited, not a user's own setting.

Design (A + flags):
1. XDG_CONFIG_HOME belongs to the user; Endless never sets it to route. Default resolution and `--db main` become ONE rule: $XDG_CONFIG_HOME/endless if set, else $HOME/.config/endless (Go dbcontext.ConfigDir/MainConfigDir, Python config._config_root/main_config_dir). "Main is not Default" retires — its only reason was escaping the injection.
2. Child routing is by flag, never env (E-1668 principle). internal/triagejob and internal/minimizerjob pass `--db main|sandbox` matching the runner's own resolution; for sandbox the child cwd is the worktree root (not TempDir). A runner on an arbitrary --db-dir has no word and refuses loudly. src/endless/triage.py spawn_detached drops its XDG line (it already passes --db).
3. `endless-go sandbox enter/run` (ED-1072 subshell) keeps its XDG_CONFIG_HOME export, commented as generic isolation of a user-entered subshell. The verify runner's temp HOME + XDG_CONFIG_HOME stays, commented as generic suite isolation.
4. Stale comments/docs fixed everywhere else, including the false internal/verifycmd/verify.go header and the E-1072 → ED-1072 reference in sandboxcmd/enter.go.

Grown scope while rebasing onto main (2026-10-02):
5. E-1993 deleted triage (triagejob, triage.py), and E-1814 moved the XDG child router into jobs.ChildEnv, shared by minimizerjob and the new autospawnjob. jobs.ChildEnv is deleted; both jobs route through monitor.ChildDBRoute (auto-spawn runs only on main and keeps the project checkout as its cwd).
6. E-2159 classified the missing-schema refusal as NO-REPORT whenever XDG_CONFIG_HOME is set. Under (1) a set XDG is the user's real config dir, so the class now keys on ENDLESS_SANDBOX (the sandbox subshell) instead; covered in tests/test_db_error_diagnostic.py.
7. ForceRealDB (no production callers; its only job was escaping the injected XDG) is deleted.

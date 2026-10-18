# Plan — register the tmux keys with kinds that say what they take

Decided with Mike, 2026-10-18 (option B): three new setting kinds, not one free-text kind and not an allowlist.

## 1. Three kinds in `internal/settings`

- **path** — an absolute path. Used by `tmux.socket_dir`.
- **name** — a plain file or session name: non-empty, no `/`, no whitespace, no `:` or `.` that tmux reads as a target separator. Used by `tmux.console_server`, `tmux.tasks_server`, `tmux.tasks_session`.
- **command** — a command template that contains `{cmd}`, or empty (detect the terminal). Used by `tmux.terminal`.

Each kind gets a shape check exported from `internal/settings`, its JSON export name (`path`, `name`, `command`), and default validation in `validate()` like the existing kinds.

## 2. Register the five keys in `internal/tmuxservers`

Beside `Resolve`, a `Settings()` provider registered from `init()`: feature `tmux`, `MachineFile` only (tmux keys are never merged from a project), none suggestable. Defaults are what `Resolve` supplies when a key is unset: `DefaultDir()`, `DefaultConsoleServer`, `DefaultTasksServer`, `DefaultTasksSession`, and empty for `terminal`.

## 3. `Resolve` uses the same shape checks

`tmuxservers.Resolve` refuses a configured value that fails its key's shape check, naming the key and what it takes, by calling the same functions the registry uses, so the two cannot disagree. It also refuses a socket path (directory + socket name) longer than tmux accepts. Ownership and permission checks on the directory stay where they are: they depend on the machine at the time of use.

## 4. Python export and the test

- `src/endless/settings_export.py`: `accepted()` and `parse()` know the three kinds.
- `cmd/endless-go/main_test.go`: drop `tmux.session_name` from `unregisteredKeys`, lower `maxUnregistered` to 20; `TestEveryConfigKeyIsRegistered` passes.

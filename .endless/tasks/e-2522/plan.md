# Drop project register and rename the project states

## States, after this task
- **noted**: the monitor added a database row for a directory a session worked in; nobody ran init, so there is no `.endless/config.json` and no identity.
- **initialized**: `project init` ran on it. `project discover` counts, since it runs the same code as `project init --infer`.
- **ignored**: `project deinit` ran on it. `ignored` is the existing word and the existing value (`config.IGNORED_STATUS`).

## Steps
1. Remove the `register` alias of `project init` (`project_cmd.add_command(init, name="register")` in `src/endless/cli.py`) and remove `"register"` from the hidden top-level moved-command stubs, so `endless register` and `endless project register` both get the ordinary unknown-command error. No message points at `init`.
2. Rename `project unregister` to `project deinit`, and `"unregister"` in the moved-command stubs to `"deinit"`. Rename the module and functions behind it (`src/endless/unregister.py` and its `unregister*` functions) to match. `project unregister` gets the ordinary unknown-command error.
3. `deinit` writes `"ignored"` as the config's `status` instead of `"unregistered"`. Code that skips a directory by that value (`project_path.py`, `project_sync.py`) checks `"ignored"`. Nothing reads `"unregistered"` any more.
4. Replace the project sense of "register/registered/unregistered/registration" with noted, initialized, ignored and init/deinit, in help text, messages, refusals, comments, `README.md`, `docs/guide/` and tests. Find them with `git grep -in 'regist'` and judge each hit: the monitor's automatic rows become "noted" (for example `ensureAutoRegisteredProject` becomes `ensureNotedProject`); what `init` or `discover` produces is "initialized"; a deinit directory is "ignored"; a command suggestion becomes `endless project init` or `endless project deinit`.
5. Run the Python and Go test suites.

## Not changed
- "Register" in other senses: background jobs ("a registered job", including the jobs view's "unregistered" text), ports, hooks and similar.
- Dated files under `docs/research-*`, `.endless/` mirrors and ledgers, and old task text.
- Database column names and ledger event kinds (none use the word).

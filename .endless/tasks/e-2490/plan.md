## Approach

**The Git hook.** A `prepare-commit-msg` script that reads the branch with `git symbolic-ref --short -q HEAD`, matches `task/<N>`, and, when the message file has no `Endless-Task` trailer, appends one with `git interpret-trailers --in-place --if-exists doNothing --trailer "Endless-Task: E-<N>"`. It always exits 0. `--no-verify` does not skip `prepare-commit-msg`.

**Detection in `project init`.** Read `core.hooksPath` and the hooks directory (`git rev-parse --git-path hooks`), and look for `lefthook.yml` / `.lefthook.yml` and lefthook's dispatcher files, and for husky (`.husky/`, `core.hooksPath` pointing into it). Then:
- lefthook: add a `prepare-commit-msg` command running Endless's script to `lefthook-local.yml` (default) or the committed `lefthook.yml`, per a user setting. The committed form starts with `command -v endless >/dev/null || exit 0`. Run `lefthook install` if lefthook's dispatcher is not yet installed.
- no manager, no Git hooks: prompt for lefthook or none (`--git-hooks=lefthook|none` answers it). None writes the script as `prepare-commit-msg` in the hooks directory.
- no manager, Git hooks present: offer lefthook; on agreement, write a `lefthook.yml` that runs each existing hook script, show it, and run `lefthook install`.
- husky, an unsupported manager, or a declined offer: finish init and print why the trailer will not be added.
Never overwrite, rename or remove a hook file or `core.hooksPath` Endless did not write.

**Settings.** Two user settings in Endless's settings registry: where the lefthook entry goes (local or committed) and whether `endless doctor` reports a repo without the trailer setup.

**Doctor.** A check that reports a repo whose task-branch commits will not get the trailer, naming the reason, skipped when the setting turns it off.

**Docs.** Describe the trailer, the setup paths and the settings in the orchestration guide.

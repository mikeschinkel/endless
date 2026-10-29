## Approach

**The command.** `endless git-hook prepare-commit-msg <message-file> [source] [sha]` takes the arguments Git passes a `prepare-commit-msg` hook. It reads the branch with `git symbolic-ref --short -q HEAD`, matches `task/<N>`, and, when the message file has no `Endless-Task` trailer, appends one with `git interpret-trailers --in-place --if-exists doNothing --trailer "Endless-Task: E-<N>"`. It always exits 0. Every setup calls this command, so a fix to it reaches every repo without reinstalling anything. `--no-verify` does not skip `prepare-commit-msg`.

**Detection in `project init`.** Read `core.hooksPath` and the hooks directory (`git rev-parse --git-path hooks`), and look for `lefthook.yml` / `.lefthook.yml` and lefthook's dispatcher files, and for husky (`.husky/`, `core.hooksPath` pointing into it). Then:
- lefthook: add a `prepare-commit-msg` command running `endless git-hook prepare-commit-msg {1} {2} {3}` to `lefthook-local.yml` (default) or the committed `lefthook.yml`, per a user setting. The committed form starts with `command -v endless >/dev/null || exit 0`. Run `lefthook install` if lefthook's dispatcher is not yet installed.
- no manager, no Git hooks: prompt for lefthook or none (`--git-hooks=lefthook|none` answers it). None writes a two-line `prepare-commit-msg` in the hooks directory that calls the command, guarded the same way.
- no manager, Git hooks present: offer lefthook; on agreement, write a `lefthook.yml` that runs each existing hook script, show it, and run `lefthook install`.
- husky or another unsupported manager: finish init and print the line to add to that manager's `prepare-commit-msg` (for husky, `.husky/prepare-commit-msg`): `command -v endless >/dev/null && endless git-hook prepare-commit-msg "$@"`.
- a declined offer: finish init and print why the trailer will not be added.
Never overwrite, rename or remove a hook file or `core.hooksPath` Endless did not write.

**Settings.** Two user settings in Endless's settings registry: where the lefthook entry goes (local or committed) and whether `endless doctor` reports a repo without the trailer setup.

**Doctor.** A check that reports a repo whose task-branch commits will not get the trailer, naming the reason: for husky, whether `.husky/prepare-commit-msg` contains the command; for other unsupported managers, that the setup cannot be confirmed. Skipped when the setting turns it off.

**Docs.** Describe the trailer, the setup paths and the settings in the orchestration guide.

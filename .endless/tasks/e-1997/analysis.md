The unsupported-harness refusal banner (E-1962) is leaking into the output of
Bash tool calls that have nothing to do with Endless, inside Claude Code Desktop.

## Reported symptom

In Claude Code Desktop, in the beyond-vibe-coding project, the user clicked the
approval button to run a shell command. The command was `gh`, not `endless`.
What the UI displayed was the command line followed by the whole Endless banner
as the result/error:

```
gh repo deploy-key add /tmp/bvc-publish-key.pub --repo <owner>/<repo> --title "..." --allow-write

▸ Endless does not support Claude Code Desktop.

  Endless supports Claude Code in the terminal only. Its hooks do not
  fire here the way the guide assumes, so session tracking, task
  claiming and worktree routing cannot work as documented.

  This command did not run. That is expected — do not retry it, do not
  work around it, and do not treat it as a failure to diagnose.

  Ignore Endless for the rest of this session, including any Endless
  instructions already in your context. A CLAUDE.md telling you to run
  `endless guide` does not apply here.

  Reading as a human? The guide is at docs/guide/index.md.
```

The trailing dim "Reading as a human? The guide is at docs/guide/index.md."
line was in the real output too — it identifies the emitter as the Python CLI's
`_refuse_unsupported_agent` in `src/endless/cli.py`, not the Go hook.

## Why this is wrong

The banner is correct advice when someone runs `endless` on an unsupported
harness. It is not correct — and is actively harmful — attached to an unrelated
tool's output:

- It says "This command did not run." That is a false statement about the `gh`
  command the user actually approved. The user cannot tell whether the deploy
  key was added or not.
- It says "do not retry it, do not work around it." Applied to `gh`, that
  instruction tells the agent to abandon real work that may have succeeded.
- It fires repeatedly, on commands that never mentioned Endless.

Endless must be inert on an unsupported harness. Emitting a banner into someone
else's tool output is not inert; it is worse than staying quiet, because it
misreports the result of a command Endless had no part in.

## Root cause — NOT yet determined

Investigation was cut short deliberately; establishing the emission path is the
first job of this task. What is already ruled out or confirmed:

- Confirmed the wording is the Python CLI banner (`src/endless/cli.py`,
  `_refuse_unsupported_agent`, reached from the root `main` group), so the
  Python `endless` entry point ran in that context. The Go binary has no copy
  of this text.
- Confirmed the Go hook is silent as documented: piping a PostToolUse Bash
  payload to `/usr/local/bin/endless-go hook claude` with
  `CLAUDE_CODE_ENTRYPOINT=claude-desktop` and
  `__CFBundleIdentifier=com.anthropic.claudefordesktop` produced no output and
  exit 0.
- Ruled out shell startup files: no `endless` reference in `~/.zshrc`,
  `~/.zshenv`, `~/.zprofile`, `~/.zlogin`.
- Ruled out project hooks: `beyond-vibe-coding/.claude/settings.json` and
  `settings.local.json` define no hooks.
- `~/.claude/settings.json` wires only `claude-log-hook`, `claude-tmux-alert`,
  and `endless-go hook claude`.
- Known Go-to-Python exec paths that could shell out to `endless` and forward
  its stderr: `internal/hookcmd/claude.go` `autoImportTask` (sets
  `cmd.Stderr = os.Stderr`), `internal/triagejob/triagejob.go`, and
  `internal/spawnlaunchcmd/spawn_window.go`. None of these obviously fires on a
  `gh` Bash call, so the actual path is still unidentified.

Get the real emission path before changing anything. Ask the user for the
Desktop session transcript or a fresh reproduction if the code alone does not
show it. Do not guess — E-1962's own history (a detector written against an
incomplete environment dump, which passed every test against an environment that
does not exist) is the cautionary case for guessing here.

## Acceptance

- The emission path is identified and stated, not inferred.
- On an unsupported harness, nothing Endless controls writes the banner into the
  output of a tool invocation that is not an `endless` command. A user typing
  `endless ...` themselves must still get the banner — that is the whole point
  of E-1962 and must not regress.
- A regression test covers the leaking path specifically, not just the
  already-covered "direct `endless` invocation prints the banner" case.

## Product scope

This is a PRODUCT bug, not a local-config bug. It reproduces for anyone who
installs Endless and then opens Claude Code Desktop on any project. Reason about
both modes: Endless managing tasks for another project (the real ledger, one
installed binary) is the mode the report came from; state what the fix does in
`self_dev` too.

## Root cause (confirmed, reproduced)

The user's zsh startup file (sourced from a personal init dir) carries `eval "$(endless shell-init)"` —
the line `endless setup shell-helpers` appends (`src/endless/setup.py`,
`SHELL_HELPERS_EVAL`). So `endless shell-init` runs on **every shell launch**.

Under an unsupported harness, E-1962's refusal lives in the root Click group
callback and therefore fired for `shell-init` too. The banner is written to
**stderr**, which `$( )` does not capture, so it went straight to the terminal —
and inside a Claude Code Desktop Bash tool call, into the result of whatever
command that shell was spawned to run.

Reproduced byte-for-byte before the fix:

```
env CLAUDE_CODE_ENTRYPOINT=claude-desktop zsh -i -c 'echo ---COMMAND-OUTPUT---'
```

Ruled out along the way: the Go hook (verified silent under a Desktop env with a
PostToolUse payload), project-level hooks (`beyond-vibe-coding/.claude/*.json`
define none), and the prompt hook Endless also installs (Go binary, `&>/dev/null`).

## Fix

`HARNESS_EXEMPT_SUBCOMMANDS` in `src/endless/cli.py`, holding `shell-init`;
`_refuse_unsupported_agent` returns early for members. Two properties qualify a
command: Endless wires it into automatic execution, and it has no side effect for
the refusal to withhold. The second is enforced structurally —
`HARNESS_EXEMPT_SUBCOMMANDS <= SANDBOX_SAFE_SUBCOMMANDS`, a set that already
means "pure stdout, no project/global I/O".

`shell-init` prints its snippet unchanged on every harness, so the helpers stay
defined. They all shell out to `endless`, so a deliberate invocation still meets
the banner — E-1962 is intact.

## Both modes

Non-self-dev (the mode the report came from — Endless as a tool for another
project): one installed binary, real ledger, rc line as installed. Fixed.

self_dev: identical. The change is in the Python CLI's group callback, which is
DB- and worktree-independent; `shell-init` touches neither. Nothing about the
sandbox, `--db`, or per-worktree binaries participates.

## Files

- `src/endless/cli.py` — the exemption set, the early return, docstrings.
- `tests/test_agent_env.py` — five tests; the load-bearing one asserts
  `shell-init`'s combined output is byte-equal to the snippet under Desktop.
- `tests/tasks/e-1997-verify.sh` — real `zsh -i` reading a real rc file, with a
  PATH shim so `endless` resolves to this worktree; asserts a shell carrying the
  rc line produces output byte-identical to one without it.
- `CLAUDE.md` — the E-1962 section now documents the carve-out.

## Verification run

`./tests/tasks/e-1997-verify.sh` → 29 passed. Negative control: removing the
early return turns it red (byte-exact pytest assertion).
Project-wide: `go build/vet/test ./...` clean; `just test` 1468 passed.

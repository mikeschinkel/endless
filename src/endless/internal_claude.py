"""Internal headless `claude -p` invocation that fires no Endless hooks (E-1470).

Endless makes a couple of internal `claude -p` calls — the verb-check in
`task_cmd` and the recap summary in `session_cmd`. They run as subprocesses
that inherit the caller's `TMUX_PANE`. Each gets a fresh Claude session UUID
and, left unguarded, fires `endless-hook`; the hook's pane-collision rule then
marks the LIVE caller's session `ended` (same pane string, different UUID),
which breaks session resolution until the caller's next Stop hook revives it.

Routing every internal call through here closes that hole. It:

  * sets ``ENDLESS_NO_HOOKS=true`` so ``endless-hook`` exits before any session
    registration, collision, or SessionEnd work (see cmd/endless-hook/main.go);
  * disables tools (no PreToolUse/PostToolUse surface), MCP servers, and
    session persistence (no throwaway transcript under ~/.claude/projects);
  * runs at low effort, since these are trivial calls.

The full parent environment is forwarded (never a bare dict) so `claude` keeps
its PATH and OAuth/keychain auth — a bare env would strip both and break the
call. `--bare` was rejected for the same reason: it forces ANTHROPIC_API_KEY /
apiKeyHelper auth and never reads OAuth/keychain, which this environment relies
on.
"""

import os
import subprocess

# Set in the child env; checked verbatim by cmd/endless-hook/main.go.
NO_HOOKS_ENV = "ENDLESS_NO_HOOKS"


def run_internal_claude(
    prompt: str,
    *,
    timeout: int,
    model: str | None = None,
) -> subprocess.CompletedProcess[str]:
    """Run a headless, hook-suppressed ``claude -p`` call.

    `model` is a claude alias or full model name; omit it to use claude's
    default. Propagates ``subprocess.TimeoutExpired`` / ``FileNotFoundError``
    to the caller, which decides how to degrade.

    Invokes `claude` via PATH (no shell), so users' `claude` shell wrappers
    are bypassed. Variadic flags (``--tools``/``--mcp-config``) are placed
    before the trailing ``-p <prompt>`` positional so neither consumes the
    prompt.
    """
    argv = ["claude"]
    if model:
        argv += ["--model", model]
    argv += [
        "--effort", "low",
        "--no-session-persistence",
        "--strict-mcp-config",
        "--tools", "",
        "--mcp-config", "",
        "-p", prompt,
    ]
    return subprocess.run(
        argv,
        capture_output=True,
        text=True,
        timeout=timeout,
        env={**os.environ, NO_HOOKS_ENV: "true"},
    )

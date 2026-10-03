# Claude Code Mods: Technical Brief and Potential Uses in Endless

*This is an informational document. It describes what Claude Code mods are, how they work, and ways Endless could potentially use them. It does not prescribe decisions; options and trade-offs are presented for the reader to weigh.*

Claude Code Mods are real: Anthropic launched "mods" as an official Claude Code feature on October 1, 2026, in Claude Code v2.1.287. A mod is a plugin whose `hooks/hooks.json` points at a JavaScript/TypeScript "hooks module." Claude Code runs that module inside its own process, as middleware over almost every internal event (tool calls, prompts, model requests, turns, sessions, UI rendering). For Endless, mods are the first mechanism that can enforce lifecycle gates, inject task context, and report structured telemetry from *inside* every Claude session. They are also unsandboxed: Anthropic's launch blog says "They aren't sandboxed, and you should only install mods from sources you trust." They are also fail-open by default, remotely switchable by Anthropic, and explicitly unstable between releases. Those properties make mods a better fit for a defense-in-depth and observability role layered on top of Endless's Go CLI than for being the place where Endless's gates actually live.

## TL;DR

- **Mods exist and are official.** Anthropic announced them on its blog on October 1, 2026, and the docs live at code.claude.com/docs/en/plugins/mods. They need Claude Code v2.1.287+ and are on by default. They are TypeScript/JavaScript event handlers shaped like `($, e, next)`, shipped inside ordinary plugins, and they can observe, rewrite, or replace tool calls, prompts, model requests, commands and UI. Separately, "claude-mods" is also the name of several unrelated community repos, such as 0xDarkMatter/claude-mods, which is a conventional skills/hooks plugin and not an official Anthropic feature.
- **What this could mean for Endless.** One Endless plugin could bundle skills, commands, the Endless MCP server config, settings hooks, and a mod. `endless task spawn` could load it per session with `--plugin-dir` or `CLAUDE_CODE_PLUGIN_DIRS`, so the sandboxed config dirs and 50+ concurrent sessions would not need a user-scope install. A mod could draw a task-status band, block `land`/merge/push before verification, log per-turn token usage to the ledger, and add commands that run instantly and cost zero tokens.
- **Mods coexist by cooperation, not isolation.** All mods handling an event share one middleware chain and one worker thread. They work together only as long as each one is a "good citizen" (see section 8). Endless can follow those practices but cannot force user mods to, which is a reason not to make Endless's correctness depend on its mod.
- **A possible staged path.** (1) Package the existing integration as a plain plugin. (2) Add a read-only observability mod. (3) Add fail-closed gate mods that mirror, rather than replace, gates enforced in the Go binary. (4) Experiment with token routing and adversarial-review features. Each stage is optional and independent of the later ones. Testing against specific Claude Code versions is possible with `claude plugin validate --strict --json` and `claude plugin test`.

## Verify Before Implementing

This brief was researched two days after mods launched. The following points were not confirmed during research and are worth checking before building anything based on this brief. Each is also discussed where it arises below.

1. **Endless CLI commands in the samples are placeholders.** `endless task current --json` and `endless event append ...` are invented for illustration. They do not correspond to documented Endless commands.
2. **Event and API field names.** Event fields and `$` methods used in the samples have not all been checked against `.claude-plugin/types/claude-code/index.d.ts`, which Claude Code writes for the installed build. Those files are authoritative over this brief and over the online docs. Some fields (e.g. `prompt.context`'s `blocks` shape) were not fully verified.
3. **Config-dir isolation.** It is unconfirmed whether Endless sandboxes change `CLAUDE_CONFIG_DIR` or only `XDG_CONFIG_HOME`. This determines whether user-scope plugin installs, `$.store`, `dev-mods` and trust settings are shared with or hidden from sandboxed sessions.
4. **Mods may be off.** `claude --version` (≥2.1.287 needed) and `claude plugin test` show whether mods can load on a given machine. Mods can be disabled by the stable release channel lagging, a remote rollout switch, `--safe-mode`/`--bare`, `disableAllHooks`, or org policy.
5. **Coexistence behavior.** Section 8's statements (chain ordering, shared-worker crash handling, command-name collisions) come from the docs and have not been tested.

## Key Findings

### 1. Verification: is "Mods" an official feature?

Yes. The primary-source chain is clear and consistent:

| Date (2026) | Event | Source type |
|---|---|---|
| Sep 3 | Anthropic engineer opens design thread anthropics/claude-code#91870 ("Mods - make Claude 10x more extensible") proposing "function hooks" behind `CLAUDE_CODE_ENABLE_FUNCTION_HOOKS=1` | Primary (Anthropic GitHub) |
| Sep 9 | Thread update says Anthropic is "now committed to shipping function hooks, on the scale of weeks." It adds: "from a product perspective, we are going to be calling this functionality 'Claude Mods'... A mod is just a plugin that uses function hooks." Source of the first built-in mods is published under `mods/` in anthropics/claude-code | Primary |
| Sep 14 | Boris Cherny (Claude Code team) posts on X: "Claude Mods are landing now" | Primary (Anthropic staff) |
| Oct 1 | Claude Code v2.1.287 released. Changelog entry: "Added Claude Mods: plugins may now modify deeper behavior." Blog post "Customize Claude Code with mods" and full docs go live | Primary |
| Oct 2 | v2.1.288 released | Primary (GitHub releases) |
| Oct 3 | Issue #99130 reports mods served *off* by a remote "rollout switch" on v2.1.288 for some accounts | Primary (user bug report) |

The terminology matters. "Mod" is the product name. "Function hook" is the engineering primitive. The docs say "hook" for a mod's handler and "settings hook" for the classic shell/HTTP/prompt hooks configured in `settings.json`.

**Community projects with similar names.** Several GitHub repos are named "claude-mods." Besides those listed below, they include DominickGiordano/claude-mods (pr-watch, meter, quick; "These need Claude Code 2.1.287 or later"), devohmycode/claude-mods (a cockpit pane) and empire/claude-mods (tictactoe):

- **0xDarkMatter/claude-mods** is a conventional plugin of skills, agents, commands, rules, hooks and output styles. It predates mods and is not a mod framework.
- **Sma1lboy/claude-mods, thieung/claude-mods and ccdwyer/claude-mods** are genuine mod collections. thieung's includes a `worktree-guard`; ccdwyer's includes `loop-breaker` and `red-squiggle`.
- **karanb192/claude-code-mods** provides a `mod-builder` skill.
- **claudemods.ai** is an independent, community-voted catalog.
- **aitmpl.com** (davila7/claude-code-templates) offers a `--mod` installer.

Several of these still describe the early-access setup. For example, davila7/claude-code-templates release v1.29.6 says mods shipped "behind an early-access flag in Claude Code >= 2.1.259" and "are behind CLAUDE_CODE_ENABLE_FUNCTION_HOOKS=1". A PR in the same repo was "verified on Claude Code 2.1.272". empire/claude-mods still types against "Claude Code 2.1.278" and says "Function hooks are early access." That is out of date: v2.1.287+ ignores the variable, so setting it to `0` does *not* turn mods off.

### 2. What mods are

**Definition.** The docs define a mod as "a plugin that changes how Claude Code looks and behaves. It's made of JavaScript or TypeScript event handlers: Claude Code calls one when an event happens... and the handler can watch the event, change it, or take it over."

**Purpose.** In Anthropic's words, "Hooks helped give users some of this control, but hooks can't rewrite events, draw new UI, or replace features. Mods can." Anthropic also says it plans to "move more built-in features to mods over time, so you can pare Claude Code down to a small core."

**Built-in mods.** Some built-in features already ship as mods. Anthropic's @ClaudeDevs launch post on X (October 1, 2026) says: "We've used mods to build features like /diff and AGENTS.md support."

| Mod | What it does |
|---|---|
| `cc-plugin-agents-md` | AGENTS.md support |
| `cc-plugin-diff` | The `/diff` pane |
| `cc-plugin-sec-default` | Security guard |
| `cc-plugin-telemetry` | Telemetry |
| `cc-plugin-plugin-authoring` | Skill only |
| `cc-plugin-you-should-know` | Opt-in side agent |

Source and tests for four of them (diff, agents-md, sec-default, telemetry) are public under `mods/` in anthropics/claude-code.

**Availability.**

| Surface | Hooks run | Mod UI appears |
|---|---|---|
| `claude` in a terminal (incl. editor terminals, JetBrains) | Yes | Yes |
| Desktop app Code tab (non-WSL) | Yes | Yes, except terminal-only elements |
| Desktop WSL session | No (plugins unavailable) | No |
| VS Code extension chat panel | Yes | No |
| `claude -p` and Agent SDK | Yes | No |
| Remote Control from claude.ai/mobile | Yes, on your machine | In your terminal |
| Cloud sessions | Yes, if the plugin reaches the session | No |

**Plan tiers.** No plan gating is documented for mods themselves. On Team and Enterprise plans, and on any machine with managed settings, the built-in `sec-default` guard loads first. It stops user-installed mods from overriding deny rules or tampering with managed hooks, managed CLAUDE.md, the system prompt and managed MCP servers.

### 3. Comparison of Claude Code extension mechanisms

| Mechanism | What it is | Runs where | Can block/rewrite? | Can draw UI? | Token cost | Possible Endless use |
|---|---|---|---|---|---|---|
| **Mod** | JS/TS functions in a plugin's hooks module | In Claude Code's process (shared hooks worker) | Observe, rewrite, or answer almost any event, including model requests and UI | Yes (panes, band above prompt, restyled rows, spinner) | Zero for local logic/UI. `$.model.*` calls bill the user's plan | Gates, telemetry, status band, instant commands |
| **Settings hook** | Shell command, HTTP call, prompt or agent on lifecycle events (PreToolUse, Stop, etc.) | Separate process | Block/allow, edit tool args/results, add context | No | Zero (prompt/agent hooks cost tokens) | Portable gates that work on every surface and version |
| **Plugin** | Distribution unit: manifest + any components | n/a | Via its components | Via mods | n/a | Packaging Endless's whole integration as one installable unit |
| **Skill (SKILL.md)** | Markdown instructions loaded on demand | Model context | No (guidance only) | No | Description always, body when invoked | Planning, handoff and verification-script conventions |
| **Subagent** | Separate agent with its own prompt/tools | Child context | Via its tools | No | Its own context | Adversarial verifier, reviewer |
| **Slash command** | Prompt template (now skills as flat .md) | Model turn | No | No | A full turn | Human-initiated workflows |
| **Mod command** (`$.command.register`) | Function run on `/name` | Mod | n/a | Yes | **Zero** (no Claude turn); `immediate: true` runs even mid-turn | `/endless-status`, `/endless-ready` |
| **MCP server** | External process exposing tools | Separate process | No (offers tools) | No | Tool schemas in context | Endless's existing Go MCP server |
| **Output style** | System-prompt modifier | Model context | No | No | Prompt tokens | Terse "quiet unless a decision is needed" reports |
| **CLAUDE.md / AGENTS.md** | Instruction files | Model context | No | No | Always loaded | Project conventions |
| **settings.json** | Permissions, env, hooks, enabledPlugins | Config | Permission rules | No | None | Per-sandbox permissions |
| **Status line** | Script output under the prompt | Separate process | No | One line | None | A mod band can show richer state |

The core distinction is this. Settings hooks, skills and MCP servers "work from outside Claude Code," while a mod "runs inside Claude Code." A plugin can hold all of them at once.

## Details

### 4. How mods work

**File layout.** The minimum is three files:

```text
endless-cc/
├── .claude-plugin/
│   ├── plugin.json        # manifest (mods add no required fields)
│   └── types/             # written by Claude Code on load: .d.ts for YOUR build
├── hooks/
│   ├── hooks.json         # "modules": ["./register.ts"]  (+ optional classic "hooks")
│   └── register.ts        # the hooks module: export function register(on, options)
├── skills/ ...            # optional, normal plugin components
├── .mcp.json              # optional
└── tests/endless.test.ts  # optional, run by `claude plugin test`
```

`hooks.json` must contain a `modules` array with one path, relative to the file. That key "is what makes the plugin a mod." The same file can also hold classic settings hooks under `hooks`. Accepted module extensions are `.js .mjs .cjs .jsx .ts .mts .cts .tsx`, and modules must be ES modules. No Node.js, bundler or build step is needed.

**The hook signature.** `register(on, options)` is called on load. `options` holds the manifest's `userConfig` values. Handlers are registered with `on(event, [matcher], async ($, e, next) => ...)`:

- `$` is the mods API. It is the only way to reach files, processes, network, UI, models or other sessions.
- `e` is the event, deeply frozen. To change it, a handler passes a copy to `next`.
- `next(e)` is the middleware continuation. It runs later mods and then Claude Code's own behavior.
- `next.signal`, `next.origin`, `next.budget` and `next.to(e, tier)` are also available. `next.to` is for managed prepend/append mods only.

A handler can respond in one of three ways:

- **Observe:** do work, then `return next(e)`.
- **Rewrite:** `return next({...e, text})`.
- **Answer:** return a result without calling `next`, such as `{ deny: reason }` or `{ result }`.

**Event surface (as of v2.1.287).**

| Group | Events |
|---|---|
| Tools | `tool.call`, `tool.check` (final allow/ask/deny after rules and hooks), `tool.describe` |
| Prompts | `prompt.submit`, `prompt.compose`, `prompt.section`, `prompt.context`, `prompt.attachment`, `skill.prompt`, `attribution.text` |
| Commands and config | `command.run`, `command.describe`, `config.set`, `config.describe` |
| Turns | `turn.start`, `turn.step` (each model request, async generator, can switch `model`/`effort`), `turn.complete` (with `usage`, `durationMs`, `isAborted`) |
| Session | `session.start`, `session.end`, `session.compact`, `session.receive`, `session.send`, `session.append`, `session.measure`, `session.attach`, `session.detach` |
| Subagents | `agent.offer`, `agent.spawn` (can set `model` or deny) |
| UI | `ui.render`, `ui.press`, `ui.input`, `ui.select`, `ui.close`, `ui.message` |
| Other | `plugin.register`, `engine.create` (act on other mods); `telemetry.*` |
| Compatibility | `classic.<Event>` mirrors every settings-hook event (e.g. `classic.Stop`, with the same stdin JSON) |
| API calls | Every `$` call is itself an event (`fs.write`, `process.run`), so earlier mods can audit or refuse later mods' calls |

**Mods API namespaces.** `$.ui`, `$.command`, `$.tool`, `$.agent`, `$.model` (`complete`, `fork`, `classify`), `$.prompt` (`submit`, `read`, `fill`...), `$.turn.abort`, `$.session` (`messages`, `cwd`, `id`, `usage`, `send`...), `$.config`, `$.settings`, `$.env`, `$.fs`, `$.store`, `$.state`, `$.clock`, `$.http`, `$.process`, `$.mcp`, `$.audio`, `$.telemetry`.

**Ordering and precedence.** Handlers for the same event form one middleware chain. As Anthropic's blog puts it, "The first mod to load sees the event first and the result last." The order is:

1. The `sec-default` guard (where it loads), managed `prependPlugins`, and other organization mods.
2. User-installed mods. Among these, a mod runs before the mods it lists under `dependencies`.
3. Managed `appendPlugins`.
4. Other built-ins.

Settings hooks have fixed slots in this chain:

- **Managed `PreToolUse` hooks** run *before* every mod, and their block is final.
- **All other `PreToolUse` hooks** (user/project settings and plugin `hooks.json`) run *after* the last mod calls `next`. A mod that answers a tool call without calling `next` therefore suppresses them.

**Plugin scopes.** Plugin install scopes (user `~/.claude/settings.json`, project `.claude/settings.json`, local `.claude/settings.local.json`) decide where a mod is enabled. Local overrides project, which overrides user. `prependPlugins`/`appendPlugins` can be set only in managed settings. A user can set them in user settings only on a machine with no managed settings and no Team/Enterprise sign-in. Repositories can never set them.

**Lifecycle.**

- `session.start` fires once per loaded mod, before the first prompt and again after a reload. It does *not* fire after `/clear`, `/resume` or `/branch`; `classic.SessionStart` with `source: 'clear'` covers those.
- `--plugin-dir` directories hot-reload on save.
- Mods Claude writes live in `~/.claude/dev-mods/<session-id>/` and reload at the end of each turn once hot reloading is approved.
- Installed plugins are cached by version, so edits need a version bump and reinstall.
- Module-level variables reset on every reload. State can persist in `$.state` (reactive, per-session) or `$.store` (on disk).

**Limits.**

| Limit | Value |
|---|---|
| Hook's own CPU time per event (excludes time inside `next` or `$` calls) | 10 s |
| `.catch` handler | 1 s |
| All `session.end` hooks together | 1.5 s |
| `$.process.run` timeout | 30 s default, 10 min max |
| `$.fs` file size | 4 MiB per file |
| `$.store` | 4 MiB total, shared across all sessions on the machine; `get`+`set` not atomic |
| `$.session.messages()` | Newest 4,096 entries |
| Redraws | Throttled to 10/s |
| Test timeout | 5 s per test |

**Failure semantics: fail-open by default.** A hook that throws, times out or returns a malformed result *before* calling `next` is skipped, and the next handler runs instead. A blocking guard therefore lets the action through unless it has a `.catch(...)` returning `{ deny }`. Installed mods share one worker thread. If it crashes three times without a single culprit, Claude Code unloads every non-built-in mod for the session until `/reload-plugins`.

**Security model.** Mods "aren't sandboxed." They can read and write any file the user can, read env vars and settings (including API keys), see and rewrite every prompt and tool call, approve tool calls before the user is asked, message other sessions, and spend the user's model quota. Claude Code's Bash sandbox does not cover processes a mod starts.

A few hard boundaries remain:

- Mods cannot restyle the permission prompt.
- Where `sec-default` loads, a user mod cannot override `deny` rules.
- Managed `PreToolUse` blocks are final.

`claude plugin validate <dir>` statically lists a mod's `hooks:` and `calls:` without running it, which allows review before installation. Static analysis is enforced: `$` calls must be literal, so Claude Code refuses modules whose calls it cannot read.

**Trust and loading conditions.**

- In an interactive session, no mod loads in a directory until the user accepts the workspace trust prompt. Trust is keyed on the git repository root, and in a worktree "it uses the main checkout's root." Fresh Endless worktrees therefore inherit trust from the main checkout.
- `claude -p` never shows the trust dialog.
- `--plugin-dir` mods do run under `-p`.
- Mods Claude writes do not load in `-p` or `dontAsk` mode.
- Neither do mods under `--safe-mode`, `--bare`, `disableAllHooks`, or org policy.

**Context and token implications.** Local mod logic and UI cost no model tokens. Mod commands skip the Claude turn entirely. Text injected through `prompt.submit` context, `prompt.section` or `skill.prompt` is read by the model. If that text changes between requests, it "invalidates the prompt cache." `$.model.complete`, `$.model.fork` and `$.agent.spawn` bill the user's plan or API key.

**Versioning and distribution.** A mod is versioned in `plugin.json` like any plugin and is distributed through marketplaces (a git repo or directory with `.claude-plugin/marketplace.json`) or the Claude directory. Users install with `/plugin install name@marketplace` or `claude plugin install --scope user|project|local`. `claude plugin validate` fails names that look like Anthropic's own (e.g. starting with `claude-`). The `.d.ts` files Claude Code writes into `.claude-plugin/types/` on each load "describe the exact events, mods API methods, and elements in the Claude Code version you're running... trust these files over any page."

**Enable/disable.**

- **One mod:** `/plugin`, Installed tab.
- **All installed mods for one session:** `--safe-mode`.
- **All installed mods everywhere:** `"disableAllHooks": true`, which also stops settings hooks and the status line.
- **Admins:** `allowManagedModsOnly` (on the `cc-plugin-sec-default@builtin` guard), `allowManagedHooksOnly`, `disableSideloadFlags` (rejects `--plugin-dir`, `--plugin-url`, `CLAUDE_CODE_PLUGIN_DIRS`), `strictKnownMarketplaces`.
- **Anthropic:** can turn installed mods off remotely. `claude plugin test` then reports "hooks modules are turned off in this process," and "No setting on your machine turns them back on" (issue #99130, October 3, 2026).

### 5. How mods are built: an illustrative walkthrough

The steps below show how an Endless mod could be built, using the platform's documented workflow. The Endless-specific parts are illustrative only.

**Step 1: Prerequisites.**

```bash
claude --version            # need >= 2.1.287
cd /tmp && claude plugin test   # "no hooks module to load" => mods can load here
```

Release channels matter here. On October 2 the npm `stable` tag still pointed at 2.1.285, while `latest`/`next` pointed at 2.1.287. Stable-channel users may not have mods yet.

**Step 2: The manifest.** An example `.claude-plugin/plugin.json`:

```json
{
  "name": "endless-cc",
  "version": "0.1.0",
  "description": "Endless task integration: lifecycle gates, task status band, ledger telemetry",
  "author": { "name": "Endless contributors" },
  "userConfig": {
    "endless_bin": { "type": "string", "title": "Endless binary", "description": "Path to the endless CLI", "default": "endless" }
  }
}
```

**Step 3: `hooks/hooks.json`.** It names the module and can keep a portable classic hook alongside it:

```json
{
  "description": "Endless hooks module plus a portable fallback gate",
  "modules": ["./register.ts"],
  "hooks": {
    "Stop": [{ "hooks": [{ "type": "command", "command": "${CLAUDE_PLUGIN_ROOT}/bin/endless-stop-gate" }] }]
  }
}
```

In this arrangement the classic `Stop` hook still works on surfaces or versions where mods don't load, and the mod adds richer behavior where they do.

**Step 4: The hooks module.** The sketch below is illustrative. The `endless` subcommands and `--json` flags are placeholders, and event fields have not all been checked against `.claude-plugin/types/claude-code/index.d.ts`.

```typescript
// hooks/register.ts
import type { Register } from 'claude-code'

let task: { id: string; status: string; verified: boolean } | null = null

// Top-level helper: static analysis allows passing $ to a top-level function
async function refresh($, bin: string) {
  try {
    const r = await $.process.run([bin, 'task', 'current', '--json'])   // placeholder CLI
    if (r.exitCode === 0) task = JSON.parse(r.stdout)
  } catch { /* keep last known state */ }
  $.ui.invalidate('ui.render')
}

export const register: Register = (on, options) => {
  const bin = options.endless_bin ?? 'endless'

  on('session.start', async ($, e, next) => {
    await refresh($, bin)
    $.clock.every(30_000, () => refresh($, bin))           // poll the ledger projection
    try {
      // Zero-token command; runs even while Claude is mid-turn
      await $.command.register({ name: 'endless-status', description: 'Show this task\'s Endless status', immediate: true })
    } catch { /* name collision: skip */ }
    return next(e)
  })

  // Gate: no land/merge/push to main until the task is verified
  const LAND = /\bendless\s+task\s+land\b|\bgit\s+(merge|push)\b/
  on('tool.call', { tool: 'Bash' }, async ($, e, next) => {
    if (!LAND.test(e.command)) return next(e)
    await refresh($, bin)
    if (task && !task.verified) {
      return { deny: `Task ${task.id} is '${task.status}' and has no confirmed verification. ` +
                     `Write the verification script and stop; the human will verify and land.` }
    }
    return next(e)
  }).catch(async () => ({ deny: 'Endless gate failed; refusing land/merge/push to be safe.' }))  // fail closed

  // Structured telemetry: one ledger event per turn, nothing shown to the user
  on('turn.complete', async ($, e, next) => {
    const result = await next(e)
    if (!e.agentId && task) {
      try {
        await $.process.run([bin, 'event', 'append', '--task', task.id, '--kind', 'turn',
          '--json', JSON.stringify({ usage: e.usage, durationMs: e.durationMs, aborted: e.isAborted })])
      } catch { /* never break the turn for telemetry */ }
    }
    return result
  })

  on('command.run', { command: 'endless-status' }, async () =>
    ({ text: task ? `${task.id}: ${task.status}${task.verified ? ' (verified)' : ''}` : 'No Endless task bound to this session' }))

  // Band above the prompt: skimmable task state, preserving other mods' bands
  on('ui.render', { component: 'AbovePrompt' }, async ($, e, next) => {
    if (!task) return next(e)
    const { Box, Text } = $.ui.resolve(e)
    const others = await next(e)
    return Box({ flexDirection: 'column', children: [
      Text({ dimColor: true, children: [`endless ${task.id} · ${task.status}${task.verified ? ' · verified' : ''}`] }),
      ...(others ? [others] : []),
    ] })
  })
}
```

**Step 5: Loading and iterating.**

```bash
claude --plugin-dir ./endless-cc            # hot-reloads register.ts on save
claude plugin validate ./endless-cc --strict --json   # lists hooks: and calls:, fails on warnings
claude -p "/endless-status" --plugin-dir ./endless-cc # headless smoke test
claude --debug-file ./mod.log --plugin-dir ./endless-cc   # skip/refusal reasons
```

**Step 6: Testing without a session.** `claude plugin test` runs `*.test.ts` files with no session, sign-in or network, and exits 1 on failure, so it works in CI. Stubs have to be registered before the first `$` call.

```typescript
// tests/gate.test.ts
import { expect, test } from 'claude-code/testing'

test('land is refused while unverified', async ($, on) => {
  on('process.run', () => ({ value: { exitCode: 0, stdout: '{"id":"E-123","status":"unverified","verified":false}', stderr: '' } }))
  on('tool.call', () => ({ result: 'ran' }))           // stands in for Claude Code
  const out = await $.tool.call({ tool: 'Bash', command: 'endless task land E-123' })
  expect(out.deny).toMatch(/E-123/)
})
```

**Step 7: Packaging and publishing.** A `.claude-plugin/marketplace.json` in a repo (Endless's own, or a dedicated `endless-plugins` repo) lists the plugin by relative path. Users then run `/plugin marketplace add mikeschinkel/endless` and `/plugin install endless-cc@<marketplace-name>`. Because installs are cached by version, each release needs a `version` bump in `plugin.json`. Stating the tested Claude Code version range in the README is common practice.

**Documented practices and gotchas.**

- **Static-analysis rules.** Every call is written in full (`$.ns.method(...)`). Aliasing, destructuring or computing names on `$` is refused. Event names must be string literals. Imports must be relative and static. Each event is registered once per matcher.
- **Failing closed is opt-in.** Guards without `.catch` fail open on throw or timeout.
- **Waits belong inside `$` calls.** Awaiting the mod's own promises counts against the 10 s budget. A timed-out guard is skipped, so the held action runs.
- **`$.command.register` throws on a taken name.** It also aborts the rest of that `session.start` hook, which is why command registration is usually done last or wrapped in `try`.
- **`$.ui.ask` rejects in `claude -p`.** A safe default answer is needed for headless runs.
- **Shared, non-atomic storage.** `$.store` and `$.fs.write` are shared machine-wide and not atomic, so they are not a concurrency-safe database across 50 sessions.
- **Known plugin-system issues.** Project/local-scope install bookkeeping has had bugs (#14185, #26513). Sensitive `userConfig` persistence has had bugs (#62442). `userConfig` cannot currently be supplied to cloud sessions (#79124).

### 6. Potential uses in Endless

Each item below maps a mod capability to a possible Endless need. Ratings: **Value** relative to Endless's stated goals; **Risk** from API churn or blast radius.

**A. One installable integration unit (Value: high, Risk: low).** Endless's Claude Code integration could be packaged as a single `endless-cc` plugin containing:

- skills for filing tasks, planning, handoff and verification-script conventions
- commands
- `.mcp.json` launching the Endless Go MCP server
- portable settings hooks
- optionally, a mod

This does not depend on mods: plugins already namespace skills (`/endless-cc:plan`) so they don't collide with users' setups. It fits "start where you are," because non-Endless sessions could install it from a marketplace too.

**B. Per-sandbox loading instead of user-scope install (Value: high, Risk: low).**

- **Why a user-scope install may not reach sandboxes:** Claude Code stores plugins under its config directory: "All settings, session history, and plugins are stored under this path" for `CLAUDE_CONFIG_DIR`. A sandbox with its own Claude config dir would not see a user-scope install.
- **An alternative:** `endless task spawn` could pass `--plugin-dir /path/to/endless-cc`, or set `CLAUDE_CODE_PLUGIN_DIRS` (absolute paths only, v2.1.280+). That loads the plugin as `endless-cc@inline` for that session only and writes nothing to settings.
- **Properties of that approach:** the plugin loads in place, so it stays pinned to the Endless binary's version with no install cache to go stale. It works under `claude -p` for future background tasks, and concurrency stays simple.
- **Name clash:** an inline plugin silently overrides a same-named marketplace install.
- **Options:** `userConfig` options for inline plugins are keyed `endless-cc@inline` under `pluginConfigs`. That key is read only from user, `--settings` or managed settings, so spawn would need to pass the options with `--settings`.
- **Uncertainty:** Endless isolates via a temp `XDG_CONFIG_HOME`, but the docs describe Claude Code's own relocation knob as `CLAUDE_CONFIG_DIR`. Which variable Endless actually swaps is unconfirmed. It is also unconfirmed whether `$.store` (`~/.claude/plugins/store/`) and `dev-mods` follow `CLAUDE_CONFIG_DIR`.

**C. Lifecycle gates as defense in depth (Value: high, Risk: medium).** A `tool.call` handler could refuse:

- `endless task land`, `git merge` and `git push` until the ledger says verified
- edits outside the task's worktree, by comparing `e.file_path` to `$.session.cwd()`
- status transitions the agent is not meant to make, e.g. `endless task approve` from inside a session, since `ready` is meant to mean human-approved

Claude reads `deny` text as the tool result, so actionable wording tends to work best. Pairing guards with `.catch` handlers that deny makes them fail closed.

This aligns with "gates over guidelines." There are five reasons to keep the authoritative gate in the Go CLI (e.g. `land` refusing an unverified task) rather than in the mod:

1. Mods fail open unless carefully written.
2. They can be disabled by `--safe-mode` or `disableAllHooks`.
3. Anthropic can switch them off remotely.
4. They are unloaded after three worker crashes.
5. A user-installed mod that loads earlier can answer the call first. Endless cannot claim the outermost position without managed `prependPlugins`.

In that arrangement, the mod's contribution is earlier, cheaper feedback inside the agent loop rather than enforcement.

**D. Handoff and context injection (Value: medium, Risk: medium).** A mod could re-inject a compact task header after compaction or `/clear`: `session.compact` signals compaction, and `classic.SessionStart` with `source: 'clear'` covers rehydration. `prompt.submit`'s `context` array is read only by Claude. Byte-stable text avoids invalidating the prompt cache.

The same mechanism could supply current task statuses on every prompt, read fresh from Endless's DB, so Claude works from current state rather than from what it remembers (statuses that other sessions may have changed). Changing text breaks the prompt cache, so a short injected block attached to the user's message works better than text inserted into earlier context: only the newest part of the conversation is then uncached, not the whole prefix. Classic `UserPromptSubmit` settings hooks can also add context and work without mods, which makes them a lower-risk first option.

The generated handoff prompt in `endless task spawn` already works on every surface, which is an argument for leaving it where it is.

**E. Ledger telemetry and per-task cost accounting (Value: high, Risk: low).**

- `turn.complete` exposes `usage`, `durationMs`, `isAborted` and `agentId`.
- `turn.step` exposes per-request cache read/write tokens.
- `session.measure` and `$.session.usage()` report context fill and rate-limit percentages.

These could be appended as typed events to the JSONL ledger through the Endless CLI or MCP server. Since the ledger is the source of record, `endless session monitor` could then show per-task tokens, cost, context fill and "stuck" signals across 50+ sessions without parsing transcripts. `$.store` is a poor fit as the transport: it is 4 MiB total, shared machine-wide, and non-atomic. `$.mcp.call` and `$.mcp.connect` only reach servers the plugin's own manifest lists, which would suit an Endless-bundled MCP server.

**F. Quiet, skimmable status in every session (Value: high, Risk: low).** An `AbovePrompt` band could show task ID, status and verification state. `$.ui.status` can hold a one-line "needs decision" marker. `immediate: true` commands (`/endless-status`, `/endless-blockers`) could answer from the ledger at zero token cost, even mid-turn. Panes opened without user action only appear at ≥144 columns, so a band is more likely to be visible in tmux splits. This lines up with "agents stay quiet unless a decision is needed": humans get state without spending an agent turn.

Endless's session status is usually several lines (2–10, occasionally far more), which suits a pane better than the band. A mod pane could serve as the persistent session monitor: it stays open and redraws as state changes, and a mod command can open it when the terminal is under 144 columns. Compared with a sibling tmux pane, the difference is that a mod pane lives inside each Claude session's process. Endless currently limits monitor CPU by running the monitor only in the visible tmux window and letting other windows sleep. A mod pane would need an equivalent: for example, rendering only state Go has already computed, and doing no work when the session isn't visible. The `session.attach`/`session.detach` events may help detect visibility, but whether they track tmux window visibility is unverified.

**G. Verification and adversarial review (Value: medium, Risk: medium-high).**

- `$.model.fork({prompt})` asks one question over the current conversation, served mostly from the prompt cache. It could serve as a cheap "did you actually run the verification script?" check before the session reports `unverified`.
- **Fanning out checks over one text.** For a block of text such as a plan, a session could read it once and then run several `$.model.fork` checks in parallel. Each fork shares the conversation as a cached prefix, so the plan is processed once and each check pays mainly for its own question and answer.
  - **Cache warming:** the cache entry is written by the first request that completes. If all N forks start at the same instant, they can all miss the cache and each pay full price. Reading the plan in the session, or running one fork first, before launching the rest in parallel (`Promise.all`) avoids that. The cache expires after a few minutes by default.
  - **Forks have no tools:** a fork is a single model call, so it cannot run commands or read files. Checks that need outside data could have the mod gather it first (via `$.process.run` of the Endless CLI or `$.fs`), such as a diff, test output or task rows, and include the results in each fork's prompt. Deterministic checking then happens in Go and the fork does only the judgment. Time spent waiting on those calls does not count against the 10 s CPU budget, and `$.process.run` allows up to 10 minutes.
- `agent.spawn` can force a different model for a reviewer subagent. `agent.offer` can withhold subagent types during implementation.
- True adversarial verification could remain a separate Endless task/session, consistent with "every task is a real steerable session," with the mod only triggering it and recording the result. All of these spend the user's quota.

**H. Token-cost reduction (Value: medium, Risk: high).**

- `turn.step` can route specific requests to a cheaper model: `next({...e, model})`.
- `tool.call` can answer repeated read-only calls from a cache.
- `prompt.attachment` can drop reminder messages.
- `tool.describe` can shorten tool descriptions.

These are powerful but invasive. They change model behavior in non-obvious ways and can break prompt caching, which makes them better suited to opt-in experiments measured with the telemetry from item E.

**I. Cross-session coordination (Value: medium, Risk: medium).** `$.session.send` and `session.receive` reuse Claude Code's cross-session messaging (v2.1.224+, macOS/Linux, local socket). A session could tell the monitor or a parent task "needs decision," and an inbound message can be `consumed` before Claude reads it. Endless already uses tmux for inter-session messaging, so this would be an alternative transport rather than a replacement. Bypass-mode sessions hold inbound messages for approval by default.

**J. Mods and the multiplexer roadmap (Value: low now).** Mod UI appears only in the terminal and the Desktop Code tab. A future web console or TUI would most likely read the ledger rather than mod UI.

**K. Background/unattended tasks (Value: medium, Risk: medium).** Under `claude -p`, `--plugin-dir` mods run but draw nothing, and `$.ui.ask` rejects. A gate that would normally ask would need to deny instead when there is no UI; `$.session.surfaces()` or catching the rejection detects this. Telemetry from item E would be the main window into background sessions, which increases its value.

### 7. Trade-offs and risks

- **Vendor lock-in.** The mods API exists only in Claude Code. Endless's other planned backends (multiplexers, JIRA/Notion) and any future agent harness will not have it. One mitigation is keeping the TypeScript a thin shim that shells out to, or MCP-calls, the Go binary, so logic and state stay in Go and the ledger.
- **API stability.** The docs say events "can change between releases," and the authoritative contract is the per-build `.d.ts` file. Community trackers note the declarations run to roughly 13k lines. Common mitigations are pinning a tested Claude Code range, regenerating types in CI, and running `claude plugin test` against each new release.
- **Availability is not guaranteed.** Mods can be off because of a remote rollout switch (issue #99130), the stable channel lagging (2.1.285 on Oct 2), `--safe-mode`/`--bare`, org policy (`allowManagedModsOnly`), or worker crashes. This argues for any mod feature degrading to "Endless still works," and for the portable plugin and classic hooks in item A not depending on mods.
- **Conflicts with users' setups.** See section 8.
- **Security posture.** An Endless mod would run with full user privileges in every session. A narrow `calls:` surface (`$.process.run` of the Endless binary, `$.ui.*`, `$.command.*`) keeps `claude plugin validate` output easy to audit. `$.http`, `$.env.set` and `$.prompt.submit({asUser:true})` widen that surface considerably.
- **Scale.** Each of 50+ sessions loads its own copy of a mod. Coarse polling intervals (≥30 s) and pushing events through the ledger reduce load on SQLite. `$.store` is shared across *all* sessions on the machine.

### 8. Composability and coexistence with other mods

**Model: cooperation, not isolation.** Mods do not run in separate sandboxes. Every mod handling a given event sits in one middleware chain, and each handler decides whether to pass the event on by calling `next`. Mods coexist the way applications coexist on a shared server: fine as long as each stays in its lane, doesn't claim a name someone else owns, and doesn't do anything hostile. One difference from separate applications: all installed mods run in a single shared worker thread, so they are closer to plugins in one shared process than to separate programs.

**Contrast with commands and skills.** Plugin skills are namespaced by plugin (`/endless-cc:add-task` vs `/other-plugin:add-task`), so they coexist under distinct prefixes. Mod commands registered with `$.command.register` share one flat namespace: registering a name that is already taken throws.

**How one mod can interfere with another (either direction):**

- **Short-circuiting.** A handler that answers or denies an event without calling `next` hides it from every mod later in the chain, and from the settings hooks that run after the last mod. Whoever loads first wins. Endless cannot claim the first slot without managed `prependPlugins`, which only admins (or users on unmanaged machines) can set.
- **Rewriting.** An earlier mod can change an event (a tool command, a prompt) before a later mod sees it.
- **UI replacement.** A `ui.render` handler that doesn't include `await next(e)` in its output erases other mods' bands and elements.
- **Command names.** A taken name makes `$.command.register` throw; uncaught, that aborts the rest of that `session.start` hook.
- **Shared storage.** `$.store` is one 4 MiB space shared by every mod on the machine, with non-atomic `get`+`set`.
- **Shared worker.** If the shared worker crashes three times with no single culprit, Claude Code unloads every non-built-in mod for the session. A buggy neighbor can take another mod down with it. Behavior when a single culprit is identified is undocumented here and untested.
- **Mods acting on mods.** `plugin.register` and `engine.create` let a mod act on other mods. Every `$` call is itself an event, so an earlier mod can audit or refuse a later mod's file, process or network calls.
- **Name override.** An inline `--plugin-dir` plugin silently replaces a marketplace install with the same name.

**Good-citizen practices an Endless mod could follow:**

1. **Observing by default.** Always calling `next`, and answering or denying only for a few narrowly matched gates (land, merge, push to main; edits outside the worktree; forbidden status transitions).
2. **Adding to UI rather than replacing it.** Composing with `await next(e)` so Endless's elements are additive.
3. **Namespacing.** Prefixed command names (`endless-...`), registrations wrapped in `try`, and state kept in the ledger or an Endless-owned path rather than `$.store`.
4. **Staying light and crash-proof.** Cheap handlers that catch all errors and never throw into the shared worker.
5. **Not touching other mods.** No use of `plugin.register` or `engine.create`, and no auditing or refusing other mods' `$` calls.
6. **A distinct plugin name,** so an inline Endless plugin can't shadow a user's marketplace plugin, and vice versa.

**Detecting interference.** A mod could emit a periodic heartbeat or a per-turn ledger event. A session showing activity but no heartbeat would let `endless session monitor` flag "Endless mod not active or bypassed."

**Why this may be acceptable.** Endless cannot force user mods to be good citizens. If the authoritative gates live in the Go CLI, a user mod that blocks, rewrites or bypasses Endless's mod costs Endless only early in-session feedback and telemetry, not integrity. In the other direction, Endless's mod would affect user mods only when it deliberately denies one of its narrow actions.

**Testing coexistence.** A small adversarial test mod could check these behaviors by (a) loading before Endless and answering `tool.call` without calling `next`, (b) registering `endless-status`, (c) rendering `AbovePrompt` without calling `next`, and (d) throwing repeatedly. That would show whether Endless detects or tolerates each case.

## Possible Adoption Path

The stages below are one way to sequence the options above. Each is optional, and earlier stages do not commit Endless to later ones.

1. **No mods dependency:** an `endless-cc` plain plugin (skills, commands, `.mcp.json`, portable `Stop`/`PreToolUse` settings hooks), loaded by `endless task spawn` with `--plugin-dir` or `CLAUDE_CODE_PLUGIN_DIRS`, and optionally published from a marketplace for non-Endless sessions. Authoritative gates (`land`, status transitions) live in the Go CLI.
2. **Read-only mod (v2.1.287+):** a hooks module limited to telemetry (`turn.complete`/`turn.step` → ledger events), the `AbovePrompt` status band, a heartbeat, and zero-token `immediate` commands. If the mod doesn't load, nothing breaks. CI could run `claude plugin validate --strict --json`, `claude plugin test`, and the section 8 coexistence tests.
3. **Gates as defense in depth:** `tool.call` guards for land/merge/push, worktree boundaries and forbidden transitions, each with a fail-closed `.catch`, with denials logged to the ledger for auditing.
4. **Experimental, opt-in:** `$.model.fork` self-checks before `unverified`, reviewer-model routing via `agent.spawn`, and token-routing/caching experiments measured with stage 2's telemetry. Adversarial verification could remain its own Endless task.

**Open items relevant to any stage:**

- Whether Endless sandboxes set `CLAUDE_CONFIG_DIR` or only `XDG_CONFIG_HOME`.
- Whether worktree trust inheritance from the main checkout behaves as documented with a fresh worktree.
- Passing inline `pluginConfigs` via `--settings`.
- Plugin names starting with `claude-` fail validation.
- anthropics/claude-code#91870 and the changelog are where API changes are likely to appear first.

## Caveats

- **Very new feature.** Mods launched two days before this brief. The docs carry the version stamp "as of v2.1.287," the community already reports rollout inconsistencies (#99130), and details will change. The `.d.ts` files for a given build are authoritative.
- **Placeholder code.** The Endless CLI subcommands in the code samples (`task current --json`, `event append`) are hypothetical placeholders, not documented Endless commands. Some event field names (e.g. `prompt.context`'s `blocks` shape) were not fully verified against the generated types.
- **Unconfirmed config-dir behavior.** It is inferred, not documented, that `$.store`, `dev-mods` and `CLAUDE_PLUGIN_DATA` follow `CLAUDE_CONFIG_DIR`. The only documented way to pre-trust a directory without the dialog is setting `projects["<path>"].hasTrustDialogAccepted` in `~/.claude.json`, and where that file lives under a relocated config dir is unconfirmed.
- **Coexistence details are from docs, not testing.** Section 8's statements about chain ordering, crash attribution and name collisions come from the launch-week documentation and have not been independently tested.
- **Source quality.** Primary sources: Anthropic's blog (Oct 1, 2026), the code.claude.com mods docs (overview, create, events, API, interface, test, troubleshoot, admin, reference), GitHub issue #91870 (Sep 3–9, 2026), and the v2.1.287/2.1.288 release notes. Secondary or community sources (The New Stack, aiweekly, mixed-news, cellcog, explainx, aitmpl, claudemods.ai, community GitHub repos) were used only for dating and ecosystem context. Some of them describe the pre-launch early-access flag, which no longer applies.

## Sources

- Anthropic blog, "Customize Claude Code with mods" (Oct 1, 2026): https://claude.com/blog/claude-code-mods
- Mods docs: https://code.claude.com/docs/en/plugins/mods/overview (also `/create`, `/events`, `/api`, `/interface`, `/test`, `/troubleshoot`, `/admin`, `/reference`)
- Plugin loading: https://code.claude.com/docs/en/plugins/loading
- Environment variables: https://code.claude.com/docs/en/env-vars
- Permissions: https://code.claude.com/docs/en/permissions
- Changelog: https://code.claude.com/docs/en/changelog
- Design thread #91870: https://github.com/anthropics/claude-code/issues/91870
- Remote-off issue #99130: https://github.com/anthropics/claude-code/issues/99130
- Releases: https://github.com/anthropics/claude-code/releases
- The New Stack coverage: https://thenewstack.io/anthropic-claude-code-mods-plugins
- mixed-news coverage: https://mixed-news.com/en/claude-code-2-1-287-mods-not-sandboxed-api-key/
- Community: https://github.com/0xDarkMatter/claude-mods, https://github.com/thieung/claude-mods, https://github.com/ccdwyer/claude-mods, https://github.com/karanb192/claude-code-mods, https://claudemods.ai/

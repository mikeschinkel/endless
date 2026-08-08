# Mechanism findings — verified against the installed Claude Code build (2.1.220)

These were read out of the shipped executable's strings, not recalled. An earlier
answer in the filing session asserted `/output-style` existed and was wrong; treat
harness priors as stale until confirmed against the installed build.

## The command is gone; it is a /config row now

There are zero slash-command registrations named `output-style`. It survives as:

- a `/config` panel row: `id:"outputStyle", label:"Output style", type:"managedEnum",
  options:Object.keys(_fe), optionsHint:"For custom styles, open /config."`
- a `/config key=value` key: `output-style` -> `Output style`
  (`/config` declares `supportsNonInteractive:!0`, `argumentHint:"key=value"`)

No doc, guide text, or help string this task produces may tell users to run
`/output-style`.

## Why this position beats the ones E-1785 already used

Style content is injected into the system prompt, and separately re-asserted every
turn as a meta system-reminder:

    output_style:(e)=>{let t=_fe[e.style];if(!t)return[];
      return pm([zr({content:`${t.name} output style is active. ${e.turnReminder??
        "Remember to follow the specific guidelines for this style."}`,isMeta:!0})])}

CLAUDE.md is injected once into user-turn context and competes with everything else
in a long session. The Stop hook (E-1901) fires after the tokens already exist and
produces a bounce loop, which is why E-1911 parked it. The per-turn re-assertion is
the property neither has.

## No built-in style helps — the "just switch styles" step would backfire

From the `_fe` built-in map:

- `default` -> `null`. Injects nothing at all.
- `Explanatory` -> literally "you may exceed typical length constraints".
- `Learning` -> pauses to ask the user to hand-write 2-10 line code pieces.
- `Proactive` -> autonomy (execute immediately, minimize interruptions), not length.

`default` is already the only built-in that adds no prose. Authoring is the only path.

## Frontmatter surface for custom styles

Confirmed key aliases: `keep-coding-instructions` -> `keepCodingInstructions`, and
`force-for-plugin` -> `forceForPlugin` (plugin-only; warns "has force-for-plugin set,
but this option only applies to plugin output styles. Ignoring." elsewhere). Plus
`name` and `description`.

`turnReminder` is built-in-only — it is not in the frontmatter alias list, and only
`Proactive` sets one. A custom style therefore gets the generic fallback reminder
text. Still per-turn, just not custom-worded.

## TRAP: a careless style can delete the anti-verbosity instructions

All three built-ins set `keepCodingInstructions:!0`. The default system prompt is
assembled from named sections via `sD(...)`, and one is literally called
`anti_verbosity`:

    m=[sD(`anti_verbosity${s}${f?":send_user_msg":""}`,()=>oMy(t)),
       sD("pronouns",()=>cMy),
       sD(`action_caution${s}`,()=>iMy(t)),
       sD("task_continuity",()=>sMy(...

A style that omits `keep-coding-instructions: true` risks dropping the very
anti-verbosity instructions the task is trying to strengthen. Set it explicitly and
verify empirically — do not assume the flag's default.

## Safe-mode gate

`fd("outputStyles")` gates custom styles: when set, the loader returns only the
built-in map and the config row renders the value as "<name> (disabled in safe mode)".
Any install path must degrade legibly under that gate rather than appearing to work.

## Existing precedent to follow for delivery

`internal/templatecmd` (E-1565) already does exactly the embed-and-materialize shape
this task needs: `//go:embed all:templates`, and a per-template lookup order of
`.endless/templates/<name>.local.tmpl` (per-developer) -> `.endless/templates/<name>.tmpl`
(committed, materialized from embed on first render) -> embedded fallback. Reuse the
pattern rather than inventing a second one.

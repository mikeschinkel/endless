Endless's workflow depends on Claude Code slash commands that live only in Mike's personal `~/.claude/commands/`, for example `/whats-left` and `/tl-dr`, and likely `/needs-plan`. Mike considers them part of Endless. Some of what they do can't be a CLI command or hook, or Endless hasn't yet grown the code to do it. He hopes to retire them eventually, but expects the first alpha to need them.

A user who installs Endless today gets none of them. Their names can also collide with commands a user already has.

Open questions for the plan:
- Which of Mike's commands and skills are part of Endless, and which are personal?
- How are they delivered: copied into the user's command directory by `endless setup` (with an option to rename on conflict), or packaged as a Claude Code plugin, which namespaces its commands (`<plugin>:<command>`) and so avoids collisions?
- Where does their source live so it's version-controlled with Endless?

Raised while planning E-2317, whose decision tests currently live only in `/whats-left`.

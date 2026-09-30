Fix: when agent_env.present(), emit ONE dense verdict line as both the FIRST and LAST line of the refusal, with the existing guidance unchanged between them.

Identical lines, not split, so whichever end survives a truncation is sufficient on its own.

Humans see today's message unchanged - the duplication is agent-only by design.

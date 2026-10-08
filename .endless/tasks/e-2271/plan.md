Prove or disprove, against scratch sessions, that a short-lived `claude -p` process can deliver a message to another live, idle Claude Code session through SendMessage, and that the message wakes that session and runs as a turn.

Steps:
1. Start a scratch interactive session named like a spawned task session (E-NNNN, per the spawn naming already in place). Let it go idle.
2. From a separate shell (not inside any session), run `claude -p` with a prompt telling it to call ListAgents and then SendMessage to that name. Record any tool or permission flags it needs (for example --allowedTools).
3. Record whether ListAgents in the -p process lists the target, whether the message arrives, whether the idle target wakes and acts without anyone typing, and what the target pane shows.
4. Repeat while the target is busy, mid-turn: is the message queued until the turn ends, interleaved, or lost?
5. Check that the sender exiting right after SendMessage does not break delivery, and whether the target's reply (if any) goes anywhere.
6. Check a session started by hand and then claimed (not spawned): does it carry the E-NNNN name, or only spawned ones?
7. Note the minimum Claude Code version and any account or setting this depends on (PRODUCT: someone else's machine).

Outcome: works / works with caveats / does not work, with the exact invocation, as the outcome text.

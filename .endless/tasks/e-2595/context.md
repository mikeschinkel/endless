E-2594 clears the environment for test runs, which stops accidents but not intent. A test written to do so can still read the real home by absolute path (~/.ssh keys, ~/.aws/credentials), find the macOS SSH agent socket under /private/tmp/com.apple.launchd.*, query the keychain with `security` or a git credential helper, and reach the network. Only OS-level isolation blocks those.

Mike, 2026-11-11: not currently worried about a rogue agent, so this stays in phase later.

This protects test runs only. An agent that can write such a test can usually run the same command directly; containing the agent is Claude Code's permission settings or the heavier isolation under E-995.

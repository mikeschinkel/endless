package hookcmd

import "os"

// terminalEntrypoint is the value Claude Code's CLI puts in
// CLAUDE_CODE_ENTRYPOINT. It is the ONE recognized terminal surface (E-1962).
const terminalEntrypoint = "cli"

// terminalSurface reports whether this hook is running inside a terminal Claude
// Code session, as opposed to the Desktop app, an IDE extension, or anything
// else that hosts an agent (E-1962).
//
// Observed on 2026-08-13, dumping the environment a hook subprocess inherits on
// each surface:
//
//	terminal (Claude Code 2.1.222)  CLAUDE_CODE_ENTRYPOINT=cli
//	                                CLAUDECODE=1
//	                                __CFBundleIdentifier=com.apple.Terminal
//
//	Desktop app                     CLAUDE_CODE_ENTRYPOINT  (absent)
//	                                CLAUDECODE              (absent)
//	                                CLAUDE_AGENT_SDK_VERSION=0.3.222
//	                                __CFBundleIdentifier=com.anthropic.claudefordesktop
//
// The Desktop app hosts the agent through the Agent SDK rather than the CLI, so
// none of the CLI's own variables are set there. That gap is the discriminator.
//
// This ALLOW-LISTS the terminal rather than deny-listing Desktop, which matters
// more than it looks. A deny-list has to name every surface that exists —
// Desktop today, whatever ships next quarter — and the failure mode of missing
// one is that a brand new surface silently starts enforcing a contract nobody
// decided it should have. The allow-list's failure mode is the opposite: an
// unrecognized surface is left ungated, which is where every surface except the
// terminal is supposed to be anyway. Absent is therefore not-terminal, and so is
// "vscode", "sdk-py", or any value we have never seen.
//
// Deliberately keyed on ONE variable. CLAUDE_AGENT_SDK_VERSION looks like a
// tempting second signal, but it says "an SDK is hosting this agent," not "this
// is not a terminal" — a terminal-launched background or SDK-driven session
// could plausibly carry it, and treating it as disqualifying would switch the
// channel off for terminal sessions that should have it.
func terminalSurface() bool {
	return os.Getenv("CLAUDE_CODE_ENTRYPOINT") == terminalEntrypoint
}

// Package agentenv identifies which agent harness is running Endless, from the
// environment that harness exports to its subprocesses (E-1962).
//
// Named agentenv rather than agent because Endless already calls something else
// an "agent": the background workers under an epic (`endless agents`). This
// package is about the HOST — the thing running the session — not about those.
//
// The question it answers is "which harness is this, and do we support it?", not
// "is this a terminal?". Terminal-vs-Desktop is the distinction that motivated
// it, but the shape has to hold for Codex CLI and whatever comes next, so the
// answer is an identity with a support flag rather than a boolean.
package agentenv

import "os"

// The environment variables and values the detectors key on. Named because they
// appear in both the table and its tests, and a typo in either would silently
// mean "no harness matched" rather than a compile error.
const (
	entrypointVar = "CLAUDE_CODE_ENTRYPOINT"
	bundleVar     = "__CFBundleIdentifier"

	cliEntrypoint     = "cli"
	desktopEntrypoint = "claude-desktop"
	desktopBundleID   = "com.anthropic.claudefordesktop"
)

// ID names an agent harness. Values are stable strings because they are
// intended to become config and DB values (see E-1505, which needs a
// platform='claude-desktop' discriminator on the session row).
type ID string

const (
	// Unknown is the answer for any environment no detector claims. It is
	// deliberately UNSUPPORTED: see Supported.
	Unknown ID = "unknown"

	// ClaudeCLI is Claude Code in a terminal. The only supported harness today.
	ClaudeCLI ID = "claude_cli"

	// ClaudeDesktop is the Claude Code Desktop app. Detected so it can be named
	// in diagnostics and so E-1505 has something to build on — not supported.
	ClaudeDesktop ID = "claude_desktop"
)

// Lookup reads one environment variable. Mirrors os.Getenv; exists so detectors
// are testable without mutating the process environment.
type Lookup func(string) string

// detector claims an environment for one harness. Order in the table is
// significant — the first claim wins — so detectors must be listed
// most-specific first.
type detector struct {
	id     ID
	claims func(Lookup) bool
}

// detectors is the extension point. Adding a harness means adding a row here
// plus its ID constant, and nothing else.
//
// There is deliberately NO Codex CLI (or any other) row yet. A detector that has
// never been checked against a real dump of that harness's environment is a
// guess, and a guess here fails silently: the harness is simply misidentified,
// and whichever way the support flag lands is wrong. Adding one is cheap once
// somebody pastes `env` from a live session — that, not code, is the missing
// input.
var detectors = []detector{
	{
		// Observed 2026-08-13, Claude Code 2.1.222 in a terminal:
		//   CLAUDE_CODE_ENTRYPOINT=cli
		//   CLAUDECODE=1
		//   CLAUDE_CODE_SESSION_ID=<uuid>
		//   AI_AGENT=claude-code_2-1-222_agent
		//
		// Keyed on the entrypoint alone: it is the variable that actually names
		// the surface, and Claude Code sets the others on surfaces this must not
		// claim. Exact match, case- and whitespace-sensitive — the value is
		// produced by Claude Code, not typed by a human, so a near-miss is a
		// different surface or a corrupted environment, not this one.
		id:     ClaudeCLI,
		claims: func(env Lookup) bool { return env(entrypointVar) == cliEntrypoint },
	},
	{
		// Observed 2026-08-13 by reading the Desktop harness process environment
		// directly (`ps eww` on the bundled `claude` binary inside Claude.app,
		// Claude Code 2.1.227):
		//
		//   CLAUDE_CODE_ENTRYPOINT=claude-desktop
		//   __CFBundleIdentifier=com.anthropic.claudefordesktop
		//   CLAUDE_AGENT_SDK_VERSION=0.3.227
		//   CLAUDE_CODE_HOST_SESSION_ID=local_<uuid>
		//
		// Desktop DOES set the entrypoint — it names itself. An earlier version
		// of this file asserted the opposite (that Desktop set no entrypoint at
		// all, and that its absence was the signal) because the only sample
		// available then came from Desktop's Bash tool and was incomplete. The
		// code happened to still classify Desktop correctly, via the bundle id,
		// which is worse than a clean failure: a wrong explanation sitting next
		// to working code, with a fallback branch that could never fire.
		//
		// Both signals below are now observed rather than inferred. The
		// entrypoint is the primary one and is portable; the bundle identifier
		// is corroboration and is macOS-only. The old "Agent SDK version is set
		// and the entrypoint is empty" branch is gone — it described an
		// environment that does not exist.
		//
		// Read the harness process directly when re-deriving this. Desktop's
		// Bash tool is a subprocess whose environment may differ from the one
		// hooks inherit, and asking the agent to run `env` samples the former.
		id: ClaudeDesktop,
		claims: func(env Lookup) bool {
			return env(entrypointVar) == desktopEntrypoint ||
				env(bundleVar) == desktopBundleID
		},
	},
}

// supported lists the harnesses Endless actually runs against. Today: one.
//
// This is an ALLOW-LIST, and the direction matters. A deny-list has to name
// every harness that exists, and the cost of missing one is that a newly
// shipped host silently starts obeying contracts nobody decided it should have.
// The allow-list's failure direction is the opposite — an unrecognized harness
// lands outside, which is where everything except Claude Code CLI belongs.
var supported = map[ID]bool{
	ClaudeCLI: true,
}

// Detect identifies the harness running this process.
func Detect() ID {
	return DetectWith(os.Getenv)
}

// DetectWith is Detect against an arbitrary environment.
func DetectWith(env Lookup) ID {
	for _, d := range detectors {
		if d.claims(env) {
			return d.id
		}
	}
	return Unknown
}

// Supported reports whether Endless supports the harness running this process.
//
// This is the predicate callers want. Asking `Detect() == ClaudeCLI` gets the
// same answer today and the wrong one the day a second harness is supported.
func Supported() bool {
	return SupportedWith(os.Getenv)
}

// SupportedWith is Supported against an arbitrary environment.
func SupportedWith(env Lookup) bool {
	return supported[DetectWith(env)]
}

// Label renders an ID for a human. Unknown gets a phrase rather than the bare
// slug because it reaches users in the `endless guide` banner.
func Label(id ID) string {
	switch id {
	case ClaudeCLI:
		return "Claude Code (terminal)"
	case ClaudeDesktop:
		return "Claude Code Desktop"
	default:
		return "an unrecognized agent harness"
	}
}

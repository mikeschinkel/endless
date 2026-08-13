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
		claims: func(env Lookup) bool { return env("CLAUDE_CODE_ENTRYPOINT") == "cli" },
	},
	{
		// Observed 2026-08-13, Claude Code Desktop:
		//   __CFBundleIdentifier=com.anthropic.claudefordesktop
		//   CLAUDE_AGENT_SDK_VERSION=0.3.222
		//   CLAUDE_CODE_ENTRYPOINT   (absent)
		//   CLAUDECODE               (absent)
		//   AI_AGENT                 (absent)
		//
		// Desktop hosts the agent through the Agent SDK rather than the CLI, so
		// none of the CLI's own variables reach a subprocess there. That absence
		// is the whole signal, which is why this row runs SECOND — ClaudeCLI's
		// positive match has to get first refusal.
		//
		// Two signals, and the honest reading of each: the bundle identifier is
		// proof but macOS-only, while the SDK version is portable but means "an
		// Agent SDK hosts this", which some future non-Desktop host could also
		// set. Neither is load-bearing today — everything except ClaudeCLI is
		// unsupported, so a mislabel here changes a diagnostic string and
		// nothing else. It becomes load-bearing under E-1505, and should be
		// re-derived from a fresh dump then rather than trusted from here.
		id: ClaudeDesktop,
		claims: func(env Lookup) bool {
			if env("__CFBundleIdentifier") == "com.anthropic.claudefordesktop" {
				return true
			}
			return env("CLAUDE_AGENT_SDK_VERSION") != "" &&
				env("CLAUDE_CODE_ENTRYPOINT") == ""
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

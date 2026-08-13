package agentenv

import "testing"

// env builds a Lookup over a fixed map. Absent keys read as "", exactly as
// os.Getenv reports an unset variable — which matters here, because the Desktop
// detector's whole signal is a set of ABSENT variables.
func env(pairs map[string]string) Lookup {
	return func(k string) string { return pairs[k] }
}

// TestDetect pins the detector table (E-1962).
//
// The two harness rows are transcriptions of real `env` dumps taken on
// 2026-08-13; the rest are the design. Nothing here should be "fixed" by
// loosening a match — if a real environment stops being identified, take a
// fresh dump and add a row.
func TestDetect(t *testing.T) {
	cases := []struct {
		name string
		vars map[string]string
		want ID
	}{
		{
			"claude code in a terminal (observed)",
			map[string]string{
				"CLAUDE_CODE_ENTRYPOINT": "cli",
				"CLAUDECODE":             "1",
				"CLAUDE_CODE_SESSION_ID": "6f53f9d3-c73e-4f9e-b04f-fcd3742290f1",
				"AI_AGENT":               "claude-code_2-1-222_agent",
				"__CFBundleIdentifier":   "com.apple.Terminal",
			},
			ClaudeCLI,
		},
		{
			"claude code desktop (observed)",
			map[string]string{
				"__CFBundleIdentifier":     "com.anthropic.claudefordesktop",
				"CLAUDE_AGENT_SDK_VERSION": "0.3.222",
			},
			ClaudeDesktop,
		},
		{
			"desktop on a platform without bundle ids",
			map[string]string{"CLAUDE_AGENT_SDK_VERSION": "0.3.222"},
			ClaudeDesktop,
		},

		// Ordering: the CLI's positive match gets first refusal. A terminal
		// session that happens to carry an SDK version — a background or
		// SDK-driven session launched FROM the terminal — is still the CLI, and
		// must not be demoted to Desktop.
		{
			"terminal carrying an sdk version is still the cli",
			map[string]string{
				"CLAUDE_CODE_ENTRYPOINT":   "cli",
				"CLAUDE_AGENT_SDK_VERSION": "0.3.222",
			},
			ClaudeCLI,
		},

		{"empty environment", map[string]string{}, Unknown},
		{"ide extension", map[string]string{"CLAUDE_CODE_ENTRYPOINT": "vscode"}, Unknown},
		{"python sdk", map[string]string{"CLAUDE_CODE_ENTRYPOINT": "sdk-py"}, Unknown},
		{"empty entrypoint", map[string]string{"CLAUDE_CODE_ENTRYPOINT": ""}, Unknown},
		{"harness nobody has seen", map[string]string{"CLAUDE_CODE_ENTRYPOINT": "holodeck"}, Unknown},

		// Exact match. The value comes from Claude Code, not a human, so a
		// near-miss is a different surface or a corrupted environment.
		{"wrong case", map[string]string{"CLAUDE_CODE_ENTRYPOINT": "CLI"}, Unknown},
		{"padded", map[string]string{"CLAUDE_CODE_ENTRYPOINT": " cli "}, Unknown},

		// CLAUDECODE alone is not enough: it says "some Claude Code", not which
		// surface, and Detect's entire job is which.
		{"claudecode without an entrypoint", map[string]string{"CLAUDECODE": "1"}, Unknown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := DetectWith(env(c.vars)); got != c.want {
				t.Errorf("DetectWith(%s) = %q, want %q", c.name, got, c.want)
			}
		})
	}
}

// TestSupported pins the allow-list: exactly one harness is supported, and
// everything else — including a harness we can NAME — is not.
//
// The ClaudeDesktop row is the one that carries meaning. Detecting a harness and
// supporting it are separate facts, and E-1505 (add support for Claude Desktop)
// is the task that would flip the second without touching the first.
func TestSupported(t *testing.T) {
	cases := []struct {
		id   ID
		want bool
	}{
		{ClaudeCLI, true},
		{ClaudeDesktop, false},
		{Unknown, false},
		{ID("codex_cli"), false},
	}
	for _, c := range cases {
		t.Run(string(c.id), func(t *testing.T) {
			if got := supported[c.id]; got != c.want {
				t.Errorf("supported[%q] = %v, want %v", c.id, got, c.want)
			}
		})
	}

	if !SupportedWith(env(map[string]string{"CLAUDE_CODE_ENTRYPOINT": "cli"})) {
		t.Error("a terminal Claude Code session is not supported")
	}
	if SupportedWith(env(map[string]string{"CLAUDE_AGENT_SDK_VERSION": "0.3.222"})) {
		t.Error("Claude Code Desktop is supported; it is not, until E-1505")
	}
	if SupportedWith(env(map[string]string{})) {
		t.Error("an unrecognized harness is supported; the allow-list leaked")
	}
}

// TestLabel pins that every ID renders as a phrase, since these reach users in
// the `endless guide` banner.
func TestLabel(t *testing.T) {
	for _, id := range []ID{ClaudeCLI, ClaudeDesktop, Unknown, ID("codex_cli")} {
		if Label(id) == "" {
			t.Errorf("Label(%q) is empty", id)
		}
		if Label(id) == string(id) {
			t.Errorf("Label(%q) returned the bare slug, not a phrase", id)
		}
	}
}

// TestDetectorsAreOrderedMostSpecificFirst pins the ordering invariant as an
// executable statement rather than a comment, since the table is the extension
// point and a new row is most likely to be appended without reading it.
func TestDetectorsAreOrderedMostSpecificFirst(t *testing.T) {
	if len(detectors) == 0 {
		t.Fatal("no detectors registered")
	}
	if detectors[0].id != ClaudeCLI {
		t.Errorf("first detector is %q; the CLI's positive match must get first "+
			"refusal, because Desktop is detected partly by ABSENCE", detectors[0].id)
	}
}

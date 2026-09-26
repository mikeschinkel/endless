package hookcmd

import (
	"strings"
	"testing"
)

// E-2177: every text-matched gate must tell a command that RUNS from one that is
// only MENTIONED — in quotes, in a commit message, or on a heredoc line that is
// bytes going into a file.
//
// Trigger strings are assembled at runtime: these gates fire on the Bash command
// that writes their own tests, so a literal here would refuse its own authoring.

// mentions wraps a command the three ways an agent names one without running
// it: as a quoted argument, inside a commit message, and on a heredoc line.
func mentions(cmd string) map[string]string {
	return map[string]string{
		"quoted":         `echo "` + cmd + `"`,
		"commit message": `git commit -m "Docs: never run ` + cmd + ` by hand"`,
		"heredoc line":   "cat > notes.md <<'EOF'\n" + cmd + "\nEOF",
		"heredoc -":      "cat > notes.md <<-EOF\n\t" + cmd + "\n\tEOF",
		"heredoc then a command": "cat > notes.md <<\"EOF\"\n" + cmd +
			"\nEOF\necho done",
	}
}

// invocations wraps a command the ways an agent really runs one.
func invocations(cmd string) map[string]string {
	return map[string]string{
		"bare":           cmd,
		"after cd &&":    "cd /tmp/x && " + cmd,
		"after ;":        "true; " + cmd,
		"wrapper prefix": "env FOO=1 " + cmd,
		"after heredoc":  "cat > notes.md <<'EOF'\nprose\nEOF\n" + cmd,
	}
}

type gateCase struct {
	name string
	cmd  string // the bare command the gate is about
	hit  func(string) bool
	// noWrapper: the gate does not admit an arbitrary word before the command.
	// suiteRunRe accepts only a shell interpreter there, because admitting any
	// word would admit `cat <suite>` — reading a suite, which is allowed.
	noWrapper bool
}

func gateCases() []gateCase {
	sq := "sql" + "ite3"
	wt := "endless worktree " + "drop E-7"
	suite := "./.endless/tasks/e-12/" + "verify.sh"
	cont := "endless task " + "continue"
	return []gateCase{
		{"sqlite on .endless", sq + " .endless/endless.db", sqliteAgainstEndless, false},
		{"worktree removal", wt, removesWorktree, false},
		{"landed-suite run", suite, func(c string) bool { return suiteTaskFromCommand(c) != 0 }, true},
		{"revisit escape", cont, clearsRevisitGate, false},
	}
}

func TestTextGates_InvocationMatches(t *testing.T) {
	for _, g := range gateCases() {
		for form, cmd := range invocations(g.cmd) {
			if g.noWrapper && form == "wrapper prefix" {
				continue
			}
			if !g.hit(cmd) {
				t.Errorf("%s: %s: a real invocation did not match: %q", g.name, form, cmd)
			}
		}
	}
}

func TestTextGates_MentionDoesNotMatch(t *testing.T) {
	for _, g := range gateCases() {
		for form, cmd := range mentions(g.cmd) {
			if g.hit(cmd) {
				t.Errorf("%s: %s: a mention matched: %q", g.name, form, strings.ReplaceAll(cmd, "\n", `\n`))
			}
		}
	}
}

// TestStripHeredocs pins the helper every text-matched gate reads commands
// through: heredoc bodies and their closing lines go, the operator line and
// everything outside a heredoc stay.
func TestStripHeredocs(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"no heredoc", "ls -la", "ls -la"},
		{"bare delimiter", "cat > f <<EOF\nbody\nEOF", "cat > f <<EOF"},
		{"single-quoted", "cat > f <<'EOF'\nbody\nEOF\nls", "cat > f <<'EOF'\nls"},
		{"double-quoted", "cat > f <<\"END\"\nbody\nEND\nls", "cat > f <<\"END\"\nls"},
		{"space before delimiter", "cat > f << EOF\nbody\nEOF\nls", "cat > f << EOF\nls"},
		{"dash strips leading tabs", "cat > f <<-EOF\n\tbody\n\tEOF\nls", "cat > f <<-EOF\nls"},
		{"no dash keeps an indented delimiter as body",
			"cat > f <<EOF\n\tEOF\nbody\nEOF\nls", "cat > f <<EOF\nls"},
		{"two heredocs on one line",
			"paste <<A <<B\na1\nA\nb1\nB\nls", "paste <<A <<B\nls"},
		{"two heredocs in sequence",
			"cat > f <<A\na\nA\ncat > g <<B\nb\nB\nls", "cat > f <<A\ncat > g <<B\nls"},
		{"unterminated swallows the rest", "cat > f <<EOF\nbody\nmore", "cat > f <<EOF"},
		{"here-string left intact", "cat <<<\"x\"\nls", "cat <<<\"x\"\nls"},
		{"here-string word left intact", "cat <<<word\nls", "cat <<<word\nls"},
		{"arithmetic shift left intact", "echo $((1<<2))\nls", "echo $((1<<2))\nls"},
		{"mismatched quote is not a delimiter", "echo <<'EOF\nls", "echo <<'EOF\nls"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := stripHeredocs(c.in); got != c.want {
				t.Errorf("stripHeredocs(%q)\n got %q\nwant %q", c.in, got, c.want)
			}
		})
	}
}

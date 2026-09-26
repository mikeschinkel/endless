package hookcmd

import (
	"regexp"
	"strings"
)

// heredocOpRe finds a heredoc operator and its delimiter: `<<DELIM`,
// `<<'DELIM'`, `<<"DELIM"` and `<<-DELIM`. The delimiter must start with a
// letter or underscore, so shell arithmetic such as `$((1<<2))` is not mistaken
// for one. A `<<<` here-string is excluded by stripHeredocs, since Go's regexp
// has no lookbehind.
var heredocOpRe = regexp.MustCompile(`<<(-?)[ \t]*(['"]?)([A-Za-z_][A-Za-z0-9_]*)(['"]?)`)

// stripHeredocs returns cmd with every heredoc BODY removed, including its
// closing delimiter line; the line carrying the operator is kept, because that
// line is the command (E-2177).
//
// Why the gates need it: cmdPos treats a newline as a command boundary, so a
// heredoc line that BEGINS with a command looks exactly like that command. But a
// heredoc body is bytes going to a file — writing documentation or a test that
// names a forbidden command is not running it.
//
// More than one heredoc per line is handled: their bodies follow in operator
// order. An unterminated heredoc swallows the rest of the command, which is how
// the shell reads it too.
//
// Known limit, accepted because gates here catch innocent mistakes and are not a
// security boundary: an UNQUOTED heredoc expands `$(…)`, so a command
// substitution inside one really does run, and is stripped unseen. Nobody writes
// that by accident. Likewise a `<<WORD` inside a quoted string is taken for an
// operator; the cost is a missed match on the lines after it, never a false
// refusal.
func stripHeredocs(cmd string) string {
	if !strings.Contains(cmd, "<<") {
		return cmd
	}
	type pending struct {
		delim     string
		stripTabs bool
	}
	lines := strings.Split(cmd, "\n")
	kept := make([]string, 0, len(lines))
	var queue []pending
	for _, line := range lines {
		if len(queue) > 0 {
			closing := line
			if queue[0].stripTabs {
				closing = strings.TrimLeft(closing, "\t")
			}
			if closing == queue[0].delim {
				queue = queue[1:]
			}
			continue
		}
		kept = append(kept, line)
		for _, m := range heredocOpRe.FindAllStringSubmatchIndex(line, -1) {
			// `<<<` here-string. The regex cannot match at its first `<` (a
			// delimiter starts with a letter), only at its second.
			if start := m[0]; start > 0 && line[start-1] == '<' {
				continue
			}
			openQ, closeQ := line[m[4]:m[5]], line[m[8]:m[9]]
			if openQ != closeQ {
				continue // `<<'EOF` — not a well-formed delimiter
			}
			queue = append(queue, pending{
				delim:     line[m[6]:m[7]],
				stripTabs: line[m[2]:m[3]] == "-",
			})
		}
	}
	return strings.Join(kept, "\n")
}

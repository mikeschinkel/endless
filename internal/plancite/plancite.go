// Package plancite finds the repository paths a plan cites and reports which of
// them no longer exist (E-1994).
//
// # Why it exists
//
// A primed session reads a task's plan and code, then waits — possibly for
// weeks — until its user resumes it. The plan does not drift underneath it: the
// paused session is the thing that edits the plan, and a plan edit by anyone
// else reaches it as a change notice. The CODE drifts. A file the plan names
// can be renamed, moved or deleted by other work while the session's
// understanding of it stays exactly as it was at read-in. Resuming then acts on
// a map of a codebase that no longer exists.
//
// So on resume the hook asks one cheap, mechanical question: does every path the
// plan cites still exist? It does not judge whether the plan is still right.
// That is the resumed session's job, and the answer here tells it where to look.
//
// # What counts as a cited path
//
// A plan is prose, so this is a heuristic, and it is tuned to miss rather than
// to cry wolf: a missed citation costs nothing more than today's behaviour, a
// false "missing" teaches the session to ignore the line. A token is a citation
// when it is relative (no leading `/` or `~`), contains a `/`, and either sits
// inside backticks or ends in a segment with a file extension. Line suffixes
// (`foo.go:42`) and trailing punctuation are stripped. Anything holding a
// placeholder or glob character (`<id>`, `*`, `{`, `$`) is a pattern rather than
// a path, and a URL is not a repository path at all; both are skipped.
package plancite

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	// backticked is an inline code span. Its contents are a citation candidate
	// whether or not the last segment has an extension: `internal/plancite` is a
	// directory, and a plan names directories as often as files.
	backticked = regexp.MustCompile("`([^`\n]+)`")

	// bareToken is a whitespace-delimited token. Outside backticks only a token
	// whose last segment carries an extension is a candidate, which is what keeps
	// "and/or", "E-1993/E-1994" and "read/write" out.
	bareToken = regexp.MustCompile(`[^\s` + "`" + `()\[\]"']+`)

	// lineSuffix is `:42` or `:42-60` after a path.
	lineSuffix = regexp.MustCompile(`:\d+(-\d+)?$`)

	// hasExtension is a final segment like `plancite.go` or `verify.sh`.
	hasExtension = regexp.MustCompile(`[^/]\.[A-Za-z0-9]{1,8}$`)
)

// Paths returns the repository-relative paths plan cites, deduplicated and
// sorted so the report is stable across runs.
func Paths(plan string) []string {
	seen := map[string]bool{}

	for _, m := range backticked.FindAllStringSubmatch(plan, -1) {
		for field := range strings.FieldsSeq(m[1]) {
			if p, ok := normalize(field); ok {
				seen[p] = true
			}
		}
	}
	prose := backticked.ReplaceAllString(plan, " ")
	for _, tok := range bareToken.FindAllString(prose, -1) {
		p, ok := normalize(tok)
		if ok && hasExtension.MatchString(p) {
			seen[p] = true
		}
	}

	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// normalize trims a candidate token to a path and reports whether it is one.
func normalize(tok string) (string, bool) {
	tok = strings.TrimRight(tok, ".,;:!?")
	tok = lineSuffix.ReplaceAllString(tok, "")
	tok = strings.TrimPrefix(tok, "./")
	switch {
	case tok == "",
		!strings.Contains(tok, "/"),
		strings.Contains(tok, "://"),
		strings.HasPrefix(tok, "/"),
		strings.HasPrefix(tok, "~"),
		strings.HasPrefix(tok, "-"),
		strings.ContainsAny(tok, "<>*{}$|=@"):
		return "", false
	}
	return strings.TrimSuffix(tok, "/"), true
}

// Missing returns the paths plan cites that do not exist under root.
func Missing(root, plan string) []string {
	var out []string
	for _, p := range Paths(plan) {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(p))); os.IsNotExist(err) {
			out = append(out, p)
		}
	}
	return out
}

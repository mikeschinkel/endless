package hookcmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mikeschinkel/endless/internal/monitor"
)

// E-940 — the Bash half of the write-target gate. One rule: a Bash command may
// not write to a path that a Write/Edit to that same path would be refused for.
// This file extracts a command's write targets; writeTargetDecision
// (write_target.go) judges each one.
//
// Shell is not parseable in general without running it, so this is a
// MISTAKE-CATCHER, not a security boundary — the stance stripHeredocs documents.
// It recognizes the write forms agents actually type (redirects, `tee`,
// `sed -i`, `mv`/`cp`/`rm`, formatters with -w, git working-tree mutators, …) and
// says nothing about the rest. Everything it cannot resolve to a concrete path
// is allowed, silently:
//
//   - a dynamic target (`> "$OUT"`, `rm "$WT"/x`) whose literal prefix is not an
//     absolute path — `$HOME` is the one variable expanded;
//   - interpreters and scripts (`python -c`, `node -e`, `awk`, `./x.sh`, `make`,
//     `just`, `go build -o`), whose writes live in their own code;
//   - indirection (`xargs rm`, `parallel`), `eval` beyond two levels, and
//     anything that will not lex (an unterminated quote).
//
// Those are the forms nobody types by accident; the gate targets the accident —
// an agent `sed -i`-ing the main checkout by absolute path. Precision over
// recall: a gate that blocks normal work is discovered on its first day and
// routed around thereafter.
//
// Deliberately never a target: `git commit` (a worktree commit writes objects
// into main's shared .git — blockCommitOnMainIfApplicable owns commits), `git
// add`, and `endless` / `endless-go`, whose subprocesses own their writes (the
// ledger, mirrors, LESSONS.md) by design.

// gitWritesCheck is the config key for the git working-tree mutators. On trial:
// they may prove too restrictive, so `"checks": {"bash_git_writes": false}` in
// .endless/config.json turns them off with no rebuild, and removing the feature
// is deleting gitWorktreeWrites, its call and its test rows.
const gitWritesCheck = "bash_git_writes"

// bashWrite is one write target recognized in a Bash command.
type bashWrite struct {
	// Path is absolute. For a dynamic or glob target it is the directory of the
	// word's static prefix; "" when nothing resolvable remains.
	Path string
	// Construct names what was recognized, for the refusal: "sed -i", ">", …
	Construct string
	// Dynamic is true when the word held a `$`, a backtick or a glob.
	Dynamic bool
}

// ─── lexer ────────────────────────────────────────────────────────────────────

// shellToken is one lexed token: an operator, or a word with its quotes removed.
type shellToken struct {
	op  string // non-empty for an operator: ";", "&&", "|", "(", ">", "<<", …
	val string // a word's value: quotes removed, `$HOME` and a leading `~` expanded
	// cut is the index in val of the first byte that could not be resolved
	// statically — a `$…`, a backtick, a process substitution or an unquoted
	// glob — or -1 when the whole word is literal.
	cut int
	// dynamic is true when cut came from an expansion rather than a glob.
	dynamic bool
}

func (t shellToken) isWord() bool { return t.op == "" }

// lexShell splits cmd into operators and words, honouring single and double
// quotes, backslash escapes, `$(…)`, backticks and `#` comments. ok is false
// when cmd will not lex — an unterminated quote or substitution — and the gate
// then has nothing to say.
func lexShell(cmd string) (toks []shellToken, ok bool) {
	home, _ := os.UserHomeDir()
	if homeAssignRe.MatchString(cmd) {
		// The command sets HOME itself — a verify script's isolation, say — so
		// the hook's HOME is not the one `$HOME` will expand to.
		home = ""
	}
	n := len(cmd)
	i := 0
	for i < n {
		c := cmd[i]
		switch {
		case c == ' ' || c == '\t' || c == '\r':
			i++
			continue
		case c == '\\' && i+1 < n && cmd[i+1] == '\n':
			i += 2
			continue
		case c == '\n' || c == ';':
			toks = append(toks, shellToken{op: ";"})
			i++
			continue
		case c == '#':
			for i < n && cmd[i] != '\n' {
				i++
			}
			continue
		case c == '&':
			switch {
			case strings.HasPrefix(cmd[i:], "&&"):
				toks = append(toks, shellToken{op: "&&"})
				i += 2
			case strings.HasPrefix(cmd[i:], "&>>"):
				toks = append(toks, shellToken{op: "&>>"})
				i += 3
			case strings.HasPrefix(cmd[i:], "&>"):
				toks = append(toks, shellToken{op: "&>"})
				i += 2
			default:
				toks = append(toks, shellToken{op: "&"})
				i++
			}
			continue
		case c == '|':
			switch {
			case strings.HasPrefix(cmd[i:], "||"):
				toks = append(toks, shellToken{op: "||"})
				i += 2
			case strings.HasPrefix(cmd[i:], "|&"):
				toks = append(toks, shellToken{op: "|&"})
				i += 2
			default:
				toks = append(toks, shellToken{op: "|"})
				i++
			}
			continue
		case c == '(' && strings.HasPrefix(cmd[i:], "(("):
			// Arithmetic `(( a > b ))`: one opaque word, so its `>` is not a
			// redirect.
			end, ok := skipBalanced(cmd, i+1)
			if !ok {
				return nil, false
			}
			toks = append(toks, shellToken{val: cmd[i:end], cut: 0, dynamic: true})
			i = end
			continue
		case c == '(' || c == ')':
			toks = append(toks, shellToken{op: string(c)})
			i++
			continue
		case (c == '<' || c == '>') && i+1 < n && cmd[i+1] == '(':
			// Process substitution `<(…)` / `>(…)` is a word, not a redirect.
		case c == '<' || c == '>' || isRedirectFD(cmd, i):
			op, next := lexRedirect(cmd, i)
			toks = append(toks, shellToken{op: op})
			i = next
			continue
		}
		tok, next, ok := lexWord(cmd, i, home)
		if !ok {
			return nil, false
		}
		toks = append(toks, tok)
		i = next
	}
	return toks, true
}

// homeAssignRe matches an assignment to HOME anywhere in a command.
var homeAssignRe = regexp.MustCompile(`(?:^|[\s;&|(])(?:export\s+)?HOME=`)

// isRedirectFD reports whether cmd[i:] is a file-descriptor number that
// prefixes a redirect (`2>`, `10>>`, `0<`) at the start of a word.
func isRedirectFD(cmd string, i int) bool {
	j := i
	for j < len(cmd) && cmd[j] >= '0' && cmd[j] <= '9' {
		j++
	}
	return j > i && j < len(cmd) && (cmd[j] == '>' || cmd[j] == '<')
}

// lexRedirect reads a redirect operator at cmd[i:], after any fd number. The
// returned op drops the fd, which no target depends on.
func lexRedirect(cmd string, i int) (op string, next int) {
	for i < len(cmd) && cmd[i] >= '0' && cmd[i] <= '9' {
		i++
	}
	for _, o := range []string{"<<<", "<<-", "<<", "<&", "<>", "<", ">>", ">|", ">&", ">"} {
		if strings.HasPrefix(cmd[i:], o) {
			return o, i + len(o)
		}
	}
	return cmd[i : i+1], i + 1
}

// wordEnd reports whether c ends an unquoted word.
func wordEnd(c byte) bool {
	switch c {
	case ' ', '\t', '\r', '\n', ';', '&', '|', '(', ')', '<', '>':
		return true
	}
	return false
}

// lexWord reads one word starting at cmd[i].
func lexWord(cmd string, i int, home string) (tok shellToken, next int, ok bool) {
	var b strings.Builder
	tok.cut = -1
	mark := func(dynamic bool) {
		if tok.cut < 0 {
			tok.cut = b.Len()
			tok.dynamic = dynamic
		}
	}
	n := len(cmd)
	start := i

	// A leading unquoted `~` or `~/…` is the home directory; `~user` is not
	// something the hook can answer.
	if cmd[i] == '~' {
		if home != "" && (i+1 >= n || cmd[i+1] == '/' || wordEnd(cmd[i+1])) {
			b.WriteString(home)
			i++
		} else {
			mark(true)
		}
	}
	// A leading `<(` or `>(` is a process substitution.
	if i < n && (cmd[i] == '<' || cmd[i] == '>') && i+1 < n && cmd[i+1] == '(' {
		end, ok := skipBalanced(cmd, i+1)
		if !ok {
			return tok, i, false
		}
		mark(true)
		i = end
	}

	for i < n && !wordEnd(cmd[i]) {
		c := cmd[i]
		switch {
		case c == '\\':
			if i+1 < n {
				if cmd[i+1] != '\n' {
					b.WriteByte(cmd[i+1])
				}
				i += 2
				continue
			}
			i++
		case c == '\'':
			end := strings.IndexByte(cmd[i+1:], '\'')
			if end < 0 {
				return tok, i, false
			}
			b.WriteString(cmd[i+1 : i+1+end])
			i += end + 2
		case c == '"':
			i++
			for {
				if i >= n {
					return tok, i, false
				}
				d := cmd[i]
				if d == '"' {
					i++
					break
				}
				switch {
				case d == '\\' && i+1 < n && strings.IndexByte("$`\"\\\n", cmd[i+1]) >= 0:
					if cmd[i+1] != '\n' {
						b.WriteByte(cmd[i+1])
					}
					i += 2
				case d == '$':
					var ok bool
					if i, ok = lexDollar(cmd, i, home, &b, mark); !ok {
						return tok, i, false
					}
				case d == '`':
					end := strings.IndexByte(cmd[i+1:], '`')
					if end < 0 {
						return tok, i, false
					}
					mark(true)
					i += end + 2
				default:
					b.WriteByte(d)
					i++
				}
			}
		case c == '$':
			var ok bool
			if i, ok = lexDollar(cmd, i, home, &b, mark); !ok {
				return tok, i, false
			}
		case c == '`':
			end := strings.IndexByte(cmd[i+1:], '`')
			if end < 0 {
				return tok, i, false
			}
			mark(true)
			i += end + 2
		case c == '*' || c == '?' || c == '[':
			mark(false)
			b.WriteByte(c)
			i++
		default:
			b.WriteByte(c)
			i++
		}
	}
	if i == start && i < n {
		// Nothing consumed: a stray byte no case claims. Take it as a word so
		// the lexer always advances.
		b.WriteByte(cmd[i])
		i++
	}
	tok.val = b.String()
	return tok, i, true
}

// lexDollar handles a `$` at cmd[i]: `$HOME` / `${HOME}` expand to home; `$(…)`,
// `${…}`, `$name`, `$1`, `$?` are unresolvable and mark the word; `$'…'` is
// ANSI-C quoting, read literally; a lone `$` is itself.
func lexDollar(cmd string, i int, home string, b *strings.Builder, mark func(bool)) (int, bool) {
	n := len(cmd)
	rest := cmd[i+1:]
	switch {
	case home != "" && strings.HasPrefix(rest, "{HOME}"):
		b.WriteString(home)
		return i + 7, true
	case home != "" && strings.HasPrefix(rest, "HOME") && (len(rest) == 4 || !isIdentByte(rest[4])):
		b.WriteString(home)
		return i + 5, true
	case strings.HasPrefix(rest, "("):
		end, ok := skipBalanced(cmd, i+1)
		if !ok {
			return i, false
		}
		mark(true)
		return end, true
	case strings.HasPrefix(rest, "{"):
		end := strings.IndexByte(rest, '}')
		if end < 0 {
			return i, false
		}
		mark(true)
		return i + 2 + end, true
	case strings.HasPrefix(rest, "'"):
		end := strings.IndexByte(rest[1:], '\'')
		if end < 0 {
			return i, false
		}
		b.WriteString(rest[1 : 1+end])
		return i + 3 + end, true
	case len(rest) > 0 && (isIdentByte(rest[0]) || strings.IndexByte("?!#@*$-", rest[0]) >= 0):
		j := i + 2
		if isIdentByte(rest[0]) && !(rest[0] >= '0' && rest[0] <= '9') {
			for j < n && isIdentByte(cmd[j]) {
				j++
			}
		}
		mark(true)
		return j, true
	}
	b.WriteByte('$')
	return i + 1, true
}

func isIdentByte(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// skipBalanced returns the index just past the `)` matching the `(` at
// cmd[open], skipping quoted text.
func skipBalanced(cmd string, open int) (int, bool) {
	depth := 0
	for i := open; i < len(cmd); i++ {
		switch cmd[i] {
		case '\\':
			i++
		case '\'':
			end := strings.IndexByte(cmd[i+1:], '\'')
			if end < 0 {
				return 0, false
			}
			i += end + 1
		case '"':
			j := i + 1
			for j < len(cmd) && cmd[j] != '"' {
				if cmd[j] == '\\' {
					j++
				}
				j++
			}
			if j >= len(cmd) {
				return 0, false
			}
			i = j
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i + 1, true
			}
		}
	}
	return 0, false
}

// ─── walking commands ─────────────────────────────────────────────────────────

// shellRedir is one redirect on a simple command.
type shellRedir struct {
	op   string
	word shellToken
}

// isWrite reports whether the redirect writes to its word. `>&N` and `>&-`
// duplicate or close a descriptor and name no file.
func (r shellRedir) isWrite() bool {
	switch r.op {
	case ">", ">>", ">|", "&>", "&>>", "<>":
		return true
	case ">&":
		return r.word.cut >= 0 || !fdWordRe.MatchString(r.word.val)
	}
	return false
}

var fdWordRe = regexp.MustCompile(`^(\d+|-)$`)

// shellWalker tracks the directory each command runs in.
type shellWalker struct {
	dir   string
	stack []string // enclosing `( … )` directories
	pushd []string
	depth int
	fn    func(dir string, argv []shellToken, redirs []shellRedir)
}

// maxShellDepth bounds recursion into `bash -c S` / `eval S`.
const maxShellDepth = 2

// walkCommands calls fn for each simple command in cmd, in order, with the
// directory it runs in: starting at cwd, applying each `cd`, `pushd` and `popd`,
// scoping a directory change inside `( … )` to the parens, and recursing once
// into `bash -c S` / `sh -c S` / `zsh -c S` (dir inherited, changes scoped) and
// `eval S` (changes shared). argv is passed after leading assignments and
// wrappers (`env`, `sudo`, `command`, `time`, `nohup`, …) are stripped. A
// directory that cannot be known — `cd "$X"` — becomes "", and relative paths
// under it resolve to nothing.
//
// Heredoc bodies are stripped first; each operator line survives, so
// `cat > f <<EOF` still names f. ok is false when cmd will not lex.
//
// A `cd` made in an EARLIER Bash call needs no tracking: the payload's cwd
// follows the shell's persisted directory.
func walkCommands(cmd, cwd string, fn func(dir string, argv []shellToken, redirs []shellRedir)) bool {
	w := &shellWalker{dir: cwd, fn: fn}
	return w.walk(cmd)
}

func (w *shellWalker) walk(cmd string) bool {
	toks, ok := lexShell(stripHeredocs(cmd))
	if !ok {
		return false
	}
	var (
		argv   []shellToken
		redirs []shellRedir
	)
	flush := func() {
		if len(argv) > 0 || len(redirs) > 0 {
			w.run(argv, redirs)
		}
		argv, redirs = nil, nil
	}
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		switch t.op {
		case "":
			argv = append(argv, t)
		case ";", "&&", "||", "|", "|&", "&":
			flush()
		case "(":
			flush()
			w.stack = append(w.stack, w.dir)
		case ")":
			flush()
			if len(w.stack) > 0 {
				w.dir = w.stack[len(w.stack)-1]
				w.stack = w.stack[:len(w.stack)-1]
			}
		default:
			r := shellRedir{op: t.op}
			if i+1 < len(toks) && toks[i+1].isWord() {
				r.word = toks[i+1]
				i++
			}
			redirs = append(redirs, r)
		}
	}
	flush()
	return true
}

// run handles one simple command: it reports it to fn, then applies any
// directory change or recursion it makes.
func (w *shellWalker) run(argv []shellToken, redirs []shellRedir) {
	argv = unwrapCommand(argv)
	w.fn(w.dir, argv, redirs)
	if len(argv) == 0 {
		return
	}
	args := argv[1:]
	switch filepath.Base(argv[0].val) {
	case "cd":
		for len(args) > 0 && (args[0].val == "-P" || args[0].val == "-L" || args[0].val == "-e" || args[0].val == "-@") {
			args = args[1:]
		}
		if len(args) == 0 {
			w.dir = resolveDir(w.dir, "~")
			return
		}
		w.chdir(args[0])
	case "pushd":
		if len(args) > 0 && !strings.HasPrefix(args[0].val, "+") && !strings.HasPrefix(args[0].val, "-") {
			w.pushd = append(w.pushd, w.dir)
			w.chdir(args[0])
		}
	case "popd":
		if len(w.pushd) > 0 {
			w.dir = w.pushd[len(w.pushd)-1]
			w.pushd = w.pushd[:len(w.pushd)-1]
		}
	case "eval":
		if w.depth < maxShellDepth {
			w.depth++
			w.walk(joinVals(args))
			w.depth--
		}
	case "bash", "sh", "zsh", "dash", "ksh":
		if script, ok := shellDashC(args); ok && w.depth < maxShellDepth {
			child := &shellWalker{dir: w.dir, depth: w.depth + 1, fn: w.fn}
			child.walk(script)
		}
	}
}

// chdir applies `cd t`. `cd -` keeps the directory (the previous one is not
// knowable from here); an unresolvable target makes the directory unknown.
func (w *shellWalker) chdir(t shellToken) {
	switch {
	case t.val == "-":
	case t.cut >= 0:
		w.dir = ""
	default:
		d := resolveDir(w.dir, t.val)
		if !filepath.IsAbs(d) {
			d = ""
		}
		w.dir = d
	}
}

// shellDashC returns the script of `bash -c S` (flags may cluster: `-lc`,
// `-ec`), or false when the shell runs no inline script.
func shellDashC(args []shellToken) (string, bool) {
	sawC := false
	for _, a := range args {
		v := a.val
		if strings.HasPrefix(v, "-") && !strings.HasPrefix(v, "--") && len(v) > 1 {
			if strings.ContainsRune(v[1:], 'c') {
				sawC = true
			}
			continue
		}
		if strings.HasPrefix(v, "--") || strings.HasPrefix(v, "+") {
			continue
		}
		if sawC {
			return v, true
		}
		return "", false
	}
	return "", false
}

func joinVals(ts []shellToken) string {
	vals := make([]string, len(ts))
	for i, t := range ts {
		vals[i] = t.val
	}
	return strings.Join(vals, " ")
}

var assignmentRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// unwrapCommand strips what runs BEFORE the real command: `VAR=val`
// assignments, compound-command keywords (`then`, `do`, `{`, `!`, …), and
// wrappers that exec their arguments (`env`, `sudo`, `command`, `builtin`,
// `exec`, `nohup`, `time`, `nice`, `timeout`). `xargs` makes the command opaque:
// its operands come from stdin, so there is nothing to judge.
func unwrapCommand(argv []shellToken) []shellToken {
	for len(argv) > 0 {
		v := argv[0].val
		switch {
		case argv[0].cut < 0 && assignmentRe.MatchString(v):
			argv = argv[1:]
		case v == "!" || v == "{" || v == "}" || v == "then" || v == "do" || v == "else" ||
			v == "elif" || v == "if" || v == "while" || v == "until" || v == "builtin" || v == "nohup":
			argv = argv[1:]
		case v == "time" || v == "command" || v == "exec":
			argv = skipFlags(argv[1:], map[string]bool{"-a": true})
		case v == "env":
			argv = argv[1:]
			for len(argv) > 0 && (strings.HasPrefix(argv[0].val, "-") || assignmentRe.MatchString(argv[0].val)) {
				if argv[0].val == "-u" || argv[0].val == "-C" || argv[0].val == "-S" {
					argv = argv[1:]
				}
				if len(argv) > 0 {
					argv = argv[1:]
				}
			}
		case v == "sudo":
			argv = skipFlags(argv[1:], map[string]bool{"-u": true, "-g": true, "-C": true, "-D": true, "-h": true, "-p": true, "-r": true, "-t": true, "-U": true})
		case v == "nice":
			argv = skipFlags(argv[1:], map[string]bool{"-n": true})
		case v == "timeout":
			argv = skipFlags(argv[1:], map[string]bool{"-s": true, "-k": true})
			if len(argv) > 0 {
				argv = argv[1:] // the duration
			}
		case v == "xargs" || v == "parallel":
			return nil
		default:
			return argv
		}
	}
	return argv
}

// skipFlags drops leading `-x` flags; those in withArg also drop their value.
func skipFlags(argv []shellToken, withArg map[string]bool) []shellToken {
	for len(argv) > 0 && strings.HasPrefix(argv[0].val, "-") && argv[0].val != "-" {
		f := argv[0].val
		argv = argv[1:]
		if f == "--" {
			break
		}
		if withArg[f] && len(argv) > 0 {
			argv = argv[1:]
		}
	}
	return argv
}

// ─── recognizing writes ───────────────────────────────────────────────────────

// bashWriteTargets returns every write target recognized in cmd, run from cwd.
// gitWrites turns on the git working-tree mutators (gitWritesCheck). A command
// that will not lex yields none.
func bashWriteTargets(cmd, cwd string, gitWrites bool) []bashWrite {
	var out []bashWrite
	walkCommands(cmd, cwd, func(dir string, argv []shellToken, redirs []shellRedir) {
		out = append(out, commandWrites(dir, argv, redirs, gitWrites)...)
	})
	return out
}

// commandWrites recognizes the writes of one simple command.
func commandWrites(dir string, argv []shellToken, redirs []shellRedir, gitWrites bool) []bashWrite {
	var out []bashWrite
	add := func(construct string, t shellToken) {
		out = appendTarget(out, dir, construct, t)
	}
	addDir := func(construct string) {
		if filepath.IsAbs(dir) {
			out = append(out, bashWrite{Path: dir, Construct: construct})
		}
	}

	// `[[ a > b ]]` compares strings; its `>` is not a redirect.
	if len(argv) == 0 || argv[0].val != "[[" {
		for _, r := range redirs {
			if r.isWrite() {
				add(r.op, r.word)
			}
		}
	}
	if len(argv) == 0 {
		return out
	}

	name := filepath.Base(argv[0].val)
	args := argv[1:]
	switch name {
	case "tee":
		for _, t := range operands(args, nil) {
			add("tee", t)
		}
	case "sed":
		for _, t := range sedInPlaceFiles(args) {
			add("sed -i", t)
		}
	case "perl":
		for _, t := range perlInPlaceFiles(args) {
			add("perl -i", t)
		}
	case "mv":
		ops, target := targetDirFlag(args, map[string]bool{"-S": true, "--suffix": true})
		if target != nil {
			add("mv", *target)
		}
		for _, t := range ops {
			add("mv", t)
		}
	case "cp", "ln", "install", "rsync":
		withArg := map[string]bool{"-S": true, "--suffix": true}
		if name == "install" {
			for _, f := range []string{"-m", "-o", "-g", "--mode", "--owner", "--group"} {
				withArg[f] = true
			}
			if hasFlag(args, "-d", "--directory") {
				for _, t := range operands(args, withArg) {
					add("install -d", t)
				}
				break
			}
		}
		if name == "rsync" {
			withArg["-e"], withArg["--rsh"], withArg["--exclude"], withArg["--include"] = true, true, true, true
		}
		ops, target := targetDirFlag(args, withArg)
		switch {
		case target != nil:
			add(name, *target)
		case len(ops) >= 2 && !remoteSpecRe.MatchString(ops[len(ops)-1].val):
			add(name, ops[len(ops)-1])
		}
	case "rm", "rmdir", "unlink", "shred", "truncate", "touch", "mkdir":
		withArg := map[string]bool{"-s": true, "-r": true, "-t": true, "-d": true, "-m": true, "-n": true,
			"--size": true, "--reference": true, "--mode": true, "--iterations": true}
		if name == "rm" || name == "rmdir" {
			withArg = nil // `rm -r` / `-d` take no value
		}
		for _, t := range operands(args, withArg) {
			add(name, t)
		}
	case "chmod", "chown", "chgrp":
		ops := operands(args, nil)
		if !hasFlagPrefix(args, "--reference") && len(ops) > 0 {
			ops = ops[1:]
		}
		for _, t := range ops {
			add(name, t)
		}
	case "dd":
		for _, t := range args {
			if strings.HasPrefix(t.val, "of=") {
				add("dd of=", trimToken(t, 3))
			}
		}
	case "gofmt", "goimports":
		if hasFlag(args, "-w") {
			for _, t := range operands(args, map[string]bool{"-r": true, "-local": true}) {
				add(name+" -w", t)
			}
		}
	case "prettier":
		if hasFlag(args, "--write", "-w") {
			for _, t := range operands(args, map[string]bool{"--config": true, "--ignore-path": true,
				"--plugin": true, "--parser": true, "--log-level": true}) {
				add("prettier --write", t)
			}
		}
	case "ruff":
		if len(args) > 0 && args[0].val == "format" {
			ops := operands(args[1:], map[string]bool{"--config": true, "--line-length": true,
				"--target-version": true, "--exclude": true})
			if len(ops) == 0 && !hasFlag(args, "--check", "--diff") {
				addDir("ruff format")
			}
			for _, t := range ops {
				add("ruff format", t)
			}
		}
	case "black":
		if !hasFlag(args, "--check", "--diff", "-c", "--code") {
			for _, t := range operands(args, map[string]bool{"-l": true, "--line-length": true, "-t": true,
				"--target-version": true, "--config": true, "--include": true, "--exclude": true}) {
				add("black", t)
			}
		}
	case "curl":
		out = append(out, curlWrites(dir, args)...)
	case "wget":
		for i, t := range args {
			switch {
			case (t.val == "-O" || t.val == "-P") && i+1 < len(args) && args[i+1].val != "-":
				add("wget "+t.val, args[i+1])
			case strings.HasPrefix(t.val, "--output-document=") && t.val != "--output-document=-":
				add("wget -O", trimToken(t, len("--output-document=")))
			case strings.HasPrefix(t.val, "--directory-prefix="):
				add("wget -P", trimToken(t, len("--directory-prefix=")))
			}
		}
	case "tar":
		if tarExtracts(args) {
			if d := flagValue(args, "-C", "--directory"); d != nil {
				add("tar -x", *d)
			} else {
				addDir("tar -x")
			}
		}
	case "unzip":
		if !hasFlag(args, "-l", "-t", "-p", "-v", "-Z") {
			if d := flagValue(args, "-d"); d != nil {
				add("unzip -d", *d)
			} else {
				addDir("unzip")
			}
		}
	case "patch":
		if hasFlag(args, "--dry-run") {
			break
		}
		if o := flagValue(args, "-o", "--output"); o != nil {
			add("patch -o", *o)
		} else if d := flagValue(args, "-d", "--directory"); d != nil {
			add("patch -d", *d)
		} else if ops := operands(args, map[string]bool{"-p": true, "-i": true, "--input": true,
			"-F": true, "-B": true, "-D": true, "-V": true, "-r": true, "-z": true}); len(ops) > 0 {
			add("patch", ops[0])
		} else {
			addDir("patch")
		}
	case "find":
		out = append(out, findWrites(dir, args)...)
	case "git":
		if gitWrites {
			out = append(out, gitWorktreeWrites(dir, args)...)
		}
	}
	return out
}

// remoteSpecRe matches an rsync/scp remote spec such as `host:path`.
var remoteSpecRe = regexp.MustCompile(`^[^/]*:`)

// targetPath resolves a word to the absolute path it writes, against dir. A
// glob, or an expansion behind an absolute literal prefix, is judged by the
// directory of that prefix; an expansion with no absolute prefix resolves to
// "" — the hook's environment is not the shell's, so guessing is worse than
// saying nothing.
func targetPath(dir string, t shellToken) (path string, dynamic bool) {
	v := t.val
	if t.cut >= 0 {
		prefix := v[:t.cut]
		if t.dynamic && !filepath.IsAbs(prefix) {
			return "", true
		}
		if prefix != "" && !strings.HasSuffix(prefix, "/") {
			prefix = filepath.Dir(prefix)
		}
		v = prefix
		dynamic = true
		if v == "" {
			v = "."
		}
	}
	if v == "" {
		return "", dynamic
	}
	if filepath.IsAbs(v) {
		return filepath.Clean(v), dynamic
	}
	if !filepath.IsAbs(dir) {
		return "", dynamic
	}
	return filepath.Join(dir, v), dynamic
}

// trimToken drops the first n bytes of a word (`of=`, `--output=`), keeping
// its cut consistent.
func trimToken(t shellToken, n int) shellToken {
	t.val = t.val[n:]
	if t.cut >= 0 {
		t.cut -= n
		if t.cut < 0 {
			t.cut = 0
		}
	}
	return t
}

// operands returns the non-flag arguments. Flags in withArg consume the next
// word; `--` ends flag parsing; a lone `-` (stdin) is not a path.
func operands(args []shellToken, withArg map[string]bool) []shellToken {
	var out []shellToken
	flags := true
	for i := 0; i < len(args); i++ {
		v := args[i].val
		switch {
		case flags && v == "--":
			flags = false
		case v == "-":
		case flags && strings.HasPrefix(v, "-"):
			if withArg[v] {
				i++
			}
		default:
			out = append(out, args[i])
		}
	}
	return out
}

// hasFlag reports whether any of flags appears before `--`.
func hasFlag(args []shellToken, flags ...string) bool {
	for _, a := range args {
		if a.val == "--" {
			return false
		}
		for _, f := range flags {
			if a.val == f {
				return true
			}
		}
	}
	return false
}

func hasFlagPrefix(args []shellToken, prefix string) bool {
	for _, a := range args {
		if strings.HasPrefix(a.val, prefix) {
			return true
		}
	}
	return false
}

// flagValue returns the value of the first of names, given as `-x v`, `-xv`
// (single-dash short flags) or `--name=v`.
func flagValue(args []shellToken, names ...string) *shellToken {
	for i, a := range args {
		for _, n := range names {
			switch {
			case a.val == n && i+1 < len(args):
				v := args[i+1]
				return &v
			case strings.HasPrefix(n, "--") && strings.HasPrefix(a.val, n+"="):
				v := trimToken(a, len(n)+1)
				return &v
			case !strings.HasPrefix(n, "--") && len(n) == 2 && strings.HasPrefix(a.val, n) && len(a.val) > 2 && !strings.HasPrefix(a.val, "--"):
				v := trimToken(a, 2)
				return &v
			}
		}
	}
	return nil
}

// targetDirFlag splits out `-t DIR` / `--target-directory[=]DIR` from the
// operands of mv/cp/ln/install.
func targetDirFlag(args []shellToken, withArg map[string]bool) (ops []shellToken, target *shellToken) {
	var rest []shellToken
	for i := 0; i < len(args); i++ {
		v := args[i].val
		switch {
		case (v == "-t" || v == "--target-directory") && i+1 < len(args):
			t := args[i+1]
			target = &t
			i++
		case strings.HasPrefix(v, "--target-directory="):
			t := trimToken(args[i], len("--target-directory="))
			target = &t
		default:
			rest = append(rest, args[i])
		}
	}
	return operands(rest, withArg), target
}

// sedInPlaceFiles returns the files `sed -i` edits, or nil when sed is not
// editing in place. Handles `-i`, `-iSUF`, BSD's `-i ”`, `--in-place[=SUF]`,
// clustered short flags (`-ni`), and a script given by `-e`/`-f` rather than as
// the first operand.
func sedInPlaceFiles(args []shellToken) []shellToken {
	inPlace, scriptFlag := false, false
	var ops []shellToken
	flags := true
	for i := 0; i < len(args); i++ {
		v := args[i].val
		switch {
		case flags && v == "--":
			flags = false
		case flags && (v == "--in-place" || strings.HasPrefix(v, "--in-place=")):
			inPlace = true
		case flags && (v == "--expression" || v == "--file"):
			scriptFlag = true
			i++
		case flags && (strings.HasPrefix(v, "--expression=") || strings.HasPrefix(v, "--file=")):
			scriptFlag = true
		case flags && strings.HasPrefix(v, "--"):
		case flags && strings.HasPrefix(v, "-") && len(v) > 1:
			for j := 1; j < len(v); j++ {
				switch v[j] {
				case 'i':
					inPlace = true
					// BSD sed takes the suffix as its own word; `''` is the usual one.
					if j == len(v)-1 && i+1 < len(args) && args[i+1].val == "" {
						i++
					}
					j = len(v)
				case 'e', 'f':
					scriptFlag = true
					if j == len(v)-1 {
						i++
					}
					j = len(v)
				case 'l':
					if j == len(v)-1 {
						i++
					}
					j = len(v)
				}
			}
		default:
			ops = append(ops, args[i])
		}
	}
	if !inPlace {
		return nil
	}
	if !scriptFlag && len(ops) > 0 {
		ops = ops[1:]
	}
	return ops
}

// perlInPlaceFiles returns the files `perl -i` edits (`-i`, `-i.bak`, `-pi`,
// `-pi -e`), or nil when perl is not editing in place.
func perlInPlaceFiles(args []shellToken) []shellToken {
	inPlace, script := false, false
	var ops []shellToken
	flags := true
	for i := 0; i < len(args); i++ {
		v := args[i].val
		switch {
		case flags && v == "--":
			flags = false
		case flags && strings.HasPrefix(v, "-") && len(v) > 1:
		cluster:
			for j := 1; j < len(v); j++ {
				switch v[j] {
				case 'i':
					inPlace = true
					break cluster
				case 'e', 'E':
					script = true
					if j == len(v)-1 {
						i++
					}
					break cluster
				case 'M', 'm', 'I', 'x', '0', 'l', 'C', 'd', 'D':
					break cluster
				}
			}
		default:
			flags = false
			ops = append(ops, args[i])
		}
	}
	if !inPlace {
		return nil
	}
	if !script && len(ops) > 0 {
		ops = ops[1:] // the script file
	}
	return ops
}

// curlWrites recognizes `curl -o f`, `--output f`, `-sSLo f`, and `-O` /
// `--remote-name` (which write into --output-dir or the current directory).
func curlWrites(dir string, args []shellToken) []bashWrite {
	var out []bashWrite
	remote := false
	for i := 0; i < len(args); i++ {
		v := args[i].val
		switch {
		case v == "--output" && i+1 < len(args):
			out = appendTarget(out, dir, "curl -o", args[i+1])
			i++
		case strings.HasPrefix(v, "--output="):
			out = appendTarget(out, dir, "curl -o", trimToken(args[i], len("--output=")))
		case v == "--remote-name" || v == "--remote-name-all":
			remote = true
		case strings.HasPrefix(v, "-") && !strings.HasPrefix(v, "--"):
			if strings.ContainsRune(v, 'O') {
				remote = true
			}
			if j := strings.IndexByte(v, 'o'); j > 0 {
				if j == len(v)-1 && i+1 < len(args) {
					out = appendTarget(out, dir, "curl -o", args[i+1])
					i++
				} else if j < len(v)-1 {
					out = appendTarget(out, dir, "curl -o", trimToken(args[i], j+1))
				}
			}
		}
	}
	if remote {
		if d := flagValue(args, "--output-dir"); d != nil {
			out = appendTarget(out, dir, "curl -O", *d)
		} else if filepath.IsAbs(dir) {
			out = append(out, bashWrite{Path: dir, Construct: "curl -O"})
		}
	}
	return out
}

// appendTarget resolves t and appends it. A literal word that resolves to
// nothing — relative, under a directory that is not known — is dropped; a
// dynamic one is kept, marked, so a test can see it was recognized.
func appendTarget(out []bashWrite, dir, construct string, t shellToken) []bashWrite {
	if t.val == "-" && t.cut < 0 {
		return out
	}
	p, dyn := targetPath(dir, t)
	if p == "" && !dyn {
		return out
	}
	return append(out, bashWrite{Path: p, Construct: construct, Dynamic: dyn})
}

// tarExtracts reports whether tar is extracting: `-x…`, `--extract`, `--get`,
// or old-style bundled first argument (`xzf`).
func tarExtracts(args []shellToken) bool {
	for i, a := range args {
		v := a.val
		switch {
		case v == "--extract" || v == "--get":
			return true
		case strings.HasPrefix(v, "-") && !strings.HasPrefix(v, "--") && strings.ContainsRune(v, 'x'):
			return true
		case i == 0 && !strings.HasPrefix(v, "-") && strings.ContainsRune(v, 'x'):
			return true
		}
	}
	return false
}

// findWrites recognizes `find P… -delete` and `find P… -exec rm …`, judging each
// starting point P (the current directory when none is given).
func findWrites(dir string, args []shellToken) []bashWrite {
	var roots []shellToken
	i := 0
	for ; i < len(args); i++ {
		v := args[i].val
		if strings.HasPrefix(v, "-") || v == "(" || v == "!" {
			break
		}
		roots = append(roots, args[i])
	}
	deletes := false
	for ; i < len(args); i++ {
		v := args[i].val
		if v == "-delete" {
			deletes = true
		}
		if (v == "-exec" || v == "-execdir" || v == "-ok" || v == "-okdir") && i+1 < len(args) {
			switch filepath.Base(args[i+1].val) {
			case "rm", "rmdir", "unlink", "shred":
				deletes = true
			}
		}
	}
	if !deletes {
		return nil
	}
	if len(roots) == 0 {
		roots = []shellToken{{val: ".", cut: -1}}
	}
	var out []bashWrite
	for _, r := range roots {
		out = appendTarget(out, dir, "find -delete", r)
	}
	return out
}

// gitWorktreeWrites recognizes the git subcommands that rewrite a working tree
// and returns that working tree's top as the target. On trial behind
// gitWritesCheck; see its comment for how to remove it.
func gitWorktreeWrites(dir string, args []shellToken) []bashWrite {
	sub, gdir, rest, workTree := gitSubcommand(dir, args)
	if !gitMutates(sub, rest) {
		return nil
	}
	construct := "git " + sub
	if workTree != "" {
		return []bashWrite{{Path: workTree, Construct: construct}}
	}
	if !filepath.IsAbs(gdir) {
		return nil
	}
	top := gitToplevel(gdir)
	if top == "" {
		return nil
	}
	return []bashWrite{{Path: top, Construct: construct}}
}

// gitSubcommand reads git's global options — `-C dir` (cumulative), `-c k=v`,
// `--work-tree`, `--git-dir`, … — and returns the subcommand, the directory it
// runs in, its arguments, and an explicit work tree if one was given.
func gitSubcommand(dir string, args []shellToken) (sub, gdir string, rest []shellToken, workTree string) {
	gdir = dir
	for i := 0; i < len(args); i++ {
		v := args[i].val
		switch {
		case v == "-C" && i+1 < len(args):
			i++
			if args[i].cut >= 0 {
				gdir = ""
			} else {
				gdir = resolveDir(gdir, args[i].val)
			}
		case v == "--work-tree" && i+1 < len(args):
			i++
			workTree, _ = targetPath(gdir, args[i])
		case strings.HasPrefix(v, "--work-tree="):
			workTree, _ = targetPath(gdir, trimToken(args[i], len("--work-tree=")))
		case (v == "-c" || v == "--git-dir" || v == "--namespace" || v == "--exec-path") && i+1 < len(args):
			i++
		case strings.HasPrefix(v, "-"):
		default:
			return v, gdir, args[i+1:], workTree
		}
	}
	return "", gdir, nil, workTree
}

// gitMutates reports whether `git sub rest…` rewrites the working tree.
func gitMutates(sub string, rest []shellToken) bool {
	switch sub {
	case "checkout", "switch", "mv", "rm", "merge", "rebase", "pull", "cherry-pick", "revert", "am":
		return true
	case "restore":
		// `--staged` alone touches only the index.
		return !hasFlag(rest, "--staged", "-S") || hasFlag(rest, "--worktree", "-W")
	case "reset":
		return hasFlag(rest, "--hard", "--merge", "--keep")
	case "clean":
		return !hasFlag(rest, "-n", "--dry-run")
	case "apply":
		if hasFlag(rest, "--check", "--cached") {
			return false
		}
		return !hasFlag(rest, "--stat", "--numstat", "--summary") || hasFlag(rest, "--apply")
	case "stash":
		if len(rest) == 0 {
			return true
		}
		switch rest[0].val {
		case "push", "save", "pop", "apply":
			return true
		}
		return strings.HasPrefix(rest[0].val, "-") // `git stash -u` is a push
	}
	return false
}

// gitToplevel is the working-tree top for dir, or "" outside a repo. A test
// seam.
var gitToplevel = func(dir string) string {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// ─── the gate ─────────────────────────────────────────────────────────────────

// blockBashWriteTargetsIfApplicable refuses a Bash command with a recognized
// write target that writeTargetDecision refuses. It applies only to a session
// that owns a claimed worktree (E-940 decision 1): a taskless session keeps a
// usable shell in main, and E-1586 recorded why extending main-checkout
// refusals to Bash makes main unusable (`just install`, `git pull`, …).
func blockBashWriteTargetsIfApplicable(projectID int64, payload claudePayload) {
	if msg, block := bashWriteDecision(projectID, payload); block {
		blockToolUse(msg)
	}
}

// bashWriteDecision is the side-effect-free core of the Bash gate.
func bashWriteDecision(projectID int64, payload claudePayload) (msg string, block bool) {
	var input toolInputBash
	if err := json.Unmarshal(payload.ToolInput, &input); err != nil || input.Command == "" {
		return "", false
	}
	scope := newWriteScope(projectID, payload)
	if scope.worktree == "" {
		return "", false
	}
	gitWrites := monitor.IsCheckEnabled(projectID, gitWritesCheck)
	return bashWritesRefusal(scope, bashWriteTargets(input.Command, payload.CWD, gitWrites))
}

// bashWritesRefusal judges each target in order; the first refusal wins. The
// message is the one a Write to that path gets, prefixed with the construct
// that was recognized.
func bashWritesRefusal(scope writeScope, writes []bashWrite) (msg string, block bool) {
	for _, w := range writes {
		target := resolveWriteTarget("", w.Path)
		body, block := writeTargetDecision(scope, target)
		if !block {
			continue
		}
		var b strings.Builder
		fmt.Fprintf(&b, "`%s` would write %s\n\n%s", w.Construct, tildePath(target), body)
		if strings.HasPrefix(w.Construct, "git ") {
			fmt.Fprintf(&b, "\n\nGit working-tree commands are judged as writes on trial. If this "+
				"refusal is a false positive, turn them off with\n"+
				`  "checks": {"%s": false}`+"\nin .endless/config.json — and say so, so the trial "+
				"hears about it.", gitWritesCheck)
		}
		return b.String(), true
	}
	return "", false
}

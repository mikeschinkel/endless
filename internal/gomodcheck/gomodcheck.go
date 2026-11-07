// Package gomodcheck checks that a Go module's go.mod and go.sum are tidy the
// way the main checkout, which has no go.work, would see them (E-2569).
//
// A task worktree holding a Go module carries a go.work (E-2563), and Go's
// workspace mode accepts a go.mod that understates a requirement: `go build`,
// `go vet` and `go test` all pass there, while the same commands in the main
// checkout refuse with "updates to go.mod needed". So an untidy go.mod passed
// every verify and broke main after the land. This package is the check that
// closes the gap: `go mod tidy -diff` under GOWORK=off, run on the module in
// the worktree, exits 1 and prints the change when go.mod or go.sum would
// change. `go mod tidy` already ignores go.work; GOWORK=off is belt and braces.
//
// One complication is a replace directive with a relative path. A worktree sits
// three directories below its main checkout, so `=> ../go-pkgs/x` resolves from
// main and not from the worktree, and a plain tidy there fails with
// "replacement directory does not exist" before it can say anything about
// drift. The check therefore does not run on the worktree's go.mod directly: it
// copies the file to a temporary directory with every relative replace path
// rewritten to the absolute path it resolves to from the module's counterpart
// directory in the main checkout, copies go.sum beside it (tidy reads the
// go.sum next to the modfile), and passes the copy with -modfile, with the
// module's own directory as the working directory. A replace for a module that
// is not in the require list is left to Go, which tolerates it.
package gomodcheck

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/mikeschinkel/go-dt"
)

const (
	GoModFile  = "go.mod"
	GoSumFile  = "go.sum"
	GoWorkFile = "go.work"

	// minGoMinor is the minor version of Go 1 that gave `go mod tidy` its
	// -diff flag, and MinGoVersion spells it the way GOVERSION does. An older
	// toolchain fails the check naming this, rather than skipping it: skipping
	// would reinstate the mask silently.
	minGoMinor   = 23
	MinGoVersion = "go1.23"
)

// PrunedDirs are the directories a module search never enters, the same four
// E-2563's go.work writer skips (go_support.PRUNED_DIRS): `.endless/` holds
// the sandbox and scratch, `vendor/` holds copies of other people's modules,
// `testdata/` holds fixtures that are modules on purpose, and `.git` is never a
// module whether it is a file (worktree) or a directory (checkout). The rule is
// duplicated here because the runner is Go and the writer is Python.
var PrunedDirs = []string{".endless", ".git", "testdata", "vendor"}

// Modules returns the directories under worktree that hold a go.mod, as
// slash-separated paths relative to worktree ("." for the root), sorted, with
// PrunedDirs skipped.
func Modules(worktree dt.DirPath) (dirs []string, err error) {
	var root string

	root = string(worktree)
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, werr error) (e error) {
		var rel string

		if werr != nil {
			e = werr
			goto end
		}
		if d.IsDir() {
			if path != root && slices.Contains(PrunedDirs, d.Name()) {
				e = fs.SkipDir
			}
			goto end
		}
		if d.Name() != GoModFile {
			goto end
		}
		rel, e = filepath.Rel(root, filepath.Dir(path))
		if e != nil {
			goto end
		}
		dirs = append(dirs, filepath.ToSlash(rel))
	end:
		return e
	})
	if err != nil {
		err = NewErr(ErrFindingModules, err, "worktree", worktree)
		dirs = nil
		goto end
	}
	slices.Sort(dirs)
end:
	return dirs, err
}

// MainCheckout returns the root of the main checkout that root belongs to: the
// parent of `git rev-parse --git-common-dir`, which resolves to the main
// checkout's .git from inside any linked worktree. A directory git does not
// recognize as a repository is its own main checkout — there is nothing to
// re-root against — so the check still runs there, unre-rooted.
func MainCheckout(root dt.DirPath) (main dt.DirPath, err error) {
	var out []byte
	var ee *exec.ExitError
	var common string

	// --path-format=absolute (git 2.31) removes the ambiguity of an answer
	// relative to the worktree; without it, the answer is joined to root.
	out, err = exec.Command("git", "-C", string(root), "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil && errors.As(err, &ee) {
		// Not a repository, or a git too old for the flag: try the bare form.
		out, err = exec.Command("git", "-C", string(root), "rev-parse", "--git-common-dir").Output()
	}
	switch {
	case err == nil:
		common = strings.TrimSpace(string(out))
		if common == "" {
			main = root
			goto end
		}
		if !filepath.IsAbs(common) {
			common = filepath.Join(string(root), common)
		}
		main = dt.DirPath(filepath.Dir(filepath.Clean(common)))
	case errors.As(err, &ee):
		// Not a git repository.
		main = root
		err = nil
	default:
		// git itself could not be run.
		err = NewErr(ErrResolvingMain, err, "root", root)
	}
end:
	return main, err
}

// RerootModfile writes a copy of the go.mod at src into dstDir with every
// relative replace path rewritten to the absolute path it resolves to from
// mainModuleDir, and copies the go.sum beside src, when there is one, beside
// the copy. It returns the copy's path and the relative paths it rewrote, in
// file order, so a caller can say when re-rooting was in play.
//
// The rewrite is line-based and otherwise byte-preserving, so the diff tidy
// prints against the copy reads exactly as it would against the original.
func RerootModfile(src dt.Filepath, mainModuleDir, dstDir dt.DirPath) (modfile dt.Filepath, rerooted []string, err error) {
	var raw, sum []byte
	var sumSrc dt.Filepath
	var exists bool

	raw, err = src.ReadFile()
	if err != nil {
		err = NewErr(ErrReadingModfile, err, "filepath", src)
		goto end
	}
	raw, rerooted = rerootReplaces(raw, string(mainModuleDir))

	modfile = dt.FilepathJoin(dstDir, GoModFile)
	err = modfile.WriteFile(raw, 0o644)
	if err != nil {
		err = NewErr(ErrWritingModfile, err, "filepath", modfile)
		goto end
	}

	sumSrc = dt.FilepathJoin(src.Dir(), GoSumFile)
	exists, err = sumSrc.Exists()
	if err != nil {
		err = NewErr(ErrCopyingSum, err, "filepath", sumSrc)
		goto end
	}
	if !exists {
		goto end
	}
	sum, err = sumSrc.ReadFile()
	if err != nil {
		err = NewErr(ErrCopyingSum, err, "filepath", sumSrc)
		goto end
	}
	err = dt.FilepathJoin(dstDir, GoSumFile).WriteFile(sum, 0o644)
	if err != nil {
		err = NewErr(ErrCopyingSum, err, "dir", dstDir)
		goto end
	}
end:
	if err != nil {
		modfile = ""
		rerooted = nil
	}
	return modfile, rerooted, err
}

// rerootReplaces rewrites the relative replace paths in a go.mod's bytes, in
// both the one-line form (`replace a => ../b`) and the block form, and returns
// the result with the original relative paths it rewrote.
func rerootReplaces(raw []byte, mainModuleDir string) (out []byte, rerooted []string) {
	var lines []string
	var inBlock bool
	var trimmed, rel string

	lines = strings.Split(string(raw), "\n")
	for i, line := range lines {
		trimmed = strings.TrimSpace(line)
		switch {
		case inBlock && strings.HasPrefix(trimmed, ")"):
			inBlock = false
			continue
		case inBlock:
		case strings.HasPrefix(trimmed, "replace"):
			if strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(trimmed, "replace")), "(") {
				inBlock = true
				continue
			}
		default:
			continue
		}
		lines[i], rel = rerootReplaceLine(line, mainModuleDir)
		if rel != "" {
			rerooted = append(rerooted, rel)
		}
	}
	out = []byte(strings.Join(lines, "\n"))
	return out, rerooted
}

// rerootReplaceLine rewrites one replace spec's path when it is relative
// (`./x` or `../x`, bare or quoted), returning the line and the relative path
// it replaced, or the line unchanged and "".
func rerootReplaceLine(line, mainModuleDir string) (out, rel string) {
	var idx, start, stop int
	var after, token, path, abs string
	var quoted bool
	var uerr error

	out = line
	idx = strings.Index(line, "=>")
	if idx < 0 {
		goto end
	}
	after = line[idx+2:]
	start = len(after) - len(strings.TrimLeft(after, " \t"))
	stop = start + tokenLen(after[start:])
	token = after[start:stop]
	if token == "" {
		goto end
	}
	path = token
	if strings.HasPrefix(token, `"`) {
		path, uerr = strconv.Unquote(token)
		if uerr != nil {
			goto end
		}
		quoted = true
	}
	if !strings.HasPrefix(path, "./") && !strings.HasPrefix(path, "../") {
		goto end
	}
	abs = filepath.Clean(filepath.Join(mainModuleDir, filepath.FromSlash(path)))
	if quoted || strings.ContainsAny(abs, " \t\"") {
		abs = strconv.Quote(abs)
	}
	out = line[:idx+2] + after[:start] + abs + after[stop:]
	rel = path
end:
	return out, rel
}

// tokenLen is the length of the go.mod token at the start of s: a quoted
// string runs to its closing quote, anything else to the next blank.
func tokenLen(s string) (n int) {
	if strings.HasPrefix(s, `"`) {
		for i := 1; i < len(s); i++ {
			switch s[i] {
			case '\\':
				i++
			case '"':
				n = i + 1
				goto end
			}
		}
	}
	n = strings.IndexAny(s, " \t")
	if n < 0 {
		n = len(s)
	}
end:
	return n
}

// Result is one module's verdict. Tidy is true when go.mod and go.sum would
// not change. Otherwise Diff holds the unified diff tidy printed (empty when
// tidy failed before it could compare, as with a replacement directory that
// does not exist) and Detail holds its stderr. Rerooted names the relative
// replace paths that were rewritten for the run, so a reader can be told that
// a plain `go mod tidy` in the worktree would not resolve them. OldGo, when
// set, is the GOVERSION of a `go` too old for -diff, and the only thing the
// verdict says: tidy was not run.
type Result struct {
	Tidy     bool
	Diff     string
	Detail   string
	Rerooted []string
	OldGo    string
}

// Run checks the module at moduleDir: it re-roots a copy of its go.mod against
// mainModuleDir, the module's counterpart directory in the main checkout, into
// a temporary directory and runs `go mod tidy -diff -modfile=<copy>` there with
// GOWORK=off and moduleDir as the working directory, under env. A `go` older
// than 1.23 is a failing verdict carrying the version found in OldGo. An error
// is infrastructure: go could not be run, or the copy could not be written.
func Run(moduleDir, mainModuleDir dt.DirPath, env []string) (res Result, err error) {
	var tmp string
	var modfile dt.Filepath
	var cmd *exec.Cmd
	var stdout, stderr bytes.Buffer
	var ee *exec.ExitError

	res.OldGo, err = oldGoVersion(moduleDir, env)
	if err != nil || res.OldGo != "" {
		goto end
	}

	tmp, err = os.MkdirTemp("", "endless-gomod-")
	if err != nil {
		err = NewErr(ErrMakingTempDir, err)
		goto end
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	modfile, res.Rerooted, err = RerootModfile(dt.FilepathJoin(moduleDir, GoModFile), mainModuleDir, dt.DirPath(tmp))
	if err != nil {
		goto end
	}

	cmd = exec.Command("go", "mod", "tidy", "-diff", "-modfile="+string(modfile))
	cmd.Dir = string(moduleDir)
	cmd.Env = withGoworkOff(env)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	switch {
	case err == nil:
		res.Tidy = true
	case errors.As(err, &ee):
		// Exit 1 is tidy's verdict, whether it printed a diff or stopped with
		// an error of its own; either way the row carries what it said.
		res.Diff = stdout.String()
		res.Detail = strings.TrimSpace(stderr.String())
		err = nil
	default:
		err = NewErr(ErrRunningTidy, err, "dir", moduleDir)
	}
end:
	return res, err
}

// withGoworkOff returns env with GOWORK set to off, replacing any value.
func withGoworkOff(env []string) (out []string) {
	out = make([]string, 0, len(env)+1)
	for _, kv := range env {
		if !strings.HasPrefix(kv, "GOWORK=") {
			out = append(out, kv)
		}
	}
	out = append(out, "GOWORK=off")
	return out
}

// goVersionRe reads the major and minor out of a GOVERSION such as go1.27.3,
// go1.23rc1 or "devel go1.28-abcdef".
var goVersionRe = regexp.MustCompile(`go(\d+)\.(\d+)`)

// oldGoVersion asks the `go` on PATH for its version, from dir under env so
// the answer is the toolchain tidy will run with, and returns it when it is
// older than Go 1.23, else "". A version it cannot parse is not refused: the
// tidy run itself then says whether -diff is understood.
func oldGoVersion(dir dt.DirPath, env []string) (old string, err error) {
	var cmd *exec.Cmd
	var out []byte
	var version string
	var major, minor int

	cmd = exec.Command("go", "env", "GOVERSION")
	cmd.Dir = string(dir)
	cmd.Env = withGoworkOff(env)
	out, err = cmd.Output()
	if err != nil {
		err = NewErr(ErrGoVersion, err)
		goto end
	}
	version = strings.TrimSpace(string(out))
	major, minor, err = parseGoVersion(version)
	if err != nil {
		err = nil
		goto end
	}
	if major < 1 || (major == 1 && minor < minGoMinor) {
		old = version
	}
end:
	return old, err
}

// parseGoVersion extracts major and minor from a GOVERSION string.
func parseGoVersion(version string) (major, minor int, err error) {
	var m []string

	m = goVersionRe.FindStringSubmatch(version)
	if m == nil {
		err = fmt.Errorf("unrecognized go version %q", version)
		goto end
	}
	major, _ = strconv.Atoi(m[1])
	minor, _ = strconv.Atoi(m[2])
end:
	return major, minor, err
}

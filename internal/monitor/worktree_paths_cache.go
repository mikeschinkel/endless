package monitor

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// E-2164 — the changed-path set of each task worktree, for the ordering graph's
// detected conflicts (`E-1 <> E-2`: both worktrees touch a common path).
//
// Same shape as the unlanded cache (unlanded_cache.go, ED-1589): a background job
// computes, the display reads. `session status` renders in the monitor's refresh
// loop, and a `git diff` per worktree per refresh is exactly the cost ED-1589
// moved off the render path. So the READER here issues no git subprocess at all
// — it reads files — and a missing or stale entry means "no detected conflict
// for this task", which degrades the graph to ordering only. That is still
// correct, just less informed.
//
// # Layout
//
//	<git-common-dir>/info/endless/paths/e-<task-id>
//
// One file per task worktree, in the directory every worktree of the repo
// shares. Contents:
//
//	head <oid>        the worktree's HEAD when computed
//	base <oid>        the base branch tip it was diffed against
//	status <sha256>   digest of `git status --porcelain -z` (the dirty marker)
//	<path>            one repo-relative changed path per line, sorted
//
// The job recomputes an entry only when head, base or status differ from what it
// holds. The reader validates head alone — it can resolve HEAD from the files git
// keeps on disk, while the other two keys cannot be read without asking git. An
// uncommitted edit made since the job's last pass is therefore visible one
// interval late; a moved HEAD is never trusted.

const worktreePathsCacheRel = "info/endless/paths"

// worktreePathsExcluded are the paths Endless itself writes, which say nothing
// about what a task's code touches: the event ledger, the verb cache, and the
// document mirrors of tasks and decisions (a task's own verify suite lives
// beside its mirrors and is per-task by construction). Counting them would make
// nearly every pair of task worktrees conflict. Everything else under .endless/
// — hooks, migrations, config.json — is the project's own and does count.
var worktreePathsExcluded = []string{
	".endless/db-ledger/",
	".endless/decisions/",
	".endless/tasks/",
	".endless/LESSONS.md",
	".endless/verbs.jsonl",
}

func isExcludedChangedPath(p string) bool {
	for _, x := range worktreePathsExcluded {
		if strings.HasSuffix(x, "/") {
			if strings.HasPrefix(p, x) {
				return true
			}
			continue
		}
		if p == x {
			return true
		}
	}
	return false
}

// worktreePathsEntry is one parsed cache file.
type worktreePathsEntry struct {
	head   string
	base   string
	status string
	paths  []string
}

func (e worktreePathsEntry) encode() []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "head %s\nbase %s\nstatus %s\n", e.head, e.base, e.status)
	for _, p := range e.paths {
		b.WriteString(p)
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

// readWorktreePathsEntry parses one cache file. Anything malformed reads as
// absent: this is rebuildable derived state, and a miss is always safe.
func readWorktreePathsEntry(path string) (worktreePathsEntry, bool) {
	f, err := os.Open(path)
	if err != nil {
		return worktreePathsEntry{}, false
	}
	defer f.Close()
	var e worktreePathsEntry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for i := 0; sc.Scan(); i++ {
		ln := sc.Text()
		switch i {
		case 0:
			e.head = strings.TrimPrefix(ln, "head ")
		case 1:
			e.base = strings.TrimPrefix(ln, "base ")
		case 2:
			e.status = strings.TrimPrefix(ln, "status ")
		default:
			if ln != "" {
				e.paths = append(e.paths, ln)
			}
		}
	}
	if sc.Err() != nil || !oidRe.MatchString(e.head) || !oidRe.MatchString(e.base) {
		return worktreePathsEntry{}, false
	}
	return e, true
}

// RefreshWorktreePathsCache brings one repository's changed-path cache up to
// date for the task worktrees whose task is in open. A worktree whose task is
// not open (finished, or unknown to this database) is skipped and its entry
// removed: the graph never draws a finished task, and a retained post-land
// worktree is the common case, so computing them would be most of the cost for
// none of the value.
//
// Per-worktree git failures write nothing, which is already the correct
// representation of "unknown". Only conditions that stop the whole pass are
// returned.
func RefreshWorktreePathsCache(ctx context.Context, repoDir string, open map[int64]bool) error {
	if !hasTaskWorktrees(repoDir) {
		return nil
	}
	common, err := gitCommonDir(ctx, repoDir)
	if err != nil {
		return nil // not a repository git will answer about: nothing to cache
	}
	dir := filepath.Join(common, filepath.FromSlash(worktreePathsCacheRel))
	if err = os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	base, err := DefaultBranch(ctx, repoDir)
	if err != nil {
		return nil // recorded by the unlanded refresh, which fails the same way
	}
	baseTip, err := revOID(ctx, repoDir, base)
	if err != nil {
		return fmt.Errorf("resolve tip of %s in %s: %w", base, repoDir, err)
	}
	refs, err := listWorktrees(ctx, repoDir)
	if err != nil {
		return fmt.Errorf("list worktrees of %s: %w", repoDir, err)
	}

	keep := map[string]bool{}
	for _, ref := range refs {
		if ctx.Err() != nil {
			return nil
		}
		// Git reports worktree paths resolved (symlinks followed), so the
		// `.endless/worktrees/e-<id>` convention is matched by name rather than
		// by comparing against repoDir, which may be spelled through a symlink.
		parent := filepath.Dir(filepath.Clean(ref.Path))
		if filepath.Base(parent) != "worktrees" || filepath.Base(filepath.Dir(parent)) != ".endless" {
			continue
		}
		m := worktreeDirRe.FindStringSubmatch(filepath.Base(ref.Path))
		if m == nil {
			continue
		}
		id, _ := strconv.ParseInt(m[1], 10, 64)
		if !open[id] {
			continue
		}
		name := filepath.Base(ref.Path)
		keep[name] = true
		entryPath := filepath.Join(dir, name)

		status, serr := runGit(ctx, ref.Path, "status", "--porcelain=v1", "-z", "--untracked-files=all")
		if serr != nil {
			continue
		}
		sum := sha256.Sum256([]byte(status))
		fp := hex.EncodeToString(sum[:])
		if old, ok := readWorktreePathsEntry(entryPath); ok &&
			old.head == ref.Head && old.base == baseTip && old.status == fp {
			continue
		}
		diff, derr := runGit(ctx, ref.Path, "diff", "--name-only", "-z", baseTip+"...HEAD")
		if derr != nil {
			continue
		}
		set := map[string]bool{}
		for _, p := range strings.Split(diff, "\x00") {
			if p != "" {
				set[p] = true
			}
		}
		for _, p := range porcelainZPaths(status) {
			set[p] = true
		}
		var paths []string
		for p := range set {
			if !isExcludedChangedPath(p) {
				paths = append(paths, p)
			}
		}
		sort.Strings(paths)
		entry := worktreePathsEntry{head: ref.Head, base: baseTip, status: fp, paths: paths}
		if werr := writeFileAtomic(entryPath, entry.encode()); werr != nil {
			return fmt.Errorf("write %s: %w", entryPath, werr)
		}
	}

	// Prune entries for worktrees that are gone or whose task finished.
	if entries, rerr := os.ReadDir(dir); rerr == nil {
		for _, e := range entries {
			if e.IsDir() || keep[e.Name()] {
				continue
			}
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
	return nil
}

// porcelainZPaths parses `git status --porcelain=v1 -z`: records are `XY path`
// separated by NUL, and a rename or copy is followed by one more NUL-terminated
// field holding the ORIGIN path. Both ends of a rename count as touched.
func porcelainZPaths(out string) []string {
	var paths []string
	fields := strings.Split(out, "\x00")
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if len(f) < 4 {
			continue
		}
		paths = append(paths, f[3:])
		if f[0] == 'R' || f[0] == 'C' {
			if i+1 < len(fields) && fields[i+1] != "" {
				paths = append(paths, fields[i+1])
			}
			i++
		}
	}
	return paths
}

// WorktreeChangedPaths returns the cached changed-path set of task taskID's
// worktree in the project rooted at projectRoot, and whether a CURRENT entry
// exists. It runs no git subprocess: the common dir, the worktree's HEAD and the
// entry are all read from files. No worktree, no entry, an unreadable entry, or
// one computed at a different HEAD all answer (nil, false).
func WorktreeChangedPaths(projectRoot string, taskID int64) ([]string, bool) {
	wt, err := worktreePathForTaskAtRoot(projectRoot, taskID)
	if err != nil || wt == "" {
		return nil, false
	}
	common, ok := commonDirFromFiles(projectRoot)
	if !ok {
		return nil, false
	}
	entry, ok := readWorktreePathsEntry(filepath.Join(
		common, filepath.FromSlash(worktreePathsCacheRel), filepath.Base(wt)))
	if !ok {
		return nil, false
	}
	head, ok := headOIDFromFiles(wt, common)
	if !ok || head != entry.head {
		return nil, false
	}
	return entry.paths, true
}

// commonDirFromFiles resolves the git common dir of the checkout at root without
// running git: `<root>/.git` is either the directory itself, or a file naming a
// linked worktree's gitdir, whose `commondir` file names the shared one.
func commonDirFromFiles(root string) (string, bool) {
	dotgit := filepath.Join(root, ".git")
	fi, err := os.Stat(dotgit)
	if err != nil {
		return "", false
	}
	if fi.IsDir() {
		return dotgit, true
	}
	gitdir, ok := gitdirFromFile(dotgit, root)
	if !ok {
		return "", false
	}
	data, err := os.ReadFile(filepath.Join(gitdir, "commondir"))
	if err != nil {
		return "", false
	}
	c := strings.TrimSpace(string(data))
	if !filepath.IsAbs(c) {
		c = filepath.Join(gitdir, c)
	}
	return filepath.Clean(c), true
}

// gitdirFromFile reads a `.git` FILE ("gitdir: <path>") and returns the absolute
// gitdir it names.
func gitdirFromFile(dotgit, base string) (string, bool) {
	data, err := os.ReadFile(dotgit)
	if err != nil {
		return "", false
	}
	line := strings.TrimSpace(string(data))
	if !strings.HasPrefix(line, "gitdir:") {
		return "", false
	}
	p := strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
	if !filepath.IsAbs(p) {
		p = filepath.Join(base, p)
	}
	return filepath.Clean(p), true
}

// headOIDFromFiles resolves the HEAD of the linked worktree at wt to an object
// id by reading git's files: the worktree's HEAD, then the ref it names as a
// loose ref (per-worktree gitdir first, then the common dir), then packed-refs.
func headOIDFromFiles(wt, common string) (string, bool) {
	gitdir := filepath.Join(wt, ".git")
	if fi, err := os.Stat(gitdir); err != nil {
		return "", false
	} else if !fi.IsDir() {
		var ok bool
		if gitdir, ok = gitdirFromFile(gitdir, wt); !ok {
			return "", false
		}
	}
	data, err := os.ReadFile(filepath.Join(gitdir, "HEAD"))
	if err != nil {
		return "", false
	}
	head := strings.TrimSpace(string(data))
	if !strings.HasPrefix(head, "ref:") {
		return head, oidRe.MatchString(head)
	}
	ref := strings.TrimSpace(strings.TrimPrefix(head, "ref:"))
	for _, d := range []string{gitdir, common} {
		if b, err := os.ReadFile(filepath.Join(d, filepath.FromSlash(ref))); err == nil {
			oid := strings.TrimSpace(string(b))
			return oid, oidRe.MatchString(oid)
		}
	}
	f, err := os.Open(filepath.Join(common, "packed-refs"))
	if err != nil {
		return "", false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		ln := sc.Text()
		if oid, name, ok := strings.Cut(ln, " "); ok && name == ref && oidRe.MatchString(oid) {
			return oid, true
		}
	}
	return "", false
}

// OpenTaskIDs returns the ids of every live task whose status is not terminal —
// the tasks whose worktrees the changed-path job keeps current.
func OpenTaskIDs() (map[int64]bool, error) {
	db, err := DB()
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT id FROM live_tasks WHERE status NOT IN (` + terminalStatusSet + `)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

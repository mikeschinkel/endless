package monitor

import (
	"context"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
)

// E-2128 — the bounded pool every cold unlanded computation goes through, and
// the order it works in.
//
// Two callers share it: the background job's cold pass over every worktree git
// lists, and `task unsettled --all`, which batches every active worktree into a
// single `session-query` call. Serially, a cold cache costs ~584ms per worktree
// — over a minute of silence before the first row prints on a repo with a
// hundred of them. Through the pool the wall clock is bounded by the pool size
// instead.
//
// SERIAL callers deliberately stay out: the reaper computing one condition-4
// answer, and `task unsettled <id>` on one worktree. A pool of one is just
// overhead with a goroutine attached.

// unlandedPoolSize bounds concurrent git children.
//
// git is CPU-bound here — computing patch-ids for range-diff is the work — so
// oversubscribing one repository buys nothing and starves whatever else the
// machine is doing. The floor of 8 is not a measurement, it is a refusal to let
// a 32-core machine fork 32 range-diffs at a process that is, after all, a
// background job.
func unlandedPoolSize() int {
	n := runtime.NumCPU()
	if n > 8 {
		n = 8
	}
	if n < 1 {
		n = 1
	}
	return n
}

// unlandedRequest is one worktree to compute, with the base its branch is
// measured against. The base travels with the request rather than being resolved
// per item: it is a property of the repository, already resolved once by the
// caller.
type unlandedRequest struct {
	Dir  string
	Base string
}

// unlandedOutcome is one computed verdict, or the error that stopped it. Commits
// is nil both for "settled" and for a failure, so Err is what distinguishes
// them — exactly as it does on the single-worktree path.
type unlandedOutcome struct {
	Dir     string
	Commits []string
	Err     error
}

// computeUnlandedBatch computes and caches every request through the bounded
// pool, returning outcomes in the SAME order as reqs.
//
// Order is preserved by indexing rather than by appending, so a caller can zip
// the results back onto its own list without carrying a key. A cancelled ctx
// stops the in-flight git children (runGit builds with exec.CommandContext) and
// their outcomes carry ErrGitInterrupted, which records no fault.
func computeUnlandedBatch(ctx context.Context, reqs []unlandedRequest) []unlandedOutcome {
	out := make([]unlandedOutcome, len(reqs))
	if len(reqs) == 0 {
		return out
	}

	sem := make(chan struct{}, unlandedPoolSize())
	var wg sync.WaitGroup
	for i, req := range reqs {
		wg.Add(1)
		go func(i int, req unlandedRequest) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			commits, err := computeUnlandedAndCache(ctx, req.Dir, req.Base)
			out[i] = unlandedOutcome{Dir: req.Dir, Commits: commits, Err: err}
		}(i, req)
	}
	wg.Wait()
	return out
}

// worktreeRef is one worktree as `git worktree list --porcelain` reports it: its
// path and the OID its HEAD resolves to. One git call yields every one of them —
// measured at 14ms for 135 worktrees on this repo — which is the entire
// enumeration-and-tip-reading step of the job's pass.
type worktreeRef struct {
	Path string
	Head string
}

// listWorktrees returns every worktree git knows about in the repository
// containing repoDir.
//
// Porcelain output is blank-line-separated blocks, each opening with
// `worktree <path>` and normally carrying `HEAD <oid>`. A block with no usable
// HEAD — a worktree git lists but cannot resolve — is DROPPED rather than
// returned with an empty tip: the cache is keyed on that tip, and an empty key
// is not a miss, it is a collision.
func listWorktrees(ctx context.Context, repoDir string) ([]worktreeRef, error) {
	out, err := runGit(ctx, repoDir, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, gitProbeError{
			Command: "git worktree list",
			Detail:  firstLine(out, err),
			Err:     err,
		}
	}
	var refs []worktreeRef
	var cur worktreeRef
	flush := func() {
		if cur.Path != "" && oidRe.MatchString(cur.Head) {
			refs = append(refs, cur)
		}
		cur = worktreeRef{}
	}
	for _, ln := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(ln, "worktree "):
			flush()
			cur.Path = strings.TrimSpace(strings.TrimPrefix(ln, "worktree "))
		case strings.HasPrefix(ln, "HEAD "):
			cur.Head = strings.TrimSpace(strings.TrimPrefix(ln, "HEAD "))
		}
	}
	flush()
	return refs, nil
}

// sortWorktreesByRecency orders refs by their directory's mtime, newest first,
// in place.
//
// Ordering matters more than scope here. The job covers every worktree git
// lists, and on a cold cache that is a long pass — so the order decides whether
// the first seconds of it converge on the worktrees somebody is actually working
// in, or on the dormant tail. Directory mtime is one stat per worktree and reads
// no database, which is what keeps the whole probe DB-free (the property E-2087
// established and `_unsettled_probe` documents).
//
// It is a NOISY signal: any write touches a directory's mtime, a build artifact
// as much as a commit. That is acceptable for a heuristic that affects nothing
// but warm-up order — every worktree is computed either way, and a wrong guess
// costs somebody one more interval of `~`. A worktree that cannot be stat'ed
// sorts last rather than failing the pass.
func sortWorktreesByRecency(refs []worktreeRef) {
	mtime := make(map[string]int64, len(refs))
	for _, r := range refs {
		if fi, err := os.Stat(r.Path); err == nil {
			mtime[r.Path] = fi.ModTime().UnixNano()
		}
	}
	sort.SliceStable(refs, func(i, j int) bool {
		return mtime[refs[i].Path] > mtime[refs[j].Path]
	})
}

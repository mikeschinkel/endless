package monitor

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/mikeschinkel/endless/internal/faults"
)

// E-2128 — the WRITER. One pass over one repository: validate what the cache
// already holds, recompute what it does not, advance the watermark, prune what no
// longer has a question behind it.
//
// internal/unlandedjob is the schedule; this is the work. The split is
// internal/backupjob's: the job package owns the cadence and the lease, the
// monitor package owns the database and git.

// baseVerdict is what the watermark comparison concluded about the base branch
// since the last pass.
type baseVerdict int

const (
	// baseAppended — the base only gained commits, or has not moved. Every
	// settled marker still stands, by the shrink-only invariant in
	// unlanded_cache.go.
	baseAppended baseVerdict = iota
	// baseRewritten — the base was amended, reset or force-pushed, or the
	// configured base NAME changed, or there is no watermark to compare against.
	// Every settled marker is invalid and the settled half is flushed.
	baseRewritten
)

// RefreshUnlandedCache brings one repository's unlanded cache up to date.
//
// It is idempotent, which the lease contract requires: every write is keyed on
// content (a branch tip, a base tip), so a re-claimed pass recomputes the same
// answers and rewrites the same paths. A pass that dies part-way leaves a cache
// that is incomplete, never one that is wrong — an absent entry is a miss, and a
// miss renders `~`.
//
// Errors are returned for the conditions that stop the whole pass (no default
// branch, git will not list worktrees). Per-worktree failures are not errors:
// they write nothing, which is already the correct representation of "unknown".
func RefreshUnlandedCache(ctx context.Context, repoDir string) error {
	cache, err := unlandedCacheFor(ctx, repoDir)
	if err != nil {
		// Not a git repository, or one git will not answer about. A registered
		// project that is not a repo has no worktrees and nothing to cache, so
		// there is nothing here to report — and reporting it would fail the sweep
		// for every OTHER project once a minute, forever, which is what E-2128
		// shipped and what ERR-0001 was counting.
		return nil
	}
	if err = os.MkdirAll(cache.dir, 0o755); err != nil {
		// A read-only checkout or a permissions problem. Every lookup in this repo
		// is a permanent miss from here on: displays show `~` and on-demand callers
		// recompute every time. Correct, just not fast — so it is recorded once,
		// against the directory, and the pass ends without an error the runner
		// would back off or re-alarm on.
		recordUnlandedCacheFault(cache.dir, err)
		return nil
	}

	base, err := DefaultBranch(ctx, repoDir)
	if err != nil {
		// Recorded, not returned. This is a durable property of ONE repository —
		// it has no discoverable default branch — and the fault is fingerprinted
		// on the repo so it raises a single incident with a rising count. Failing
		// the sweep over it would raise a SECOND incident (ERR-0001, "the job
		// failed") every interval about a condition the first one already names,
		// and would say the job is broken when one project is.
		recordRefreshDefaultBranchFault(repoDir, err)
		return nil
	}
	baseTip, err := revOID(ctx, repoDir, base)
	if err != nil {
		return fmt.Errorf("resolve tip of %s in %s: %w", base, repoDir, err)
	}

	refs, err := listWorktrees(ctx, repoDir)
	if err != nil {
		return fmt.Errorf("list worktrees of %s: %w", repoDir, err)
	}

	if verdict := classifyBaseMovement(ctx, cache, repoDir, base, baseTip); verdict == baseRewritten {
		if err = cache.flushSettled(); err != nil {
			recordUnlandedCacheFault(cache.dir, err)
			return nil
		}
	}

	// The watermark is advanced BEFORE the recomputation, not after. Readers
	// address `unsettled/<base-tip>/` through it, so publishing the new tip first
	// is what makes each entry this pass writes readable the moment it lands. The
	// cost of that order is that a pass which dies half-way leaves the rest of the
	// repo reading `~` until the next one — which is the same one-interval window
	// a cold start already has, and strictly better than readers trusting a tip
	// the cache is no longer being written under.
	if err = cache.writeWatermark(base, baseTip); err != nil {
		recordUnlandedCacheFault(cache.dir, err)
		return nil
	}

	sortWorktreesByRecency(refs)

	var misses []unlandedRequest
	liveHeads := make(map[string]bool, len(refs))
	for _, ref := range refs {
		liveHeads[ref.Head] = true
		if _, known := cache.readEntry(baseTip, ref.Head); known {
			continue
		}
		misses = append(misses, unlandedRequest{Dir: ref.Path, Base: base})
	}
	computeUnlandedBatch(ctx, misses)

	cache.prune(liveHeads, baseTip)
	return nil
}

// classifyBaseMovement runs the watermark half of the validity protocol.
//
// No watermark, or one naming a DIFFERENT base branch, reads as rewritten: the
// settled markers on disk were computed against a base this pass cannot vouch
// for, and "recompute everything" is the only answer that cannot be wrong.
// Editing `default_branch` in .endless/config.json therefore invalidates the
// cache for free, because the name lives in the watermark's filename.
//
// An equal tip needs no git call at all — the overwhelmingly common case, and the
// reason a warm pass is one `git worktree list` plus N path lookups.
//
// A MOVED tip asks git one question: `merge-base --is-ancestor <stored>
// <current>`. Exit 0 means the base only gained commits and every settled marker
// stands. Exit 1 means its history was rewritten. Any other exit means git could
// not tell us, and that fails CLOSED — treated as rewritten, because the cache is
// rebuildable derived state and recomputing it costs one pass, while trusting a
// settled marker that is no longer true costs somebody their work.
func classifyBaseMovement(ctx context.Context, cache unlandedCache, repoDir, base, baseTip string) baseVerdict {
	wm, err := cache.readWatermark()
	if err != nil || wm.base != base {
		return baseRewritten
	}
	if wm.tip == baseTip {
		return baseAppended
	}
	if _, err = runGit(ctx, repoDir, "merge-base", "--is-ancestor", wm.tip, baseTip); err == nil {
		return baseAppended
	}
	return baseRewritten
}

// recordRefreshDefaultBranchFault reports the resolver failure that makes a whole
// repository unjudgeable.
//
// The display path no longer resolves the default branch at all — it reads the
// base NAME from the watermark — so this is where ERR-0011 now comes from for a
// repo nobody is reaping. Fingerprinted on the repo rather than on a worktree,
// which is both the blast radius and an improvement: the condition used to raise
// one incident per worktree per process, and it has exactly one remedy.
func recordRefreshDefaultBranchFault(repoDir string, err error) {
	if errors.Is(err, ErrGitInterrupted) {
		return
	}
	faults.Record(faults.Fault{
		Code:        faults.ErrCodeDefaultBranchUnresolved,
		Source:      "worktree:unlanded-cache",
		Fingerprint: repoDir,
		Summary: fmt.Sprintf(
			"%s: the repository's default branch could not be resolved, so no worktree's unlanded state can be computed",
			displayPath(repoDir)),
		Detail: err.Error(),
		Fields: map[string]any{
			"project_root": repoDir,
			"command":      "monitor.DefaultBranch",
			"error":        err.Error(),
		},
	})
}

// ProjectRoots returns the resolved filesystem root of every registered project
// that still exists on disk, ordered by project id.
//
// A project whose directory has gone — moved, deleted, on an unmounted volume —
// is skipped rather than reported: that is a condition for the project-path
// repair to resolve, and a background sweep must not raise it per pass. Ordering
// by id only makes the pass deterministic; nothing depends on which project is
// visited first.
func ProjectRoots() ([]string, error) {
	db, err := DB()
	if err != nil {
		return nil, err
	}
	rows, err := db.Query("SELECT path FROM projects WHERE status = 'active' ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var roots []string
	for rows.Next() {
		var stored string
		if err = rows.Scan(&stored); err != nil {
			return nil, err
		}
		// The column is STORED form (normally `~/...`); everything below this point
		// hands the path to git and the filesystem (E-2011).
		resolved, rerr := ResolvedProjectPath(stored)
		if rerr != nil || resolved == "" {
			continue
		}
		if fi, serr := os.Stat(resolved); serr != nil || !fi.IsDir() {
			continue
		}
		roots = append(roots, resolved)
	}
	return roots, rows.Err()
}

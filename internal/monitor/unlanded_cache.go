package monitor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/mikeschinkel/endless/internal/faults"
)

// E-2128 / ED-1589 — the exact unlanded verdict is DERIVED STATE, written by one
// background job and read by everything else.
//
// `git range-diff` answers "does this branch hold work the base lacks?" exactly
// (see worktree_unlanded.go for why nothing cheaper is also correct), and it
// costs about 584ms per worktree. Every consumer used to recompute it on every
// tick: the reaper on five Claude hook branches including PreToolUse and
// PostToolUse, and every `session monitor` pane on every rendered row every two
// seconds. This file is the shared answer they read instead.
//
// # The invariant, and why it is checked rather than trusted
//
// The unlanded set can only SHRINK as the base branch gains commits. The probe
// asks, for each branch commit since the fork, whether a matching commit exists
// on base since the fork; adding commits to base only adds candidates to match
// against. A commit that already matched keeps its match; one that did not may
// acquire one. Nothing that matched becomes unmatched. Therefore:
//
//   - a verdict of ZERO unlanded stays true for as long as the BRANCH tip does
//     not move, however far the base advances;
//   - a verdict of N>0 is valid only while BOTH tips are unchanged, because base
//     movement can shrink it.
//
// That rests on the base being append-only, which is an assumption — so it is
// CHECKED, by a repo-level watermark, not trusted. An amended, reset or
// force-pushed base is detected and flushes the settled half; a `--amend` on a
// task branch moves that branch's tip and self-invalidates its own entry.
//
// Two things the invariant deliberately does not cover, both pre-existing and
// neither introduced here: work that landed and was later REVERTED still reads
// as settled (the original landed commit is still in base's history to match
// against), and uncommitted or untracked files are not commits at all — they are
// a separate probe that stays live on every tick.
//
// # There is no file format
//
// Every key is a PATH, and the only file with contents holds exactly the bytes a
// consumer needs:
//
//	<git-common-dir>/info/endless/unlanded/
//	  base-<name>                                  # the watermark: one base-tip OID
//	  settled/<branch-tip-oid>                     # EMPTY file; presence = fully landed
//	  unsettled/<base-tip-oid>/<branch-tip-oid>    # the unlanded commit lines
//
//   - settled/<branch-tip-oid> is empty because "fully landed" is a property of
//     the branch-tip OID and the base branch ALONE — the same HEAD and the same
//     base give the same answer whichever directory the probe ran in — and the
//     invariant above makes it permanent while the base is append-only. There is
//     nothing left to qualify it with, and two worktrees sitting at one OID
//     legitimately share the entry.
//   - unsettled/ nests under the BASE tip because a non-zero verdict is valid
//     only until the base moves. Nesting makes base movement render the whole
//     directory unreachable, so invalidation is a path miss requiring no logic
//     and no stored field.
//   - base-<name> carries the base branch NAME in its filename, so editing
//     `default_branch` in .endless/config.json orphans the old watermark
//     automatically and reads as full invalidation.
//
// Deliberately absent, each removable without breaking a rule: a format version
// (this is rebuildable derived state, so an unreadable file is simply a miss and
// no migration can ever be needed), the worktree path, a per-entry base name, a
// stored probe error (absence already means unknown, and unknown already fails
// closed — a failed compute writes nothing), and a computed-at timestamp (no
// validity rule reads one). Nothing here can drift out of step with git, because
// every key IS a git object id.
//
// # Why .git/info/
//
// `git rev-parse --path-format=absolute --git-common-dir` resolves to the main
// checkout's .git from inside any linked worktree, so every worktree of a repo
// shares one cache directory for free. `.git/info/` is untracked, never pushed,
// per-clone, and survives `git gc` — the right home for derived state describing
// this clone. No database is involved, so sandbox routing does not apply and the
// probe stays DB-free: `--db main` from inside a worktree and a bare run agree by
// construction, the property `_unsettled_probe` documents.

const (
	// unlandedCacheRel is the cache root, relative to the git common dir.
	unlandedCacheRel = "info/endless/unlanded"

	// watermarkPrefix leads the watermark filename; the rest is the escaped base
	// branch name.
	watermarkPrefix = "base-"

	settledSubdir   = "settled"
	unsettledSubdir = "unsettled"
)

// oidRe matches a full git object id. Every cache key is one, and every key is a
// path component, so this is also the guard that keeps a surprising git answer
// from escaping the cache directory.
var oidRe = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

// errNoWatermark means the repo's watermark is absent or ambiguous, so no
// reader can address the cache yet. Not a failure: it is the state of a fresh
// clone until the job's first pass.
var errNoWatermark = errors.New("unlanded cache: no watermark")

// unlandedCache is one repository's cache directory. It holds no state beyond
// the path, so it is free to construct and safe to share.
type unlandedCache struct {
	dir string
}

// unlandedWatermark is the repo-level validity record: the base branch the
// cache was computed against, and that branch's tip as of the last job pass.
type unlandedWatermark struct {
	base string
	tip  string
}

// unlandedLookup is what a reader gets back. Known distinguishes "the verdict is
// established" from "nothing has computed this yet" — collapsing the two is the
// lie ED-1589 exists to stop telling, because a missing answer used to render as
// the all-clear.
type unlandedLookup struct {
	// Base is the base branch named by the watermark, or "" when there is none.
	// Carried even on a miss: it costs nothing and a caller that goes on to
	// compute can skip resolving it.
	Base string
	// Commits are the unlanded commits, newest first, UNBOUNDED. The display
	// sample is capped by the caller exactly as the computing path caps it, so
	// the count stays exact for a branch with more than unlandedLogLimit
	// outstanding (see writeEntry).
	Commits []string
	Known   bool
}

// gitCommonDirCache memoizes the git common dir per directory. A worktree's
// common dir is a structural fact about the repository — it cannot change
// without the worktree being recreated or repaired — so this is memoized with
// the same justification as defaultBranchCache, and for the same reason: a
// monitor pane would otherwise pay one git call per row per tick to re-derive a
// constant.
var gitCommonDirCache sync.Map // dir -> gitCommonDirResult

type gitCommonDirResult struct {
	path string
	err  error
}

// gitCommonDir returns the absolute git common directory for dir — the main
// checkout's .git, whether dir is that checkout or a linked worktree.
//
// An INTERRUPTED resolution is not memoized, for E-2130's reason on
// DefaultBranch: every other outcome is a fact about the repository, while an
// interrupt is a fact about the process that asked and stops being true as soon
// as the signal is handled. Caching it would let one Ctrl-C answer for every
// later lookup of this directory.
func gitCommonDir(ctx context.Context, dir string) (string, error) {
	if cached, ok := gitCommonDirCache.Load(dir); ok {
		res := cached.(gitCommonDirResult)
		return res.path, res.err
	}
	path, err := resolveGitCommonDir(ctx, dir)
	if errors.Is(err, ErrGitInterrupted) {
		return "", err
	}
	gitCommonDirCache.Store(dir, gitCommonDirResult{path: path, err: err})
	return path, err
}

// resetGitCommonDirCache drops every memoized answer. Tests only: the cache is
// keyed by directory, and a test that builds a fresh fixture repo at a path a
// previous test used would otherwise read the previous answer.
func resetGitCommonDirCache() {
	gitCommonDirCache.Range(func(k, _ any) bool {
		gitCommonDirCache.Delete(k)
		return true
	})
}

// resolveGitCommonDir is gitCommonDir without the memoization.
//
// `--path-format=absolute` arrived in git 2.31 and is asked for first because it
// removes all ambiguity. The fallback is not defensive clutter: without the
// flag, `--git-common-dir` may answer a path relative to the WORKTREE, and a
// relative answer joined to the wrong base would put a cache directory somewhere
// nobody looks — silently, since a missing entry is indistinguishable from one
// never computed.
func resolveGitCommonDir(ctx context.Context, dir string) (string, error) {
	out, err := runGit(ctx, dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err == nil {
		if p := strings.TrimSpace(out); filepath.IsAbs(p) {
			return filepath.Clean(p), nil
		}
	}
	if errors.Is(err, ErrGitInterrupted) {
		return "", err
	}
	out, err = runGit(ctx, dir, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", fmt.Errorf("resolve git common dir for %s: %w", dir, err)
	}
	p := strings.TrimSpace(out)
	if p == "" {
		return "", fmt.Errorf("resolve git common dir for %s: git answered nothing", dir)
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	return filepath.Clean(p), nil
}

// unlandedCacheFor returns the cache shared by every worktree of the repository
// containing dir.
func unlandedCacheFor(ctx context.Context, dir string) (unlandedCache, error) {
	common, err := gitCommonDir(ctx, dir)
	if err != nil {
		return unlandedCache{}, err
	}
	return unlandedCache{dir: filepath.Join(common, filepath.FromSlash(unlandedCacheRel))}, nil
}

// worktreeHead returns the OID HEAD resolves to in dir. It is the per-worktree
// half of the cache key, and the reason a `git commit --amend` invalidates
// exactly one entry and nothing else.
func worktreeHead(ctx context.Context, dir string) (string, error) {
	return revOID(ctx, dir, "HEAD")
}

// revOID resolves rev to a commit OID in dir.
func revOID(ctx context.Context, dir, rev string) (string, error) {
	out, err := runGit(ctx, dir, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
	if err != nil {
		return "", gitProbeError{Command: "git rev-parse", Detail: firstLine(out, err), Err: err}
	}
	oid := strings.TrimSpace(out)
	if !oidRe.MatchString(oid) {
		return "", gitProbeError{
			Command: "git rev-parse",
			Detail:  fmt.Sprintf("unusable object id %q for %s", oid, rev),
		}
	}
	return oid, nil
}

// settledPath is the marker asserting that the branch at head holds nothing the
// base lacks.
func (c unlandedCache) settledPath(head string) string {
	return filepath.Join(c.dir, settledSubdir, head)
}

// unsettledPath is the entry holding the unlanded commit lines for head,
// measured against baseTip.
func (c unlandedCache) unsettledPath(baseTip, head string) string {
	return filepath.Join(c.dir, unsettledSubdir, baseTip, head)
}

// readWatermark returns the repo's watermark, or errNoWatermark when it is
// absent, unreadable or ambiguous.
//
// Ambiguity — two `base-*` files — is treated as no watermark rather than
// resolved by a rule. It can only arise from a half-applied write, and guessing
// which of two base branches the cache was computed against is exactly the kind
// of guess this file refuses to make. The next job pass writes one and removes
// the other.
func (c unlandedCache) readWatermark() (unlandedWatermark, error) {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return unlandedWatermark{}, errNoWatermark
	}
	var name string
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), watermarkPrefix) {
			continue
		}
		if name != "" {
			return unlandedWatermark{}, errNoWatermark
		}
		name = e.Name()
	}
	if name == "" {
		return unlandedWatermark{}, errNoWatermark
	}
	data, err := os.ReadFile(filepath.Join(c.dir, name))
	if err != nil {
		return unlandedWatermark{}, errNoWatermark
	}
	tip := strings.TrimSpace(string(data))
	if !oidRe.MatchString(tip) {
		return unlandedWatermark{}, errNoWatermark
	}
	base := unescapeBaseName(strings.TrimPrefix(name, watermarkPrefix))
	if base == "" {
		return unlandedWatermark{}, errNoWatermark
	}
	return unlandedWatermark{base: base, tip: tip}, nil
}

// writeWatermark records base's tip as verified and removes any watermark for a
// different base name, so the ambiguity readWatermark refuses cannot persist.
//
// Only the job calls this. Readers never advance it, and a compute-on-miss
// caller does not either: the watermark is the output of the validity protocol,
// and a caller that has not run that protocol has not earned the right to assert
// it. The cost is that a fresh clone's cache is unreadable until the job's first
// pass, which is one interval.
func (c unlandedCache) writeWatermark(base, tip string) error {
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return err
	}
	want := watermarkPrefix + escapeBaseName(base)
	if err := writeFileAtomic(filepath.Join(c.dir, want), []byte(tip+"\n")); err != nil {
		return err
	}
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), watermarkPrefix) || e.Name() == want {
			continue
		}
		_ = os.Remove(filepath.Join(c.dir, e.Name()))
	}
	return nil
}

// readEntry answers the cache for one branch tip measured against baseTip.
//
// settled/ is tested FIRST, because it is the answer that survives base
// movement and so is the one more likely to be right.
//
// An unsettled entry with no lines is treated as a MISS, not as "settled". This
// writer never produces one — zero unlanded commits are recorded as a settled
// marker instead — so an empty file can only be a truncated write, and reading
// it as the all-clear is the one mistake this cache must not make.
func (c unlandedCache) readEntry(baseTip, head string) ([]string, bool) {
	if _, err := os.Stat(c.settledPath(head)); err == nil {
		return nil, true
	}
	data, err := os.ReadFile(c.unsettledPath(baseTip, head))
	if err != nil {
		return nil, false
	}
	var lines []string
	for _, ln := range strings.Split(string(data), "\n") {
		if ln = strings.TrimSpace(ln); ln != "" {
			lines = append(lines, ln)
		}
	}
	if len(lines) == 0 {
		return nil, false
	}
	return lines, true
}

// writeEntry stores one verdict. An empty commits slice becomes the settled
// marker; anything else becomes the unsettled entry under baseTip.
//
// The lines are stored UNBOUNDED, unlike the UnlandedLog every display renders.
// The count must stay exact — `task unsettled` says how many more there are than
// it printed — and with no file format the only place a count can live is the
// line count, so truncating here would silently cap it at unlandedLogLimit for
// exactly the worktrees that have drifted furthest. The reader applies the
// display cap itself, identically to the computing path.
func (c unlandedCache) writeEntry(baseTip, head string, commits []string) error {
	if !oidRe.MatchString(head) || !oidRe.MatchString(baseTip) {
		return fmt.Errorf("unlanded cache: refusing to key on %q/%q", baseTip, head)
	}
	if len(commits) == 0 {
		dir := filepath.Join(c.dir, settledSubdir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		// Create-or-nothing: an empty marker has no partial state to observe, so
		// it needs no temp-then-rename and two writers racing cannot disagree.
		f, err := os.OpenFile(filepath.Join(dir, head), os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		return f.Close()
	}
	dir := filepath.Join(c.dir, unsettledSubdir, baseTip)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, head), []byte(strings.Join(commits, "\n")+"\n"))
}

// flushSettled removes every settled marker. Called when the watermark says the
// base branch's history was REWRITTEN rather than appended to, which is the one
// event that can turn a settled verdict false.
func (c unlandedCache) flushSettled() error {
	err := os.RemoveAll(filepath.Join(c.dir, settledSubdir))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// prune drops what no longer has a question behind it: settled markers for an
// OID no current worktree HEAD points at, and every unsettled directory keyed on
// a base tip that is no longer current.
//
// Best-effort by design — a cache that cannot be pruned is a cache that wastes
// disk, not one that lies — so a failure to remove one entry does not abort the
// rest.
func (c unlandedCache) prune(liveHeads map[string]bool, baseTip string) {
	if entries, err := os.ReadDir(filepath.Join(c.dir, settledSubdir)); err == nil {
		for _, e := range entries {
			if e.IsDir() || liveHeads[e.Name()] {
				continue
			}
			_ = os.Remove(filepath.Join(c.dir, settledSubdir, e.Name()))
		}
	}
	if entries, err := os.ReadDir(filepath.Join(c.dir, unsettledSubdir)); err == nil {
		for _, e := range entries {
			if !e.IsDir() || e.Name() == baseTip {
				continue
			}
			_ = os.RemoveAll(filepath.Join(c.dir, unsettledSubdir, e.Name()))
		}
	}
}

// cachedUnlanded is the READ-ONLY mode, and the whole reason this task exists.
// It computes nothing: a miss answers Known=false so the caller can render "not
// yet determined" instead of a verdict derived from something else.
//
// It is what AnnotateSessionStatusUnsettled — the ◆ column in `session status`
// and `session monitor` — is allowed to call. Its git cost is one memoized
// common-dir lookup and one `git rev-parse HEAD`, against the 584ms range-diff
// it replaces.
func cachedUnlanded(ctx context.Context, worktreeDir string) unlandedLookup {
	var res unlandedLookup

	c, err := unlandedCacheFor(ctx, worktreeDir)
	if err != nil {
		return res
	}
	wm, err := c.readWatermark()
	if err != nil {
		return res
	}
	res.Base = wm.base
	head, err := worktreeHead(ctx, worktreeDir)
	if err != nil {
		// A miss, not a fault. The only caller is the display path, which ran
		// `git status --porcelain` in this same directory first — so a git that
		// cannot answer here has already been reported by that probe, and
		// recording it twice would just double the incident.
		return res
	}
	res.Commits, res.Known = c.readEntry(wm.tip, head)
	return res
}

// computeUnlandedAndCache is the COMPUTE-ON-MISS mode: it answers from the
// cache when the cache has an answer, and otherwise runs the exact comparison
// and stores what it found so the next reader gets it free.
//
// Three callers, each for its own reason: `session-query worktree-unsettled`
// (the Go entry point behind `task unsettled`), because a direct question
// deserves a real answer; the reaper's condition 4, because a guess is not
// acceptable before a delete; and the background job, because it is the writer.
//
// READING FIRST IS NOT AN OPTIMIZATION HERE, IT IS THE CONTRACT. E-2128 shipped
// this function computing unconditionally, which was invisible on the two paths
// that consult the cache themselves before calling — but the reaper calls it
// directly, so every sweep paid the full ~584ms comparison per eligible worktree
// with a fully warm cache sitting beside it. `task spawn` runs the reaper, and a
// spawn took about a minute. That is E-2087's regression verbatim, which is the
// one this whole task exists to make affordable; a function named
// compute-on-MISS must therefore miss before it computes.
//
// The cache read keys on the watermark's base tip rather than on the caller's
// freshly resolved base, and that is safe in both directions: a `settled` hit is
// permanent while the branch tip has not moved, and a stale `unsettled` hit can
// only over-report, which makes the reaper skip — the safe direction for a
// decision that ends in a removed directory.
//
// The caller supplies base — every one of them has already resolved it, and
// taking it as a parameter keeps this function from quietly becoming a second
// resolver with its own failure mode.
func computeUnlandedAndCache(ctx context.Context, worktreeDir, base string) ([]string, error) {
	if hit := cachedUnlanded(ctx, worktreeDir); hit.Known {
		return hit.Commits, nil
	}
	commits, err := unlandedCommits(ctx, worktreeDir, base)
	if err != nil {
		// A failed compute writes NOTHING. Absence already means unknown, and
		// unknown already fails closed, so there is no error state to store.
		return nil, err
	}
	storeUnlanded(ctx, worktreeDir, base, commits)
	return commits, nil
}

// storeUnlanded writes one computed verdict into the cache. Best-effort: the
// answer has already been established, so a cache that cannot hold it costs
// speed and nothing else.
func storeUnlanded(ctx context.Context, worktreeDir, base string, commits []string) {
	c, err := unlandedCacheFor(ctx, worktreeDir)
	if err != nil {
		return
	}
	head, err := worktreeHead(ctx, worktreeDir)
	if err != nil {
		return
	}
	baseTip, err := revOID(ctx, worktreeDir, base)
	if err != nil {
		return
	}
	if err := c.writeEntry(baseTip, head, commits); err != nil {
		recordUnlandedCacheFault(c.dir, err)
	}
}

// recordUnlandedCacheFault reports a cache that cannot be written — a read-only
// checkout, a permissions problem, a full disk.
//
// Fingerprinted on the DIRECTORY, never per worktree: one unwritable directory
// is one condition with one remedy, and a pass over 135 worktrees must not raise
// 135 incidents about it. This mirrors recordDefaultBranchFault's treatment of a
// resolver that will never succeed.
func recordUnlandedCacheFault(dir string, err error) {
	if errors.Is(err, ErrGitInterrupted) {
		return
	}
	faults.Record(faults.Fault{
		Code:        faults.ErrCodeUnlandedCacheUnwritable,
		Source:      "worktree:unlanded-cache",
		Fingerprint: dir,
		Summary:     "the unlanded-verdict cache cannot be written, so every worktree reads as not yet determined",
		Detail:      err.Error(),
		Fields: map[string]any{
			"cache_dir": dir,
			"error":     err.Error(),
		},
	})
}

// escapeBaseName makes a branch name usable as one path component. A branch may
// contain `/` — `release/2.0` is an ordinary default branch on somebody's
// repository — and the watermark carries the name in its FILENAME so that
// editing `default_branch` orphans the old record automatically. Percent-escaping
// keeps both properties: every distinct branch name still maps to a distinct
// filename, and no name can nest a directory or escape the cache.
func escapeBaseName(name string) string {
	var b strings.Builder
	for i := 0; i < len(name); i++ {
		ch := name[i]
		switch {
		case ch >= 'A' && ch <= 'Z', ch >= 'a' && ch <= 'z',
			ch >= '0' && ch <= '9', ch == '.', ch == '_', ch == '-':
			b.WriteByte(ch)
		default:
			fmt.Fprintf(&b, "%%%02X", ch)
		}
	}
	return b.String()
}

// unescapeBaseName reverses escapeBaseName, returning "" for input it cannot
// decode — which readWatermark treats as no watermark at all.
func unescapeBaseName(escaped string) string {
	var b strings.Builder
	for i := 0; i < len(escaped); i++ {
		if escaped[i] != '%' {
			b.WriteByte(escaped[i])
			continue
		}
		if i+2 >= len(escaped) {
			return ""
		}
		var v int
		if _, err := fmt.Sscanf(escaped[i+1:i+3], "%02X", &v); err != nil {
			return ""
		}
		b.WriteByte(byte(v))
		i += 2
	}
	return b.String()
}

// writeFileAtomic writes data to path via a temp file in the SAME directory and
// a rename, so a reader never observes a partial entry and two writers racing
// leave one complete file rather than an interleaving.
func writeFileAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err = f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err = f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err = os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

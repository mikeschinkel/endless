package monitor

import (
	"context"
	"sync"
)

// One `git status` per worktree per frame (E-2228).
//
// A `session status` frame asks the focal worktree for its status twice: once
// for the row's ◆ verdict (worktreeUnsettledAt) and once for the focal anomaly
// lines under it (worktreeAnomaliesAt). In an Endless worktree a status costs
// 40–80ms of lstat, so the second ask was the single most expensive thing a
// frame repeated. Both callers now read through gitStatusPorcelain, and a
// caller that wants the frame's asks shared carries a memo on its context.
//
// The memo lives on the context, not in a package variable, so it lasts exactly
// as long as the frame that made it: the next frame starts empty and sees the
// worktree as it is now. A context without a memo — every one-shot surface —
// runs git every time, as before.

type frameMemoKey struct{}

type frameMemo struct {
	mu     sync.Mutex
	status map[string]gitResult
}

type gitResult struct {
	out string
	err error
}

// WithFrameMemo returns a context whose git status reads are shared, per
// worktree, by everything that renders one frame under it.
func WithFrameMemo(ctx context.Context) context.Context {
	return context.WithValue(ctx, frameMemoKey{}, &frameMemo{status: map[string]gitResult{}})
}

// gitStatusPorcelain is `git status --porcelain` on dir, answered from the
// context's frame memo when it carries one.
func gitStatusPorcelain(ctx context.Context, dir string) (string, error) {
	memo, _ := ctx.Value(frameMemoKey{}).(*frameMemo)
	if memo == nil {
		return runGit(ctx, dir, "status", "--porcelain")
	}
	memo.mu.Lock()
	defer memo.mu.Unlock()
	if r, ok := memo.status[dir]; ok {
		return r.out, r.err
	}
	out, err := runGit(ctx, dir, "status", "--porcelain")
	memo.status[dir] = gitResult{out: out, err: err}
	return out, err
}

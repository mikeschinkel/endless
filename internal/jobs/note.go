package jobs

import (
	"context"
	"sync"
)

// A note is a job's one-line account of what its run did, for `jobs list` to
// show (E-1814). It exists because a run can end SUCCESSFULLY without doing its
// work — "nothing was eligible", "no tmux client is attached" — and neither
// last_error (a skip is not a failure) nor silence tells an operator which of
// those happened.
//
// The Job interface is unchanged: a job that has something to say calls Note on
// the context it was handed, and every other job is untouched. The runner
// writes the note into jobs.last_note when the run completes, replacing the
// previous one, so the column always describes the LAST run and never a stale
// earlier one.

type noteKey struct{}

// noteBox carries a run's note from the job back to the runner. Guarded because
// nothing stops a job from calling Note from a goroutine of its own.
type noteBox struct {
	mu   sync.Mutex
	text string
}

// withNote returns ctx carrying a fresh note box, and the box.
func withNote(ctx context.Context) (context.Context, *noteBox) {
	box := &noteBox{}
	return context.WithValue(ctx, noteKey{}, box), box
}

// Note records why this run did what it did. The last call wins. Calling it
// outside a runner-started run is a no-op, so a job's logic can be exercised
// directly in a test without a runner behind it.
func Note(ctx context.Context, text string) {
	box, ok := ctx.Value(noteKey{}).(*noteBox)
	if !ok {
		return
	}
	box.mu.Lock()
	box.text = text
	box.mu.Unlock()
}

// get returns the recorded note, or "" when the job recorded none.
func (b *noteBox) get() (text string) {
	if b == nil {
		return ""
	}
	b.mu.Lock()
	text = b.text
	b.mu.Unlock()
	return text
}

package mainsyncjob

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/events"
)

// ledgerCommit appends a line to a db-ledger segment on main and commits it the
// way every event emit does, through the real auto-commit: a new commit when
// HEAD is anything else, an amend when HEAD is an unpublished ledger commit.
func ledgerCommit(t *testing.T, f fixture, line string) string {
	t.Helper()
	rel := ".endless/db-ledger/0001.jsonl"
	abs := filepath.Join(f.main, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	fh, err := os.OpenFile(abs, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fh.WriteString(line + "\n"); err != nil {
		t.Fatal(err)
	}
	if err = fh.Close(); err != nil {
		t.Fatal(err)
	}
	if err = events.CommitLedgerSegment(f.main, rel); err != nil {
		t.Fatalf("commit ledger segment: %v", err)
	}
	return git(t, f.main, "rev-parse", "HEAD")
}

// inFlightAmend is the second half of an auto-commit that decided to amend
// before main-sync's push and commits after it (E-2273): canAmend has already
// said yes, so nothing the push did is consulted again.
func inFlightAmend(t *testing.T, f fixture, line string) {
	t.Helper()
	rel := ".endless/db-ledger/0001.jsonl"
	fh, err := os.OpenFile(filepath.Join(f.main, rel), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fh.WriteString(line + "\n"); err != nil {
		t.Fatal(err)
	}
	if err = fh.Close(); err != nil {
		t.Fatal(err)
	}
	git(t, f.main, "commit", "-q", "-o", rel, "--amend", "--no-edit")
}

// assertOriginBeneathMain fails when origin holds a commit main no longer has:
// the divergence ERR-0030 reports, which only a hand merge clears.
func assertOriginBeneathMain(t *testing.T, f fixture) {
	t.Helper()
	tip := f.originTip(t)
	cmd := []string{"merge-base", "--is-ancestor", tip, "refs/heads/main"}
	if _, err := systemGit(context.Background(), f.main, cmd...); err != nil {
		t.Errorf("origin's %s is not on main: an amend rewrote a commit main-sync pushed\n%s",
			tip[:9], git(t, f.main, "log", "--oneline", "--graph", "--all"))
	}
}

// TestSync_AmendableTipStaysLocal reproduces ERR-0030 as main's reflog showed
// it: main's tip is a ledger commit the auto-commit may still amend, main-sync
// pushes it, and an amend already past canAmend then rewrites it.
func TestSync_AmendableTipStaysLocal(t *testing.T) {
	f := newFixture(t)
	below := commit(t, f.main, "work.txt")
	ledgerCommit(t, f, `{"n":1}`)

	res, err := sync1(t, f, systemGit)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if f.originTip(t) != below {
		t.Errorf("origin = %s, want the commit beneath the ledger tip, %s", f.originTip(t), below)
	}
	if res.Action != Pushed || res.Ahead != 2 {
		t.Errorf("result = %+v, want pushed with 2 ahead", res)
	}

	inFlightAmend(t, f, `{"n":2}`)
	assertOriginBeneathMain(t, f)
}

// TestSync_PushesTheTipItInspected: the ledger auto-commit lands a new commit on
// main between main-sync reading the tip and its push. A push of the branch by
// name would publish that commit too, and its next amend would diverge main.
func TestSync_PushesTheTipItInspected(t *testing.T) {
	f := newFixture(t)
	inspected := commit(t, f.main, "work.txt")

	racing := func(ctx context.Context, dir string, args ...string) (string, error) {
		if len(args) > 0 && args[0] == "push" {
			ledgerCommit(t, f, `{"n":1}`)
			defer inFlightAmend(t, f, `{"n":2}`)
		}
		return systemGit(ctx, dir, args...)
	}
	if _, err := sync1(t, f, racing); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if f.originTip(t) != inspected {
		t.Errorf("origin = %s, want the tip main-sync inspected, %s", f.originTip(t), inspected)
	}
	assertOriginBeneathMain(t, f)
}

// TestSync_OnlyAnAmendableTipIsHeldBack: one ledger commit with nothing
// unpushed beneath it leaves nothing to push, and that is in sync, not a push of
// zero commits; once a commit lands on top of it, all of it goes.
func TestSync_OnlyAnAmendableTipIsHeldBack(t *testing.T) {
	f := newFixture(t)
	seed := f.originTip(t)
	ledgerCommit(t, f, `{"n":1}`)

	res, err := sync1(t, f, systemGit)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if res.Action != HeldBack || f.originTip(t) != seed {
		t.Errorf("result = %+v, origin = %s; want held back with origin still at %s",
			res, f.originTip(t), seed)
	}
	if !strings.Contains(res.String(), "Endless: record ledger entry") {
		t.Errorf("note %q does not say what was held back", res.String())
	}

	tip := commit(t, f.main, "work.txt")
	if res, err = sync1(t, f, systemGit); err != nil || res.Action != Pushed {
		t.Fatalf("second run = %+v, %v; want pushed", res, err)
	}
	if f.originTip(t) != tip {
		t.Errorf("origin = %s, want main's %s", f.originTip(t), tip)
	}
}

package monitor

import (
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/schema"
)

func claimTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err = db.Exec(schema.SQL); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	return db
}

// The whole point: two claimants, one task, exactly one winner. Without this
// the inline path and a sweep both pay for the same model call.
func TestClaimTriage_SecondClaimantLoses(t *testing.T) {
	db := claimTestDB(t)

	first, err := claimTriage(db, 42, "owner-a", time.Minute)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if !first {
		t.Fatal("first claimant did not win an unclaimed task")
	}

	second, err := claimTriage(db, 42, "owner-b", time.Minute)
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if second {
		t.Error("second claimant won a live claim; duplicate model spend")
	}
}

// A claimant that dies mid-call must not wedge the task forever — that is why
// the claim is time-boxed rather than an OS lock.
func TestClaimTriage_LapsedClaimIsReclaimable(t *testing.T) {
	db := claimTestDB(t)

	// Zero TTL expires at the same second it is taken, so the next claim sees
	// expires_at <= now and takes it over.
	if _, err := claimTriage(db, 7, "dead-owner", 0); err != nil {
		t.Fatalf("seed lapsed claim: %v", err)
	}
	got, err := claimTriage(db, 7, "live-owner", time.Minute)
	if err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	if !got {
		t.Error("a lapsed claim was not reclaimable; a dead claimant would wedge the task")
	}
}

func TestReleaseTriage_FreesTheTask(t *testing.T) {
	db := claimTestDB(t)
	if _, err := claimTriage(db, 9, "owner-a", time.Minute); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := releaseTriage(db, 9, "owner-a"); err != nil {
		t.Fatalf("release: %v", err)
	}
	got, err := claimTriage(db, 9, "owner-b", time.Minute)
	if err != nil {
		t.Fatalf("re-claim after release: %v", err)
	}
	if !got {
		t.Error("release did not free the task")
	}
}

// Releasing a claim that already lapsed and was retaken must not steal it.
func TestReleaseTriage_DoesNotStealAnotherOwnersClaim(t *testing.T) {
	db := claimTestDB(t)
	if _, err := claimTriage(db, 11, "owner-a", 0); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := claimTriage(db, 11, "owner-b", time.Minute); err != nil {
		t.Fatalf("takeover: %v", err)
	}
	// owner-a, finishing late, releases what it thinks is its claim.
	if err := releaseTriage(db, 11, "owner-a"); err != nil {
		t.Fatalf("stale release: %v", err)
	}
	stillHeld, err := claimTriage(db, 11, "owner-c", time.Minute)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if stillHeld {
		t.Error("a stale release freed the live owner's claim")
	}
}

func TestTriageClaimOwner_IsStableWithinAProcess(t *testing.T) {
	if TriageClaimOwner() != TriageClaimOwner() {
		t.Error("owner identity must be stable so release matches claim")
	}
	if TriageClaimOwner() == "" {
		t.Error("owner identity is empty")
	}
}

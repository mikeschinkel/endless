package events

import "testing"

// TestMayAmend: every subject commitPaths is given carries the auto-commit
// prefix, so that prefix is exactly what main-sync must hold back (E-2273).
func TestMayAmend(t *testing.T) {
	for subject, want := range map[string]bool{
		LedgerCommitSubject:                     true,
		"Endless: add context for E-2273":       true,
		VerifyReportSubject("E-1", "r.json"):    true,
		"Endless: reconcile document mirrors":   true,
		"E-2273: hold back an amendable tip":    false,
		"Merge branch 'task/2273'":              false,
		"endless: lower-case is someone else's": false,
	} {
		if got := MayAmend(subject); got != want {
			t.Errorf("MayAmend(%q) = %v, want %v", subject, got, want)
		}
	}
}

package questionstatus_test

import (
	"slices"
	"testing"

	"github.com/mikeschinkel/endless/internal/questionstatus"
)

func TestCanMove_TheWholeLifecycle(t *testing.T) {
	legal := map[[2]string]bool{
		{"open", "answered"}:       true,
		{"open", "withdrawn"}:      true,
		{"open", "invalid"}:        true,
		{"open", "superseded"}:     true,
		{"answered", "superseded"}: true,
	}
	for _, from := range questionstatus.All {
		for _, to := range questionstatus.All {
			want := legal[[2]string{from, to}]
			if got := questionstatus.CanMove(from, to); got != want {
				t.Errorf("CanMove(%s, %s) = %v, want %v", from, to, got, want)
			}
		}
	}
}

func TestFrom(t *testing.T) {
	if got := questionstatus.From("superseded"); !slices.Equal(got, []string{"open", "answered"}) {
		t.Errorf("From(superseded) = %v", got)
	}
	if got := questionstatus.From("open"); len(got) != 0 {
		t.Errorf("From(open) = %v, want nothing — no status leads back to open", got)
	}
}

func TestValidate(t *testing.T) {
	for _, s := range questionstatus.All {
		if err := questionstatus.Validate(s); err != nil {
			t.Errorf("Validate(%q): %v", s, err)
		}
	}
	for _, s := range []string{"", "Open", "closed", "rejected"} {
		if questionstatus.Validate(s) == nil {
			t.Errorf("Validate(%q) accepted", s)
		}
	}
}

func TestValidateAnsweredBy(t *testing.T) {
	for _, s := range []string{"user", "ES-1", "ES-1236"} {
		if err := questionstatus.ValidateAnsweredBy(s); err != nil {
			t.Errorf("ValidateAnsweredBy(%q): %v", s, err)
		}
	}
	for _, s := range []string{"", "User", "1236", "ES-", "ES-0", "es-12", "E-12", "ES-12x"} {
		if questionstatus.ValidateAnsweredBy(s) == nil {
			t.Errorf("ValidateAnsweredBy(%q) accepted", s)
		}
	}
}

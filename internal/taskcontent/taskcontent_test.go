package taskcontent_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/taskcontent"
)

func TestParse_RoundTripsEverySlug(t *testing.T) {
	for _, n := range taskcontent.All() {
		got, err := taskcontent.Parse(n.Slug())
		if err != nil {
			t.Errorf("Parse(%q): %v", n.Slug(), err)
			continue
		}
		if got != n {
			t.Errorf("Parse(%q) = %v, want %v", n.Slug(), got, n)
		}
	}
}

func TestParse_RejectsUnknown(t *testing.T) {
	for _, s := range []string{"", "Plan", "text", "description", "justification"} {
		if _, err := taskcontent.Parse(s); err == nil {
			t.Errorf("Parse(%q) accepted an unknown name", s)
		}
	}
}

// The convention, asserted: one lowercase single-token name per kind, because
// the same token is the column value, the CLI flag and the mirror file stem.
func TestSlugs_FollowTheNamingConvention(t *testing.T) {
	token := regexp.MustCompile(`^[a-z]+$`)
	seen := map[string]bool{}
	for _, s := range taskcontent.Slugs() {
		if !token.MatchString(s) {
			t.Errorf("slug %q is not one lowercase token", s)
		}
		if seen[s] {
			t.Errorf("slug %q is declared twice", s)
		}
		seen[s] = true
	}
}

// String is the heading, and it is derived from the name rather than chosen
// per call site: the label is the slug, capitalized.
func TestString_IsTheCapitalizedSlug(t *testing.T) {
	for _, n := range taskcontent.All() {
		want := strings.ToUpper(n.Slug()[:1]) + n.Slug()[1:]
		if n.String() != want {
			t.Errorf("%s.String() = %q, want %q", n.Slug(), n.String(), want)
		}
	}
}

// Display order is part of the contract: `task show` renders sections in it.
func TestAll_IsDisplayOrder(t *testing.T) {
	got := strings.Join(taskcontent.Slugs(), ",")
	if want := "context,analysis,plan,outcome,reason,notes"; got != want {
		t.Errorf("Slugs() = %s, want %s", got, want)
	}
}

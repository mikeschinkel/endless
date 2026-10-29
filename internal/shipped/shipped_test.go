package shipped

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func rules(t *testing.T) (File, string) {
	t.Helper()
	f, ok := Lookup(TasksRules)
	if !ok {
		t.Fatal("tasks-rules is not registered")
	}
	b, err := f.Content()
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return f, string(b)
}

// Every kind's current rendering must be listed, or the version this Endless
// writes would read as a local edit to the next one and never be updated again.
func TestEveryCurrentVersionIsListed(t *testing.T) {
	for _, k := range Kinds() {
		f, _ := Lookup(k)
		b, err := f.Content()
		if err != nil {
			t.Fatalf("%s: render: %v", k, err)
		}
		if !f.IsShipped(Hash(b)) {
			t.Errorf("%s: the current rendering (sha256 %s) is not in versions.go — append it", k, Hash(b))
		}
	}
}

func TestVersionsAreUniqueAndBelongToAKind(t *testing.T) {
	for k, hashes := range versions {
		if _, ok := Lookup(k); !ok {
			t.Errorf("versions lists %q, which is not a shipped kind", k)
		}
		seen := map[string]bool{}
		for _, h := range hashes {
			if len(h) != 64 {
				t.Errorf("%s: %q is not a hex SHA-256", k, h)
			}
			if seen[h] {
				t.Errorf("%s: %s is listed twice", k, h)
			}
			seen[h] = true
		}
	}
}

func TestDecide(t *testing.T) {
	f, cur := rules(t)
	current := Hash([]byte(cur))
	older := versions[TasksRules][0]
	oldText := []byte("an older text")
	edited := []byte("# Our own rules\n")

	// Pretend oldText is a shipped version, so the no-record rule has one.
	versions[TasksRules] = append(versions[TasksRules], Hash(oldText))
	t.Cleanup(func() { versions[TasksRules] = versions[TasksRules][:len(versions[TasksRules])-1] })

	cases := []struct {
		name     string
		existing []byte
		recorded string
		want     Action
	}{
		{"no copy", nil, "", Created},
		{"no copy, but a record", nil, older, Created},
		{"already current", []byte(cur), "", Current},
		{"what init last wrote", oldText, Hash(oldText), Updated},
		{"edited since init wrote it", edited, Hash(oldText), Edited},
		{"a shipped version, but not the one init recorded", oldText, older, Edited},
		{"no record, a shipped version", oldText, "", Updated},
		{"no record, not a shipped version", edited, "", Edited},
	}
	for _, c := range cases {
		if got := f.Decide(c.existing, c.recorded, current); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

// Endless's own copy is the shipped text, not a hand-maintained twin. Two copies
// of a rules file drift, and the one that drifts is the one nobody reads.
func TestEndlessShipsTheSameFileItHolds(t *testing.T) {
	f, cur := rules(t)
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	own, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(f.Path)))
	if err != nil {
		t.Fatalf("read the repo's own copy: %v", err)
	}
	if string(own) != cur {
		t.Errorf("%s differs from the shipped template", f.Path)
	}
}

// The runner's refusal and the harness both send a reader here by path. If the
// file stops covering what they promise, the refusal becomes a dead link.
func TestTheRulesNameWhatTheRefusalsPointAt(t *testing.T) {
	_, cur := rules(t)
	for _, phrase := range []string{
		"endless task verify",
		"_harness.sh",
		"land-time gate",
		"Do not run another task's suite",
		"Do not edit a landed task's suite",
	} {
		if !strings.Contains(cur, phrase) {
			t.Errorf("the rules no longer mention %q", phrase)
		}
	}
}

func section(t *testing.T, text, heading string) string {
	t.Helper()
	start := strings.Index(text, "## "+heading+"\n")
	if start < 0 {
		t.Fatalf("no section %q", heading)
	}
	end := strings.Index(text[start+1:], "\n## ")
	if end < 0 {
		return text[start:]
	}
	return text[start : start+1+end]
}

// E-2419: the rules ship to every project, and only the endless repo has
// `_harness.sh` and `_guard.sh`. So the opening lines, the file table and
// "Writing one" name no harness, guard or script template.
func TestTheRulesTeachTheManifestFormOnly(t *testing.T) {
	_, cur := rules(t)
	opening := cur[:strings.Index(cur, "\n## ")]
	fileTable := section(t, cur, "Three kinds of file, and only one of them is yours")
	writing := section(t, cur, "Writing one")
	scriptOnly := []string{"_guard.sh", "#!/usr/bin/env bash", `source "$(dirname`, "assert_eq",
		"as_agent", "with_tmux"}

	for name, text := range map[string]string{"opening lines": opening, "file table": fileTable} {
		for _, word := range append([]string{"_harness.sh", "verify.sh"}, scriptOnly...) {
			if strings.Contains(text, word) {
				t.Errorf("the %s name %q", name, word)
			}
		}
	}
	for _, word := range scriptOnly {
		if strings.Contains(writing, word) {
			t.Errorf("'Writing one' names %q", word)
		}
	}
	for _, want := range []string{"verify.toml", "[[check]]", "sh ", "contains", "not_contains",
		"equals", "name", "Native tests first"} {
		if !strings.Contains(writing, want) {
			t.Errorf("'Writing one' does not teach %q", want)
		}
	}
}

func TestOneLineSendsAScriptSuiteReaderToTheHarnessHeader(t *testing.T) {
	_, cur := rules(t)
	var lines []string
	for _, l := range strings.Split(cur, "\n") {
		if strings.Contains(l, "_harness.sh") {
			lines = append(lines, l)
		}
	}
	if len(lines) != 1 {
		t.Fatalf("_harness.sh is named on %d lines: %q", len(lines), lines)
	}
	var sends []string
	for _, p := range strings.Split(section(t, cur, "Writing one"), "\n\n") {
		if strings.Contains(p, "_harness.sh") {
			sends = append(sends, p)
		}
	}
	if len(sends) == 0 || !strings.Contains(sends[0], "header") || !strings.Contains(sends[0], "verify.sh") {
		t.Errorf("the _harness.sh paragraph does not send a reader to its header: %q", sends)
	}
}

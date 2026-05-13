package events

import (
	"strings"
	"testing"
)

func TestRenderSessionStatusMarkdown_Headline(t *testing.T) {
	p := &SessionStatusRecordedPayload{Headline: "E-1312 v1 landed."}
	md := renderSessionStatusMarkdown(p)
	if !strings.Contains(md, "## Status\nE-1312 v1 landed.") {
		t.Fatalf("missing Status section: %q", md)
	}
}

func TestRenderSessionStatusMarkdown_EmptySectionsShowEmpty(t *testing.T) {
	p := &SessionStatusRecordedPayload{}
	md := renderSessionStatusMarkdown(p)
	for _, want := range []string{
		"## Resolved\n(empty)",
		"## Pending\n(empty)",
		"## Blocked\n(empty)",
		"## Verify\n(empty)",
		"## Decisions\n(empty)",
		"## Commits\n(empty)",
		"## Memory\n(empty)",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("missing section/empty marker %q in:\n%s", want, md)
		}
	}
}

func TestRenderSessionStatusMarkdown_TaskTable(t *testing.T) {
	p := &SessionStatusRecordedPayload{
		Tasks:`<task id="E-1208" status="confirmed">verbs.jsonl write-time</task>` +
			"\n" +
			`<task id="E-1206" status="confirmed" filed="true">db-ledger write-time</task>`,
	}
	md := renderSessionStatusMarkdown(p)
	if !strings.Contains(md, "| Task | Status | Note |") {
		t.Fatalf("missing table header: %q", md)
	}
	if !strings.Contains(md, "| E-1208 | confirmed | verbs.jsonl write-time |") {
		t.Errorf("missing first task row: %q", md)
	}
	if !strings.Contains(md, "| E-1206 (filed) | confirmed | db-ledger write-time |") {
		t.Errorf("missing filed-marked second task row: %q", md)
	}
}

func TestRenderSessionStatusMarkdown_MultiLineNote(t *testing.T) {
	// Multi-line task body: newlines should render as <br> in the markdown
	// table cell.
	p := &SessionStatusRecordedPayload{
		Tasks:`<task id="E-1" status="confirmed">line one
line two</task>`,
	}
	md := renderSessionStatusMarkdown(p)
	if !strings.Contains(md, "line one<br>line two") {
		t.Fatalf("expected <br> between lines in cell: %q", md)
	}
}

func TestRenderSessionStatusMarkdown_DecisionsBulleted(t *testing.T) {
	p := &SessionStatusRecordedPayload{
		Decisions: `<decision>chose XML over markdown</decision>` +
			"\n" +
			`<decision>kept filed as attribute</decision>`,
	}
	md := renderSessionStatusMarkdown(p)
	if !strings.Contains(md, "- chose XML over markdown") {
		t.Errorf("missing first bullet: %q", md)
	}
	if !strings.Contains(md, "- kept filed as attribute") {
		t.Errorf("missing second bullet: %q", md)
	}
}

func TestRenderSessionStatusMarkdown_CommitsTable(t *testing.T) {
	p := &SessionStatusRecordedPayload{
		Commits: `<commit sha="1e3bbfc">ledger split 1264 to 500/500/264</commit>`,
	}
	md := renderSessionStatusMarkdown(p)
	if !strings.Contains(md, "| SHA | Description |") {
		t.Errorf("missing commits table header: %q", md)
	}
	if !strings.Contains(md, "| 1e3bbfc | ledger split 1264 to 500/500/264 |") {
		t.Errorf("missing commit row: %q", md)
	}
}

func TestRenderSessionStatusMarkdown_MemoryTable(t *testing.T) {
	p := &SessionStatusRecordedPayload{
		Memory: `<entry path="feedback_no_autonomous_remediation.md">report and ask on partial fail</entry>`,
	}
	md := renderSessionStatusMarkdown(p)
	if !strings.Contains(md, "| Path | Summary |") {
		t.Errorf("missing memory table header: %q", md)
	}
	if !strings.Contains(md, "| feedback_no_autonomous_remediation.md | report and ask on partial fail |") {
		t.Errorf("missing memory row: %q", md)
	}
}

// --- Helpers --------------------------------------------------------------

func TestExtractAttr(t *testing.T) {
	cases := []struct {
		line, attr, want string
	}{
		{`<task id="E-1" status="confirmed">x</task>`, "id", "E-1"},
		{`<task id="E-1" status="confirmed">x</task>`, "status", "confirmed"},
		{`<task id="E-1" status="confirmed">x</task>`, "filed", ""},
		{`<commit sha="1e3bbfc">x</commit>`, "sha", "1e3bbfc"},
		{`<entry path="a/b/c.md">x</entry>`, "path", "a/b/c.md"},
	}
	for _, c := range cases {
		if got := extractAttr(c.line, c.attr); got != c.want {
			t.Errorf("extractAttr(%q, %q) = %q, want %q", c.line, c.attr, got, c.want)
		}
	}
}

func TestExtractElementText(t *testing.T) {
	cases := []struct {
		line, tag, want string
	}{
		{`<task id="E-1" status="confirmed">body text</task>`, "task", "body text"},
		{`<decision>chose XML</decision>`, "decision", "chose XML"},
		{`<commit sha="abc">desc</commit>`, "commit", "desc"},
		{`<task id="E-1" status="confirmed">multi
line</task>`, "task", "multi\nline"},
	}
	for _, c := range cases {
		if got := extractElementText(c.line, c.tag); got != c.want {
			t.Errorf("extractElementText(%q, %q) = %q, want %q",
				c.line, c.tag, got, c.want)
		}
	}
}

func TestNullableEq(t *testing.T) {
	empty := ""
	hello := "hello"
	cases := []struct {
		col     *string
		payload string
		want    bool
	}{
		{nil, "", true},         // both effectively empty
		{nil, "hello", false},   // null col vs non-empty payload
		{&empty, "", true},      // empty col vs empty payload
		{&hello, "hello", true}, // matching strings
		{&hello, "world", false},
	}
	for _, c := range cases {
		got := nullableEq(c.col, c.payload)
		if got != c.want {
			t.Errorf("nullableEq(%v, %q) = %v, want %v", c.col, c.payload, got, c.want)
		}
	}
}

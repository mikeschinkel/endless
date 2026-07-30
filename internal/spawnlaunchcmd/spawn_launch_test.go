package spawnlaunchcmd

import (
	"os"
	"reflect"
	"testing"
)

// TestBuildClaudeArgv_Default pins the minimal argv: binary, permission mode,
// and the positional prompt. No --model / --name when unset.
func TestBuildClaudeArgv_Default(t *testing.T) {
	spec := LaunchSpec{ClaudeBin: "/bin/claude", PermissionMode: "auto"}
	got := buildClaudeArgv(spec, "do the thing")
	want := []string{"/bin/claude", "--permission-mode", "auto", "do the thing"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %q, want %q", got, want)
	}
}

// TestBuildClaudeArgv_ModelAndName adds --model and --name before the prompt,
// in that order, when both are set.
func TestBuildClaudeArgv_ModelAndName(t *testing.T) {
	spec := LaunchSpec{
		ClaudeBin:      "/bin/claude",
		PermissionMode: "plan",
		Model:          "claude-opus-4-8",
		Name:           "E-1705",
	}
	got := buildClaudeArgv(spec, "prompt")
	want := []string{
		"/bin/claude", "--permission-mode", "plan",
		"--model", "claude-opus-4-8", "--name", "E-1705", "prompt",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %q, want %q", got, want)
	}
}

// TestBuildClaudeArgv_EmptyPromptOmitsPositional pins the bare-interactive case:
// an empty or whitespace-only handoff produces no positional argument, so claude
// opens interactively with nothing typed.
func TestBuildClaudeArgv_EmptyPromptOmitsPositional(t *testing.T) {
	spec := LaunchSpec{ClaudeBin: "/bin/claude", PermissionMode: "auto"}
	for _, prompt := range []string{"", "   ", "\n\t  \n"} {
		got := buildClaudeArgv(spec, prompt)
		want := []string{"/bin/claude", "--permission-mode", "auto"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("prompt %q: argv = %q, want %q (no positional)", prompt, got, want)
		}
	}
}

// TestBuildClaudeArgv_PromptBytesIntact confirms the prompt is passed verbatim
// as one argv element — quotes, backticks, `$`, and newlines are preserved,
// never re-interpreted (there is no shell between argv and claude).
func TestBuildClaudeArgv_PromptBytesIntact(t *testing.T) {
	prompt := "line one\n`backtick` \"quote\" $VAR 'apos'\n\nlast"
	spec := LaunchSpec{ClaudeBin: "/bin/claude", PermissionMode: "auto"}
	got := buildClaudeArgv(spec, prompt)
	if len(got) != 4 {
		t.Fatalf("argv len = %d, want 4: %q", len(got), got)
	}
	if got[3] != prompt {
		t.Fatalf("positional = %q, want verbatim %q", got[3], prompt)
	}
}

// TestSpecRoundTrip pins that a spec survives write→read unchanged, including
// special characters in fields.
func TestSpecRoundTrip(t *testing.T) {
	spec := LaunchSpec{
		ClaudeBin:      "/bin/claude",
		HandoffFile:    "/tmp/handoff.md",
		PermissionMode: "auto",
		Model:          "m",
		Name:           `endless_add "spawn" flag[E-1705]`,
		TaskID:         "1705",
		ProjectID:      "3",
		SpawnedBy:      "sess-abc",
		WindowName:     "endless_deliver[E-1705]",
		Cwd:            "/path/to/worktree",
	}
	path, err := writeSpecFile(spec)
	if err != nil {
		t.Fatalf("writeSpecFile: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })

	got, err := readSpecFile(path)
	if err != nil {
		t.Fatalf("readSpecFile: %v", err)
	}
	if !reflect.DeepEqual(got, spec) {
		t.Fatalf("round-trip spec = %+v, want %+v", got, spec)
	}
}

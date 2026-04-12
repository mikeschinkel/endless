package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/mikeschinkel/endless/internal/monitor"
)

type claudePayload struct {
	SessionID      string          `json:"session_id"`
	CWD            string          `json:"cwd"`
	EventName      string          `json:"hook_event_name"`
	ToolName       string          `json:"tool_name,omitempty"`
	ToolInput      json.RawMessage `json:"tool_input,omitempty"`
	TranscriptPath string          `json:"transcript_path,omitempty"`
}

type toolInputWrite struct {
	FilePath string `json:"file_path"`
	Content  string `json:"content"`
}

// hookResponse is the JSON we return on stdout for Claude to read.
type hookResponse struct {
	AdditionalContext string `json:"additionalContext,omitempty"`
}

func runClaude(args []string) error {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("reading stdin: %w", err)
	}

	var payload claudePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return fmt.Errorf("parsing payload: %w", err)
	}

	if payload.CWD == "" {
		return nil
	}

	projectID, _, err := monitor.ProjectIDForPath(payload.CWD)
	if err != nil {
		return nil
	}

	// Record activity (throttled)
	throttled, err := monitor.ShouldThrottle(projectID, "claude", 2)
	if err != nil {
		return err
	}
	if !throttled {
		sessionCtx := map[string]string{
			"session_id": payload.SessionID,
			"event":      payload.EventName,
		}
		if payload.ToolName != "" {
			sessionCtx["tool_name"] = payload.ToolName
		}
		_ = monitor.RecordActivity(projectID, "claude", payload.CWD, sessionCtx)
	}

	// Event-specific handling
	switch payload.EventName {
	case "SessionStart", "UserPromptSubmit":
		return handlePlanContextInjection(projectID, payload)

	case "PostToolUse":
		return handlePostToolUse(projectID, payload)

	case "Stop", "SessionEnd":
		changes, err := monitor.DetectFileChanges(projectID, payload.CWD)
		if err != nil {
			return err
		}
		if len(changes) > 0 {
			_ = monitor.RecordFileChanges(projectID, changes, "claude")
		}
	}

	return nil
}

func handlePlanContextInjection(projectID int64, payload claudePayload) error {
	// Only inject once per session
	if monitor.HasInjectedContext(payload.SessionID) {
		return nil
	}

	projectName, err := monitor.GetProjectName(projectID)
	if err != nil {
		return nil
	}

	items, err := monitor.GetActivePlanItems(projectID)
	if err != nil {
		return nil
	}

	context := monitor.FormatPlanContext(projectName, items)

	// Mark as injected so we don't repeat
	monitor.MarkContextInjected(projectID, payload.SessionID, payload.CWD)

	resp := hookResponse{
		AdditionalContext: context,
	}
	return json.NewEncoder(os.Stdout).Encode(resp)
}

func handlePostToolUse(projectID int64, payload claudePayload) error {
	// Detect file changes
	changes, err := monitor.DetectFileChanges(projectID, payload.CWD)
	if err == nil && len(changes) > 0 {
		_ = monitor.RecordFileChanges(projectID, changes, "claude")
	}

	// Check if a plan file was written
	if payload.ToolName != "Write" {
		return nil
	}

	var input toolInputWrite
	if err := json.Unmarshal(payload.ToolInput, &input); err != nil {
		return nil
	}

	// Check if this is a plan file
	if !isPlanFile(input.FilePath) {
		return nil
	}

	// Auto-import the plan
	err = autoImportPlan(projectID, input.FilePath)
	if err != nil {
		return nil
	}

	// Return context about what was synced
	items, err := monitor.GetActivePlanItems(projectID)
	if err != nil {
		return nil
	}

	resp := hookResponse{
		AdditionalContext: fmt.Sprintf(
			"Plan file synced to Endless. %d active item(s) tracked.",
			len(items),
		),
	}
	return json.NewEncoder(os.Stdout).Encode(resp)
}

func isPlanFile(path string) bool {
	lower := strings.ToLower(path)
	if strings.Contains(lower, "/.claude/plans/") {
		return true
	}
	if strings.HasSuffix(lower, "/plan.md") {
		return true
	}
	return false
}

func autoImportPlan(projectID int64, filePath string) error {
	// Shell out to `endless plan import` for now.
	// This reuses the Python parser which handles all the
	// markdown parsing complexity.
	projectName, err := monitor.GetProjectName(projectID)
	if err != nil {
		return err
	}

	cmd := exec.Command(
		"endless", "plan", "import", filePath,
		"--project", projectName, "--clear",
	)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

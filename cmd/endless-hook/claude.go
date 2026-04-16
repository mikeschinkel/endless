package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
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

type toolInputBash struct {
	Command string `json:"command"`
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

	projectID, isRegistered, err := monitor.ProjectIDForPath(payload.CWD)
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
	case "SessionStart":
		// Track the session from the start
		_ = monitor.InitSession(payload.SessionID, projectID, payload.CWD)
		return handlePlanContextInjection(projectID, payload)

	case "UserPromptSubmit":
		return handlePlanContextInjection(projectID, payload)

	case "PreToolUse":
		return handlePreToolUse(projectID, isRegistered, payload)

	case "PostToolUse":
		return handlePostToolUse(projectID, payload)

	case "ExitPlanMode":
		return handleExitPlanMode(projectID, payload)

	case "Stop", "SessionEnd":
		_ = monitor.EndSession(payload.SessionID)
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

	// Detect endless plan start/complete/chat commands and update session state
	handlePostToolUseSession(projectID, payload)

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

	// Record which plan file this session is editing
	_ = monitor.SetPlanFilePath(payload.SessionID, input.FilePath)

	// Auto-import the plan
	err = autoImportPlan(projectID, payload.SessionID, input.FilePath)
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

// writeTools are the only tools that require task registration.
// Everything else (Read, Glob, Grep, Bash, etc.) passes through.
var writeTools = map[string]bool{
	"Write":        true,
	"Edit":         true,
	"NotebookEdit": true,
}

var planStartRe = regexp.MustCompile(`endless\s+plan\s+start\s+(\d+)`)
var planCompleteRe = regexp.MustCompile(`endless\s+plan\s+complete\s+(\d+)`)
var planChatRe = regexp.MustCompile(`endless\s+plan\s+chat`)

func handlePreToolUse(projectID int64, isRegistered bool, payload claudePayload) error {
	// No enforcement for unregistered/anonymous projects
	if !isRegistered {
		return nil
	}

	// Only enforce on file-writing tools
	if !writeTools[payload.ToolName] {
		return nil
	}

	// Check tracking mode
	mode := monitor.GetTrackingMode(projectID)
	if mode != "enforce" {
		return nil
	}

	// Check for active session
	session, err := monitor.GetActiveSession(payload.SessionID)
	if err == nil && session != nil {
		if session.State == "working" {
			// Check expiration
			if monitor.IsSessionExpired(session, 30) {
				blockToolUse("Your work session has expired due to inactivity.\n\n" +
					"Run `endless plan start <id>` to resume working on a task.\n" +
					"Run `endless plan show` to see available tasks.")
			}
			// Active and valid — allow through, touch session
			_ = monitor.TouchSession(payload.SessionID)
			return nil
		}
	}

	// No active session — block with helpful message
	projectName, _ := monitor.GetProjectName(projectID)
	items, _ := monitor.GetActivePlanItems(projectID)

	var msg strings.Builder
	fmt.Fprintf(&msg, "BLOCKED: No active work session for project '%s'.\n", projectName)
	msg.WriteString("You must register which task you're working on before making changes.\n\n")

	if len(items) > 0 {
		msg.WriteString("Available tasks:\n")
		limit := 10
		if len(items) < limit {
			limit = len(items)
		}
		for _, item := range items[:limit] {
			fmt.Fprintf(&msg, "  #%d [%s] %s\n", item.ID, item.Status, item.Text)
		}
		if len(items) > 10 {
			fmt.Fprintf(&msg, "  ... and %d more\n", len(items)-10)
		}
		msg.WriteString("\n")
	}

	msg.WriteString("Run one of:\n")
	msg.WriteString("  endless plan start <id>   — start working on a specific task\n")
	msg.WriteString("  endless plan show         — see all available tasks\n")
	msg.WriteString("  endless plan chat         — start a chat-only session (no task tracking)\n")

	blockToolUse(msg.String())
	return nil // unreachable, blockToolUse calls os.Exit
}

// blockToolUse writes an error to stderr and exits with code 2.
// Claude Code interprets exit code 2 as "action blocked" and feeds stderr
// back to Claude as context.
func blockToolUse(message string) {
	fmt.Fprint(os.Stderr, message)
	os.Exit(2)
}

func handlePostToolUseSession(projectID int64, payload claudePayload) {
	if payload.ToolName != "Bash" {
		return
	}

	var input toolInputBash
	if err := json.Unmarshal(payload.ToolInput, &input); err != nil {
		return
	}

	// Detect: endless plan start <id>
	if m := planStartRe.FindStringSubmatch(input.Command); m != nil {
		taskID, err := strconv.ParseInt(m[1], 10, 64)
		if err == nil {
			_ = monitor.StartWorkSession(payload.SessionID, projectID, taskID, payload.CWD)
		}
		return
	}

	// Detect: endless plan complete <id>
	if m := planCompleteRe.FindStringSubmatch(input.Command); m != nil {
		taskID, err := strconv.ParseInt(m[1], 10, 64)
		if err == nil {
			_ = monitor.CompleteTask(payload.SessionID, taskID)
		}
		return
	}

	// Detect: endless plan chat
	if planChatRe.MatchString(input.Command) {
		_ = monitor.StartChatSession(payload.SessionID, projectID, payload.CWD)
		return
	}
}

func handleExitPlanMode(projectID int64, payload claudePayload) error {
	// Use the plan file path recorded during PostToolUse/Write
	planFile := monitor.GetPlanFilePath(payload.SessionID)

	// Fall back to most recently modified plan file if not recorded
	if planFile == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil
		}
		plansDir := filepath.Join(home, ".claude", "plans")
		entries, err := os.ReadDir(plansDir)
		if err != nil {
			return nil
		}
		var newestTime int64
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			if info.ModTime().Unix() > newestTime {
				newestTime = info.ModTime().Unix()
				planFile = filepath.Join(plansDir, e.Name())
			}
		}
	}

	if planFile == "" {
		return nil
	}

	err := autoImportPlan(projectID, payload.SessionID, planFile)
	if err != nil {
		return nil
	}

	items, err := monitor.GetActivePlanItems(projectID)
	if err != nil {
		return nil
	}

	resp := hookResponse{
		AdditionalContext: fmt.Sprintf(
			"Plan accepted and synced to Endless. %d active item(s) tracked.",
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

// tmuxPlanID reads @endless_plan_id from the current tmux window.
// Returns 0 if not in tmux or not set.
func tmuxPlanID() int64 {
	pane := os.Getenv("TMUX_PANE")
	if pane == "" {
		return 0
	}
	out, err := exec.Command(
		"tmux", "display-message", "-p", "-t", pane, "#{@endless_plan_id}",
	).Output()
	if err != nil {
		return 0
	}
	val := strings.TrimSpace(string(out))
	if val == "" {
		return 0
	}
	id, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		return 0
	}
	return id
}

// resolveParentPlanID determines the parent plan ID for auto-import.
// Priority: session's active goal > tmux @endless_plan_id > none.
func resolveParentPlanID(sessionID string) *int64 {
	// Check session's active goal first
	session, err := monitor.GetActiveSession(sessionID)
	if err == nil && session != nil && session.ActiveGoalID != nil {
		return session.ActiveGoalID
	}
	// Fall back to tmux window option
	if id := tmuxPlanID(); id > 0 {
		return &id
	}
	return nil
}

func autoImportPlan(projectID int64, sessionID, filePath string) error {
	projectName, err := monitor.GetProjectName(projectID)
	if err != nil {
		return err
	}

	args := []string{"plan", "import", filePath, "--project", projectName, "--replace"}

	if parentID := resolveParentPlanID(sessionID); parentID != nil {
		args = append(args, "--parent", strconv.FormatInt(*parentID, 10))
	}

	cmd := exec.Command("endless", args...)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

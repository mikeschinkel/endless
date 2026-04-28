package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mikeschinkel/endless/internal/monitor"
)

func init() {
	// Log to both stderr and a persistent log file
	logDir := filepath.Join(monitor.ConfigDir(), "log")
	os.MkdirAll(logDir, 0755)
	logFile, err := os.OpenFile(
		filepath.Join(logDir, "hook.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		0644,
	)
	if err != nil {
		// Fall back to stderr only
		log.SetOutput(os.Stderr)
	} else {
		log.SetOutput(io.MultiWriter(os.Stderr, logFile))
	}
	log.SetFlags(log.Ldate | log.Ltime)
	log.SetPrefix("endless-hook: ")
}

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
		return fmt.Errorf("looking up project for %s: %w", payload.CWD, err)
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
		if err := monitor.RecordActivity(projectID, "claude", payload.CWD, sessionCtx); err != nil {
			return fmt.Errorf("recording activity: %w", err)
		}
	}

	// Event-specific handling
	switch payload.EventName {
	case "SessionStart":
		// Track the session from the start — DB errors here are critical
		if err := monitor.InitSession(payload.SessionID, projectID); err != nil {
			return fmt.Errorf("initializing session: %w", err)
		}
		if err := monitor.SetProcess(payload.SessionID, os.Getenv("TMUX_PANE")); err != nil {
			return fmt.Errorf("setting process: %w", err)
		}
		// Store transcript path for reimport
		if payload.TranscriptPath != "" {
			monitor.SetTranscriptPath(payload.SessionID, payload.TranscriptPath)
		}
		// Auto-associate session with task from tmux @endless_task_id
		if taskID := tmuxTaskID(); taskID > 0 {
			monitor.StartWorkSession(payload.SessionID, projectID, taskID)
		}
		return handleTaskContextInjection(projectID, payload)

	case "UserPromptSubmit":
		// Parse transcript to capture new messages
		monitor.ParseTranscript(payload.SessionID, payload.TranscriptPath)
		// Scan the freshly-parsed assistant turn(s) for SUGGESTION banners (E-918)
		if err := monitor.ScanRecentSuggestions(payload.SessionID, projectID); err != nil {
			log.Printf("scanning suggestions: %v", err)
		}
		// Backfill process if not yet recorded
		if err := monitor.BackfillProcess(payload.SessionID, os.Getenv("TMUX_PANE")); err != nil {
			return fmt.Errorf("backfilling process: %w", err)
		}
		// Fallback message check for sessions without MCP channel plugin
		pane := os.Getenv("TMUX_PANE")
		port, _, _ := monitor.LookupChannelPort(pane)
		if port == 0 {
			hasMsgs, err := monitor.HasPendingMessages(pane)
			if err == nil && hasMsgs {
				resp := hookResponse{
					AdditionalContext: "You have pending inter-session messages. Run: endless channel inbox",
				}
				return json.NewEncoder(os.Stdout).Encode(resp)
			}
		}
		return handleTaskContextInjection(projectID, payload)

	case "PreToolUse":
		return handlePreToolUse(projectID, isRegistered, payload)

	case "PostToolUse":
		return handlePostToolUse(projectID, payload)

	case "ExitPlanMode":
		return handleExitPlanMode(projectID, payload)

	case "Stop":
		// Parse transcript before idling — captures the assistant's last response
		monitor.ParseTranscript(payload.SessionID, payload.TranscriptPath)
		monitor.FlagNeedsRecap(payload.SessionID)
		if err := monitor.IdleSession(payload.SessionID); err != nil {
			return fmt.Errorf("idling session: %w", err)
		}
	case "PreCompact":
		// Capture everything before compaction
		monitor.ParseTranscript(payload.SessionID, payload.TranscriptPath)
	case "SessionEnd":
		// Final parse
		monitor.ParseTranscript(payload.SessionID, payload.TranscriptPath)
		monitor.FlagNeedsRecap(payload.SessionID)
		if err := monitor.EndSession(payload.SessionID); err != nil {
			return fmt.Errorf("ending session: %w", err)
		}
	}

	return nil
}

func handleTaskContextInjection(projectID int64, payload claudePayload) error {
	// Only inject once per session
	if monitor.HasInjectedContext(payload.SessionID) {
		return nil
	}

	projectName, err := monitor.GetProjectName(projectID)
	if err != nil {
		return fmt.Errorf("getting project name: %w", err)
	}

	items, err := monitor.GetActiveTasks(projectID)
	if err != nil {
		return fmt.Errorf("getting active tasks: %w", err)
	}

	context := monitor.FormatTasks(projectName, items)

	// Append open-suggestion count if any (E-918)
	if n, err := monitor.CountOpenSuggestions(projectID); err == nil && n > 0 {
		context += fmt.Sprintf(
			"\n\n%d unreviewed AI-agent suggestion(s) for project %q. Run `endless suggestions list` to review.",
			n, projectName,
		)
	}

	// Mark as injected so we don't repeat
	monitor.MarkContextInjected(projectID, payload.SessionID, payload.CWD)

	resp := hookResponse{
		AdditionalContext: context,
	}
	return json.NewEncoder(os.Stdout).Encode(resp)
}

func handlePostToolUse(projectID int64, payload claudePayload) error {
	// Detect endless task start/complete/chat commands and update session state
	if err := handlePostToolUseSession(projectID, payload); err != nil {
		return fmt.Errorf("post tool use session: %w", err)
	}

	// Register edited file with the active task (E-917 edit-set).
	// Runs for any write tool, regardless of whether drift_detection is enforcing.
	if writeTools[payload.ToolName] {
		if path := extractFilePath(payload.ToolName, payload.ToolInput); path != "" {
			if session, err := monitor.GetActiveSession(payload.SessionID); err == nil && session != nil && session.ActiveTaskID != nil {
				if err := monitor.RegisterTaskFile(*session.ActiveTaskID, payload.SessionID, path); err != nil {
					log.Printf("registering task file: %v", err)
				}
			}
		}
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

	// Record which plan file this session is editing (used by ExitPlanMode)
	if err := monitor.SetPlanFilePath(payload.SessionID, input.FilePath); err != nil {
		return fmt.Errorf("setting plan file path: %w", err)
	}

	// Snapshot the plan content so within-session overwrites and never-attached
	// plans don't lose data (E-969). Per-session, content-addressed by SHA-256.
	if err := snapshotPlanFile(projectID, payload.SessionID, input.FilePath); err != nil {
		// Non-fatal: snapshotting should never block a hook
		log.Printf("plan snapshot: %v", err)
	}

	// NOTE: Auto-import disabled. Sessions should use `endless task update <id> --text <file>`
	// to save task text, and `endless task add` to create child items explicitly.
	// Auto-import created duplicate items at the wrong granularity (every bullet became a task item).

	items, err := monitor.GetActiveTasks(projectID)
	if err != nil {
		return fmt.Errorf("getting active tasks: %w", err)
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

// extractFilePath pulls the target file path out of a write-tool's input.
// Write/Edit use "file_path"; NotebookEdit uses "notebook_path".
func extractFilePath(toolName string, raw json.RawMessage) string {
	var probe struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return ""
	}
	if probe.FilePath != "" {
		return probe.FilePath
	}
	return probe.NotebookPath
}

var taskStartRe = regexp.MustCompile(`endless\s+task\s+start\s+(\d+)`)
var taskCompleteRe = regexp.MustCompile(`endless\s+task\s+complete\s+(\d+)`)
var taskChatRe = regexp.MustCompile(`endless\s+task\s+chat`)
var channelBeaconRe = regexp.MustCompile(`endless\s+channel\s+beacon`)
var channelConnectRe = regexp.MustCompile(`endless\s+channel\s+connect\s+(\S+)`)
var channelSendRe = regexp.MustCompile(`endless\s+channel\s+send`)

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
					"Run `endless task start <id>` to resume working on a task.\n" +
					"Run `endless task show` to see available tasks.")
			}
			// Active and valid — allow through, touch session
			if err := monitor.TouchSession(payload.SessionID); err != nil {
				log.Printf("touching session: %v", err)
			}
			// Drift detection (E-917): if enabled, ensure target file is in scope.
			if monitor.IsCheckEnabled(projectID, "drift_detection") && session.ActiveTaskID != nil {
				path := extractFilePath(payload.ToolName, payload.ToolInput)
				if path != "" {
					inScope, err := monitor.IsFileInTaskScope(*session.ActiveTaskID, path)
					if err != nil {
						log.Printf("drift scope check: %v", err)
					} else if !inScope {
						blockDriftViolation(*session.ActiveTaskID, path)
					}
				}
			}
			return nil
		}
	}

	// No active session — block with helpful message
	projectName, _ := monitor.GetProjectName(projectID)
	items, _ := monitor.GetActiveTasks(projectID)

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
			fmt.Fprintf(&msg, "  E-%d [%s] %s\n", item.ID, item.Status, item.Text)
		}
		if len(items) > 10 {
			fmt.Fprintf(&msg, "  ... and %d more\n", len(items)-10)
		}
		msg.WriteString("\n")
	}

	msg.WriteString("Run one of:\n")
	msg.WriteString("  endless task start <id>   — start working on a specific task\n")
	msg.WriteString("  endless task show         — see all available tasks\n")
	msg.WriteString("  endless task chat         — start a chat-only session (no task tracking)\n")

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

// blockDriftViolation blocks an edit that targets a file outside the active
// task's scope. The message offers three remedies (switch, sub-task, extend)
// and invites Claude to suggest a rule relaxation via a SUGGESTION banner.
func blockDriftViolation(activeTaskID int64, filePath string) {
	taskTitle, _ := monitor.GetTaskTitle(activeTaskID)
	var msg strings.Builder
	fmt.Fprintf(&msg, "BLOCKED (drift_detection): editing `%s` but it is not in scope of active task E-%d", filePath, activeTaskID)
	if taskTitle != "" {
		fmt.Fprintf(&msg, " %q", taskTitle)
	}
	msg.WriteString(".\n\nChoose one:\n")
	fmt.Fprintf(&msg, "  endless task start <id>                                  switch focus (active task is preserved)\n")
	fmt.Fprintf(&msg, "  endless task add \"<title>\" --parent E-%d                 add a sub-task for this work\n", activeTaskID)
	fmt.Fprintf(&msg, "  endless task touch E-%d --add-file %s    register this file as in-scope of the current task\n", activeTaskID, filePath)
	msg.WriteString("\nIf this prompt is needless here, also include in your next response:\n")
	msg.WriteString("  **SUGGESTION (drift_detection):** <one-line explanation of why this should not have blocked>\n")
	blockToolUse(msg.String())
}

func handlePostToolUseSession(projectID int64, payload claudePayload) error {
	if payload.ToolName != "Bash" {
		return nil
	}

	var input toolInputBash
	if err := json.Unmarshal(payload.ToolInput, &input); err != nil {
		return nil
	}

	// Detect: endless task start <id>
	if m := taskStartRe.FindStringSubmatch(input.Command); m != nil {
		taskID, err := strconv.ParseInt(m[1], 10, 64)
		if err == nil {
			if err := monitor.StartWorkSession(payload.SessionID, projectID, taskID); err != nil {
				return fmt.Errorf("starting work session: %w", err)
			}
		}
		return nil
	}

	// Detect: endless task complete <id>
	if m := taskCompleteRe.FindStringSubmatch(input.Command); m != nil {
		taskID, err := strconv.ParseInt(m[1], 10, 64)
		if err == nil {
			if err := monitor.CompleteTask(payload.SessionID, taskID); err != nil {
				return fmt.Errorf("completing task: %w", err)
			}
		}
		return nil
	}

	// Detect: endless task chat
	if taskChatRe.MatchString(input.Command) {
		if err := monitor.StartChatSession(payload.SessionID, projectID); err != nil {
			return fmt.Errorf("starting chat session: %w", err)
		}
		return nil
	}

	// Detect: endless channel beacon/connect/send (for activity tracking)
	if channelBeaconRe.MatchString(input.Command) ||
		channelConnectRe.MatchString(input.Command) ||
		channelSendRe.MatchString(input.Command) {
		if err := monitor.TouchSession(payload.SessionID); err != nil {
			return fmt.Errorf("touching session: %w", err)
		}
		return nil
	}

	return nil
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

	// NOTE: Auto-import disabled. The plan file path is still tracked so sessions
	// can reference it with `endless task update <id> --text <plan-file>`.
	// See PostToolUse/Write handler for rationale.

	items, err := monitor.GetActiveTasks(projectID)
	if err != nil {
		return fmt.Errorf("getting active tasks: %w", err)
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

// snapshotPlanFile captures the just-written content of a plan file into
// <project-root>/.endless/plans/snapshots/<ts>-<sha8>.{md,json} so that
// within-session overwrites and never-attached plans don't lose data.
//
// Idempotent: same content from the same session is only stored once
// (filename includes content hash, so re-runs are no-ops).
//
// projectID and sessionID identify the snapshot's session origin in the
// sidecar. srcPath is the harness path Claude wrote to.
func snapshotPlanFile(projectID int64, sessionID, srcPath string) error {
	content, err := os.ReadFile(srcPath)
	if err != nil {
		return fmt.Errorf("read plan file: %w", err)
	}

	// Content hash; first 8 hex chars suffice for filename disambiguation.
	sum := sha256.Sum256(content)
	shaFull := hex.EncodeToString(sum[:])
	sha8 := shaFull[:8]
	ts := time.Now().UTC().Format("20060102T150405")

	projectRoot, err := monitor.ProjectPath(projectID)
	if err != nil {
		return fmt.Errorf("project path lookup: %w", err)
	}
	snapsDir := filepath.Join(projectRoot, ".endless", "plans", "snapshots")
	if err := os.MkdirAll(snapsDir, 0755); err != nil {
		return fmt.Errorf("create snapshots dir: %w", err)
	}

	// Idempotency: if a snapshot for this content+session already exists today,
	// skip. Match by sha8 in the filename and session_id in the sidecar.
	if existingSnapshot(snapsDir, sha8, sessionID) {
		return nil
	}

	stem := fmt.Sprintf("%s-%s", ts, sha8)
	mdPath := filepath.Join(snapsDir, stem+".md")
	jsonPath := filepath.Join(snapsDir, stem+".json")

	if err := os.WriteFile(mdPath, content, 0644); err != nil {
		return fmt.Errorf("write snapshot md: %w", err)
	}

	sidecar := map[string]string{
		"session_id":  sessionID,
		"written_at":  time.Now().UTC().Format(time.RFC3339),
		"source_path": srcPath,
		"sha256":      shaFull,
	}
	jsonBytes, _ := json.MarshalIndent(sidecar, "", "  ")
	if err := os.WriteFile(jsonPath, jsonBytes, 0644); err != nil {
		return fmt.Errorf("write snapshot json: %w", err)
	}
	return nil
}

// existingSnapshot returns true if a snapshot with the given sha8 prefix
// exists in snapsDir whose sidecar's session_id matches.
func existingSnapshot(snapsDir, sha8, sessionID string) bool {
	entries, err := os.ReadDir(snapsDir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		// Filename format: <ts>-<sha8>.json
		if !strings.Contains(name, "-"+sha8+".") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(snapsDir, name))
		if err != nil {
			continue
		}
		var meta struct {
			SessionID string `json:"session_id"`
		}
		if err := json.Unmarshal(data, &meta); err != nil {
			continue
		}
		if meta.SessionID == sessionID {
			return true
		}
	}
	return false
}

// tmuxTaskID reads @endless_task_id from the current tmux window.
// Returns 0 if not in tmux or not set.
func tmuxTaskID() int64 {
	pane := os.Getenv("TMUX_PANE")
	if pane == "" {
		return 0
	}
	out, err := exec.Command(
		"tmux", "display-message", "-p", "-t", pane, "#{@endless_task_id}",
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

// resolveParentTaskID determines the parent task ID for auto-import.
// Priority: session's active goal > tmux @endless_task_id > none.
func resolveParentTaskID(sessionID string) *int64 {
	// Check session's active goal first
	session, err := monitor.GetActiveSession(sessionID)
	if err == nil && session != nil && session.ActiveTaskID != nil {
		return session.ActiveTaskID
	}
	// Fall back to tmux window option
	if id := tmuxTaskID(); id > 0 {
		return &id
	}
	return nil
}

func autoImportTask(projectID int64, sessionID, filePath string) error {
	projectName, err := monitor.GetProjectName(projectID)
	if err != nil {
		return err
	}

	args := []string{"task", "import", filePath, "--project", projectName, "--replace"}

	if parentID := resolveParentTaskID(sessionID); parentID != nil {
		args = append(args, "--parent", strconv.FormatInt(*parentID, 10))
	}

	cmd := exec.Command("endless", args...)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

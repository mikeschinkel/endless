package data

import (
	"fmt"
	"time"
)

// SmartDate formats a timestamp string as a human-friendly relative date.
func SmartDate(ts string) string {
	if ts == "" {
		return "-"
	}

	t, err := time.Parse("2006-01-02T15:04:05", ts)
	if err != nil {
		return ts
	}

	now := time.Now().UTC()
	diff := now.Sub(t)

	switch {
	case diff < time.Minute:
		return "just now"
	case diff < time.Hour:
		mins := int(diff.Minutes())
		if mins == 1 {
			return "1 min ago"
		}
		return fmt.Sprintf("%d min ago", mins)
	case diff < 24*time.Hour:
		hours := int(diff.Hours())
		if hours == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	case diff < 48*time.Hour:
		return "yesterday"
	case diff < 7*24*time.Hour:
		days := int(diff.Hours() / 24)
		return fmt.Sprintf("%d days ago", days)
	default:
		if t.Year() == now.Year() {
			return t.Format("Jan 2")
		}
		return t.Format("Jan 2, 2006")
	}
}

type DashboardProject struct {
	ID             int64
	Name           string
	Label          string
	Description    string
	Status         string
	Language       string
	Path           string
	ShortPath      string
	GroupName      string
	PendingNotes   int
	ActiveTasks    int
	TaskTotal      int
	TaskCompleted  int
	TaskInProgress int
	LastActivity   string
}

type TaskView struct {
	ID          int64
	Title       string
	Text        string
	Phase       string
	Status      string
	Type        string
	ParentID    *int64
	ChildCount  int
	BlockedBy   string
	Tier        *int
	CreatedAt   string
	UpdatedAt   string
	CompletedAt string
	Depth       int // nesting depth for tree display (0 = root)
	SiblingNum  int // 1-based position among siblings (per parent)
}

// TierLabel returns the human-readable label for a tier value.
func TierLabel(tier *int) string {
	if tier == nil {
		return ""
	}
	labels := map[int]string{1: "auto", 2: "quick", 3: "deep", 4: "discuss"}
	if label, ok := labels[*tier]; ok {
		return label
	}
	return ""
}

type ActivityView struct {
	ID        int64
	Project   string
	Source    string
	Event     string
	WorkDir   string
	CreatedAt string
}

type NoteView struct {
	ID       int64
	Project  string
	Type     string
	Message  string
	Created  string
	Resolved bool
}

type TaskSummary struct {
	Total      int
	InProgress int
	Completed  int
}

type CurrentWorkItem struct {
	Project      string
	Title        string
	Text         string
	TaskID       int64
	Status       string // task item status
	SessionState string
	LastActivity string
}

type TaskGroup struct {
	GroupID   int64
	GroupName string
	Tasks    []TaskView // max 3 next tasks
	Total    int
	Done     int
}

type StatusDetail struct {
	Project      DashboardProject
	TaskGroups   []TaskGroup
	TaskItems    []TaskView // tree-ordered items with Depth
	PendingNotes int
}

type DependencyView struct {
	SourceType string
	SourceID   int64
	SourceName string
	TargetType string
	TargetID   int64
	TargetName string
	DepType    string
}

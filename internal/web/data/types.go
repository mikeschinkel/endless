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
	ActivePlan     int
	TaskTotal      int
	TaskCompleted  int
	TaskInProgress int
	LastActivity   string
}

type PlanItemView struct {
	ID         int64
	Title      string
	Text       string
	Phase      string
	Status     string
	ParentID   *int64
	ChildCount int
	BlockedBy  string
	Depth      int // nesting depth for tree display (0 = root)
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

type PlanSummary struct {
	Total      int
	InProgress int
	Completed  int
}

type CurrentWorkItem struct {
	Project      string
	Title        string
	Text         string
	TaskID       int64
	Status       string // plan item status
	SessionState string
	LastActivity string
}

type PlanWithTasks struct {
	PlanID   int64
	PlanName string
	Tasks    []PlanItemView // max 3 next tasks
	Total    int
	Done     int
}

type StatusDetail struct {
	Project      DashboardProject
	Plans        []PlanWithTasks
	PlanItems    []PlanItemView // tree-ordered items with Depth
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

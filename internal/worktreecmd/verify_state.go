package worktreecmd

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/mikeschinkel/endless/internal/dbprovenance"
	"github.com/mikeschinkel/endless/internal/events"
	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/refusal"
	"github.com/mikeschinkel/endless/internal/taskstatus"
	"github.com/mikeschinkel/endless/internal/tasktype"
)

// VerifyState is what `worktree land` and the `unlanded` reconcile need to know
// about one task (E-2262): its type's land properties, its status, the commit
// its user verify passed at, and whether the branch has moved past that commit.
//
// The decisions — refuse, settle, reset — are the Python caller's. This verb
// only answers, because the answer needs the database and git, and Python may
// not read the database (CLAUDE.md: Go owns database access).
type VerifyState struct {
	TaskID  int64  `json:"task_id"`
	Project string `json:"project"`
	Root    string `json:"root"`
	Type    string `json:"type"`
	Status  string `json:"status"`

	// Lands, RequiresVerifySuite and SettlesOnLand are the type's properties
	// (tasktype). UnlandedLane says whether the type's lifecycle has an
	// `unlanded` at all — a type without one cannot be gated on it.
	Lands               bool `json:"lands"`
	RequiresVerifySuite bool `json:"requires_verify_suite"`
	SettlesOnLand       bool `json:"settles_on_land"`
	UnlandedLane        bool `json:"unlanded_lane"`

	// VerifiedSHA is the passed commit; empty when none is recorded.
	VerifiedSHA string `json:"verified_sha,omitempty"`

	// Stale is meaningful only on an `unlanded` task: true when the pass no
	// longer covers the branch, with StaleReason saying why.
	Stale       bool   `json:"stale"`
	StaleReason string `json:"stale_reason,omitempty"`
}

// runVerifyState answers VerifyState for one task, or for every `unlanded` task.
//
//	endless-go worktree verify-state [--task <id>]
//
// Always JSON on stdout: an array, empty when nothing matches. Exit 0 whenever
// it could answer, 1 when it could not — the land caller treats that as a
// refusal and the reconcile caller as "nothing to reset". Takes the caller's
// --db context, like `in-use`, because it reads the tasks the caller sees.
func runVerifyState(args []string) int {
	fs := refusal.NewFlags("verify-state")
	taskID := fs.Int64("task", 0, "one task's state, whatever its status; 0 means every unlanded task")
	if err := fs.Parse(args); err != nil {
		verifyStateFault("%s", err).Text(fs.Output()).Print()
		return exitUsage
	}

	db, err := monitor.DB()
	if err != nil {
		verifyStateFault("%s", err).Print()
		return exitUndetermined
	}
	states, err := VerifyStates(db, *taskID)
	if err != nil {
		verifyStateFault("%s", err).Print()
		return exitUndetermined
	}
	if err = dbprovenance.EncodeIndent(os.Stdout, states, "  "); err != nil {
		verifyStateFault("%s", err).Print()
		return exitUndetermined
	}
	return exitNotInUse
}

// verifyStateFault classifies every failure the way the other verbs here do:
// its callers build the argv and treat a non-zero exit as "no answer", so
// nothing is retypable and nothing is the user's to decide.
func verifyStateFault(format string, args ...any) *refusal.Error {
	return refusal.Faultf("endless-go worktree verify-state: "+format, args...).
		Command("worktree verify-state")
}

// VerifyStates reads one task (taskID > 0), or every `unlanded` task, and
// computes whether each recorded pass is stale.
func VerifyStates(db *sql.DB, taskID int64) ([]VerifyState, error) {
	query := `SELECT t.id, p.name, p.path, t.type_id, t.status, COALESCE(t.verified_sha, '')
	            FROM live_tasks t JOIN projects p ON p.id = t.project_id`
	var args []any
	if taskID > 0 {
		query += ` WHERE t.id = ?`
		args = append(args, taskID)
	} else {
		query += ` WHERE t.status = ?`
		args = append(args, taskstatus.Unlanded)
	}
	query += ` ORDER BY t.id`

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("read tasks: %w", err)
	}
	defer rows.Close()

	states := []VerifyState{}
	for rows.Next() {
		var s VerifyState
		var typeID sql.NullInt64
		if err = rows.Scan(&s.TaskID, &s.Project, &s.Root, &typeID, &s.Status, &s.VerifiedSHA); err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		// A stored path may be `~/…` (E-2011); git needs the real directory.
		if s.Root, err = monitor.ResolvedProjectPath(s.Root); err != nil {
			return nil, fmt.Errorf("resolve project %s path: %w", s.Project, err)
		}
		tt := tasktype.TaskTypeTask
		if typeID.Valid {
			tt = tasktype.TaskType(typeID.Int64)
		}
		s.Type = tt.String()
		s.Lands = tt.Lands()
		s.RequiresVerifySuite = tt.RequiresVerifySuite()
		s.SettlesOnLand = tt.SettlesOnLand()
		s.UnlandedLane = taskstatus.TransitionAllowed(taskstatus.Unverified, taskstatus.Unlanded, tt)
		states = append(states, s)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("read tasks: %w", err)
	}

	for i := range states {
		if states[i].Status != taskstatus.Unlanded {
			continue
		}
		states[i].Stale, states[i].StaleReason = PassIsStale(
			states[i].Root, states[i].VerifiedSHA, TaskBranch(states[i].TaskID))
	}
	return states, nil
}

// TaskBranch is a task's branch name (ED-1587).
func TaskBranch(taskID int64) string {
	return "task/" + strconv.FormatInt(taskID, 10)
}

// PassIsStale reports whether branch holds any change past the passed commit
// sha, other than the files Endless itself commits onto a task branch
// (monitor.AutoManagedStatusGlobs). A content diff rather than a SHA match:
// Endless commits ledger entries onto the branch after a verify, and those
// must not undo the pass.
//
// Anything that stops the comparison — no recorded commit, a commit or branch
// git cannot resolve — counts as stale: a pass that cannot be shown to cover
// the branch does not unlock a land.
func PassIsStale(repo, sha, branch string) (bool, string) {
	if sha == "" {
		return true, "no passing verify commit is recorded"
	}
	args := []string{"-C", repo, "diff", "--quiet", sha, branch, "--", "."}
	for _, glob := range monitor.AutoManagedStatusGlobs {
		args = append(args, ":(exclude,glob)"+glob)
	}
	cmd := exec.Command("git", args...)
	cmd.Env = events.SanitizedGitEnv()
	out, err := cmd.CombinedOutput()
	if err == nil {
		return false, ""
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return true, fmt.Sprintf("%s has changed since the verify passed at %s", branch, short(sha))
	}
	return true, fmt.Sprintf("cannot compare %s with the verify that passed at %s: %s",
		branch, short(sha), strings.TrimSpace(string(out)))
}

func short(sha string) string {
	if len(sha) > 10 {
		return sha[:10]
	}
	return sha
}

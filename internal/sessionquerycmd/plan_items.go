package sessionquerycmd

import (
	"context"
	"os"
	"os/exec"
	"strings"

	"github.com/mikeschinkel/endless/internal/dbprovenance"
	"github.com/mikeschinkel/endless/internal/docmirror"
	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/reconcilegate"
	"github.com/mikeschinkel/endless/internal/refusal"
)

// runPlanItems prints a task's plan items (E-2280) as a JSON array, each with
// what the latest reconciliation said about it.
func runPlanItems(args []string) error {
	fs := refusal.NewFlags("plan-items")
	id := fs.Int64("id", 0, "task id")
	all := fs.Bool("all", false, "include withdrawn items")
	if err := parseFlags(fs, "plan-items", args); err != nil {
		return err
	}
	if *id <= 0 {
		return refusal.NoReport("--id is required", "Pass --id <task-id> and retry")
	}
	items, err := monitor.PlanItems(*id, *all)
	if err != nil {
		return err
	}
	return dbprovenance.Encode(os.Stdout, items)
}

// runMootQuestions prints the open discrepancy questions on withdrawn items of a
// task — what a re-approval closes.
func runMootQuestions(args []string) error {
	fs := refusal.NewFlags("moot-questions")
	id := fs.Int64("id", 0, "task id")
	if err := parseFlags(fs, "moot-questions", args); err != nil {
		return err
	}
	if *id <= 0 {
		return refusal.NoReport("--id is required", "Pass --id <task-id> and retry")
	}
	qs, err := monitor.MootQuestions(*id)
	if err != nil {
		return err
	}
	return dbprovenance.Encode(os.Stdout, qs)
}

// runFingerprint prints {fingerprint, head} for the branch checked out at --repo.
func runFingerprint(args []string) error {
	fs := refusal.NewFlags("fingerprint")
	repo := fs.String("repo", "", "the task's worktree (or checkout)")
	if err := parseFlags(fs, "fingerprint", args); err != nil {
		return err
	}
	if *repo == "" {
		return refusal.NoReport("--repo is required", "Pass --repo <worktree> and retry")
	}
	fp, head, err := reconcilegate.Fingerprint(context.Background(), *repo)
	if err != nil {
		return err
	}
	return dbprovenance.Encode(os.Stdout, map[string]string{"fingerprint": fp, "head": head})
}

// runReconcileGate prints the gate's verdict on one task against the branch at
// --repo. It exits 0 either way: the verdict is the answer, and the caller
// decides what a failing one refuses.
func runReconcileGate(args []string) error {
	fs := refusal.NewFlags("reconcile-gate")
	id := fs.Int64("id", 0, "task id")
	repo := fs.String("repo", "", "the task's worktree (or checkout)")
	mode := fs.String("mode", string(reconcilegate.ModeHandover), "handover | backstop")
	to := fs.String("to", "", "the status being moved to; the gate stands aside when the lifecycle refuses the move")
	if err := parseFlags(fs, "reconcile-gate", args); err != nil {
		return err
	}
	if *id <= 0 || *repo == "" {
		return refusal.NoReport("--id and --repo are required", "Pass both and retry")
	}
	m := reconcilegate.Mode(*mode)
	if m != reconcilegate.ModeHandover && m != reconcilegate.ModeBackstop {
		return refusal.NoReport("--mode must be handover or backstop", "Pass a valid --mode and retry")
	}
	fp, _, err := reconcilegate.Fingerprint(context.Background(), *repo)
	if err != nil {
		return err
	}
	res, err := reconcilegate.Check(*id, fp, m, *to)
	if err != nil {
		return err
	}
	return dbprovenance.Encode(os.Stdout, res)
}

// reconcileContext is what `task reconcile` shows the agent before it writes a
// reconciliation: the current items with their earlier verdicts and answers,
// and what changed since the last reconciliation across the whole task.
type reconcileContext struct {
	Items          []monitor.PlanItem      `json:"items"`
	Last           *monitor.Reconciliation `json:"last"`
	PlanReapproved bool                    `json:"plan_reapproved"`
	PlanEdited     bool                    `json:"plan_edited"`
	FilesChanged   []string                `json:"files_changed"`
	FilesError     string                  `json:"files_error,omitempty"`
}

func runReconcileContext(args []string) error {
	fs := refusal.NewFlags("reconcile-context")
	id := fs.Int64("id", 0, "task id")
	repo := fs.String("repo", "", "the task's worktree (or checkout)")
	if err := parseFlags(fs, "reconcile-context", args); err != nil {
		return err
	}
	if *id <= 0 {
		return refusal.NoReport("--id is required", "Pass --id <task-id> and retry")
	}
	items, err := monitor.PlanItems(*id, true)
	if err != nil {
		return err
	}
	last, err := monitor.LatestReconciliation(*id)
	if err != nil {
		return err
	}
	ctx := reconcileContext{Items: items, Last: last, FilesChanged: []string{}}
	if last != nil {
		for _, it := range items {
			if it.ApprovedAt != nil && *it.ApprovedAt > last.CreatedAt {
				ctx.PlanReapproved = true
			}
			if it.UpdatedAt > last.CreatedAt {
				ctx.PlanEdited = true
			}
		}
		if *repo != "" && last.Head != nil {
			out, err := exec.Command("git", "-C", *repo, "diff", "--name-only", *last.Head, "HEAD",
				"--", ".", ":(exclude).endless").Output()
			if err != nil {
				ctx.FilesError = err.Error()
			} else {
				for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
					if l != "" {
						ctx.FilesChanged = append(ctx.FilesChanged, l)
					}
				}
			}
		}
	}
	return dbprovenance.Encode(os.Stdout, ctx)
}

// runPlanItemsTOML prints {path, content} for a task's items.toml mirror.
func runPlanItemsTOML(args []string) error {
	fs := refusal.NewFlags("plan-items-toml")
	id := fs.Int64("id", 0, "task id")
	if err := parseFlags(fs, "plan-items-toml", args); err != nil {
		return err
	}
	if *id <= 0 {
		return refusal.NoReport("--id is required", "Pass --id <task-id> and retry")
	}
	content, err := monitor.PlanItemsTOML(*id)
	if err != nil {
		return err
	}
	return dbprovenance.Encode(os.Stdout, map[string]string{
		"path": docmirror.TaskItemsPath(*id), "content": content})
}

// runPlanReadiness prints a task's PlanReadiness.
func runPlanReadiness(args []string) error {
	fs := refusal.NewFlags("plan-readiness")
	id := fs.Int64("id", 0, "task id")
	if err := parseFlags(fs, "plan-readiness", args); err != nil {
		return err
	}
	if *id <= 0 {
		return refusal.NoReport("--id is required", "Pass --id <task-id> and retry")
	}
	r, err := monitor.ReadPlanReadiness(*id)
	if err != nil {
		return err
	}
	return dbprovenance.Encode(os.Stdout, r)
}

// runPostLandState prints a task's PostLandState (E-2444): its post-land items
// with their current verdicts, and the open ones.
func runPostLandState(args []string) error {
	fs := refusal.NewFlags("post-land-state")
	id := fs.Int64("id", 0, "task id")
	if err := parseFlags(fs, "post-land-state", args); err != nil {
		return err
	}
	if *id <= 0 {
		return refusal.NoReport("--id is required", "Pass --id <task-id> and retry")
	}
	st, err := monitor.ReadPostLandState(*id)
	if err != nil {
		return err
	}
	return dbprovenance.Encode(os.Stdout, st)
}

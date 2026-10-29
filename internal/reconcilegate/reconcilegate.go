// Package reconcilegate is the one gate between finished work and a verify
// (E-2280): it passes only when the task's latest reconciliation is current and
// every discrepancy it found is settled.
//
// One function, Check, called from every hand-over — setting `unverified`, an
// agent setting `assumed`, the verify request (E-2263) — and from `task verify`
// itself as a backstop. It is deterministic: it reads rows and a git
// fingerprint, never judges text. Whether a kept answer's reason is a good one
// is the agent's call, recorded in the reconciliation; the gate only checks that
// the call was made.
package reconcilegate

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/planitem"
	"github.com/mikeschinkel/endless/internal/questionstatus"
	"github.com/mikeschinkel/endless/internal/taskstatus"
	"github.com/mikeschinkel/endless/internal/tasktype"
)

// EmptyFingerprint is the fingerprint of a branch with no changes of its own
// outside `.endless/`.
const EmptyFingerprint = "empty"

// Fingerprint identifies the branch's own changes: the `git patch-id --stable`
// of the diff from its merge-base with the project's base branch to HEAD, with
// `.endless/` excluded. A clean rebase onto a moved base leaves it alone; an
// edit, or a conflict resolved differently, changes it. Endless's own ledger and
// mirror commits never do.
func Fingerprint(ctx context.Context, repo string) (fingerprint, head string, err error) {
	base, err := monitor.DefaultBranch(ctx, repo)
	if err != nil {
		return "", "", fmt.Errorf("resolve base branch: %w", err)
	}
	if head, err = git(ctx, repo, nil, "rev-parse", "HEAD"); err != nil {
		return "", "", err
	}
	mb, err := git(ctx, repo, nil, "merge-base", base, "HEAD")
	if err != nil {
		return "", "", fmt.Errorf("merge-base of %s and HEAD: %w", base, err)
	}
	diff, err := git(ctx, repo, nil, "diff", "--binary", "--no-color", "--no-ext-diff",
		"--no-renames", "--src-prefix=a/", "--dst-prefix=b/", mb, "HEAD",
		"--", ".", ":(exclude).endless")
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(diff) == "" {
		return EmptyFingerprint, head, nil
	}
	out, err := git(ctx, repo, []byte(diff+"\n"), "patch-id", "--stable")
	if err != nil {
		return "", "", err
	}
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return "", "", errors.New("git patch-id printed nothing")
	}
	return fields[0], head, nil
}

func git(ctx context.Context, repo string, stdin []byte, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// Mode says which caller is asking, for the one place they differ.
type Mode string

const (
	// ModeHandover is an agent handing work over: setting `unverified`, setting
	// `assumed`, or requesting a verify.
	ModeHandover Mode = "handover"
	// ModeBackstop is `task verify` itself. It skips a task with no plan items
	// at all: after this ships a task cannot reach `unverified` without items,
	// so the only ones it lets through were already there when it shipped.
	ModeBackstop Mode = "backstop"
)

// Result is the gate's verdict. Skipped, when set, says why the gate does not
// apply; otherwise Problems lists every condition that failed, and the gate
// passes when it is empty.
type Result struct {
	TaskID   int64    `json:"task_id"`
	Skipped  string   `json:"skipped,omitempty"`
	Problems []string `json:"problems"`
	Pass     bool     `json:"pass"`
}

// Check runs the gate on one task against the branch's current fingerprint.
//
// to is the status being moved to, or "" for none (the backstop). When the
// lifecycle refuses that move outright, the gate stands aside and passes, so
// the caller's refusal is the lifecycle's — the one that says what is wrong.
func Check(taskID int64, fingerprint string, mode Mode, to string) (Result, error) {
	db, err := monitor.DB()
	if err != nil {
		return Result{}, err
	}
	return check(db, taskID, fingerprint, mode, to)
}

func activeItems(items []gateItem) int {
	n := 0
	for _, it := range items {
		if it.state == planitem.StateActive {
			n++
		}
	}
	return n
}

type gateItem struct {
	seq               int64
	kind, origin      string
	state             string
	statement         string
	approvedStatement sql.NullString
	approvedKind      sql.NullString
	approvedAt        sql.NullString
	postLand          bool
	approvedPostLand  bool
}

func check(db *sql.DB, taskID int64, fingerprint string, mode Mode, to string) (Result, error) {
	res := Result{TaskID: taskID, Problems: []string{}}
	var typ, status string
	if err := db.QueryRow(
		`SELECT COALESCE(tt.slug, ''), t.status FROM live_tasks t
		   LEFT JOIN task_types tt ON tt.id = t.type_id WHERE t.id = ?`, taskID,
	).Scan(&typ, &status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return res, fmt.Errorf("no task E-%d", taskID)
		}
		return res, fmt.Errorf("look up E-%d: %w", taskID, err)
	}
	if to != "" {
		if tt, err := tasktype.Parse(typ); err == nil && !taskstatus.TransitionAllowed(status, to, tt) {
			res.Skipped = fmt.Sprintf("the lifecycle refuses %s → %s itself", status, to)
			res.Pass = true
			return res, nil
		}
	}
	items, err := readGateItems(db, taskID)
	if err != nil {
		return res, err
	}
	// A task with plan items is gated whatever its type; a todo or bugfix is
	// gated even without them, since it must have them.
	if !planitem.MustItemize(typ) && activeItems(items) == 0 {
		res.Skipped = fmt.Sprintf("the %s task has no plan items", typ)
		res.Pass = true
		return res, nil
	}
	if len(items) == 0 && mode == ModeBackstop {
		res.Skipped = "the task has no plan items; it reached unverified before itemized plans shipped"
		res.Pass = true
		return res, nil
	}
	fail := func(format string, a ...any) { res.Problems = append(res.Problems, fmt.Sprintf(format, a...)) }

	// The task has at least one active deliverable item.
	baselined := false
	deliverables := 0
	for _, it := range items {
		if it.approvedAt.Valid {
			baselined = true
		}
		if it.state == planitem.StateActive && it.kind == planitem.KindDeliverable {
			deliverables++
		}
	}
	if deliverables == 0 {
		fail("the task has no active deliverable plan item; add the items with `endless task plan add`")
	}

	// A hand-in needs every active planned item approved (E-2485), baselined or
	// not: items freeze from `unverified` on, so an approval skipped now could
	// not be given later. Grown items are the discrepancy check's below. A task
	// already frozen is not refused, since it could not comply; neither is the
	// backstop, so a task already at `unverified` verifies as it did.
	requireApproved := mode == ModeHandover && !planitem.IsFrozen(status)
	if requireApproved {
		var unapproved []string
		for _, it := range items {
			if it.state == planitem.StateActive && it.origin == planitem.OriginPlanned && !it.approvedAt.Valid {
				unapproved = append(unapproved, planitem.Label(it.seq))
			}
		}
		if len(unapproved) > 0 {
			fail("plan items %s are not approved; the user reads them (`endless task show E-%d --plan`) and approves them with `endless task plan approve E-%d %s`",
				strings.Join(unapproved, ", "), taskID, taskID, strings.Join(unapproved, " "))
		}
	}

	// The item set matches the approved baseline.
	if baselined {
		var differ []string
		for _, it := range items {
			label := planitem.Label(it.seq)
			switch {
			case it.state == planitem.StateWithdrawn && it.approvedAt.Valid:
				differ = append(differ, label+" withdrawn")
			case it.state != planitem.StateActive:
			case it.approvedAt.Valid && (it.statement != it.approvedStatement.String || it.kind != it.approvedKind.String ||
				it.postLand != it.approvedPostLand):
				differ = append(differ, label+" changed")
			case !it.approvedAt.Valid && it.origin == planitem.OriginPlanned && !requireApproved:
				differ = append(differ, label+" added")
			}
		}
		if len(differ) > 0 {
			fail("the plan differs from what the user approved (%s); the user re-approves with `endless task approve`", strings.Join(differ, ", "))
		}
	}

	// The latest reconciliation exists and is current.
	var recID int64
	var recFP, recDigest string
	err = db.QueryRow(
		`SELECT id, fingerprint, plan_digest FROM task_reconciliations
		  WHERE task_id = ? ORDER BY seq DESC LIMIT 1`, taskID,
	).Scan(&recID, &recFP, &recDigest)
	if errors.Is(err, sql.ErrNoRows) {
		fail("the task has not been reconciled; run `endless task reconcile E-%d`", taskID)
		res.Pass = len(res.Problems) == 0
		return res, nil
	}
	if err != nil {
		return res, fmt.Errorf("read reconciliation of E-%d: %w", taskID, err)
	}
	if recFP != fingerprint {
		fail("the code changed since the last reconciliation; reconcile again")
	}
	digest, err := monitor.PlanDigest(db, taskID)
	if err != nil {
		return res, err
	}
	if digest != recDigest {
		fail("the plan items changed or were re-approved since the last reconciliation; reconcile again")
	}

	// Every active item has exactly one entry, and every discrepancy is settled.
	type entry struct {
		verdict string
		qid     sql.NullInt64
		qstatus sql.NullString
	}
	entries := map[int64]entry{}
	// An item's newest post-land row, when it has one, is its current verdict
	// (E-2444), so it overrides the reconciliation's entry: post-land rows sort
	// after it and the map keeps the last.
	rows, err := db.Query(
		`SELECT item_seq, verdict, question_id, status FROM (
		     SELECT e.item_seq, e.verdict, e.question_id, q.status, 0 AS attempt
		       FROM task_reconciliation_entries e
		       LEFT JOIN task_questions q ON q.id = e.question_id
		      WHERE e.reconciliation_id = ?1
		     UNION ALL
		     SELECT p.item_seq, p.verdict, p.question_id, q.status, p.attempt
		       FROM task_post_land_entries p
		       LEFT JOIN task_questions q ON q.id = p.question_id
		      WHERE p.reconciliation_id = ?1)
		  ORDER BY attempt`, recID)
	if err != nil {
		return res, fmt.Errorf("read reconciliation entries: %w", err)
	}
	for rows.Next() {
		var seq int64
		var e entry
		if err := rows.Scan(&seq, &e.verdict, &e.qid, &e.qstatus); err != nil {
			rows.Close()
			return res, err
		}
		entries[seq] = e
	}
	rows.Close()

	var missing, open []string
	for _, it := range items {
		if it.state != planitem.StateActive {
			continue
		}
		label := planitem.Label(it.seq)
		e, ok := entries[it.seq]
		if !ok {
			missing = append(missing, label)
			continue
		}
		discrepancy := planitem.IsDiscrepancy(e.verdict) ||
			(it.origin == planitem.OriginGrown && !it.approvedAt.Valid)
		if !discrepancy {
			continue
		}
		if !e.qid.Valid || !e.qstatus.Valid || e.qstatus.String == questionstatus.Open {
			q := label
			if e.qid.Valid {
				q = fmt.Sprintf("%s (EQ-%d)", label, e.qid.Int64)
			}
			open = append(open, q)
		}
	}
	if len(missing) > 0 {
		fail("the last reconciliation has no entry for %s; reconcile again", strings.Join(missing, ", "))
	}
	if len(open) > 0 {
		fail("discrepancies are waiting for the user's answer: %s", strings.Join(open, ", "))
	}
	res.Pass = len(res.Problems) == 0
	return res, nil
}

func readGateItems(db *sql.DB, taskID int64) ([]gateItem, error) {
	rows, err := db.Query(
		`SELECT seq, kind, origin, state, statement, approved_statement, approved_kind,
		        approved_at, post_land, COALESCE(approved_post_land, 0)
		   FROM task_plan_items WHERE task_id = ? ORDER BY sort_order, seq`, taskID)
	if err != nil {
		return nil, fmt.Errorf("read plan items of E-%d: %w", taskID, err)
	}
	defer rows.Close()
	var out []gateItem
	for rows.Next() {
		var it gateItem
		if err := rows.Scan(&it.seq, &it.kind, &it.origin, &it.state, &it.statement,
			&it.approvedStatement, &it.approvedKind, &it.approvedAt, &it.postLand, &it.approvedPostLand); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// Package outagejob is the outage-check job (E-2589): once a minute it reads
// the status page of each provider with an open outage, and closes the outage
// when the provider reports the component operational again.
//
// It is the only thing that polls a status page. Monitors and hooks read the
// outages table; they never make a request of their own. With no outage open
// the job reads that table and makes no request at all, so a machine with no
// outage pays one local query a minute.
package outagejob

import (
	"context"
	"fmt"
	"strings"

	"github.com/mikeschinkel/endless/internal/jobs"
	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/outages"
)

// JobName is the stable scheduling key.
const JobName = "outage-check"

type job struct{}

func init() {
	jobs.Register(job{})
}

// Name returns the stable scheduling key.
func (job) Name() string { return JobName }

// Schedule polls every outages.PollInterval. No backoff: a run with nothing
// open is one local read, and a run with an outage open must keep polling at
// the cadence the status line promises.
func (job) Schedule() jobs.Schedule {
	return jobs.Schedule{Interval: outages.PollInterval}
}

// Run checks every open outage.
func (job) Run(ctx context.Context) (err error) {
	// A sandbox database is a throwaway copy; polling for it would spend real
	// requests on outages no person is shown.
	if monitor.IsSandboxActive() {
		jobs.Note(ctx, "skipped: the runner is on a worktree sandbox database")
		return nil
	}
	note, err := check(ctx)
	jobs.Note(ctx, note)
	return err
}

// check reads each open outage's status page once and acts on it, returning
// the run's note. A status page that cannot be read leaves its outage open for
// the next run: a status page down during an outage is plausibly part of it,
// and is not a failure of this job.
func check(ctx context.Context) (note string, err error) {
	var open []outages.Outage
	var notes []string

	open, err = outages.List(false)
	if err != nil {
		return "", fmt.Errorf("list open outages: %w", err)
	}
	if len(open) == 0 {
		return "no outage open", nil
	}
	for _, o := range open {
		p, ok := outages.Lookup(o.Provider)
		if !ok {
			// A provider this binary does not know — a newer binary opened it.
			notes = append(notes, o.Provider+": unknown provider, left open")
			continue
		}
		status, rerr := outages.Read(ctx, p)
		switch {
		case rerr != nil:
			notes = append(notes, p.Name+": "+rerr.Error()+"; left open")
		case status == outages.Operational:
			if _, cerr := outages.Close(p); cerr != nil {
				return strings.Join(notes, "; "), fmt.Errorf("close %s outage: %w", p.Name, cerr)
			}
			notes = append(notes, p.Name+": operational, closed")
		default:
			if uerr := outages.Refresh(p, status); uerr != nil {
				return strings.Join(notes, "; "), fmt.Errorf("refresh %s outage: %w", p.Name, uerr)
			}
			notes = append(notes, p.Name+": "+status)
		}
	}
	return strings.Join(notes, "; "), nil
}

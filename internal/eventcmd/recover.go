package eventcmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/mikeschinkel/endless/internal/dbrecover"
	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/refusal"
	"github.com/mikeschinkel/endless/internal/schema"
)

// runRecover is `endless db recover` (E-2397): return the database to the
// backup the unwanted migration took just before it applied. It prints the
// plan as JSON for the Python CLI to render; refusals go out through the relay
// funnel with the class dbrecover chose.
//
// Like `event upgrade` it never connects through monitor.DB() — the database
// it exists for is one DB() refuses — and keeps only DB()'s answer to WHICH
// database: the E-1429 gate and ED-1601's refusal of a worktree build aimed at
// main (monitor.UpgradeTarget).
func runRecover(args []string) {
	fs := refusal.NewFlags("recover")
	backup := fs.String("backup", "", "Restore this backup (a path, or a file in the backups directory) instead of the one linked to the migration")
	dryRun := fs.Bool("dry-run", false, "Report what recovery would do; change nothing")
	force := fs.Bool("force", false, "Restore even though processes still hold the database open")
	wait := fs.Duration("wait", 30*time.Second, "How long to wait for open handles to close")
	parseFlags(fs, "event recover", args)

	fail := func(err error) { exitRelay("event recover", "endless-go event recover: ", err) }

	path, err := monitor.UpgradeTarget()
	if err != nil {
		fail(err)
	}
	if _, err = os.Stat(path); err != nil {
		fail(refusal.NoReport(fmt.Sprintf("no database at %s: nothing to recover", path),
			"Check which database --db names, and retry"))
	}
	latest, err := schema.LatestVersion()
	if err != nil {
		fail(err)
	}
	plan, err := dbrecover.Recover(context.Background(), dbrecover.Options{
		DBPath:        path,
		BackupsDir:    monitor.BackupsDir(),
		ParkDir:       monitor.PreRestoreDir(),
		BinaryVersion: latest,
		Backup:        *backup,
		Wait:          *wait,
		Poll:          250 * time.Millisecond,
		Force:         *force,
		DryRun:        *dryRun,
		Stamp:         time.Now().Format("20060102-150405"),
		Command:       "endless db recover",
	})
	if err != nil {
		fail(err)
	}
	b, _ := json.Marshal(plan)
	fmt.Println(string(b))
}

// runSwap is `endless db restore`'s install (E-2397): under the maintenance
// lock, put an already-chosen backup in place atomically and verify it. The
// Python command keeps the choosing, the vetting and the holder policy.
func runSwap(args []string) {
	fs := refusal.NewFlags("swap")
	backup := fs.String("backup", "", "The backup to put in place")
	stamp := fs.String("stamp", "", "The timestamp the parked copy is named with")
	parseFlags(fs, "event swap", args)

	fail := func(err error) { exitRelay("event swap", "endless-go event swap: ", err) }
	if *backup == "" || *stamp == "" {
		refusal.NoReport("endless-go event swap: --backup and --stamp are required",
			"Pass both and retry").Command("event swap").Exit(2)
	}
	path, err := monitor.UpgradeTarget()
	if err != nil {
		fail(err)
	}
	res, err := dbrecover.Swap(context.Background(), dbrecover.SwapOptions{
		DBPath:  path,
		Backup:  *backup,
		ParkDir: monitor.PreRestoreDir(),
		Stamp:   *stamp,
		Command: "endless db restore",
	})
	if err != nil {
		fail(err)
	}
	b, _ := json.Marshal(res)
	fmt.Println(string(b))
}

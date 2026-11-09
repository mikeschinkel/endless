package dbrecover

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/mikeschinkel/endless/internal/dbmaint"
	"github.com/mikeschinkel/endless/internal/refusal"
)

// Options is one recovery: which database, how far the binary reaches, and
// what the caller asked for.
type Options struct {
	DBPath        string
	BackupsDir    string
	ParkDir       string
	BinaryVersion int64
	// Backup names a backup outright — a path, or a file in BackupsDir —
	// instead of the one linked to the migration. Empty: use the link.
	Backup string
	Wait   time.Duration
	Poll   time.Duration
	Force  bool
	DryRun bool
	Stamp  string
	// Command labels the lock, so an opener's refusal says who holds it.
	Command string
}

// Plan is what recovery found and, unless it was a dry run, did.
type Plan struct {
	// Status is "current" (nothing to recover), "planned" (dry run) or
	// "recovered".
	Status        string        `json:"status"`
	DB            string        `json:"db"`
	DBVersion     int64         `json:"db_version"`
	BinaryVersion int64         `json:"binary_version"`
	Backup        string        `json:"backup,omitempty"`
	BackupVersion int64         `json:"backup_version,omitempty"`
	Link          *dbmaint.Link `json:"link,omitempty"`
	Land          *LandAdvice   `json:"land,omitempty"`
	Holders       []Holder      `json:"holders"`
	// HoldersUnknown: neither lsof nor /proc could be asked.
	HoldersUnknown bool     `json:"holders_unknown,omitempty"`
	Parked         []string `json:"parked,omitempty"`
	Check          *Check   `json:"check,omitempty"`
}

// LandAdvice is the cheaper fix, when there is one: the branch whose code took
// the database forward, and whether it can land. Landing rolls forward — the
// installed binary catches up to the database and nothing written since the
// migration is lost — where a restore throws those writes away.
type LandAdvice struct {
	Task     string `json:"task"`
	Worktree string `json:"worktree"`
	Status   string `json:"status"`
	Landable bool   `json:"landable"`
	Command  string `json:"command"`
}

// Recover returns the database to the backup the unwanted migration took just
// before it applied (E-2397). See the package doc for the install; the steps
// around it are:
//
//  1. Detect: the database's schema version is ahead of this binary. If not,
//     there is nothing to recover and nothing is changed.
//  2. Pick the backup by its link to the migration, never "newest".
//  3. Say whether landing the migrating branch is the cheaper fix.
//  4. Take the maintenance lock, wait for open handles to close, name any
//     that remain; install; verify; release the lock on every exit path.
//
// It never exits the process: every refusal is returned, after the lock is
// released, for the caller to print.
func Recover(ctx context.Context, opts Options) (plan Plan, err error) {
	var check Check
	var release func() error

	plan = Plan{DB: opts.DBPath, BinaryVersion: opts.BinaryVersion, Holders: []Holder{}}

	plan.DBVersion, err = Version(ctx, opts.DBPath)
	if err != nil {
		err = refusal.Fault(fmt.Errorf("reading the schema version of %s: %w", opts.DBPath, err))
		goto end
	}
	if plan.DBVersion <= opts.BinaryVersion {
		plan.Status = "current"
		goto end
	}

	plan.Backup, plan.Link, err = pickBackup(opts, plan.DBVersion)
	if err != nil {
		goto end
	}
	plan.BackupVersion, err = vetBackup(ctx, plan.Backup, opts.BinaryVersion)
	if err != nil {
		goto end
	}
	plan.Land = landAdvice(ctx, opts.DBPath, plan.Link)

	plan.Holders, err = Holders(ctx, FileSet(opts.DBPath))
	if errors.Is(err, ErrHoldersUnknown) {
		plan.HoldersUnknown, err = true, nil
	}
	if err != nil {
		goto end
	}
	if opts.DryRun {
		plan.Status = "planned"
		goto end
	}

	release, err = lockForMaintenance(opts.DBPath, opts.Command)
	if err != nil {
		goto end
	}
	defer func() { _ = release() }()

	plan.Holders, err = WaitForHolders(ctx, FileSet(opts.DBPath), opts.Wait, opts.Poll)
	if errors.Is(err, ErrHoldersUnknown) {
		plan.HoldersUnknown, err = true, nil
	}
	if err != nil {
		goto end
	}
	if (plan.HoldersUnknown || len(plan.Holders) > 0) && !opts.Force {
		err = holdersRefusal(plan, opts.Wait)
		goto end
	}

	plan.Parked, check, err = swap(ctx, opts.DBPath, plan.Backup, opts.ParkDir, opts.Stamp)
	plan.Check = &check
	if err != nil {
		goto end
	}
	plan.Status = "recovered"

end:
	if plan.Holders == nil {
		plan.Holders = []Holder{}
	}
	return plan, err
}

// SwapOptions is one `db restore`: the database, the backup the caller already
// chose and vetted, and where the replaced file is parked.
type SwapOptions struct {
	DBPath  string
	Backup  string
	ParkDir string
	Stamp   string
	Command string
}

// SwapResult is what Swap did.
type SwapResult struct {
	Parked []string `json:"parked"`
	Check  Check    `json:"check"`
}

// Swap is `db restore`'s half of the shared path: take the maintenance lock,
// install the backup atomically, verify it against the backup, release. The
// holder policy — refuse unless --force — stays with `db restore`, which has
// already applied it.
func Swap(ctx context.Context, opts SwapOptions) (res SwapResult, err error) {
	release, err := lockForMaintenance(opts.DBPath, opts.Command)
	if err != nil {
		return res, err
	}
	defer func() { _ = release() }()
	res.Parked, res.Check, err = swap(ctx, opts.DBPath, opts.Backup, opts.ParkDir, opts.Stamp)
	if res.Parked == nil {
		res.Parked = []string{}
	}
	return res, err
}

// swap installs and verifies, and turns either failing into a REPORT refusal
// that names the parked copy: rolling back by hand is then the user's move.
func swap(ctx context.Context, dbPath, backup, parkDir, stamp string) (parked []string, check Check, err error) {
	var installed InstallResult
	var problems []string

	installed, err = Install(backup, dbPath, parkDir, stamp)
	parked = installed.Parked
	if err != nil {
		err = refusal.Report(
			fmt.Sprintf("The restore failed partway: %v. The backup was NOT put in place.", err),
			"whether to retry, or to put the parked database back by hand",
		).Cause(err)
		return parked, check, err
	}

	check, err = Verify(ctx, dbPath, backup)
	if err == nil {
		problems = check.Problems()
	}
	if err != nil || len(problems) > 0 {
		if err != nil {
			problems = append(problems, err.Error())
		}
		undo := "No pre-restore database was kept: the target did not exist."
		if installed.ParkedDB() != "" {
			undo = fmt.Sprintf("The pre-restore database is at %s; copy it back to %s to undo this.",
				installed.ParkedDB(), dbPath)
		}
		err = refusal.Report(
			fmt.Sprintf("The restore completed but %s does not match its backup.", dbPath),
			"whether to keep the unverified restore or roll back to the parked copy",
		).Detail("  " + strings.Join(problems, "\n  ") + "\n" + undo)
	}
	return parked, check, err
}

// lockForMaintenance takes the lock and arranges for it to be released if the
// process is interrupted: SIGINT, SIGTERM or SIGHUP release it and exit, so a
// Ctrl-C mid-recover does not leave every opener refusing until the stale-pid
// rule notices. The returned release also stops that handler.
func lockForMaintenance(dbPath, command string) (release func() error, err error) {
	unlock, err := dbmaint.Acquire(dbPath, command)
	if err != nil {
		if errors.Is(err, dbmaint.ErrHeld) {
			err = refusal.NoReport(err.Error()+"; nothing was changed.",
				"Wait for that command to finish, then re-run").Cause(err)
		}
		return nil, err
	}
	sigs := make(chan os.Signal, 1)
	done := make(chan struct{})
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		select {
		case sig := <-sigs:
			_ = unlock()
			code := 1
			if s, ok := sig.(syscall.Signal); ok {
				code = 128 + int(s)
			}
			os.Exit(code)
		case <-done:
		}
	}()
	release = func() error {
		signal.Stop(sigs)
		close(done)
		return unlock()
	}
	return release, nil
}

// pickBackup resolves the backup: the one the caller named, or the one linked
// to the migration that took the database to dbVersion.
func pickBackup(opts Options, dbVersion int64) (backup string, link *dbmaint.Link, err error) {
	links, err := dbmaint.ReadLinks(opts.BackupsDir)
	if err != nil {
		return "", nil, refusal.Fault(fmt.Errorf("reading %s: %w",
			filepath.Join(opts.BackupsDir, dbmaint.LinksFile), err))
	}

	if opts.Backup != "" {
		backup = opts.Backup
		if _, statErr := os.Stat(backup); statErr != nil {
			backup = filepath.Join(opts.BackupsDir, opts.Backup)
		}
		if _, statErr := os.Stat(backup); statErr != nil {
			return "", nil, refusal.Report(
				fmt.Sprintf("No backup named %q exists, as a path or in %s. Nothing was changed.",
					opts.Backup, opts.BackupsDir),
				"which backup to restore from")
		}
		for i := len(links) - 1; i >= 0; i-- {
			if links[i].Backup == filepath.Base(backup) {
				l := links[i]
				link = &l
				break
			}
		}
		return backup, link, nil
	}

	found, ok := dbmaint.LinkFor(links, dbVersion, opts.BinaryVersion)
	if !ok {
		return "", nil, refusal.Report(
			fmt.Sprintf("No backup is linked to a migration that took %s from version %d or "+
				"below to version %d, so there is no backup recovery can vouch for. "+
				"Nothing was changed.", opts.DBPath, opts.BinaryVersion, dbVersion),
			"which backup to restore — the newest one may already carry the migration",
		).HumanRemedy("Pass --backup <file> to restore a backup you choose from " + opts.BackupsDir + ".")
	}
	backup = filepath.Join(opts.BackupsDir, found.Backup)
	if _, statErr := os.Stat(backup); statErr != nil {
		return "", nil, refusal.Report(
			fmt.Sprintf("The backup linked to the migration from version %d to %d, %s, "+
				"is gone. Nothing was changed.", found.From, found.To, backup),
			"which other backup, if any, to restore",
		).HumanRemedy("Pass --backup <file> to restore a backup you choose from " + opts.BackupsDir + ".")
	}
	return backup, &found, nil
}

// vetBackup proves the backup is a sound database this binary can use, before
// anything is changed: integrity ok, and a version the binary carries.
func vetBackup(ctx context.Context, backup string, binaryVersion int64) (version int64, err error) {
	db, err := OpenReadOnly(backup)
	if err != nil {
		return 0, refusal.Report(
			fmt.Sprintf("The backup %s cannot be opened: %v. Nothing was changed.", backup, err),
			"which other backup, if any, to restore")
	}
	defer func() { _ = db.Close() }()
	integrity, err := Integrity(ctx, db)
	if err == nil && integrity != "ok" {
		err = fmt.Errorf("integrity_check: %s", integrity)
	}
	if err == nil {
		version, err = Version(ctx, backup)
	}
	if err != nil {
		return 0, refusal.Report(
			fmt.Sprintf("The backup %s is not usable: %v. Nothing was changed.", backup, err),
			"which other backup, if any, to restore")
	}
	if version > binaryVersion {
		return version, refusal.Report(
			fmt.Sprintf("The backup %s is at schema version %d, still ahead of this endless "+
				"(version %d): restoring it would not recover anything. Nothing was changed.",
				backup, version, binaryVersion),
			"which older backup, if any, to restore")
	}
	return version, nil
}

// worktreeDirRE finds a task worktree in a path: <root>/.endless/worktrees/e-<n>.
var worktreeDirRE = regexp.MustCompile(`^(.*/\.endless/worktrees/(e-\d+))(/|$)`)

// landAdvice names the branch whose code migrated the database, when the link
// says where it ran, and whether that branch can land. A task lands only from
// `unlanded` — a verify passed at its current code — so that is "landable".
func landAdvice(ctx context.Context, dbPath string, link *dbmaint.Link) *LandAdvice {
	if link == nil {
		return nil
	}
	var worktree, dir string
	for _, candidate := range []string{link.Binary, link.Cwd} {
		if m := worktreeDirRE.FindStringSubmatch(candidate); m != nil {
			worktree, dir = m[1], m[2]
			break
		}
	}
	if worktree == "" {
		return nil
	}
	if info, err := os.Stat(worktree); err != nil || !info.IsDir() {
		return nil
	}
	task := strings.ToUpper(dir)
	advice := &LandAdvice{
		Task:     task,
		Worktree: worktree,
		Command:  "endless worktree land " + task,
	}
	advice.Status = taskStatus(ctx, dbPath, strings.TrimPrefix(dir, "e-"))
	advice.Landable = advice.Status == "unlanded"
	return advice
}

// taskStatus reads one task's status read-only, or "" if it cannot.
func taskStatus(ctx context.Context, dbPath, id string) (status string) {
	db, err := OpenReadOnly(dbPath)
	if err != nil {
		return ""
	}
	defer func() { _ = db.Close() }()
	_ = db.QueryRowContext(ctx, "SELECT status FROM tasks WHERE id = ?", id).Scan(&status)
	return status
}

// holdersRefusal is the refusal for handles that outlived the wait.
func holdersRefusal(plan Plan, wait time.Duration) error {
	why := fmt.Sprintf("%d process(es) still have the database open after %s",
		len(plan.Holders), wait)
	if plan.HoldersUnknown {
		why = "could not determine what has the database open (no lsof and no /proc)"
	}
	var lines []string
	for _, h := range plan.Holders {
		lines = append(lines, fmt.Sprintf("  pid %-7d %s", h.PID, h.Command))
	}
	return refusal.Report(
		fmt.Sprintf("Refusing to recover: %s. Nothing was changed, and the maintenance lock "+
			"was released.", why),
		"whether to stop those processes, or to restore over an open database",
	).Detail(strings.Join(lines, "\n")).
		HumanRemedy("Stop them and re-run, or pass --force to restore anyway (they keep the " +
			"replaced file until they reopen).")
}

package liveview

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mikeschinkel/endless/internal/faults"
)

// restartFixture is a restarter watching a symlink in a temp dir, with every
// seam recorded rather than acted on.
type restartFixture struct {
	t        *testing.T
	dir      string
	link     string
	r        *restarter
	probes   []string
	execs    []string
	faults   []faults.Fault
	probeErr error
	execErr  error
	before   int
	after    int
}

func newRestartFixture(t *testing.T) *restartFixture {
	t.Helper()
	f := &restartFixture{t: t, dir: t.TempDir()}
	f.link = filepath.Join(f.dir, "bin")
	f.write("v1", "one")
	f.point("v1")

	start, err := identityOf(f.link)
	if err != nil {
		t.Fatalf("identityOf: %v", err)
	}
	f.r = &restarter{
		path:     f.link,
		start:    start,
		identify: identityOf,
		probe: func(path string) error {
			f.probes = append(f.probes, path)
			return f.probeErr
		},
		exec: func(path string, argv, env []string) error {
			f.execs = append(f.execs, path)
			return f.execErr
		},
		record:          func(flt faults.Fault) { f.faults = append(f.faults, flt) },
		beforeExec:      func() { f.before++ },
		afterFailedExec: func() { f.after++ },
	}
	return f
}

// write creates (or replaces, by rename, as `go build` does) a file in dir.
func (f *restartFixture) write(name, content string) {
	f.t.Helper()
	tmp := filepath.Join(f.dir, name+".tmp")
	if err := os.WriteFile(tmp, []byte(content), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.Rename(tmp, filepath.Join(f.dir, name)); err != nil {
		f.t.Fatal(err)
	}
}

// point repoints the watched symlink at name, as `ln -sfn` does.
func (f *restartFixture) point(name string) {
	f.t.Helper()
	tmp := f.link + ".tmp"
	_ = os.Remove(tmp)
	if err := os.Symlink(filepath.Join(f.dir, name), tmp); err != nil {
		f.t.Fatal(err)
	}
	if err := os.Rename(tmp, f.link); err != nil {
		f.t.Fatal(err)
	}
}

// TestRestarter_IdentityRule pins when a replacement counts: changed from the
// start identity AND the same for two consecutive ticks.
func TestRestarter_IdentityRule(t *testing.T) {
	t.Run("unchanged never restarts", func(t *testing.T) {
		f := newRestartFixture(t)
		for range 5 {
			if !f.r.tick(false) {
				t.Fatal("an unchanged binary stopped job firing")
			}
		}
		if len(f.probes) != 0 {
			t.Fatalf("an unchanged binary was probed: %v", f.probes)
		}
	})

	t.Run("changed once is not yet a replacement", func(t *testing.T) {
		f := newRestartFixture(t)
		f.write("v1", "one, rewritten")
		if !f.r.tick(false) {
			t.Fatal("the first sighting of a change stopped job firing")
		}
		if len(f.probes) != 0 {
			t.Fatal("a change seen once was probed; it may be a half-written build")
		}
	})

	t.Run("still changing is not yet a replacement", func(t *testing.T) {
		f := newRestartFixture(t)
		f.write("v1", "one, partly")
		f.r.tick(false)
		f.write("v1", "one, partly more")
		f.r.tick(false)
		if len(f.probes) != 0 {
			t.Fatal("a file that changed between ticks was probed")
		}
		f.r.tick(false)
		if len(f.probes) != 1 {
			t.Fatalf("a file that then held still was probed %d times, want 1", len(f.probes))
		}
	})

	t.Run("changed and stable restarts", func(t *testing.T) {
		f := newRestartFixture(t)
		f.write("v1", "one, rewritten")
		f.r.tick(false)
		if f.r.tick(false) {
			t.Error("jobs fired on the tick that decided to exec")
		}
		if len(f.execs) != 1 {
			t.Fatalf("exec called %d times, want 1", len(f.execs))
		}
	})

	t.Run("symlink repointed with the old target untouched", func(t *testing.T) {
		f := newRestartFixture(t)
		f.write("v2", "two")
		f.point("v2")
		f.r.tick(false)
		f.r.tick(false)
		if len(f.execs) != 1 {
			t.Fatalf("a repointed symlink did not restart (execs=%d)", len(f.execs))
		}
		// The watched path, not v2: the new process must watch the symlink too,
		// or the next repoint goes unseen.
		if f.execs[0] != f.link {
			t.Errorf("exec'd %q, want the watched symlink %q", f.execs[0], f.link)
		}
		if f.probes[0] != f.link {
			t.Errorf("probed %q, want %q", f.probes[0], f.link)
		}
	})

	t.Run("a missing path mid-install is neither change nor failure", func(t *testing.T) {
		f := newRestartFixture(t)
		if err := os.Remove(f.link); err != nil {
			t.Fatal(err)
		}
		for range 3 {
			if !f.r.tick(false) {
				t.Fatal("a missing binary stopped job firing")
			}
		}
		if len(f.probes)+len(f.faults) != 0 {
			t.Fatal("a missing binary was probed or reported")
		}
	})
}

// TestRestarter_SuccessfulRestart pins the exec path: the terminal is handed
// back first and the exec gets the ORIGINAL argv.
func TestRestarter_SuccessfulRestart(t *testing.T) {
	f := newRestartFixture(t)
	var gotArgv []string
	f.r.exec = func(path string, argv, env []string) error {
		f.execs = append(f.execs, path)
		gotArgv = argv
		return nil
	}
	f.write("v1", "one, rewritten")
	f.r.tick(false)
	f.r.tick(false)

	if f.before != 1 {
		t.Errorf("beforeExec ran %d times, want 1", f.before)
	}
	if strings.Join(gotArgv, " ") != strings.Join(startArgs, " ") {
		t.Errorf("exec argv = %q, want the process's original %q", gotArgv, startArgs)
	}
	if len(f.faults) != 0 || f.r.Notice() != "" || !f.r.jobsAllowed() {
		t.Error("a successful exec left a failure behind")
	}
}

// TestRestarter_JobInFlightDefersTheExec pins that an exec never lands on top
// of a running job — it would end the job mid-write with its lease held.
func TestRestarter_JobInFlightDefersTheExec(t *testing.T) {
	f := newRestartFixture(t)
	f.write("v1", "one, rewritten")
	f.r.tick(true)
	if f.r.tick(true) {
		t.Error("a new job fired while a restart was waiting for the last one")
	}
	if len(f.execs) != 0 {
		t.Fatal("exec'd while a job was in flight")
	}
	f.r.tick(false)
	if len(f.execs) != 1 {
		t.Fatalf("exec did not follow once the job finished (execs=%d)", len(f.execs))
	}
}

// TestRestarter_FailedProbe pins the failure contract: jobs stop for good, the
// frame says why, and one fault is recorded however many ticks follow.
func TestRestarter_FailedProbe(t *testing.T) {
	f := newRestartFixture(t)
	f.probeErr = errors.New("exit status 3: refusing to start")
	f.write("v1", "one, broken")
	f.r.tick(false)
	f.r.tick(false)

	if len(f.execs) != 0 {
		t.Fatal("a binary that failed its probe was exec'd")
	}
	for range 5 {
		if f.r.tick(false) {
			t.Fatal("jobs fired after a failed restart")
		}
	}
	notice := f.r.Notice()
	for _, want := range []string{"probe", "refusing to start", "restart this monitor"} {
		if !strings.Contains(notice, want) {
			t.Errorf("notice %q does not mention %q", notice, want)
		}
	}
	if len(f.faults) != 1 {
		t.Fatalf("recorded %d faults, want exactly 1", len(f.faults))
	}
	if f.faults[0].Code.ID != "WARN-0019" {
		t.Errorf("fault code = %s, want WARN-0019", f.faults[0].Code.ID)
	}
	if len(f.probes) != 1 {
		t.Errorf("the same broken replacement was probed %d times, want 1", len(f.probes))
	}

	// The next build is usually the fix: a LATER replacement is tried afresh.
	f.probeErr = nil
	f.write("v1", "one, fixed")
	f.r.tick(false)
	f.r.tick(false)
	if len(f.execs) != 1 {
		t.Fatalf("a later good replacement was not exec'd (execs=%d)", len(f.execs))
	}
}

// TestRestarter_FailedExec pins that an exec error takes the terminal back and
// is reported under its own stage.
func TestRestarter_FailedExec(t *testing.T) {
	f := newRestartFixture(t)
	f.execErr = syscall.ENOEXEC
	f.write("v1", "one, rewritten")
	f.r.tick(false)
	f.r.tick(false)

	if f.before != 1 || f.after != 1 {
		t.Errorf("beforeExec/afterFailedExec = %d/%d, want 1/1", f.before, f.after)
	}
	if f.r.jobsAllowed() {
		t.Error("jobs still allowed after a failed exec")
	}
	if len(f.faults) != 1 || f.faults[0].Fields["stage"] != "exec" {
		t.Errorf("faults = %+v, want one at stage exec", f.faults)
	}
}

// TestRestarter_NilIsInert pins the no-watch fallback: a loop that cannot find
// its own binary behaves exactly as it did before E-2193.
func TestRestarter_NilIsInert(t *testing.T) {
	var r *restarter
	if !r.tick(false) || r.Notice() != "" || !r.jobsAllowed() {
		t.Fatal("a nil restarter changed the loop's behaviour")
	}
}

// TestLoopReexecsIntoReplacedBinary runs a real Loop in a real process, started
// through a symlink, and swaps what the symlink points at:
//
//   - onto a good build — the same PID must come back running it;
//   - then onto a build whose probe fails — the process must stay on the good
//     build and say so in its frame.
//
// The second swap only works if the first exec re-ran the SYMLINK: a process
// exec'd through the resolved path would be watching the wrong file. And the
// helper strips its trailing args before looping, as main strips --db, so
// "extra=true" after the exec proves the ORIGINAL command line was re-run.
func TestLoopReexecsIntoReplacedBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs a helper binary")
	}
	dir := t.TempDir()
	build := func(name string, ldflags string) string {
		t.Helper()
		out := filepath.Join(dir, name)
		cmd := exec.Command("go", "build", "-ldflags", ldflags, "-o", out, "./testdata/reexecprobe")
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("building %s: %v\n%s", name, err, b)
		}
		return out
	}
	a := build("a", "-X main.build=A")
	b := build("b", "-X main.build=B")
	c := build("c", "-X main.build=C -X main.probeFails=yes")

	link := filepath.Join(dir, "monitor")
	repoint := func(target string) {
		t.Helper()
		tmp := link + ".tmp"
		_ = os.Remove(tmp)
		if err := os.Symlink(target, tmp); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, link); err != nil {
			t.Fatal(err)
		}
	}
	repoint(a)

	marker := filepath.Join(dir, "marker")
	outPath := filepath.Join(dir, "frames")
	outFile, err := os.Create(outPath)
	if err != nil {
		t.Fatal(err)
	}
	defer outFile.Close()
	proc := exec.Command(link, marker, "--db", "main")
	proc.Stdout = outFile
	proc.Stderr = outFile
	if err := proc.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = proc.Process.Signal(syscall.SIGTERM)
		_ = proc.Wait()
	}()
	pid := proc.Process.Pid

	waitFor := func(what string, ok func() bool) {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			if ok() {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		frames, _ := os.ReadFile(outPath)
		t.Fatalf("timed out waiting for %s; output:\n%s", what, frames)
	}
	markerIs := func(want string) func() bool {
		return func() bool {
			got, _ := os.ReadFile(marker)
			return strings.TrimSpace(string(got)) == want+" "+strconv.Itoa(pid)+" extra=true"
		}
	}

	waitFor("build A to render", markerIs("A"))
	repoint(b)
	waitFor("the same PID to re-exec into build B", markerIs("B"))

	repoint(c)
	waitFor("the failed-restart notice", func() bool {
		frames, _ := os.ReadFile(outPath)
		return strings.Contains(string(frames), "could not restart")
	})
	if !markerIs("B")() {
		got, _ := os.ReadFile(marker)
		t.Fatalf("after a failed probe the process is no longer build B at pid %d: %q", pid, got)
	}
}

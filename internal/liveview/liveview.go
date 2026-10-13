// Package liveview holds the machinery a live terminal dashboard needs and its
// one-shot twin does not: the redraw loop, the flicker-free repaint, the tmux
// self-fit, and the width/color detection both share.
//
// Extracted from internal/sessionstatuscmd by E-1976, which added a second
// dashboard — `endless project monitor` — that needs every part of it.
// The extraction is verbatim: sessionstatuscmd keeps one-line wrappers over
// these functions, so its own tests still exercise the same code under the same
// names.
//
// What is deliberately NOT here: anything that knows what a row is. This
// package takes a Frame func and paints whatever it writes. Ranking, glyphs,
// legends and column widths belong to each view.
package liveview

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/mattn/go-runewidth"
	"golang.org/x/term"

	"github.com/mikeschinkel/endless/internal/jobs"
	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/refusal"
)

// Interval is the redraw cadence for every live view, inherited from the bash
// prototype's watch loop. One number, shared, so two dashboards open side by
// side repaint in step instead of beating against each other.
const Interval = 2 * time.Second

// Self-sizing constants for a monitor's own tmux pane (E-1851). A live view is
// one pane of a split, above a bare shell; it owns exactly as many rows as its
// frame needs so the shell keeps the rest of the column. Because the frame grows
// and shrinks with the row set, the fit is re-applied on every repaint rather
// than fixed at spawn time — which also means whatever built the layout never
// has to guess a height it cannot know.
const (
	// PaneSlack is the spare row kept below the frame so the last row isn't
	// flush against the pane border.
	PaneSlack = 1
	// PaneMinHeight is the absolute floor for a real frame — a pane thinner than
	// legend+slack is not worth having.
	PaneMinHeight = 2
	// PaneEmptyHeight is the height held while there are no rows at all (the
	// view's own nothing-here hint). Exact-fitting the hint gives a 2-row sliver
	// that reads as a broken pane; holding a modest block reads as an empty
	// monitor with room to grow. Costs the shell pane a few rows in the empty
	// case only.
	PaneEmptyHeight = 8
	// PanePctOfWindow caps the fit as a percentage of the window height, so an
	// unusually long row set can't swallow the shell pane below it. A frame
	// taller than the cap simply scrolls inside its pane.
	PanePctOfWindow = 80
)

// ANSI helpers. Intensity only — bold and dim (SGR 1/2) — so a frame reads on
// any theme without color-profile guessing. Views that need a fixed color
// (the fault row's severity chip) reach for 256-color indices themselves;
// the 30-47 range is remapped by the user's theme and is never safe here.
const (
	Reset  = "\x1b[0m"
	Bold   = "\x1b[1m"
	DimSGR = "\x1b[2m"
)

// SafetyNet is how often a visibility-gated view asks for itself whether it can
// be seen (E-2229), inside the 15–30s range ED-1606 set. The tmux hooks are the
// primary signal; this is what corrects a missed one, and what keeps the hooks
// installed.
const SafetyNet = 20 * time.Second

// Visibility is what gates a live view on being seen (E-2229). A view nobody
// can see does no work at all: no frame, no query, no git, no job trigger. A
// change of visibility arrives as a signal — SIGUSR1 "you are visible",
// SIGUSR2 "you are hidden" — sent by whatever watches the screen;
// internal/monitorvis implements it for tmux.
type Visibility interface {
	// Start begins being findable by whatever sends the signals, and reports
	// the visibility now. Loop calls it after it is already listening.
	Start() (visible bool)
	// Check is the safety-net poll, run every SafetyNet.
	Check() (visible bool)
	// Missed says a poll disagreed with the last signalled state.
	Missed(visible bool)
	// Close stops being findable. Called on every clean exit, before Fatal.
	Close()
}

// Frame renders one frame of a live view into w and returns the number of
// CONTENT rows it drew. The count is the pane fit's input, and 0 has a specific
// meaning: the view drew its nothing-here hint rather than a short real frame
// (see PaneHeightForFrame).
//
// It returns an error only for conditions that should stop the loop outright —
// a failed query, not an empty result.
type Frame func(w io.Writer, cols int, color bool) (rows int, err error)

// LoopConfig is everything Loop needs that isn't the frame itself.
type LoopConfig struct {
	// Render draws one frame. Called once per tick.
	Render Frame
	// ColsOverride pins the terminal width; 0 re-detects it every tick, which is
	// what honors a resize mid-run.
	ColsOverride int
	// FallbackCols is the width used when detection finds nothing (output not a
	// tty, no $COLUMNS).
	FallbackCols int
	// Color enables the intensity escapes. Resolved once by the caller, because
	// the color decision is about the destination, and the destination does not
	// change under the loop.
	Color bool
	// Pane is the tmux pane the view should fit itself to — normally
	// os.Getenv("TMUX_PANE"). Empty disables the fit entirely.
	Pane string
	// FireJobs runs the E-698 fire-once job runner on each refresh. Left true by
	// every real dashboard: repetition lives in the trigger, never in the runner.
	FireJobs bool
	// Out is where the frame is painted. Defaults to os.Stdout when nil; tests
	// pass their own.
	Out io.Writer
	// Fatal is called with a render error before the loop unwinds. Defaults to
	// exiting the process, which is what a one-frame-per-2s view can do about a
	// query that has started failing.
	Fatal func(error)
	// Tick overrides Interval. Zero — every real dashboard — means Interval; a
	// test drives the loop faster.
	Tick time.Duration
	// Visibility gates the loop on being seen (E-2229). Nil — outside tmux, and
	// every test that does not ask — means always visible, the loop as it was.
	Visibility Visibility
	// SafetyNet overrides the package SafetyNet; zero means SafetyNet.
	SafetyNet time.Duration

	// wake and sigs replace the SIGUSR1/SIGUSR2 and SIGINT/SIGTERM
	// subscriptions, so a test can deliver a signal without signalling its own
	// process.
	wake chan os.Signal
	sigs chan os.Signal
}

// Loop redraws cfg.Render every Interval until SIGINT/SIGTERM, repainting only
// when the rendered frame changes (so an idle view doesn't flicker). It hides
// the cursor for the duration and restores it on every exit path. Width is
// re-detected each tick so a terminal resize is honored.
//
// On every repaint it also fits its own tmux pane to the frame (E-1851) — the
// view knows its row count, whatever built the layout cannot, so the pane sizes
// itself instead of being guessed at creation. Best-effort and silent: outside
// tmux, or when tmux refuses (a single-pane window), the view is unchanged.
// The fit asks tmux only when what it decides on has changed (paneFit).
//
// It also watches the binary it was started from, and re-execs in place when an
// install replaces it (E-2193; see restart.go). A replacement that cannot start
// leaves this process rendering, with job firing stopped and a notice line
// appended to the frame.
//
// With cfg.Visibility set it works only while it can be seen (E-2229). Hidden,
// it stops its ticker and waits for a signal, a termination, or the safety net
// every SafetyNet — no frame, no query, no git, no job trigger. Becoming
// visible repaints at once and resumes the ticker; a visible view also polls
// on the safety net, so a missed "hidden" signal is corrected too.
func Loop(cfg LoopConfig) {
	out := cfg.Out
	if out == nil {
		out = os.Stdout
	}
	fatal := cfg.Fatal
	if fatal == nil {
		fatal = func(err error) { os.Exit(1) }
	}

	prof, err := startProfiler()
	if err != nil {
		refusal.NoReport(profileEnv+" cannot be used: "+err.Error(),
			"point "+profileEnv+" at a writable directory, or unset it").Exit(1)
	}

	io.WriteString(out, "\x1b[?25l")                         // hide cursor
	restore := func() { io.WriteString(out, "\x1b[?25h\n") } // show cursor + trailing newline

	sigs := cfg.sigs
	if sigs == nil {
		sigs = make(chan os.Signal, 1)
		signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	}
	defer signal.Stop(sigs)
	// SIGWINCH says the pane changed size, which is when the pane fit has to
	// ask tmux again even though the frame did not change.
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)

	// The visibility signals (E-2229). Subscribed BEFORE the gate starts:
	// once it has tagged the pane, a window switch anywhere on the server
	// signals this process.
	gate := cfg.Visibility
	wake := cfg.wake
	if gate != nil && wake == nil {
		wake = make(chan os.Signal, 4)
		signal.Notify(wake, syscall.SIGUSR1, syscall.SIGUSR2)
		defer signal.Stop(wake)
	}

	io.WriteString(out, "\x1b[2J\x1b[H") // clear screen, cursor home
	tick := cfg.Tick
	if tick <= 0 {
		tick = Interval
	}
	ticker := time.NewTicker(tick)
	defer ticker.Stop()

	// The job-runner trigger (E-698). Each refresh fires the fire-once runner,
	// which executes any DUE jobs and returns; repetition lives here, in the
	// trigger, never in the runner.
	//
	// On a goroutine behind a single-in-flight guard: a job slower than the tick
	// must never stack up invocations or stall the redraw. Concurrency with OTHER
	// monitors is not this guard's job — the runner's DB lease arbitrates that,
	// so at most one process runs any given due job.
	var jobsInFlight atomic.Bool
	fireJobs := func(allowed bool) {
		if !allowed || !cfg.FireJobs || jobsInFlight.Swap(true) {
			return
		}
		go func() {
			defer jobsInFlight.Store(false)
			jobs.RunDue(context.Background())
		}()
	}

	prev := ""
	fit := &paneFit{pane: cfg.Pane}

	// The binary watch (E-2193). Before an exec the cursor comes back and the
	// signal relay stops, so the new image starts from a terminal and a signal
	// disposition that look like a fresh launch; it clears and redraws itself.
	// If the exec does not happen, both are taken again.
	//
	// The visibility signals are IGNORED across the exec rather than stopped:
	// the pane keeps its tag (the new image keeps this UPID and re-tags), and
	// an ignored disposition survives exec, so a wake landing before the new
	// image subscribes is dropped instead of taking the default action, which
	// for SIGUSR1 is to terminate.
	watch := newRestarter(
		func() {
			signal.Stop(sigs)
			signal.Stop(winch)
			if gate != nil && cfg.wake == nil {
				signal.Ignore(syscall.SIGUSR1, syscall.SIGUSR2)
			}
			io.WriteString(out, "\x1b[?25h")
		},
		func() {
			signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
			signal.Notify(winch, syscall.SIGWINCH)
			if gate != nil && cfg.wake == nil {
				signal.Notify(wake, syscall.SIGUSR1, syscall.SIGUSR2)
			}
			io.WriteString(out, "\x1b[?25l")
			prev = "" // force a full repaint over whatever the attempt left
		},
	)

	// exit is every clean way out: the gate stops being findable first, since
	// fatal may end the process before a deferred Close would run.
	exit := func(err error) {
		prof.stop()
		restore()
		if gate != nil {
			gate.Close()
		}
		if err != nil {
			fatal(err)
		}
	}

	// paint renders and paints one frame, and reports false when the render
	// failed and the loop must end.
	paint := func() bool {
		var b strings.Builder
		prof.begin()

		// Checked BEFORE jobs fire: on the tick that decides to exec, nothing
		// is fired — and once a restart has failed, nothing ever is again.
		fireJobs(watch.tick(jobsInFlight.Load()))
		cols := DetectCols(cfg.ColsOverride, cfg.FallbackCols)
		rows, err := cfg.Render(&b, cols, cfg.Color)
		if err != nil {
			exit(err)
			return false
		}
		if notice := watch.Notice(); notice != "" {
			fmt.Fprintln(&b, Strong("⚠ "+notice, cfg.Color))
		}
		// A SIGWINCH that arrived while the view was hidden waits here, in
		// its buffered channel, and is honored by the first paint after.
		resized := false
		select {
		case <-winch:
			resized = true
			fit.resized()
		default:
		}
		if frame := b.String(); frame != prev || resized {
			// Resize BEFORE painting so the frame lands in a pane already the
			// right size (a shrink after the paint would scroll rows away).
			fit.fit(frame, cols, rows)
			// Home, repaint each line (erased to end-of-line), then clear to
			// end-of-display so a now-shorter frame leaves no stale rows behind.
			io.WriteString(out, "\x1b[H"+EraseEachLineToEOL(frame)+"\x1b[J")
			prev = frame
		}
		prof.end("visible")
		return true
	}

	// The visibility gate (E-2229). Without one the view is always visible and
	// neither the safety net nor the signals exist. A hidden view stops its
	// ticker and does nothing until a signal or the safety net says otherwise;
	// the last frame stays on screen, unseen, and is repainted on return.
	visible := true
	var netC <-chan time.Time
	if gate != nil {
		every := cfg.SafetyNet
		if every <= 0 {
			every = SafetyNet
		}
		net := time.NewTicker(every)
		defer net.Stop()
		netC = net.C
		visible = gate.Start()
	}
	setVisible := func(now bool) bool {
		if now == visible {
			return true
		}
		visible = now
		if !visible {
			ticker.Stop()
			return true
		}
		ticker.Reset(tick)
		return paint()
	}

	if visible && !paint() {
		return
	}
	if !visible {
		ticker.Stop()
	}
	for {
		select {
		case <-sigs:
			exit(nil)
			return
		case <-ticker.C:
			if !paint() {
				return
			}
		case sig := <-wake:
			if !setVisible(sig == syscall.SIGUSR1) {
				return
			}
		case <-netC:
			prof.begin()
			now := gate.Check()
			if now != visible {
				gate.Missed(now)
			}
			if !visible {
				// A hidden view still follows its binary: a restart runs
				// here at the poll cadence instead of at the tick's. Jobs
				// never fire from a hidden view.
				watch.tick(jobsInFlight.Load())
				prof.end("hidden")
			} else {
				prof.end("poll")
			}
			if !setVisible(now) {
				return
			}
		}
	}
}

// FrameLines counts the terminal rows one rendered frame occupies. A view emits
// every line with Fprintln, so the newline count IS the line count — no
// off-by-one for a trailing empty segment.
//
// Measuring the RENDERED frame, rather than deriving a height from the row
// count, is what keeps the fit correct as a view grows new parts: the fault
// fault row adds a line when an incident is open and none when it isn't, and the fit
// tracks that for free.
func FrameLines(frame string) int {
	return strings.Count(frame, "\n")
}

// FrameDisplayRows counts the SCREEN rows a frame occupies in a pane `width`
// columns wide: each line takes ceil(display width / width) rows, and an empty
// line takes one (E-2188). FrameLines counts a line that wraps as one, which is
// what hid a monitor's last task row behind a wrapped legend; this is what the
// pane fit measures instead.
//
// Width is measured with go-runewidth, as the views measure their own columns,
// after stripping the ANSI escapes a coloured frame carries — they occupy no
// columns. width <= 0 means the width is unknown, and every line counts as one
// row, which is FrameLines' answer.
func FrameDisplayRows(frame string, width int) int {
	if width <= 0 {
		return FrameLines(frame)
	}
	lines := strings.Split(frame, "\n")
	// Every line is Fprintln'd, so the segment after the last newline is empty
	// and is not a row.
	lines = lines[:len(lines)-1]
	n := 0
	for _, line := range lines {
		w := runewidth.StringWidth(ansiEscape.ReplaceAllString(line, ""))
		if w == 0 {
			n++
			continue
		}
		n += (w + width - 1) / width
	}
	return n
}

// ansiEscape matches the CSI sequences a frame is painted with (SGR colours
// and the like).
var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)

// PaneHeightForFrame is the pure sizing rule: the frame's lines plus a slack
// row, floored at PaneMinHeight and capped at PanePctOfWindow percent of
// windowHeight. A windowHeight of 0 means "unknown": no cap.
//
// rows == 0 is the view's nothing-here hint, NOT a short real frame, and gets
// PaneEmptyHeight instead of an exact fit. Sizing the hint exactly collapses the
// pane to a 2-row sliver that reads as broken rather than as empty, and leaves
// no room to grow into the moment content appears. The cap still applies, so a
// tiny window never gets an oversized monitor.
func PaneHeightForFrame(lines, rows, windowHeight int) int {
	height := lines + PaneSlack
	if rows == 0 {
		height = PaneEmptyHeight
	}
	if height < PaneMinHeight {
		height = PaneMinHeight
	}
	if windowHeight > 0 {
		maxHeight := windowHeight * PanePctOfWindow / 100
		if maxHeight < PaneMinHeight {
			maxHeight = PaneMinHeight
		}
		if height > maxHeight {
			height = maxHeight
		}
	}
	return height
}

// FitPaneToFrame resizes pane to hold frame and returns the height now in
// effect. cols is the width the frame was rendered for — the pane's own width —
// and is what its wrapped lines are counted against (FrameDisplayRows). `fitted` is the height the last successful resize applied, so an
// unchanged fit costs no tmux subprocess; a failed resize leaves it untouched so
// the next repaint retries. Returns fitted unchanged when not running in tmux.
func FitPaneToFrame(pane, frame string, cols, rows, fitted int) int {
	if pane == "" {
		return fitted
	}
	height := PaneHeightForFrame(FrameDisplayRows(frame, cols), rows, monitor.PaneWindowHeight(pane))
	if height == fitted {
		return fitted
	}
	if err := monitor.ResizePaneHeight(pane, height); err != nil {
		return fitted
	}
	return height
}

// paneWindowHeight and resizePaneHeight are the tmux calls the pane fit makes,
// held as vars so a test can count them without a tmux server.
var (
	paneWindowHeight = monitor.PaneWindowHeight
	resizePaneHeight = monitor.ResizePaneHeight
)

// paneFit is Loop's pane fit with memory (E-2228). FitPaneToFrame asks tmux for
// the window height on every changed frame, but a frame changes far more often
// than the height it needs: a row's text moves while its line count does not.
// So paneFit remembers what its last decision was based on, and asks tmux again
// only when one of those inputs moved — the frame's measured height, the row
// count, the width — or when the terminal was resized (SIGWINCH), which is
// the one way the window height changes underneath an unchanged frame.
//
// A failed resize is remembered the same way: the same inputs will not retry
// it on every repaint, and the next change of input or size will.
type paneFit struct {
	pane    string
	decided bool
	lines   int
	rows    int
	cols    int
	fitted  int
}

// resized forgets the last decision, so the next fit asks tmux again.
func (f *paneFit) resized() {
	f.decided = false
}

// fit resizes the pane to hold frame when its inputs changed since the last
// decision.
func (f *paneFit) fit(frame string, cols, rows int) {
	if f.pane == "" {
		return
	}
	lines := FrameDisplayRows(frame, cols)
	if f.decided && lines == f.lines && rows == f.rows && cols == f.cols {
		return
	}
	f.decided, f.lines, f.rows, f.cols = true, lines, rows, cols
	height := PaneHeightForFrame(lines, rows, paneWindowHeight(f.pane))
	if height == f.fitted {
		return
	}
	if resizePaneHeight(f.pane, height) != nil {
		return
	}
	f.fitted = height
}

// EraseEachLineToEOL wraps a rendered frame so that repainting it over a prior
// frame leaves no stale characters. It appends an erase-to-end-of-line (\x1b[K)
// before every newline and one after the final line, so a row whose new content
// is shorter than the prior frame's on that row does not keep the old tail. The
// caller's trailing \x1b[J still clears whole rows below a now-shorter frame;
// \x1b[J alone cannot, because it only erases from the cursor's final position
// to end-of-display, never the tails of the overwritten lines above it (E-1699).
func EraseEachLineToEOL(frame string) string {
	return strings.ReplaceAll(frame, "\n", "\x1b[K\n") + "\x1b[K"
}

// DetectCols resolves the terminal width: an explicit override wins, then the
// real tty, then $COLUMNS, then the caller's fallback.
func DetectCols(override, fallback int) int {
	if override > 0 {
		return override
	}
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		return w
	}
	if v := os.Getenv("COLUMNS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return fallback
}

// ColorEnabled reports whether intensity escapes should be emitted: never under
// NO_COLOR, otherwise only to a real terminal.
func ColorEnabled() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	return term.IsTerminal(int(os.Stdout.Fd()))
}

// Dim wraps s in the dim escape when enabled.
func Dim(s string, enabled bool) string {
	if !enabled {
		return s
	}
	return DimSGR + s + Reset
}

// Strong wraps s in the bold escape when enabled. Bold is the loudest thing a
// theme-independent renderer has, so views reserve it for rows that must not be
// scrolled past.
func Strong(s string, enabled bool) string {
	if !enabled {
		return s
	}
	return Bold + s + Reset
}

// Collapse squeezes internal whitespace runs to single spaces so multi-line or
// padded titles render on one line (matches `endless session list`).
func Collapse(s string) string {
	out := make([]rune, 0, len(s))
	prevSpace := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			if !prevSpace {
				out = append(out, ' ')
			}
			prevSpace = true
			continue
		}
		out = append(out, r)
		prevSpace = false
	}
	return string(out)
}

// IsTerminal reports whether f is an interactive terminal. A live view's redraw
// uses cursor-positioning escapes, so a piped or captured destination must get a
// single frame instead of an endless loop that would hang the pipe.
func IsTerminal(f *os.File) bool {
	if f == nil {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

// DetectRows resolves how many terminal rows one frame may occupy, or 0 for
// "unbounded" — piped output, a captured snapshot, or a terminal whose size
// cannot be read.
//
// Inside tmux it reports PanePctOfWindow of the WINDOW height, not the pane's
// current height, and that distinction is the whole reason this function exists.
// The pane is fitted TO the frame on every repaint (FitPaneToFrame), so a frame
// sized to the pane would be sizing itself to itself: it would shrink one row
// per repaint until nothing was left. The window's share is the fixed ceiling
// the fit is already capped at, so it is the honest budget.
//
// A view that overruns this budget does not wrap — it SCROLLS, and a scrolled
// frame loses its TOP, which in a ranked frame is the loudest rows. Any view
// whose row set can outgrow a pane has to spend this budget deliberately rather
// than discover it.
// `pct` is the share of the tmux WINDOW this view may claim WHEN IT IS SHARING
// THAT WINDOW; <= 0 means PanePctOfWindow. A view whose frame always grows to
// fill its budget wants a smaller share than that default, because for such a
// view the cap is not a safety net that rarely binds — it binds every frame, and
// whatever it leaves over is all the pane below it will ever get.
//
// The share applies ONLY when another pane is actually there to receive the
// remainder. Alone in its window a view takes the whole height: the reservation
// exists to feed a companion pane, and reserving a third of the window for a
// pane that does not exist just draws a short frame above a block of dead space.
// That is what `endless project monitor` did when run in a plain terminal or a
// single-pane window — 27 lines of monitor in a 44-row pane (E-1976).
func DetectRows(pane string, pct, fallback int) int {
	if pct <= 0 {
		pct = PanePctOfWindow
	}
	if pane != "" {
		if h := monitor.PaneWindowHeight(pane); h > 0 {
			// PaneWindowPanes returns 0 for "could not tell". Treating unknown as
			// shared keeps the cautious behaviour: an over-reserved frame is a
			// cosmetic loss, while an under-reserved one squeezes a companion pane
			// the view cannot see.
			if n := monitor.PaneWindowPanes(pane); n == 1 {
				pct = 100
			}
			if budget := h * pct / 100; budget > 0 {
				return budget
			}
		}
	}
	if _, h, err := term.GetSize(int(os.Stdout.Fd())); err == nil && h > 0 {
		return h
	}
	return fallback
}

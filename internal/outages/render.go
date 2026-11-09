package outages

import (
	"regexp"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"
)

// Outage line colours (E-2589). 256-colour indices for faultrow's reason: the
// 30-47 range is remapped by the terminal's theme. Deliberately NOT reversed
// and not the fault row's gold or red: an outage asks nothing of the reader,
// and a calm foreground reads as information beside a bar that reads as a
// problem.
const (
	colorBase    = "\033[38;5;110m"  // steel blue: the sentence
	colorName    = "\033[1;38;5;81m" // bold sky blue: the provider
	colorNumbers = "\033[38;5;151m"  // pale green: how long, how often
	colorReset   = "\033[0m"
)

// lineParts are an outage line's pieces: the provider name, the status, how
// long it has lasted and how often it is polled.
func lineParts(o Outage, now time.Time) (name, status, lasted, every string) {
	return o.Provider, StatusText(o.Status), Duration(o.Lasted(now)), Duration(PollInterval)
}

// Line renders one open outage for a status view, fitted to cols-1 columns
// (faultrow's rule: a line ending exactly at the margin can wrap in tmux):
//
//	Outage[GitHub]: Degraded for 4 hrs 59 min; polling every 1 min
//
// A duration rather than a start time, so the reader need not do clock
// arithmetic. With color, the provider name and the numbers each take a
// contrasting colour. No action hint: there is nothing to do. "" when cols is
// too narrow for anything legible; cols <= 0 means unbounded.
func Line(o Outage, now time.Time, cols int, color bool) string {
	name, status, lasted, every := lineParts(o, now)
	text := "Outage[" + name + "]: " + status + " for " + lasted + "; polling every " + every
	width := cols - 1
	if cols <= 0 {
		width = runewidth.StringWidth(text)
	}
	if width < 12 {
		return ""
	}
	if runewidth.StringWidth(text) > width {
		text = runewidth.Truncate(text, width, "…")
		if !color {
			return text
		}
		return colorBase + text + colorReset
	}
	if !color {
		return text
	}
	return colorBase + "Outage[" + colorName + name + colorReset + colorBase + "]: " +
		status + " for " + numbers(lasted) + "; polling every " + numbers(every) + colorReset
}

// numbers colours each run of digits in s, which carries no escapes of its
// own, and returns to the base colour after each.
func numbers(s string) string {
	return digits.ReplaceAllString(s, colorNumbers+"$0"+colorReset+colorBase)
}

var digits = regexp.MustCompile(`\d+`)

// AgentContext renders the open outages as hook context for an agent (E-2589),
// so a failed push or an API error during one is not debugged as a local
// problem. "" when none is open.
func AgentContext(open []Outage, now time.Time) string {
	var lines []string
	for _, o := range open {
		if !o.Open() {
			continue
		}
		what := "A failed fetch or push to a github.com remote during it is GitHub's, not a local problem."
		if o.Provider == Anthropic.Name {
			what = "An API error ending a turn during it is Anthropic's, not a local problem."
		}
		lines = append(lines, "Outage: "+o.Provider+"'s status page reports "+o.Component+
			" "+strings.ToLower(StatusText(o.Status))+" ("+Duration(o.Lasted(now))+" so far). "+
			what+" Endless closes this notice itself when "+o.Provider+" reports it operational.")
	}
	return strings.Join(lines, "\n")
}

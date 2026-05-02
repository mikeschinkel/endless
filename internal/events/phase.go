package events

import "fmt"

// ValidPhases is the closed set of recognized task-phase values.
// Anything outside this set is rejected at the events boundary so it
// cannot enter the event log or projection.
var ValidPhases = map[string]bool{
	"now":   true,
	"next":  true,
	"later": true,
	"maybe": true,
}

// ValidatePhase returns an error if s is not a recognized phase value.
// Empty string is rejected.
func ValidatePhase(s string) error {
	if !ValidPhases[s] {
		return fmt.Errorf("events: invalid phase %q (valid: now, next, later, maybe)", s)
	}
	return nil
}

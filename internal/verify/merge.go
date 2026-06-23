package verify

// Merge layers a project-level config beneath a per-task manifest and returns
// the effective manifest a runner executes. It never mutates either input; the
// task manifest passed to discovery is decoded leniently (required fields may be
// absent) and the caller validates the returned effective manifest.
//
// Merge semantics (mirrors Endless's layered config in internal/config — project
// supplies defaults, the more-specific layer wins):
//
//   - Setup: list-append, PROJECT FIRST. The project's shared setup steps
//     (e.g. build the binaries, initialize schema) run before the task's own
//     setup. This is the ordering the runner relies on: shared preparation of
//     the project precedes task-specific preparation.
//   - Teardown: list-append, PROJECT FIRST (same direction as Setup, for
//     simplicity). A strict reverse-of-setup (LIFO) ordering is a possible
//     future refinement; deferred because teardown has no live consumer until
//     the runner (downstream) executes it.
//   - Seed: list-append, PROJECT FIRST. Shared fixtures load before
//     task-specific fixtures. (What a seed entry MEANS is E-1606's concern; the
//     layering is defined here.)
//   - Needs: scalar-style default. The task value wins when it sets the key at
//     all — including an explicit empty list (needs = []) to override the
//     project default down to "no needs". A task that omits needs entirely
//     (nil) inherits the project default.
//   - Schema, Task, Checks, Tiers: per-task only; copied straight from the task
//     manifest. The project config cannot carry them. Format is per-check, not a
//     manifest field, so it does not participate in the merge.
//
// A nil project config is the identity layer: Merge returns a copy of the task
// manifest unchanged. A nil task manifest yields an empty effective manifest
// seeded only from the project config (validation will then report the missing
// required fields).
func Merge(pc *ProjectConfig, task *Manifest) (eff *Manifest) {
	eff = &Manifest{}
	if task != nil {
		*eff = *task
	}
	if pc == nil {
		return eff
	}

	// List-append preconditions: project's shared steps run first.
	eff.Setup = concatStrings(pc.Setup, eff.Setup)
	eff.Teardown = concatStrings(pc.Teardown, eff.Teardown)
	eff.Seed = concatStrings(pc.Seed, eff.Seed)

	// Needs distinguishes "unset" (nil -> inherit) from "explicit empty"
	// (non-nil, len 0 -> override the default down to none).
	if eff.Needs == nil {
		eff.Needs = pc.Needs
	}

	return eff
}

// concatStrings returns a new slice with a's elements followed by b's. It never
// returns or aliases an input slice when both are non-empty, so callers can hold
// the result without risk of later mutation reaching the inputs. An empty input
// is treated as absent.
func concatStrings(a, b []string) (out []string) {
	switch {
	case len(a) == 0:
		out = b
	case len(b) == 0:
		out = a
	default:
		out = make([]string, 0, len(a)+len(b))
		out = append(out, a...)
		out = append(out, b...)
	}
	return out
}

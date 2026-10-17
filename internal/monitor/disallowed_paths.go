package monitor

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Some directories can never be a project (E-2369), whatever the projects table
// says. Three kinds of rule decide it, all applied by ResolveDirectory so every
// registration path — the hook's auto-registration, `project register`, discover
// and reconcile — refuses the same directories:
//
//  1. $HOME and every ancestor of it (/Users, /home, C:\Users, `/`). Built in and
//     not overridable: a project at $HOME makes every directory under it a part
//     of that project, which is the bug this file exists to stop.
//  2. A per-OS list of system and user-data directories, each scoped SELF (the
//     directory itself is refused, its children may be projects — ~/Documents)
//     or SUBTREE (nothing at or under it may be — /etc, ~/Downloads).
//  3. Parent directories (~/Projects): not a project, AND the walk-up stops
//     there, so nothing below inherits a project registered above. Declared by
//     the list, by an override, or by a ParentDirMarker file on disk.
//
// The built-in list lives here in the binary. A user's changes live in
// DisallowedOverridesFile in the config directory, holding only additions and
// removals, so a later Endless that adds a default reaches every user and the
// user's edits still win. The full built-in list is rendered into the guide by
// DisallowedPathsMarkdown, and a test fails when the committed page drifts.

// ParentDirMarker is the on-disk way to declare a parent directory: a file a
// user drops in a directory of projects. At the same rung it wins over the
// projects table, as IgnoreMarker does.
const ParentDirMarker = ".endless-parent-dir"

// DisallowedOverridesFile is the user's overrides to the built-in list, in the
// config directory.
const DisallowedOverridesFile = "disallowed-paths.json"

// Path scopes.
const (
	ScopeSelf    = "self"
	ScopeSubtree = "subtree"
)

// ErrDisallowedDirectory is returned by ProjectIDForPath for a directory that
// can never be a project, or that holds one. Like ErrIgnoredDirectory it is not
// a failure: a caller recording activity treats it as nothing to record.
var ErrDisallowedDirectory = errors.New("directory cannot be an Endless project")

// NotAProject reports whether err says the directory is not a project — ignored
// by the user or disallowed by rule — rather than that the lookup failed.
func NotAProject(err error) bool {
	return errors.Is(err, ErrIgnoredDirectory) || errors.Is(err, ErrDisallowedDirectory)
}

// disallowedEntry is one row of a built-in or override list, before resolution.
type disallowedEntry struct {
	// Path is `~/`-relative, absolute, or a %VAR%-led Windows path. Empty when
	// Known names the directory instead.
	Path string
	// Known names a user folder whose real location the OS knows (its
	// localized name on a Linux desktop, a relocated one on Windows).
	Known     string
	Scope     string
	ParentDir bool
	Why       string
}

// Reasons, shared by the built-in entries so the guide page groups them.
const (
	whySystem    = "operating-system files"
	whyUserData  = "a user folder; its children may be projects"
	whyMedia     = "downloads or media, not code"
	whyAppData   = "application data and settings"
	whyParentDir = "a folder of projects"
	whyMount     = "a mount point or scratch root; its children may be projects"
)

func sysTree(path string) disallowedEntry {
	return disallowedEntry{Path: path, Scope: ScopeSubtree, Why: whySystem}
}

func mountRoot(path string) disallowedEntry {
	return disallowedEntry{Path: path, Scope: ScopeSelf, Why: whyMount}
}

func knownDir(name, scope, why string) disallowedEntry {
	return disallowedEntry{Known: name, Scope: scope, Why: why}
}

func parentDirEntry(path string) disallowedEntry {
	return disallowedEntry{Path: path, Scope: ScopeSelf, ParentDir: true, Why: whyParentDir}
}

// builtinDisallowed is the per-OS list. Every OS is data in every build, so the
// guide page can document all three from any machine.
var builtinDisallowed = map[string][]disallowedEntry{
	// The Filesystem Hierarchy Standard: system trees are refused whole; the
	// roots that hold deployed or mounted work (/opt, /srv, /var/www, /mnt) only
	// as themselves, since /opt/app and /var/www/site are real project homes.
	"linux": {
		sysTree("/bin"), sysTree("/sbin"), sysTree("/lib"), sysTree("/lib32"), sysTree("/lib64"), sysTree("/libx32"),
		sysTree("/boot"), sysTree("/dev"), sysTree("/proc"), sysTree("/sys"), sysTree("/run"), sysTree("/etc"),
		sysTree("/usr/bin"), sysTree("/usr/sbin"), sysTree("/usr/lib"), sysTree("/usr/lib64"),
		sysTree("/usr/libexec"), sysTree("/usr/include"), sysTree("/usr/share"),
		sysTree("/var/lib"), sysTree("/var/log"), sysTree("/var/cache"), sysTree("/var/spool"), sysTree("/var/tmp"),
		sysTree("/lost+found"),
		mountRoot("/usr"), mountRoot("/usr/local"), mountRoot("/var"), mountRoot("/var/www"), mountRoot("/opt"),
		mountRoot("/srv"), mountRoot("/mnt"), mountRoot("/media"), mountRoot("/tmp"), mountRoot("/root"),
		{Path: "~/.config", Scope: ScopeSubtree, Why: whyAppData},
		{Path: "~/.local", Scope: ScopeSubtree, Why: whyAppData},
		{Path: "~/.cache", Scope: ScopeSubtree, Why: whyAppData},
		knownDir("Downloads", ScopeSubtree, whyMedia),
		knownDir("Music", ScopeSubtree, whyMedia),
		knownDir("Pictures", ScopeSubtree, whyMedia),
		knownDir("Videos", ScopeSubtree, whyMedia),
		knownDir("Desktop", ScopeSelf, whyUserData),
		knownDir("Documents", ScopeSelf, whyUserData),
		knownDir("Public", ScopeSelf, whyUserData),
		knownDir("Templates", ScopeSelf, whyUserData),
		parentDirEntry("~/Projects"),
	},
	// /etc, /tmp and /var are symlinks into /private, and paths are compared
	// resolved, so /private and /private/var are refused only as themselves:
	// refusing either whole would refuse every temp directory.
	"darwin": {
		sysTree("/System"), sysTree("/Library"), sysTree("/Applications"),
		sysTree("/bin"), sysTree("/sbin"),
		sysTree("/usr/bin"), sysTree("/usr/sbin"), sysTree("/usr/lib"), sysTree("/usr/libexec"), sysTree("/usr/share"),
		sysTree("/etc"), sysTree("/dev"), sysTree("/cores"),
		sysTree("/private/var/db"), sysTree("/private/var/log"),
		mountRoot("/private"), mountRoot("/private/var"), mountRoot("/tmp"),
		mountRoot("/usr"), mountRoot("/usr/local"), mountRoot("/opt"), mountRoot("/Volumes"),
		{Path: "~/Library", Scope: ScopeSubtree, Why: whyAppData},
		{Path: "~/Applications", Scope: ScopeSubtree, Why: whyAppData},
		{Path: "~/.Trash", Scope: ScopeSubtree, Why: whyAppData},
		knownDir("Downloads", ScopeSubtree, whyMedia),
		knownDir("Music", ScopeSubtree, whyMedia),
		knownDir("Pictures", ScopeSubtree, whyMedia),
		knownDir("Videos", ScopeSubtree, whyMedia),
		knownDir("Desktop", ScopeSelf, whyUserData),
		knownDir("Documents", ScopeSelf, whyUserData),
		knownDir("Public", ScopeSelf, whyUserData),
		parentDirEntry("~/Projects"),
	},
	// Program Files is refused whole: installers write it, writing needs
	// elevation, and UAC virtualizes writes to it.
	"windows": {
		sysTree(`%SystemRoot%`), sysTree(`%ProgramFiles%`), sysTree(`%ProgramFiles(x86)%`),
		sysTree(`%ProgramData%`), sysTree(`%PUBLIC%`), sysTree(`%SystemDrive%\Users\Default`),
		mountRoot(`%SystemDrive%\`),
		{Path: `~\AppData`, Scope: ScopeSubtree, Why: whyAppData},
		knownDir("Downloads", ScopeSubtree, whyMedia),
		knownDir("Music", ScopeSubtree, whyMedia),
		knownDir("Pictures", ScopeSubtree, whyMedia),
		knownDir("Videos", ScopeSubtree, whyMedia),
		knownDir("Desktop", ScopeSelf, whyUserData),
		knownDir("Documents", ScopeSelf, whyUserData),
		knownDir("OneDrive", ScopeSelf, whyUserData),
		parentDirEntry(`~\Projects`),
		parentDirEntry(`~\source\repos`),
	},
}

// dirRule is one resolved rule: what ResolveDirectory matches against.
type dirRule struct {
	Scope     string
	ParentDir bool
	Why       string
}

// dirRules maps a resolved directory (pathKey form) to its rule.
type dirRules map[string]dirRule

// merge folds r into the rule already at key: SUBTREE outranks SELF, and a
// parent-directory declaration from either side sticks.
func (rs dirRules) merge(key string, r dirRule) {
	prev, ok := rs[key]
	if !ok {
		rs[key] = r
		return
	}
	if r.Scope == ScopeSubtree && prev.Scope != ScopeSubtree {
		prev.Scope, prev.Why = ScopeSubtree, r.Why
	}
	prev.ParentDir = prev.ParentDir || r.ParentDir
	rs[key] = prev
}

// subtreeRoot is the nearest SUBTREE rule at or above dir.
func (rs dirRules) subtreeRoot(dir string) (string, dirRule, bool) {
	for check := dir; ; {
		if r, ok := rs[pathKey(check)]; ok && r.Scope == ScopeSubtree {
			return check, r, true
		}
		parent := filepath.Dir(check)
		if parent == check {
			return "", dirRule{}, false
		}
		check = parent
	}
}

// pathKey folds case on Windows, whose file system does.
func pathKey(p string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(p)
	}
	return p
}

// disallowedOverrides is DisallowedOverridesFile's shape.
type disallowedOverrides struct {
	Doc string `json:"_doc,omitempty"`
	Add []struct {
		Path      string `json:"path"`
		Scope     string `json:"scope"`
		ParentDir bool   `json:"parent_dir"`
		Why       string `json:"why"`
	} `json:"add"`
	Remove []string `json:"remove"`
}

const overridesDoc = "Overrides to Endless's built-in list of directories that cannot be " +
	"projects. `add` takes {path, scope: self|subtree, parent_dir}; `remove` takes " +
	"built-in paths. $HOME and the directories above it cannot be removed. " +
	"The built-in list: endless guide disallowed-paths"

// loadDirRules builds the rules in force for this OS: the built-in list, the
// user's overrides applied to it, then rule 1, which no override reaches.
func loadDirRules(home string) (dirRules, error) {
	rs := dirRules{}
	ov, err := readOverrides(filepath.Join(ConfigDir(), DisallowedOverridesFile))
	if err != nil {
		return nil, err
	}
	removed := map[string]bool{}
	for _, p := range ov.Remove {
		if r, ok := resolveRulePath(p, home); ok {
			removed[pathKey(r)] = true
		}
	}
	for _, e := range builtinDisallowed[runtime.GOOS] {
		p, ok := resolveEntry(e, home)
		if !ok || removed[pathKey(p)] {
			continue
		}
		rs.merge(pathKey(p), dirRule{Scope: e.Scope, ParentDir: e.ParentDir, Why: e.Why})
	}
	for _, a := range ov.Add {
		p, ok := resolveRulePath(a.Path, home)
		if !ok {
			continue
		}
		scope := a.Scope
		if scope != ScopeSubtree {
			scope = ScopeSelf
		}
		why := a.Why
		if why == "" {
			why = "listed in " + DisallowedOverridesFile
		}
		rs.merge(pathKey(p), dirRule{Scope: scope, ParentDir: a.ParentDir, Why: why})
	}
	for check := home; ; {
		why := "the home directory"
		if check != home {
			why = "a directory above the home directory"
		}
		rs.merge(pathKey(check), dirRule{Scope: ScopeSelf, Why: why})
		parent := filepath.Dir(check)
		if parent == check {
			break
		}
		check = parent
	}
	return rs, nil
}

// readOverrides reads the overrides file, creating it as an empty skeleton the
// first time so a user has something to find and edit. A malformed file is an
// error naming it: silently dropping a user's override would register a
// directory they said must not be.
func readOverrides(path string) (ov disallowedOverrides, err error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		writeOverridesSkeleton(path)
		return ov, nil
	}
	if err != nil {
		return ov, fmt.Errorf("reading %s: %w", path, err)
	}
	if err = json.Unmarshal(data, &ov); err != nil {
		return ov, fmt.Errorf("parsing %s: %w", path, err)
	}
	return ov, nil
}

// writeOverridesSkeleton is best-effort: the skeleton is a convenience for the
// user, and a read-only config directory must not stop a project lookup.
// O_EXCL keeps two racing processes from both writing it.
func writeOverridesSkeleton(path string) {
	data, err := json.MarshalIndent(disallowedOverrides{
		Doc:    overridesDoc,
		Add:    nil,
		Remove: []string{},
	}, "", "  ")
	if err != nil {
		return
	}
	data = []byte(strings.Replace(string(data), `"add": null`, `"add": []`, 1) + "\n")
	if err = os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return
	}
	_, _ = f.Write(data)
	_ = f.Close()
}

// resolveEntry resolves a built-in entry to a directory on this machine. False
// when the entry does not apply here — a known folder this desktop lacks, a
// Windows variable that is unset.
func resolveEntry(e disallowedEntry, home string) (string, bool) {
	if e.Known != "" {
		p, ok := knownFolder(runtime.GOOS, e.Known, home)
		if !ok || resolveAbs(p) == home {
			return "", false
		}
		return resolveAbs(p), true
	}
	return resolveRulePath(e.Path, home)
}

// resolveRulePath expands a leading ~ and %VAR% references, then resolves.
func resolveRulePath(p, home string) (string, bool) {
	switch {
	case p == "~":
		p = home
	case strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`):
		p = filepath.Join(home, p[2:])
	case strings.HasPrefix(p, "%"):
		var ok bool
		if p, ok = expandWindowsVars(p); !ok {
			return "", false
		}
	}
	if p == "" || !filepath.IsAbs(p) {
		return "", false
	}
	return resolveAbs(p), true
}

// expandWindowsVars expands %NAME% references. False when one is unset, so an
// entry for a directory this machine does not have is skipped, not misplaced.
func expandWindowsVars(p string) (string, bool) {
	var b strings.Builder
	for {
		i := strings.Index(p, "%")
		if i < 0 {
			b.WriteString(p)
			return b.String(), true
		}
		j := strings.Index(p[i+1:], "%")
		if j < 0 {
			return "", false
		}
		v := os.Getenv(p[i+1 : i+1+j])
		if v == "" {
			return "", false
		}
		b.WriteString(p[:i])
		b.WriteString(v)
		p = p[i+2+j:]
	}
}

// DisallowedPathsMarkdown renders the built-in list for every OS — the guide
// page `endless guide disallowed-paths` prints. Generated so the page cannot
// drift from the code; TestDisallowedPathsDocIsCurrent enforces it.
func DisallowedPathsMarkdown() string {
	var b strings.Builder
	b.WriteString(`# Directories that cannot be projects

<!-- GENERATED by ` + "`just disallowed-doc`" + ` from internal/monitor/disallowed_paths.go — do not hand-edit -->

Endless refuses to register, auto-register, or resolve a project at certain
directories. Every registration path applies the same rules: the hook's
auto-registration, ` + "`endless project register`" + `, discover and reconcile.

- **self** — the directory itself can never be a project; a directory inside it can.
- **subtree** — neither the directory nor anything inside it can be a project.
- **parent directory** — not a project itself, and the search for an enclosing
  project stops there, so nothing inside inherits a project registered above it.

## Always refused

` + "`$HOME`" + ` itself, and every directory above it (` + "`/Users`, `/home`, `C:\\Users`, `/`" + `),
scope **self**. This cannot be overridden. Directories outside ` + "`$HOME`" + ` (such
as ` + "`/work/app`" + ` in a container) are allowed.

A project nested inside another registered project is also refused, in either
direction.

## Overrides

Add or remove entries in ` + "`~/.config/endless/" + DisallowedOverridesFile + "`" + `, which Endless
creates empty the first time it needs it. It holds only your changes, so new
built-in entries in a later Endless still reach you:

` + "```json" + `
{
  "add": [
    {"path": "~/Scratch", "scope": "subtree"},
    {"path": "~/Code", "parent_dir": true}
  ],
  "remove": ["/etc"]
}
` + "```" + `

A directory can also declare itself a parent directory with a ` + "`" + ParentDirMarker + "`" + `
file, or opt out entirely with ` + "`" + IgnoreMarker + "`" + `.

User folders such as Downloads are found where the OS keeps them: on Linux
from ` + "`~/.config/user-dirs.dirs`" + ` (so localized names work, and servers
without the file skip them), on Windows from the known-folder API (so a
relocated folder still matches), and on macOS at their fixed English names.
`)
	for _, osName := range []string{"darwin", "linux", "windows"} {
		fmt.Fprintf(&b, "\n## %s\n\n", map[string]string{
			"darwin": "macOS", "linux": "Linux", "windows": "Windows",
		}[osName])
		b.WriteString("| Directory | Scope | Why |\n|---|---|---|\n")
		entries := append([]disallowedEntry(nil), builtinDisallowed[osName]...)
		sort.SliceStable(entries, func(i, j int) bool {
			return entryOrder(entries[i]) < entryOrder(entries[j])
		})
		for _, e := range entries {
			scope := e.Scope
			if e.ParentDir {
				scope = "parent directory"
			}
			fmt.Fprintf(&b, "| `%s` | %s | %s |\n", entryLabel(osName, e), scope, e.Why)
		}
	}
	return b.String()
}

// entryOrder groups the guide's rows: subtree, then self, then parent dirs.
func entryOrder(e disallowedEntry) int {
	switch {
	case e.ParentDir:
		return 2
	case e.Scope == ScopeSelf:
		return 1
	}
	return 0
}

// entryLabel is how an entry reads in the guide.
func entryLabel(osName string, e disallowedEntry) string {
	if e.Known == "" {
		return e.Path
	}
	if osName == "windows" {
		return `~\` + e.Known + " (known folder)"
	}
	if osName == "darwin" {
		return "~/" + darwinFolderName(e.Known)
	}
	return "~/" + e.Known + " (XDG user dir)"
}

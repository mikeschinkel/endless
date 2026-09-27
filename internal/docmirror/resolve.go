package docmirror

import (
	"regexp"
	"strconv"

	"github.com/mikeschinkel/endless/internal/taskcontent"
)

// Source is what a mirror path resolves to: the task and content name, or the
// decision, that own the file's content.
//
// Exactly one of TaskID / DecisionID is non-zero. Name is the task_content name
// for a task mirror and zero for a decision, whose body has one home
// (`decisions.description`) and needs no naming.
type Source struct {
	TaskID     int64
	DecisionID int64
	Name       taskcontent.Name

	// Legacy is true when the path matched a pre-consolidation location. The
	// content is identical either way; the flag exists so a caller relocating
	// files knows the file it just read is in the wrong place.
	Legacy bool
}

// taskDocCapture and legacyTaskDocCapture are the capturing forms of the
// recognizers in docmirror.go. Two expressions rather than capture groups bolted
// onto the originals, because the originals are used as plain predicates by the
// hook on every tool call and adding groups there would make every match
// allocate for a result nobody reads.
var (
	taskDocCapture       = regexp.MustCompile(`(?:^|/)\.endless/tasks/e-(\d+)/(` + stemAlternation() + `)\.md$`)
	legacyTaskDocCapture = regexp.MustCompile(`(?:^|/)\.endless/(` + legacyDirAlternation() + `)/E-(\d+)\.md$`)
	decisionDocCapture   = regexp.MustCompile(`(?:^|/)\.endless/decisions/ED-(\d+)\.md$`)
)

// legacyDirName maps a pre-consolidation directory to the content name that
// owns it, and stemName a consolidated stem to its content name. Derived from
// TaskKinds so neither can disagree with it.
var legacyDirName, stemName = func() (map[string]taskcontent.Name, map[string]taskcontent.Name) {
	dirs := make(map[string]taskcontent.Name, len(TaskKinds))
	stems := make(map[string]taskcontent.Name, len(TaskKinds))
	for _, k := range TaskKinds {
		if k.LegacyDir != "" {
			dirs[k.LegacyDir] = k.Name
		}
		stems[k.Stem] = k.Name
	}
	return dirs, stems
}()

// Resolve reports which row and column own the content of a mirror path, and
// whether the path was recognized at all.
//
// One function for all four shapes because every caller that reads a mirror
// asks the same question of it — the sweep that rewrites drifted files, the
// orphan-branch check that refuses to delete a branch whose copy disagrees with
// the database, and the branch-history cleanup. Asking it in one place is what
// keeps "which side is authoritative" from being decided three different ways.
func Resolve(path string) (src Source, ok bool) {
	var m []string
	var id int64
	var err error

	m = taskDocCapture.FindStringSubmatch(path)
	if m != nil {
		id, err = strconv.ParseInt(m[1], 10, 64)
		if err != nil {
			goto end
		}
		src = Source{TaskID: id, Name: stemName[m[2]]}
		ok = true
		goto end
	}

	m = legacyTaskDocCapture.FindStringSubmatch(path)
	if m != nil {
		id, err = strconv.ParseInt(m[2], 10, 64)
		if err != nil {
			goto end
		}
		src = Source{TaskID: id, Name: legacyDirName[m[1]], Legacy: true}
		ok = true
		goto end
	}

	m = decisionDocCapture.FindStringSubmatch(path)
	if m != nil {
		id, err = strconv.ParseInt(m[1], 10, 64)
		if err != nil {
			goto end
		}
		src = Source{DecisionID: id}
		ok = true
		goto end
	}

end:
	return src, ok
}

// KindByName returns the Kind mirroring a content name.
func KindByName(name taskcontent.Name) (kind Kind, ok bool) {
	for _, k := range TaskKinds {
		if k.Name == name {
			kind, ok = k, true
			break
		}
	}
	return kind, ok
}

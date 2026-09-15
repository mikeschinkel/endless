package docmirror

import (
	"regexp"
	"strconv"
)

// Source is what a mirror path resolves to: the database row and column that
// own the file's content.
//
// Exactly one of TaskID / DecisionID is non-zero. Column is the `tasks` column
// for a task mirror and empty for a decision, whose body has one home
// (`decisions.description`) and needs no naming.
type Source struct {
	TaskID     int64
	DecisionID int64
	Column     string

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
	taskDocCapture       = regexp.MustCompile(`(?:^|/)\.endless/tasks/e-(\d+)/(plan|outcome|analysis)\.md$`)
	legacyTaskDocCapture = regexp.MustCompile(`(?:^|/)\.endless/(plans|outcomes|analyses)/E-(\d+)\.md$`)
	decisionDocCapture   = regexp.MustCompile(`(?:^|/)\.endless/decisions/ED-(\d+)\.md$`)
)

// legacyDirColumn maps a pre-consolidation directory to the column that owns it.
// Derived from TaskKinds so the two can never disagree.
var legacyDirColumn = func() map[string]string {
	m := make(map[string]string, len(TaskKinds))
	for _, k := range TaskKinds {
		m[k.LegacyDir] = k.Column
	}
	return m
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
		src = Source{TaskID: id, Column: m[2]}
		ok = true
		goto end
	}

	m = legacyTaskDocCapture.FindStringSubmatch(path)
	if m != nil {
		id, err = strconv.ParseInt(m[2], 10, 64)
		if err != nil {
			goto end
		}
		src = Source{TaskID: id, Column: legacyDirColumn[m[1]], Legacy: true}
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

// KindByColumn returns the Kind owning a `tasks` column.
func KindByColumn(column string) (kind Kind, ok bool) {
	for _, k := range TaskKinds {
		if k.Column == column {
			kind, ok = k, true
			break
		}
	}
	return kind, ok
}

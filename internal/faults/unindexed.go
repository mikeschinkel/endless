package faults

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mikeschinkel/go-doterr"
)

// Unindexed fault occurrences, and the watermark that dismisses them (E-1887).
//
// # Why this exists
//
// A fault raised BECAUSE the database failed cannot be recorded through the
// database. Record still writes its detail line (see appendDetail), marked
// unindexed and carrying no `errors` row id — so the report survives, but every
// surface over the `errors` table is blind to it, including the fault row whose
// whole job is to say something is wrong.
//
// These readers are that blind spot's only eyes. They need nothing but the
// filesystem, which is the point: they are read in exactly the state where the
// database is what failed.
//
// # The watermark, and why the table's cleared_at cannot serve
//
// `errors clear` marks ROWS cleared. An unindexed occurrence has no row, so
// that state is unreachable for precisely the entries this file is about — and
// a notice that cannot be dismissed is a notice a user learns to ignore.
//
// So the file side carries its own watermark: a byte offset into errors.jsonl,
// cleared up to. `errors clear` moves both, or a cleared database and an
// uncleared log disagree forever.
//
// An OFFSET rather than a timestamp, because the log is append-only and offsets
// are monotonic without depending on a clock that two processes may not share.
//
// An offset alone is not enough, though, and the gap is not theoretical: a log
// rotated, restored from a backup or truncated by hand leaves an offset
// pointing into bytes that are no longer the bytes it was measured against.
// Once the file grows past it again the offset is back "in range" and silently
// skips the wrong records — including ones nobody has seen. So the watermark
// also carries a DIGEST of the log's opening bytes, and a file that no longer
// hashes to it reads as 0. That fails toward showing an entry twice rather than
// toward swallowing one, which is the only acceptable direction for a surface
// whose job is to make sure nothing goes unreported.
//
// # Read cost, which is load-bearing here
//
// The fault row repaints every two seconds on a live monitor, and calls
// UnindexedCount on every repaint. So the read is deliberately proportional to
// what is NEW rather than to the size of the log: an identity digest over a
// bounded prefix, then a seek to the watermark and a read of the tail. On the
// ordinary healthy path — everything cleared, nothing appended — that is a stat
// and a few kilobytes, whatever the log has grown to.

// detailLogFile's clear watermark. It lives beside the log, under the same
// ConfigDir-routed directory, so a self-dev sandbox clears its own and not the
// real one.
const watermarkFile = "errors-cleared.json"

// identityPrefix is how many opening bytes of the log the watermark's digest
// covers.
//
// BOUNDED, not the whole cleared span, because this is read on a two-second
// repaint. What it has to detect is the log being replaced — rotated, restored,
// truncated — and a file's opening kilobytes identify it for that purpose as
// well as all of it would, at a cost that does not grow with the log. The size
// check beside it catches the shrinking case outright.
const identityPrefix = 4096

// watermark is the file's whole content: how far into errors.jsonl was cleared,
// and an identity digest of the log it was measured against.
//
// A struct rather than a bare number so a later field can be added without
// breaking a reader. A watermark written without a digest decodes with an empty
// one and reads as stale — one extra showing of entries already dismissed,
// once.
type watermark struct {
	Offset    int64  `json:"offset"`
	Digest    string `json:"digest"`
	DigestLen int64  `json:"digest_len"`
}

// Unindexed returns every unindexed occurrence newer than the clear watermark,
// oldest first.
//
// Lines that fail to parse are skipped rather than failing the read, for the
// same reason Details skips them: several processes append to this log, so a
// torn final line is possible and must not hide the intact records before it.
// A missing file is not an error — it means nothing has been recorded.
func Unindexed() (details []Detail, err error) {
	var file *os.File
	var info os.FileInfo
	var tail []byte
	var offset int64

	file, err = openDetailLog()
	if err != nil || file == nil {
		goto end
	}
	defer func() { _ = file.Close() }()

	info, err = file.Stat()
	if err != nil {
		err = doterr.NewErr(ErrFaults, ErrReadingLog, err)
		goto end
	}

	offset = watermarkFor(file, info.Size())

	tail = make([]byte, info.Size()-offset)
	_, err = io.ReadFull(file, tail)
	if err != nil {
		// A concurrent append can only make the file longer, so a short read
		// here means the file shrank under us. Nothing sensible is left to
		// parse; report nothing rather than half a line.
		err = nil
		goto end
	}
	details = parseUnindexed(tail)

end:
	return details, err
}

// UnindexedCount returns how many unindexed occurrences are outstanding. Zero
// on any read failure: this backs a notice, and a notice must not be able to
// fail the view it annotates.
func UnindexedCount() (n int) {
	var details []Detail
	var err error

	details, err = Unindexed()
	if err != nil {
		goto end
	}
	n = len(details)

end:
	return n
}

// ClearUnindexed moves the watermark to the end of the log, dismissing every
// unindexed occurrence outstanding now. It returns how many that was.
//
// Called by `errors clear` in its no-id form only. The id form names a TABLE
// row, and an unindexed occurrence has no id to name — clearing the whole file
// because a user dismissed one row would dismiss reports they never saw.
func ClearUnindexed() (cleared int, err error) {
	var details []Detail
	var dir string
	var info os.FileInfo
	var file *os.File
	var prefix []byte
	var encoded []byte

	dir = logDir()
	if dir == "" {
		err = doterr.NewErr(ErrFaults, ErrNotBound)
		goto end
	}

	details, err = Unindexed()
	if err != nil {
		goto end
	}
	cleared = len(details)

	file, err = openDetailLog()
	if err != nil {
		goto end
	}
	if file == nil {
		// Nothing has ever been recorded, so there is nothing to mark. Writing
		// a watermark over an absent log would be a marker for a file that does
		// not exist yet and whose first bytes are therefore unknowable.
		goto end
	}
	defer func() { _ = file.Close() }()

	info, err = file.Stat()
	if err != nil {
		err = doterr.NewErr(ErrFaults, ErrClearing, err)
		goto end
	}

	prefix, err = readPrefix(file, info.Size())
	if err != nil {
		err = doterr.NewErr(ErrFaults, ErrClearing, err)
		goto end
	}

	err = os.MkdirAll(dir, 0o755)
	if err != nil {
		err = doterr.NewErr(ErrFaults, ErrClearing, err)
		goto end
	}

	encoded, err = json.Marshal(watermark{
		Offset:    info.Size(),
		Digest:    digestOf(prefix),
		DigestLen: int64(len(prefix)),
	})
	if err != nil {
		err = doterr.NewErr(ErrFaults, ErrClearing, err)
		goto end
	}

	err = os.WriteFile(filepath.Join(dir, watermarkFile), append(encoded, '\n'), 0o644)
	if err != nil {
		err = doterr.NewErr(ErrFaults, ErrClearing, err)
	}

end:
	return cleared, err
}

// WatermarkPath returns the absolute path of the clear watermark, or "" when
// the package is unbound. Exposed for the same reason DetailLogPath is: a
// verify suite should read the real location rather than re-derive it.
func WatermarkPath() (path string) {
	var dir string

	dir = logDir()
	if dir == "" {
		goto end
	}
	path = filepath.Join(dir, watermarkFile)

end:
	return path
}

// openDetailLog opens the log for reading. A missing file yields a nil handle
// and no error, which is what "nothing has been recorded yet" means.
func openDetailLog() (file *os.File, err error) {
	var path string

	path = DetailLogPath()
	if path == "" {
		err = doterr.NewErr(ErrFaults, ErrNotBound)
		goto end
	}

	file, err = os.Open(path)
	if err != nil {
		file = nil
		if os.IsNotExist(err) {
			err = nil
			goto end
		}
		err = doterr.NewErr(ErrFaults, ErrReadingLog, err)
	}

end:
	return file, err
}

// watermarkFor returns how far into this log was cleared, and 0 whenever that
// cannot be established beyond doubt. It leaves the file positioned at the
// offset it returns, ready for the caller to read the tail.
//
// It reads 0 for an absent, unreadable or malformed watermark; for one whose
// offset is out of range for the log as it stands now; and for one whose digest
// does not match the log's opening bytes. Every one of those is a log this
// watermark was not measured against, and the forgiving direction is to show
// entries again: a duplicate notice costs a second dismissal, while a swallowed
// one costs the report entirely.
func watermarkFor(file *os.File, size int64) (offset int64) {
	var mark watermark
	var prefix []byte
	var err error

	mark, err = readWatermark()
	if err != nil {
		goto end
	}
	if mark.Offset <= 0 || mark.Offset > size || mark.DigestLen > size {
		goto end
	}

	prefix = make([]byte, mark.DigestLen)
	_, err = io.ReadFull(file, prefix)
	if err != nil {
		goto end
	}
	if mark.Digest == "" || mark.Digest != digestOf(prefix) {
		// The log this watermark was measured against is not the log in front
		// of us: it was rotated, restored or truncated. Honouring the offset
		// would skip records nobody has ever seen.
		goto end
	}
	offset = mark.Offset

end:
	// Whatever was decided, the caller reads from `offset` — so seek there
	// rather than leaving the handle wherever the prefix read left it.
	_, err = file.Seek(offset, io.SeekStart)
	if err != nil {
		offset = size
	}
	return offset
}

// readWatermark decodes the marker file. An absent or unreadable one is not an
// error worth a caller's attention — it means nothing has been cleared.
func readWatermark() (mark watermark, err error) {
	var path string
	var data []byte

	path = WatermarkPath()
	if path == "" {
		err = doterr.NewErr(ErrFaults, ErrNotBound)
		goto end
	}

	data, err = os.ReadFile(path)
	if err != nil {
		goto end
	}
	err = json.Unmarshal(data, &mark)

end:
	return mark, err
}

// readPrefix reads the log's opening bytes — the span a watermark's digest
// covers — and leaves the file positioned after them.
func readPrefix(file *os.File, size int64) (prefix []byte, err error) {
	var n int64

	n = size
	if n > identityPrefix {
		n = identityPrefix
	}

	prefix = make([]byte, n)
	_, err = io.ReadFull(file, prefix)
	if err != nil {
		prefix = nil
	}

	return prefix, err
}

// digestOf identifies a span of the log. Truncated to 16 hex characters, the
// same measure a fault fingerprint uses and for the same reason: a collision
// here costs one missed re-showing of an already-dismissed entry, which is far
// below the cost of the bytes this keeps readable.
func digestOf(data []byte) (digest string) {
	sum := sha256.Sum256(data)
	digest = hex.EncodeToString(sum[:])[:16]
	return digest
}

// parseUnindexed decodes the unindexed occurrences out of a span of the log,
// in the order they were appended.
func parseUnindexed(data []byte) (details []Detail) {
	var line string
	var detail Detail

	for _, line = range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		detail = Detail{}
		if json.Unmarshal([]byte(line), &detail) != nil {
			continue
		}
		if !detail.Unindexed {
			continue
		}
		details = append(details, detail)
	}

	return details
}

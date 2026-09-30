package upid

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// startTime reads pid's start time from /proc/<pid>/stat: field 22, in clock
// ticks since boot. The comm field (2) is parenthesized and may itself hold
// spaces or parens, so fields are counted from the LAST ')'.
func startTime(pid int) (start int64, err error) {
	var raw []byte
	var fields []string
	raw, err = os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		goto end
	}
	fields = strings.Fields(string(raw[strings.LastIndexByte(string(raw), ')')+1:]))
	// After comm, field 3 (state) is fields[0], so field 22 is fields[19].
	if len(fields) < 20 {
		err = fmt.Errorf("/proc/%d/stat: too few fields", pid)
		goto end
	}
	start, err = strconv.ParseInt(fields[19], 10, 64)
end:
	return start, err
}

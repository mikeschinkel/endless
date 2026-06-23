package verify

import (
	"github.com/mikeschinkel/go-doterr"
)

// Normalize reads a native result stream named by format and produces a
// CTRF-subset Report. It is the seam the runner (E-1603) calls once per check
// (interface contract section B, step 4); the runner then merges the per-check
// reports with MergeReports. Endless reads the native producer itself here — no
// external CTRF reporter is involved.
//
// An unknown format is an error wrapping ErrUnknownFormat so the caller can
// distinguish "unsupported stream" from a malformed stream of a known format.
func Normalize(format Format, raw []byte) (rpt *Report, err error) {
	switch format {
	case FormatGotestJSON:
		rpt, err = parseGotestJSON(raw)
	case FormatPytestJSON:
		rpt, err = parsePytestJSON(raw)
	case FormatTAP:
		rpt, err = parseTAP(raw)
	default:
		err = doterr.NewErr(ErrNormalizingResults, ErrUnknownFormat,
			"format", string(format), "supported", formatList())
	}
	return rpt, err
}

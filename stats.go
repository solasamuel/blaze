package main

import "time"

// percentile returns the value at the nearest-rank position for p in [0,1] over
// a slice that is already sorted ascending. Empty input returns 0.
//
// The index uses (len-1)*p so that p=1.0 maps to the last element (the max) and
// never indexes out of range.
func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)-1) * p)
	return sorted[idx]
}

// classifyError maps an error to a coarse kind. Epic 4 (F-4.1) expands this with
// errors.Is/errors.As rules; for now anything non-nil is "other".
func classifyError(err error) string {
	if err == nil {
		return ""
	}
	return "other"
}

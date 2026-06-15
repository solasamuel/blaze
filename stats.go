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

// Percentiles holds the latency distribution points reported at end of run.
type Percentiles struct {
	P50, P95, P99, Max time.Duration
}

// computePercentiles derives the reported latency points from a sorted slice in
// a single helper so the summary and any future output format share one source
// of truth. The slice is assumed sorted ascending (collect sorts it).
func computePercentiles(sorted []time.Duration) Percentiles {
	return Percentiles{
		P50: percentile(sorted, 0.50),
		P95: percentile(sorted, 0.95),
		P99: percentile(sorted, 0.99),
		Max: percentile(sorted, 1.0),
	}
}

// classifyError maps an error to a coarse kind. Epic 4 (F-4.1) expands this with
// errors.Is/errors.As rules; for now anything non-nil is "other".
func classifyError(err error) string {
	if err == nil {
		return ""
	}
	return "other"
}

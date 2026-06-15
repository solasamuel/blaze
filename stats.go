package main

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

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

// throughput is successful-or-total requests per second over wall-clock elapsed.
// Returns 0 when no time has elapsed to avoid a divide-by-zero.
func throughput(count int, elapsed time.Duration) float64 {
	if elapsed <= 0 {
		return 0
	}
	return float64(count) / elapsed.Seconds()
}

// errorLabels gives the breakdown a human-readable name per kind.
var errorLabels = map[string]string{
	"timeout": "timeout",
	"refused": "connection refused",
	"other":   "other",
}

// formatErrorBreakdown renders "[2× timeout, 1× connection refused]" with a
// deterministic order: highest count first, then alphabetical by kind. Returns
// an empty string when there are no errors.
func formatErrorBreakdown(kinds map[string]int) string {
	type kc struct {
		kind  string
		count int
	}
	items := make([]kc, 0, len(kinds))
	for k, c := range kinds {
		if c > 0 {
			items = append(items, kc{k, c})
		}
	}
	if len(items) == 0 {
		return ""
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].count != items[j].count {
			return items[i].count > items[j].count
		}
		return items[i].kind < items[j].kind
	})

	parts := make([]string, len(items))
	for i, it := range items {
		label := errorLabels[it.kind]
		if label == "" {
			label = it.kind
		}
		parts[i] = fmt.Sprintf("%d× %s", it.count, label)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// formatThousands inserts comma separators into a non-negative integer, e.g.
// 1240 -> "1,240".
func formatThousands(n int) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	lead := len(s) % 3
	if lead > 0 {
		b.WriteString(s[:lead])
	}
	for i := lead; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

// formatSummary renders the end-of-run report matching the product overview:
//
//	Latency:   p50  42ms   p95  118ms   p99  203ms   max  412ms
//	Throughput: 1,240 req/s
//	Errors:     3 (0.3%)   [2× timeout, 1× connection refused]
func formatSummary(s Summary, elapsed time.Duration) string {
	p := computePercentiles(s.Latencies)
	var b strings.Builder

	fmt.Fprintf(&b, "\nLatency:   p50  %v   p95  %v   p99  %v   max  %v\n",
		p.P50, p.P95, p.P99, p.Max)

	tput := throughput(len(s.Latencies), elapsed)
	fmt.Fprintf(&b, "Throughput: %s req/s\n", formatThousands(int(tput)))

	errPct := 0.0
	if s.Total > 0 {
		errPct = 100 * float64(s.Errors) / float64(s.Total)
	}
	fmt.Fprintf(&b, "Errors:     %d (%.1f%%)", s.Errors, errPct)
	if bd := formatErrorBreakdown(s.ErrorKinds); bd != "" {
		fmt.Fprintf(&b, "   %s", bd)
	}
	b.WriteByte('\n')

	return b.String()
}

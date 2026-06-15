package main

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// TC-1.4.a — errors counted separately from latencies.
func TestCollect_ErrorsVsLatencies(t *testing.T) {
	results := make(chan Result, 5)
	results <- Result{Err: errors.New("boom")}
	results <- Result{Err: errors.New("boom")}
	results <- Result{Latency: 10 * time.Millisecond}
	results <- Result{Latency: 20 * time.Millisecond}
	results <- Result{Latency: 30 * time.Millisecond}
	close(results)

	s := collect(results)
	if s.Total != 5 {
		t.Errorf("Total = %d, want 5", s.Total)
	}
	if s.Errors != 2 {
		t.Errorf("Errors = %d, want 2", s.Errors)
	}
	if len(s.Latencies) != 3 {
		t.Errorf("Latencies = %d, want 3", len(s.Latencies))
	}
}

// TC-1.4.b — collect terminates once the closed channel is drained.
func TestCollect_TerminatesOnClose(t *testing.T) {
	results := make(chan Result, 1)
	results <- Result{Latency: time.Millisecond}
	close(results)

	done := make(chan Summary, 1)
	go func() { done <- collect(results) }()

	select {
	case s := <-done:
		if s.Total != 1 {
			t.Errorf("Total = %d, want 1", s.Total)
		}
	case <-time.After(time.Second):
		t.Fatal("collect did not terminate")
	}
}

// TC-1.4.a/b also exercise sorting; verify latencies come back ascending.
func TestCollect_SortsLatencies(t *testing.T) {
	results := make(chan Result, 3)
	results <- Result{Latency: 30 * time.Millisecond}
	results <- Result{Latency: 10 * time.Millisecond}
	results <- Result{Latency: 20 * time.Millisecond}
	close(results)

	s := collect(results)
	for i := 1; i < len(s.Latencies); i++ {
		if s.Latencies[i-1] > s.Latencies[i] {
			t.Fatalf("latencies not sorted: %v", s.Latencies)
		}
	}
}

// TC-2.1.a/b/c/d — percentile, table-driven (build plan ties this to commit #1).
func TestPercentile(t *testing.T) {
	// 1ms..100ms ascending.
	hundred := make([]time.Duration, 100)
	for i := range hundred {
		hundred[i] = time.Duration(i+1) * time.Millisecond
	}

	tests := []struct {
		name  string
		input []time.Duration
		p     float64
		want  time.Duration
	}{
		{"empty returns zero", nil, 0.95, 0},                                  // TC-2.1.a (Corner)
		{"p50 of 100", hundred, 0.50, 50 * time.Millisecond},                  // TC-2.1.b (Boundary)
		{"p95 of 100", hundred, 0.95, 95 * time.Millisecond},                  // TC-2.1.b
		{"p99 of 100", hundred, 0.99, 99 * time.Millisecond},                  // TC-2.1.b
		{"p100 is max via len-1", hundred, 1.0, 100 * time.Millisecond},       // TC-2.1.c (Off-by-one)
		{"single element any p", []time.Duration{7 * time.Millisecond}, 0.42, 7 * time.Millisecond}, // TC-2.1.d (Boundary)
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := percentile(tc.input, tc.p); got != tc.want {
				t.Errorf("percentile(%v) = %v, want %v", tc.p, got, tc.want)
			}
		})
	}
}

// TC-2.1.b (aggregate) — computePercentiles derives p50/p95/p99/max in one pass.
func TestComputePercentiles(t *testing.T) {
	hundred := make([]time.Duration, 100)
	for i := range hundred {
		hundred[i] = time.Duration(i+1) * time.Millisecond
	}

	got := computePercentiles(hundred)
	want := Percentiles{
		P50: 50 * time.Millisecond,
		P95: 95 * time.Millisecond,
		P99: 99 * time.Millisecond,
		Max: 100 * time.Millisecond,
	}
	if got != want {
		t.Errorf("computePercentiles = %+v, want %+v", got, want)
	}
}

// Corner: empty input yields an all-zero distribution, no panic.
func TestComputePercentiles_Empty(t *testing.T) {
	if got := computePercentiles(nil); got != (Percentiles{}) {
		t.Errorf("computePercentiles(nil) = %+v, want zero", got)
	}
}

// TC-2.2.a — throughput is requests over elapsed.
func TestThroughput(t *testing.T) {
	got := throughput(1000, 800*time.Millisecond)
	if got != 1250 {
		t.Errorf("throughput = %v, want 1250", got)
	}
	// Corner: zero elapsed must not divide by zero.
	if z := throughput(10, 0); z != 0 {
		t.Errorf("throughput(_,0) = %v, want 0", z)
	}
}

// TC-2.2.b — error breakdown rendered per kind, highest count first.
func TestFormatErrorBreakdown(t *testing.T) {
	got := formatErrorBreakdown(map[string]int{"timeout": 2, "refused": 1})
	want := "[2× timeout, 1× connection refused]"
	if got != want {
		t.Errorf("breakdown = %q, want %q", got, want)
	}
	// Corner: no errors -> empty string.
	if e := formatErrorBreakdown(map[string]int{}); e != "" {
		t.Errorf("empty breakdown = %q, want \"\"", e)
	}
}

func TestFormatThousands(t *testing.T) {
	tests := []struct {
		in   int
		want string
	}{
		{0, "0"},
		{42, "42"},
		{999, "999"},   // boundary: no separator
		{1000, "1,000"}, // off-by-one: first separator
		{1240, "1,240"},
		{1234567, "1,234,567"},
	}
	for _, tc := range tests {
		if got := formatThousands(tc.in); got != tc.want {
			t.Errorf("formatThousands(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Integration of F-2.2: the full summary matches the spec layout.
func TestFormatSummary_Layout(t *testing.T) {
	lat := []time.Duration{}
	for i := 0; i < 100; i++ {
		lat = append(lat, time.Duration(i+1)*time.Millisecond)
	}
	s := Summary{
		Total:      103,
		Errors:     3,
		Latencies:  lat,
		ErrorKinds: map[string]int{"timeout": 2, "refused": 1},
	}
	out := formatSummary(s, 100*time.Millisecond) // 100 reqs / 0.1s = 1000 req/s

	for _, want := range []string{
		"p50  50ms", "p95  95ms", "p99  99ms", "max  100ms",
		"Throughput: 1,000 req/s",
		"Errors:     3 (2.9%)",
		"[2× timeout, 1× connection refused]",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("summary missing %q\n--- got ---\n%s", want, out)
		}
	}
}

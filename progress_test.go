package main

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TC-3.1.a — the counter incremented by all workers reaches total under -race.
func TestWorker_CounterReachesTotal(t *testing.T) {
	srv := okServer()
	defer srv.Close()

	const total = 200
	jobs := make(chan Job, total)
	results := make(chan Result, total)
	var completed int64
	var wg sync.WaitGroup

	client := &http.Client{Timeout: 5 * time.Second}
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go worker(context.Background(), jobs, results, client, specFor(srv.URL), &completed, &wg)
	}
	for i := 0; i < total; i++ {
		jobs <- Job{ID: i}
	}
	close(jobs)

	// drain results concurrently so the buffered sends never block.
	drained := make(chan int, 1)
	go func() {
		n := 0
		for range results {
			n++
		}
		drained <- n
	}()

	wg.Wait()
	close(results)
	<-drained

	if got := atomic.LoadInt64(&completed); got != total {
		t.Fatalf("completed = %d, want %d", got, total)
	}
}

// TC-3.1.b — many goroutines each adding 1 a fixed number of times lose no
// updates (lock-free correctness).
func TestAtomicCounter_NoLostUpdates(t *testing.T) {
	const goroutines, iterations = 50, 1000
	var counter int64
	var wg sync.WaitGroup

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				atomic.AddInt64(&counter, 1)
			}
		}()
	}
	wg.Wait()

	if want := int64(goroutines * iterations); counter != want {
		t.Fatalf("counter = %d, want %d", counter, want)
	}
}

// TC-3.2.a — progressBar fill is proportional; the total-1 vs total step is the
// off-by-one boundary.
func TestProgressBar_Proportional(t *testing.T) {
	const w = 24
	tests := []struct {
		done, total, wantFilled int
	}{
		{0, 100, 0},    // empty
		{50, 100, 12},  // half of 24
		{99, 100, 23},  // total-1: not yet full
		{100, 100, 24}, // total: full
	}
	for _, tc := range tests {
		bar := progressBar(tc.done, tc.total, w)
		gotFilled := strings.Count(bar, string(barFilled))
		if gotFilled != tc.wantFilled {
			t.Errorf("progressBar(%d,%d) filled = %d, want %d", tc.done, tc.total, gotFilled, tc.wantFilled)
		}
		if rune := []rune(bar); len(rune) != w {
			t.Errorf("progressBar(%d,%d) width = %d, want %d", tc.done, tc.total, len(rune), w)
		}
	}
}

// TC-3.2.b — bar clamps at full and never exceeds width, even when done > total
// or total is zero.
func TestProgressBar_Clamps(t *testing.T) {
	const w = 24
	for _, tc := range []struct{ done, total int }{
		{100, 100}, // exactly full
		{150, 100}, // overshoot
		{5, 0},     // zero total, work reported
	} {
		bar := progressBar(tc.done, tc.total, w)
		filled := strings.Count(bar, string(barFilled))
		if filled != w {
			t.Errorf("progressBar(%d,%d) filled = %d, want %d (full)", tc.done, tc.total, filled, w)
		}
		if got := len([]rune(bar)); got != w {
			t.Errorf("progressBar(%d,%d) width = %d, want %d", tc.done, tc.total, got, w)
		}
	}
}

// startProgress writes in-place frames and stops cleanly, painting a final frame.
func TestStartProgress_StopsCleanly(t *testing.T) {
	var buf bytes.Buffer
	var completed int64 = 7
	stop := startProgress(&buf, &completed, 10)
	time.Sleep(250 * time.Millisecond) // let a couple of ticks fire
	atomic.StoreInt64(&completed, 10)
	stop()

	out := buf.String()
	if !strings.Contains(out, "\r") {
		t.Error("expected carriage-return in-place updates")
	}
	if !strings.Contains(out, "10/10") {
		t.Errorf("expected final 10/10 frame, got:\n%q", out)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Error("expected trailing newline after final frame")
	}
}

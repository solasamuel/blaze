package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"testing"
	"time"
)

func runtimeNumGoroutine() int { return runtime.NumGoroutine() }

func okServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
}

func specFor(url string) RequestSpec {
	return RequestSpec{URL: url, Method: "GET", Headers: map[string]string{}, Body: func() io.Reader { return nil }}
}

func startWorker(ctx context.Context, jobs chan Job, results chan Result, spec RequestSpec) *sync.WaitGroup {
	var wg sync.WaitGroup
	wg.Add(1)
	go worker(ctx, jobs, results, &http.Client{Timeout: 5 * time.Second}, spec, &wg)
	return &wg
}

// TC-1.2.a — worker emits one result per job with StatusCode 200 and nil Err.
func TestWorker_OneResultPerJob(t *testing.T) {
	srv := okServer()
	defer srv.Close()

	jobs := make(chan Job, 1)
	results := make(chan Result, 1)
	wg := startWorker(context.Background(), jobs, results, specFor(srv.URL))

	jobs <- Job{ID: 0}
	close(jobs)
	wg.Wait()
	close(results)

	got := 0
	for r := range results {
		got++
		if r.Err != nil {
			t.Errorf("unexpected err: %v", r.Err)
		}
		if r.StatusCode != http.StatusOK {
			t.Errorf("status = %d, want 200", r.StatusCode)
		}
	}
	if got != 1 {
		t.Fatalf("results = %d, want 1", got)
	}
}

// TC-1.2.b — worker exits immediately when jobs is closed empty, sends nothing.
func TestWorker_ExitsOnClosedEmpty(t *testing.T) {
	jobs := make(chan Job)
	results := make(chan Result, 1)
	wg := startWorker(context.Background(), jobs, results, specFor("http://unused"))

	close(jobs)
	wg.Wait()
	close(results)

	if got := len(results); got != 0 {
		t.Fatalf("results = %d, want 0", got)
	}
}

// TC-1.2.c — worker reports a transport error (unreachable server) as Result.Err.
func TestWorker_TransportError(t *testing.T) {
	srv := okServer()
	url := srv.URL
	srv.Close() // now unreachable

	jobs := make(chan Job, 1)
	results := make(chan Result, 1)
	wg := startWorker(context.Background(), jobs, results, specFor(url))

	jobs <- Job{ID: 0}
	close(jobs)
	wg.Wait()
	close(results)

	r := <-results
	if r.Err == nil {
		t.Fatal("expected transport error, got nil")
	}
	if r.StatusCode != 0 {
		t.Errorf("status = %d, want 0", r.StatusCode)
	}
}

// TC-1.3.a — every job produces exactly one collected result.
func TestRun_CountMatches(t *testing.T) {
	srv := okServer()
	defer srv.Close()

	s := run(specFor(srv.URL), 50, 1000, 5*time.Second)
	if s.Total != 1000 {
		t.Fatalf("Total = %d, want 1000", s.Total)
	}
	if len(s.Latencies) != 1000 {
		t.Errorf("Latencies = %d, want 1000", len(s.Latencies))
	}
}

// TC-1.3.b — n=1 edge of the fan-out loop: single worker, single request.
func TestRun_SingleWorkerSingleRequest(t *testing.T) {
	srv := okServer()
	defer srv.Close()

	s := run(specFor(srv.URL), 1, 1, 5*time.Second)
	if s.Total != 1 {
		t.Fatalf("Total = %d, want 1", s.Total)
	}
}

// TC-1.3.c — no goroutine leak after run returns (run under `go test -race`).
func TestRun_NoGoroutineLeak(t *testing.T) {
	srv := okServer()
	defer srv.Close()

	before := runtimeNumGoroutine()
	_ = run(specFor(srv.URL), 10, 200, 5*time.Second)

	// allow scheduled goroutines to wind down
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtimeNumGoroutine() <= before {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("goroutines did not return to baseline: before=%d after=%d", before, runtimeNumGoroutine())
}

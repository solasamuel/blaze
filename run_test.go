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
	var completed int64
	wg.Add(1)
	go worker(workerDeps{
		ctx:       ctx,
		jobs:      jobs,
		results:   results,
		client:    &http.Client{Timeout: 5 * time.Second},
		req:       spec,
		timeout:   5 * time.Second,
		completed: &completed,
		wg:        &wg,
	})
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

	s := run(runOpts{Req: specFor(srv.URL), Concurrency: 50, Requests: 1000, Timeout: 5 * time.Second})
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

	s := run(runOpts{Req: specFor(srv.URL), Concurrency: 1, Requests: 1, Timeout: 5 * time.Second})
	if s.Total != 1 {
		t.Fatalf("Total = %d, want 1", s.Total)
	}
}

// TC-1.3.c — no goroutine leak after run returns (run under `go test -race`).
func TestRun_NoGoroutineLeak(t *testing.T) {
	srv := okServer()
	defer srv.Close()

	before := runtimeNumGoroutine()
	_ = run(runOpts{Req: specFor(srv.URL), Concurrency: 10, Requests: 200, Timeout: 5 * time.Second})

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

// TC-4.2.a — a slow endpoint exceeding the timeout produces a timeout-classified
// error.
func TestRun_SlowEndpointTimesOut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(2 * time.Second):
			w.WriteHeader(http.StatusOK)
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()

	s := run(runOpts{Req: specFor(srv.URL), Concurrency: 2, Requests: 4, Timeout: 100 * time.Millisecond})
	if s.Errors != 4 {
		t.Fatalf("Errors = %d, want 4 (all timed out)", s.Errors)
	}
	if got := s.ErrorKinds[errTimeout]; got != 4 {
		t.Errorf("timeout errors = %d, want 4 (kinds=%v)", got, s.ErrorKinds)
	}
}

// TC-4.2.b — each request is built with a context that is cancelled when run
// returns, so the server observes cancellation rather than a leaked request.
func TestRun_RequestContextCancelledOnReturn(t *testing.T) {
	cancelled := make(chan struct{}, 16)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Slow handler: returns only if the request context is cancelled.
		select {
		case <-time.After(3 * time.Second):
			w.WriteHeader(http.StatusOK)
		case <-r.Context().Done():
			select {
			case cancelled <- struct{}{}:
			default:
			}
		}
	}))
	defer srv.Close()

	// Short per-request timeout: requests time out, cancelling their contexts.
	_ = run(runOpts{Req: specFor(srv.URL), Concurrency: 2, Requests: 2, Timeout: 100 * time.Millisecond})

	select {
	case <-cancelled:
		// server saw at least one cancelled request context — good.
	case <-time.After(2 * time.Second):
		t.Fatal("server never observed a cancelled request context")
	}
}

// TC-4.3.a — duration mode stops feeding when the window elapses, returns
// promptly, and completes a positive number of requests.
func TestRun_DurationModeStopsOnWindow(t *testing.T) {
	srv := okServer()
	defer srv.Close()

	start := time.Now()
	s := run(runOpts{
		Req:         specFor(srv.URL),
		Concurrency: 10,
		Duration:    300 * time.Millisecond,
		Timeout:     5 * time.Second,
	})
	elapsed := time.Since(start)

	if s.Total == 0 {
		t.Fatal("duration mode completed 0 requests, want > 0")
	}
	// Should finish shortly after the window, not hang.
	if elapsed > 3*time.Second {
		t.Errorf("duration run took %v, expected ~window + drain", elapsed)
	}
}

// TC-4.3.b — duration mode ignores --requests: the run is bounded by time, not
// by the (small) request count.
func TestRun_DurationModeIgnoresRequests(t *testing.T) {
	srv := okServer()
	defer srv.Close()

	s := run(runOpts{
		Req:         specFor(srv.URL),
		Concurrency: 10,
		Requests:    3, // would be a tiny run in count mode
		Duration:    250 * time.Millisecond,
		Timeout:     5 * time.Second,
	})

	// In 250ms against a local server we expect many more than 3 requests.
	if s.Total <= 3 {
		t.Errorf("Total = %d, expected duration mode to far exceed the 3-request count", s.Total)
	}
}

// TC-4.4.a — the transport reuses connections: MaxIdleConnsPerHost == concurrency.
func TestNewTransport_MaxIdleConnsPerHost(t *testing.T) {
	for _, c := range []int{1, 10, 50} {
		if got := newTransport(c).MaxIdleConnsPerHost; got != c {
			t.Errorf("newTransport(%d).MaxIdleConnsPerHost = %d, want %d", c, got, c)
		}
	}
}

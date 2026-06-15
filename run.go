package main

import (
	"context"
	"net/http"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// workerDeps bundles everything a worker needs, keeping the goroutine signature
// small and the shared state explicit. Channel directions are constrained at the
// type level: jobs is receive-only and results is send-only, so the compiler
// rejects sending on the wrong one.
type workerDeps struct {
	ctx       context.Context
	jobs      <-chan Job    // receive-only
	results   chan<- Result // send-only
	client    *http.Client
	req       RequestSpec
	timeout   time.Duration
	completed *int64 // lock-free counter, incremented once per finished request
	wg        *sync.WaitGroup
}

// worker consumes jobs and issues one HTTP request per job until jobs is closed.
func worker(d workerDeps) {
	defer d.wg.Done()

	for range d.jobs { // ends when jobs is closed and drained
		d.doRequest()
	}
}

// doRequest performs a single request and emits its Result. It is a method so
// the per-request context cancel can be deferred and released on every path.
func (d workerDeps) doRequest() {
	// Per-request deadline derived from the run context: cancelling the run (or
	// hitting --timeout) aborts the in-flight request. errors.Is then sees
	// context.DeadlineExceeded for a precise "timeout" classification.
	reqCtx, cancel := context.WithTimeout(d.ctx, d.timeout)
	defer cancel()
	defer atomic.AddInt64(d.completed, 1)

	start := time.Now()
	httpReq, err := http.NewRequestWithContext(reqCtx, d.req.Method, d.req.URL, d.req.Body())
	if err != nil {
		d.results <- Result{Latency: time.Since(start), Err: err}
		return
	}
	for k, v := range d.req.Headers {
		httpReq.Header.Set(k, v)
	}

	resp, err := d.client.Do(httpReq)
	latency := time.Since(start)
	if err != nil {
		d.results <- Result{Latency: latency, Err: err}
		return
	}
	resp.Body.Close() // CRITICAL: close to return the connection to the pool
	d.results <- Result{Latency: latency, StatusCode: resp.StatusCode}
}

// runOpts configures a load-test run. Exactly one of Requests (count mode) or
// Duration (duration mode) drives how long the run lasts; Duration takes
// precedence when non-zero.
type runOpts struct {
	Req         RequestSpec
	Concurrency int
	Requests    int
	Duration    time.Duration
	Timeout     time.Duration
}

// newTransport builds the shared HTTP transport. MaxIdleConnsPerHost is set to
// the worker concurrency so every worker can keep a warm keep-alive connection
// to the target host instead of dialling a fresh TCP/TLS connection per request
// (workers must close response bodies for connections to return to the pool).
func newTransport(concurrency int) *http.Transport {
	return &http.Transport{
		MaxIdleConnsPerHost: concurrency,
	}
}

// run executes a load test using the fan-out/fan-in pattern and returns the
// aggregated Summary. It runs for a fixed request count, or for a fixed
// duration when o.Duration > 0.
func run(o runOpts) Summary {
	jobs := make(chan Job, o.Concurrency)
	results := make(chan Result, o.Concurrency)
	var wg sync.WaitGroup

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	transport := newTransport(o.Concurrency)
	// Release pooled idle connections (and their background goroutines) when the
	// run ends, so run() owns no resources after it returns.
	defer transport.CloseIdleConnections()
	client := &http.Client{Timeout: o.Timeout, Transport: transport}

	var completed int64 // lock-free, shared by all workers and the progress loop

	// fan-out: start N workers
	for i := 0; i < o.Concurrency; i++ {
		wg.Add(1)
		go worker(workerDeps{
			ctx:       ctx,
			jobs:      jobs,
			results:   results,
			client:    client,
			req:       o.Req,
			timeout:   o.Timeout,
			completed: &completed,
			wg:        &wg,
		})
	}

	// feeder owns the send side of jobs and closes it when feeding is done.
	go feedJobs(ctx, jobs, o)

	// closer owns the send side of results: once every worker is done, close.
	go func() {
		wg.Wait()
		close(results)
	}()

	// live progress: redraw from the atomic counter until the run completes. In
	// duration mode the total is unknown (0), so progress reports a live count.
	progressTotal := o.Requests
	if o.Duration > 0 {
		progressTotal = 0
	}
	stopProgress := startProgress(os.Stdout, &completed, progressTotal)

	// fan-in: collect on the main goroutine until results is closed.
	s := collect(results)
	stopProgress() // halt and paint the final frame before the summary prints
	return s
}

// feedJobs sends jobs into the channel and closes it. In count mode it sends
// exactly o.Requests jobs; in duration mode it sends until the window elapses
// (a context deadline) or the run is cancelled, then closes.
func feedJobs(ctx context.Context, jobs chan<- Job, o runOpts) {
	defer close(jobs) // feeder owns the send side: it always closes.

	if o.Duration > 0 {
		deadline, cancel := context.WithTimeout(ctx, o.Duration)
		defer cancel()
		for i := 0; ; i++ {
			select {
			case <-deadline.Done():
				return
			case jobs <- Job{ID: i}:
			}
		}
	}

	for i := 0; i < o.Requests; i++ {
		select {
		case <-ctx.Done():
			return
		case jobs <- Job{ID: i}:
		}
	}
}

// collect drains the results channel into a Summary. It is the single reader,
// so the Summary needs no locking.
func collect(results <-chan Result) Summary {
	s := Summary{ErrorKinds: make(map[string]int)}
	for r := range results {
		s.Total++
		if r.Err != nil {
			s.Errors++
			s.ErrorKinds[classifyError(r.Err)]++
			continue
		}
		s.Latencies = append(s.Latencies, r.Latency)
	}
	sort.Slice(s.Latencies, func(i, j int) bool {
		return s.Latencies[i] < s.Latencies[j]
	})
	return s
}
